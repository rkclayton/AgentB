@echo off
setlocal EnableExtensions
cd /d "%~dp0"

set "AGENTB_ROOT=%~dp0"
set "GO_EXE="

if defined AGENTB_GO if exist "%AGENTB_GO%" set "GO_EXE=%AGENTB_GO%"
if not defined GO_EXE if exist "%AGENTB_ROOT%.tools\go\bin\go.exe" set "GO_EXE=%AGENTB_ROOT%.tools\go\bin\go.exe"
if not defined GO_EXE for /f "delims=" %%G in ('where go.exe 2^>nul') do if not defined GO_EXE set "GO_EXE=%%G"

if /i "%~1"=="--check" goto check

if defined GO_EXE (
  echo Building AgentB...
  "%GO_EXE%" build -o "%AGENTB_ROOT%harness.exe" ./cmd/harness
  if errorlevel 1 goto build_failed
) else if not exist "%AGENTB_ROOT%harness.exe" (
  goto go_missing
)

if /i "%~1"=="--build-only" (
  echo AgentB is built and ready. Nothing was started.
  exit /b 0
)

if not defined AGENTB_NO_BROWSER (
  start "" /b powershell.exe -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -Command "$uri='http://127.0.0.1:8790/'; for($attempt=0; $attempt -lt 100; $attempt++){ try { $null=Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 1; Start-Process $uri; exit 0 } catch { Start-Sleep -Milliseconds 200 } }"
)

echo Starting AgentB. Close this window or press Ctrl+C to stop it.
"%AGENTB_ROOT%harness.exe"
set "AGENTB_EXIT=%ERRORLEVEL%"
if not "%AGENTB_EXIT%"=="0" (
  echo.
  echo AgentB stopped with exit code %AGENTB_EXIT%.
  pause
)
exit /b %AGENTB_EXIT%

:check
if not defined GO_EXE goto go_missing
"%GO_EXE%" version
if errorlevel 1 goto build_failed
echo Launcher prerequisites are ready. Nothing was built or started.
exit /b 0

:go_missing
echo.
echo AgentB could not find Go 1.24 or newer and no existing harness.exe is available.
echo Install Go from https://go.dev/dl/ or place a local SDK at .tools\go, then run this launcher again.
pause
exit /b 1

:build_failed
echo.
echo AgentB could not be built. Review the error above; nothing was started.
pause
exit /b 1
