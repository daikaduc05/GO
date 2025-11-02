# Flow Kết Nối P2P với STUN/TURN (Custom Logic, Không Dùng ICE Agent)

## Tổng Quan

Flow này mô tả quy trình kết nối P2P giữa các client, sử dụng STUN để lấy public IP và TURN (coturn) để làm relay khi NAT hole punching không thành công. **Không sử dụng ICE agent**, tự custom logic kết nối với cơ chế lưu cache phương thức kết nối đã thành công.

## Chi Tiết Flow

### 1. Client Start

- Client khởi động ứng dụng
- Khởi tạo các component cần thiết (network manager, signaling client, etc.)

### 2. Đăng Nhập (Login)

- Client gửi thông tin đăng nhập (username/password hoặc credentials) đến authentication server
- Server xác thực thông tin
- Nếu thành công, tiến tới bước 3

### 3. Lấy Token

- Server trả về authentication token (JWT hoặc session token)
- Client lưu token để sử dụng cho các request sau
- Token được dùng để authenticate các API calls tiếp theo

### 4. Lấy Public IP qua STUN

- Client sử dụng token để authenticate với STUN server
- Gửi STUN binding request đến STUN server
- STUN server trả về public IP và port của client
- Client lưu thông tin public IP này

### 5. Allocate IP qua coturn (TURN)

- Client sử dụng token để authenticate với coturn server
- Gửi TURN allocate request để yêu cầu một relay address
- coturn server allocate một relay IP và port cho client
- Client lưu thông tin relay IP này

### 6. Kết Nối WebSocket và Đăng Ký với Signaling Server

Quá trình kết nối và đăng ký với signaling server qua WebSocket để trao đổi thông tin giữa các peer.

**Lưu ý**: Peer sẽ lưu thông tin của các peer cùng subnet mask trong bộ nhớ tạm để sử dụng sau này.

#### 6.1. Peer → WebSocket Connect (/ws/) với JWT Token

- Client mở kết nối WebSocket đến signaling server tại endpoint `/ws/`
- **Authentication:** JWT token có thể được gửi qua:

  1. **Query parameter**: `ws://localhost:80/ws?token=<jwt_token>`

  2. **Authorization header**: `Authorization: Bearer <jwt_token>`

- Client thiết lập WebSocket connection với timeout và keep-alive

#### 6.2. Server → Verify Token, Authenticate User

- Server nhận connection request từ peer
- Verify JWT token:
  - Kiểm tra signature và expiration
  - Extract user ID và claims từ token
  - Validate user permissions
- Nếu token hợp lệ → accept connection
- Nếu token không hợp lệ → reject connection (HTTP 401/403)

#### 6.3. Peer → Send Register Message

- Sau khi WebSocket connection established, peer gửi registration message
- **Đây là message bắt buộc đầu tiên** sau khi kết nối

**Request Format:**

```json
{
  "type": "register",
  "agent_id": "agent-123", // Optional: ID của agent
  "public_ip": "203.0.113.1", // Required: Public IP từ STUN server
  "public_port": 50000, // Required: Public port từ STUN server
  "relay_ip": "203.0.113.10", // Optional: Relay IP từ TURN server
  "relay_port": 50001 // Optional: Relay port từ TURN server
}
```

**Field Descriptions:**

- `type`: Luôn là `"register"`
- `agent_id`: (Optional) ID tùy chỉnh của agent. Nếu không có, server sẽ tự generate
- `public_ip`: (Required) Public IP address của peer từ STUN discovery
- `public_port`: (Required) Public port của peer từ STUN discovery
- `relay_ip`: (Optional) Relay IP address từ TURN server nếu sử dụng relay
- `relay_port`: (Optional) Relay port từ TURN server nếu sử dụng relay

**Example:**

```json
{
  "type": "register",
  "agent_id": "peer-device-001",
  "public_ip": "203.0.113.1",
  "public_port": 50000,
  "relay_ip": "203.0.113.10",
  "relay_port": 50001
}
```

#### 6.4. Server → Validate, Get Virtual IP, Store Agent Info

