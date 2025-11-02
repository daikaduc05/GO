package main

import (
	"fmt"
	"net"
)

// ErrIPv6NotSupported is returned when packet is IPv6
var ErrIPv6NotSupported = fmt.Errorf("IPv6 not supported")

// ParseIPPacket extracts destination IP from IP packet
// Returns ErrIPv6NotSupported for IPv6 packets (should be silently skipped)
func ParseIPPacket(packet []byte) (string, error) {
	if len(packet) < 20 {
		return "", fmt.Errorf("packet too short")
	}

	// Check IP version (first 4 bits)
	version := packet[0] >> 4
	if version == 6 {
		// IPv6 - return special error to be silently skipped
		return "", ErrIPv6NotSupported
	}
	if version != 4 {
		return "", fmt.Errorf("unsupported IP version: %d", version)
	}

	// Extract destination IP (bytes 12-15 in IPv4 header)
	destIP := net.IPv4(packet[12], packet[13], packet[14], packet[15])
	return destIP.String(), nil
}

