@echo off
REM Test script for simple UDP message agent on Windows
REM This script starts two agents and tests message sending

echo === Simple UDP Message Agent Test ===
echo.

REM Configuration
set SIGNALING_URL=ws://localhost:8000

echo Starting signaling server...
cd "..\..\Signaling Server"
start /B python main.py
timeout /t 3 /nobreak > nul

echo Starting Agent 1 (Answerer)...
cd "..\GO\simple_agent"
start /B go run main.go -env=config.env -listen -signaling-url=%SIGNALING_URL%
timeout /t 2 /nobreak > nul

echo Starting Agent 2 (Offerer)...
start /B go run main.go -env=config2.env -peer-id=agent-001 -signaling-url=%SIGNALING_URL%

echo Waiting for connection...
timeout /t 5 /nobreak > nul

echo.
echo ========================================
echo 🎉 TEST READY! 🎉
echo ========================================
echo.
echo Now you can:
echo 1. Type messages in Agent 1 window to send to Agent 2
echo 2. Type messages in Agent 2 window to send to Agent 1
echo 3. Type 'list' to see connected peers
echo 4. Type 'quit' to exit
echo.
echo Press any key to stop all processes
pause > nul

echo Stopping all processes...
taskkill /F /IM python.exe 2>nul
taskkill /F /IM go.exe 2>nul
taskkill /F /IM main.exe 2>nul
