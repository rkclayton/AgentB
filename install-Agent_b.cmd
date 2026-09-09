@echo off
setlocal EnableExtensions
cd /d "%~dp0"

if not exist "%LOCALAPPDATA%\Agent_b\logs" mkdir "%LOCALAPPDATA%\Agent_b\logs"
for /f %%I in ('powershell.exe -NoLogo -NoProfile -Command "[DateTime]::Now.ToString('yyyyMMdd-HHmmss-fff')"') do set "AGENT_B_INSTALL_STAMP=%%I"
set "AGENT_B_INSTALL_LOG=%LOCALAPPDATA%\Agent_b\logs\installer-%AGENT_B_INSTALL_STAMP%.log"

powershell.exe -NoLogo -NoProfile -File "%~dp0scripts\install-Agent_b.ps1" -TranscriptPath "%AGENT_B_INSTALL_LOG%"
set "AGENT_B_EXIT=%ERRORLEVEL%"
echo.
if "%AGENT_B_EXIT%"=="0" (
  echo Agent_b installation is complete. Transcript: %AGENT_B_INSTALL_LOG%
) else (
  echo Agent_b installation failed with exit code %AGENT_B_EXIT%. Transcript: %AGENT_B_INSTALL_LOG%
)
pause
exit /b %AGENT_B_EXIT%
