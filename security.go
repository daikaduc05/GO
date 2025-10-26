package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// ============================================================================
// SECURITY IMPROVEMENTS
// ============================================================================

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	// Encryption settings
	EncryptionEnabled bool   `json:"encryption_enabled"`
	EncryptionKey     string `json:"encryption_key"`
	KeyDerivationSalt string `json:"key_derivation_salt"`

	// Authentication settings
	AuthRequired     bool          `json:"auth_required"`
	TokenExpiry      time.Duration `json:"token_expiry"`
	MaxLoginAttempts int           `json:"max_login_attempts"`
	LoginCooldown    time.Duration `json:"login_cooldown"`

	// Input validation
	MaxMessageSize    int    `json:"max_message_size"`
	AllowedVIPPattern string `json:"allowed_vip_pattern"`

	// Rate limiting
	RateLimitEnabled bool          `json:"rate_limit_enabled"`
	RateLimitWindow  time.Duration `json:"rate_limit_window"`
	RateLimitMax     int           `json:"rate_limit_max"`
}

// EncryptedUDPFrame represents an encrypted UDP frame
type EncryptedUDPFrame struct {
	Version          uint8    `json:"version"`
	MessageType      uint8    `json:"message_type"`
	SrcVIP           [4]byte  `json:"src_vip"`
	DstVIP           [4]byte  `json:"dst_vip"`
	PayloadLen       uint16   `json:"payload_len"`
	Nonce            [12]byte `json:"nonce"`
	AuthTag          [16]byte `json:"auth_tag"`
	EncryptedPayload []byte   `json:"encrypted_payload"`
}

// AuthToken represents an authentication token with expiration
type AuthToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Scope     []string  `json:"scope"`
	UserID    string    `json:"user_id"`
	IssuedAt  time.Time `json:"issued_at"`
}

// SecurityManager handles all security-related operations
type SecurityManager struct {
	config        *SecurityConfig
	encryptionKey []byte
	rateLimiter   *RateLimiter
	loginAttempts map[string]*LoginAttempt
	mu            sync.RWMutex
}

// LoginAttempt tracks login attempts for rate limiting
type LoginAttempt struct {
	Count        int
	LastAttempt  time.Time
	BlockedUntil time.Time
}

// RateLimiter implements rate limiting for security
type RateLimiter struct {
	requests map[string][]time.Time
	window   time.Duration
	maxReqs  int
	mu       sync.RWMutex
}

// NewSecurityManager creates a new security manager
func NewSecurityManager(config *SecurityConfig) (*SecurityManager, error) {
	sm := &SecurityManager{
		config:        config,
		rateLimiter:   NewRateLimiter(config.RateLimitWindow, config.RateLimitMax),
		loginAttempts: make(map[string]*LoginAttempt),
	}

	// Derive encryption key if provided
	if config.EncryptionEnabled && config.EncryptionKey != "" {
		key, err := sm.deriveKey(config.EncryptionKey, config.KeyDerivationSalt)
		if err != nil {
			return nil, fmt.Errorf("failed to derive encryption key: %w", err)
		}
		sm.encryptionKey = key
	}

	return sm, nil
}

// deriveKey derives a cryptographic key from password using Argon2
func (sm *SecurityManager) deriveKey(password, salt string) ([]byte, error) {
	// Use Argon2id for key derivation
	key := argon2.IDKey([]byte(password), []byte(salt), 3, 32*1024, 4, 32)
	return key, nil
}