- Server validate registration message
- Server xác thực token và extract user ID, email từ JWT claims
- Server assign virtual IP cho peer trong organization subnet
- Xác định subnet mask của virtual IP được assign
- Lưu thông tin agent vào connection pool:
  - Connection ID (auto-generated UUID)
  - User ID (từ token)
  - Email (từ token)
  - Virtual IP
  - Subnet mask (để filter peers cùng subnet)
  - Public IP và public port
  - Relay IP và relay port (nếu có)
  - Agent ID (nếu có, hoặc auto-generated)
  - Connection timestamp
- Store mapping: connection → agent info

#### 6.5. Server → Send RegisterAgentResponse

- Server gửi response xác nhận registration thành công

**Response Format:**

```json
{
  "type": "register_agent_response",
  "status": "registered",
  "virtual_ip": "10.0.0.5",
  "connection_id": "550e8400-e29b-41d4-a716-446655440000",
  "existing_peers": [
    {
      "peer_id": "peer-device-002",
      "user_id": 2,
      "email": "user2@example.com",
      "agent_id": "peer-device-002",
      "public_ip": "203.0.113.2",
      "public_port": 50000,
      "relay_ip": "203.0.113.11",
      "relay_port": 50001,
      "virtual_ip": "10.0.0.6"
    }
  ]
}
```

**Field Descriptions:**

- `type`: Luôn là `"register_agent_response"`
- `status`: Trạng thái registration, thường là `"registered"`
- `virtual_ip`: Virtual IP được assign cho peer trong organization subnet
- `connection_id`: Unique connection ID được generate bởi server
- `existing_peers`: Danh sách các peers hiện đang online **cùng subnet mask** với peer mới
  - Chỉ include peers có virtual IP cùng subnet
  - Peers khác subnet không được include

**Peer Object Structure trong existing_peers:**

- `peer_id`: ID của peer (agent_id nếu có, hoặc auto-generated)
- `user_id`: User ID của peer
- `email`: Email của user
- `agent_id`: Agent ID (optional)
- `public_ip`: Public IP của peer
- `public_port`: Public port của peer
- `relay_ip`: Relay IP của peer (optional)
- `relay_port`: Relay port của peer (optional)
- `virtual_ip`: Virtual IP của peer trong subnet

**Lưu ý:** Client nên lưu thông tin các peers trong `existing_peers` vào bộ nhớ tạm để sử dụng cho P2P connection sau này.

#### 6.6. Client → Tạo TUN Interface và Assign Virtual IP

- Sau khi nhận được virtual IP từ server, client tạo TUN interface để route traffic qua mạng ảo
- **Tự động tạo TUN khi khởi động app**, không cần user command

**Library sử dụng:**

- `github.com/songgao/water` - Tạo và quản lý TUN/TAP interface

**Functions chính:**

1. **Tạo TUN Interface:**

   ```go
   import "github.com/songgao/water"

   config := water.Config{
       DeviceType: water.TUN,
   }
   // Optionally set name, or let system auto-assign
   config.Name = "tun0"  // Optional

   iface, err := water.New(config)
   if err != nil {
       log.Fatalf("Failed to create TUN interface: %v", err)
   }
   defer iface.Close()
   ```

2. **Set IP và Subnet cho Interface:**

   - Sử dụng system commands hoặc libraries tùy platform:
     - **Linux**: `ip addr add <virtual_ip>/<subnet_mask> dev <iface_name>`
     - **Windows**: `netsh interface ip set address <iface_name> static <virtual_ip> <subnet_mask>`
     - **macOS**: `ifconfig <iface_name> <virtual_ip> netmask <subnet_mask>`
   - Hoặc sử dụng Go libraries:
     - **Linux**: `github.com/vishvananda/netlink`
     - **Windows/macOS**: System API calls hoặc exec commands

3. **Đọc packets từ TUN:**

   ```go
   buffer := make([]byte, 1500) // MTU size
   n, err := iface.Read(buffer)
   if err != nil {
       log.Printf("Read error: %v", err)
       continue
   }
   packet := buffer[:n]
   // Process packet...
   ```

4. **Ghi packets vào TUN:**

   ```go
   _, err := iface.Write(packet)
   if err != nil {
       log.Printf("Write error: %v", err)
   }
   ```

5. **Lấy tên interface:**
   ```go
   interfaceName := iface.Name()
   ```

**Quy trình:**

