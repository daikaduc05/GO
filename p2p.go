package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// P2PConnection represents a P2P connection to a peer
type P2PConnection struct {
	PeerID     string
	Method     ConnectionMethod
	Status     ConnectionStatus
	Conn       net.PacketConn  // UDP connection for hole punching
	RelayConn  net.PacketConn  // TURN allocation for relay
	PeerAddr   *net.UDPAddr    // Destination address
	RelayAddr  *net.UDPAddr    // Relay destination address
	PublicIP   string
	PublicPort int
	RelayIP    string
	RelayPort  int
	LastUsed   time.Time
	mu         sync.RWMutex
}

// P2PManager manages all P2P connections
type P2PManager struct {
	connections map[string]*P2PConnection
	localConn   net.PacketConn  // Local UDP connection for hole punching
	turnClient  *TURNClient     // TURN client for relay
	mu          sync.RWMutex
	logger      *log.Logger
}

// NewP2PManager creates a new P2P connection manager
func NewP2PManager(localConn net.PacketConn, turnClient *TURNClient) *P2PManager {
	return &P2PManager{
		connections: make(map[string]*P2PConnection),
		localConn:   localConn,
		turnClient:  turnClient,
		logger:      log.New(os.Stdout, "[P2P] ", log.LstdFlags),
	}
}

// Connect establishes P2P connection to a peer
func (pm *P2PManager) Connect(peerID string, peerInfo PeerInfo) (*P2PConnection, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already connected
	if conn, exists := pm.connections[peerID]; exists && conn.Status == StatusConnected {
		return conn, nil
	}

	// Try hole punching first
	conn, err := pm.tryHolePunching(peerID, peerInfo)
	if err == nil {
		pm.connections[peerID] = conn
		pm.logger.Printf("✅ P2P connection established via hole punching: %s", peerID)
		return conn, nil
	}

	pm.logger.Printf("⚠️  Hole punching failed for %s: %v, trying relay...", peerID, err)

	// Fallback to relay
	conn, err = pm.tryRelay(peerID, peerInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to establish P2P connection: %w", err)
	}

	pm.connections[peerID] = conn
	pm.logger.Printf("✅ P2P connection established via relay: %s", peerID)
	return conn, nil
}

