@echo off
echo ========================================
echo Testing TURN Fallback Functionality
echo ========================================
echo.

echo Starting Agent 1 (with TURN fallback)...
start "Agent1" cmd /k "go run main.go -env=config.env -agent-id=agent1"

echo Waiting 5 seconds for Agent 1 to start...
timeout /t 5 /nobreak >nul

echo Starting Agent 2 (with TURN fallback)...
start "Agent2" cmd /k "go run main.go -env=config.env -agent-id=agent2"

echo.
echo Both agents started. Check the console windows for:
echo - TURN client creation logs
echo - NAT punching attempts
echo - TURN fallback activation (if UDP punching fails)
echo - Statistics showing TURN-relayed peers
echo.
echo Press any key to exit...
pause >nul