- Client nhận virtual IP từ `register_agent_response` (ví dụ: `10.0.0.5`)
- Tạo TUN interface (tự động, không cần user command)
- Assign virtual IP và subnet mask cho TUN interface
- Set route cho subnet mạng ảo (ví dụ: `10.0.0.0/24`) qua TUN interface
- Start goroutine để đọc packets từ TUN interface
- Packets từ TUN sẽ được route qua P2P connections (bước 8-9)

**Yêu cầu quyền:**

- **Linux**: Cần `CAP_NET_ADMIN` capability hoặc chạy với `sudo`
- **Windows**: Cần quyền Administrator
- **macOS**: Cần quyền Administrator

**Lưu ý:**

- TUN interface được tạo tự động, user không cần chạy command nào
- Interface sẽ được cleanup khi app shutdown

#### 6.7. Server → Broadcast PeerOnlineNotification

- Server broadcast notification đến **chỉ các peers có cùng subnet mask** với peer mới
- **Lọc peers nhận notification**:
  - Server chỉ gửi notification đến peers có virtual IP cùng subnet với peer mới
  - Peers khác subnet không nhận được notification
  - Sử dụng subnet mask để xác định peers cùng subnet

**Notification Format:**

```json
{
  "type": "peer_online",
  "peer": {
    "peer_id": "peer-device-003",
    "user_id": 3,
    "email": "user3@example.com",
    "agent_id": "peer-device-003",
    "public_ip": "203.0.113.3",
    "public_port": 50000,
    "relay_ip": "203.0.113.12",
    "relay_port": 50001,
    "virtual_ip": "10.0.0.7"
  }
}
```

**Field Descriptions:**

- `type`: Luôn là `"peer_online"`
- `peer`: Object chứa thông tin đầy đủ của peer mới online

  - `peer_id`: ID của peer
  - `user_id`: User ID của peer
  - `email`: Email của user
  - `agent_id`: Agent ID (optional)
  - `public_ip`: Public IP của peer
  - `public_port`: Public port của peer
  - `relay_ip`: Relay IP của peer (optional)
  - `relay_port`: Relay port của peer (optional)
  - `virtual_ip`: Virtual IP của peer trong subnet

- Mỗi peer online cùng subnet nhận được notification về peer mới
- **Peer lưu thông tin peer mới vào bộ nhớ tạm** (cùng subnet)
- Peer có thể sử dụng thông tin này để initiate P2P connection

#### 6.8. Connection → Keep Alive, Handle Messages

- WebSocket connection được maintain với keep-alive:
  - Ping/Pong messages mỗi 25-30 giây
  - Read deadline timeout (3 phút)
  - Auto-reconnect nếu connection bị drop
- Server và client xử lý các message types:
  - `register` / `register_agent_response`
  - `peer_online` / `peer_offline`
  - `connection_request` (yêu cầu kết nối P2P)
  - `connection_response` (phản hồi kết nối P2P)
  - `error` / `notification`

#### 6.9. On Disconnect → Remove from Connections

- Khi peer disconnect (mất kết nối, close connection):
  - Server remove connection khỏi connection pool
  - Cleanup agent info và virtual IP mapping
  - **Note**: Chưa notify các peers khác về việc peer này offline (có thể implement sau)
  - Connection resources được release

### 7. Quản Lý Connection Cache (Bộ Nhớ Tạm)

- Mỗi peer duy trì một **connection cache** trong bộ nhớ tạm
- Cache lưu thông tin về các kết nối đã thực hiện:
  - Peer ID đã kết nối
  - Phương thức kết nối đã sử dụng: `"hole"` (NAT hole punching) hoặc `"relay"` (TURN relay)
  - Trạng thái kết nối: `"connected"`, `"failed"`, `"disconnected"`
  - Timestamp của lần kết nối cuối cùng
- Cache được sử dụng để quyết định phương thức kết nối khi kết nối lại với peer đã biết

### 8. Bắt Đầu P2P Connection (Custom Logic, Không Dùng ICE Agent)

- Peer A muốn kết nối tới Peer B
- **Không sử dụng ICE agent**, tự custom logic kết nối

#### 8.1. Kiểm Tra Connection Cache

