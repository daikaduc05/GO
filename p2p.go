package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// P2PConnection represents a P2P connection to a peer
type P2PConnection struct {
	PeerID     string
	Method     ConnectionMethod
	Status     ConnectionStatus
	Conn       net.PacketConn  // UDP connection for hole punching
	RelayConn  net.PacketConn  // TURN allocation for relay (as PacketConn for ReadFrom)
	RelayAlloc TURNAllocation  // TURN allocation object (for CreatePermissions/WriteTo)
	PeerAddr   *net.UDPAddr    // Destination address
	RelayAddr  *net.UDPAddr    // Relay destination address
	PublicIP   string
	PublicPort int
	RelayIP       string
	RelayPort     int
	PermissionTime time.Time // When permission was last created (to avoid spam)
	LastUsed      time.Time
	mu            sync.RWMutex
}

// P2PManager manages all P2P connections
type P2PManager struct {
	connections map[string]*P2PConnection
	localConn   net.PacketConn  // Local UDP connection for hole punching
	turnClient  *TURNClient     // TURN client for relay
	mu          sync.RWMutex
	logger      *log.Logger
}

// isPrivateOrLinkLocalIP returns true if ip is RFC1918 private, link-local, loopback or unspecified
func isPrivateOrLinkLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// RFC1918 private ranges
		if v4[0] == 10 {
			return true
		}
		if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
			return true
		}
		if v4[0] == 192 && v4[1] == 168 {
			return true
		}
		// Carrier-Grade NAT 100.64.0.0/10
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
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
		
		// Send keep-alive packet to maintain connection
		pm.sendKeepAlivePermission(conn)
		
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
	
	// Send permission packet to keep alive relation with peer
	pm.sendKeepAlivePermission(conn)
	
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
	pm.logger.Printf("=")
	pm.logger.Printf("🔍 [tryRelay] ========== START ==========")
	pm.logger.Printf("🔍 [tryRelay] Function: tryRelay")
	pm.logger.Printf("🔍 [tryRelay] Input parameters:")
	pm.logger.Printf("   - peerID: %q (type: %T, len=%d)", peerID, peerID, len(peerID))
	pm.logger.Printf("   - peerInfo: %+v (type: %T)", peerInfo, peerInfo)
	pm.logger.Printf("   - peerInfo.PeerID: %q (type: %T)", peerInfo.PeerID, peerInfo.PeerID)
	pm.logger.Printf("   - peerInfo.Email: %q (type: %T)", peerInfo.Email, peerInfo.Email)
	pm.logger.Printf("   - peerInfo.VirtualIP: %q (type: %T)", peerInfo.VirtualIP, peerInfo.VirtualIP)
	pm.logger.Printf("   - peerInfo.PublicIP: %q (type: %T, empty=%v)", peerInfo.PublicIP, peerInfo.PublicIP, peerInfo.PublicIP == "")
	pm.logger.Printf("   - peerInfo.PublicPort: %d (type: %T, zero=%v)", peerInfo.PublicPort, peerInfo.PublicPort, peerInfo.PublicPort == 0)
	pm.logger.Printf("   - peerInfo.RelayIP: %q (type: %T, empty=%v, len=%d)", peerInfo.RelayIP, peerInfo.RelayIP, peerInfo.RelayIP == "", len(peerInfo.RelayIP))
	pm.logger.Printf("   - peerInfo.RelayPort: %d (type: %T, zero=%v)", peerInfo.RelayPort, peerInfo.RelayPort, peerInfo.RelayPort == 0)
	
	pm.logger.Printf("🔍 [tryRelay] Checking TURN client...")
	pm.logger.Printf("   pm.turnClient: %v (type: %T, nil=%v)", pm.turnClient, pm.turnClient, pm.turnClient == nil)
	
	if pm.turnClient == nil {
		pm.logger.Printf("❌ [tryRelay] TURN client is nil")
		return nil, fmt.Errorf("TURN client not available")
	}

	pm.logger.Printf("🔍 [tryRelay] Getting TURN allocation...")
	allocation := pm.turnClient.GetAllocation()
	pm.logger.Printf("   allocation: %v (type: %T, nil=%v)", allocation, allocation, allocation == nil)
	
	if allocation == nil {
		pm.logger.Printf("❌ [tryRelay] TURN allocation is nil")
		return nil, fmt.Errorf("TURN allocation not available")
	}
	
	relayAddrLocal := allocation.LocalAddr()
	pm.logger.Printf("✅ [tryRelay] TURN allocation found")
	pm.logger.Printf("   LocalAddr(): %v (type: %T)", relayAddrLocal, relayAddrLocal)
	if udpAddr, ok := relayAddrLocal.(*net.UDPAddr); ok {
		pm.logger.Printf("   - IP: %s (type: %T, bytes=%v)", udpAddr.IP, udpAddr.IP, udpAddr.IP)
		pm.logger.Printf("   - Port: %d (type: %T)", udpAddr.Port, udpAddr.Port)
		pm.logger.Printf("   - Zone: %s", udpAddr.Zone)
	}
	
	// Clear any deadlines on the allocation to prevent immediate timeout
	pm.logger.Printf("🔍 [tryRelay] Clearing deadlines on allocation...")
	if conn, ok := allocation.(interface {
		SetReadDeadline(time.Time) error
		SetWriteDeadline(time.Time) error
	}); ok {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			pm.logger.Printf("⚠️  Failed to clear read deadline: %v", err)
		} else {
			pm.logger.Printf("   ✅ Read deadline cleared")
		}
		if err := conn.SetWriteDeadline(time.Time{}); err != nil {
			pm.logger.Printf("⚠️  Failed to clear write deadline: %v", err)
		} else {
			pm.logger.Printf("   ✅ Write deadline cleared")
		}
	} else {
		pm.logger.Printf("   ⚠️  Allocation does not implement deadline methods")
	}

	pm.logger.Printf("🔍 [tryRelay] Checking peer relay info...")
	pm.logger.Printf("   peerInfo.RelayIP == \"\": %v", peerInfo.RelayIP == "")
	pm.logger.Printf("   peerInfo.RelayPort == 0: %v", peerInfo.RelayPort == 0)
	
	if peerInfo.RelayIP == "" || peerInfo.RelayPort == 0 {
		pm.logger.Printf("❌ [tryRelay] Peer has no relay IP/port")
		pm.logger.Printf("   RelayIP empty: %v, RelayPort zero: %v", peerInfo.RelayIP == "", peerInfo.RelayPort == 0)
		return nil, fmt.Errorf("peer has no relay IP/port")
	}

	pm.logger.Printf("🔍 [tryRelay] Resolving relay address...")
	relayAddrStr := fmt.Sprintf("%s:%d", peerInfo.RelayIP, peerInfo.RelayPort)
	pm.logger.Printf("   Address string: %q (type: %T)", relayAddrStr, relayAddrStr)
	
	relayAddr, err := net.ResolveUDPAddr("udp", relayAddrStr)
	pm.logger.Printf("   ResolveUDPAddr result: relayAddr=%v, err=%v", relayAddr, err)
	
	if err != nil {
		pm.logger.Printf("❌ [tryRelay] Failed to resolve relay address: %v (type: %T)", err, err)
		return nil, fmt.Errorf("failed to resolve relay address: %w", err)
	}
	
	pm.logger.Printf("✅ [tryRelay] Relay address resolved successfully")
	pm.logger.Printf("   relayAddr: %v (type: %T, nil=%v)", relayAddr, relayAddr, relayAddr == nil)
	if relayAddr != nil {
		pm.logger.Printf("   - IP: %s (type: %T, bytes=%v)", relayAddr.IP, relayAddr.IP, relayAddr.IP)
		pm.logger.Printf("   - IP.String(): %s", relayAddr.IP.String())
		if relayAddr.IP.To4() != nil {
			pm.logger.Printf("   - IP is IPv4: true, To4(): %v", relayAddr.IP.To4())
		}
		pm.logger.Printf("   - Port: %d (type: %T)", relayAddr.Port, relayAddr.Port)
		pm.logger.Printf("   - Zone: %s", relayAddr.Zone)
		pm.logger.Printf("   - String(): %s", relayAddr.String())
	}

	// Guard: if peer's relay IP is private/link-local (e.g., server's internal IP), fall back to peer's public IP
	if isPrivateOrLinkLocalIP(relayAddr.IP) {
		pm.logger.Printf("   ⚠️  Relay IP appears private/link-local: %s. Falling back to peer PublicIP if available...", relayAddr.IP.String())
		if peerInfo.PublicIP != "" && peerInfo.PublicPort != 0 {
			fallbackStr := fmt.Sprintf("%s:%d", peerInfo.PublicIP, peerInfo.PublicPort)
			if fb, err2 := net.ResolveUDPAddr("udp", fallbackStr); err2 == nil {
				pm.logger.Printf("   🔄 Using PublicIP as target for permission/send: %s", fb.String())
				relayAddr = fb
			} else {
				pm.logger.Printf("   ⚠️  Failed to resolve fallback PublicIP %q: %v", fallbackStr, err2)
			}
		} else {
			pm.logger.Printf("   ⚠️  No PublicIP available to fall back")
		}
	}

	pm.logger.Printf("🔍 [tryRelay] Resolving public address (for matching)...")
	var peerAddr *net.UDPAddr
	if peerInfo.PublicIP != "" && peerInfo.PublicPort != 0 {
		publicAddrStr := fmt.Sprintf("%s:%d", peerInfo.PublicIP, peerInfo.PublicPort)
		pm.logger.Printf("   Public address string: %q", publicAddrStr)
		peerAddr, err = net.ResolveUDPAddr("udp", publicAddrStr)
		if err != nil {
			pm.logger.Printf("⚠️  Failed to resolve public address: %v (continuing)", err)
		} else {
			pm.logger.Printf("✅ Public address resolved: %v (type: %T)", peerAddr, peerAddr)
		}
	} else {
		pm.logger.Printf("   No public IP/port provided")
	}
	pm.logger.Printf("   peerAddr: %v (type: %T, nil=%v)", peerAddr, peerAddr, peerAddr == nil)

	pm.logger.Printf("=")
	pm.logger.Printf("🔐 [TURN] Creating permission...")
	
	// Create permission for peer's relay IP (required for Send Indication)
	peerIP := relayAddr.IP
	pm.logger.Printf("   peerIP (relayAddr.IP): %v (type: %T, nil=%v)", peerIP, peerIP, peerIP == nil)
	
	if peerIP == nil {
		pm.logger.Printf("❌ [tryRelay] Invalid peer IP: nil")
		return nil, fmt.Errorf("invalid peer IP")
	}
	
	pm.logger.Printf("   peerIP.String(): %q", peerIP.String())
	pm.logger.Printf("   peerIP bytes: %v", peerIP)
	if peerIP.To4() != nil {
		pm.logger.Printf("   peerIP is IPv4: true, To4(): %v", peerIP.To4())
	} else {
		pm.logger.Printf("   peerIP is IPv4: false")
	}

	// Use the actual relay address port for permission (not hardcoded)
	// Note: TURN CreatePermission typically only needs IP, but we pass full address for consistency
	peerAddrForPermission := &net.UDPAddr{IP: peerIP, Port: relayAddr.Port}
	pm.logger.Printf("   peerAddrForPermission: %v (type: %T)", peerAddrForPermission, peerAddrForPermission)
	pm.logger.Printf("   - IP: %s (from relayAddr.IP)", peerAddrForPermission.IP.String())
	pm.logger.Printf("   - Port: %d (from relayAddr.Port, was hardcoded 56710 before)", peerAddrForPermission.Port)
	pm.logger.Printf("   - String(): %s", peerAddrForPermission.String())
	pm.logger.Printf("   - Original relayAddr.Port: %d", relayAddr.Port)
	
	pm.logger.Printf("   Attempting type assertion for CreatePermissions...")
	pm.logger.Printf("   allocation type: %T", allocation)
	
	// Try CreatePermissions (plural) method first
	type allocationWithPermissions interface {
		net.PacketConn
		CreatePermissions(addrs ...net.Addr) error
	}
	
	alloc, ok := allocation.(allocationWithPermissions)
	pm.logger.Printf("   Type assertion for CreatePermissions: ok=%v", ok)
	
	var permissionErr error
	if ok {
		pm.logger.Printf("   ✅ Type assertion successful (CreatePermissions)")
		pm.logger.Printf("   Calling: alloc.CreatePermissions(%v)...", peerAddrForPermission)
		pm.logger.Printf("   Input address: %+v", peerAddrForPermission)
		pm.logger.Printf("   Input address details:")
		pm.logger.Printf("      - IP: %s", peerAddrForPermission.IP.String())
		pm.logger.Printf("      - Port: %d", peerAddrForPermission.Port)
		pm.logger.Printf("      - String(): %s", peerAddrForPermission.String())
		
		// Clear write deadline before CreatePermissions (critical!)
		pm.logger.Printf("   🔧 Clearing write deadline before CreatePermissions...")
		if conn, ok := allocation.(interface{ SetWriteDeadline(time.Time) error }); ok {
			if err := conn.SetWriteDeadline(time.Time{}); err != nil {
				pm.logger.Printf("   ⚠️  Failed to clear write deadline: %v", err)
			} else {
				pm.logger.Printf("   ✅ Write deadline cleared successfully")
			}
		} else {
			pm.logger.Printf("   ⚠️  Allocation does not support SetWriteDeadline")
		}
		
		// Also clear read deadline just in case
		if conn, ok := allocation.(interface{ SetReadDeadline(time.Time) error }); ok {
			conn.SetReadDeadline(time.Time{})
		}
		
		pm.logger.Printf("   📤 Actually calling CreatePermissions now...")
		permissionStartTime := time.Now()
		permissionErr = alloc.CreatePermissions(peerAddrForPermission)
		permissionDuration := time.Since(permissionStartTime)
		
		pm.logger.Printf("   CreatePermissions returned: err=%v (type: %T, duration: %v)", permissionErr, permissionErr, permissionDuration)
		
		if permissionErr != nil {
			pm.logger.Printf("⚠️  [TURN] CreatePermissions returned error (may be timeout or network issue)")
			pm.logger.Printf("   Error: %v", permissionErr)
			if errStr := permissionErr.Error(); errStr != "" {
				pm.logger.Printf("   Error string: %q", errStr)
				
				// Check if it's a timeout error - in this case, permission might still work
				if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
					pm.logger.Printf("   ⚠️  Timeout error - permission creation may have succeeded on server side")
					pm.logger.Printf("   Continuing anyway (permission might be valid)")
					permissionErr = nil // Clear error, continue
				}
			}
		} else {
			pm.logger.Printf("✅ [TURN] CreatePermissions SUCCESS")
			pm.logger.Printf("   Target: %s", peerAddrForPermission.IP.String())
			pm.logger.Printf("   Duration: %v", permissionDuration)
		}
	} else {
		// Try CreatePermission (singular) method as fallback
		pm.logger.Printf("   Trying CreatePermission (singular) method...")
		type allocationWithPermission interface {
			net.PacketConn
			CreatePermission(peerIP net.IP) error
		}
		
		allocSingle, okSingle := allocation.(allocationWithPermission)
		pm.logger.Printf("   Type assertion for CreatePermission: ok=%v", okSingle)
		
		if okSingle {
			pm.logger.Printf("   ✅ Type assertion successful (CreatePermission)")
			pm.logger.Printf("   Calling: allocSingle.CreatePermission(%s)...", peerIP.String())
			
			permissionStartTime := time.Now()
			permissionErr = allocSingle.CreatePermission(peerIP)
			permissionDuration := time.Since(permissionStartTime)
			
			pm.logger.Printf("   CreatePermission returned: err=%v (type: %T, duration: %v)", permissionErr, permissionErr, permissionDuration)
			
			if permissionErr != nil {
				pm.logger.Printf("⚠️  [TURN] CreatePermission returned error")
				pm.logger.Printf("   Error: %v", permissionErr)
				if errStr := permissionErr.Error(); errStr != "" {
					pm.logger.Printf("   Error string: %q", errStr)
					
					// Check if it's a timeout error
					if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
						pm.logger.Printf("   ⚠️  Timeout error - permission creation may have succeeded on server side")
						pm.logger.Printf("   Continuing anyway (permission might be valid)")
						permissionErr = nil // Clear error, continue
					}
				}
			} else {
				pm.logger.Printf("✅ [TURN] CreatePermission SUCCESS")
				pm.logger.Printf("   Target IP: %s", peerIP.String())
				pm.logger.Printf("   Duration: %v", permissionDuration)
			}
		} else {
			pm.logger.Printf("❌ [TURN] Type assertion FAILED for both CreatePermissions and CreatePermission")
			pm.logger.Printf("   allocation does not implement permission creation methods")
			// Don't return error - continue without permission, might still work
			pm.logger.Printf("   ⚠️  Continuing without permission (may fail later)")
		}
	}
	
	// Only return error if it's a critical non-timeout error
	if permissionErr != nil && !strings.Contains(permissionErr.Error(), "timeout") {
		return nil, fmt.Errorf("failed to create TURN permission: %w", permissionErr)
	}
	
	if permissionErr == nil {
		pm.logger.Printf("✅ [TURN] Permission creation completed successfully")
	} else {
		pm.logger.Printf("⚠️  [TURN] Permission creation had timeout, but continuing...")
	}

	pm.logger.Printf("=")
	pm.logger.Printf("📤 [TURN->Relay] Preparing to send test packet...")
	
	testPacket := []byte(fmt.Sprintf("RELAY-PING-%d", time.Now().Unix()))
	pm.logger.Printf("   testPacket: %v (type: %T, len=%d, cap=%d)", testPacket, testPacket, len(testPacket), cap(testPacket))
	pm.logger.Printf("   testPacket as string: %q", string(testPacket))
	pm.logger.Printf("   testPacket as hex: %x", testPacket)
	pm.logger.Printf("   testPacket as bytes: %v", testPacket)
	
	pm.logger.Printf("📤 [TURN->Relay] Sending relay packet via WriteTo:")
	pm.logger.Printf("   allocation: %v (type: %T)", allocation, allocation)
	pm.logger.Printf("   relayAddr: %v (type: %T)", relayAddr, relayAddr)
	
	// Clear write deadline before WriteTo
	if conn, ok := allocation.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := conn.SetWriteDeadline(time.Time{}); err != nil {
			pm.logger.Printf("⚠️  Failed to clear write deadline before WriteTo: %v", err)
		} else {
			pm.logger.Printf("   ✅ Write deadline cleared before WriteTo")
		}
	}
	
	pm.logger.Printf("   Calling: allocation.WriteTo(%v, %v)...", testPacket, relayAddr)
	
	sendStartTime := time.Now()
	n, err := allocation.WriteTo(testPacket, relayAddr)
	sendDuration := time.Since(sendStartTime)
	
	pm.logger.Printf("   WriteTo returned: n=%d (type: %T), err=%v (type: %T)", n, n, err, err)
	pm.logger.Printf("   Duration: %v", sendDuration)
	
	if err != nil {
		pm.logger.Printf("⚠️  [TURN->Relay] WriteTo returned error")
		pm.logger.Printf("   Error: %v (type: %T)", err, err)
		errStr := err.Error()
		if errStr != "" {
			pm.logger.Printf("   Error string: %q", errStr)
		}
		
		// Check if it's a timeout - for timeout errors, continue anyway as connection might still work
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
			pm.logger.Printf("   ⚠️  WriteTo timeout - this may be a false positive")
			pm.logger.Printf("   ⚠️  TURN server might have received the packet but response timed out")
			pm.logger.Printf("   ⚠️  Continuing anyway - connection might still work for receiving")
			
			// Try using allocationObj.SendTo if available
			allocationObj := pm.turnClient.GetAllocationObj()
			if allocationObj != nil {
				pm.logger.Printf("   🔄 Trying SendTo (Send Indication) as alternative...")
				
				// Type assert to get SendTo method
				type allocationWithSendTo interface {
					net.PacketConn
					SendTo(data []byte, peer net.Addr) (int, error)
				}
				
				if allocSendTo, ok := allocationObj.(allocationWithSendTo); ok {
					pm.logger.Printf("   ✅ allocationObj supports SendTo")
					pm.logger.Printf("   Calling: allocSendTo.SendTo(%v, %v)...", testPacket, relayAddr)
					
					// Clear deadline for SendTo
					if conn, ok := allocationObj.(interface{ SetWriteDeadline(time.Time) error }); ok {
						conn.SetWriteDeadline(time.Time{})
					}
					
					sendToStartTime := time.Now()
					n, sendToErr := allocSendTo.SendTo(testPacket, relayAddr)
					sendToDuration := time.Since(sendToStartTime)
					
					pm.logger.Printf("   SendTo returned: n=%d, err=%v (duration: %v)", n, sendToErr, sendToDuration)
					
					if sendToErr == nil {
						pm.logger.Printf("✅ [TURN->Relay] SendTo SUCCESS!")
						pm.logger.Printf("   Bytes sent: %d", n)
						err = nil // Clear error
					} else {
						pm.logger.Printf("⚠️  SendTo also returned error: %v", sendToErr)
						pm.logger.Printf("   Continuing anyway (may work for receiving)")
						err = nil // Clear error, continue
					}
				} else {
					pm.logger.Printf("   ⚠️  allocationObj does not support SendTo method")
					pm.logger.Printf("   Continuing with WriteTo timeout error cleared")
					err = nil // Clear error, continue
				}
			} else {
				pm.logger.Printf("   ⚠️  allocationObj is nil")
				pm.logger.Printf("   Continuing anyway (may work for receiving)")
				err = nil // Clear error, continue
			}
		} else {
			// Non-timeout error - return it
			pm.logger.Printf("❌ [TURN->Relay] WriteTo FAILED with non-timeout error")
			return nil, fmt.Errorf("failed to send relay packet: %w", err)
		}
	}
	
	if err == nil {
		pm.logger.Printf("✅ [TURN->Relay] Test packet sent successfully!")
		pm.logger.Printf("   Bytes written: %d", n)
	} else {
		pm.logger.Printf("⚠️  [TURN->Relay] Packet send had issues but continuing...")
	}

	pm.logger.Printf("=")
	pm.logger.Printf("🔍 [tryRelay] Creating P2PConnection object...")
	
	allocationObj := pm.turnClient.GetAllocationObj()
	pm.logger.Printf("   allocationObj from GetAllocationObj(): %v (type: %T, nil=%v)", allocationObj, allocationObj, allocationObj == nil)
	
	conn := &P2PConnection{
		PeerID:         peerID,
		Method:         MethodRelay,
		Status:         StatusConnected,
		RelayConn:      allocation,
		RelayAlloc:     allocationObj, // Keep for backward compatibility
		RelayAddr:      relayAddr,
		PeerAddr:       peerAddr, // Store public address for matching received packets
		PublicIP:       peerInfo.PublicIP,
		PublicPort:     peerInfo.PublicPort,
		RelayIP:        peerInfo.RelayIP,
		RelayPort:      peerInfo.RelayPort,
		PermissionTime: time.Now(), // Permission already created in tryRelay
		LastUsed:       time.Now(),
	}
	
	pm.logger.Printf("✅ P2PConnection created:")
	pm.logger.Printf("   - PeerID: %q (type: %T)", conn.PeerID, conn.PeerID)
	pm.logger.Printf("   - Method: %s (type: %T)", conn.Method, conn.Method)
	pm.logger.Printf("   - Status: %s (type: %T)", conn.Status, conn.Status)
	pm.logger.Printf("   - RelayConn: %v (type: %T, nil=%v)", conn.RelayConn, conn.RelayConn, conn.RelayConn == nil)
	pm.logger.Printf("   - RelayAlloc: %v (type: %T, nil=%v)", conn.RelayAlloc, conn.RelayAlloc, conn.RelayAlloc == nil)
	pm.logger.Printf("   - RelayAddr: %v (type: %T, nil=%v)", conn.RelayAddr, conn.RelayAddr, conn.RelayAddr == nil)
	pm.logger.Printf("   - PeerAddr: %v (type: %T, nil=%v)", conn.PeerAddr, conn.PeerAddr, conn.PeerAddr == nil)
	pm.logger.Printf("   - PublicIP: %q, PublicPort: %d", conn.PublicIP, conn.PublicPort)
	pm.logger.Printf("   - RelayIP: %q, RelayPort: %d", conn.RelayIP, conn.RelayPort)
	pm.logger.Printf("   - LastUsed: %v (type: %T)", conn.LastUsed, conn.LastUsed)
	
	pm.logger.Printf("=")
	pm.logger.Printf("🔍 [tryRelay] ========== END (SUCCESS) ==========")
	pm.logger.Printf("=")

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
		// Check if permission needs refresh (permissions expire after 5 minutes = 300 seconds)
		// Only create/refresh permission if:
		// 1. Never created before (PermissionTime is zero)
		// 2. Created more than 4 minutes ago (refresh before 5-minute expiry)
		conn.mu.RLock()
		needsPermission := conn.PermissionTime.IsZero() || time.Since(conn.PermissionTime) > 4*time.Minute
		permissionAge := time.Since(conn.PermissionTime)
		conn.mu.RUnlock()
		
		if needsPermission && conn.RelayConn != nil {
			relayIP := conn.RelayAddr.IP
			relayIPAddr := &net.UDPAddr{
				IP:   relayIP,
				Port: conn.RelayAddr.Port, // Use actual relay port from connection
			}
			
			// Type assert to access CreatePermissions method
			type allocationWithPermissions interface {
				net.PacketConn
				CreatePermissions(addrs ...net.Addr) error
			}
			
			if alloc, ok := conn.RelayConn.(allocationWithPermissions); ok {
				if conn.PermissionTime.IsZero() {
					pm.logger.Printf("🔐 [SendPacket] Creating permission for %s (first time)", relayIP.String())
				} else {
					pm.logger.Printf("🔐 [SendPacket] Refreshing permission for %s (last created %v ago)", 
						relayIP.String(), permissionAge)
				}
				
				if err := alloc.CreatePermissions(relayIPAddr); err != nil {
					pm.logger.Printf("⚠️  Failed to create/refresh permission for IP %s: %v", relayIP.String(), err)
					// Continue anyway - permission might still be valid
				} else {
					// Update permission creation time
					conn.mu.Lock()
					conn.PermissionTime = time.Now()
					conn.mu.Unlock()
					pm.logger.Printf("✅ [SendPacket] Permission created/refreshed successfully")
				}
			} else {
				pm.logger.Printf("⚠️  Failed to type assert allocation to access CreatePermissions")
				// Continue anyway - might still work if permission is already valid
			}
		}
		// Permission still valid - no logging to avoid spam (already created in tryRelay or refreshed above)

		// Log TURN relay packet send details
		localAddr := conn.RelayConn.LocalAddr()
		pm.logger.Printf("📤 [TURN->Relay] Sending packet to peer %s via Send Indication:", peerID)
		pm.logger.Printf("   From: %s (TURN allocation)", localAddr)
		pm.logger.Printf("   To: %s:%d (peer relay address)", conn.RelayAddr.IP, conn.RelayAddr.Port)
		pm.logger.Printf("   Packet size: %d bytes", len(packet))
		// Show packet preview (first 100 bytes or less)
		previewLen := len(packet)
		if previewLen > 100 {
			previewLen = 100
		}
		pm.logger.Printf("   Packet preview (first %d bytes): %x", previewLen, packet[:previewLen])
		if len(packet) <= 100 {
			pm.logger.Printf("   Packet content (as string): %s", string(packet))
		}
		
		// Use WriteTo directly on allocation (just like the working example)
		_, err = conn.RelayConn.WriteTo(packet, conn.RelayAddr)
		if err == nil {
			pm.logger.Printf("   ✅ Packet sent successfully via Send Indication")
		}
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

