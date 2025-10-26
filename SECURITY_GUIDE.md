# 🔒 Security Guide for UDP+TUN Agent

## Tổng Quan Bảo Mật

Hệ thống UDP+TUN Agent đã được cải tiến với các tính năng bảo mật toàn diện để đảm bảo an toàn cho dữ liệu và kết nối.

## 🛡️ Các Tính Năng Bảo Mật

### 1. **Mã Hóa End-to-End**
- **ChaCha20-Poly1305**: Thuật toán mã hóa hiện đại, nhanh và an toàn
- **Key Derivation**: Sử dụng Argon2id để tạo khóa từ mật khẩu
- **Nonce Random**: Mỗi gói tin có nonce ngẫu nhiên duy nhất
- **Authentication Tag**: Xác thực tính toàn vẹn dữ liệu

### 2. **Xác Thực và Phân Quyền**
- **JWT Tokens**: Token xác thực với thời hạn
- **Scope-based Permissions**: Phân quyền chi tiết
- **Session Management**: Quản lý phiên đăng nhập
- **Login Rate Limiting**: Chống tấn công brute force

### 3. **Input Validation**
- **VIP Pattern Matching**: Chỉ cho phép IP trong dải private
- **Message Size Limits**: Giới hạn kích thước tin nhắn
- **Content Filtering**: Lọc nội dung nguy hiểm
- **URL Validation**: Kiểm tra URL signaling server

### 4. **Rate Limiting**
- **Per-client Limits**: Giới hạn theo từng client
- **Time Windows**: Cửa sổ thời gian linh hoạt
- **DoS Protection**: Chống tấn công từ chối dịch vụ

## 🚀 Cài Đặt Bảo Mật

### 1. **Cài Đặt Dependencies**

```bash
# Cài đặt thư viện crypto
go get golang.org/x/crypto

# Cài đặt các dependencies khác
go mod tidy
```

### 2. **Cấu Hình Bảo Mật**

```bash
# Copy file cấu hình bảo mật
cp security.env .security.env

# Chỉnh sửa cấu hình
nano .security.env
```

### 3. **Tạo Encryption Key**

```bash
# Tạo key mã hóa mạnh
openssl rand -base64 32

# Hoặc sử dụng Go
go run -c 'package main; import ("crypto/rand"; "encoding/base64"; "fmt"); func main() { key := make([]byte, 32); rand.Read(key); fmt.Println(base64.StdEncoding.EncodeToString(key)) }'
```

## 📋 Cấu Hình Chi Tiết

### **File: security.env**

```bash
# Encryption Settings
SECURITY_ENCRYPTION_ENABLED=true
SECURITY_ENCRYPTION_KEY=your-32-byte-base64-key-here
SECURITY_KEY_DERIVATION_SALT=your-unique-salt-here

# Authentication Settings  
SECURITY_AUTH_REQUIRED=true
SECURITY_TOKEN_EXPIRY=24h
SECURITY_MAX_LOGIN_ATTEMPTS=5
SECURITY_LOGIN_COOLDOWN=15m

# Input Validation
SECURITY_MAX_MESSAGE_SIZE=1024
SECURITY_ALLOWED_VIP_PATTERN=^10\.10\.\d{1,3}\.\d{1,3}$

# Rate Limiting
SECURITY_RATE_LIMIT_ENABLED=true
SECURITY_RATE_LIMIT_WINDOW=1m
SECURITY_RATE_LIMIT_MAX=60
```

## 🔧 Sử Dụng Secure Agent

### **1. Khởi Tạo Secure Agent**

```go
package main

import (
    "log"
    "time"
)

func main() {
    // Load base configuration
    config, err := loadConfig("config.env")
    if err != nil {
        log.Fatal(err)
    }
    
    // Create base agent
    agent, err := NewAgent(config)
    if err != nil {
        log.Fatal(err)
    }
    
    // Integrate security
    secureAgent, err := IntegrateSecurity(agent, "security.env")
    if err != nil {
        log.Fatal(err)
    }
    
    // Start secure agent
    if err := secureAgent.Start(); err != nil {
        log.Fatal(err)
    }
    defer secureAgent.Stop()
    
    // Run agent...
}
```

### **2. Xác Thực Client**

```go
// Authenticate client
err := secureAgent.AuthenticateClient("client-001", "jwt-token-here")
if err != nil {
    log.Printf("Authentication failed: %v", err)
    return
}

// Send secure message
err = secureAgent.SendSecureMessage("client-001", "Hello World", "10.10.0.6")
if err != nil {
    log.Printf("Failed to send message: %v", err)
}
```

### **3. Tạo Authentication Token**

```go
// Generate secure token
token, err := secureAgent.GenerateSecureToken("user-001", []string{"send_message", "ping"})
if err != nil {
    log.Printf("Failed to generate token: %v", err)
    return
}

fmt.Printf("Authentication token: %s\n", token)
```

## 🛠️ API Bảo Mật

### **Security Endpoints**

