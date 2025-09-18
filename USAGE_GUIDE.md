# Hướng Dẫn Sử Dụng UDP+TUN Agent

## Tổng Quan

Agent UDP+TUN là phiên bản cải tiến thay thế cho WebRTC agent cũ, sử dụng:

- **TUN Interface**: Tạo mạng ảo (10.0.0.0/8) để truyền IP packets
- **UDP Transport**: Socket UDP thô với NAT traversal
- **Signaling**: WebSocket để trao đổi endpoint (tương thích với Python server)
- **NAT Traversal**: UDP hole punching + TURN fallback

## Cài Đặt và Cấu Hình

### 1. Cài Đặt Dependencies

```bash
# Cài đặt các thư viện cần thiết
go get github.com/songgao/water github.com/pion/stun github.com/pion/turn/v2 github.com/gorilla/websocket github.com/joho/godotenv

# Build agent
go build main.go
```

### 2. Cấu Hình TUN Interface

#### Linux:

```bash
# Tạo TUN interface (cần quyền sudo)
sudo ip tuntap add dev tun0 mode tun
sudo ip addr add 10.10.0.5/24 dev tun0
sudo ip link set tun0 up

# Hoặc sử dụng script tự động
sudo ./setup-tun.sh
```

#### macOS:

```bash
# TUN interface sẽ được tạo tự động
# Cấu hình IP (cần quyền sudo)
sudo ifconfig utun0 10.10.0.5/24
```

#### Windows:

- Cài đặt Wintun driver từ https://www.wintun.net/
- Agent sẽ tự động tạo TUN interface

### 3. Cấu Hình File .env

```bash
# Copy file cấu hình mẫu
cp config.env .env

# Chỉnh sửa file .env với thông tin của bạn
# Ví dụ:
ORG_TOKEN=your-org-token
SIGNALING_URL=ws://localhost:8000/ws/agent-001
VIRTUAL_IP=10.10.0.5/24
STUN_SERVER=54.151.153.64:3478
TURN_SERVER=54.151.153.64:3478
TURN_USER=test
TURN_PASS=1234
MTU=1300
VERBOSE=false
```

## Các Cách Sử Dụng

### Cách 1: Sử Dụng File .env (Khuyến nghị)

```bash
# Terminal 1: Answerer (người nhận)
sudo go run main.go -env=.env -listen

# Terminal 2: Offerer (người kết nối)
sudo go run main.go -env=.env -peer-id=agent-002
```

### Cách 2: Sử Dụng Command Line

```bash
# Terminal 1: Answerer
sudo go run main.go -signaling-url=ws://localhost:8000/ws/answerer-001 -listen

# Terminal 2: Offerer
sudo go run main.go -signaling-url=ws://localhost:8000/ws/offerer-001 -peer-id=answerer-001
```

### Cách 3: Test Connectivity

```bash
# Test ping đến một VIP cụ thể
sudo go run main.go -env=.env -ping=10.10.0.6
```

## Chức Năng Chat

### Các Lệnh Chat

Khi agent đã kết nối, bạn có thể sử dụng các lệnh sau:

```bash
# Broadcast message đến tất cả peers
Hello everyone!

# Gửi message đến peer cụ thể
send 10.10.0.6 Hello from 10.10.0.5!

# Test ping
ping 10.10.0.6

# Thoát
quit
```

### Ví Dụ Chat Session

```
🎉 UDP+TUN CHAT SESSION STARTED 🎉
==================================================
💬 Type your messages and press Enter to send
🚪 Type 'quit' to exit
🔧 Use 'ping <vip>' to test connectivity
📝 Use 'send <vip> <message>' to send to specific peer
📢 Type message without prefix to broadcast to all peers
--------------------------------------------------
You: Hello everyone!
✅ Broadcast message: Hello everyone!

[10.10.0.6] Hi there! How are you?
You: send 10.10.0.6 I'm doing great, thanks!
✅ Message sent to 10.10.0.6: I'm doing great, thanks!

You: ping 10.10.0.6
✅ Ping sent to 10.10.0.6

You: quit
👋 Exiting session...
```

## Cấu Hình Chi Tiết

### Biến Môi Trường

| Biến                   | Mô tả                                    | Mặc định             |
| ---------------------- | ---------------------------------------- | -------------------- |
| `ORG_TOKEN`            | Token tổ chức cho signaling              | ""                   |
| `SIGNALING_URL`        | URL WebSocket signaling server           | **Bắt buộc**         |
| `ICE_URLS`             | ICE server URLs (stun:...,turn:...)      | ""                   |
| `ICE_USERNAME`         | Username cho ICE server                  | ""                   |
| `ICE_CREDENTIAL`       | Password cho ICE server                  | ""                   |
| `STUN_SERVER`          | STUN server                              | "54.151.153.64:3478" |
| `TURN_SERVER`          | TURN server                              | "54.151.153.64:3478" |
| `TURN_USER`            | TURN username                            | "test"               |
| `TURN_PASS`            | TURN password                            | "1234"               |
| `VIRTUAL_SUBNET`       | Subnet ảo                                | "10.10.0.0/16"       |
| `VIRTUAL_IP`           | IP ảo với mask                           | "10.10.0.5/24"       |
| `UDP_BIND_IP`          | IP bind cho UDP                          | "0.0.0.0"            |
| `UDP_BIND_PORT`        | Port bind cho UDP                        | "0"                  |
| `MTU`                  | MTU cho TUN interface                    | 1300                 |
| `PUNCH_ATTEMPTS`       | Số lần thử NAT punch                     | 20                   |
| `PUNCH_INTERVAL_MS`    | Khoảng thời gian giữa các lần punch (ms) | 250                  |
| `KEEPALIVE_INTERVAL_S` | Khoảng thời gian keepalive (s)           | 15                   |
| `PEER_STALE_TIMEOUT_S` | Timeout cho peer cũ (s)                  | 60                   |
| `VERBOSE`              | Bật log chi tiết                         | false                |

