# Flow khi Client A ping Client B qua TUN Interface

## Tổng quan
Khi Client A muốn ping Client B, toàn bộ traffic được route qua TUN interface (virtual network). Flow được mô tả chi tiết dưới đây.

## Flow khi A ping B

### **BƯỚC 1: User gửi ping từ terminal**
```bash
# Trên Client A
ping 10.0.0.2  # Virtual IP của Client B
```

### **BƯỚC 2: Kernel routing → TUN interface**
1. Kernel nhận ICMP ping packet (dest IP = 10.0.0.2)
2. Kernel check routing table → thấy route `10.0.0.0/24 dev tun0`
3. Kernel **inject packet vào TUN interface** (`tunIface.Read()`)

### **BƯỚC 3: Main loop đọc từ TUN** (`main.go` dòng 240-331)
```go
// TUN receiver goroutine (line 240)
buffer := make([]byte, 1500)
n, err := tunIface.Read(buffer)
packet := buffer[:n]

// Parse IP header để lấy destination IP
destIP := parseIPPacket(packet) // Ví dụ: 10.0.0.2
```

**Chi tiết:**
- `tunIface.Read()` nhận raw IP packet từ kernel
- Parse IP header để lấy `destIP = 10.0.0.2`

### **BƯỚC 4: Lookup peer từ Virtual IP** (line 259-277)
```go
// Tìm peerID từ virtualIPMap
peerCacheMu.RLock()
peerID, exists := virtualIPMap[destIP]  // "10.0.0.2" → "peer-B"
peerInfo, peerExists := peerCache[peerID] // Lấy peerInfo của B
peerCacheMu.RUnlock()
```

**Ví dụ:**
- `destIP = "10.0.0.2"` → `peerID = "peer-B"`
- `peerInfo` chứa: `PublicIP`, `PublicPort`, `RelayIP`, `RelayPort`, `VirtualIP`

### **BƯỚC 5: Kiểm tra P2P connection** (line 279-315)
```go
// Check xem đã có connection chưa
p2pConn, connExists := p2pManager.GetConnection(peerID)
```

**Nếu CHƯA có connection:**
- **Step 8: Establish P2P Connection** (`p2pManager.Connect()`)
  - Thử **Hole Punching** trước
  - Nếu fail → fallback sang **Relay** (`tryRelay()`)
  - Trong `tryRelay()`:
    1. Gọi `CreatePermissions(peerB.RelayIP)` → Tạo permission trên TURN server
    2. Gửi test packet qua `allocation.SendTo()` hoặc `allocation.WriteTo()`
    3. Tạo `P2PConnection` object với `Method = MethodRelay`
  - Update connection cache

**Nếu ĐÃ có connection:**
- Sử dụng connection hiện tại (có thể là Hole Punching hoặc Relay)

### **BƯỚC 6: Forward packet qua P2P** (line 317-330)
```go
// SendPacket() trong p2p.go
if conn.Method == MethodHole {
    // Direct UDP (hole punching)
    conn.Conn.WriteTo(packet, conn.PeerAddr)
} else {
    // Relay via TURN
    // 6.1: Check permission (refresh nếu cần, sau 4 phút)
    if needsPermission {
        conn.RelayAlloc.CreatePermissions(relayIPAddr)
    }
    
    // 6.2: Send packet qua TURN relay
    conn.RelayAlloc.SendTo(packet, conn.RelayAddr)
    // HOẶC
    conn.RelayConn.WriteTo(packet, conn.RelayAddr)
}
```

**Chi tiết Relay path:**
1. **Permission check:**
   - Kiểm tra `PermissionTime`
   - Nếu đã tạo > 4 phút → refresh permission
   - Gọi `CreatePermissions(peerB.RelayIP)` → TURN server cho phép forward

2. **Send packet:**
   - `SendTo(packet, relayAddr)` hoặc `WriteTo(packet, relayAddr)`
   - Packet được gửi từ **A's TURN allocation** đến **B's TURN allocation**
   - TURN server forward packet theo Send Indication protocol

### **BƯỚC 7: TURN Server forward packet**
```
Client A's Allocation (13.229.230.15:54442)
    ↓ Send Indication với permission
TURN Server (13.229.230.15:3478)
    ↓ Forward to B's allocation
Client B's Allocation (13.229.230.15:49312)
```

**Quá trình:**
1. TURN server nhận Send Indication từ A
2. Check permission: A có permission gửi đến B's relay IP không?
3. Nếu có → forward packet đến B's allocation
4. B's allocation nhận packet qua `allocation.ReadFrom()`

