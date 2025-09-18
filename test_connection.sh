#!/bin/bash

# Test script for UDP+TUN agent connection
# This script starts two agents and tests connectivity

echo "=== UDP+TUN Agent Connection Test ==="
echo

# Configuration
SIGNALING_URL="ws://localhost:8000"
AGENT1_VIP="10.10.0.5/24"
AGENT2_VIP="10.10.0.6/24"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Starting signaling server...${NC}"
cd "../Signaling Server"
python main.py &
SIGNALING_PID=$!
sleep 3

echo -e "${YELLOW}Starting Agent 1 (Answerer)...${NC}"
cd "../GO"
sudo go run main.go -env=config.env -listen -signaling-url="$SIGNALING_URL" &
AGENT1_PID=$!
sleep 2

echo -e "${YELLOW}Starting Agent 2 (Offerer)...${NC}"
sudo go run main.go -env=config.env -peer-id=agent-10-10-0-5 -signaling-url="$SIGNALING_URL" &
AGENT2_PID=$!

echo -e "${YELLOW}Waiting for connection...${NC}"
sleep 10

echo -e "${GREEN}Testing connectivity...${NC}"
echo "You can now test connectivity by:"
echo "1. From Agent 1: ping 10.10.0.6"
echo "2. From Agent 2: ping 10.10.0.5"
echo
echo "Press Ctrl+C to stop all processes"

# Wait for user interrupt
trap 'echo -e "\n${RED}Stopping all processes...${NC}"; kill $AGENT1_PID $AGENT2_PID $SIGNALING_PID 2>/dev/null; exit 0' INT

# Keep script running
wait
