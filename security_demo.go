package main

import (
	"fmt"
	"log"
	"time"
)

// ============================================================================
// SECURITY DEMO - Test Security Features
// ============================================================================

// RunSecurityDemo demonstrates security features
func RunSecurityDemo() {
	fmt.Println("🔒 Security Features Demo")
	fmt.Println("=========================")

	// 1. Test Encryption
	fmt.Println("\n1. Testing Encryption...")
	testEncryption()

	// 2. Test Authentication
	fmt.Println("\n2. Testing Authentication...")
	testAuthentication()

	// 3. Test Input Validation
	fmt.Println("\n3. Testing Input Validation...")
	testInputValidation()

	// 4. Test Rate Limiting
	fmt.Println("\n4. Testing Rate Limiting...")
	testRateLimiting()

	// 5. Test Security Audit
	fmt.Println("\n5. Testing Security Audit...")
	testSecurityAudit()

	fmt.Println("\n✅ Security demo completed!")
}

// testEncryption tests encryption/decryption functionality
func testEncryption() {
	// Create security config
	config := &SecurityConfig{
		EncryptionEnabled: true,
		EncryptionKey:     "test-key-32-bytes-long-123456789",
		KeyDerivationSalt: "test-salt",
	}

	// Create security manager
	sm, err := NewSecurityManager(config)
	if err != nil {
		log.Printf("Failed to create security manager: %v", err)
		return
	}

	// Create test frame
	frame := UDPFrame{
		Version:     1,
		MessageType: 0,
		SrcVIP:      [4]byte{10, 10, 0, 5},
		DstVIP:      [4]byte{10, 10, 0, 6},
		PayloadLen:  5,
		Payload:     []byte("hello"),
	}

	// Encrypt frame
	encFrame, err := sm.EncryptFrame(frame)
	if err != nil {
		log.Printf("Encryption failed: %v", err)
		return
	}

	fmt.Printf("  ✅ Frame encrypted successfully\n")
	fmt.Printf("  📦 Original payload: %s\n", string(frame.Payload))
	fmt.Printf("  🔐 Encrypted size: %d bytes\n", len(encFrame.EncryptedPayload))

	// Decrypt frame
	decFrame, err := sm.DecryptFrame(encFrame)
	if err != nil {
		log.Printf("Decryption failed: %v", err)
		return
	}

	fmt.Printf("  ✅ Frame decrypted successfully\n")
	fmt.Printf("  📦 Decrypted payload: %s\n", string(decFrame.Payload))

	// Verify integrity
	if string(decFrame.Payload) == string(frame.Payload) {
		fmt.Printf("  ✅ Data integrity verified\n")
	} else {
		fmt.Printf("  ❌ Data integrity check failed\n")
	}
}

// testAuthentication tests authentication functionality
func testAuthentication() {
	// Create security config
	config := &SecurityConfig{
		AuthRequired:     true,
		TokenExpiry:      1 * time.Hour,
		MaxLoginAttempts: 3,
		LoginCooldown:    1 * time.Minute,
	}

	sm, err := NewSecurityManager(config)
	if err != nil {
		log.Printf("Failed to create security manager: %v", err)
		return
	}

	// Test valid token
	validToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTAwMSIsImlhdCI6MTY0MDk5NTIwMCwiZXhwIjoxNjQxMDgxNjAwLCJzY29wZSI6WyJzZW5kX21lc3NhZ2UiXX0.signature"

	authToken, err := sm.ValidateToken(validToken)
	if err != nil {
		fmt.Printf("  ⚠️  Token validation failed (expected for demo): %v\n", err)
	} else {
		fmt.Printf("  ✅ Token validated successfully\n")
		fmt.Printf("  👤 User ID: %s\n", authToken.UserID)
		fmt.Printf("  🔑 Permissions: %v\n", authToken.Scope)
	}

	// Test login attempts
	clientID := "client-001"

	// Simulate failed attempts
	for i := 0; i < 3; i++ {
		err := sm.RecordLoginAttempt(clientID, false)
		if err != nil {
			fmt.Printf("  ⚠️  Login attempt %d failed: %v\n", i+1, err)
		}
	}

	// Test rate limiting
	fmt.Printf("  🚦 Testing rate limiting...\n")
	for i := 0; i < 5; i++ {
		err := sm.CheckRateLimit(clientID)
		if err != nil {
			fmt.Printf("  ⚠️  Rate limit hit at attempt %d: %v\n", i+1, err)
			break
		}
		fmt.Printf("  ✅ Request %d allowed\n", i+1)
	}
}

