@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Agent_b

set "AGENT_B_URL=http://127.0.0.1:8790/"
powershell.exe -NoLogo -NoProfile -NonInteractive -Command "try { $null=Invoke-WebRequest -UseBasicParsing -Uri '%AGENT_B_URL%' -TimeoutSec 1; exit 0 } catch { exit 1 }"
if not errorlevel 1 (
  start "" "%AGENT_B_URL%"
  exit /b 0
)

start "" /b powershell.exe -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -Command "$uri='%AGENT_B_URL%'; for($attempt=0; $attempt -lt 100; $attempt++){ try { $null=Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 1; Start-Process $uri; exit 0 } catch { Start-Sleep -Milliseconds 200 } }"

echo Starting Agent_b. Close this window or press Ctrl+C to stop it.
"%~dp0Agent_b.exe" -config "%~dp0harness.json"
set "AGENT_B_EXIT=%ERRORLEVEL%"
if not "%AGENT_B_EXIT%"=="0" (
  echo.
  echo Agent_b stopped with exit code %AGENT_B_EXIT%.
  pause
)
exit /b %AGENT_B_EXIT%
