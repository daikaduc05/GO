# TURN Agent

Đơn giản hóa agent chỉ với flow:
1. Đăng nhập
2. Kết nối WebSocket signaling
3. Kết nối TURN server

## Cấu trúc

```
├── main.go          # Main flow
├── config.go        # Configuration loading
├── login.go         # Login functionality
├── signaling.go     # WebSocket signaling client
├── turn.go          # TURN client với 401 handling
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
go run main.go -env=config.env
```

Hoặc với token:

```bash
go run main.go -env=config.env -token=your-token
```

## Flow

1. **Load config** từ file `.env`
2. **Login** (nếu chưa có token) → lấy token
3. **Connect signaling** → WebSocket đến backend
4. **Register** với signaling server
5. **Connect TURN** → Tạo TURN allocation với long-term credential auth

TURN client tự động xử lý:
- 401 challenge-response
- MESSAGE-INTEGRITY (HMAC-SHA1)
- FINGERPRINT
- Nonce handling
