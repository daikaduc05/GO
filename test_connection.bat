@echo off
REM Test script for UDP+TUN agent connection on Windows
REM This script starts two agents and tests connectivity

echo === UDP+TUN Agent Connection Test ===
echo.

REM Configuration
set SIGNALING_URL=ws://localhost:8000
set AGENT1_VIP=10.10.0.5/24
set AGENT2_VIP=10.10.0.6/24

echo Starting signaling server...
cd "..\Signaling Server"
start /B python main.py
timeout /t 3 /nobreak > nul

echo Starting Agent 1 (Answerer)...
cd "..\GO"
start /B go run main.go -env=config.env -listen -signaling-url=%SIGNALING_URL%
timeout /t 2 /nobreak > nul

echo Starting Agent 2 (Offerer)...
start /B go run main.go -env=config2.env -peer-id=agent-10-10-0-5 -signaling-url=%SIGNALING_URL%

echo Waiting for connection...
timeout /t 10 /nobreak > nul

echo Testing connectivity...
echo You can now test connectivity by:
echo 1. From Agent 1: ping 10.10.0.6
echo 2. From Agent 2: ping 10.10.0.5
echo.
echo Press any key to stop all processes
pause > nul

echo Stopping all processes...
taskkill /F /IM python.exe 2>nul
taskkill /F /IM go.exe 2>nul
taskkill /F /IM main.exe 2>nul
