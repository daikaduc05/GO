# P2P Agent với STUN/TURN

Agent P2P tự động kết nối peers qua NAT hole punching hoặc TURN relay, sử dụng TUN interface để route traffic.

## Cấu trúc

```
├── main.go          # Main flow và TUN routing
├── config.go        # Configuration loading
├── login.go         # Authentication
├── signaling.go     # WebSocket signaling client
├── stun.go          # STUN client (lấy public IP)
├── turn.go          # TURN client (relay allocation)
├── p2p.go           # P2P connection manager
├── tun.go           # TUN interface management
├── packet.go        # IP packet parsing
├── cache.go         # Connection cache
└── config.env       # Configuration file
```

## Cài đặt

```bash
go mod download
```

## Cấu hình

Tạo file `config.env`:

```env
SIGNALING_URL=ws://your-server:8000
TURN_SERVER=13.229.230.15:3478
TURN_USER=test
TURN_PASS=1234
TURN_REALM=turn.demo
STUN_SERVER=13.229.230.15:3478
```

## Chạy

```bash
# Với auto-login
go run . -env=config.env

# Với token có sẵn
go run . -env=config.env -token=your-token

# Với agent ID
go run . -env=config.env -agent-id=peer-001
```

**Lưu ý:** Cần quyền root/sudo để tạo TUN interface:
```bash
sudo go run . -env=config.env
```

## Flow chính

### 1. Khởi động và Authentication
- Load config từ `config.env`
- Login (nếu chưa có token) → lấy JWT token
- Token được dùng cho tất cả requests sau

### 2. Network Discovery
- **STUN**: Lấy public IP và port
- **TURN**: Allocate relay IP/port (nếu cần)

### 3. Signaling Connection
- Kết nối WebSocket đến signaling server
- Gửi `register` message với public IP và relay IP
- Nhận `register_agent_response` với:
  - Virtual IP được assign
  - Danh sách peers cùng subnet đang online

### 4. TUN Interface
- Tự động tạo TUN interface (`tun0`)
- Assign virtual IP và subnet mask
- Set route cho virtual subnet qua TUN

### 5. Peer Management
- Lưu thông tin peers từ `register_agent_response` và `peer_online` notification
- Khi peer online: thử NAT hole punching trước, nếu fail thì tạo TURN permission
- Khi peer offline: cleanup connection và cache

### 6. P2P Connection
**Thứ tự kết nối:**
1. **NAT Hole Punching** (thử trước)
   - Gửi UDP packets đến public IP của peer
   - Nếu thành công → dùng direct connection
   - Keep-alive mỗi 20 giây để maintain NAT binding

2. **TURN Relay** (fallback)
   - Chỉ dùng khi hole punching fail
   - Tạo TURN permission cho peer's relay IP
   - Gửi packets qua TURN server
   - Keep-alive mỗi 20 giây (refresh permission + send packet)

### 7. Traffic Routing
**Từ TUN → Peer:**
1. Packet đến TUN interface (dest IP trong virtual subnet)
2. Parse IP header → lấy destination IP
3. Lookup peer từ virtual IP → peerID
4. Get/create P2P connection
5. Forward packet qua connection (hole punching hoặc relay)

**Từ Peer → TUN:**
1. Nhận packet từ P2P connection
2. Match connection → peerID
3. Inject packet vào TUN interface
4. Kernel route đến ứng dụng

## Connection Cache

- Lưu phương thức kết nối đã thành công (`hole` hoặc `relay`)
- Khi reconnect: dùng lại phương thức đã thành công
- Tự động cleanup khi peer offline

## Keep-Alive

- **Hole Punching**: Gửi UDP packet mỗi 20 giây để maintain NAT binding
- **Relay**: Refresh TURN permission + gửi packet mỗi 20 giây để maintain relay address

## Signaling Messages

### Register Message
```json
{
  "type": "register",
  "agent_id": "peer-001",
  "public_ip": "203.0.113.1",
  "public_port": 50000,
  "relay_ip": "203.0.113.10",
  "relay_port": 50001
}
```

### Register Response
```json
{
  "type": "register_agent_response",
  "status": "registered",
  "virtual_ip": "10.0.0.5",
  "connection_id": "uuid",
  "existing_peers": [...]
}
```

### Peer Online Notification
```json
{
  "type": "peer_online",
  "peer": {
    "peer_id": "peer-002",
    "user_id": 2,
    "email": "user2@example.com",
    "public_ip": "203.0.113.2",
    "public_port": 50000,
    "relay_ip": "203.0.113.11",
    "relay_port": 50001,
    "virtual_ip": "10.0.0.6"
  }
}
```

### Peer Offline Notification
```json
{
  "type": "peer_offline",
  "peer_id": "peer-002",
  "user_id": 2,
  "virtual_ip": "10.0.0.6"
}
```

## Yêu cầu

- Go 1.19+
- Quyền root/sudo (để tạo TUN interface)
- STUN/TURN server đã cấu hình
- Signaling server với WebSocket endpoint

## Dependencies

- `github.com/gorilla/websocket` - WebSocket client
- `github.com/pion/turn/v2` - TURN client
- `github.com/songgao/water` - TUN interface

## Lưu ý

1. **TUN Interface**: Tự động tạo khi khởi động, cần quyền root
2. **Connection Method**: Luôn thử hole punching trước, relay sau
3. **Keep-Alive**: Tự động maintain connections (20 giây)
4. **Permissions**: TURN permissions tự động refresh trước khi hết hạn (5 phút)
5. **Cache**: Connection cache giúp tái sử dụng phương thức đã thành công
