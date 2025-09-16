package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

func main() {
	// Command line flags
	var (
		envFile       = flag.String("env", "", "Path to .env file (optional)")
		agentID       = flag.String("agent-id", "", "Agent ID (overrides env)")
		signalingURL  = flag.String("signaling-url", "", "Signaling server URL (overrides env)")
		peerID        = flag.String("peer-id", "", "Peer ID to connect to (for offerer mode)")
		mode          = flag.String("mode", "", "Transport mode: chat, json, bytes (overrides env)")
		listen        = flag.Bool("listen", false, "Listen for incoming connections (answerer mode)")
		verbose       = flag.Bool("verbose", false, "Enable verbose logging (overrides env)")
		iceURL        = flag.String("ice-url", "", "ICE server URL (overrides env)")
		iceUsername   = flag.String("ice-username", "", "ICE server username (overrides env)")
		iceCredential = flag.String("ice-credential", "", "ICE server credential (overrides env)")
		help          = flag.Bool("help", false, "Show configuration help")
	)
	flag.Parse()

	// Show help if requested
	if *help {
		PrintConfigHelp()
		return
	}

	// Load settings from .env file and environment variables
	settings, err := LoadSettings(*envFile)
	if err != nil {
		log.Fatalf("Failed to load settings: %v", err)
	}

	// Override with command line flags if provided
	if *agentID != "" {
		settings.AgentID = *agentID
	}
	if *signalingURL != "" {
		settings.SignalingURL = *signalingURL
	}
	if *mode != "" {
		settings.Mode = *mode
	}
	if *iceURL != "" {
		// Override ICE servers with command line values
		iceServer := webrtc.ICEServer{
			URLs: []string{*iceURL},
		}
		if *iceUsername != "" {
			iceServer.Username = *iceUsername
		}
		if *iceCredential != "" {
			iceServer.Credential = *iceCredential
		}
		settings.ICEServers = []webrtc.ICEServer{iceServer}
	}

	// Set up logging
	if *verbose || getEnvBoolOrDefault("VERBOSE", false) {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Validate required parameters
	if settings.AgentID == "" {
		log.Fatal("agent-id is required (set AGENT_ID env var or use -agent-id flag)")
	}
	if settings.SignalingURL == "" {
		log.Fatal("signaling-url is required (set SIGNALING_URL env var or use -signaling-url flag)")
	}
	if !*listen && *peerID == "" {
		log.Fatal("peer-id is required for offerer mode, or use -listen for answerer mode")
	}

	// Create signaling client and agent core
	signaling := NewSignalingClient(*settings)
	core := NewAgentCore(*settings, signaling)

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logrus.Info("Received shutdown signal")
		cancel()
	}()

	// Start agent core
	if err := core.Start(); err != nil {
		log.Fatalf("Failed to start agent core: %v", err)
	}

	if *listen {
		// Answerer mode - listen for incoming connections
		if err := runAnswerer(ctx, core); err != nil {
			log.Fatalf("Answerer failed: %v", err)
		}
	} else {
		// Offerer mode - connect to specific peer
		if err := runOfferer(ctx, core, *peerID); err != nil {
			log.Fatalf("Offerer failed: %v", err)
		}
	}

	// Cleanup
	if err := core.Stop(); err != nil {
		logrus.WithError(err).Error("Error stopping agent core")
	}
}

// runOfferer runs in offerer mode (initiates connection)
func runOfferer(ctx context.Context, core *AgentCore, peerID string) error {
	logrus.WithField("peer_id", peerID).Info("Starting offerer mode")

	// Connect to peer
	if err := core.ConnectTo(peerID); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	// Wait for connection with timeout
	timeout := 30 * time.Second
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			session := core.GetSession(peerID)
			if session != nil && session.IsConnected() {
				logrus.WithField("peer_id", peerID).Info("Connected to peer")
				goto connected // Break out of the entire loop
			}

			if time.Since(startTime) > timeout {
				return fmt.Errorf("connection timeout after %v", timeout)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}

connected:

	// Start interactive chat session
	logrus.WithField("peer_id", peerID).Info("Starting interactive chat session")
	return runChatSession(ctx, core, peerID)
}