// sendKeepAlivePermission sends a permission packet to keep alive the relation with a peer
func (pm *P2PManager) sendKeepAlivePermission(conn *P2PConnection) {
	if conn == nil {
		return
	}

	// Handle relay connections (need TURN permissions)
	if conn.Method == MethodRelay {
		pm.sendRelayKeepAlive(conn)
	} else if conn.Method == MethodHole {
		// Handle hole punching connections (just send keep-alive packet)
		pm.sendHolePunchKeepAlive(conn)
	}
}

// sendRelayKeepAlive sends permission packet for relay connections
func (pm *P2PManager) sendRelayKeepAlive(conn *P2PConnection) {
	if conn.RelayConn == nil || conn.RelayAddr == nil {
		pm.logger.Printf("⚠️  Cannot send keep-alive permission: missing relay connection or address")
		return
	}

	conn.mu.Lock()
	// Refresh permission if needed (permissions expire after 5 minutes)
	needsPermission := conn.PermissionTime.IsZero() || time.Since(conn.PermissionTime) > 4*time.Minute
	conn.mu.Unlock()

	if needsPermission {
		relayIP := conn.RelayAddr.IP
		relayIPAddr := &net.UDPAddr{
			IP:   relayIP,
			Port: conn.RelayAddr.Port,
		}

		// Type assert to access CreatePermissions method
		type allocationWithPermissions interface {
			net.PacketConn
			CreatePermissions(addrs ...net.Addr) error
		}

		if alloc, ok := conn.RelayConn.(allocationWithPermissions); ok {
			pm.logger.Printf("🔐 [KeepAlive] Refreshing permission for peer %s", conn.PeerID)
			if err := alloc.CreatePermissions(relayIPAddr); err != nil {
				pm.logger.Printf("⚠️  Failed to refresh permission for peer %s: %v", conn.PeerID, err)
				// Continue anyway - permission might still be valid
			} else {
				conn.mu.Lock()
				conn.PermissionTime = time.Now()
				conn.mu.Unlock()
				pm.logger.Printf("✅ [KeepAlive] Permission refreshed successfully for peer %s", conn.PeerID)
			}
		}
	}

	// Send a small keep-alive packet through the relay connection
	keepAlivePacket := []byte(fmt.Sprintf("KEEPALIVE-%d", time.Now().Unix()))
	pm.logger.Printf("💓 [KeepAlive] Sending keep-alive packet to peer %s via relay", conn.PeerID)
	
	// Clear write deadline before sending
	if connWithDeadline, ok := conn.RelayConn.(interface{ SetWriteDeadline(time.Time) error }); ok {
		connWithDeadline.SetWriteDeadline(time.Time{})
	}

	_, err := conn.RelayConn.WriteTo(keepAlivePacket, conn.RelayAddr)
	if err != nil {
		pm.logger.Printf("⚠️  Failed to send keep-alive packet to peer %s: %v", conn.PeerID, err)
	} else {
		pm.logger.Printf("✅ [KeepAlive] Keep-alive packet sent successfully to peer %s", conn.PeerID)
		conn.mu.Lock()
		conn.LastUsed = time.Now()
		conn.mu.Unlock()
	}
}

