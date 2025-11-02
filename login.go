package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Login performs login and returns the authentication token
func Login(cfg *Config) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)
	
	fmt.Print("Password: ")
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)

	// Determine login URL
	loginURL := strings.TrimSpace(cfg.LoginURL)
	if loginURL == "" {
		base := strings.TrimSuffix(cfg.SignalingURL, "/")
		// Convert ws:// or wss:// to http:// or https://
		if strings.HasPrefix(base, "ws://") {
			base = "http://" + strings.TrimPrefix(base, "ws://")
		} else if strings.HasPrefix(base, "wss://") {
			base = "https://" + strings.TrimPrefix(base, "wss://")
		}
		// Extract host only
		if u, err := url.Parse(base); err == nil {
			hostBase := u.Scheme + "://" + u.Hostname()
			loginURL = hostBase + "/login"
		} else {
			loginURL = base + "/login"
		}
	}

	// Prepare request
	payload := map[string]string{
		"email":    email,
		"password": password,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", loginURL, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	// Retry with backoff
	client := &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(1+attempt) * time.Second)
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login failed: HTTP %d - %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	// Parse response
	b, _ := io.ReadAll(resp.Body)
	var res struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return "", fmt.Errorf("invalid login response: %w", err)
	}

	token := res.Token
	if token == "" {
		token = res.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("empty token in response")
	}

	fmt.Println("✅ Login successful")
	return token, nil
}
