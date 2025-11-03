package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pion/turn/v2"
)

// TURNAllocation interface for TURN allocation with CreatePermissions/WriteTo methods
// Matches the actual methods from pion/turn/v2 internal/client.UDPConn
type TURNAllocation interface {
	net.PacketConn
	// CreatePermissions creates TURN permissions for the given addresses
	// Per RFC 5766 Section 9: XOR-PEER-ADDRESS contains IP address (port portion is ignored)
	// The actual signature from pion/turn/v2 is: CreatePermissions(addrs ...net.Addr) error
	// We pass net.UDPAddr with the IP, and port is ignored by the server
	CreatePermissions(addrs ...net.Addr) error
}

// TURNClient handles TURN relay connection
type TURNClient struct {
	config        *Config
	client        *turn.Client
	udpConn       *net.UDPConn // UDP connection for TURN (may be separate from STUN)
	allocation    net.PacketConn // For backward compatibility (used as PacketConn)
	allocationObj TURNAllocation // Store actual allocation object for CreatePermissions/WriteTo
	logger        *log.Logger
}

// NewTURNClient creates a new TURN client
func NewTURNClient(config *Config, udpConn *net.UDPConn) *TURNClient {
	return &TURNClient{
		config: config,
		logger: log.New(os.Stdout, "[TURN] ", log.LstdFlags),
	}
}