// tryHolePunching attempts NAT hole punching via public IP
func (pm *P2PManager) tryHolePunching(peerID string, peerInfo PeerInfo) (*P2PConnection, error) {
	if peerInfo.PublicIP == "" || peerInfo.PublicPort == 0 {
		return nil, fmt.Errorf("peer has no public IP/port")
	}

	peerAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", peerInfo.PublicIP, peerInfo.PublicPort))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve peer address: %w", err)
	}

	// Create connection entry
	conn := &P2PConnection{
		PeerID:     peerID,
		Method:     MethodHole,
		Status:     StatusConnected,
		Conn:       pm.localConn,
		PeerAddr:   peerAddr,
		PublicIP:   peerInfo.PublicIP,
		PublicPort: peerInfo.PublicPort,
		LastUsed:   time.Now(),
	}

	// Try to establish connection by sending a few packets
	success := false
	for i := 0; i < 3; i++ {
		testPacket := []byte(fmt.Sprintf("PING-%d", time.Now().Unix()))
		_, err := pm.localConn.WriteTo(testPacket, peerAddr)
		if err != nil {
			// Check if it's a timeout error - this might be network issue, not code issue
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				pm.logger.Printf("Hole punch packet send timeout (attempt %d/3): %v", i+1, err)
			} else {
				pm.logger.Printf("Failed to send hole punch packet (attempt %d/3): %v", i+1, err)
			}
			// Continue to next attempt even on timeout
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Wait for response (or timeout)
		pm.localConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		buffer := make([]byte, 1024)
		_, addr, err := pm.localConn.ReadFrom(buffer)
		// Clear the deadline after read attempt
		pm.localConn.SetReadDeadline(time.Time{})
		if err == nil && addr.String() == peerAddr.String() {
			success = true
			pm.logger.Printf("Hole punching successful: received response from %s", addr)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !success {
		conn.Status = StatusFailed
		return nil, fmt.Errorf("hole punching failed: no response from peer")
	}

	return conn, nil
}

// tryRelay establishes connection via TURN relay
func (pm *P2PManager) tryRelay(peerID string, peerInfo PeerInfo) (*P2PConnection, error) {
	if pm.turnClient == nil {
		return nil, fmt.Errorf("TURN client not available")
	}

	allocation := pm.turnClient.GetAllocation()
	if allocation == nil {
		return nil, fmt.Errorf("TURN allocation not available")
	}

	if peerInfo.RelayIP == "" || peerInfo.RelayPort == 0 {
		return nil, fmt.Errorf("peer has no relay IP/port")
	}

	relayAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", peerInfo.RelayIP, peerInfo.RelayPort))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve relay address: %w", err)
	}

	// Also resolve public address for matching packets received via relay
	var peerAddr *net.UDPAddr
	if peerInfo.PublicIP != "" && peerInfo.PublicPort != 0 {
		peerAddr, _ = net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", peerInfo.PublicIP, peerInfo.PublicPort))
	}

	conn := &P2PConnection{
		PeerID:     peerID,
		Method:     MethodRelay,
		Status:     StatusConnected,
		RelayConn:  allocation,
		RelayAddr:  relayAddr,
		PeerAddr:   peerAddr, // Store public address for matching received packets
		PublicIP:   peerInfo.PublicIP,
		PublicPort: peerInfo.PublicPort,
		RelayIP:    peerInfo.RelayIP,
		RelayPort:  peerInfo.RelayPort,
		LastUsed:   time.Now(),
	}

	// Send test packet via relay
	// Note: WriteTo on PacketConn is usually non-blocking, but timeout can occur
	// if there's network routing issue or firewall blocking
	testPacket := []byte(fmt.Sprintf("RELAY-PING-%d", time.Now().Unix()))
	_, err = allocation.WriteTo(testPacket, relayAddr)
	if err != nil {
		// Check if it's timeout - might be network/firewall issue
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			pm.logger.Printf("⚠️  Relay packet send timeout - may be network/firewall issue")
			// Continue anyway - connection might still work for receiving
			// The timeout could be false positive if TURN server is just slow
		} else {
			return nil, fmt.Errorf("failed to send relay packet: %w", err)
		}
	} else {
		pm.logger.Printf("✅ Relay test packet sent successfully")
	}

	return conn, nil
}

// GetConnection retrieves an existing connection
func (pm *P2PManager) GetConnection(peerID string) (*P2PConnection, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	conn, exists := pm.connections[peerID]
	return conn, exists
}

// SendPacket sends a packet to a peer via P2P connection
func (pm *P2PManager) SendPacket(peerID string, packet []byte) error {
	conn, exists := pm.GetConnection(peerID)
	if !exists || conn.Status != StatusConnected {
		return fmt.Errorf("no active connection to peer %s", peerID)
	}

	conn.mu.Lock()
	conn.LastUsed = time.Now()
	conn.mu.Unlock()

	var err error
	if conn.Method == MethodHole {
		_, err = conn.Conn.WriteTo(packet, conn.PeerAddr)
	} else {
		_, err = conn.RelayConn.WriteTo(packet, conn.RelayAddr)
	}

	if err != nil {
		// Check if it's timeout - log but still mark as failed
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			pm.logger.Printf("⚠️  Packet send timeout to %s via %s: %v", peerID, conn.Method, err)
		}
		conn.mu.Lock()
		conn.Status = StatusFailed
		conn.mu.Unlock()
		return fmt.Errorf("failed to send packet: %w", err)
	}

	return nil
}