// runAnswerer runs in answerer mode (listens for connections)
func runAnswerer(ctx context.Context, core *AgentCore) error {
	logrus.Info("Starting answerer mode - listening for connections")

	// Wait for first connection
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			sessions := core.GetAllSessions()
			for peerID, session := range sessions {
				if session.IsConnected() {
					logrus.WithField("peer_id", peerID).Info("Peer connected, starting interactive chat session")
					return runChatSession(ctx, core, peerID)
				}
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runChatSession runs the interactive chat session
func runChatSession(ctx context.Context, core *AgentCore, peerID string) error {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Printf("🎉 CHAT SESSION STARTED WITH %s 🎉\n", peerID)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("💬 Type your messages and press Enter to send")
	fmt.Println("🚪 Type 'quit' to exit")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Print("You: ")

	// Get the session
	session := core.GetSession(peerID)
	if session == nil {
		return fmt.Errorf("session not found for peer %s", peerID)
	}

	// Set up custom message handler for better chat display
	session.Transport.OnMessage(func(data []byte) {
		message := string(data)
		fmt.Printf("\n[%s] %s\n", peerID, message)
		fmt.Print("You: ") // Prompt for next input
	})

	// Ensure sender is started (in case OnOpen event was missed)
	session.Transport.EnsureSenderStarted()

	// Set up stdin reader for sending messages
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				message := scanner.Text()
				if message == "quit" {
					fmt.Println("\n👋 Exiting chat session...")
					return
				}

				if message != "" {
					// Send message via transport
					if err := session.Transport.SendText(message); err != nil {
						fmt.Printf("❌ Failed to send message: %v\n", err)
						fmt.Print("You: ")
					} else {
						fmt.Printf("✅ Sent: %s\n", message)
						fmt.Print("You: ")
					}
				} else {
					fmt.Print("You: ")
				}
			}
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	fmt.Println("\n🔚 Chat session shutting down")
	return nil
}

// Example usage functions for different modes

// ExampleChatOfferer demonstrates chat offerer functionality
func ExampleChatOfferer() {
	// Load settings from .env file
	settings, err := LoadSettings("config.env")
	if err != nil {
		log.Fatalf("Failed to load settings: %v", err)
	}

	// Override specific values for this example
	settings.AgentID = "chat-offerer-001"
	settings.SignalingURL = "ws://localhost:8000/ws/chat-offerer-001"
	settings.Mode = "chat"

	signaling := NewSignalingClient(*settings)
	core := NewAgentCore(*settings, signaling)

	if err := core.Start(); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// Connect to peer
	if err := core.ConnectTo("chat-answerer-001"); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection
	time.Sleep(5 * time.Second)

	// Cleanup
	core.Stop()
}

// ExampleChatAnswerer demonstrates chat answerer functionality
func ExampleChatAnswerer() {
	// Load settings from .env file
	settings, err := LoadSettings("config.env")
	if err != nil {
		log.Fatalf("Failed to load settings: %v", err)
	}

	// Override specific values for this example
	settings.AgentID = "chat-answerer-001"
	settings.SignalingURL = "ws://localhost:8000/ws/chat-answerer-001"
	settings.Mode = "chat"

	signaling := NewSignalingClient(*settings)
	core := NewAgentCore(*settings, signaling)

	if err := core.Start(); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// Wait for incoming connection
	time.Sleep(30 * time.Second)

	// Cleanup
	core.Stop()
}

// ExampleJSONTransport demonstrates JSON transport functionality
func ExampleJSONTransport() {
	// Load settings from .env file
	settings, err := LoadSettings("config.env")
	if err != nil {
		log.Fatalf("Failed to load settings: %v", err)
	}

	// Override specific values for this example
	settings.AgentID = "json-agent-001"
	settings.SignalingURL = "ws://localhost:8000/ws/json-agent-001"
	settings.Mode = "json"

	signaling := NewSignalingClient(*settings)
	core := NewAgentCore(*settings, signaling)

	if err := core.Start(); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// Connect to peer
	if err := core.ConnectTo("json-agent-002"); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection
	time.Sleep(5 * time.Second)

	// Send JSON message
	session := core.GetSession("json-agent-002")
	if session != nil && session.IsConnected() {
		transport := session.Transport
		jsonData := map[string]interface{}{
			"type":      "message",
			"content":   "Hello from Go WebRTC!",
			"timestamp": time.Now().Unix(),
		}
		transport.SendJSON(jsonData)
	}

	// Cleanup
	core.Stop()
}

// ExampleBytesTransport demonstrates bytes transport functionality
func ExampleBytesTransport() {
	// Load settings from .env file
	settings, err := LoadSettings("config.env")
	if err != nil {
		log.Fatalf("Failed to load settings: %v", err)
	}

	// Override specific values for this example
	settings.AgentID = "bytes-agent-001"
	settings.SignalingURL = "ws://localhost:8000/ws/bytes-agent-001"
	settings.Mode = "bytes"

	signaling := NewSignalingClient(*settings)
	core := NewAgentCore(*settings, signaling)

	if err := core.Start(); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// Connect to peer
	if err := core.ConnectTo("bytes-agent-002"); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection
	time.Sleep(5 * time.Second)

	// Send binary data
	session := core.GetSession("bytes-agent-002")
	if session != nil && session.IsConnected() {
		transport := session.Transport
		binaryData := []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F} // "Hello" in binary
		transport.SendBytes(binaryData)
	}

	// Cleanup
	core.Stop()
}