// EncryptFrame encrypts a UDP frame using ChaCha20-Poly1305
func (sm *SecurityManager) EncryptFrame(frame UDPFrame) (*EncryptedUDPFrame, error) {
	if !sm.config.EncryptionEnabled {
		// Return unencrypted frame if encryption disabled
		return &EncryptedUDPFrame{
			Version:          frame.Version,
			MessageType:      frame.MessageType,
			SrcVIP:           frame.SrcVIP,
			DstVIP:           frame.DstVIP,
			PayloadLen:       frame.PayloadLen,
			EncryptedPayload: frame.Payload,
		}, nil
	}

	// Generate random nonce
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Create ChaCha20-Poly1305 cipher
	aead, err := chacha20poly1305.New(sm.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Encrypt payload
	encrypted := aead.Seal(nil, nonce[:], frame.Payload, nil)

	// Create encrypted frame
	encFrame := &EncryptedUDPFrame{
		Version:          frame.Version,
		MessageType:      frame.MessageType,
		SrcVIP:           frame.SrcVIP,
		DstVIP:           frame.DstVIP,
		PayloadLen:       uint16(len(encrypted)),
		Nonce:            nonce,
		EncryptedPayload: encrypted,
	}

	// Calculate auth tag (last 16 bytes of encrypted data)
	copy(encFrame.AuthTag[:], encrypted[len(encrypted)-16:])

	return encFrame, nil
}

// DecryptFrame decrypts an encrypted UDP frame
func (sm *SecurityManager) DecryptFrame(encFrame *EncryptedUDPFrame) (*UDPFrame, error) {
	if !sm.config.EncryptionEnabled {
		// Return unencrypted frame if encryption disabled
		return &UDPFrame{
			Version:     encFrame.Version,
			MessageType: encFrame.MessageType,
			SrcVIP:      encFrame.SrcVIP,
			DstVIP:      encFrame.DstVIP,
			PayloadLen:  encFrame.PayloadLen,
			Payload:     encFrame.EncryptedPayload,
		}, nil
	}

	// Create ChaCha20-Poly1305 cipher
	aead, err := chacha20poly1305.New(sm.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Decrypt payload
	decrypted, err := aead.Open(nil, encFrame.Nonce[:], encFrame.EncryptedPayload, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload: %w", err)
	}

	// Create decrypted frame
	frame := &UDPFrame{
		Version:     encFrame.Version,
		MessageType: encFrame.MessageType,
		SrcVIP:      encFrame.SrcVIP,
		DstVIP:      encFrame.DstVIP,
		PayloadLen:  uint16(len(decrypted)),
		Payload:     decrypted,
	}

	return frame, nil
}

// ValidateInput validates user input for security
func (sm *SecurityManager) ValidateInput(input string, inputType string) error {
	// Check message size
	if len(input) > sm.config.MaxMessageSize {
		return fmt.Errorf("input too large: %d bytes (max: %d)", len(input), sm.config.MaxMessageSize)
	}

	// Validate based on input type
	switch inputType {
	case "vip":
		return sm.validateVIP(input)
	case "message":
		return sm.validateMessage(input)
	case "url":
		return sm.validateURL(input)
	default:
		return fmt.Errorf("unknown input type: %s", inputType)
	}
}

// validateVIP validates Virtual IP format
func (sm *SecurityManager) validateVIP(vip string) error {
	// Check if VIP matches allowed pattern
	if sm.config.AllowedVIPPattern != "" {
		matched, err := regexp.MatchString(sm.config.AllowedVIPPattern, vip)
		if err != nil {
			return fmt.Errorf("invalid VIP pattern: %w", err)
		}
		if !matched {
			return fmt.Errorf("VIP %s does not match allowed pattern", vip)
		}
	}

	// Validate IP format
	ip := net.ParseIP(vip)
	if ip == nil {
		return fmt.Errorf("invalid IP format: %s", vip)
	}

	// Check if IP is in private range (10.x.x.x, 172.16-31.x.x, 192.168.x.x)
	if !isPrivateIP(ip) {
		return fmt.Errorf("VIP must be in private IP range: %s", vip)
	}

	return nil
}

// validateMessage validates message content
func (sm *SecurityManager) validateMessage(message string) error {
	// Check for potential injection attacks
	dangerousPatterns := []string{
		"<script", "javascript:", "data:", "vbscript:",
		"onload=", "onerror=", "onclick=",
		"../", "..\\", "/etc/", "C:\\",
	}

	lowerMessage := strings.ToLower(message)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerMessage, pattern) {
			return fmt.Errorf("potentially dangerous content detected: %s", pattern)
		}
	}

	return nil
}

// validateURL validates URL format
func (sm *SecurityManager) validateURL(url string) error {
	// Basic URL validation
	if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		return fmt.Errorf("URL must start with ws:// or wss://")
	}

	// Check for suspicious patterns
	suspiciousPatterns := []string{
		"localhost", "127.0.0.1", "0.0.0.0",
		"file://", "ftp://", "gopher://",
	}

	lowerURL := strings.ToLower(url)
	for _, pattern := range suspiciousPatterns {
		if strings.Contains(lowerURL, pattern) {
			return fmt.Errorf("suspicious URL pattern detected: %s", pattern)
		}
	}

	return nil
}

