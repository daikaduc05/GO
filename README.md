# WebRTC Agent - Go Implementation

This is a Go implementation of the Python WebRTC agent, converted from **non-trickle ICE** (Python aiortc default) to **trickle ICE** (Go pion/webrtc standard).

## Key Differences from Python Version

### 1. Trickle ICE Implementation

- **Python (aiortc)**: Uses non-trickle ICE by default - waits for all ICE candidates to be gathered before sending offer/answer
- **Go (pion/webrtc)**: Uses trickle ICE - sends offer/answer immediately, then sends each ICE candidate as it's discovered

### 2. ICE Candidate Handling

```go
// Go: Send each candidate immediately via OnICECandidate callback
session.PC.OnICECandidate(func(candidate *webrtc.ICECandidate) {
    if candidate == nil {
        return // End of candidates
    }

    // Send candidate immediately via signaling
    candidateMsg := SignalingMessage{
        Type:      "candidate",
        From:      ac.settings.AgentID,
        To:        peerID,
        Candidate: candidate.ToJSON(),
    }
    ac.signaling.Send(candidateMsg)
})
```

### 3. Early Offer/Answer Sending

```go
// Go: Send offer immediately, candidates sent separately
offer, err := session.PC.CreateOffer(nil)
session.PC.SetLocalDescription(offer)

// Send offer with incomplete SDP (no ICE candidates yet)
offerMsg := SignalingMessage{
    Type: "offer",
    From: ac.settings.AgentID,
    To:   peerID,
    SDP:  offer.SDP, // SDP without ICE candidates
}
ac.signaling.Send(offerMsg)
```

### 4. ICE Candidate Addition

```go
// Go: Add ICE candidates as they arrive
func (ac *AgentCore) handleCandidate(message SignalingMessage) error {
    candidate := message.Candidate
    if candidate == nil {
        return nil // End of candidates
    }

    // Add ICE candidate immediately
    return session.PC.AddICECandidate(*candidate)
}
```

## Usage

### Using .env Configuration File

```bash
# Copy and edit configuration file
cp config.env .env
# Edit .env with your settings

# Terminal 1: Start answerer (listener)
go run . -env=config.env -listen

# Terminal 2: Start offerer (connector)
go run . -env=config.env -peer-id=answerer-001
```

### Basic Chat Example (Command Line)

```bash
# Terminal 1: Start answerer (listener)
go run . -agent-id=answerer-001 -signaling-url=ws://localhost:8000/ws/answerer-001 -listen

# Terminal 2: Start offerer (connector)
go run . -agent-id=offerer-001 -signaling-url=ws://localhost:8000/ws/offerer-001 -peer-id=answerer-001
```

### With ICE Servers

```bash
go run . -agent-id=agent-001 -signaling-url=ws://localhost:8000/ws/agent-001 -peer-id=agent-002 -ice-url=stun:stun.l.google.com:19302
```

### Configuration File (.env)

The agent supports loading configuration from a `.env` file. Copy `config.env` to `.env` and modify the values:

```bash
# Agent Configuration
AGENT_ID=agent-001
SIGNALING_URL=ws://localhost:8000/ws/agent-001
SIGNALING_TOKEN=

# ICE Servers Configuration
ICE_URL=stun:stun.l.google.com:19302
ICE_USERNAME=
ICE_CREDENTIAL=

# Transport Configuration
DATA_CHANNEL_LABEL=mvp
MODE=chat
VERBOSE=false
```

**Environment Variables:**

- `AGENT_ID`: Unique agent identifier (required)
- `SIGNALING_URL`: WebSocket signaling server URL (required)
- `SIGNALING_TOKEN`: Optional authentication token
- `ICE_URL`: Single ICE server URL
- `ICE_URLS`: Multiple ICE server URLs (comma-separated)
- `ICE_USERNAME`: ICE server username
- `ICE_CREDENTIAL`: ICE server credential
- `DATA_CHANNEL_LABEL`: Data channel label (default: mvp)
- `MODE`: Transport mode: chat, json, bytes (default: chat)
- `VERBOSE`: Enable verbose logging: true/false (default: false)

**Command Line Override:**
Command line flags override environment variables:

```bash
./webrtc-agent -env=config.env -agent-id=custom-agent -peer-id=target-peer
```

### Different Transport Modes

```bash
# Chat mode (default)
go run . -agent-id=chat-001 -signaling-url=ws://localhost:8000/ws/chat-001 -peer-id=chat-002 -mode=chat

# JSON mode
go run . -agent-id=json-001 -signaling-url=ws://localhost:8000/ws/json-001 -peer-id=json-002 -mode=json

# Bytes mode
go run . -agent-id=bytes-001 -signaling-url=ws://localhost:8000/ws/bytes-001 -peer-id=bytes-002 -mode=bytes
```

## Architecture

### Core Components

1. **AgentCore** (`core.go`): Main WebRTC peer connection management
2. **SignalingClient** (`signaling.go`): WebSocket signaling communication
3. **Transport** (`transport.go`): Data channel transport abstraction
4. **Main** (`main.go`): CLI interface and example usage

### Trickle ICE Flow

1. **Offerer**:

   - Creates offer with `CreateOffer()`
   - Sets local description with `SetLocalDescription()`
   - Sends offer immediately via signaling
   - ICE candidates sent separately via `OnICECandidate` callback

2. **Answerer**:

   - Receives offer via signaling
   - Sets remote description with `SetRemoteDescription()`
   - Creates answer with `CreateAnswer()`
   - Sets local description with `SetLocalDescription()`
   - Sends answer immediately via signaling
   - ICE candidates sent separately via `OnICECandidate` callback

3. **Both peers**:
   - Receive ICE candidates via signaling
   - Add candidates with `AddICECandidate()`
   - Connection established when ICE gathering completes

## Dependencies

- `github.com/pion/webrtc/v3`: WebRTC implementation
- `github.com/gorilla/websocket`: WebSocket client
- `github.com/sirupsen/logrus`: Logging

## Compatibility

This Go implementation is compatible with the Python signaling server and can communicate with Python agents using the same signaling protocol. The main difference is in the ICE candidate handling - Go sends candidates individually (trickle ICE) while Python sends them all at once (non-trickle ICE).