### Cấu Hình ICE/TURN

```bash
# Sử dụng ICE URLs (khuyến nghị)
ICE_URLS=stun:54.151.153.64:3478,turn:54.151.153.64:3478?transport=udp
ICE_USERNAME=test
ICE_CREDENTIAL=1234

# Hoặc cấu hình riêng lẻ
STUN_SERVER=54.151.153.64:3478
TURN_SERVER=54.151.153.64:3478
TURN_USER=test
TURN_PASS=1234
```

## Ví Dụ Thực Tế

### Ví Dụ 1: Chat Đơn Giản

```bash
# File .env
SIGNALING_URL=ws://localhost:8000/ws/chat-agent-001
VIRTUAL_IP=10.10.0.5/24

# Terminal 1: Answerer
sudo go run main.go -env=.env -listen

# Terminal 2: Offerer
sudo go run main.go -env=.env -peer-id=chat-agent-002
```

### Ví Dụ 2: Với Custom TURN Server

```bash
# File .env
SIGNALING_URL=ws://localhost:8000/ws/agent-001
TURN_SERVER=your-turn-server.com:3478
TURN_USER=your-username
TURN_PASS=your-password
VIRTUAL_IP=10.10.0.5/24

# Chạy agent
sudo go run main.go -env=.env -listen
```

### Ví Dụ 3: Test Connectivity

```bash
# Test ping đến peer
sudo go run main.go -env=.env -ping=10.10.0.6

# Test với verbose logging
sudo go run main.go -env=.env -ping=10.10.0.6 -verbose
```

## Troubleshooting

### Lỗi "signaling-url is required"

- Kiểm tra file .env có `SIGNALING_URL` không
- Hoặc sử dụng flag `-signaling-url=ws://...`

### Lỗi "no peer mapping for VIP"

- Peer chưa được kết nối
- Kiểm tra NAT punching có thành công không
- Thử sử dụng TURN server

### Lỗi "failed to create TUN interface"

- **Linux/macOS**: Cần quyền sudo
- **Windows**: Cài đặt Wintun driver
- Kiểm tra TUN interface đã tồn tại chưa

### Không kết nối được

- Kiểm tra signaling server có chạy không
- Kiểm tra firewall/network
- Thử sử dụng TURN server thay vì direct connection

### Lỗi "STUN discovery failed"

- Kiểm tra STUN server có accessible không
- Kiểm tra network connectivity
- Agent vẫn hoạt động với local endpoint

## So Sánh với WebRTC Cũ

### Ưu Điểm của UDP+TUN Agent

1. **Đơn Giản Hơn**: Không cần WebRTC stack phức tạp
2. **Hiệu Suất Cao**: Latency thấp hơn, ít overhead
3. **Debug Dễ Dàng**: Có thể inspect raw packets
4. **Kiểm Soát Tốt**: Direct control over network layer
5. **Cross-Platform**: TUN libraries có sẵn cho mọi OS

### Tương Thích

- **Signaling**: Hoàn toàn tương thích với Python signaling server
- **Protocol**: Sử dụng cùng JSON message format
- **NAT Traversal**: UDP hole punching + TURN fallback

### Migration từ WebRTC

| WebRTC Component        | UDP+TUN Equivalent         |
| ----------------------- | -------------------------- |
| RTCPeerConnection       | UDP socket + peer mapping  |
| DataChannel.Send()      | udpSendData()              |
| DataChannel.OnMessage() | udpReceiveData() → TUN     |
| ICE candidates          | STUN discovery + signaling |
| TURN relay              | Direct TURN client         |

## Help Command

```bash
go run main.go -help
```

Sẽ hiển thị thông tin chi tiết về các biến môi trường và cách sử dụng.

## Scripts Hỗ Trợ

### setup-tun.sh (Linux)

```bash
#!/bin/bash
# Tạo TUN interface cho Linux
sudo ip tuntap add dev tun0 mode tun
sudo ip addr add 10.10.0.5/24 dev tun0
sudo ip link set tun0 up
echo "TUN interface created: tun0"
```

### cleanup-tun.sh (Linux)

```bash
#!/bin/bash
# Xóa TUN interface
sudo ip link delete tun0
echo "TUN interface deleted"
```

## Performance Tips

1. **MTU**: Sử dụng MTU 1300 để tránh fragmentation
2. **Punch Interval**: Tăng `PUNCH_INTERVAL_MS` nếu network chậm
3. **Keepalive**: Điều chỉnh `KEEPALIVE_INTERVAL_S` theo nhu cầu
4. **TURN**: Sử dụng TURN server gần để giảm latency
