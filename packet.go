package main

import (
	"fmt"
	"net"
)

// ParseIPPacket extracts destination IP from IP packet
func ParseIPPacket(packet []byte) (string, error) {
	if len(packet) < 20 {
		return "", fmt.Errorf("packet too short")
	}

	// Check IP version (first 4 bits)
	version := packet[0] >> 4
	if version != 4 {
		return "", fmt.Errorf("unsupported IP version: %d", version)
	}

	// Extract destination IP (bytes 16-19 in IPv4 header)
	destIP := net.IPv4(packet[12], packet[13], packet[14], packet[15])
	return destIP.String(), nil
}

