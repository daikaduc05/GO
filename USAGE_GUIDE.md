# Hướng Dẫn Sử Dụng Go WebRTC Agent

## Cài Đặt và Cấu Hình

### 1. Cài Đặt Dependencies

```bash
go mod tidy
go build .
```

### 2. Cấu Hình File .env

```bash
# Copy file cấu hình mẫu
cp config.env .env

# Chỉnh sửa file .env với thông tin của bạn
# Ví dụ:
AGENT_ID=agent-001
SIGNALING_URL=ws://localhost:8000/ws/agent-001
ICE_URL=stun:stun.l.google.com:19302
MODE=chat
VERBOSE=false
```

## Các Cách Sử Dụng

### Cách 1: Sử Dụng File .env (Khuyến nghị)

```bash
# Terminal 1: Answerer (người nhận)
./webrtc-agent.exe -env=.env -listen

# Terminal 2: Offerer (người kết nối)
./webrtc-agent.exe -env=.env -peer-id=agent-001
```

### Cách 2: Sử Dụng Command Line

```bash
# Terminal 1: Answerer
./webrtc-agent.exe -agent-id=answerer-001 -signaling-url=ws://localhost:8000/ws/answerer-001 -listen

# Terminal 2: Offerer
./webrtc-agent.exe -agent-id=offerer-001 -signaling-url=ws://localhost:8000/ws/offerer-001 -peer-id=answerer-001
```

### Cách 3: Override File .env với Command Line

```bash
# Sử dụng .env nhưng override một số giá trị
./webrtc-agent.exe -env=.env -agent-id=custom-agent -peer-id=target-peer
```

## Các Chế Độ Transport

### Chat Mode (Mặc định)

```bash
./webrtc-agent.exe -env=.env -mode=chat -listen
```

### JSON Mode

```bash
./webrtc-agent.exe -env=.env -mode=json -listen
```

### Bytes Mode

```bash
./webrtc-agent.exe -env=.env -mode=bytes -listen
```

## Cấu Hình ICE Servers

### STUN Server (Mặc định)

```bash
ICE_URL=stun:stun.l.google.com:19302
```

### Multiple ICE Servers

```bash
ICE_URLS=stun:stun1.l.google.com:19302,stun:stun2.l.google.com:19302
```

### TURN Server với Authentication

```bash
ICE_URL=turn:turn.server.com:3478
ICE_USERNAME=your-username
ICE_CREDENTIAL=your-password
```

## Ví Dụ Thực Tế

### Ví Dụ 1: Chat Đơn Giản

```bash
# File .env
AGENT_ID=chat-agent-001
SIGNALING_URL=ws://localhost:8000/ws/chat-agent-001
MODE=chat

# Terminal 1: Answerer
./webrtc-agent.exe -env=.env -listen

# Terminal 2: Offerer
./webrtc-agent.exe -env=.env -peer-id=chat-agent-001
```

### Ví Dụ 2: JSON Communication

```bash
# File .env
AGENT_ID=json-agent-001
SIGNALING_URL=ws://localhost:8000/ws/json-agent-001
MODE=json

# Terminal 1: Answerer
./webrtc-agent.exe -env=.env -listen

# Terminal 2: Offerer
./webrtc-agent.exe -env=.env -peer-id=json-agent-001
```

### Ví Dụ 3: Với Custom ICE Server

```bash
# File .env
AGENT_ID=agent-001
SIGNALING_URL=ws://localhost:8000/ws/agent-001
ICE_URL=stun:stun.l.google.com:19302
ICE_USERNAME=
ICE_CREDENTIAL=
MODE=chat

# Terminal 1: Answerer
./webrtc-agent.exe -env=.env -listen

# Terminal 2: Offerer
./webrtc-agent.exe -env=.env -peer-id=agent-001
```

## Troubleshooting

### Lỗi "agent-id is required"

- Kiểm tra file .env có AGENT_ID không
- Hoặc sử dụng flag `-agent-id=your-id`

### Lỗi "signaling-url is required"

- Kiểm tra file .env có SIGNALING_URL không
- Hoặc sử dụng flag `-signaling-url=ws://...`

### Lỗi "peer-id is required for offerer mode"

- Sử dụng flag `-peer-id=target-peer-id` khi chạy offerer
- Hoặc sử dụng `-listen` để chạy answerer

### Không kết nối được

- Kiểm tra signaling server có chạy không
- Kiểm tra URL trong SIGNALING_URL có đúng không
- Kiểm tra firewall/network

## So Sánh với Python Version

### Ưu Điểm của Go Version

1. **Trickle ICE**: Kết nối nhanh hơn, gửi offer/answer ngay lập tức
2. **Performance**: Hiệu suất cao hơn, ít tài nguyên hơn
3. **Type Safety**: Kiểm tra lỗi tại compile time
4. **Concurrency**: Xử lý đồng thời tốt hơn với goroutines

### Tương Thích

- Hoàn toàn tương thích với Python signaling server
- Có thể kết nối giữa Go agent và Python agent
- Cùng protocol signaling

## Help Command

```bash
./webrtc-agent.exe -help
```

Sẽ hiển thị thông tin chi tiết về các biến môi trường và cách sử dụng.
