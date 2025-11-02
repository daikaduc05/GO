package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SignalingClient handles WebSocket signaling communication
type SignalingClient struct {
	config    *Config
	conn      *websocket.Conn
	connected bool
	mu        sync.RWMutex
	logger    *log.Logger
}

// SignalingMessage represents signaling server messages
type SignalingMessage struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

// RegisterMessage represents register request message
type RegisterMessage struct {
	Type       string `json:"type"`
	AgentID    string `json:"agent_id,omitempty"`
	PublicIP   string `json:"public_ip"`
	PublicPort int    `json:"public_port"`
	RelayIP    string `json:"relay_ip,omitempty"`
	RelayPort  int    `json:"relay_port,omitempty"`
}

// RegisterAgentResponse represents register response message
type RegisterAgentResponse struct {
	Type         string      `json:"type"`
	Status       string      `json:"status"`
	VirtualIP    string      `json:"virtual_ip"`
	ConnectionID string      `json:"connection_id"`
	ExistingPeers []PeerInfo `json:"existing_peers"`
}

// PeerInfo represents peer information
type PeerInfo struct {
	PeerID     string `json:"peer_id"`
	UserID     int    `json:"user_id"`
	Email      string `json:"email"`
	AgentID    string `json:"agent_id,omitempty"`
	PublicIP   string `json:"public_ip"`
	PublicPort int    `json:"public_port"`
	RelayIP    string `json:"relay_ip,omitempty"`
	RelayPort  int    `json:"relay_port,omitempty"`
	VirtualIP  string `json:"virtual_ip"`
}

// PeerOnlineNotification represents peer online notification
type PeerOnlineNotification struct {
	Type string   `json:"type"`
	Peer PeerInfo `json:"peer"`
}

// NewSignalingClient creates a new signaling client
func NewSignalingClient(config *Config) *SignalingClient {
	return &SignalingClient{
		config: config,
		logger: log.New(os.Stdout, "[SIGNALING] ", log.LstdFlags),
	}
}

// Connect establishes connection to signaling server
func (sc *SignalingClient) Connect() error {
	// Construct WebSocket URL - preserve port if present
	baseURL := strings.TrimSuffix(sc.config.SignalingURL, "/")
	
	// Parse the base URL to preserve port and scheme
	var wsURL string
	var originalURL string
	if u, err := url.Parse(baseURL); err == nil {
		// Keep the full host (including port if present)
		host := u.Host
		if host == "" {
			host = u.Hostname()
		}
		wsURL = u.Scheme + "://" + host + "/ws"
		originalURL = wsURL
	} else {
		// Fallback if parsing fails
		wsURL = baseURL + "/ws"
		originalURL = wsURL
	}
	
	// Add token to query parameter or Authorization header
	if sc.config.Token != "" {
		// Prefer query parameter, but can also use Authorization header
		separator := "?"
		if strings.Contains(wsURL, "?") {
			separator = "&"
		}
		wsURL += separator + "token=" + sc.config.Token
	}

	// Prepare headers
	hdr := http.Header{}
	if sc.config.WSOrigin != "" {
		hdr.Set("Origin", sc.config.WSOrigin)
	}
	// Also add Authorization header if token is present
	if sc.config.Token != "" {
		hdr.Set("Authorization", "Bearer "+sc.config.Token)
	}

	// Custom dialer
	d := *websocket.DefaultDialer
	d.HandshakeTimeout = 20 * time.Second
	d.EnableCompression = false
	d.NetDialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext

	// Try connection - fallback to port 80 if current port fails
	conn, resp, err := sc.attemptConnection(d, wsURL, hdr)
	if err != nil {
		// If timeout/failure and not already on port 80, try port 80
		if netErr, ok := err.(net.Error); ok && (netErr.Timeout() || strings.Contains(err.Error(), "timeout")) {
			if u, parseErr := url.Parse(originalURL); parseErr == nil {
				if u.Port() != "" && u.Port() != "80" {
					// Try port 80 as fallback
					hostWithoutPort := u.Hostname()
					fallbackURL := u.Scheme + "://" + hostWithoutPort + ":80/ws"
					if sc.config.Token != "" {
						fallbackURL += "?token=" + sc.config.Token
					}
					sc.logger.Printf("Connection to port %s failed, trying port 80...", u.Port())
					
					// Update origin header for port 80
					if sc.config.WSOrigin == "" {
						originScheme := "http"
						if u.Scheme == "wss" {
							originScheme = "https"
						}
						hdr.Set("Origin", originScheme+"://"+hostWithoutPort+":80")
					}
					
					conn, resp, err = sc.attemptConnection(d, fallbackURL, hdr)
					if err == nil {
						wsURL = fallbackURL
					}
				}
			}
		}
		
		// If still failed after fallback, return error
		if err != nil {
			if resp != nil {
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed to connect: %s (HTTP %d) body=%s", 
					err.Error(), resp.StatusCode, strings.TrimSpace(string(b)))
			}
			return fmt.Errorf("failed to connect: %w", err)
		}
	}

	sc.mu.Lock()
	sc.conn = conn
	sc.connected = true
	sc.mu.Unlock()

	// Keep-alive
	conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
		return nil
	})

	// Start ping loop
	go sc.pingLoop(conn)

	sc.logger.Println("✅ Connected to signaling server")
	return nil
}

