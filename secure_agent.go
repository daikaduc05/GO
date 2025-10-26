package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// ============================================================================
// SECURE AGENT IMPLEMENTATION
// ============================================================================

// SecureAgent extends the base Agent with security features
type SecureAgent struct {
	*Agent
	securityManager *SecurityManager
	clientSessions  map[string]*ClientSession
	sessionMu       sync.RWMutex
}

// ClientSession represents an authenticated client session
type ClientSession struct {
	ClientID    string
	AuthToken   *AuthToken
	LastSeen    time.Time
	RateLimiter *RateLimiter
	Permissions []string
}

// NewSecureAgent creates a new secure agent
func NewSecureAgent(config *AgentConfig, securityConfig *SecurityConfig) (*SecureAgent, error) {
	// Create base agent
	agent, err := NewAgent(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create base agent: %w", err)
	}

	// Create security manager
	securityManager, err := NewSecurityManager(securityConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create security manager: %w", err)
	}

	// Create secure agent
	secureAgent := &SecureAgent{
		Agent:           agent,
		securityManager: securityManager,
		clientSessions:  make(map[string]*ClientSession),
	}

	return secureAgent, nil
}

// Start starts the secure agent with security features
func (sa *SecureAgent) Start() error {
	// Start base agent
	if err := sa.Agent.Start(); err != nil {
		return fmt.Errorf("failed to start base agent: %w", err)
	}

	// Start security monitoring
	go sa.securityMonitor()

	// Start session cleanup
	go sa.sessionCleanup()

	log.Println("Secure agent started with security features enabled")
	return nil
}

// Stop stops the secure agent
func (sa *SecureAgent) Stop() error {
	// Stop base agent
	if err := sa.Agent.Stop(); err != nil {
		return fmt.Errorf("failed to stop base agent: %w", err)
	}

	// Clear all sessions
	sa.sessionMu.Lock()
	sa.clientSessions = make(map[string]*ClientSession)
	sa.sessionMu.Unlock()

	log.Println("Secure agent stopped")
	return nil
}

// AuthenticateClient authenticates a client connection
func (sa *SecureAgent) AuthenticateClient(clientID, token string) error {
	// Validate token
	authToken, err := sa.securityManager.ValidateToken(token)
	if err != nil {
		// Record failed attempt
		sa.securityManager.RecordLoginAttempt(clientID, false)
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Create client session
	session := &ClientSession{
		ClientID:    clientID,
		AuthToken:   authToken,
		LastSeen:    time.Now(),
		RateLimiter: NewRateLimiter(1*time.Minute, 60),
		Permissions: authToken.Scope,
	}

	// Store session
	sa.sessionMu.Lock()
	sa.clientSessions[clientID] = session
	sa.sessionMu.Unlock()

	// Record successful login
	sa.securityManager.RecordLoginAttempt(clientID, true)

	log.Printf("Client %s authenticated successfully", clientID)
	return nil
}

// SendSecureMessage sends a message with security checks
func (sa *SecureAgent) SendSecureMessage(clientID, message, targetVIP string) error {
	// Check if client is authenticated
	session, err := sa.getClientSession(clientID)
	if err != nil {
		return fmt.Errorf("client not authenticated: %w", err)
	}

	// Check rate limits
	if err := sa.securityManager.CheckRateLimit(clientID); err != nil {
		return fmt.Errorf("rate limit exceeded: %w", err)
	}

	// Validate input
	if err := sa.securityManager.ValidateInput(message, "message"); err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}

	if err := sa.securityManager.ValidateInput(targetVIP, "vip"); err != nil {
		return fmt.Errorf("invalid target VIP: %w", err)
	}

	// Check permissions
	if !sa.hasPermission(session, "send_message") {
		return fmt.Errorf("insufficient permissions to send messages")
	}

	// Create UDP frame
	frame := UDPFrame{
		Version:     1,
		MessageType: 0, // DATA
		SrcVIP:      ipToBytes(sa.getLocalVIP()),
		DstVIP:      ipToBytes(targetVIP),
		PayloadLen:  uint16(len(message)),
		Payload:     []byte(message),
	}

	// Encrypt frame if encryption enabled
	encFrame, err := sa.securityManager.EncryptFrame(frame)
	if err != nil {
		return fmt.Errorf("failed to encrypt frame: %w", err)
	}

	// Send encrypted frame
	return sa.sendEncryptedFrame(encFrame, targetVIP)
}

// sendEncryptedFrame sends an encrypted frame to target VIP
func (sa *SecureAgent) sendEncryptedFrame(encFrame *EncryptedUDPFrame, targetVIP string) error {
	// Look up peer endpoint
	sa.mu.RLock()
	peerAddr, exists := sa.peerMappings[targetVIP]
	sa.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no peer mapping for VIP: %s", targetVIP)
	}

	// Serialize encrypted frame
	data, err := sa.serializeEncryptedFrame(encFrame)
	if err != nil {
		return fmt.Errorf("failed to serialize encrypted frame: %w", err)
	}

	// Send via UDP
	_, err = sa.udpConn.WriteToUDP(data, peerAddr)
	return err
}

