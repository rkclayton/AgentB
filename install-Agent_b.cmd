@echo off
setlocal EnableExtensions
cd /d "%~dp0"

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\install-Agent_b.ps1"
set "AGENT_B_EXIT=%ERRORLEVEL%"
echo.
if "%AGENT_B_EXIT%"=="0" (
  echo Agent_b installation is complete. Open Agent_b from the Start menu.
) else (
  echo Agent_b was not installed. Review the message above.
)
pause
exit /b %AGENT_B_EXIT%