// Connect establishes TURN allocation with proper 401 handling
// Implements long-term credential authentication (lt-cred-mech) per RFC 5389
func (tc *TURNClient) Connect(udpConn *net.UDPConn) error {
	if tc.config.TURNServer == "" {
		return fmt.Errorf("no TURN server configured")
	}

	tc.logger.Printf("Connecting to TURN server: %s", tc.config.TURNServer)
	tc.logger.Printf("Username: %s, Realm: %s", tc.config.TURNUser, tc.config.TURNRealm)

	// CRITICAL: Create a fresh UDP connection for TURN (like reference code)
	// This prevents any deadlines or state from STUN operations affecting TURN
	// The reference code creates a new connection: net.ListenPacket("udp4", "0.0.0.0:0")
	tc.logger.Printf("🔧 Creating fresh UDP connection for TURN (to avoid STUN deadlines)...")
	turnUDPConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		tc.logger.Printf("⚠️  Failed to create fresh UDP connection for TURN, using provided connection: %v", err)
		tc.udpConn = udpConn // Fallback to provided connection
		turnUDPConn = udpConn
	} else {
		tc.logger.Printf("✅ Fresh UDP connection created for TURN: %s", turnUDPConn.LocalAddr())
		// Store it for cleanup and refresh operations
		tc.udpConn = turnUDPConn
	}

	// Create TURN client config
	// The pion/turn/v2 library automatically handles:
	// - 401 challenge-response
	// - MESSAGE-INTEGRITY computation with HMAC-SHA1(username:realm:password)
	// - FINGERPRINT inclusion in Allocate requests
	// - Nonce handling and retry logic
	clientConfig := &turn.ClientConfig{
		STUNServerAddr: tc.config.STUNServer,
		TURNServerAddr: tc.config.TURNServer,
		Username:       tc.config.TURNUser,
		Password:       tc.config.TURNPass,
		Conn:           turnUDPConn,
	}

	// Set realm if configured
	// If server responds with different realm in 401, library will use that
	if tc.config.TURNRealm != "" {
		clientConfig.Realm = tc.config.TURNRealm
		tc.logger.Printf("Using realm: %s (may be overwritten by server)", tc.config.TURNRealm)
	}

	// Create TURN client
	client, err := turn.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create TURN client: %w", err)
	}

	tc.client = client

	// Start listening (required before Allocate)
	tc.logger.Printf("Starting TURN client listener...")
	if err := client.Listen(); err != nil {
		return fmt.Errorf("failed to start TURN client listener: %w", err)
	}

	// Perform allocation with timeout
	tc.logger.Printf("Sending TURN Allocate request...")
	allocCtx, allocCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer allocCancel()

	allocChan := make(chan struct {
		conn net.PacketConn
		err  error
	}, 1)

	go func() {
		allocation, err := client.Allocate()
		if err != nil {
			allocChan <- struct {
				conn net.PacketConn
				err  error
			}{nil, err}
			return
		}
		// Store both PacketConn and allocation object
		allocChan <- struct {
			conn net.PacketConn
			err  error
		}{allocation, nil}
	}()

	// Wait for allocation or timeout
	select {
	case <-allocCtx.Done():
		return fmt.Errorf("TURN allocation timeout after 15s\n"+
			"Server: %s, Username: %s, Realm: %s",
			tc.config.TURNServer, tc.config.TURNUser, tc.config.TURNRealm)
	case result := <-allocChan:
		if result.err != nil {
			errStr := result.err.Error()
			if strings.Contains(errStr, "401") || strings.Contains(errStr, "Unauthorized") {
				return fmt.Errorf("TURN authentication failed (401 Unauthorized): %w\n"+
					"Verify credentials match coturn config:\n"+
					"  Username: %s\n"+
					"  Password: %s\n"+
					"  Realm: %s\n"+
					"  Server: %s",
					result.err, tc.config.TURNUser, tc.config.TURNPass,
					tc.config.TURNRealm, tc.config.TURNServer)
			}
			return fmt.Errorf("TURN allocation failed: %w", result.err)
		}
		
		if result.conn == nil {
			return fmt.Errorf("TURN allocation returned nil connection")
		}
		
		// After Allocate succeeds, save allocation to struct TURNClient
		// The allocation from client.Allocate() returns net.PacketConn
		// The actual type is *client.UDPConn which has CreatePermissions method
		// Type assert to TURNAllocation interface to get access to CreatePermissions
		allocObj, ok := result.conn.(TURNAllocation)
		if !ok {
			// If type assertion fails, allocation doesn't have CreatePermissions method
			// This shouldn't happen with pion/turn/v2, but handle it gracefully
			return fmt.Errorf("TURN allocation does not implement TURNAllocation interface (missing CreatePermissions method)")
		}
		
		// Assign allocation to struct fields
		tc.allocation = result.conn
		tc.allocationObj = allocObj
		// Note: tc.udpConn already set when creating fresh connection above (line 64)
		
		// Verify allocation was saved successfully
		if tc.allocation == nil {
			return fmt.Errorf("TURN allocation is nil after assignment to struct")
		}
		if tc.allocationObj == nil {
			return fmt.Errorf("TURN allocation object is nil after assignment to struct")
		}
		
		// CRITICAL: Clear any deadlines immediately after allocation
		// The shared UDP connection might have deadlines from STUN operations
		// These deadlines will cause immediate timeout when calling CreatePermissions/WriteTo
		tc.logger.Printf("🔧 Clearing any deadlines on allocation (critical for CreatePermissions)...")
		if deadlineConn, ok := result.conn.(interface {
			SetReadDeadline(time.Time) error
			SetWriteDeadline(time.Time) error
		}); ok {
			if err := deadlineConn.SetReadDeadline(time.Time{}); err != nil {
				tc.logger.Printf("⚠️  Warning: failed to clear read deadline: %v", err)
			} else {
				tc.logger.Printf("   ✅ Read deadline cleared")
			}
			if err := deadlineConn.SetWriteDeadline(time.Time{}); err != nil {
				tc.logger.Printf("⚠️  Warning: failed to clear write deadline: %v", err)
			} else {
				tc.logger.Printf("   ✅ Write deadline cleared")
			}
		} else {
			tc.logger.Printf("   ⚠️  Allocation does not support deadline methods")
		}
		
		relayAddr := result.conn.LocalAddr()
		tc.logger.Printf("✅ TURN allocation created successfully")
		tc.logger.Printf("   Relay address: %s", relayAddr)
		tc.logger.Printf("   Allocation saved to struct: allocation=%v, allocationObj=%v", 
			tc.allocation != nil, tc.allocationObj != nil)
		return nil
	}
}

// GetAllocation returns the TURN allocation connection (as PacketConn)
func (tc *TURNClient) GetAllocation() net.PacketConn {
	return tc.allocation
}

// GetAllocationObj returns the TURN allocation object with CreatePermissions/WriteTo methods
func (tc *TURNClient) GetAllocationObj() TURNAllocation {
	return tc.allocationObj
}

// Close closes TURN connection
func (tc *TURNClient) Close() error {
	tc.logger.Println("Closing TURN client")
	
	if tc.allocation != nil {
		tc.allocation.Close()
	}
	if tc.client != nil {
		tc.client.Close()
	}
	
	return nil
}
