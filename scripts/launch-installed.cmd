@echo off
setlocal EnableExtensions
title Agent_b
set "AGENT_B_AUTO_CLOSE=0"
for %%A in (%*) do (
  if /i "%%~A"=="-NoPause" set "AGENT_B_AUTO_CLOSE=1"
  if /i "%%~A"=="-Detached" set "AGENT_B_AUTO_CLOSE=1"
)

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\launch-Agent_b.ps1" %*
set "AGENT_B_EXIT=%ERRORLEVEL%"
if not "%AGENT_B_EXIT%"=="0" (
  echo.
  echo Agent_b could not be opened. Review the error above or %LOCALAPPDATA%\Agent_b\logs\launcher-errors.log.
  if "%AGENT_B_AUTO_CLOSE%"=="0" (
    echo This window will close automatically in 10 seconds.
    timeout /t 10 /nobreak >nul 2>&1
  )
)
exit /b %AGENT_B_EXIT%
