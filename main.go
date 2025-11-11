package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	// Command line flags
	var (
		envFile = flag.String("env", "config.env", "Path to .env file")
		token   = flag.String("token", "", "Auth token (overrides env)")
		agentID = flag.String("agent-id", "", "Agent ID (optional)")
	)
	flag.Parse()

	// Load configuration
	config, err := LoadConfig(*envFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override token if provided via flag
	if *token != "" {
		config.Token = *token
	}

	// Step 2 & 3: Login if no token
	if config.Token == "" {
		log.Println("🔐 Login required")
		token, err := Login(config)
		if err != nil {
			log.Fatalf("Login failed: %v", err)
		}
		config.Token = token
		log.Println("✅ Token obtained")
	}

	// Create UDP socket for STUN/TURN (bind to IPv4 only)
	// Use udp4 instead of udp to force IPv4-only socket
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		log.Fatalf("Failed to create UDP socket: %v", err)
	}
	defer udpConn.Close()
	log.Printf("UDP socket created: %s", udpConn.LocalAddr())

	// Step 4: Get Public IP via STUN
	log.Println("📍 Step 4: Getting public IP via STUN...")
	stunClient := NewSTUNClient(config.STUNServer)
	publicIP, publicPort, err := stunClient.GetPublicAddress(udpConn)
	if err != nil {
		log.Fatalf("Failed to get public IP: %v", err)
	}
	log.Printf("✅ Public IP: %s:%d", publicIP, publicPort)

	// Step 5: Allocate Relay IP via TURN
	log.Println("📍 Step 5: Allocating relay IP via TURN...")
	turnClient := NewTURNClient(config, udpConn)
	var relayIP string
	var relayPort int
	if err := turnClient.Connect(udpConn); err != nil {
		log.Printf("⚠️  TURN allocation failed: %v (continuing without relay)", err)
	} else {
		allocation := turnClient.GetAllocation()
		if allocation != nil {
			relayAddr := allocation.LocalAddr()
			if udpAddr, ok := relayAddr.(*net.UDPAddr); ok {
				relayIP = udpAddr.IP.String()
				relayPort = udpAddr.Port
				log.Printf("✅ Relay IP: %s:%d", relayIP, relayPort)
			}
		}
	}
	defer turnClient.Close()

	// Step 6: Connect WebSocket and Register
	log.Println("📍 Step 6: Connecting to WebSocket...")
	signaling := NewSignalingClient(config)
	if err := signaling.Connect(); err != nil {
		log.Fatalf("Failed to connect to signaling: %v", err)
	}
	defer signaling.Close()

	// Step 6.3: Send Register Message (first mandatory message)
	log.Println("📍 Step 6.3: Registering with signaling server...")
	log.Printf("📤 Register message details:")
	log.Printf("   Public IP: %s:%d", publicIP, publicPort)
	if relayIP != "" && relayPort != 0 {
		log.Printf("   Relay IP: %s:%d", relayIP, relayPort)
	} else {
		log.Printf("   Relay IP: (empty - no relay allocated)")
	}
	log.Printf("   Agent ID: %s", *agentID)
	
	registerMsg := RegisterMessage{
		Type:       "register",
		AgentID:    *agentID,
		PublicIP:   publicIP,
		PublicPort: publicPort,
		RelayIP:    relayIP,
		RelayPort:  relayPort,
	}
	if err := signaling.SendRegister(registerMsg); err != nil {
		log.Fatalf("Failed to register: %v", err)
	}
	log.Printf("✅ Register message sent successfully")

	// Step 6.5: Receive RegisterAgentResponse
	log.Println("📍 Step 6.5: Waiting for register response...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	registerResponse, err := signaling.ReceiveRegisterResponse(ctx)
	cancel()
	if err != nil {
		log.Fatalf("Failed to receive register response: %v", err)
	}
	log.Printf("✅ Registered: status=%s, virtual_ip=%s, connection_id=%s", 
		registerResponse.Status, registerResponse.VirtualIP, registerResponse.ConnectionID)
	log.Printf("   Existing peers: %d", len(registerResponse.ExistingPeers))

	// Step 6.6: Create TUN Interface and Assign Virtual IP
	log.Println("📍 Step 6.6: Creating TUN interface...")
	virtualIP := registerResponse.VirtualIP
	subnetMask := "255.255.255.0" // Default /24 subnet, can be configurable
	
	// Parse virtual IP to determine subnet if needed
	if ip := net.ParseIP(virtualIP); ip != nil {
		if ip.To4() != nil {
			// IPv4 - use /24 by default
			subnetMask = "24" // prefix length format
		}
	}

	tunConfig := &TUNConfig{
		Name:       "tun0",
		VirtualIP:  virtualIP,
		SubnetMask: subnetMask,
		MTU:        1500,
	}

	tunIface := NewTUNInterface(tunConfig)
	if err := tunIface.Create(); err != nil {
		log.Printf("⚠️  Failed to create TUN interface: %v", err)
		log.Println("💡 TIP: To create TUN interface, run the program with sudo:")
		log.Println("   sudo go run . -env=config.env")
		log.Println("   Or build first: go build . && sudo ./webrtc-agent -env=config.env")
		log.Println("   Continuing without TUN interface...")
		tunIface = nil
	} else {
		defer tunIface.Close()
		
		// Calculate subnet from virtual IP (assuming /24)
		ip := net.ParseIP(virtualIP)
		if ip != nil && ip.To4() != nil {
			// Get first 3 octets and add .0/24
			ipBytes := ip.To4()
			subnet := fmt.Sprintf("%d.%d.%d.0/24", ipBytes[0], ipBytes[1], ipBytes[2])
			
			// Set route for virtual subnet
			if err := tunIface.SetRoute(subnet); err != nil {
				log.Printf("⚠️  Failed to set route: %v", err)
			}
		}
		log.Printf("✅ TUN interface ready: %s", tunIface.Name())
	}

	// Store peers in memory (Step 6.5)
	peerCache := make(map[string]PeerInfo)
	peerCacheMu := sync.RWMutex{}
	// Map for quick lookup: virtualIP -> peerID
	virtualIPMap := make(map[string]string)
	
	for _, peer := range registerResponse.ExistingPeers {
		peerCacheMu.Lock()
		peerCache[peer.PeerID] = peer
		virtualIPMap[peer.VirtualIP] = peer.PeerID
		peerCacheMu.Unlock()
		log.Printf("   Peer cached: %s (%s) - virtual_ip=%s", 
			peer.PeerID, peer.Email, peer.VirtualIP)
	}

	// Connection cache for tracking connection methods
	connCache := NewConnectionCache()

	// Create P2P manager
	p2pManager := NewP2PManager(udpConn, turnClient)

	// Step 6.7: Handle incoming messages (peer_online notifications, etc.)
	signaling.StartMessageHandler(func(msg SignalingMessage) {
		switch msg.Type {
		case "peer_online":
			var notif PeerOnlineNotification
			if err := json.Unmarshal(msg.Raw, &notif); err == nil {
				peerCacheMu.Lock()
				peerCache[notif.Peer.PeerID] = notif.Peer
				virtualIPMap[notif.Peer.VirtualIP] = notif.Peer.PeerID
				peerCacheMu.Unlock()
				log.Printf("📢 Peer online: %s (%s) - virtual_ip=%s", 
					notif.Peer.PeerID, notif.Peer.Email, notif.Peer.VirtualIP)

				// Proactively create TURN permission so that relay path is ready even before traffic
				if notif.Peer.RelayIP != "" && notif.Peer.RelayPort != 0 {
					if err := p2pManager.PrepareRelayPermission(notif.Peer.PeerID, notif.Peer); err != nil {
						log.Printf("⚠️  PrepareRelayPermission failed for %s: %v", notif.Peer.PeerID, err)
					} else {
						log.Printf("✅ PrepareRelayPermission succeeded for %s (%s:%d)", notif.Peer.PeerID, notif.Peer.RelayIP, notif.Peer.RelayPort)
					}
				} else {
					log.Printf("⚠️  Peer %s has no relay info; skipping permission prepare", notif.Peer.PeerID)
				}
			}
		case "peer_offline":
			// Handle peer offline
			var notif PeerOnlineNotification
			if err := json.Unmarshal(msg.Raw, &notif); err == nil {
				log.Printf("📢 Peer offline: %s", notif.Peer.PeerID)
				p2pManager.RemoveConnection(notif.Peer.PeerID)
				connCache.Remove(notif.Peer.PeerID)
				peerCacheMu.Lock()
				delete(peerCache, notif.Peer.PeerID)
				delete(virtualIPMap, notif.Peer.VirtualIP)
				peerCacheMu.Unlock()
			} else {
				log.Printf("📢 Peer offline notification received")
			}
		default:
			log.Printf("📨 Received message: type=%s", msg.Type)
		}
	})

	// Step 9.1: Route Traffic qua TUN Interface
	// Start packet reader from TUN interface (if created)
	if tunIface != nil && tunIface.GetInterface() != nil {
		go func() {
			buffer := make([]byte, 1500) // MTU size
			log.Println("📦 TUN packet reader started")
			
			for {
				n, err := tunIface.Read(buffer)
				if err != nil {
					log.Printf("TUN read error: %v", err)
					break
				}
				
				packet := make([]byte, n)
				copy(packet, buffer[:n])
				
				// Parse packet to get destination IP
				destIP, err := ParseIPPacket(packet)
				if err != nil {
					// Silently skip IPv6 packets
					if err == ErrIPv6NotSupported {
						continue
					}
					log.Printf("⚠️  Failed to parse IP packet: %v", err)
					continue
				}
				
				log.Printf("📦 Received packet from TUN: %d bytes, dest=%s", n, destIP)
				
				// Lookup peer by virtual IP
				peerCacheMu.RLock()
				peerID, exists := virtualIPMap[destIP]
				peerCacheMu.RUnlock()
				
				if !exists {
					log.Printf("⚠️  No peer found for destination IP: %s", destIP)
					continue
				}
				
				// Get peer info
				peerCacheMu.RLock()
				peerInfo, peerExists := peerCache[peerID]
				peerCacheMu.RUnlock()
				
				if !peerExists {
					log.Printf("⚠️  Peer info not found: %s", peerID)
					continue
				}
				
				// Check if we have P2P connection (Step 8.1: Check Connection Cache)
				p2pConn, connExists := p2pManager.GetConnection(peerID)
				
				if !connExists || p2pConn.Status != StatusConnected {
					// Step 8: Establish P2P Connection
					log.Printf("📍 Step 8: Establishing P2P connection to %s...", peerID)
					
					// Step 8.1: Check Connection Cache
					var cacheEntry *ConnectionCacheEntry
					connCacheEntry, cacheExists := connCache.Get(peerID)
					if cacheExists && connCacheEntry.Status == StatusConnected {
						// Use cached method
						cacheEntry = connCacheEntry
						log.Printf("Using cached connection method: %s", cacheEntry.Method)
					}
					
					// Establish connection using Connect method
					var err error
					p2pConn, err = p2pManager.Connect(peerID, peerInfo)
					if err != nil {
						log.Printf("❌ Failed to establish P2P connection: %v", err)
						// Update cache with failed status
						if cacheExists && cacheEntry != nil {
							connCache.Set(peerID, cacheEntry.Method, StatusFailed,
								cacheEntry.PublicIP, cacheEntry.PublicPort,
								cacheEntry.RelayIP, cacheEntry.RelayPort, cacheEntry.VirtualIP)
						}
						continue
					}
					
					// Step 9: Connection Established - Update cache
					connCache.Set(peerID, p2pConn.Method, StatusConnected, 
						peerInfo.PublicIP, peerInfo.PublicPort,
						peerInfo.RelayIP, peerInfo.RelayPort, peerInfo.VirtualIP)
					
					log.Printf("✅ P2P connection established: %s via %s", peerID, p2pConn.Method)
				}
				
				// Forward packet via P2P connection
				if err := p2pManager.SendPacket(peerID, packet); err != nil {
					log.Printf("❌ Failed to send packet to %s: %v", peerID, err)
					// Remove failed connection
					p2pManager.RemoveConnection(peerID)
					connCacheEntry, _ := connCache.Get(peerID)
					if connCacheEntry != nil {
						connCache.Set(peerID, connCacheEntry.Method, StatusFailed,
							connCacheEntry.PublicIP, connCacheEntry.PublicPort,
							connCacheEntry.RelayIP, connCacheEntry.RelayPort, connCacheEntry.VirtualIP)
					}
				} else {
					log.Printf("✅ Packet forwarded to %s via %s", peerID, p2pConn.Method)
				}
			}
		}()
		
		// Start P2P packet receiver to inject packets back into TUN
		p2pManager.StartPacketReceiver(func(peerID string, packet []byte) {
			log.Printf("📥 [TUN Handler] ========== CALLBACK TRIGGERED ==========")
			log.Printf("   ✅ Received packet from P2P peer: %s", peerID)
			log.Printf("   📦 Packet size: %d bytes", len(packet))
			
			// Parse IP header for logging
			if len(packet) >= 20 {
				destIP := net.IP(packet[16:20]).String()
				srcIP := net.IP(packet[12:16]).String()
				protocol := packet[9]
				log.Printf("   📋 IP Header:")
				log.Printf("      - Source IP: %s", srcIP)
				log.Printf("      - Dest IP: %s", destIP)
				log.Printf("      - Protocol: %d", protocol)
			}
			
			// Inject packet into TUN interface
			if tunIface == nil {
				log.Printf("   ❌ TUN interface is nil - cannot inject packet")
				return
			}
			
			if tunIface.GetInterface() == nil {
				log.Printf("   ❌ TUN interface.GetInterface() is nil - cannot inject packet")
				return
			}
			
			log.Printf("   📤 Injecting packet into TUN interface...")
			log.Printf("   📍 TUN interface name: %s", tunIface.Name())
			
			n, err := tunIface.Write(packet)
			if err != nil {
				log.Printf("   ❌ FAILED to inject packet into TUN: %v", err)
				log.Printf("   ========================================")
				return
			}
			
			log.Printf("   ✅ SUCCESS! Packet injected into TUN")
			log.Printf("   ✅ Bytes written: %d (expected: %d)", n, len(packet))
			if n != len(packet) {
				log.Printf("   ⚠️  Warning: Partial write (%d/%d bytes)", n, len(packet))
			}
			log.Printf("   ✅ Packet should now be available in TUN interface")
			log.Printf("   ========================================")
		})
	}

	log.Println("✅ All connections established successfully!")
	log.Println("Press Ctrl+C to exit")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Keep connection alive
	keepAliveTicker := time.NewTicker(30 * time.Second)
	defer keepAliveTicker.Stop()

	go func() {
		for range keepAliveTicker.C {
			peerCacheMu.RLock()
			peerCount := len(peerCache)
			peerCacheMu.RUnlock()
			log.Printf("💓 Connection alive... (peers: %d)", peerCount)
		}
	}()

	<-sigChan
	log.Println("\n👋 Shutting down...")

	// Cleanup: Remove connection cache entries
	peerCacheMu.RLock()
	for peerID := range peerCache {
		connCache.Remove(peerID)
	}
	peerCacheMu.RUnlock()
}