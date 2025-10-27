#!/bin/bash

echo "========================================"
echo "Testing TURN Fallback Functionality"
echo "========================================"
echo

echo "Starting Agent 1 (with TURN fallback)..."
gnome-terminal --title="Agent1" -- bash -c "go run main.go -env=config.env -agent-id=agent1; exec bash" &

echo "Waiting 5 seconds for Agent 1 to start..."
sleep 5

echo "Starting Agent 2 (with TURN fallback)..."
gnome-terminal --title="Agent2" -- bash -c "go run main.go -env=config.env -agent-id=agent2; exec bash" &

echo
echo "Both agents started. Check the terminal windows for:"
echo "- TURN client creation logs"
echo "- NAT punching attempts" 
echo "- TURN fallback activation (if UDP punching fails)"
echo "- Statistics showing TURN-relayed peers"
echo
echo "Press Enter to exit..."
read
