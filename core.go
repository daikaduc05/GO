package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

// AgentSettings holds configuration for the WebRTC agent
type AgentSettings struct {
	AgentID          string             `json:"agent_id"`
	SignalingURL     string             `json:"signaling_url"`
	SignalingToken   string             `json:"signaling_token,omitempty"`
	ICEServers       []webrtc.ICEServer `json:"ice_servers"`
	DataChannelLabel string             `json:"data_channel_label"`
	Mode             string             `json:"mode"`
}

// PeerSession represents a WebRTC peer connection session
type PeerSession struct {
	PeerID      string
	PC          *webrtc.PeerConnection
	DataChannel *webrtc.DataChannel
	Transport   Transport
	CreatedAt   time.Time
	mu          sync.RWMutex
}

// IsConnected checks if the peer connection is established
func (ps *PeerSession) IsConnected() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if ps.PC == nil || ps.DataChannel == nil {
		return false
	}

	return ps.PC.ConnectionState() == webrtc.PeerConnectionStateConnected &&
		ps.DataChannel.ReadyState() == webrtc.DataChannelStateOpen
}

// GetStats returns session statistics
func (ps *PeerSession) GetStats() map[string]interface{} {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	stats := map[string]interface{}{
		"peer_id":              ps.PeerID,
		"connection_state":     ps.PC.ConnectionState().String(),
		"ice_connection_state": ps.PC.ICEConnectionState().String(),
		"ice_gathering_state":  ps.PC.ICEGatheringState().String(),
		"created_at":           ps.CreatedAt,
		"uptime":               time.Since(ps.CreatedAt).Seconds(),
	}

	if ps.DataChannel != nil {
		stats["data_channel_state"] = ps.DataChannel.ReadyState().String()
	}

	return stats
}

// AgentCore manages WebRTC peer connections and signaling
type AgentCore struct {
	settings       AgentSettings
	signaling      *SignalingClient
	peerSessions   map[string]*PeerSession
	mu             sync.RWMutex
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	logger         *logrus.Logger
}

// NewAgentCore creates a new WebRTC agent core
func NewAgentCore(settings AgentSettings, signaling *SignalingClient) *AgentCore {
	ctx, cancel := context.WithCancel(context.Background())

	return &AgentCore{
		settings:       settings,
		signaling:      signaling,
		peerSessions:   make(map[string]*PeerSession),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		logger:         logrus.New(),
	}
}

// Start begins the agent core and signaling loop
func (ac *AgentCore) Start() error {
	ac.logger.Info("Starting agent core")

	// Connect to signaling server
	if err := ac.signaling.Connect(); err != nil {
		return fmt.Errorf("failed to connect to signaling: %w", err)
	}

	// Start signaling message processing loop
	go ac.signalingLoop()

	ac.logger.Info("Agent core started")
	return nil
}

// signalingLoop processes incoming signaling messages
func (ac *AgentCore) signalingLoop() {
	for {
		select {
		case <-ac.shutdownCtx.Done():
			ac.logger.Debug("Signaling loop shutting down")
			return
		default:
			// Process messages with timeout
			ctx, cancel := context.WithTimeout(ac.shutdownCtx, time.Second)
			message, err := ac.signaling.Receive(ctx)
			cancel()

			if err != nil {
				if err == context.DeadlineExceeded {
					continue // Timeout is normal, continue loop
				}
				ac.logger.WithError(err).Error("Error receiving signaling message")
				time.Sleep(time.Second)
				continue
			}

			if err := ac.handleIncomingMessage(message); err != nil {
				ac.logger.WithError(err).Error("Error handling signaling message")
			}
		}
	}
}

// handleIncomingMessage processes incoming signaling messages
func (ac *AgentCore) handleIncomingMessage(message SignalingMessage) error {
	ac.logger.WithField("type", message.Type).Debug("Handling incoming message")

	// Check if message is for us
	if message.To != "" && message.To != ac.settings.AgentID {
		ac.logger.WithField("to", message.To).Debug("Message not for us, ignoring")
		return nil
	}

	switch message.Type {
	case "offer":
		return ac.handleOffer(message)
	case "answer":
		return ac.handleAnswer(message)
	case "candidate":
		return ac.handleCandidate(message)
	case "bye":
		return ac.handleBye(message)
	case "error":
		return ac.handleError(message)
	default:
		ac.logger.WithField("type", message.Type).Warning("Unknown message type")
		return nil
	}
}

