package main

import (
	"sync"
	"time"
)

// ConnectionMethod represents the method used for P2P connection
type ConnectionMethod string

const (
	MethodHole  ConnectionMethod = "hole"  // NAT hole punching via public IP
	MethodRelay ConnectionMethod = "relay" // TURN relay
)

// ConnectionStatus represents connection status
type ConnectionStatus string

const (
	StatusConnected    ConnectionStatus = "connected"
	StatusFailed       ConnectionStatus = "failed"
	StatusDisconnected ConnectionStatus = "disconnected"
)

// ConnectionCacheEntry stores connection information for a peer
type ConnectionCacheEntry struct {
	PeerID       string
	Method       ConnectionMethod
	Status       ConnectionStatus
	LastUsed     time.Time
	PublicIP     string
	PublicPort   int
	RelayIP      string
	RelayPort    int
	VirtualIP    string // Virtual IP of the peer
}

// ConnectionCache manages P2P connection cache
type ConnectionCache struct {
	mu    sync.RWMutex
	cache map[string]*ConnectionCacheEntry
}

// NewConnectionCache creates a new connection cache
func NewConnectionCache() *ConnectionCache {
	return &ConnectionCache{
		cache: make(map[string]*ConnectionCacheEntry),
	}
}

// Get retrieves connection cache entry for a peer
func (cc *ConnectionCache) Get(peerID string) (*ConnectionCacheEntry, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	
	entry, exists := cc.cache[peerID]
	return entry, exists
}

// Set stores or updates connection cache entry
func (cc *ConnectionCache) Set(peerID string, method ConnectionMethod, status ConnectionStatus, publicIP string, publicPort int, relayIP string, relayPort int, virtualIP string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	
	cc.cache[peerID] = &ConnectionCacheEntry{
		PeerID:     peerID,
		Method:     method,
		Status:     status,
		LastUsed:   time.Now(),
		PublicIP:   publicIP,
		PublicPort: publicPort,
		RelayIP:    relayIP,
		RelayPort:  relayPort,
		VirtualIP:  virtualIP,
	}
}

// Remove removes a cache entry
func (cc *ConnectionCache) Remove(peerID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.cache, peerID)
}

// Clear removes all cache entries
func (cc *ConnectionCache) Clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.cache = make(map[string]*ConnectionCacheEntry)
}

// GetAll returns all cache entries
func (cc *ConnectionCache) GetAll() map[string]*ConnectionCacheEntry {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	
	result := make(map[string]*ConnectionCacheEntry)
	for k, v := range cc.cache {
		result[k] = v
	}
	return result
}
