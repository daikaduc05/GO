# Simple UDP Message Agent

A simplified version that only uses UDP sockets to send/receive text messages between agents. No TUN interface required.

## Features

- ✅ **No TUN interface needed** - Only uses UDP sockets
- ✅ **Send/receive text messages** between agents
- ✅ **Signaling server** for peer discovery
- ✅ **Interactive chat** - Type messages and press Enter
- ✅ **Commands:** `list` (see peers), `quit` (exit)

## Quick Start

### 1. Start Signaling Server

```bash
cd "../../Signaling Server"
python main.py
```

### 2. Start Agent 1 (Answerer)

```bash
cd "GO/simple_agent"
go run main.go -env=config.env -listen
```

### 3. Start Agent 2 (Offerer)

```bash
go run main.go -env=config2.env -peer-id=agent-001
```

### 4. Or use the test script

```bash
test_messages.bat
```

## Usage

Once both agents are running:

1. **Type messages** in either agent window and press Enter to send
2. **Type `list`** to see connected peers
3. **Type `quit`** to exit

## Configuration

### Environment Variables

- `SIGNALING_URL` - WebSocket signaling server URL (required)
- `AGENT_ID` - Agent identifier (optional)
- `UDP_BIND_IP` - UDP bind IP (default: 0.0.0.0)
- `UDP_BIND_PORT` - UDP bind port (default: 0)

### Config Files

- `config.env` - Configuration for Agent 1
- `config2.env` - Configuration for Agent 2

## How It Works

1. **Registration:** Agent registers with signaling server using WebSocket
2. **Peer Discovery:** Signaling server notifies agents about available peers
3. **Direct UDP:** Agents send messages directly via UDP sockets
4. **Message Format:** JSON messages with type, from, to, content, timestamp

## Message Flow

```
Agent A                    Signaling Server              Agent B
   |                            |                          |
   |-- register (IP:port) ---->|                          |
   |                            |-- peer info ---------->|
   |<-- peer info --------------|                          |
   |                            |                          |
   |-- UDP message ------------>|                          |
   |                            |                          |
   |<-- UDP message ------------|                          |
```

## Troubleshooting

- Make sure signaling server is running first
- Check firewall settings for UDP ports
- Verify agent IDs are unique
- Check network connectivity between agents

## Dependencies

- Go 1.21+
- github.com/gorilla/websocket
- github.com/joho/godotenv
