package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
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
	config        *Config
	client        *turn.Client
	udpConn       *net.UDPConn // Store UDP connection for refresh
	allocation    net.PacketConn // For backward compatibility (used as PacketConn)
	allocationObj TURNAllocation // Store actual allocation object for CreatePermission/SendTo
	lastRefresh   time.Time      // Track last refresh time
	mu            sync.RWMutex   // Protect allocation access
	logger        *log.Logger
	stopKeepalive chan struct{} // Channel to stop keepalive goroutine
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
		
		tc.mu.Lock()
		tc.allocation = result.conn
		tc.udpConn = udpConn
		// Store allocation object for CreatePermission/SendTo methods
		if result.conn != nil {
			// Type assert to get access to CreatePermission/SendTo methods
			// The allocation returned from client.Allocate() implements these methods
			allocObj, ok := result.conn.(TURNAllocation)
			if !ok {
				tc.mu.Unlock()
				return fmt.Errorf("TURN allocation does not implement TURNAllocation interface")
			}
			tc.allocationObj = allocObj
			tc.lastRefresh = time.Now()
		}
		relayAddr := result.conn.LocalAddr()
		tc.mu.Unlock()
		
		tc.logger.Printf("✅ TURN allocation created successfully")
		tc.logger.Printf("   Relay address: %s", relayAddr)
		tc.logger.Printf("   Allocation will be refreshed every 2 minutes")
		return nil
	}
}

// GetAllocation returns the TURN allocation connection (as PacketConn)
func (tc *TURNClient) GetAllocation() net.PacketConn {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.allocation
}

// GetAllocationObj returns the TURN allocation object with CreatePermission/SendTo methods
func (tc *TURNClient) GetAllocationObj() TURNAllocation {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.allocationObj
}

// GetRelayAddress returns the current relay address from allocation
func (tc *TURNClient) GetRelayAddress() (string, int, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	
	if tc.allocation == nil {
		return "", 0, fmt.Errorf("TURN allocation not available")
	}
	
	relayAddr := tc.allocation.LocalAddr()
	if udpAddr, ok := relayAddr.(*net.UDPAddr); ok {
		return udpAddr.IP.String(), udpAddr.Port, nil
	}
	return "", 0, fmt.Errorf("failed to get relay address")
}

// RefreshAllocation refreshes the TURN allocation by creating a new one
func (tc *TURNClient) RefreshAllocation() error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	
	if tc.udpConn == nil {
		return fmt.Errorf("UDP connection not available for refresh")
	}
	
	if tc.client == nil {
		return fmt.Errorf("TURN client not initialized")
	}
	
	tc.logger.Printf("🔄 Refreshing TURN allocation...")
	
	// Close old allocation if exists
	if tc.allocation != nil {
		tc.allocation.Close()
		tc.allocation = nil
		tc.allocationObj = nil
	}
	
	// Create new allocation
	allocCtx, allocCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer allocCancel()
	
	allocChan := make(chan struct {
		conn net.PacketConn
		err  error
	}, 1)
	
	go func() {
		allocation, err := tc.client.Allocate()
		if err != nil {
			allocChan <- struct {
				conn net.PacketConn
				err  error
			}{nil, err}
			return
		}
		allocChan <- struct {
			conn net.PacketConn
			err  error
		}{allocation, nil}
	}()
	
	// Wait for allocation or timeout
	select {
	case <-allocCtx.Done():
		return fmt.Errorf("TURN allocation refresh timeout after 10s")
	case result := <-allocChan:
		if result.err != nil {
			return fmt.Errorf("TURN allocation refresh failed: %w", result.err)
		}
		
		if result.conn == nil {
			return fmt.Errorf("TURN allocation refresh returned nil connection")
		}
		
		tc.allocation = result.conn
		allocObj, ok := result.conn.(TURNAllocation)
		if !ok {
			return fmt.Errorf("TURN allocation does not implement TURNAllocation interface")
		}
		tc.allocationObj = allocObj
		tc.lastRefresh = time.Now()
		
		relayAddr := result.conn.LocalAddr()
		tc.logger.Printf("✅ TURN allocation refreshed successfully")
		tc.logger.Printf("   New relay address: %s", relayAddr)
		return nil
	}
}

// IsAllocationValid checks if allocation exists and is not expired
func (tc *TURNClient) IsAllocationValid() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.allocation != nil && tc.allocationObj != nil
}

// StartKeepalive starts a goroutine that refreshes allocation every 2 minutes
func (tc *TURNClient) StartKeepalive() {
	if tc.stopKeepalive != nil {
		// Already running
		return
	}
	
	tc.stopKeepalive = make(chan struct{})
	
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				if !tc.IsAllocationValid() {
					tc.logger.Printf("⚠️  TURN allocation invalid, attempting refresh...")
				} else {
					tc.logger.Printf("🔄 Refreshing TURN allocation (periodic keepalive)...")
				}
				
				if err := tc.RefreshAllocation(); err != nil {
					tc.logger.Printf("❌ Failed to refresh TURN allocation: %v", err)
					// Continue trying - allocation might still work
				}
				
			case <-tc.stopKeepalive:
				tc.logger.Printf("🛑 Stopping TURN keepalive")
				return
			}
		}
	}()
	
	tc.logger.Printf("✅ TURN keepalive started (refresh every 2 minutes)")
}

// StopKeepalive stops the keepalive goroutine
func (tc *TURNClient) StopKeepalive() {
	if tc.stopKeepalive != nil {
		close(tc.stopKeepalive)
		tc.stopKeepalive = nil
	}
}

// Close closes TURN connection
func (tc *TURNClient) Close() error {
	tc.logger.Println("🔌 Closing TURN client...")
	
	// Stop keepalive first
	tc.StopKeepalive()
	
	tc.mu.Lock()
	defer tc.mu.Unlock()
	
	if tc.allocation != nil {
		tc.allocation.Close()
		tc.allocation = nil
		tc.allocationObj = nil
	}
	if tc.client != nil {
		tc.client.Close()
		tc.client = nil
	}
	
	tc.logger.Println("✅ TURN client closed")
	return nil
}