// handleOffer processes incoming WebRTC offer
func (ac *AgentCore) handleOffer(message SignalingMessage) error {
	fromID := message.From
	sdp := message.SDP

	ac.logger.WithField("from", fromID).Info("Received offer")

	// Create peer session if not exists
	if _, exists := ac.peerSessions[fromID]; !exists {
		if err := ac.createPeerSession(fromID, false); err != nil {
			return fmt.Errorf("failed to create peer session: %w", err)
		}
	}

	session := ac.peerSessions[fromID]

	// Set remote description
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}

	if err := session.PC.SetRemoteDescription(offer); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	// Create answer
	answer, err := session.PC.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %w", err)
	}

	// Set local description
	if err := session.PC.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	// Send answer immediately (trickle ICE - candidates will be sent separately)
	answerMsg := SignalingMessage{
		Type: "answer",
		From: ac.settings.AgentID,
		To:   fromID,
		SDP:  answer.SDP,
	}

	if err := ac.signaling.Send(answerMsg); err != nil {
		return fmt.Errorf("failed to send answer: %w", err)
	}

	ac.logger.WithField("to", fromID).Info("Sent answer (trickle ICE)")
	return nil
}

// handleAnswer processes incoming WebRTC answer
func (ac *AgentCore) handleAnswer(message SignalingMessage) error {
	fromID := message.From
	sdp := message.SDP

	ac.logger.WithField("from", fromID).Info("Received answer")

	session, exists := ac.peerSessions[fromID]
	if !exists {
		ac.logger.WithField("from", fromID).Warning("Received answer from unknown peer")
		return nil
	}

	// Set remote description
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}

	if err := session.PC.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	ac.logger.WithField("from", fromID).Info("Set remote description")
	return nil
}

// handleCandidate processes incoming ICE candidate (trickle ICE)
func (ac *AgentCore) handleCandidate(message SignalingMessage) error {
	fromID := message.From
	candidate := message.Candidate

	ac.logger.WithField("from", fromID).Debug("Received ICE candidate (trickle ICE)")

	session, exists := ac.peerSessions[fromID]
	if !exists {
		ac.logger.WithField("from", fromID).Warning("Received candidate from unknown peer")
		return nil
	}

	// Handle end-of-candidates
	if candidate == nil {
		ac.logger.WithField("from", fromID).Debug("Remote ICE gathering complete")
		return nil
	}

	// Add ICE candidate
	if err := session.PC.AddICECandidate(*candidate); err != nil {
		ac.logger.WithError(err).Error("Failed to add ICE candidate")
		return err
	}

	ac.logger.WithField("from", fromID).Debug("Applied ICE candidate")
	return nil
}

// handleBye processes connection termination message
func (ac *AgentCore) handleBye(message SignalingMessage) error {
	fromID := message.From
	ac.logger.WithField("from", fromID).Info("Received bye")
	return ac.cleanupSession(fromID)
}

// handleError processes error message
func (ac *AgentCore) handleError(message SignalingMessage) error {
	errorMsg := message.Error
	fromID := message.From

	ac.logger.WithFields(logrus.Fields{
		"from":  fromID,
		"error": errorMsg,
	}).Error("Received error")
	return nil
}

// ConnectTo initiates connection to a peer
func (ac *AgentCore) ConnectTo(peerID string) error {
	ac.logger.WithField("peer_id", peerID).Info("Connecting to peer")

	// Create peer session
	if err := ac.createPeerSession(peerID, true); err != nil {
		return fmt.Errorf("failed to create peer session: %w", err)
	}

	session := ac.peerSessions[peerID]

	// Create data channel
	dataChannel, err := session.PC.CreateDataChannel(ac.settings.DataChannelLabel, nil)
	if err != nil {
		return fmt.Errorf("failed to create data channel: %w", err)
	}

	session.DataChannel = dataChannel
	session.Transport.AttachChannel(dataChannel)

	// Set up ICE candidate handler (trickle ICE - send each candidate immediately)
	session.PC.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			ac.logger.WithField("peer_id", peerID).Debug("ICE gathering complete")
			return
		}

		// Send candidate immediately via signaling
		candidateJSON := candidate.ToJSON()
		candidateMsg := SignalingMessage{
			Type:      "candidate",
			From:      ac.settings.AgentID,
			To:        peerID,
			Candidate: &candidateJSON,
		}

		if err := ac.signaling.Send(candidateMsg); err != nil {
			ac.logger.WithError(err).Error("Failed to send ICE candidate")
		} else {
			ac.logger.WithField("peer_id", peerID).Debug("Sent ICE candidate (trickle ICE)")
		}
	})

	// Set up ICE connection state handler
	session.PC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		ac.logger.WithFields(logrus.Fields{
			"peer_id": peerID,
			"state":   state.String(),
		}).Info("ICE connection state changed")

		if state == webrtc.ICEConnectionStateFailed {
			ac.logger.WithField("peer_id", peerID).Error("ICE connection failed")
		}
	})

	// Set up connection state handler
	session.PC.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		ac.logger.WithFields(logrus.Fields{
			"peer_id": peerID,
			"state":   state.String(),
		}).Info("Peer connection state changed")

		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			ac.logger.WithField("peer_id", peerID).Warning("Connection lost, cleaning up")
			go ac.cleanupSession(peerID)
		}
	})

	// Create offer
	offer, err := session.PC.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %w", err)
	}

	// Set local description
	if err := session.PC.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	// Send offer immediately (trickle ICE - candidates will be sent separately)
	offerMsg := SignalingMessage{
		Type: "offer",
		From: ac.settings.AgentID,
		To:   peerID,
		SDP:  offer.SDP,
	}

	if err := ac.signaling.Send(offerMsg); err != nil {
		return fmt.Errorf("failed to send offer: %w", err)
	}

	ac.logger.WithField("peer_id", peerID).Info("Sent offer (trickle ICE)")
	return nil
}

