[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',

    [string]$ModelAddress = '127.0.0.1',

    [ValidateRange(1, 65535)]
    [int]$ModelPort = 8080,

    [switch]$Remove
)

$ErrorActionPreference = 'Stop'
$blockRuleName = 'AgentB-Svc-Outbound-Block'
$allowRuleName = 'AgentB-Svc-Model-Allow'

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-LocalUserSid {
    param([string]$Name)

    $user = Get-LocalUser -Name $Name -ErrorAction SilentlyContinue
    if (-not $user) {
        throw "Local account '$Name' does not exist. Run setup-service-account.ps1 first."
    }
    return $user.SID.Value
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
    exit 1
}

Write-Host 'AgentB service-account outbound firewall policy'
Write-Host "Account: $env:COMPUTERNAME\$AccountName"

if ($Remove) {
    foreach ($name in @($blockRuleName, $allowRuleName)) {
        $rule = Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue
        if (-not $rule) {
            Write-Host "UNCHANGED: firewall rule is already absent :: $name"
            continue
        }
        if ($PSCmdlet.ShouldProcess($name, 'Remove AgentB firewall rule')) {
            Remove-NetFirewallRule -Name $name
            Write-Host "REMOVED: $name"
        }
    }
    exit 0
}

$localUserSddl = if ($WhatIfPreference) {
    'D:(A;;CC;;;<resolved local-account SID>)'
} else {
    $sid = Resolve-LocalUserSid -Name $AccountName
    "D:(A;;CC;;;$sid)"
}

Write-Host "Requested block rule name: $blockRuleName"
Write-Host "Requested model exception: TCP $ModelAddress`:$ModelPort ($allowRuleName)"
Write-Host "LocalUser form: $localUserSddl"
Write-Host 'Verified Windows behavior: an explicit Block rule wins over a conflicting explicit Allow rule.'
Write-Host 'Therefore a blanket user-scoped Block plus a narrower model-server Allow would also block the model server.'

$profiles = Get-NetFirewallProfile | Sort-Object Name
$profileSummary = ($profiles | ForEach-Object { "$($_.Name)=$($_.DefaultOutboundAction)" }) -join ', '
Write-Host "Current profile DefaultOutboundAction values: $profileSummary"

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no firewall rule or profile setting will be changed.'
    Write-Host 'No rules would be created: the requested allow-list policy first requires an operator decision to set outbound default action to Block or to choose a different non-overlapping policy design.'
    Write-Host 'This script intentionally does not change the machine-wide outbound default.'
    exit 0
}

Write-Error 'No firewall rules were created. A correct deny-by-default policy with an allow-list requires a machine-wide outbound-default decision; this script will not make that decision or install a misleading block-plus-allow pair.' -ErrorAction Continue
exit 2
