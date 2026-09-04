@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Agent_b

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\launch-Agent_b.ps1" %*
set "AGENT_B_EXIT=%ERRORLEVEL%"
if not "%AGENT_B_EXIT%"=="0" (
  echo.
  echo Agent_b could not be opened. Review the specific error above.
  pause
)
exit /b %AGENT_B_EXIT%
