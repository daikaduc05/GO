package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the agent
type Config struct {
	// Signaling
	SignalingURL string
	Token        string
	LoginURL     string
	WSOrigin     string

	// TURN/STUN
	TURNServer string
	TURNUser   string
	TURNPass   string
	TURNRealm  string
	STUNServer string
}

// LoadConfig loads configuration from .env file or environment variables
func LoadConfig(envFile string) (*Config, error) {
	// Load .env file if specified
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			log.Printf("Warning: Failed to load .env file: %v", err)
		}
	} else {
		// Try to load default .env file
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found, using environment variables only")
		}
	}

	config := &Config{
		SignalingURL: getEnvOrDefault("SIGNALING_URL", ""),
		Token:        getEnvOrDefault("TOKEN", ""),
		LoginURL:     getEnvOrDefault("LOGIN_URL", ""),
		WSOrigin:     getEnvOrDefault("WS_ORIGIN", ""),
		TURNServer:   getEnvOrDefault("TURN_SERVER", "13.229.230.15:3478"),
		TURNUser:     getEnvOrDefault("TURN_USER", "test"),
		TURNPass:     getEnvOrDefault("TURN_PASS", "1234"),
		TURNRealm:    getEnvOrDefault("TURN_REALM", "turn.demo"),
		STUNServer:   getEnvOrDefault("STUN_SERVER", "13.229.230.15:3478"),
	}

	// Validate required fields
	if config.SignalingURL == "" {
		return nil, fmt.Errorf("SIGNALING_URL is required")
	}
	if !strings.HasPrefix(config.SignalingURL, "ws://") && !strings.HasPrefix(config.SignalingURL, "wss://") {
		return nil, fmt.Errorf("SIGNALING_URL must start with ws:// or wss://")
	}

	log.Printf("Config loaded: signaling=%s, turn=%s", config.SignalingURL, config.TURNServer)
	return config, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