- Peer A kiểm tra trong connection cache xem đã từng kết nối với Peer B chưa
- **Nếu đã kết nối trước đó**:
  - Kiểm tra phương thức đã sử dụng (`hole` hoặc `relay`)
  - **Sử dụng lại phương thức đã thành công trước đó**
  - Nếu phương thức là `"hole"` → đi tới bước 8.2
  - Nếu phương thức là `"relay"` → đi tới bước 8.3
- **Nếu chưa từng kết nối**:
  - Đi tới bước 8.2 để thử hole trước

#### 8.2. Thử Kết Nối qua NAT Hole Punching (Public IP)

- **Mục tiêu**: Kết nối trực tiếp giữa Peer A và Peer B qua public IP (NAT traversal)
- Peer A và Peer B đồng thời thử kết nối đến public IP của nhau
- **Quy trình**:
  - Peer A gửi UDP/TCP packets đến `public_ip:port` của Peer B
  - Peer B gửi UDP/TCP packets đến `public_ip:port` của Peer A
  - Thử nhiều lần với timeout ngắn (2-3 giây)
  - Nếu nhận được response → **NAT hole punching thành công**
- **Kết quả**:
  - **Thành công**: Lưu vào cache với phương thức `"hole"`, tiến tới bước 9
  - **Thất bại**: Chuyển sang bước 8.3

#### 8.3. Fallback qua Relay IP (TURN)

- **Chỉ khi**: NAT hole punching thất bại hoặc phương thức cache là `"relay"`
- Peer A và Peer B kết nối qua TURN relay server
- **Quy trình**:
  - Peer A gửi data đến relay IP của Peer B (qua TURN server)
  - Peer B gửi data đến relay IP của Peer A (qua TURN server)
  - Tất cả traffic đi qua TURN relay server
- **Kết quả**:
  - Lưu vào cache với phương thức `"relay"`
  - Tiến tới bước 9

### 9. Tạo Kết Nối (Connection Established)

- Kết nối P2P đã được thiết lập (qua hole punching hoặc relay)
- **Lưu thông tin vào connection cache**:
  - Peer ID
  - Phương thức sử dụng (`"hole"` hoặc `"relay"`)
  - Trạng thái: `"connected"`
  - Timestamp
  - Public IP/Port hoặc Relay IP/Port (tùy phương thức)
  - Actual P2P connection object (UDPConn hoặc TURN allocation)
- Client có thể bắt đầu trao đổi data qua kết nối này
- Connection state = "connected"

### 9.1. Route Traffic qua TUN Interface

- Sau khi P2P connection established, client có thể route traffic qua mạng ảo
- **Routing flow:**
  1. Packet đến TUN interface từ OS (destination IP trong virtual subnet)
  2. **Lookup trong peerCache**: Tìm peer có `virtual_ip` = destination IP
  3. **Lookup trong connCache**: Lấy connection method và endpoint từ `peerID`
  4. **Forward packet**:
     - Nếu `Method = "hole"` → Gửi qua `PublicIP:PublicPort`
     - Nếu `Method = "relay"` → Gửi qua `RelayIP:RelayPort` (TURN relay)
  5. Peer đích nhận packet và inject vào TUN interface của mình

**Functions sử dụng:**

- Đọc từ TUN: `iface.Read(buffer)` (step 6.6)
- Ghi vào TUN: `iface.Write(packet)` (step 6.6)
- Lookup cache: `peerCache[peerID]`, `connCache.Get(peerID)`
- Send qua P2P: `conn.WriteTo(packet, peerAddr)` hoặc TURN allocation

**Cache structure cho routing:**

- `peerCache`: map[peerID]PeerInfo (chứa virtual_ip)
- `connCache`: map[peerID]ConnectionCacheEntry (chứa method, endpoint, connection object)
- Cần thêm: map[virtualIP]peerID để lookup nhanh từ destination IP

### 10. Connection Cache Management

- Cache được xóa khi:
  - Peer B disconnect/offline
  - Kết nối bị mất (timeout, network error)
  - Cache expired (sau một khoảng thời gian nhất định, tùy chọn)
- Khi cache bị xóa, lần kết nối tiếp theo sẽ thử hole trước (bước 8.2)

## Sơ Đồ Flow

