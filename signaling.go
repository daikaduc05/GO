package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

// SignalingMessage represents a signaling message
type SignalingMessage struct {
	Type      string                   `json:"type"`
	From      string                   `json:"from"`
	To        string                   `json:"to"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Error     string                   `json:"error,omitempty"`
	Data      map[string]interface{}   `json:"data,omitempty"`
}

// SignalingClient handles WebSocket signaling communication
type SignalingClient struct {
	settings             AgentSettings
	conn                 *websocket.Conn
	connected            bool
	mu                   sync.RWMutex
	messageQueue         chan SignalingMessage
	outboxQueue          chan SignalingMessage
	shutdownCtx          context.Context
	shutdownCancel       context.CancelFunc
	logger               *logrus.Logger
	reconnectAttempts    int
	maxReconnectAttempts int
	reconnectDelay       time.Duration
	maxReconnectDelay    time.Duration
}

// NewSignalingClient creates a new signaling client
func NewSignalingClient(settings AgentSettings) *SignalingClient {
	ctx, cancel := context.WithCancel(context.Background())

	return &SignalingClient{
		settings:             settings,
		connected:            false,
		messageQueue:         make(chan SignalingMessage, 100),
		outboxQueue:          make(chan SignalingMessage, 100),
		shutdownCtx:          ctx,
		shutdownCancel:       cancel,
		logger:               logrus.New(),
		maxReconnectAttempts: 10,
		reconnectDelay:       time.Second,
		maxReconnectDelay:    time.Minute,
	}
}

// Connect establishes connection to signaling server with auto-reconnection
func (sc *SignalingClient) Connect() error {
	sc.logger.WithField("url", sc.settings.SignalingURL).Info("Connecting to signaling server")

	for sc.reconnectAttempts < sc.maxReconnectAttempts {
		if err := sc.doConnect(); err != nil {
			sc.reconnectAttempts++
			sc.logger.WithError(err).WithField("attempt", sc.reconnectAttempts).Warning("Connection attempt failed")

			if sc.reconnectAttempts >= sc.maxReconnectAttempts {
				return fmt.Errorf("failed to connect after %d attempts: %w", sc.maxReconnectAttempts, err)
			}

			// Exponential backoff with jitter
			delay := sc.reconnectDelay * time.Duration(1<<uint(sc.reconnectAttempts-1))
			if delay > sc.maxReconnectDelay {
				delay = sc.maxReconnectDelay
			}

			sc.logger.WithField("delay", delay).Info("Reconnecting...")
			time.Sleep(delay)
			continue
		}

		sc.reconnectAttempts = 0
		sc.logger.Info("Successfully connected to signaling server")
		return nil
	}

	return fmt.Errorf("max reconnection attempts reached")
}

// doConnect performs the actual WebSocket connection
func (sc *SignalingClient) doConnect() error {
	// Parse URL
	u, err := url.Parse(sc.settings.SignalingURL)
	if err != nil {
		return fmt.Errorf("invalid signaling URL: %w", err)
	}

	// Add client ID to path
	u.Path = fmt.Sprintf("/ws/%s", sc.settings.AgentID)

	// Set up headers
	headers := http.Header{}
	if sc.settings.SignalingToken != "" {
		headers.Set("Authorization", fmt.Sprintf("Bearer %s", sc.settings.SignalingToken))
	}

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), headers)
	if err != nil {
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	sc.mu.Lock()
	sc.conn = conn
	sc.connected = true
	sc.mu.Unlock()

	// Start background tasks
	go sc.messageReader()
	go sc.messageSender()
	go sc.heartbeat()

	// Send registration message
	if err := sc.sendRegister(); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	// Flush any queued messages
	sc.flushOutbox()

	return nil
}

// sendRegister sends registration message to server
func (sc *SignalingClient) sendRegister() error {
	registerMsg := SignalingMessage{
		Type: "register",
		Data: map[string]interface{}{
			"id":    sc.settings.AgentID,
			"token": sc.settings.SignalingToken,
		},
	}

	return sc.sendRaw(registerMsg)
}

// messageReader reads messages from WebSocket
func (sc *SignalingClient) messageReader() {
	defer func() {
		sc.mu.Lock()
		sc.connected = false
		sc.mu.Unlock()
	}()

	for {
		select {
		case <-sc.shutdownCtx.Done():
			sc.logger.Debug("Message reader shutting down")
			return
		default:
			sc.mu.RLock()
			conn := sc.conn
			sc.mu.RUnlock()

			if conn == nil {
				return
			}

			// Set read deadline
			conn.SetReadDeadline(time.Now().Add(time.Minute))

			// Read message
			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					sc.logger.Info("WebSocket connection closed normally")
					return
				}
				sc.logger.WithError(err).Error("Error reading WebSocket message")
				return
			}

			// Parse message
			var message SignalingMessage
			if err := json.Unmarshal(data, &message); err != nil {
				sc.logger.WithError(err).Error("Invalid JSON received")
				continue
			}

			// Validate message
			if err := sc.validateMessage(message); err != nil {
				sc.logger.WithError(err).Error("Message validation failed")
				continue
			}

			// Queue message for processing
			select {
			case sc.messageQueue <- message:
				sc.logger.WithField("type", message.Type).Debug("Received message")
			case <-sc.shutdownCtx.Done():
				return
			default:
				sc.logger.Warning("Message queue full, dropping message")
			}
		}
	}
}

// messageSender sends queued messages
func (sc *SignalingClient) messageSender() {
	for {
		select {
		case <-sc.shutdownCtx.Done():
			sc.logger.Debug("Message sender shutting down")
			return
		case message := <-sc.outboxQueue:
			if err := sc.sendRaw(message); err != nil {
				sc.logger.WithError(err).Error("Failed to send message")
			}
		}
	}
}

// heartbeat sends periodic ping messages
func (sc *SignalingClient) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sc.shutdownCtx.Done():
			sc.logger.Debug("Heartbeat shutting down")
			return
		case <-ticker.C:
			if sc.IsConnected() {
				pingMsg := SignalingMessage{Type: "ping"}
				if err := sc.sendRaw(pingMsg); err != nil {
					sc.logger.WithError(err).Error("Failed to send heartbeat")
				} else {
					sc.logger.Debug("Sent heartbeat ping")
				}
			}
		}
	}
}

// flushOutbox flushes any queued outgoing messages
func (sc *SignalingClient) flushOutbox() {
	for {
		select {
		case message := <-sc.outboxQueue:
			if err := sc.sendRaw(message); err != nil {
				sc.logger.WithError(err).Error("Failed to flush message")
			}
		default:
			return
		}
	}
}

// sendRaw sends raw message over WebSocket
func (sc *SignalingClient) sendRaw(message SignalingMessage) error {
	sc.mu.RLock()
	conn := sc.conn
	connected := sc.connected
	sc.mu.RUnlock()

	if !connected || conn == nil {
		return fmt.Errorf("not connected to signaling server")
	}

	// Set write deadline
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// Marshal and send message
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// validateMessage validates incoming message structure
func (sc *SignalingClient) validateMessage(message SignalingMessage) error {
	if message.Type == "" {
		return fmt.Errorf("message missing 'type' field")
	}

	// Basic validation based on message type
	switch message.Type {
	case "offer":
		if message.SDP == "" {
			return fmt.Errorf("offer message missing SDP")
		}
	case "answer":
		if message.SDP == "" {
			return fmt.Errorf("answer message missing SDP")
		}
	case "candidate":
		// Candidate can be nil for end-of-candidates
	case "bye":
		// Bye message is simple
	case "error":
		// Error message
	case "ping", "pong":
		// Simple ping/pong messages
	default:
		sc.logger.WithField("type", message.Type).Warning("Unknown message type")
	}

	return nil
}

// Send sends a message to signaling server
func (sc *SignalingClient) Send(message SignalingMessage) error {
	if sc.IsConnected() {
		return sc.sendRaw(message)
	}

	// Queue message for later sending
	select {
	case sc.outboxQueue <- message:
		sc.logger.Debug("Queued message for later sending")
		return nil
	case <-sc.shutdownCtx.Done():
		return fmt.Errorf("client is shutting down")
	default:
		return fmt.Errorf("outbox queue full")
	}
}

// Receive receives a message from signaling server
func (sc *SignalingClient) Receive(ctx context.Context) (SignalingMessage, error) {
	select {
	case message := <-sc.messageQueue:
		return message, nil
	case <-ctx.Done():
		return SignalingMessage{}, ctx.Err()
	case <-sc.shutdownCtx.Done():
		return SignalingMessage{}, fmt.Errorf("client is shutting down")
	}
}

// IsConnected checks if client is connected
func (sc *SignalingClient) IsConnected() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.connected && sc.conn != nil
}

// Close gracefully closes signaling connection
func (sc *SignalingClient) Close() error {
	sc.logger.Info("Closing signaling client")

	// Signal shutdown
	sc.shutdownCancel()

	// Close WebSocket connection
	sc.mu.Lock()
	if sc.conn != nil {
		sc.conn.Close()
		sc.conn = nil
	}
	sc.connected = false
	sc.mu.Unlock()

	// Close channels
	close(sc.messageQueue)
	close(sc.outboxQueue)

	sc.logger.Info("Signaling client closed")
	return nil
}
