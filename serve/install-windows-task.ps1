[CmdletBinding()]
param(
    [string]$BindAddress,
    [string]$FirewallLocalAddress,
    [string]$AllowedRemoteAddress,
    [ValidateRange(1, 65535)]
    [int]$Port = 8080,
    [string]$TaskName = 'AgentB llama-server',
    [string]$FirewallRule = 'AgentB llama-server (Tailscale)',
    [string]$RepoRoot,
    [string]$LogFile
)

$ErrorActionPreference = 'Stop'
$defaultRepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if (-not $RepoRoot) { $RepoRoot = $defaultRepoRoot }
if (-not $LogFile) { $LogFile = Join-Path $RepoRoot 'logs\llama-server.log' }

if (-not $BindAddress) { throw 'BindAddress is required.' }
if (-not $AllowedRemoteAddress) { throw 'AllowedRemoteAddress is required.' }
if (-not $FirewallLocalAddress) { $FirewallLocalAddress = $BindAddress }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this script from an elevated PowerShell session.'
}

$startScript = Join-Path $RepoRoot 'serve\start.ps1'
if (-not (Test-Path -LiteralPath $startScript -PathType Leaf)) {
    throw "start script not found: $startScript"
}

$logDirectory = Split-Path -Parent $LogFile
if ($logDirectory) { New-Item -ItemType Directory -Force -Path $logDirectory | Out-Null }

$arguments = '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{0}" -BIND_ADDRESS "{1}" -PORT {2} -LOG_FILE "{3}"' -f `
    $startScript, $BindAddress, $Port, $LogFile
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $arguments -WorkingDirectory $RepoRoot
$trigger = New-ScheduledTaskTrigger -AtStartup
$trigger.Delay = 'PT30S'
$taskPrincipal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -RestartCount 10 `
    -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Principal $taskPrincipal -Settings $settings `
    -Description 'Serves Agent_b local inference on the configured private address.' -Force | Out-Null

$rule = Get-NetFirewallRule -DisplayName $FirewallRule -ErrorAction SilentlyContinue | Select-Object -First 1
if ($rule) {
    $rule | Set-NetFirewallRule -Direction Inbound -Action Allow -Enabled True -Profile Any | Out-Null
    $rule | Get-NetFirewallPortFilter | Set-NetFirewallPortFilter -Protocol TCP -LocalPort $Port | Out-Null
    $rule | Get-NetFirewallAddressFilter | Set-NetFirewallAddressFilter `
        -LocalAddress $FirewallLocalAddress -RemoteAddress $AllowedRemoteAddress | Out-Null
} else {
    New-NetFirewallRule -DisplayName $FirewallRule -Direction Inbound -Action Allow -Enabled True `
        -Profile Any -Protocol TCP -LocalPort $Port -LocalAddress $FirewallLocalAddress `
        -RemoteAddress $AllowedRemoteAddress | Out-Null
}

Start-ScheduledTask -TaskName $TaskName

[pscustomobject]@{
    TaskName = $TaskName
    BindAddress = $BindAddress
    FirewallLocalAddress = $FirewallLocalAddress
    Port = $Port
    AllowedRemoteAddress = $AllowedRemoteAddress
    FirewallRule = $FirewallRule
    LogFile = $LogFile
} | ConvertTo-Json -Compress