```go
// Health check endpoint
http.HandleFunc("/security/health", SecurityHealthCheck(securityManager))

// Metrics endpoint  
http.HandleFunc("/security/metrics", func(w http.ResponseWriter, r *http.Request) {
    metrics := secureAgent.GetSecurityMetrics()
    json.NewEncoder(w).Encode(metrics)
})

// Status endpoint
http.HandleFunc("/security/status", func(w http.ResponseWriter, r *http.Request) {
    status := secureAgent.GetSecurityStatus()
    json.NewEncoder(w).Encode(status)
})
```

### **Middleware Bảo Mật**

```go
// Apply security middleware
securityMiddleware := SecurityMiddleware(securityManager)
http.Handle("/api/", securityMiddleware(http.HandlerFunc(apiHandler)))
```

## 📊 Monitoring Bảo Mật

### **Security Metrics**

```go
type SecurityMetrics struct {
    EncryptionEnabled    bool    `json:"encryption_enabled"`
    AuthRequired         bool    `json:"auth_required"`
    RateLimitEnabled     bool    `json:"rate_limit_enabled"`
    ActiveSessions       int     `json:"active_sessions"`
    FailedLogins         int64   `json:"failed_logins"`
    BlockedRequests      int64   `json:"blocked_requests"`
    EncryptedMessages    int64   `json:"encrypted_messages"`
    DecryptionFailures   int64   `json:"decryption_failures"`
    RateLimitHits        int64   `json:"rate_limit_hits"`
}
```

### **Security Events**

```go
// Log security events
LogSecurityEvent("login_failed", map[string]interface{}{
    "client_id": "client-001",
    "ip": "192.168.1.100",
    "reason": "invalid_token",
})

LogSecurityEvent("rate_limit_exceeded", map[string]interface{}{
    "client_id": "client-002", 
    "ip": "192.168.1.101",
    "requests_per_minute": 65,
})
```

## 🔍 Security Audit

### **Chạy Security Audit**

```go
// Load security config
config, err := LoadSecurityConfig("security.env")
if err != nil {
    log.Fatal(err)
}

// Perform security audit
issues := SecurityAudit(config)
if len(issues) > 0 {
    log.Println("Security issues found:")
    for _, issue := range issues {
        log.Printf("  - %s", issue)
    }
}

// Get security recommendations
recommendations := SecurityRecommendations()
for _, rec := range recommendations {
    log.Printf("  - %s", rec)
}
```

## ⚠️ Security Best Practices

### **1. Key Management**
- ✅ Sử dụng key mạnh (32+ bytes)
- ✅ Thay đổi key định kỳ
- ✅ Lưu trữ key an toàn
- ❌ Không hardcode key trong code

### **2. Authentication**
- ✅ Sử dụng JWT với expiration ngắn
- ✅ Implement refresh token
- ✅ Logout khi token hết hạn
- ❌ Không lưu password dạng plaintext

### **3. Network Security**
- ✅ Sử dụng TLS cho signaling
- ✅ Validate tất cả input
- ✅ Implement rate limiting
- ❌ Không trust user input

### **4. Monitoring**
- ✅ Log tất cả security events
- ✅ Monitor failed logins
- ✅ Alert khi có suspicious activity
- ❌ Không ignore security warnings

## 🚨 Security Checklist

### **Before Production**

- [ ] Encryption enabled với key mạnh
- [ ] Authentication required
- [ ] Rate limiting configured
- [ ] Input validation enabled
- [ ] VIP pattern restricted
- [ ] TLS cho signaling server
- [ ] Security monitoring enabled
- [ ] Logging configured
- [ ] Security audit passed
- [ ] Dependencies updated

### **Regular Maintenance**

- [ ] Rotate encryption keys
- [ ] Update security configs
- [ ] Review access logs
- [ ] Test security features
- [ ] Update dependencies
- [ ] Security training
- [ ] Penetration testing
- [ ] Backup security configs

## 🔧 Troubleshooting

### **Common Issues**

#### **1. Encryption Errors**
```bash
# Check encryption key
echo $SECURITY_ENCRYPTION_KEY | base64 -d | wc -c
# Should output 32

# Regenerate key
openssl rand -base64 32
```

#### **2. Authentication Failures**
```bash
# Check token format
echo "your-token" | cut -d. -f2 | base64 -d

# Validate token expiry
# Check JWT payload for 'exp' field
```

#### **3. Rate Limit Issues**
```bash
# Check rate limit config
grep SECURITY_RATE_LIMIT .security.env

# Monitor rate limit hits
tail -f logs/security.log | grep "rate_limit"
```

## 📚 References

- [ChaCha20-Poly1305 RFC](https://tools.ietf.org/html/rfc8439)
- [Argon2 Specification](https://github.com/P-H-C/phc-winner-argon2)
- [JWT Best Practices](https://tools.ietf.org/html/rfc8725)
- [OWASP Security Guidelines](https://owasp.org/www-project-top-ten/)
- [Go Security Best Practices](https://golang.org/doc/security/)
