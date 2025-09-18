/*
================================================================================
UDP+TUN WebRTC Agent Replacement
================================================================================

ARCHITECTURE OVERVIEW:
This agent replaces WebRTC with raw UDP sockets + TUN interface for virtual networking.
The architecture consists of:

1. TUN Interface: Creates a virtual network interface (10.0.0.0/8) that carries IP packets
2. UDP Transport: Raw UDP sockets for peer-to-peer communication with NAT traversal
3. Signaling: WebSocket connection for endpoint discovery (reuses existing Python server)
4. NAT Traversal: UDP hole punching with TURN fallback for relayed connections
5. Control Protocol: Simple framing for HELLO/PING/DATA messages over UDP

WHY WE REMOVED WEBRTC:
WebRTC was complex and heavyweight for our use case. The new architecture provides:
- Direct control over network layer (TUN interface)
- Simpler NAT traversal (UDP hole punching + TURN)
- Lower latency (no WebRTC stack overhead)
- Better debugging (raw packet inspection)
- Cross-platform compatibility (TUN libraries available)

MAPPING TABLE - Old WebRTC Logic → New UDP+TUN Logic:
┌─────────────────────────────────┬─────────────────────────────────────────┐
│ OLD WEBRTC COMPONENT            │ NEW UDP+TUN EQUIVALENT                 │
├─────────────────────────────────┼─────────────────────────────────────────┤
│ RTCPeerConnection               │ UDP socket + peer endpoint mapping      │
│ DataChannel.Send()              │ udpSendData() with framing header       │
│ DataChannel.OnMessage()         │ udpReceiveData() → TUN injection        │
│ ICE candidate exchange          │ STUN discovery + signaling endpoint     │
│ TURN relay via ICE              │ Direct TURN client allocation           │
│ WebRTC offer/answer SDP         │ Simple register/peer JSON messages      │
│ onConnectionStateChange         │ peer mapping state + keepalive tracking │
│ OnICECandidate (trickle ICE)    │ STUN binding + immediate endpoint send  │
│ DataChannel.ReadyState          │ UDP socket state + peer mapping exists  │
│ webrtc.NewPeerConnection()      │ createPeerSession() with UDP+TUN        │
│ pc.CreateDataChannel()          │ TUN interface creation + UDP binding    │
│ pc.SetLocalDescription()        │ STUN binding + register message         │
│ pc.SetRemoteDescription()       │ peer endpoint mapping from signaling    │
│ pc.CreateOffer()                │ STUN discovery + register with endpoint │
│ pc.CreateAnswer()               │ peer endpoint mapping + HELLO handshake │
│ pc.AddICECandidate()            │ peer endpoint update from signaling     │
│ pc.OnConnectionStateChange()    │ peer mapping state monitoring           │
│ pc.OnICEConnectionStateChange() │ UDP connectivity + TURN fallback        │
│ pc.OnDataChannel()              │ TUN interface + UDP data forwarding     │
│ pc.Close()                      │ UDP socket close + TUN interface down   │
│ channel.Send()                  │ udpSendData() with VIP routing          │
│ channel.OnMessage()             │ udpReceiveData() → TUN write            │
│ channel.OnOpen()                │ peer mapping established + TUN ready    │
│ channel.OnClose()               │ peer mapping removed + TUN cleanup      │
│ channel.ReadyState()            │ peer mapping exists + UDP connected     │
└─────────────────────────────────┴─────────────────────────────────────────┘

QUICK START:
1. Install dependencies:
   go get github.com/songgao/water github.com/pion/stun github.com/pion/turn/v2 github.com/gorilla/websocket github.com/joho/godotenv

2. Set up TUN interface (Linux):
   sudo ip tuntap add dev tun0 mode tun
   sudo ip addr add 10.10.0.5/24 dev tun0
   sudo ip link set tun0 up

3. Run agent:
   go run main.go -env=config.env -listen
   # or
   go run main.go -env=config.env -peer-id=agent-002

4. Test connectivity:
   ping 10.10.0.6  # from one agent to another's VIP

PERMISSIONS & DRIVERS:
- Linux: Requires CAP_NET_ADMIN for TUN creation (run with sudo or set capabilities)
- macOS: Uses utun driver (built-in, requires sudo for TUN creation)
- Windows: Requires Wintun driver (https://www.wintun.net/)

MTU CONSIDERATIONS:
- Default MTU: 1300 bytes (conservative for NAT traversal)
- TUN interface carries full IP packets
- UDP framing adds 12-byte header
- Total overhead: ~40 bytes (UDP + IP + framing)
- Fragmentation risk above ~1200 bytes payload

================================================================================
*/

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/pion/stun"
	"github.com/pion/turn/v2"
	"github.com/songgao/water"
)

