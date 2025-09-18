/*
================================================================================
Simple UDP Message Agent
================================================================================

A simplified version that only uses UDP sockets to send/receive text messages
between agents. No TUN interface required.

Usage:
  go run main.go -env=config.env -listen
  go run main.go -env=config.env -peer-id=agent-10-10-0-5

================================================================================
*/

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

// ============================================================================
// CONFIGURATION & TYPES
// ============================================================================

// AgentConfig holds configuration for the simple UDP agent
type AgentConfig struct {
	// Organization & Signaling
	OrgToken     string `json:"org_token"`
	SignalingURL string `json:"signaling_url"`

	// Local UDP
	UDPBindIP   string `json:"udp_bind_ip"`
	UDPBindPort string `json:"udp_bind_port"`

	// Agent identification
	AgentID string `json:"agent_id"`
}

// PeerEndpoint represents a peer's network endpoint
type PeerEndpoint struct {
	IP       string    `json:"ip"`
	Port     int       `json:"port"`
	AgentID  string    `json:"agent_id"`
	LastSeen time.Time `json:"last_seen"`
}

// Message represents a text message between agents
type Message struct {
	Type      string    `json:"type"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// SignalingMessage represents signaling server messages
type SignalingMessage struct {
	Type    string `json:"type"`
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

// SimpleAgent represents the main UDP agent
type SimpleAgent struct {
	config         *AgentConfig
	signaling      *SignalingClient
	peerEndpoints  map[string]*PeerEndpoint
	udpConn        *net.UDPConn
	publicEndpoint *net.UDPAddr
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	mu             sync.RWMutex
	logger         *log.Logger
}

// ============================================================================
// MAIN FUNCTION
// ============================================================================

func main() {
	// Command line flags
	var (
		envFile      = flag.String("env", "", "Path to .env file (optional)")
		agentID      = flag.String("agent-id", "", "Agent ID (overrides env)")
		signalingURL = flag.String("signaling-url", "", "Signaling server URL (overrides env)")
		peerID       = flag.String("peer-id", "", "Peer ID to connect to (for offerer mode)")
		listen       = flag.Bool("listen", false, "Listen for incoming connections (answerer mode)")
		help         = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	// Show help if requested
	if *help {
		printHelp()
		return
	}

	// Load configuration
	config, err := loadConfig(*envFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override with command line flags
	if *agentID != "" {
		config.AgentID = *agentID
	}
	if *signalingURL != "" {
		config.SignalingURL = *signalingURL
	}

	// Validate required parameters
	if config.SignalingURL == "" {
		log.Fatal("signaling-url is required (set SIGNALING_URL env var or use -signaling-url flag)")
	}
	if config.AgentID == "" {
		config.AgentID = fmt.Sprintf("agent-%d", time.Now().Unix())
	}
	if !*listen && *peerID == "" {
		log.Fatal("peer-id is required for offerer mode, or use -listen for answerer mode")
	}

	// Create agent
	agent, err := NewSimpleAgent(config)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Start agent
	if err := agent.Start(); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}

	if *listen {
		// Answerer mode - listen for incoming connections
		if err := runAnswerer(ctx, agent); err != nil {
			log.Fatalf("Answerer failed: %v", err)
		}
	} else {
		// Offerer mode - connect to specific peer
		if err := runOfferer(ctx, agent, *peerID); err != nil {
			log.Fatalf("Offerer failed: %v", err)
		}
	}

	// Cleanup
	if err := agent.Stop(); err != nil {
		log.Printf("Error stopping agent: %v", err)
	}
}

// ============================================================================
// CONFIGURATION LOADING
// ============================================================================

func loadConfig(envFile string) (*AgentConfig, error) {
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

	config := &AgentConfig{
		// Organization & Signaling
		OrgToken:     getEnvOrDefault("ORG_TOKEN", ""),
		SignalingURL: getEnvOrDefault("SIGNALING_URL", ""),

		// Local UDP
		UDPBindIP:   getEnvOrDefault("UDP_BIND_IP", "0.0.0.0"),
		UDPBindPort: getEnvOrDefault("UDP_BIND_PORT", "0"),

		// Agent identification
		AgentID: getEnvOrDefault("AGENT_ID", ""),
	}

	// Validate required fields
	if config.SignalingURL == "" {
		return nil, fmt.Errorf("SIGNALING_URL is required")
	}
	if !strings.HasPrefix(config.SignalingURL, "ws://") && !strings.HasPrefix(config.SignalingURL, "wss://") {
		return nil, fmt.Errorf("SIGNALING_URL must start with ws:// or wss://")
	}

	log.Printf("Loaded configuration: signaling=%s, agent_id=%s",
		config.SignalingURL, config.AgentID)

	return config, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func printHelp() {
	fmt.Println("=== Simple UDP Message Agent ===")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  # Answerer mode (listen for messages)")
	fmt.Println("  go run main.go -env=config.env -listen")
	fmt.Println()
	fmt.Println("  # Offerer mode (connect to specific peer)")
	fmt.Println("  go run main.go -env=config.env -peer-id=agent-123")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  SIGNALING_URL         - WebSocket signaling server URL (required)")
	fmt.Println("  AGENT_ID              - Agent identifier (optional)")
	fmt.Println("  UDP_BIND_IP           - UDP bind IP (default: 0.0.0.0)")
	fmt.Println("  UDP_BIND_PORT         - UDP bind port (default: 0)")
}

// ============================================================================
// AGENT IMPLEMENTATION
// ============================================================================

// NewSimpleAgent creates a new simple UDP agent
func NewSimpleAgent(config *AgentConfig) (*SimpleAgent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	agent := &SimpleAgent{
		config:         config,
		peerEndpoints:  make(map[string]*PeerEndpoint),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		logger:         log.New(os.Stdout, "[AGENT] ", log.LstdFlags),
	}

	// Create signaling client
	agent.signaling = NewSignalingClient(config)

	return agent, nil
}

// Start begins the agent
func (a *SimpleAgent) Start() error {
	a.logger.Println("Starting simple UDP agent")

	// Create UDP socket
	if err := a.createUDPSocket(); err != nil {
		return fmt.Errorf("failed to create UDP socket: %w", err)
	}

	// Connect to signaling server
	if err := a.signaling.Connect(); err != nil {
		return fmt.Errorf("failed to connect to signaling: %w", err)
	}

	// Register with signaling server
	if err := a.registerWithSignaling(); err != nil {
		return fmt.Errorf("failed to register with signaling: %w", err)
	}

	// Start background tasks
	go a.udpMessageReceiver() // UDP message processing
	go a.signalingLoop()      // Signaling message processing

	a.logger.Println("Agent started successfully")
	return nil
}

// Stop stops the agent
func (a *SimpleAgent) Stop() error {
	a.logger.Println("Stopping agent")

	// Signal shutdown
	a.shutdownCancel()

	// Close UDP socket
	if a.udpConn != nil {
		a.udpConn.Close()
	}

	// Close signaling connection
	if a.signaling != nil {
		a.signaling.Close()
	}

	a.logger.Println("Agent stopped")
	return nil
}

// createUDPSocket creates and binds the UDP socket
func (a *SimpleAgent) createUDPSocket() error {
	// Parse bind address
	bindAddr := fmt.Sprintf("%s:%s", a.config.UDPBindIP, a.config.UDPBindPort)
	udpAddr, err := net.ResolveUDPAddr("udp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	// Create UDP socket
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("failed to create UDP socket: %w", err)
	}

	a.udpConn = conn
	a.logger.Printf("Created UDP socket: %s", conn.LocalAddr())

	return nil
}

// registerWithSignaling registers our endpoint with the signaling server
func (a *SimpleAgent) registerWithSignaling() error {
	// Determine endpoint to register
	localAddr := a.udpConn.LocalAddr().(*net.UDPAddr)
	ip := localAddr.IP.String()
	port := localAddr.Port

	// Send register message
	msg := SignalingMessage{
		Type:    "register",
		IP:      ip,
		Port:    port,
		AgentID: a.config.AgentID,
	}

	if err := a.signaling.Send(msg); err != nil {
		return fmt.Errorf("failed to send register message: %w", err)
	}

	a.logger.Printf("Registered with signaling: %s:%d (AgentID: %s)", ip, port, a.config.AgentID)
	return nil
}

// ============================================================================
// UDP MESSAGE HANDLING
// ============================================================================

// udpMessageReceiver receives and processes UDP messages
func (a *SimpleAgent) udpMessageReceiver() {
	buffer := make([]byte, 1024)

	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		default:
			// Read from UDP socket
			n, addr, err := a.udpConn.ReadFromUDP(buffer)
			if err != nil {
				if a.shutdownCtx.Err() != nil {
					return
				}
				a.logger.Printf("Error reading from UDP: %v", err)
				continue
			}

			// Parse message
			var msg Message
			if err := json.Unmarshal(buffer[:n], &msg); err != nil {
				a.logger.Printf("Error parsing message from %s: %v", addr, err)
				continue
			}

			// Handle message
			a.handleMessage(msg, addr)
		}
	}
}

// handleMessage processes incoming messages
func (a *SimpleAgent) handleMessage(msg Message, addr *net.UDPAddr) {
	a.logger.Printf("Received message from %s: %s", addr, msg.Content)

	// Update peer endpoint
	a.mu.Lock()
	a.peerEndpoints[msg.From] = &PeerEndpoint{
		IP:       addr.IP.String(),
		Port:     addr.Port,
		AgentID:  msg.From,
		LastSeen: time.Now(),
	}
	a.mu.Unlock()

	// Display message
	fmt.Printf("\n💬 Message from %s: %s\n", msg.From, msg.Content)
	fmt.Print("You: ")
}

// sendMessage sends a message to a peer
func (a *SimpleAgent) sendMessage(to, content string) error {
	// Look up peer endpoint
	a.mu.RLock()
	peer, exists := a.peerEndpoints[to]
	a.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no peer found with ID: %s", to)
	}

	// Create message
	msg := Message{
		Type:      "text",
		From:      a.config.AgentID,
		To:        to,
		Content:   content,
		Timestamp: time.Now(),
	}

	// Serialize message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Send via UDP
	peerAddr := &net.UDPAddr{
		IP:   net.ParseIP(peer.IP),
		Port: peer.Port,
	}

	_, err = a.udpConn.WriteToUDP(data, peerAddr)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	a.logger.Printf("Sent message to %s: %s", to, content)
	return nil
}

// ============================================================================
// SIGNALING CLIENT
// ============================================================================

// SignalingClient handles WebSocket signaling communication
type SignalingClient struct {
	config    *AgentConfig
	conn      *websocket.Conn
	connected bool
	mu        sync.RWMutex
	logger    *log.Logger
}

// NewSignalingClient creates a new signaling client
func NewSignalingClient(config *AgentConfig) *SignalingClient {
	return &SignalingClient{
		config: config,
		logger: log.New(os.Stdout, "[SIGNALING] ", log.LstdFlags),
	}
}

// Connect establishes connection to signaling server
func (sc *SignalingClient) Connect() error {
	// Construct WebSocket URL with client ID
	wsURL := strings.TrimSuffix(sc.config.SignalingURL, "/") + "/ws/" + sc.config.AgentID
	sc.logger.Printf("Connecting to signaling server: %s", wsURL)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to signaling server: %w", err)
	}

	sc.mu.Lock()
	sc.conn = conn
	sc.connected = true
	sc.mu.Unlock()

	sc.logger.Println("Connected to signaling server")
	return nil
}

// Send sends a message to signaling server
func (sc *SignalingClient) Send(msg SignalingMessage) error {
	sc.mu.RLock()
	conn := sc.conn
	connected := sc.connected
	sc.mu.RUnlock()

	if !connected || conn == nil {
		return fmt.Errorf("not connected to signaling server")
	}

	// Marshal and send message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	sc.logger.Printf("Sent message: %s", msg.Type)
	return nil
}

// Receive receives a message from signaling server
func (sc *SignalingClient) Receive(ctx context.Context) (SignalingMessage, error) {
	sc.mu.RLock()
	conn := sc.conn
	sc.mu.RUnlock()

	if conn == nil {
		return SignalingMessage{}, fmt.Errorf("not connected")
	}

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(time.Minute))

	// Read message
	_, data, err := conn.ReadMessage()
	if err != nil {
		return SignalingMessage{}, err
	}

	// Parse message
	var msg SignalingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return SignalingMessage{}, fmt.Errorf("failed to parse message: %w", err)
	}

	return msg, nil
}

// Close closes signaling connection
func (sc *SignalingClient) Close() error {
	sc.logger.Println("Closing signaling client")

	sc.mu.Lock()
	if sc.conn != nil {
		sc.conn.Close()
		sc.conn = nil
	}
	sc.connected = false
	sc.mu.Unlock()

	sc.logger.Println("Signaling client closed")
	return nil
}

// ============================================================================
// BACKGROUND TASKS
// ============================================================================

// signalingLoop processes incoming signaling messages
func (a *SimpleAgent) signalingLoop() {
	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		default:
			// Process messages with timeout
			ctx, cancel := context.WithTimeout(a.shutdownCtx, time.Second)
			message, err := a.signaling.Receive(ctx)
			cancel()

			if err != nil {
				if err == context.DeadlineExceeded {
					continue // Timeout is normal, continue loop
				}
				a.logger.Printf("Error receiving signaling message: %v", err)
				time.Sleep(time.Second)
				continue
			}

			if err := a.handleSignalingMessage(message); err != nil {
				a.logger.Printf("Error handling signaling message: %v", err)
			}
		}
	}
}

// handleSignalingMessage processes incoming signaling messages
func (a *SimpleAgent) handleSignalingMessage(msg SignalingMessage) error {
	a.logger.Printf("Received signaling message: %s", msg.Type)

	switch msg.Type {
	case "peer":
		return a.handlePeerMessage(msg)
	case "registered":
		a.logger.Println("Successfully registered with signaling server")
	case "error":
		a.logger.Printf("Signaling error: %s", msg.Type)
	default:
		a.logger.Printf("Unknown signaling message type: %s", msg.Type)
	}

	return nil
}

// handlePeerMessage handles peer discovery messages
func (a *SimpleAgent) handlePeerMessage(msg SignalingMessage) error {
	if msg.IP == "" || msg.Port == 0 {
		return fmt.Errorf("invalid peer message: missing IP or port")
	}

	// Use agent_id if available, otherwise use IP:Port as fallback
	agentID := msg.AgentID
	if agentID == "" {
		agentID = fmt.Sprintf("%s:%d", msg.IP, msg.Port)
	}

	// Create peer endpoint
	peer := &PeerEndpoint{
		IP:       msg.IP,
		Port:     msg.Port,
		AgentID:  agentID,
		LastSeen: time.Now(),
	}

	a.mu.Lock()
	a.peerEndpoints[agentID] = peer
	a.mu.Unlock()

	a.logger.Printf("Added peer: %s -> %s:%d", agentID, msg.IP, msg.Port)
	return nil
}

// ============================================================================
// MODE RUNNERS
// ============================================================================

// runAnswerer runs in answerer mode (listens for connections)
func runAnswerer(ctx context.Context, agent *SimpleAgent) error {
	agent.logger.Println("Starting answerer mode - listening for messages")

	// Wait for first connection
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			agent.mu.RLock()
			peersCount := len(agent.peerEndpoints)
			agent.mu.RUnlock()

			if peersCount > 0 {
				agent.logger.Printf("Peer connected, starting interactive session")
				return runInteractiveSession(ctx, agent)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runOfferer runs in offerer mode (connects to specific peer)
func runOfferer(ctx context.Context, agent *SimpleAgent, peerID string) error {
	agent.logger.Printf("Starting offerer mode - connecting to %s", peerID)

	// Wait for connection with timeout
	timeout := 30 * time.Second
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			agent.mu.RLock()
			peersCount := len(agent.peerEndpoints)
			agent.mu.RUnlock()

			if peersCount > 0 {
				agent.logger.Printf("Connected to peer")
				return runInteractiveSession(ctx, agent)
			}

			if time.Since(startTime) > timeout {
				return fmt.Errorf("connection timeout after %v", timeout)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runInteractiveSession runs the interactive session
func runInteractiveSession(ctx context.Context, agent *SimpleAgent) error {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("🎉 UDP MESSAGE SESSION STARTED 🎉")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("💬 Type your messages and press Enter to send")
	fmt.Println("🚪 Type 'quit' to exit")
	fmt.Println("📋 Type 'list' to see connected peers")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Print("You: ")

	// Set up stdin reader for sending messages
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				line := strings.TrimSpace(scanner.Text())
				if line == "quit" {
					fmt.Println("\n👋 Exiting session...")
					return
				}

				if line == "list" {
					// List connected peers
					agent.mu.RLock()
					peers := make([]string, 0, len(agent.peerEndpoints))
					for _, peer := range agent.peerEndpoints {
						peers = append(peers, peer.AgentID)
					}
					agent.mu.RUnlock()

					if len(peers) == 0 {
						fmt.Println("No peers connected")
					} else {
						fmt.Printf("Connected peers: %s\n", strings.Join(peers, ", "))
					}
				} else if line != "" {
					// Send message to first available peer
					agent.mu.RLock()
					var targetPeer string
					for _, peer := range agent.peerEndpoints {
						targetPeer = peer.AgentID
						break
					}
					agent.mu.RUnlock()

					if targetPeer == "" {
						fmt.Println("❌ No peers available to send message to")
					} else {
						if err := agent.sendMessage(targetPeer, line); err != nil {
							fmt.Printf("❌ Failed to send message: %v\n", err)
						} else {
							fmt.Printf("✅ Message sent to %s\n", targetPeer)
						}
					}
				}

				fmt.Print("You: ")
			}
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	fmt.Println("\n🔚 Session shutting down")
	return nil
}