// CheckRateLimit checks if request is within rate limits
func (sm *SecurityManager) CheckRateLimit(clientID string) error {
	if !sm.config.RateLimitEnabled {
		return nil
	}

	return sm.rateLimiter.Allow(clientID)
}

// ValidateToken validates authentication token
func (sm *SecurityManager) ValidateToken(token string) (*AuthToken, error) {
	if !sm.config.AuthRequired {
		return nil, nil // No authentication required
	}

	// Parse token (assuming JWT format)
	authToken, err := sm.parseToken(token)
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	// Check expiration
	if time.Now().After(authToken.ExpiresAt) {
		return nil, fmt.Errorf("token expired")
	}

	return authToken, nil
}

// parseToken parses JWT token (simplified implementation)
func (sm *SecurityManager) parseToken(token string) (*AuthToken, error) {
	// Split JWT token
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	// Decode payload (base64)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode token payload: %w", err)
	}

	// Parse JSON payload
	var claims struct {
		Exp   int64    `json:"exp"`
		Scope []string `json:"scope"`
		Sub   string   `json:"sub"`
		Iat   int64    `json:"iat"`
	}

	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse token claims: %w", err)
	}

	// Create AuthToken
	authToken := &AuthToken{
		Token:     token,
		ExpiresAt: time.Unix(claims.Exp, 0),
		Scope:     claims.Scope,
		UserID:    claims.Sub,
		IssuedAt:  time.Unix(claims.Iat, 0),
	}

	return authToken, nil
}

// RecordLoginAttempt records a login attempt for rate limiting
func (sm *SecurityManager) RecordLoginAttempt(clientID string, success bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	attempt, exists := sm.loginAttempts[clientID]
	if !exists {
		attempt = &LoginAttempt{}
		sm.loginAttempts[clientID] = attempt
	}

	if success {
		// Reset on successful login
		attempt.Count = 0
		attempt.BlockedUntil = time.Time{}
		return nil
	}

	// Increment failed attempts
	attempt.Count++
	attempt.LastAttempt = time.Now()

	// Check if should be blocked
	if attempt.Count >= sm.config.MaxLoginAttempts {
		attempt.BlockedUntil = time.Now().Add(sm.config.LoginCooldown)
		return fmt.Errorf("too many failed login attempts, blocked until %v", attempt.BlockedUntil)
	}

	// Check if currently blocked
	if time.Now().Before(attempt.BlockedUntil) {
		return fmt.Errorf("login blocked, try again after %v", attempt.BlockedUntil)
	}

	return nil
}

// isPrivateIP checks if IP is in private range
func isPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8
		if ip4[0] == 10 {
			return true
		}
		// 172.16.0.0/12
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return true
		}
		// 192.168.0.0/16
		if ip4[0] == 192 && ip4[1] == 168 {
			return true
		}
	}
	return false
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(window time.Duration, maxReqs int) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		window:   window,
		maxReqs:  maxReqs,
	}
}

// Allow checks if request is allowed under rate limit
func (rl *RateLimiter) Allow(clientID string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Clean old requests
	requests := rl.requests[clientID]
	var validRequests []time.Time
	for _, reqTime := range requests {
		if reqTime.After(cutoff) {
			validRequests = append(validRequests, reqTime)
		}
	}

	// Check if under limit
	if len(validRequests) >= rl.maxReqs {
		return fmt.Errorf("rate limit exceeded for client %s", clientID)
	}

	// Add current request
	validRequests = append(validRequests, now)
	rl.requests[clientID] = validRequests

	return nil
}

// HashPassword hashes a password using Argon2
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, 3, 32*1024, 4, 32)

	// Combine salt and hash
	combined := make([]byte, 16+32)
	copy(combined[:16], salt)
	copy(combined[16:], hash)

	return base64.StdEncoding.EncodeToString(combined), nil
}

// VerifyPassword verifies a password against its hash
func VerifyPassword(password, hash string) bool {
	decoded, err := base64.StdEncoding.DecodeString(hash)
	if err != nil || len(decoded) != 48 {
		return false
	}

	salt := decoded[:16]
	storedHash := decoded[16:]

	computedHash := argon2.IDKey([]byte(password), salt, 3, 32*1024, 4, 32)

	return sha256.Sum256(computedHash) == sha256.Sum256(storedHash)
}
