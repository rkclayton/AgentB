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
  echo Building Agent_b...
  "%GO_EXE%" build -o "%AGENTB_ROOT%Agent_b.exe" ./cmd/harness
  if errorlevel 1 goto build_failed
) else if not exist "%AGENTB_ROOT%Agent_b.exe" (
  goto go_missing
)

if /i "%~1"=="--build-only" (
  echo Agent_b is built and ready. Nothing was started.
  exit /b 0
)

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%AGENTB_ROOT%scripts\launch-Agent_b.ps1" -RootDirectory "%AGENTB_ROOT%"
set "AGENTB_EXIT=%ERRORLEVEL%"
if not "%AGENTB_EXIT%"=="0" (
  echo.
  echo Agent_b could not be opened. Review the specific error above.
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
echo Agent_b could not find Go 1.24 or newer and no existing Agent_b.exe is available.
echo Install Go from https://go.dev/dl/ or place a local SDK at .tools\go, then run this launcher again.
pause
exit /b 1

:build_failed
echo.
echo Agent_b could not be built. Review the error above; nothing was started.
pause
exit /b 1
