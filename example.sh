#!/bin/bash

# Example usage script for Go WebRTC Agent

echo "=== Go WebRTC Agent Example ==="
echo "This script demonstrates the Go WebRTC agent with trickle ICE"
echo ""

# Check if signaling server is running
echo "Make sure the signaling server is running on localhost:8000"
echo "You can start it with: cd '../Signaling Server' && python main.py"
echo ""

# Build the Go agent
echo "Building Go agent..."
go build .
if [ $? -ne 0 ]; then
    echo "Build failed!"
    exit 1
fi
echo "Build successful!"
echo ""

# Example 1: Using .env file
echo "=== Example 1: Using .env Configuration ==="
echo "First, copy and edit config.env:"
echo "cp config.env .env"
echo "# Edit .env with your settings"
echo ""
echo "Terminal 1 (Answerer):"
echo "./webrtc-agent -env=config.env -listen"
echo ""
echo "Terminal 2 (Offerer):"
echo "./webrtc-agent -env=config.env -peer-id=answerer-001"
echo ""

# Example 2: Chat mode
echo "=== Example 2: Chat Mode (Command Line) ==="
echo "Terminal 1 (Answerer):"
echo "./webrtc-agent -agent-id=answerer-001 -signaling-url=ws://localhost:8000/ws/answerer-001 -listen"
echo ""
echo "Terminal 2 (Offerer):"
echo "./webrtc-agent -agent-id=offerer-001 -signaling-url=ws://localhost:8000/ws/offerer-001 -peer-id=answerer-001"
echo ""

# Example 3: JSON mode
echo "=== Example 3: JSON Mode ==="
echo "Terminal 1 (Answerer):"
echo "./webrtc-agent -agent-id=json-answerer-001 -signaling-url=ws://localhost:8000/ws/json-answerer-001 -listen -mode=json"
echo ""
echo "Terminal 2 (Offerer):"
echo "./webrtc-agent -agent-id=json-offerer-001 -signaling-url=ws://localhost:8000/ws/json-offerer-001 -peer-id=json-answerer-001 -mode=json"
echo ""

# Example 4: With ICE servers
echo "=== Example 4: With ICE Servers ==="
echo "./webrtc-agent -agent-id=agent-001 -signaling-url=ws://localhost:8000/ws/agent-001 -peer-id=agent-002 -ice-url=stun:stun.l.google.com:19302"
echo ""

# Example 5: Override .env with command line
echo "=== Example 5: Override .env with Command Line ==="
echo "./webrtc-agent -env=config.env -agent-id=custom-agent -peer-id=target-peer"
echo ""

echo "=== Key Differences from Python Version ==="
echo "1. Uses trickle ICE - sends offer/answer immediately, then sends ICE candidates separately"
echo "2. ICE candidates are sent via OnICECandidate callback as they're discovered"
echo "3. Compatible with Python signaling server and agents"
echo "4. Faster connection establishment due to trickle ICE"
echo ""

echo "=== Usage Notes ==="
echo "- Use -listen flag for answerer mode (waits for incoming connections)"
echo "- Use -peer-id flag for offerer mode (initiates connection to specific peer)"
echo "- Use -mode flag to specify transport mode: chat, json, bytes"
echo "- Use -verbose flag for detailed logging"
echo "- Use -ice-url, -ice-username, -ice-credential for custom ICE servers"
echo ""
