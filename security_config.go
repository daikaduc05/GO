package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"
)

// ============================================================================
// SECURITY CONFIGURATION MANAGEMENT
// ============================================================================

// DefaultSecurityConfig returns default security configuration
func DefaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		// Encryption settings
		EncryptionEnabled: true, //BẬT MÃ HÓA
		EncryptionKey:     "", // Will be generated if emptY tạo tự động
		KeyDerivationSalt: "udp-tun-agent-salt-2024",

		// Authentication settings
		AuthRequired:     true, //giới hạn 1 kb
		TokenExpiry:      24 * time.Hour, 
		MaxLoginAttempts: 5,
		LoginCooldown:    15 * time.Minute,

		// Input validation
		MaxMessageSize:    1024,
		AllowedVIPPattern: `^10\.10\.\d{1,3}\.\d{1,3}$`,

		// Rate limiting
		RateLimitEnabled: true, //bật giới hạn tốc độ
		RateLimitWindow:  1 * time.Minute,
		RateLimitMax:     60, // 60 requests per minute
	}
}

// LoadSecurityConfig loads security configuration from file
func LoadSecurityConfig(configPath string) (*SecurityConfig, error) {
	// Try to load from file first
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			var config SecurityConfig
			if err := json.Unmarshal(data, &config); err == nil {
				return &config, nil
			}
		}
	}

	// Fall back to environment variables
	config := DefaultSecurityConfig()

	// Load from environment variables
	if os.Getenv("SECURITY_ENCRYPTION_ENABLED") == "false" {
		config.EncryptionEnabled = false
	}

	if key := os.Getenv("SECURITY_ENCRYPTION_KEY"); key != "" {
		config.EncryptionKey = key
	}

	if salt := os.Getenv("SECURITY_KEY_DERIVATION_SALT"); salt != "" {
		config.KeyDerivationSalt = salt
	}

	if os.Getenv("SECURITY_AUTH_REQUIRED") == "false" {
		config.AuthRequired = false
	}

	if maxSize := os.Getenv("SECURITY_MAX_MESSAGE_SIZE"); maxSize != "" {
		if size, err := strconv.Atoi(maxSize); err == nil {
			config.MaxMessageSize = size
		}
	}

	if pattern := os.Getenv("SECURITY_ALLOWED_VIP_PATTERN"); pattern != "" {
		config.AllowedVIPPattern = pattern
	}

	if os.Getenv("SECURITY_RATE_LIMIT_ENABLED") == "false" {
		config.RateLimitEnabled = false
	}

	return config, nil
}

// SaveSecurityConfig saves security configuration to file
func SaveSecurityConfig(config *SecurityConfig, configPath string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal security config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write security config: %w", err)
	}

	return nil
}

// ValidateSecurityConfig validates security configuration
func ValidateSecurityConfig(config *SecurityConfig) error {
	// Validate encryption settings
	if config.EncryptionEnabled {
		if config.EncryptionKey == "" {
			return fmt.Errorf("encryption key is required when encryption is enabled")
		}
		if len(config.EncryptionKey) < 32 {
			return fmt.Errorf("encryption key must be at least 32 characters")
		}
		if config.KeyDerivationSalt == "" {
			return fmt.Errorf("key derivation salt is required when encryption is enabled")
		}
	}

	// Validate authentication settings
	if config.AuthRequired {
		if config.TokenExpiry <= 0 {
			return fmt.Errorf("token expiry must be positive")
		}
		if config.MaxLoginAttempts <= 0 {
			return fmt.Errorf("max login attempts must be positive")
		}
		if config.LoginCooldown <= 0 {
			return fmt.Errorf("login cooldown must be positive")
		}
	}

	// Validate input validation settings
	if config.MaxMessageSize <= 0 {
		return fmt.Errorf("max message size must be positive")
	}
	if config.MaxMessageSize > 65535 {
		return fmt.Errorf("max message size too large: %d", config.MaxMessageSize)
	}

	// Validate VIP pattern
	if config.AllowedVIPPattern != "" {
		if _, err := regexp.Compile(config.AllowedVIPPattern); err != nil {
			return fmt.Errorf("invalid VIP pattern: %w", err)
		}
	}

	// Validate rate limiting settings
	if config.RateLimitEnabled {
		if config.RateLimitWindow <= 0 {
			return fmt.Errorf("rate limit window must be positive")
		}
		if config.RateLimitMax <= 0 {
			return fmt.Errorf("rate limit max must be positive")
		}
	}

	return nil
}

// GenerateEncryptionKey generates a secure encryption key
func GenerateEncryptionKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to generate encryption key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(key), nil
}

// SecurityConfigTemplate returns a template for security configuration
func SecurityConfigTemplate() string {
	return `{
  "encryption_enabled": true,
  "encryption_key": "GENERATE_NEW_KEY_HERE",
  "key_derivation_salt": "udp-tun-agent-salt-2024",
  "auth_required": true,
  "token_expiry": "24h",
  "max_login_attempts": 5,
  "login_cooldown": "15m",
  "max_message_size": 1024,
  "allowed_vip_pattern": "^10\\.10\\.\\d{1,3}\\.\\d{1,3}$",
  "rate_limit_enabled": true,
  "rate_limit_window": "1m",
  "rate_limit_max": 60
}`
}

// SecurityAudit performs security audit of configuration
func SecurityAudit(config *SecurityConfig) []string {
	var issues []string

	// Check encryption
	if !config.EncryptionEnabled {
		issues = append(issues, "WARNING: Encryption is disabled - data will be transmitted in plaintext")
	}

	if config.EncryptionEnabled && config.EncryptionKey == "" {
		issues = append(issues, "CRITICAL: Encryption enabled but no key provided")
	}

	// Check authentication
	if !config.AuthRequired {
		issues = append(issues, "WARNING: Authentication is disabled - anyone can connect")
	}

	// Check rate limiting
	if !config.RateLimitEnabled {
		issues = append(issues, "WARNING: Rate limiting is disabled - vulnerable to DoS attacks")
	}

	// Check message size
	if config.MaxMessageSize > 4096 {
		issues = append(issues, "WARNING: Large message size limit may allow memory exhaustion attacks")
	}

	// Check VIP pattern
	if config.AllowedVIPPattern == "" {
		issues = append(issues, "WARNING: No VIP pattern restriction - any IP format allowed")
	}

	// Check login attempts
	if config.MaxLoginAttempts > 10 {
		issues = append(issues, "WARNING: High login attempt limit may allow brute force attacks")
	}

	// Check cooldown
	if config.LoginCooldown < 5*time.Minute {
		issues = append(issues, "WARNING: Short login cooldown may allow rapid retry attacks")
	}

	return issues
}

// SecurityRecommendations returns security recommendations
func SecurityRecommendations() []string {
	return []string{
		"Enable encryption for all communications",
		"Use strong, unique encryption keys",
		"Enable authentication for all connections",
		"Implement rate limiting to prevent DoS attacks",
		"Restrict VIP patterns to private IP ranges only",
		"Set reasonable message size limits",
		"Use short token expiry times",
		"Implement proper logging and monitoring",
		"Regular security audits and updates",
		"Use TLS for signaling server connections",
	}
}
