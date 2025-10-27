# TURN Fallback Feature

## Overview

Đã thêm tính năng TURN fallback để gửi qua STUN/TURN server khi UDP hole punching thất bại. Tính năng này sử dụng cấu hình TURN server từ `config.env`.

## Cấu hình TURN Server

Trong `config.env`, các thông số TURN server đã được cấu hình:

```env
ICE_URLS=stun:13.229.230.15:3478,turn:13.229.230.15:3478
ICE_USERNAME=test
ICE_CREDENTIAL=1234

# Direct STUN/TURN (overridden by ICE_* if provided)
STUN_SERVER=13.229.230.15:3478
TURN_SERVER=13.229.230.15:3478
TURN_USER=test
TURN_PASS=1234
```

## Cách hoạt động

### 1. TURN Client Creation

- Agent tự động tạo TURN client khi khởi động
- Tạo TURN allocation để có relay address
- Log: `TURN: allocation created at <relay-address>`

### 2. NAT Punching với Fallback

- Thử UDP hole punching trước (như cũ)
- Nếu UDP punching thất bại sau `PUNCH_ATTEMPTS` lần
- Tự động chuyển sang TURN relay
- Log: `[FALLBACK] UDP punching failed for <peer>, attempting TURN relay`

### 3. TURN Relay Mode

- Đánh dấu peer là "relayed" trong `turnRelayedPeers` map
- Gửi tất cả frames qua TURN allocation thay vì direct UDP
- Log: `TURN: relayed <bytes> bytes to <peer>`

### 4. Statistics

- Hiển thị số lượng peers đang sử dụng TURN relay
- Log: `Stats: TX=<count>, RX=<count>, Peers=<count>, TURN-Relayed=<count>`

## Code Changes

### Agent Struct

```go
type Agent struct {
    // ... existing fields ...
    turnAllocation *turn.Allocation // TURN allocation for relay
    turnRelayedPeers map[string]bool // VIP -> isRelayed
}
```

### New Functions

- `createTURNClient()` - Tạo TURN client và allocation
- `sendViaTURNRelay()` - Gửi frame qua TURN relay
- `sendViaDirectUDP()` - Gửi frame qua direct UDP
- `startTURNFallback()` - Bắt đầu TURN fallback khi UDP thất bại

### Modified Functions

- `sendUDPFrame()` - Kiểm tra và chọn phương thức gửi (direct/TURN)
- `startNATPunching()` - Thêm logic fallback khi UDP thất bại
- `logStats()` - Hiển thị thống kê TURN relay

## Testing

### Chạy test script:

```bash
# Windows
test_turn_fallback.bat

# Linux/macOS
chmod +x test_turn_fallback.sh
./test_turn_fallback.sh
```

### Kiểm tra logs:

1. TURN client creation: `TURN: creating client for server...`
2. TURN allocation: `TURN: allocation created at...`
3. NAT punching: `[PUNCH] try X/Y -> <peer>`
4. Fallback activation: `[FALLBACK] UDP punching failed...`
5. TURN relay: `TURN: relayed X bytes to <peer>`
6. Statistics: `Stats: ..., TURN-Relayed=X`

## Benefits

- Tự động fallback khi NAT traversal thất bại
- Tăng tỷ lệ thành công kết nối peer-to-peer
- Sử dụng TURN server có sẵn trong config
- Transparent cho user - không cần thay đổi cách sử dụng
- Logging chi tiết để debug và monitor