// testInputValidation tests input validation
func testInputValidation() {
	config := &SecurityConfig{
		MaxMessageSize:    1024,
		AllowedVIPPattern: `^10\.10\.\d{1,3}\.\d{1,3}$`,
	}

	sm, err := NewSecurityManager(config)
	if err != nil {
		log.Printf("Failed to create security manager: %v", err)
		return
	}

	// Test valid inputs
	validInputs := []struct {
		input      string
		inputType  string
		shouldPass bool
	}{
		{"10.10.0.5", "vip", true},
		{"Hello World", "message", true},
		{"ws://localhost:8000", "url", true},
		{"10.10.0.999", "vip", false},                       // Invalid IP
		{"<script>alert('xss')</script>", "message", false}, // XSS attempt
		{"http://malicious.com", "url", false},              // Suspicious URL
	}

	for _, test := range validInputs {
		err := sm.ValidateInput(test.input, test.inputType)
		if (err == nil) == test.shouldPass {
			if test.shouldPass {
				fmt.Printf("  Valid input accepted: %s\n", test.input)
			} else {
				fmt.Printf("  Invalid input rejected: %s\n", test.input)
			}
		} else {
			fmt.Printf("   Validation failed for: %s (expected: %v, got: %v)\n",
				test.input, test.shouldPass, err == nil)
		}
	}
}

// testRateLimiting tests rate limiting functionality
func testRateLimiting() {
	// Create rate limiter
	rl := NewRateLimiter(1*time.Second, 3) // 3 requests per second

	clientID := "test-client"

	// Test normal usage
	for i := 0; i < 3; i++ {
		err := rl.Allow(clientID)
		if err != nil {
			fmt.Printf("  Request %d blocked unexpectedly: %v\n", i+1, err)
		} else {
			fmt.Printf("  Request %d allowed\n", i+1)
		}
	}

	// Test rate limit
	err := rl.Allow(clientID)
	if err == nil {
		fmt.Printf("  Rate limit not enforced\n")
	} else {
		fmt.Printf("  Rate limit enforced: %v\n", err)
	}

	// Wait and test again
	time.Sleep(2 * time.Second)
	err = rl.Allow(clientID)
	if err != nil {
		fmt.Printf("  Rate limit still active after cooldown: %v\n", err)
	} else {
		fmt.Printf("  Rate limit reset after cooldown\n")
	}
}

// testSecurityAudit tests security audit functionality
func testSecurityAudit() {
	// Test secure configuration
	secureConfig := &SecurityConfig{
		EncryptionEnabled: true,
		EncryptionKey:     "strong-key-32-bytes-long-123456",
		AuthRequired:      true,
		RateLimitEnabled:  true,
		MaxMessageSize:    1024,
		AllowedVIPPattern: `^10\.10\.\d{1,3}\.\d{1,3}$`,
	}

	issues := SecurityAudit(secureConfig)
	if len(issues) == 0 {
		fmt.Printf("   Security audit passed - no issues found\n")
	} else {
		fmt.Printf("   Security audit found %d issues:\n", len(issues))
		for _, issue := range issues {
			fmt.Printf("    - %s\n", issue)
		}
	}

	// Test insecure configuration
	insecureConfig := &SecurityConfig{
		EncryptionEnabled: false,
		AuthRequired:      false,
		RateLimitEnabled:  false,
		MaxMessageSize:    10000,
		AllowedVIPPattern: "",
	}

	issues = SecurityAudit(insecureConfig)
	fmt.Printf("  ⚠️  Insecure config has %d security issues\n", len(issues))

	// Show recommendations
	recommendations := SecurityRecommendations()
	fmt.Printf("  📋 Security recommendations:\n")
	for i, rec := range recommendations {
		if i < 5 { // Show first 5 recommendations
			fmt.Printf("    - %s\n", rec)
		}
	}
	if len(recommendations) > 5 {
		fmt.Printf("    ... and %d more\n", len(recommendations)-5)
	}
}

// DemoMain demonstrates how to use secure agent
func DemoMain() {
	fmt.Println("🚀 Secure Agent Demo")
	fmt.Println("===================")

	// Load security configuration
	securityConfig, err := LoadSecurityConfig("security.env")
	if err != nil {
		log.Printf("Failed to load security config: %v", err)
		// Use default config
		securityConfig = DefaultSecurityConfig()
	}

	// Validate configuration
	if err := ValidateSecurityConfig(securityConfig); err != nil {
		log.Printf("Invalid security config: %v", err)
		return
	}

	// Perform security audit
	issues := SecurityAudit(securityConfig)
	if len(issues) > 0 {
		fmt.Printf("⚠️  Security issues found:\n")
		for _, issue := range issues {
			fmt.Printf("  - %s\n", issue)
		}
	}

	// Create secure agent (simplified demo)
	fmt.Printf("✅ Security configuration loaded successfully\n")
	fmt.Printf("🔐 Encryption enabled: %v\n", securityConfig.EncryptionEnabled)
	fmt.Printf("🔑 Authentication required: %v\n", securityConfig.AuthRequired)
	fmt.Printf("🚦 Rate limiting enabled: %v\n", securityConfig.RateLimitEnabled)

	// Show security recommendations
	recommendations := SecurityRecommendations()
	fmt.Printf("\n📋 Security recommendations:\n")
	for i, rec := range recommendations {
		if i < 3 { // Show first 3 recommendations
			fmt.Printf("  - %s\n", rec)
		}
	}
}
