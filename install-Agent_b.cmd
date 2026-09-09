@echo off
setlocal EnableExtensions
cd /d "%~dp0"

if not defined AGENT_B_INSTALL_LOG (
  if not exist "%LOCALAPPDATA%\Agent_b\logs" mkdir "%LOCALAPPDATA%\Agent_b\logs"
  for /f %%I in ('powershell.exe -NoLogo -NoProfile -Command "Get-Date -Format yyyyMMdd-HHmmss-fff"') do set "AGENT_B_INSTALL_STAMP=%%I"
)
if not defined AGENT_B_INSTALL_LOG (
  if not defined AGENT_B_INSTALL_STAMP (
    echo Agent_b installation could not create a transcript timestamp.
    if not defined AGENT_B_INSTALL_NO_PAUSE pause
    exit /b 1
  )
  set "AGENT_B_INSTALL_LOG=%LOCALAPPDATA%\Agent_b\logs\installer-%AGENT_B_INSTALL_STAMP%.log"
)
for %%I in ("%AGENT_B_INSTALL_LOG%") do if not exist "%%~dpI" mkdir "%%~dpI"

powershell.exe -NoLogo -NoProfile -File "%~dp0scripts\install-Agent_b.ps1" -TranscriptPath "%AGENT_B_INSTALL_LOG%" %*
set "AGENT_B_EXIT=%ERRORLEVEL%"
echo.
if not "%AGENT_B_EXIT%"=="0" (
  echo Agent_b installation failed with exit code %AGENT_B_EXIT%. Transcript: %AGENT_B_INSTALL_LOG%
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b %AGENT_B_EXIT%
)

powershell.exe -NoLogo -NoProfile -Command "$p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent()); if($p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){exit 0}else{exit 1}" >nul 2>&1
if not errorlevel 1 (
  echo Agent_b installation is complete, but autostart was skipped because this wrapper is elevated. Open Agent_b from the Start menu. Transcript: %AGENT_B_INSTALL_LOG%
  set "AGENT_B_INSTALL_RECORD=AUTOSTART SKIPPED: wrapper is elevated; open Agent_b from the Start menu."
  call :append_install_record
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b 0
)

for /f "tokens=1,* delims=:" %%A in ('findstr.exe /b /c:"Application: " "%AGENT_B_INSTALL_LOG%"') do set "AGENT_B_INSTALLED_ROOT=%%B"
for /f "tokens=1,* delims=:" %%A in ('findstr.exe /b /c:"Operator data: " "%AGENT_B_INSTALL_LOG%"') do set "AGENT_B_INSTALLED_DATA=%%B"
for /f "tokens=* delims= " %%I in ("%AGENT_B_INSTALLED_ROOT%") do set "AGENT_B_INSTALLED_ROOT=%%I"
for /f "tokens=* delims= " %%I in ("%AGENT_B_INSTALLED_DATA%") do set "AGENT_B_INSTALLED_DATA=%%I"
if not defined AGENT_B_INSTALLED_ROOT (
  echo Agent_b was installed but could not start because the application root is missing from the transcript: %AGENT_B_INSTALL_LOG%
  set "AGENT_B_INSTALL_RECORD=AUTOSTART FAILED: application root is missing from the transcript."
  call :append_install_record
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b 1
)
if not defined AGENT_B_INSTALLED_DATA (
  echo Agent_b was installed but could not start because the data root is missing from the transcript: %AGENT_B_INSTALL_LOG%
  set "AGENT_B_INSTALL_RECORD=AUTOSTART FAILED: operator data root is missing from the transcript."
  call :append_install_record
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b 1
)
set "AGENT_B_INSTALLED_LAUNCHER=%AGENT_B_INSTALLED_ROOT%\Agent_b.cmd"
if not exist "%AGENT_B_INSTALLED_LAUNCHER%" (
  echo Agent_b was installed but could not start because %AGENT_B_INSTALLED_LAUNCHER% is missing. Transcript: %AGENT_B_INSTALL_LOG%
  set "AGENT_B_INSTALL_RECORD=AUTOSTART FAILED: installed launcher is missing: %AGENT_B_INSTALLED_LAUNCHER%"
  call :append_install_record
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b 1
)

set "AGENT_B_AUTOSTART_BROWSER="
if defined AGENT_B_INSTALL_NO_BROWSER set "AGENT_B_AUTOSTART_BROWSER=-NoBrowser"
powershell.exe -NoLogo -NoProfile -Command "$launchArgs=@('-Console','-Detached','-NoPause','-DataDirectory',$env:AGENT_B_INSTALLED_DATA); if($env:AGENT_B_AUTOSTART_BROWSER){$launchArgs += $env:AGENT_B_AUTOSTART_BROWSER}; $output = @(& $env:AGENT_B_INSTALLED_LAUNCHER @launchArgs 2>&1 | ForEach-Object { $_.ToString() }); $code=$LASTEXITCODE; $output | Write-Output; if($output.Count){[IO.File]::AppendAllText($env:AGENT_B_INSTALL_LOG, (($output -join [Environment]::NewLine) + [Environment]::NewLine), [Text.UTF8Encoding]::new($false))}; exit $code"
set "AGENT_B_START_EXIT=%ERRORLEVEL%"
if not "%AGENT_B_START_EXIT%"=="0" (
  echo Agent_b was installed but failed to start with exit code %AGENT_B_START_EXIT%. Transcript: %AGENT_B_INSTALL_LOG%
  set "AGENT_B_INSTALL_RECORD=AUTOSTART FAILED: Agent_b exited with code %AGENT_B_START_EXIT%."
  call :append_install_record
  if not defined AGENT_B_INSTALL_NO_PAUSE pause
  exit /b %AGENT_B_START_EXIT%
)

echo Agent_b installation is complete and Agent_b started. Transcript: %AGENT_B_INSTALL_LOG%
set "AGENT_B_INSTALL_RECORD=AUTOSTART COMPLETE: Agent_b started through %AGENT_B_INSTALLED_LAUNCHER%."
call :append_install_record
if not defined AGENT_B_INSTALL_NO_PAUSE pause
exit /b 0

:append_install_record
powershell.exe -NoLogo -NoProfile -Command "[IO.File]::AppendAllText($env:AGENT_B_INSTALL_LOG, $env:AGENT_B_INSTALL_RECORD + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))"
exit /b %ERRORLEVEL%