// serializeEncryptedFrame serializes an encrypted UDP frame
func (sa *SecureAgent) serializeEncryptedFrame(encFrame *EncryptedUDPFrame) ([]byte, error) {
	// Calculate total size
	totalSize := 12 + 12 + 16 + len(encFrame.EncryptedPayload) // header + nonce + auth_tag + payload

	// Create buffer
	data := make([]byte, totalSize)
	offset := 0

	// Version (1 byte)
	data[offset] = encFrame.Version
	offset++

	// Message type (1 byte)
	data[offset] = encFrame.MessageType
	offset++

	// Source VIP (4 bytes)
	copy(data[offset:offset+4], encFrame.SrcVIP[:])
	offset += 4

	// Destination VIP (4 bytes)
	copy(data[offset:offset+4], encFrame.DstVIP[:])
	offset += 4

	// Payload length (2 bytes, little-endian)
	binary.LittleEndian.PutUint16(data[offset:offset+2], encFrame.PayloadLen)
	offset += 2

	// Nonce (12 bytes)
	copy(data[offset:offset+12], encFrame.Nonce[:])
	offset += 12

	// Auth tag (16 bytes)
	copy(data[offset:offset+16], encFrame.AuthTag[:])
	offset += 16

	// Encrypted payload
	copy(data[offset:], encFrame.EncryptedPayload)

	return data, nil
}

// parseEncryptedFrame parses bytes into an encrypted UDP frame
func (sa *SecureAgent) parseEncryptedFrame(data []byte) (*EncryptedUDPFrame, error) {
	if len(data) < 40 { // Minimum size for encrypted frame
		return nil, fmt.Errorf("encrypted frame too short: %d bytes", len(data))
	}

	offset := 0
	encFrame := &EncryptedUDPFrame{}

	// Version (1 byte)
	encFrame.Version = data[offset]
	offset++

	// Message type (1 byte)
	encFrame.MessageType = data[offset]
	offset++

	// Source VIP (4 bytes)
	copy(encFrame.SrcVIP[:], data[offset:offset+4])
	offset += 4

	// Destination VIP (4 bytes)
	copy(encFrame.DstVIP[:], data[offset:offset+4])
	offset += 4

	// Payload length (2 bytes, little-endian)
	encFrame.PayloadLen = binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Nonce (12 bytes)
	copy(encFrame.Nonce[:], data[offset:offset+12])
	offset += 12

	// Auth tag (16 bytes)
	copy(encFrame.AuthTag[:], data[offset:offset+16])
	offset += 16

	// Encrypted payload
	encFrame.EncryptedPayload = make([]byte, encFrame.PayloadLen)
	copy(encFrame.EncryptedPayload, data[offset:])

	return encFrame, nil
}

// getClientSession retrieves a client session
func (sa *SecureAgent) getClientSession(clientID string) (*ClientSession, error) {
	sa.sessionMu.RLock()
	session, exists := sa.clientSessions[clientID]
	sa.sessionMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("client session not found: %s", clientID)
	}

	// Check if session is expired
	if time.Now().After(session.AuthToken.ExpiresAt) {
		sa.sessionMu.Lock()
		delete(sa.clientSessions, clientID)
		sa.sessionMu.Unlock()
		return nil, fmt.Errorf("client session expired: %s", clientID)
	}

	// Update last seen
	session.LastSeen = time.Now()

	return session, nil
}

// hasPermission checks if client has required permission
func (sa *SecureAgent) hasPermission(session *ClientSession, permission string) bool {
	for _, perm := range session.Permissions {
		if perm == permission || perm == "admin" {
			return true
		}
	}
	return false
}

// securityMonitor monitors for security threats
func (sa *SecureAgent) securityMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sa.shutdownCtx.Done():
			return
		case <-ticker.C:
			sa.performSecurityCheck()
		}
	}
}

// performSecurityCheck performs security monitoring
func (sa *SecureAgent) performSecurityCheck() {
	sa.sessionMu.RLock()
	activeSessions := len(sa.clientSessions)
	sa.sessionMu.RUnlock()

	// Check for suspicious activity
	if activeSessions > 100 {
		log.Printf("WARNING: High number of active sessions: %d", activeSessions)
	}

	// Check for expired sessions
	sa.cleanupExpiredSessions()

	// Log security metrics
	log.Printf("Security check: %d active sessions", activeSessions)
}

// cleanupExpiredSessions removes expired client sessions
func (sa *SecureAgent) cleanupExpiredSessions() {
	sa.sessionMu.Lock()
	defer sa.sessionMu.Unlock()

	now := time.Now()
	for clientID, session := range sa.clientSessions {
		if now.After(session.AuthToken.ExpiresAt) {
			delete(sa.clientSessions, clientID)
			log.Printf("Removed expired session for client: %s", clientID)
		}
	}
}

// sessionCleanup periodically cleans up old sessions
func (sa *SecureAgent) sessionCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sa.shutdownCtx.Done():
			return
		case <-ticker.C:
			sa.cleanupExpiredSessions()
		}
	}
}

// GenerateSecureToken generates a secure authentication token
func (sa *SecureAgent) GenerateSecureToken(userID string, permissions []string) (string, error) {
	// Create token claims
	claims := map[string]interface{}{
		"sub":   userID,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(sa.securityManager.config.TokenExpiry).Unix(),
		"scope": permissions,
	}

	// Marshal to JSON
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}

	// Create JWT header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}

	// Encode header and claims
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Create signature (simplified - in production use proper JWT library)
	signature := sha256.Sum256([]byte(headerB64 + "." + claimsB64 + "." + sa.securityManager.config.EncryptionKey))
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature[:])

	// Create JWT token
	token := headerB64 + "." + claimsB64 + "." + signatureB64

	return token, nil
}

// GetSecurityStatus returns current security status
func (sa *SecureAgent) GetSecurityStatus() map[string]interface{} {
	sa.sessionMu.RLock()
	activeSessions := len(sa.clientSessions)
	sa.sessionMu.RUnlock()

	return map[string]interface{}{
		"encryption_enabled": sa.securityManager.config.EncryptionEnabled,
		"auth_required":      sa.securityManager.config.AuthRequired,
		"rate_limit_enabled": sa.securityManager.config.RateLimitEnabled,
		"active_sessions":    activeSessions,
		"max_message_size":   sa.securityManager.config.MaxMessageSize,
		"uptime":             time.Since(time.Now()),
	}
}
