package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

// LoadSettings loads agent settings from environment variables and .env file
func LoadSettings(envFile string) (*AgentSettings, error) {
	// Load .env file if specified
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			logrus.WithError(err).Warning("Failed to load .env file, using environment variables only")
		} else {
			logrus.WithField("file", envFile).Info("Loaded configuration from .env file")
		}
	} else {
		// Try to load default .env file
		if err := godotenv.Load(); err != nil {
			logrus.Debug("No .env file found, using environment variables only")
		} else {
			logrus.Info("Loaded configuration from .env file")
		}
	}

	// Build ICE servers from environment
	iceServers := buildICEServersFromEnv()

	// Create settings from environment variables
	settings := &AgentSettings{
		AgentID:          getEnvOrDefault("AGENT_ID", ""),
		SignalingURL:     getEnvOrDefault("SIGNALING_URL", ""),
		SignalingToken:   getEnvOrDefault("SIGNALING_TOKEN", ""),
		ICEServers:       iceServers,
		DataChannelLabel: getEnvOrDefault("DATA_CHANNEL_LABEL", "mvp"),
		Mode:             getEnvOrDefault("MODE", "chat"),
	}

	// Validate required fields
	if settings.AgentID == "" {
		return nil, fmt.Errorf("AGENT_ID is required")
	}
	if settings.SignalingURL == "" {
		return nil, fmt.Errorf("SIGNALING_URL is required")
	}

	// Validate signaling URL format
	if !strings.HasPrefix(settings.SignalingURL, "ws://") && !strings.HasPrefix(settings.SignalingURL, "wss://") {
		return nil, fmt.Errorf("SIGNALING_URL must start with ws:// or wss://")
	}

	// Validate mode
	validModes := []string{"chat", "json", "bytes"}
	if !contains(validModes, settings.Mode) {
		return nil, fmt.Errorf("MODE must be one of: %s", strings.Join(validModes, ", "))
	}

	logrus.WithFields(logrus.Fields{
		"agent_id":      settings.AgentID,
		"signaling_url": settings.SignalingURL,
		"mode":          settings.Mode,
		"ice_servers":   len(settings.ICEServers),
	}).Info("Loaded agent settings")

	return settings, nil
}

// buildICEServersFromEnv builds ICE servers list from environment variables
func buildICEServersFromEnv() []webrtc.ICEServer {
	var iceServers []webrtc.ICEServer

	// Single ICE server from individual env vars
	iceURL := getEnvOrDefault("ICE_URL", "")
	if iceURL != "" {
		iceServer := webrtc.ICEServer{
			URLs: []string{iceURL},
		}

		if username := getEnvOrDefault("ICE_USERNAME", ""); username != "" {
			iceServer.Username = username
		}
		if credential := getEnvOrDefault("ICE_CREDENTIAL", ""); credential != "" {
			iceServer.Credential = credential
		}

		iceServers = append(iceServers, iceServer)
		logrus.WithField("url", iceURL).Info("Added ICE server from ICE_URL")
	}

	// Multiple ICE servers from CSV
	iceURLs := getEnvOrDefault("ICE_URLS", "")
	if iceURLs != "" {
		urls := strings.Split(iceURLs, ",")
		for _, url := range urls {
			url = strings.TrimSpace(url)
			if url == "" {
				continue
			}

			iceServer := webrtc.ICEServer{
				URLs: []string{url},
			}

			if username := getEnvOrDefault("ICE_USERNAME", ""); username != "" {
				iceServer.Username = username
			}
			if credential := getEnvOrDefault("ICE_CREDENTIAL", ""); credential != "" {
				iceServer.Credential = credential
			}

			iceServers = append(iceServers, iceServer)
			logrus.WithField("url", url).Info("Added ICE server from ICE_URLS")
		}
	}

	// Default STUN server if no ICE servers configured
	if len(iceServers) == 0 {
		defaultSTUN := webrtc.ICEServer{
			URLs: []string{"stun:stun.l.google.com:19302"},
		}
		iceServers = append(iceServers, defaultSTUN)
		logrus.Info("Using default STUN server")
	}

	return iceServers
}

// getEnvOrDefault gets environment variable value or returns default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBoolOrDefault gets boolean environment variable value or returns default
func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// contains checks if slice contains string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// PrintConfigHelp prints configuration help
func PrintConfigHelp() {
	fmt.Println("=== WebRTC Agent Configuration ===")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  AGENT_ID          - Unique agent identifier (required)")
	fmt.Println("  SIGNALING_URL     - WebSocket signaling server URL (required)")
	fmt.Println("  SIGNALING_TOKEN   - Optional authentication token")
	fmt.Println("  ICE_URL           - Single ICE server URL")
	fmt.Println("  ICE_URLS          - Multiple ICE server URLs (comma-separated)")
	fmt.Println("  ICE_USERNAME      - ICE server username")
	fmt.Println("  ICE_CREDENTIAL    - ICE server credential")
	fmt.Println("  DATA_CHANNEL_LABEL - Data channel label (default: mvp)")
	fmt.Println("  MODE              - Transport mode: chat, json, bytes (default: chat)")
	fmt.Println("  VERBOSE           - Enable verbose logging: true/false (default: false)")
	fmt.Println()
	fmt.Println("Example .env file:")
	fmt.Println("  AGENT_ID=agent-001")
	fmt.Println("  SIGNALING_URL=ws://localhost:8000/ws/agent-001")
	fmt.Println("  ICE_URL=stun:stun.l.google.com:19302")
	fmt.Println("  MODE=chat")
	fmt.Println("  VERBOSE=false")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  # Load from .env file")
	fmt.Println("  ./webrtc-agent -env=config.env -listen")
	fmt.Println()
	fmt.Println("  # Override with command line flags")
	fmt.Println("  ./webrtc-agent -agent-id=agent-001 -signaling-url=ws://localhost:8000/ws/agent-001")
	fmt.Println()
}
