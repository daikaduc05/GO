package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/pion/stun"
)

// STUNClient handles STUN server queries to get public IP/port
type STUNClient struct {
	serverAddr string
	logger     *log.Logger
}

// NewSTUNClient creates a new STUN client
func NewSTUNClient(serverAddr string) *STUNClient {
	return &STUNClient{
		serverAddr: serverAddr,
		logger:     log.New(os.Stdout, "[STUN] ", log.LstdFlags),
	}
}

// GetPublicAddress queries STUN server to get public IP and port
func (sc *STUNClient) GetPublicAddress(localConn *net.UDPConn) (string, int, error) {
	if sc.serverAddr == "" {
		return "", 0, fmt.Errorf("no STUN server configured")
	}

	sc.logger.Printf("Querying STUN server: %s", sc.serverAddr)

	// Resolve STUN server address
	serverAddr, err := net.ResolveUDPAddr("udp", sc.serverAddr)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve STUN server: %w", err)
	}

	// Create STUN message
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	// Send request with timeout
	deadline := time.Now().Add(5 * time.Second)
	if err := localConn.SetReadDeadline(deadline); err != nil {
		return "", 0, fmt.Errorf("failed to set read deadline: %w", err)
	}

	if err := localConn.SetWriteDeadline(deadline); err != nil {
		return "", 0, fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Send STUN binding request
	if _, err := localConn.WriteTo(message.Raw, serverAddr); err != nil {
		return "", 0, fmt.Errorf("failed to send STUN request: %w", err)
	}

	// Read response
	buffer := make([]byte, 1024)
	n, _, err := localConn.ReadFromUDP(buffer)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read STUN response: %w", err)
	}

	// Parse STUN response
	var response stun.Message
	response.Raw = buffer[:n]
	if err := response.Decode(); err != nil {
		return "", 0, fmt.Errorf("failed to decode STUN response: %w", err)
	}

	// Extract MAPPED-ADDRESS or XOR-MAPPED-ADDRESS
	var mappedAddr stun.XORMappedAddress
	if err := mappedAddr.GetFrom(&response); err != nil {
		return "", 0, fmt.Errorf("failed to get mapped address: %w", err)
	}

	publicIP := mappedAddr.IP.String()
	publicPort := mappedAddr.Port

	sc.logger.Printf("✅ Public address: %s:%d", publicIP, publicPort)
	return publicIP, publicPort, nil
}