// StartPacketReceiver starts receiving packets from P2P connections
func (pm *P2PManager) StartPacketReceiver(onPacket func(peerID string, packet []byte)) {
	// Receiver for hole punching (UDP)
	go func() {
		if pm.localConn == nil {
			return
		}

		// Clear any leftover read deadlines from hole punching attempts
		pm.localConn.SetReadDeadline(time.Time{})

		buffer := make([]byte, 1500)
		for {
			n, addr, err := pm.localConn.ReadFrom(buffer)
			if err != nil {
				// Check if it's a timeout error - these are expected and should be ignored
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout is expected when waiting for packets, silently continue
					continue
				}
				// Log other errors
				pm.logger.Printf("UDP read error: %v", err)
				continue
			}

			// Find connection by address
			pm.mu.RLock()
			var peerID string
			for pid, conn := range pm.connections {
				if conn.Method == MethodHole && conn.PeerAddr != nil && conn.PeerAddr.String() == addr.String() {
					peerID = pid
					break
				}
			}
			pm.mu.RUnlock()

			if peerID != "" {
				onPacket(peerID, buffer[:n])
			} else {
				pm.logger.Printf("Received packet from unknown address: %s", addr)
			}
		}
	}()

	// Receiver for relay (TURN)
	go func() {
		if pm.turnClient == nil {
			return
		}

		allocation := pm.turnClient.GetAllocation()
		if allocation == nil {
			return
		}

		buffer := make([]byte, 1500)
		for {
			n, addr, err := allocation.ReadFrom(buffer)
			if err != nil {
				// Check if it's a timeout error - these are expected and should be ignored
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout is expected when waiting for packets, silently continue
					continue
				}
				// Log other errors
				pm.logger.Printf("Relay read error: %v", err)
				continue
			}

			// Find connection by relay address or public address
			// TURN relay may return either relay address or peer's public address
			pm.mu.RLock()
			var peerID string
			for pid, conn := range pm.connections {
				if conn.Method == MethodRelay {
					// Try matching with relay address
					if conn.RelayAddr != nil && conn.RelayAddr.String() == addr.String() {
						peerID = pid
						break
					}
					// Also try matching with public address (TURN may return peer's public addr)
					if conn.PeerAddr != nil && conn.PeerAddr.String() == addr.String() {
						peerID = pid
						break
					}
					// Try matching IP only (in case port is different)
					if conn.RelayAddr != nil {
						relayIP := conn.RelayAddr.IP.String()
						if udpAddr, ok := addr.(*net.UDPAddr); ok {
							addrIP := udpAddr.IP.String()
							if relayIP == addrIP {
								peerID = pid
								pm.logger.Printf("Matched relay connection by IP: %s -> %s (port may differ)", relayIP, addrIP)
								break
							}
						}
					}
				}
			}
			pm.mu.RUnlock()

			if peerID != "" {
				pm.logger.Printf("📥 Received packet via relay from %s (%s)", peerID, addr)
				onPacket(peerID, buffer[:n])
			} else {
				pm.logger.Printf("⚠️  Received packet from unknown relay address: %s (trying to match...)", addr)
				// Try to find any relay connection and use it (fallback)
				// This handles cases where TURN returns a different address format
				pm.mu.RLock()
				relayConnections := make([]string, 0)
				for pid, conn := range pm.connections {
					if conn.Method == MethodRelay && conn.Status == StatusConnected {
						relayConnections = append(relayConnections, pid)
					}
				}
				pm.mu.RUnlock()
				
				// If we only have one relay connection, assume it's from that peer
				if len(relayConnections) == 1 {
					peerID = relayConnections[0]
					pm.logger.Printf("📥 Assumed packet from %s (only relay connection)", peerID)
					onPacket(peerID, buffer[:n])
				}
			}
		}
	}()
}

// RemoveConnection removes a P2P connection
func (pm *P2PManager) RemoveConnection(peerID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if conn, exists := pm.connections[peerID]; exists {
		conn.mu.Lock()
		conn.Status = StatusDisconnected
		conn.mu.Unlock()
		delete(pm.connections, peerID)
		pm.logger.Printf("Removed P2P connection: %s", peerID)
	}
}

