[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [ValidateSet('Apply', 'Verify', 'Remove')]
    [string]$Mode = 'Apply',
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',
    [Parameter(Mandatory = $true)]
    [string]$HarnessDirectory,
    [Parameter(Mandatory = $true)]
    [string]$WorkspaceDirectory,
    [Parameter(Mandatory = $true)]
    [string]$ModelAddress,
    [ValidateRange(1, 65535)]
	[int]$ModelPort,
	# Internal result channel used by the non-elevated web process. The elevated
	# process cannot return its PowerShell streams through Start-Process reliably.
	[string]$ResultPath
)

$ErrorActionPreference = 'Stop'

trap {
    $message = $_.Exception.Message
    if (-not [string]::IsNullOrWhiteSpace($ResultPath)) {
        try { [IO.File]::WriteAllText($ResultPath, $message, [Text.UTF8Encoding]::new($false)) } catch { }
    }
    [Console]::Error.WriteLine($message)
    exit 1
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-HardeningScript {
    param([string]$Path, [string[]]$Arguments)
    $powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $all = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $Path) + $Arguments
    $output = @(& $powershell @all 2>&1 | ForEach-Object { [string]$_ })
    $exitCode = $LASTEXITCODE
    foreach ($line in $output) { Write-Host $line }
    if ($exitCode -ne 0) {
        $detail = ($output | Select-Object -Last 20) -join [Environment]::NewLine
        if ([string]::IsNullOrWhiteSpace($detail)) { $detail = 'no diagnostic output was returned' }
        throw "$(Split-Path -Leaf $Path) exited $exitCode`n$detail"
    }
}

$requiresElevation = $Mode -ne 'Verify' -and -not $WhatIfPreference
if ($requiresElevation -and -not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required to apply or remove Agent_b hardening.')
    exit 1
}

$aclScript = Join-Path $PSScriptRoot 'apply-acls.ps1'
$firewallScript = Join-Path $PSScriptRoot 'apply-firewall-rule.ps1'
$aclArguments = @('-AccountName', $AccountName, '-HarnessDirectory', $HarnessDirectory, '-WorkspaceDirectory', $WorkspaceDirectory, '-NoPrompt')
$firewallArguments = @('-AccountName', $AccountName, '-ModelAddress', $ModelAddress, '-ModelPort', $ModelPort.ToString(), '-NoPrompt')

if ($WhatIfPreference) {
    $aclArguments += '-WhatIf'
    $firewallArguments += '-WhatIf'
} elseif ($Mode -eq 'Verify') {
    $aclArguments += '-Verify'
    $firewallArguments += '-Verify'
} elseif ($Mode -eq 'Remove') {
    $firewallArguments += '-Remove'
    $aclArguments += '-Remove'
}

Write-Host "Agent_b hardening orchestration: $Mode"
Write-Host "Application: $HarnessDirectory"
Write-Host "Workspace: $WorkspaceDirectory"
Write-Host "Model endpoint: $ModelAddress`:$ModelPort"

if ($Mode -eq 'Remove') {
    Invoke-HardeningScript -Path $firewallScript -Arguments $firewallArguments
    Invoke-HardeningScript -Path $aclScript -Arguments $aclArguments
} else {
    Invoke-HardeningScript -Path $aclScript -Arguments $aclArguments
    Invoke-HardeningScript -Path $firewallScript -Arguments $firewallArguments
}

if ($Mode -eq 'Apply' -and -not $WhatIfPreference) {
    Invoke-HardeningScript -Path $aclScript -Arguments @('-AccountName', $AccountName, '-HarnessDirectory', $HarnessDirectory, '-WorkspaceDirectory', $WorkspaceDirectory, '-Verify')
    Invoke-HardeningScript -Path $firewallScript -Arguments @('-AccountName', $AccountName, '-ModelAddress', $ModelAddress, '-ModelPort', $ModelPort.ToString(), '-Verify')
}

Write-Host "AGENTB_HARDENING_COMPLETE=$Mode"