```
┌─────────┐
│ Client  │
│  Start  │
└────┬────┘
     │
     ▼
┌─────────┐
│  Login  │ ────► Authentication Server
└────┬────┘
     │
     ▼
┌─────────┐
│  Token  │ ◄──── Token Response
└────┬────┘
     │
     ├─────────────────┐
     ▼                 ▼
┌─────────┐      ┌──────────┐
│  STUN   │      │  coturn  │
│ Request │      │ Allocate │
└────┬────┘      └────┬─────┘
     │                 │
     ▼                 ▼
┌─────────┐      ┌──────────┐
│ Public  │      │  Relay   │
│   IP    │      │    IP    │
└────┬────┘      └────┬─────┘
     │                 │
     └────────┬────────┘
              ▼
        ┌─────────────┐
        │   WebSocket │
        │   Connect   │
        │  (ws/ws)    │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │  Auth Token │
        │  (Verify)   │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │  Register   │
        │  Message    │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │ Store Agent │
        │   & Assign  │
        │  Virtual IP │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │Register Resp│
        │ + Peer List │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │  Broadcast  │
        │ Peer Online │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │ Check Cache │
        │  (Peer Info)│
        └──────┬──────┘
               │
        ┌──────┴──────┐
        │             │
        ▼             ▼
   Connected?    Not Connected
        │             │
        │             ▼
        │      ┌─────────────┐
        │      │  Try Hole   │
        │      │  Punching   │
        │      │ (Public IP) │
        │      └──────┬──────┘
        │             │
        │      ┌──────┴──────┐
        │      │             │
        │      ▼             ▼
        │  Success?      Failed?
        │      │             │
        │      │             ▼
        │      │      ┌─────────────┐
        │      │      │ Use Relay   │
        │      │      │    IP       │
        │      │      └──────┬──────┘
        │      │             │
        │      └──────┬──────┘
        │             │
        │      ┌──────┴──────┐
        │      │  Update     │
        │      │   Cache     │
        │      └──────┬──────┘
        │             │
        └──────┬──────┘
               │
               ▼
        ┌─────────────┐
        │  Connection │
        │ Established │
        └─────────────┘
```

## Các Thành Phần

- **STUN Server**: Lấy public IP và port
- **coturn (TURN) Server**: Cung cấp relay IP khi NAT hole punching fail
- **Signaling Server**: Trao đổi thông tin peer và coordinate P2P connections
- **TUN Interface**: Virtual network interface (tự động tạo bởi `github.com/songgao/water`) để route traffic qua mạng ảo
- **Connection Cache**: Bộ nhớ tạm lưu thông tin phương thức kết nối đã sử dụng với mỗi peer
- **Peer Cache**: Bộ nhớ tạm lưu thông tin các peers cùng subnet (virtual IP, public IP, relay IP)
- **Custom Connection Logic**: Logic tự custom để kết nối P2P, không sử dụng ICE agent

## Lưu Ý

1. Token phải được bảo mật và validate ở mỗi request
2. STUN và TURN servers cần được cấu hình đúng với credentials
3. **Không sử dụng ICE agent** - tự implement logic kết nối custom
4. **TUN Interface**:
   - Tự động tạo khi khởi động app, không cần user command
   - Cần quyền Administrator/root để tạo và configure
   - Sử dụng library `github.com/songgao/water`
   - Tự động cleanup khi app shutdown
5. **Connection cache** cần được quản lý cẩn thận:
   - Lưu phương thức kết nối đã thành công (`hole` hoặc `relay`)
   - Lưu actual connection object để forward packets
   - Xóa cache khi peer offline hoặc connection bị mất
   - Khi chưa có cache, luôn thử hole trước, relay sau
6. **Peer cache** cần được quản lý:
   - Lưu thông tin peers cùng subnet từ `register_agent_response` và `peer_online`
   - Cần thêm mapping `virtualIP → peerID` để lookup nhanh khi routing
7. **Thứ tự thử kết nối**:
   - Nếu có cache và đã kết nối trước: dùng lại phương thức đã thành công
   - Nếu chưa có cache: thử hole punching (public IP) trước, relay sau
8. **Routing traffic**:
   - Packet từ TUN → lookup cache → forward qua P2P connection
   - Cần handle cả chiều ngược lại: nhận từ P2P → inject vào TUN
9. Connection timeout cần được handle để tránh hang
10. NAT hole punching cần timing chính xác (cả 2 peer gửi đồng thời)
