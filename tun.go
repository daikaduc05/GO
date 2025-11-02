package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/songgao/water"
)

// TUNInterface manages TUN interface for virtual network
type TUNInterface struct {
	iface      *water.Interface
	config     *TUNConfig
	logger     *log.Logger
	isUp       bool
}

// TUNConfig holds configuration for TUN interface
type TUNConfig struct {
	Name       string // Interface name (e.g., "tun0")
	VirtualIP  string // Virtual IP to assign
	SubnetMask string // Subnet mask (e.g., "255.255.255.0" or "/24")
	MTU        int    // MTU size (default 1500)
}

// NewTUNInterface creates a new TUN interface manager
func NewTUNInterface(config *TUNConfig) *TUNInterface {
	return &TUNInterface{
		config: config,
		logger: log.New(os.Stdout, "[TUN] ", log.LstdFlags),
	}
}

// checkRootPrivileges checks if the process has root privileges
func checkRootPrivileges() bool {
	return os.Geteuid() == 0
}

// Create creates and configures TUN interface
func (t *TUNInterface) Create() error {
	// Only support Linux for now
	if runtime.GOOS != "linux" {
		return fmt.Errorf("TUN interface currently only supported on Linux")
	}

	// Check for root privileges
	if !checkRootPrivileges() {
		return fmt.Errorf("TUN interface creation requires root privileges. Please run with 'sudo' or set CAP_NET_ADMIN capability")
	}

	// Create TUN interface
	waterConfig := water.Config{
		DeviceType: water.TUN,
	}
	
	// Note: water library auto-assigns interface name
	// Name from config will be used when getting interface name after creation

	iface, err := water.New(waterConfig)
	if err != nil {
		return fmt.Errorf("failed to create TUN interface: %w (you may need root privileges: run with 'sudo')", err)
	}

	t.iface = iface
	t.logger.Printf("✅ TUN interface created: %s", iface.Name())

	// Set IP and subnet mask
	if err := t.setIPAndSubnet(); err != nil {
		iface.Close()
		return fmt.Errorf("failed to set IP: %w", err)
	}

	// Bring interface up
	if err := t.bringUp(); err != nil {
		iface.Close()
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	t.isUp = true
	t.logger.Printf("✅ TUN interface configured: %s, IP: %s/%s", 
		iface.Name(), t.config.VirtualIP, t.convertSubnetMask())

	return nil
}

// setIPAndSubnet sets IP address and subnet mask for the interface (Linux)
func (t *TUNInterface) setIPAndSubnet() error {
	ifaceName := t.iface.Name()
	subnetPrefix := t.convertSubnetMask()
	
	// Use ip command: ip addr add <ip>/<prefix> dev <iface>
	cmd := exec.Command("ip", "addr", "add", 
		fmt.Sprintf("%s/%s", t.config.VirtualIP, subnetPrefix),
		"dev", ifaceName)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if address already exists (can ignore)
		if strings.Contains(string(output), "File exists") {
			t.logger.Printf("IP address already configured on %s", ifaceName)
			return nil
		}
		return fmt.Errorf("failed to set IP: %s: %w", string(output), err)
	}

	t.logger.Printf("Set IP %s/%s on %s", t.config.VirtualIP, subnetPrefix, ifaceName)
	return nil
}

// bringUp brings the interface up (Linux)
func (t *TUNInterface) bringUp() error {
	ifaceName := t.iface.Name()
	
	cmd := exec.Command("ip", "link", "set", "dev", ifaceName, "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring interface up: %s: %w", string(output), err)
	}

	return nil
}

// convertSubnetMask converts subnet mask to prefix length
// Supports both formats: "255.255.255.0" -> "24" or "/24" -> "24"
func (t *TUNInterface) convertSubnetMask() string {
	mask := t.config.SubnetMask
	
	// Remove leading "/" if present
	if strings.HasPrefix(mask, "/") {
		return strings.TrimPrefix(mask, "/")
	}

	// Convert dotted decimal to prefix length
	if strings.Contains(mask, ".") {
		ip := net.ParseIP(mask)
		if ip == nil {
			// Fallback to /24 if invalid
			return "24"
		}
		
		ones, _ := net.IPMask(ip.To4()).Size()
		return fmt.Sprintf("%d", ones)
	}

	// Already a prefix length
	return mask
}

// SetRoute sets route for virtual subnet through TUN interface
func (t *TUNInterface) SetRoute(subnet string) error {
	ifaceName := t.iface.Name()
	
	// Parse subnet (e.g., "10.0.0.0/24")
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("invalid subnet format: %w", err)
	}

	// Add route: ip route add <subnet> dev <iface>
	cmd := exec.Command("ip", "route", "add", ipNet.String(), "dev", ifaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if route already exists (can ignore)
		if strings.Contains(string(output), "File exists") {
			t.logger.Printf("Route already exists: %s", ipNet.String())
			return nil
		}
		return fmt.Errorf("failed to add route: %s: %w", string(output), err)
	}

	t.logger.Printf("✅ Route added: %s dev %s", ipNet.String(), ifaceName)
	return nil
}

// Read reads a packet from TUN interface
func (t *TUNInterface) Read(buffer []byte) (int, error) {
	if t.iface == nil {
		return 0, fmt.Errorf("TUN interface not created")
	}
	return t.iface.Read(buffer)
}

// Write writes a packet to TUN interface
func (t *TUNInterface) Write(packet []byte) (int, error) {
	if t.iface == nil {
		return 0, fmt.Errorf("TUN interface not created")
	}
	return t.iface.Write(packet)
}

// Name returns the name of the TUN interface
func (t *TUNInterface) Name() string {
	if t.iface == nil {
		return ""
	}
	return t.iface.Name()
}

// Close closes the TUN interface
func (t *TUNInterface) Close() error {
	if t.iface == nil {
		return nil
	}

	t.logger.Println("Closing TUN interface")
	t.isUp = false
	
	return t.iface.Close()
}

// GetInterface returns the underlying water.Interface
func (t *TUNInterface) GetInterface() *water.Interface {
	return t.iface
}