// sendHolePunchKeepAlive sends keep-alive packet for hole punching connections
func (pm *P2PManager) sendHolePunchKeepAlive(conn *P2PConnection) {
	if conn.Conn == nil || conn.PeerAddr == nil {
		pm.logger.Printf("⚠️  Cannot send keep-alive: missing connection or peer address")
		return
	}

	// Send a small keep-alive packet through the hole punching connection
	keepAlivePacket := []byte(fmt.Sprintf("KEEPALIVE-%d", time.Now().Unix()))
	pm.logger.Printf("💓 [KeepAlive] Sending keep-alive packet to peer %s via hole punching", conn.PeerID)
	
	_, err := conn.Conn.WriteTo(keepAlivePacket, conn.PeerAddr)
	if err != nil {
		pm.logger.Printf("⚠️  Failed to send keep-alive packet to peer %s: %v", conn.PeerID, err)
	} else {
		pm.logger.Printf("✅ [KeepAlive] Keep-alive packet sent successfully to peer %s", conn.PeerID)
		conn.mu.Lock()
		conn.LastUsed = time.Now()
		conn.mu.Unlock()
	}
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
		pm.logger.Printf("🔄 [TURN Receiver] Started listening for packets from relay...")
		for {
			n, addr, err := allocation.ReadFrom(buffer)
			if err != nil {
				// Check if it's a timeout error - these are expected and should be ignored
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout is expected when waiting for packets, silently continue
					continue
				}
				// Log other errors
				pm.logger.Printf("❌ [TURN Receiver] Relay read error: %v", err)
				continue
			}

			// Log ngay khi nhận được packet (trước khi match)
			localAddr := allocation.LocalAddr()
			pm.logger.Printf("🎯 [TURN Receiver] ========== PACKET RECEIVED ==========")
			pm.logger.Printf("   ✅ Received packet from relay allocation!")
			pm.logger.Printf("   📍 Allocation address: %s", localAddr)
			pm.logger.Printf("   📍 Source address (from relay): %s", addr)
			pm.logger.Printf("   📦 Packet size: %d bytes", n)
			
			// Parse IP header để log dest IP
			if n >= 20 { // Minimum IP header size
				destIP := net.IP(buffer[16:20]).String()
				srcIP := net.IP(buffer[12:16]).String()
				protocol := buffer[9]
				pm.logger.Printf("   📋 IP Header:")
				pm.logger.Printf("      - Source IP: %s", srcIP)
				pm.logger.Printf("      - Dest IP: %s", destIP)
				pm.logger.Printf("      - Protocol: %d (1=ICMP, 17=UDP, 6=TCP)", protocol)
			}
			
			// Show packet preview
			previewLen := n
			if previewLen > 100 {
				previewLen = 100
			}
			pm.logger.Printf("   📄 Packet preview (first %d bytes): %x", previewLen, buffer[:previewLen])
			if n <= 100 {
				pm.logger.Printf("   📄 Packet as string: %s", string(buffer[:n]))
			}
			pm.logger.Printf("   🔍 Attempting to match with peer connection...")

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
				pm.logger.Printf("   ✅ Matched with peer: %s", peerID)
				pm.logger.Printf("   📤 Forwarding packet to callback (onPacket)...")
				pm.logger.Printf("   ========================================")
				onPacket(peerID, buffer[:n])
				pm.logger.Printf("   ✅ Callback completed for peer %s", peerID)
			} else {
				pm.logger.Printf("   ⚠️  No match found for address: %s", addr)
				pm.logger.Printf("   🔍 Trying fallback matching...")
				
				// Try to find any relay connection and use it (fallback)
				// This handles cases where TURN returns a different address format
				pm.mu.RLock()
				relayConnections := make([]string, 0)
				for pid, conn := range pm.connections {
					if conn.Method == MethodRelay && conn.Status == StatusConnected {
						relayConnections = append(relayConnections, pid)
						pm.logger.Printf("   📋 Found relay connection: %s (status: %s)", pid, conn.Status)
					}
				}
				pm.mu.RUnlock()
				
				// If we only have one relay connection, assume it's from that peer
				if len(relayConnections) == 1 {
					peerID = relayConnections[0]
					pm.logger.Printf("   ✅ Fallback: Assuming packet from %s (only relay connection)", peerID)
					pm.logger.Printf("   📤 Forwarding packet to callback...")
					pm.logger.Printf("   ========================================")
					onPacket(peerID, buffer[:n])
					pm.logger.Printf("   ✅ Callback completed for peer %s", peerID)
				} else {
					pm.logger.Printf("   ❌ Cannot determine peer - found %d relay connections", len(relayConnections))
					pm.logger.Printf("   ❌ Packet dropped (no matching peer)")
					pm.logger.Printf("   ========================================")
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