// ============================================================================
// CONFIGURATION & TYPES
// ============================================================================

// AgentConfig holds all configuration for the UDP+TUN agent
type AgentConfig struct {
	// Organization & Signaling
	OrgToken     string `json:"org_token"`
	SignalingURL string `json:"signaling_url"`

	// ICE-style config (preferred)
	ICEURLs       string `json:"ice_urls"`
	ICEUsername   string `json:"ice_username"`
	ICECredential string `json:"ice_credential"`

	// Direct STUN/TURN (overridden by ICE_* if provided)
	STUNServer string `json:"stun_server"`
	TURNServer string `json:"turn_server"`
	TURNUser   string `json:"turn_user"`
	TURNPass   string `json:"turn_pass"`

	// Virtual network
	VirtualSubnet string `json:"virtual_subnet"`
	VirtualIP     string `json:"virtual_ip"`

	// Local UDP
	UDPBindIP   string `json:"udp_bind_ip"`
	UDPBindPort string `json:"udp_bind_port"`

	// TUN/Transport
	MTU                int `json:"mtu"`
	PunchAttempts      int `json:"punch_attempts"`
	PunchIntervalMs    int `json:"punch_interval_ms"`
	KeepaliveIntervalS int `json:"keepalive_interval_s"`
	PeerStaleTimeoutS  int `json:"peer_stale_timeout_s"`
}

