package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ============================================================================
// SECURITY INTEGRATION WITH MAIN AGENT
// ============================================================================

// IntegrateSecurity integrates security features into the main agent
func IntegrateSecurity(agent *Agent, securityConfigPath string) (*SecureAgent, error) {
	// Load security configuration
	securityConfig, err := LoadSecurityConfig(securityConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load security config: %w", err)
	}

	// Validate security configuration
	if err := ValidateSecurityConfig(securityConfig); err != nil {
		return nil, fmt.Errorf("invalid security config: %w", err)
	}

	// Perform security audit
	issues := SecurityAudit(securityConfig)
	if len(issues) > 0 {
		log.Println("Security audit issues:")
		for _, issue := range issues {
			log.Printf("  - %s", issue)
		}
	}

	// Create secure agent
	secureAgent, err := NewSecureAgent(agent.config, securityConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create secure agent: %w", err)
	}

	// Override agent methods with secure versions
	// Note: Method overriding in Go requires interface-based approach
	// This is a simplified version - in production, use proper interface design

	return secureAgent, nil
}

// overrideMethods overrides agent methods with secure versions
func (sa *SecureAgent) overrideMethods() {
	// Note: Direct method overriding is not possible in Go
	// In production, use interface-based design:
	// type SecureTransport interface {
	//     SendUDPFrame(frame UDPFrame, addr *net.UDPAddr) error
	//     HandleDataFrame(frame UDPFrame, addr *net.UDPAddr)
	//     SendPing(vip string) error
	// }
}

// sendSecureUDPFrame sends UDP frame with encryption
func (sa *SecureAgent) sendSecureUDPFrame(frame UDPFrame, addr *net.UDPAddr) error {
	// Encrypt frame
	encFrame, err := sa.securityManager.EncryptFrame(frame)
	if err != nil {
		return fmt.Errorf("failed to encrypt frame: %w", err)
	}

	// Serialize encrypted frame
	data, err := sa.serializeEncryptedFrame(encFrame)
	if err != nil {
		return fmt.Errorf("failed to serialize encrypted frame: %w", err)
	}

	// Send via UDP
	_, err = sa.udpConn.WriteToUDP(data, addr)
	return err
}

// handleSecureDataFrame handles data frame with decryption
func (sa *SecureAgent) handleSecureDataFrame(frame UDPFrame, addr *net.UDPAddr) {
	// Check if frame is encrypted
	if sa.securityManager.config.EncryptionEnabled {
		// Parse as encrypted frame
		encFrame, err := sa.parseEncryptedFrame(frame.Payload)
		if err != nil {
			sa.logger.Printf("Failed to parse encrypted frame: %v", err)
			return
		}

		// Decrypt frame
		decryptedFrame, err := sa.securityManager.DecryptFrame(encFrame)
		if err != nil {
			sa.logger.Printf("Failed to decrypt frame: %v", err)
			return
		}

		// Handle decrypted frame
		sa.Agent.handleDataFrame(*decryptedFrame, addr)
	} else {
		// Handle unencrypted frame
		sa.Agent.handleDataFrame(frame, addr)
	}
}

// sendSecurePing sends ping with security checks
func (sa *SecureAgent) sendSecurePing(vip string) error {
	// Validate VIP
	if err := sa.securityManager.ValidateInput(vip, "vip"); err != nil {
		return fmt.Errorf("invalid VIP: %w", err)
	}

	// Check rate limits
	if err := sa.securityManager.CheckRateLimit("system"); err != nil {
		return fmt.Errorf("rate limit exceeded: %w", err)
	}

	// Send ping using base method
	return sa.Agent.sendPing(vip)
}

// SetupSecurityConfig creates default security configuration
func SetupSecurityConfig(configPath string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Generate encryption key if not provided
	config := DefaultSecurityConfig()
	if config.EncryptionKey == "" {
		key, err := GenerateEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to generate encryption key: %w", err)
		}
		config.EncryptionKey = key
	}

	// Save configuration
	if err := SaveSecurityConfig(config, configPath); err != nil {
		return fmt.Errorf("failed to save security config: %w", err)
	}

	log.Printf("Security configuration created at: %s", configPath)
	return nil
}

// SecurityMiddleware provides security middleware for HTTP endpoints
func SecurityMiddleware(securityManager *SecurityManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check rate limits
			clientIP := getClientIP(r)
			if err := securityManager.CheckRateLimit(clientIP); err != nil {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			// Check authentication if required
			if securityManager.config.AuthRequired {
				token := r.Header.Get("Authorization")
				if token == "" {
					http.Error(w, "Authentication required", http.StatusUnauthorized)
					return
				}

				// Remove "Bearer " prefix if present
				if len(token) > 7 && token[:7] == "Bearer " {
					token = token[7:]
				}

				// Validate token
				_, err := securityManager.ValidateToken(token)
				if err != nil {
					http.Error(w, "Invalid token", http.StatusUnauthorized)
					return
				}
			}

			// Continue to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts client IP from request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take first IP in the list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// SecurityHealthCheck provides health check for security features
func SecurityHealthCheck(securityManager *SecurityManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := map[string]interface{}{
			"encryption_enabled": securityManager.config.EncryptionEnabled,
			"auth_required":      securityManager.config.AuthRequired,
			"rate_limit_enabled": securityManager.config.RateLimitEnabled,
			"status":             "healthy",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	}
}

// LogSecurityEvent logs security-related events
func LogSecurityEvent(event string, details map[string]interface{}) {
	// In production, send to security monitoring system
	log.Printf("SECURITY_EVENT: %s - %+v", event, details)
}

// SecurityMetrics provides security metrics
type SecurityMetrics struct {
	EncryptionEnabled  bool  `json:"encryption_enabled"`
	AuthRequired       bool  `json:"auth_required"`
	RateLimitEnabled   bool  `json:"rate_limit_enabled"`
	ActiveSessions     int   `json:"active_sessions"`
	FailedLogins       int64 `json:"failed_logins"`
	BlockedRequests    int64 `json:"blocked_requests"`
	EncryptedMessages  int64 `json:"encrypted_messages"`
	DecryptionFailures int64 `json:"decryption_failures"`
	RateLimitHits      int64 `json:"rate_limit_hits"`
}

// GetSecurityMetrics returns current security metrics
func (sa *SecureAgent) GetSecurityMetrics() SecurityMetrics {
	sa.sessionMu.RLock()
	activeSessions := len(sa.clientSessions)
	sa.sessionMu.RUnlock()

	return SecurityMetrics{
		EncryptionEnabled: sa.securityManager.config.EncryptionEnabled,
		AuthRequired:      sa.securityManager.config.AuthRequired,
		RateLimitEnabled:  sa.securityManager.config.RateLimitEnabled,
		ActiveSessions:    activeSessions,
		// Add more metrics as needed
	}
}
