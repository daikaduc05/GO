@echo off
REM Test script for 2-way messaging
echo === Testing 2-Way UDP Messaging ===
echo.

echo Starting signaling server...
cd "..\..\Signaling Server"
start /B python main.py
timeout /t 3 /nobreak > nul

echo.
echo Starting Agent 1 (Answerer)...
cd "..\GO\simple_agent"
start "Agent1" go run main.go -env=config.env -listen -signaling-url=ws://localhost:8000
timeout /t 2 /nobreak > nul

echo.
echo Starting Agent 2 (Offerer)...
start "Agent2" go run main.go -env=config2.env -peer-id=agent-001 -signaling-url=ws://localhost:8000

echo.
echo ========================================
echo 🎉 TEST READY! 🎉
echo ========================================
echo.
echo Instructions:
echo 1. In Agent1 window: Type messages and press Enter
echo 2. In Agent2 window: Type messages and press Enter
echo 3. Both should receive each other's messages
echo 4. Type 'quit' to exit either agent
echo.
echo Press any key to stop all processes
pause > nul

echo Stopping all processes...
taskkill /F /IM python.exe 2>nul
taskkill /F /IM go.exe 2>nul
taskkill /F /IM simple_agent.exe 2>nul
