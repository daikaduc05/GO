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

// TURNAllocation interface for TURN allocation with CreatePermission/SendTo methods
type TURNAllocation interface {
	net.PacketConn
	CreatePermission(peerIP net.IP) error
	SendTo(data []byte, peer net.Addr) (int, error)
}

// TURNClient handles TURN relay connection
type TURNClient struct {
	config     *Config
	client     *turn.Client
	allocation net.PacketConn // For backward compatibility (used as PacketConn)
	allocationObj TURNAllocation // Store actual allocation object for CreatePermission/SendTo
	logger     *log.Logger
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
		Conn:           udpConn,
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
		
		tc.allocation = result.conn
		// Store allocation object for CreatePermission/SendTo methods
		if result.conn != nil {
			// Type assert to get access to CreatePermission/SendTo methods
			// The allocation returned from client.Allocate() implements these methods
			allocObj, ok := result.conn.(TURNAllocation)
			if !ok {
				return fmt.Errorf("TURN allocation does not implement TURNAllocation interface")
			}
			tc.allocationObj = allocObj
		}
		relayAddr := result.conn.LocalAddr()
		tc.logger.Printf("✅ TURN allocation created successfully")
		tc.logger.Printf("   Relay address: %s", relayAddr)
		return nil
	}
}

// GetAllocation returns the TURN allocation connection (as PacketConn)
func (tc *TURNClient) GetAllocation() net.PacketConn {
	return tc.allocation
}

// GetAllocationObj returns the TURN allocation object with CreatePermission/SendTo methods
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