// createPeerSession creates a new peer session
func (ac *AgentCore) createPeerSession(peerID string, isOfferer bool) error {
	ac.logger.WithFields(logrus.Fields{
		"peer_id": peerID,
		"offerer": isOfferer,
	}).Info("Creating peer session")

	// Create WebRTC configuration
	config := webrtc.Configuration{
		ICEServers: ac.settings.ICEServers,
	}

	// Create peer connection
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}

	// Create transport based on mode
	transport := CreateTransport(ac.settings.Mode)
	transport.SetDefaultMessageHandler()

	// Create session
	session := &PeerSession{
		PeerID:    peerID,
		PC:        pc,
		Transport: transport,
		CreatedAt: time.Now(),
	}

	ac.mu.Lock()
	ac.peerSessions[peerID] = session
	ac.mu.Unlock()

	// Set up data channel handler for answerer
	if !isOfferer {
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			ac.logger.WithFields(logrus.Fields{
				"peer_id": peerID,
				"label":   dc.Label(),
			}).Info("Received data channel")

			session.DataChannel = dc
			session.Transport.AttachChannel(dc)
		})
	}

	// Set up ICE candidate handler (trickle ICE - send each candidate immediately)
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			ac.logger.WithField("peer_id", peerID).Debug("ICE gathering complete")
			return
		}

		// Send candidate immediately via signaling
		candidateJSON := candidate.ToJSON()
		candidateMsg := SignalingMessage{
			Type:      "candidate",
			From:      ac.settings.AgentID,
			To:        peerID,
			Candidate: &candidateJSON,
		}

		if err := ac.signaling.Send(candidateMsg); err != nil {
			ac.logger.WithError(err).Error("Failed to send ICE candidate")
		} else {
			ac.logger.WithField("peer_id", peerID).Debug("Sent ICE candidate (trickle ICE)")
		}
	})

	// Set up ICE connection state handler
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		ac.logger.WithFields(logrus.Fields{
			"peer_id": peerID,
			"state":   state.String(),
		}).Info("ICE connection state changed")
	})

	ac.logger.WithField("peer_id", peerID).Info("Created peer session")
	return nil
}

// cleanupSession cleans up a peer session
func (ac *AgentCore) cleanupSession(peerID string) error {
	ac.mu.Lock()
	session, exists := ac.peerSessions[peerID]
	if exists {
		delete(ac.peerSessions, peerID)
	}
	ac.mu.Unlock()

	if !exists {
		return nil
	}

	ac.logger.WithField("peer_id", peerID).Info("Cleaning up session")

	// Close transport
	if err := session.Transport.Close(); err != nil {
		ac.logger.WithError(err).Error("Error closing transport")
	}

	// Close peer connection
	if err := session.PC.Close(); err != nil {
		ac.logger.WithError(err).Error("Error closing peer connection")
	}

	ac.logger.WithField("peer_id", peerID).Info("Cleaned up session")
	return nil
}

// GetSession returns a peer session by ID
func (ac *AgentCore) GetSession(peerID string) *PeerSession {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.peerSessions[peerID]
}

// GetAllSessions returns all peer sessions
func (ac *AgentCore) GetAllSessions() map[string]*PeerSession {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	sessions := make(map[string]*PeerSession)
	for k, v := range ac.peerSessions {
		sessions[k] = v
	}
	return sessions
}

// GetStats returns agent core statistics
func (ac *AgentCore) GetStats() map[string]interface{} {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	peers := make(map[string]interface{})
	for peerID, session := range ac.peerSessions {
		peers[peerID] = session.GetStats()
	}

	return map[string]interface{}{
		"agent_id":   ac.settings.AgentID,
		"peer_count": len(ac.peerSessions),
		"peers":      peers,
	}
}

// Stop stops the agent core and cleans up resources
func (ac *AgentCore) Stop() error {
	ac.logger.Info("Stopping agent core")

	// Signal shutdown
	ac.shutdownCancel()

	// Clean up all peer sessions
	ac.mu.Lock()
	peerIDs := make([]string, 0, len(ac.peerSessions))
	for peerID := range ac.peerSessions {
		peerIDs = append(peerIDs, peerID)
	}
	ac.mu.Unlock()

	for _, peerID := range peerIDs {
		ac.cleanupSession(peerID)
	}

	// Close signaling connection
	if err := ac.signaling.Close(); err != nil {
		ac.logger.WithError(err).Error("Error closing signaling connection")
	}

	ac.logger.Info("Agent core stopped")
	return nil
}

// WaitForShutdown waits for shutdown signal
func (ac *AgentCore) WaitForShutdown() {
	<-ac.shutdownCtx.Done()
}