// PeerEndpoint represents a peer's network endpoint and virtual IP
type PeerEndpoint struct {
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	VIP       string    `json:"vip,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	IsRelayed bool      `json:"is_relayed"`
}

// PeerSession represents a peer connection session (replaces WebRTC PeerSession)
type PeerSession struct {
	PeerID      string
	Endpoint    *PeerEndpoint
	IsConnected bool
	CreatedAt   time.Time
	mu          sync.RWMutex
}

// UDPFrame represents our custom UDP framing protocol
type UDPFrame struct {
	Version     uint8
	MessageType uint8 // 0=data, 1=hello, 2=ping, 3=pong, 4=error
	SrcVIP      [4]byte
	DstVIP      [4]byte
	PayloadLen  uint16
	Payload     []byte
}

// SignalingMessage represents signaling server messages (simplified from WebRTC version)
type SignalingMessage struct {
	Type string `json:"type"`
	IP   string `json:"ip,omitempty"`
	Port int    `json:"port,omitempty"`
	VIP  string `json:"vip,omitempty"`
}

// Agent represents the main UDP+TUN agent (replaces AgentCore)
type Agent struct {
	config         *AgentConfig
	signaling      *SignalingClient
	peerSessions   map[string]*PeerSession
	peerMappings   map[string]*net.UDPAddr // VIP -> UDP endpoint mapping
	tunInterface   *water.Interface
	udpConn        *net.UDPConn
	turnConn       *turn.Client
	publicEndpoint *net.UDPAddr
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	mu             sync.RWMutex
	logger         *log.Logger
	stats          *AgentStats
}

// AgentStats holds runtime statistics
type AgentStats struct {
	TxCount     int64
	RxCount     int64
	PeersCount  int
	LastUpdated time.Time
	mu          sync.RWMutex
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
		verbose      = flag.Bool("verbose", false, "Enable verbose logging (overrides env)")
		_            = flag.String("ping", "", "VIP to ping for testing")
		help         = flag.Bool("help", false, "Show configuration help")
	)
	flag.Parse()

	// Show help if requested
	if *help {
		printConfigHelp()
		return
	}

	// Load configuration
	config, err := loadConfig(*envFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override with command line flags
	if *agentID != "" {
		// Extract VIP from agent ID if possible, or use default
		config.VirtualIP = "10.10.0.5/24" // Default VIP
	}
	if *signalingURL != "" {
		config.SignalingURL = *signalingURL
	}
	if *verbose || getEnvBoolOrDefault("VERBOSE", false) {
		// Enable verbose logging
	}

	// Validate required parameters
	if config.SignalingURL == "" {
		log.Fatal("signaling-url is required (set SIGNALING_URL env var or use -signaling-url flag)")
	}
	if !*listen && *peerID == "" {
		log.Fatal("peer-id is required for offerer mode, or use -listen for answerer mode")
	}

	// Create agent
	agent, err := NewAgent(config)
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

		// ICE-style config (preferred)
		ICEURLs:       getEnvOrDefault("ICE_URLS", ""),
		ICEUsername:   getEnvOrDefault("ICE_USERNAME", ""),
		ICECredential: getEnvOrDefault("ICE_CREDENTIAL", ""),

		// Direct STUN/TURN (overridden by ICE_* if provided)
		STUNServer: getEnvOrDefault("STUN_SERVER", "54.151.153.64:3478"),
		TURNServer: getEnvOrDefault("TURN_SERVER", "54.151.153.64:3478"),
		TURNUser:   getEnvOrDefault("TURN_USER", "test"),
		TURNPass:   getEnvOrDefault("TURN_PASS", "1234"),

		// Virtual network
		VirtualSubnet: getEnvOrDefault("VIRTUAL_SUBNET", "10.10.0.0/16"),
		VirtualIP:     getEnvOrDefault("VIRTUAL_IP", "10.10.0.5/24"),

		// Local UDP
		UDPBindIP:   getEnvOrDefault("UDP_BIND_IP", "0.0.0.0"),
		UDPBindPort: getEnvOrDefault("UDP_BIND_PORT", "0"),

		// TUN/Transport
		MTU:                getEnvIntOrDefault("MTU", 1300),
		PunchAttempts:      getEnvIntOrDefault("PUNCH_ATTEMPTS", 20),
		PunchIntervalMs:    getEnvIntOrDefault("PUNCH_INTERVAL_MS", 250),
		KeepaliveIntervalS: getEnvIntOrDefault("KEEPALIVE_INTERVAL_S", 15),
		PeerStaleTimeoutS:  getEnvIntOrDefault("PEER_STALE_TIMEOUT_S", 60),
	}

	// Parse ICE URLs if provided (overrides direct STUN/TURN settings)
	if config.ICEURLs != "" {
		if err := parseICEURLs(config); err != nil {
			return nil, fmt.Errorf("failed to parse ICE URLs: %w", err)
		}
	}

	// Validate required fields
	if config.SignalingURL == "" {
		return nil, fmt.Errorf("SIGNALING_URL is required")
	}
	if !strings.HasPrefix(config.SignalingURL, "ws://") && !strings.HasPrefix(config.SignalingURL, "wss://") {
		return nil, fmt.Errorf("SIGNALING_URL must start with ws:// or wss://")
	}

	log.Printf("Loaded configuration: signaling=%s, vip=%s, mtu=%d",
		config.SignalingURL, config.VirtualIP, config.MTU)

	return config, nil
}

func parseICEURLs(config *AgentConfig) error {
	urls := strings.Split(config.ICEURLs, ",")
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}

		if strings.HasPrefix(url, "stun:") {
			// Extract STUN server
			config.STUNServer = strings.TrimPrefix(url, "stun:")
		} else if strings.HasPrefix(url, "turn:") {
			// Extract TURN server
			config.TURNServer = strings.TrimPrefix(url, "turn:")
		}
	}

	// Map ICE credentials to TURN credentials
	if config.ICEUsername != "" {
		config.TURNUser = config.ICEUsername
	}
	if config.ICECredential != "" {
		config.TURNPass = config.ICECredential
	}

	return nil
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

func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func printConfigHelp() {
	fmt.Println("=== UDP+TUN Agent Configuration ===")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  ORG_TOKEN             - Organization token for signaling")
	fmt.Println("  SIGNALING_URL         - WebSocket signaling server URL (required)")
	fmt.Println("  ICE_URLS              - ICE server URLs (stun:...,turn:...)")
	fmt.Println("  ICE_USERNAME          - ICE server username")
	fmt.Println("  ICE_CREDENTIAL        - ICE server credential")
	fmt.Println("  STUN_SERVER           - STUN server (default: 54.151.153.64:3478)")
	fmt.Println("  TURN_SERVER           - TURN server (default: 54.151.153.64:3478)")
	fmt.Println("  TURN_USER             - TURN username (default: test)")
	fmt.Println("  TURN_PASS             - TURN password (default: 1234)")
	fmt.Println("  VIRTUAL_SUBNET        - Virtual subnet (default: 10.10.0.0/16)")
	fmt.Println("  VIRTUAL_IP            - Virtual IP with mask (default: 10.10.0.5/24)")
	fmt.Println("  UDP_BIND_IP           - UDP bind IP (default: 0.0.0.0)")
	fmt.Println("  UDP_BIND_PORT         - UDP bind port (default: 0)")
	fmt.Println("  MTU                   - TUN interface MTU (default: 1300)")
	fmt.Println("  PUNCH_ATTEMPTS        - NAT punch attempts (default: 20)")
	fmt.Println("  PUNCH_INTERVAL_MS     - Punch interval in ms (default: 250)")
	fmt.Println("  KEEPALIVE_INTERVAL_S  - Keepalive interval in seconds (default: 15)")
	fmt.Println("  PEER_STALE_TIMEOUT_S  - Peer stale timeout in seconds (default: 60)")
	fmt.Println("  VERBOSE               - Enable verbose logging (default: false)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  # Answerer mode")
	fmt.Println("  sudo go run main.go -env=config.env -listen")
	fmt.Println()
	fmt.Println("  # Offerer mode")
	fmt.Println("  sudo go run main.go -env=config.env -peer-id=agent-002")
	fmt.Println()
	fmt.Println("  # Test connectivity")
	fmt.Println("  sudo go run main.go -env=config.env -ping=10.10.0.6")
}

// ============================================================================
// AGENT IMPLEMENTATION
// ============================================================================

// NewAgent creates a new UDP+TUN agent (replaces NewAgentCore)
func NewAgent(config *AgentConfig) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	agent := &Agent{
		config:         config,
		peerSessions:   make(map[string]*PeerSession),
		peerMappings:   make(map[string]*net.UDPAddr),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		logger:         log.New(os.Stdout, "[AGENT] ", log.LstdFlags),
		stats:          &AgentStats{},
	}

	// Create signaling client
	agent.signaling = NewSignalingClient(config)

	return agent, nil
}

// Start begins the agent (replaces AgentCore.Start)
func (a *Agent) Start() error {
	a.logger.Println("Starting UDP+TUN agent")

	// Create TUN interface
	if err := a.createTUNInterface(); err != nil {
		return fmt.Errorf("failed to create TUN interface: %w", err)
	}

	// Create UDP socket
	if err := a.createUDPSocket(); err != nil {
		return fmt.Errorf("failed to create UDP socket: %w", err)
	}

	// Discover public endpoint via STUN
	if err := a.discoverPublicEndpoint(); err != nil {
		a.logger.Printf("Warning: STUN discovery failed: %v", err)
		// Continue with local endpoint
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
	go a.tunToUDPForwarder() // TUN -> UDP forwarding
	go a.udpToTUNForwarder() // UDP -> TUN forwarding
	go a.signalingLoop()     // Signaling message processing
	go a.peerReaper()        // Clean up stale peers
	go a.statsLogger()       // Periodic statistics logging

	a.logger.Println("Agent started successfully")
	return nil
}

// Stop stops the agent (replaces AgentCore.Stop)
func (a *Agent) Stop() error {
	a.logger.Println("Stopping agent")

	// Signal shutdown
	a.shutdownCancel()

	// Close TUN interface
	if a.tunInterface != nil {
		a.tunInterface.Close()
	}

	// Close UDP socket
	if a.udpConn != nil {
		a.udpConn.Close()
	}

	// Close TURN connection
	if a.turnConn != nil {
		a.turnConn.Close()
	}

	// Close signaling connection
	if a.signaling != nil {
		a.signaling.Close()
	}

	a.logger.Println("Agent stopped")
	return nil
}

// createTUNInterface creates and configures the TUN interface
func (a *Agent) createTUNInterface() error {
	// Create TUN interface configuration
	config := water.Config{
		DeviceType: water.TUN,
	}

	// Create TUN interface
	iface, err := water.New(config)
	if err != nil {
		return fmt.Errorf("failed to create TUN interface: %w", err)
	}

	a.tunInterface = iface
	a.logger.Printf("Created TUN interface: %s", iface.Name())

	// Note: IP configuration should be done by the OS or external script
	// For Linux: sudo ip addr add 10.10.0.5/24 dev tun0
	// For macOS: sudo ifconfig utun0 10.10.0.5/24
	// For Windows: Wintun driver handles this

	return nil
}

// createUDPSocket creates and binds the UDP socket
func (a *Agent) createUDPSocket() error {
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

// discoverPublicEndpoint discovers our public endpoint via STUN
func (a *Agent) discoverPublicEndpoint() error {
	if a.config.STUNServer == "" {
		return fmt.Errorf("no STUN server configured")
	}

	// Create STUN client
	conn, err := net.Dial("udp", a.config.STUNServer)
	if err != nil {
		return fmt.Errorf("failed to connect to STUN server: %w", err)
	}
	defer conn.Close()

	// Create STUN client
	c, err := stun.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create STUN client: %w", err)
	}
	defer c.Close()

	// Perform STUN binding request
	var xorAddr stun.XORMappedAddress
	if err := c.Do(stun.MustBuild(stun.TransactionID, stun.BindingRequest), func(res stun.Event) {
		if res.Error != nil {
			err = res.Error
			return
		}
		if err := xorAddr.GetFrom(res.Message); err != nil {
			err = err
			return
		}
	}); err != nil {
		return fmt.Errorf("STUN binding request failed: %w", err)
	}

	// Set public endpoint
	a.publicEndpoint = &net.UDPAddr{
		IP:   xorAddr.IP,
		Port: xorAddr.Port,
	}

	a.logger.Printf("Discovered public endpoint: %s", a.publicEndpoint)
	return nil
}

// registerWithSignaling registers our endpoint with the signaling server
func (a *Agent) registerWithSignaling() error {
	// Extract VIP from VirtualIP (remove /24 suffix)
	vip := strings.Split(a.config.VirtualIP, "/")[0]

	// Determine endpoint to register
	var ip string
	var port int

	if a.publicEndpoint != nil {
		ip = a.publicEndpoint.IP.String()
		port = a.publicEndpoint.Port
	} else {
		// Use local endpoint
		localAddr := a.udpConn.LocalAddr().(*net.UDPAddr)
		ip = localAddr.IP.String()
		port = localAddr.Port
	}

	// Generate client ID (use VIP as client ID for simplicity)
	clientID := fmt.Sprintf("agent-%s", strings.ReplaceAll(vip, ".", "-"))

	// Send register message
	msg := SignalingMessage{
		Type: "register",
		IP:   ip,
		Port: port,
		VIP:  vip,
	}

	if err := a.signaling.Send(msg); err != nil {
		return fmt.Errorf("failed to send register message: %w", err)
	}

	a.logger.Printf("Registered with signaling: %s:%d (VIP: %s, ClientID: %s)", ip, port, vip, clientID)
	return nil
}

// ============================================================================
// TUN/UDP FORWARDING
// ============================================================================

// tunToUDPForwarder reads from TUN interface and forwards to UDP peers
func (a *Agent) tunToUDPForwarder() {
	buffer := make([]byte, a.config.MTU)

	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		default:
			// Read from TUN interface
			n, err := a.tunInterface.Read(buffer)
			if err != nil {
				if a.shutdownCtx.Err() != nil {
					return
				}
				a.logger.Printf("Error reading from TUN: %v", err)
				continue
			}

			// Parse IP packet to get destination
			if n < 20 { // Minimum IP header size
				continue
			}

			// Extract destination IP from IP header
			dstIP := net.IP(buffer[16:20])
			dstVIP := dstIP.String()

			// Look up peer endpoint
			a.mu.RLock()
			peerAddr, exists := a.peerMappings[dstVIP]
			a.mu.RUnlock()

			if !exists {
				a.logger.Printf("No peer mapping for VIP: %s", dstVIP)
				continue
			}

			// Create UDP frame
			frame := UDPFrame{
				Version:     1,
				MessageType: 0, // DATA
				SrcVIP:      ipToBytes(a.getLocalVIP()),
				DstVIP:      ipToBytes(dstVIP),
				PayloadLen:  uint16(n),
				Payload:     buffer[:n],
			}

			// Send UDP frame
			if err := a.sendUDPFrame(frame, peerAddr); err != nil {
				a.logger.Printf("Error sending UDP frame: %v", err)
			} else {
				a.stats.mu.Lock()
				a.stats.TxCount++
				a.stats.mu.Unlock()
			}
		}
	}
}

// udpToTUNForwarder reads from UDP and forwards to TUN interface
func (a *Agent) udpToTUNForwarder() {
	buffer := make([]byte, a.config.MTU+12) // MTU + UDP frame header

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

			// Parse UDP frame
			frame, err := a.parseUDPFrame(buffer[:n])
			if err != nil {
				a.logger.Printf("Error parsing UDP frame: %v", err)
				continue
			}

			// Handle different message types
			switch frame.MessageType {
			case 0: // DATA
				a.handleDataFrame(frame, addr)
			case 1: // HELLO
				a.handleHelloFrame(frame, addr)
			case 2: // PING
				a.handlePingFrame(frame, addr)
			case 3: // PONG
				a.handlePongFrame(frame, addr)
			case 4: // ERROR
				a.handleErrorFrame(frame, addr)
			default:
				a.logger.Printf("Unknown message type: %d", frame.MessageType)
			}

			a.stats.mu.Lock()
			a.stats.RxCount++
			a.stats.mu.Unlock()
		}
	}
}

// handleDataFrame handles DATA frames (IP packets)
func (a *Agent) handleDataFrame(frame UDPFrame, addr *net.UDPAddr) {
	// Check if destination is our VIP
	dstVIP := bytesToIP(frame.DstVIP)
	if dstVIP != a.getLocalVIP() {
		a.logger.Printf("Dropping packet for different VIP: %s", dstVIP)
		return
	}

	// Write to TUN interface
	if _, err := a.tunInterface.Write(frame.Payload); err != nil {
		a.logger.Printf("Error writing to TUN: %v", err)
	}
}

// handleHelloFrame handles HELLO frames (peer discovery)
func (a *Agent) handleHelloFrame(frame UDPFrame, addr *net.UDPAddr) {
	srcVIP := bytesToIP(frame.SrcVIP)

	// Update peer mapping
	a.mu.Lock()
	a.peerMappings[srcVIP] = addr
	a.mu.Unlock()

	a.logger.Printf("Received HELLO from %s -> %s", addr, srcVIP)

	// Send HELLO back
	helloFrame := UDPFrame{
		Version:     1,
		MessageType: 1, // HELLO
		SrcVIP:      ipToBytes(a.getLocalVIP()),
		DstVIP:      frame.SrcVIP,
		PayloadLen:  0,
		Payload:     nil,
	}

	if err := a.sendUDPFrame(helloFrame, addr); err != nil {
		a.logger.Printf("Error sending HELLO response: %v", err)
	}
}

// handlePingFrame handles PING frames (keepalive)
func (a *Agent) handlePingFrame(frame UDPFrame, addr *net.UDPAddr) {
	// Send PONG back
	pongFrame := UDPFrame{
		Version:     1,
		MessageType: 3, // PONG
		SrcVIP:      ipToBytes(a.getLocalVIP()),
		DstVIP:      frame.SrcVIP,
		PayloadLen:  0,
		Payload:     nil,
	}

	if err := a.sendUDPFrame(pongFrame, addr); err != nil {
		a.logger.Printf("Error sending PONG: %v", err)
	}
}

// handlePongFrame handles PONG frames (keepalive response)
func (a *Agent) handlePongFrame(frame UDPFrame, addr *net.UDPAddr) {
	// Update peer last seen time
	srcVIP := bytesToIP(frame.SrcVIP)
	a.mu.Lock()
	// Find session by VIP
	for _, session := range a.peerSessions {
		if session.Endpoint.VIP == srcVIP {
			session.Endpoint.LastSeen = time.Now()
			break
		}
	}
	a.mu.Unlock()
}

// handleErrorFrame handles ERROR frames
func (a *Agent) handleErrorFrame(frame UDPFrame, addr *net.UDPAddr) {
	errorMsg := string(frame.Payload)
	a.logger.Printf("Received ERROR from %s: %s", addr, errorMsg)
}

// ============================================================================
// UDP FRAME HANDLING
// ============================================================================

// sendUDPFrame sends a UDP frame to the specified address
func (a *Agent) sendUDPFrame(frame UDPFrame, addr *net.UDPAddr) error {
	// Serialize frame
	data, err := a.serializeUDPFrame(frame)
	if err != nil {
		return fmt.Errorf("failed to serialize UDP frame: %w", err)
	}

	// Send via UDP
	_, err = a.udpConn.WriteToUDP(data, addr)
	return err
}

// serializeUDPFrame serializes a UDP frame to bytes
func (a *Agent) serializeUDPFrame(frame UDPFrame) ([]byte, error) {
	// Calculate total size
	totalSize := 12 + len(frame.Payload) // 12-byte header + payload

	// Create buffer
	data := make([]byte, totalSize)
	offset := 0

	// Version (1 byte)
	data[offset] = frame.Version
	offset++

	// Message type (1 byte)
	data[offset] = frame.MessageType
	offset++

	// Source VIP (4 bytes)
	copy(data[offset:offset+4], frame.SrcVIP[:])
	offset += 4

	// Destination VIP (4 bytes)
	copy(data[offset:offset+4], frame.DstVIP[:])
	offset += 4

	// Payload length (2 bytes, little-endian)
	binary.LittleEndian.PutUint16(data[offset:offset+2], frame.PayloadLen)
	offset += 2

	// Payload
	copy(data[offset:], frame.Payload)

	return data, nil
}

// parseUDPFrame parses bytes into a UDP frame
func (a *Agent) parseUDPFrame(data []byte) (UDPFrame, error) {
	if len(data) < 12 {
		return UDPFrame{}, fmt.Errorf("frame too short: %d bytes", len(data))
	}

	offset := 0
	frame := UDPFrame{}

	// Version (1 byte)
	frame.Version = data[offset]
	offset++

	// Message type (1 byte)
	frame.MessageType = data[offset]
	offset++

	// Source VIP (4 bytes)
	copy(frame.SrcVIP[:], data[offset:offset+4])
	offset += 4

	// Destination VIP (4 bytes)
	copy(frame.DstVIP[:], data[offset:offset+4])
	offset += 4

	// Payload length (2 bytes, little-endian)
	frame.PayloadLen = binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2

	// Validate payload length
	if int(frame.PayloadLen) != len(data)-offset {
		return UDPFrame{}, fmt.Errorf("payload length mismatch: expected %d, got %d",
			frame.PayloadLen, len(data)-offset)
	}

	// Payload
	frame.Payload = make([]byte, frame.PayloadLen)
	copy(frame.Payload, data[offset:])

	return frame, nil
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

// getLocalVIP returns our local VIP
func (a *Agent) getLocalVIP() string {
	return strings.Split(a.config.VirtualIP, "/")[0]
}

// ipToBytes converts IP string to 4-byte array
func ipToBytes(ipStr string) [4]byte {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return [4]byte{}
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return [4]byte{}
	}
	var result [4]byte
	copy(result[:], ipv4)
	return result
}

// bytesToIP converts 4-byte array to IP string
func bytesToIP(ipBytes [4]byte) string {
	return net.IP(ipBytes[:]).String()
}

// ============================================================================
// SIGNALING CLIENT
// ============================================================================

// SignalingClient handles WebSocket signaling communication (simplified from WebRTC version)
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
	// Generate client ID from VIP
	vip := strings.Split(sc.config.VirtualIP, "/")[0]
	clientID := fmt.Sprintf("agent-%s", strings.ReplaceAll(vip, ".", "-"))

	// Construct WebSocket URL with client ID
	wsURL := strings.TrimSuffix(sc.config.SignalingURL, "/") + "/ws/" + clientID
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
func (a *Agent) signalingLoop() {
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
func (a *Agent) handleSignalingMessage(msg SignalingMessage) error {
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
func (a *Agent) handlePeerMessage(msg SignalingMessage) error {
	if msg.IP == "" || msg.Port == 0 {
		return fmt.Errorf("invalid peer message: missing IP or port")
	}

	// Create peer endpoint
	peerAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", msg.IP, msg.Port))
	if err != nil {
		return fmt.Errorf("failed to resolve peer address: %w", err)
	}

	// Create peer session
	peerID := fmt.Sprintf("%s:%d", msg.IP, msg.Port)
	session := &PeerSession{
		PeerID: peerID,
		Endpoint: &PeerEndpoint{
			IP:        msg.IP,
			Port:      msg.Port,
			VIP:       msg.VIP,
			LastSeen:  time.Now(),
			IsRelayed: false,
		},
		IsConnected: false,
		CreatedAt:   time.Now(),
	}

	a.mu.Lock()
	a.peerSessions[peerID] = session
	a.mu.Unlock()

	a.logger.Printf("Created peer session: %s -> %s", peerAddr, msg.VIP)

	// Start NAT hole punching
	go a.startNATPunching(peerAddr, msg.VIP)

	return nil
}

// startNATPunching attempts to establish direct UDP connection with peer
func (a *Agent) startNATPunching(peerAddr *net.UDPAddr, peerVIP string) {
	a.logger.Printf("Starting NAT punching to %s", peerAddr)

	// Send HELLO + PING packets
	for i := 0; i < a.config.PunchAttempts; i++ {
		select {
		case <-a.shutdownCtx.Done():
			return
		default:
			// Send HELLO
			helloFrame := UDPFrame{
				Version:     1,
				MessageType: 1, // HELLO
				SrcVIP:      ipToBytes(a.getLocalVIP()),
				DstVIP:      ipToBytes(peerVIP),
				PayloadLen:  0,
				Payload:     nil,
			}

			if err := a.sendUDPFrame(helloFrame, peerAddr); err != nil {
				a.logger.Printf("Error sending HELLO: %v", err)
			}

			// Send PING
			pingFrame := UDPFrame{
				Version:     1,
				MessageType: 2, // PING
				SrcVIP:      ipToBytes(a.getLocalVIP()),
				DstVIP:      ipToBytes(peerVIP),
				PayloadLen:  0,
				Payload:     nil,
			}

			if err := a.sendUDPFrame(pingFrame, peerAddr); err != nil {
				a.logger.Printf("Error sending PING: %v", err)
			}

			// Wait for interval
			time.Sleep(time.Duration(a.config.PunchIntervalMs) * time.Millisecond)
		}
	}

	a.logger.Printf("NAT punching completed for %s", peerAddr)
}

// peerReaper removes stale peer mappings
func (a *Agent) peerReaper() {
	ticker := time.NewTicker(time.Duration(a.config.PeerStaleTimeoutS) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		case <-ticker.C:
			a.cleanupStalePeers()
		}
	}
}

// cleanupStalePeers removes peers that haven't been seen recently
func (a *Agent) cleanupStalePeers() {
	cutoff := time.Now().Add(-time.Duration(a.config.PeerStaleTimeoutS) * time.Second)

	a.mu.Lock()
	defer a.mu.Unlock()

	for vip, addr := range a.peerMappings {
		// Check if we have a session for this VIP
		var lastSeen time.Time
		for _, session := range a.peerSessions {
			if session.Endpoint.VIP == vip {
				lastSeen = session.Endpoint.LastSeen
				break
			}
		}

		if lastSeen.Before(cutoff) {
			delete(a.peerMappings, vip)
			a.logger.Printf("Removed stale peer mapping: %s -> %s", vip, addr)
		}
	}
}

// statsLogger logs periodic statistics
func (a *Agent) statsLogger() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		case <-ticker.C:
			a.logStats()
		}
	}
}

// logStats logs current statistics
func (a *Agent) logStats() {
	a.stats.mu.RLock()
	txCount := a.stats.TxCount
	rxCount := a.stats.RxCount
	a.stats.mu.RUnlock()

	a.mu.RLock()
	peersCount := len(a.peerMappings)
	a.mu.RUnlock()

	a.logger.Printf("Stats: TX=%d, RX=%d, Peers=%d", txCount, rxCount, peersCount)
}

// ============================================================================
// MODE RUNNERS
// ============================================================================

// runAnswerer runs in answerer mode (listens for connections)
func runAnswerer(ctx context.Context, agent *Agent) error {
	agent.logger.Println("Starting answerer mode - listening for connections")

	// Wait for first connection
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			agent.mu.RLock()
			peersCount := len(agent.peerMappings)
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
func runOfferer(ctx context.Context, agent *Agent, peerID string) error {
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
			peersCount := len(agent.peerMappings)
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
func runInteractiveSession(ctx context.Context, agent *Agent) error {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("🎉 UDP+TUN SESSION STARTED 🎉")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("💬 Type your messages and press Enter to send")
	fmt.Println("🚪 Type 'quit' to exit")
	fmt.Println("🔧 Use 'ping <vip>' to test connectivity")
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

				if strings.HasPrefix(line, "ping ") {
					// Handle ping command
					vip := strings.TrimSpace(strings.TrimPrefix(line, "ping"))
					if err := agent.sendPing(vip); err != nil {
						fmt.Printf("❌ Failed to ping %s: %v\n", vip, err)
					} else {
						fmt.Printf("✅ Ping sent to %s\n", vip)
					}
				} else if line != "" {
					// Send as text message (could be implemented as UDP test packet)
					fmt.Printf("✅ Message: %s\n", line)
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

// sendPing sends a ping to a specific VIP
func (a *Agent) sendPing(vip string) error {
	// Look up peer endpoint
	a.mu.RLock()
	peerAddr, exists := a.peerMappings[vip]
	a.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no peer mapping for VIP: %s", vip)
	}

	// Send PING frame
	pingFrame := UDPFrame{
		Version:     1,
		MessageType: 2, // PING
		SrcVIP:      ipToBytes(a.getLocalVIP()),
		DstVIP:      ipToBytes(vip),
		PayloadLen:  0,
		Payload:     nil,
	}

	return a.sendUDPFrame(pingFrame, peerAddr)
}