### **BƯỚC 8: Client B nhận packet** (`p2p.go` line 705-826)
```go
// TURN receiver goroutine (line 705)
allocation := pm.turnClient.GetAllocation()
for {
    n, addr, err := allocation.ReadFrom(buffer)
    // addr = address của A's relay allocation
    
    // Match connection by relay address
    for pid, conn := range pm.connections {
        if conn.Method == MethodRelay {
            // Match với A's relay address
            if conn.RelayAddr.String() == addr.String() {
                peerID = pid  // "peer-A"
                break
            }
        }
    }
    
    // Callback: inject packet vào TUN
    onPacket(peerID, buffer[:n])
}
```

**Chi tiết:**
- `allocation.ReadFrom()` nhận packet từ TURN server
- Match connection bằng relay address của A
- Tìm `peerID = "peer-A"`

### **BƯỚC 9: Inject packet vào TUN của B** (`main.go` line 335-346)
```go
// StartPacketReceiver callback (line 335)
p2pManager.StartPacketReceiver(func(peerID string, packet []byte) {
    // packet = ICMP ping từ A
    
    // Inject vào TUN interface của B
    tunIface.Write(packet)
    // → Kernel nhận packet từ TUN
    // → Kernel forward đến ứng dụng ping của B
})
```

**Chi tiết:**
- `tunIface.Write(packet)` inject packet vào TUN interface
- Kernel nhận packet từ TUN → route đến ứng dụng ping
- **Ping response được tạo**

### **BƯỚC 10: Ping response (tương tự flow ngược lại)**
```
B → TUN → TURN → A's allocation → A's TUN → Kernel → Ping app
```

**Flow giống hệt như trên, nhưng ngược chiều:**
1. B's ping app tạo ICMP reply (dest = 10.0.0.1)
2. Kernel route qua TUN
3. TUN → P2P → TURN → A
4. A nhận → inject vào TUN → Kernel → Ping app

---

## Tóm tắt Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  Client A                                                       │
├─────────────────────────────────────────────────────────────────┤
│  1. ping 10.0.0.2                                               │
│     ↓                                                            │
│  2. Kernel → Route to TUN                                        │
│     ↓                                                            │
│  3. TUN Interface (Read packet)                                 │
│     ↓                                                            │
│  4. Parse dest IP → Lookup peerID "peer-B"                      │
│     ↓                                                            │
│  5. Get/Create P2P Connection                                   │
│     ├─ Try Hole Punching                                        │
│     └─ Fallback: Relay (via TURN)                               │
│        └─ CreatePermissions(B's RelayIP)                       │
│     ↓                                                            │
│  6. SendPacket() via P2P                                        │
│     └─ Relay: SendTo(packet, B's relayAddr)                     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
                              ↓
                        ┌─────────────┐
                        │ TURN Server │
                        │ Forward via │
                        │  Send Ind.  │
                        └─────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  Client B                                                       │
├─────────────────────────────────────────────────────────────────┤
│  7. allocation.ReadFrom() nhận packet                          │
│     ↓                                                            │
│  8. Match connection → peerID "peer-A"                         │
│     ↓                                                            │
│  9. Inject packet vào TUN (Write)                              │
│     ↓                                                            │
│  10. Kernel → Route to ping app                                 │
│      ↓                                                            │
│  11. Ping app nhận ICMP → Tạo reply                             │
│                                                                  │
│  (Reply flow tương tự, ngược chiều)                            │
└─────────────────────────────────────────────────────────────────┘
```

## Key Points

1. **TUN Interface:**
   - Là virtual network interface (tun0)
   - Kernel inject packet vào → app đọc qua `Read()`
   - App inject packet ra → kernel nhận qua `Write()`

2. **Routing:**
   - Traffic đến 10.0.0.0/24 → route qua tun0
   - App lookup peer từ virtual IP → gửi qua P2P

3. **P2P Connection:**
   - **Hole Punching:** Direct UDP giữa 2 clients
   - **Relay:** Qua TURN server (khi hole punching fail)

4. **TURN Relay:**
   - Cần **CreatePermission** trước khi gửi
   - Permission hết hạn sau 5 phút → refresh sau 4 phút
   - Packet được forward qua Send Indication protocol

5. **Bidirectional:**
   - Flow này áp dụng cho cả chiều A→B và B→A
   - Mỗi client có receiver goroutine riêng

