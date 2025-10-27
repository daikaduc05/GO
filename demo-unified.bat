@echo off
echo ========================================
echo UDP+TUN Agent - Unified Mode Demo
echo ========================================
echo.
echo This demo shows how ALL peers use the SAME command
echo No more listen/offerer distinction!
echo.
echo Commands for ALL peers:
echo   go run main.go -env=config.env
echo.
echo Or with custom agent ID:
echo   go run main.go -env=config.env -agent-id=peer-A
echo   go run main.go -env=config.env -agent-id=peer-B
echo.
echo Or no-TUN mode for testing:
echo   go run main.go -env=config.env -no-tun
echo.
echo ========================================
echo Starting unified mode demo...
echo ========================================
echo.

REM Start first peer
echo Starting Peer A...
start "Peer A" cmd /k "go run main.go -env=config.env -agent-id=peer-A -verbose"

REM Wait a bit
timeout /t 3 /nobreak > nul

REM Start second peer  
echo Starting Peer B...
start "Peer B" cmd /k "go run main.go -env=config.env -agent-id=peer-B -verbose"

echo.
echo ========================================
echo Both peers started! They will automatically:
echo 1. Connect to signaling server
echo 2. Register their endpoints
echo 3. Discover each other
echo 4. Start NAT hole punching
echo 5. Join the same virtual network
echo ========================================
echo.
echo Press any key to exit...
pause > nul