// attemptConnection attempts a WebSocket connection with redirect handling
func (sc *SignalingClient) attemptConnection(d websocket.Dialer, wsURL string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	sc.logger.Printf("Connecting to: %s", wsURL)
	
	// Ensure origin header is set
	if hdr.Get("Origin") == "" {
		if u, err := url.Parse(wsURL); err == nil {
			originScheme := "http"
			if u.Scheme == "wss" {
				originScheme = "https"
			}
			origin := originScheme + "://" + u.Host
			hdr.Set("Origin", origin)
		}
	}

	// Attempt connection
	conn, resp, err := d.Dial(wsURL, hdr)
	if err != nil {
		// Handle HTTP redirects (301, 302, 307, 308)
		if resp != nil {
			status := resp.StatusCode
			location := resp.Header.Get("Location")
			
			if (status == http.StatusMovedPermanently || 
				status == http.StatusFound || 
				status == http.StatusTemporaryRedirect || 
				status == http.StatusPermanentRedirect) && location != "" {
				
				// Build redirected WebSocket URL
				redirectedURL := location
				if strings.HasPrefix(location, "http://") {
					redirectedURL = "ws://" + strings.TrimPrefix(location, "http://")
				} else if strings.HasPrefix(location, "https://") {
					redirectedURL = "wss://" + strings.TrimPrefix(location, "https://")
				} else if !strings.HasPrefix(location, "ws://") && !strings.HasPrefix(location, "wss://") {
					// Relative redirect - resolve against current URL
					if u, err := url.Parse(wsURL); err == nil {
						if base, err := url.Parse(location); err == nil {
							redirectedURL = u.ResolveReference(base).String()
						}
					}
				}
				
				// Add token back if present
				if sc.config.Token != "" && !strings.Contains(redirectedURL, "token=") {
					separator := "?"
					if strings.Contains(redirectedURL, "?") {
						separator = "&"
					}
					redirectedURL += separator + "token=" + sc.config.Token
				}
				
				sc.logger.Printf("Following redirect to: %s", redirectedURL)
				
				// Retry with redirected URL
				resp.Body.Close()
				return d.Dial(redirectedURL, hdr)
			} else {
				// Non-redirect error
				resp.Body.Close()
				return nil, resp, fmt.Errorf("HTTP %d: %w", resp.StatusCode, err)
			}
		}
		return nil, nil, err
	}
	
	return conn, resp, nil
}

// pingLoop sends periodic ping messages
func (sc *SignalingClient) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		sc.mu.RLock()
		alive := sc.connected && sc.conn == conn
		sc.mu.RUnlock()
		
		if !alive {
			return
		}
		
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
			sc.logger.Printf("Ping error: %v", err)
			return
		}
	}
}

// SendRegister sends a register message to signaling server
func (sc *SignalingClient) SendRegister(msg RegisterMessage) error {
	sc.mu.RLock()
	conn := sc.conn
	connected := sc.connected
	sc.mu.RUnlock()

	if !connected || conn == nil {
		return fmt.Errorf("not connected to signaling server")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal register message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to write register message: %w", err)
	}

	sc.logger.Printf("Sent register message: agent_id=%s, public_ip=%s:%d", msg.AgentID, msg.PublicIP, msg.PublicPort)
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

	conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
	
	_, data, err := conn.ReadMessage()
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return SignalingMessage{}, context.DeadlineExceeded
		}
		return SignalingMessage{}, err
	}

	var msg SignalingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return SignalingMessage{}, fmt.Errorf("failed to parse message: %w", err)
	}
	msg.Raw = data

	sc.logger.Printf("Received message: type=%s", msg.Type)
	return msg, nil
}

// ReceiveRegisterResponse waits for and parses register_agent_response
func (sc *SignalingClient) ReceiveRegisterResponse(ctx context.Context) (*RegisterAgentResponse, error) {
	msg, err := sc.Receive(ctx)
	if err != nil {
		return nil, err
	}

	if msg.Type != "register_agent_response" {
		return nil, fmt.Errorf("unexpected message type: %s", msg.Type)
	}

	var response RegisterAgentResponse
	if err := json.Unmarshal(msg.Raw, &response); err != nil {
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}

	sc.logger.Printf("Received register response: status=%s, virtual_ip=%s, peers=%d", 
		response.Status, response.VirtualIP, len(response.ExistingPeers))
	return &response, nil
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
	
	return nil
}

// StartMessageHandler starts a goroutine to handle incoming messages
func (sc *SignalingClient) StartMessageHandler(handler func(SignalingMessage)) {
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			msg, err := sc.Receive(ctx)
			cancel()
			
			if err != nil {
				if err == context.DeadlineExceeded {
					continue
				}
				sc.logger.Printf("Receive error: %v", err)
				return
			}
			
			handler(msg)
		}
	}()
}
