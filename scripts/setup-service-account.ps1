[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc'
)

$ErrorActionPreference = 'Stop'

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-BuiltinGroupName {
    param([Security.Principal.WellKnownSidType]$SidType)

    $sid = [Security.Principal.SecurityIdentifier]::new($SidType, $null)
    return $sid.Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
    exit 1
}

$qualifiedName = "$env:COMPUTERNAME\$AccountName"
$existing = Get-LocalUser -Name $AccountName -ErrorAction SilentlyContinue

Write-Host 'AgentB service-account setup'
Write-Host "Account: $qualifiedName"

if ($existing) {
    Write-Host 'Result: existing local account found; idempotent no-op (no account or group changes made).'

    $administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
    $isAdministrator = Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue |
        Where-Object { $_.SID -eq $existing.SID }
    if ($isAdministrator) {
        Write-Warning 'The existing account is an Administrator. This script does not modify pre-existing accounts; remove that membership before using it for AgentB.'
    }

    Write-Host "Password never expires: $($existing.PasswordExpires -eq $null)"
    Write-Host 'Manual next steps: configure the harness launch under this account, then run apply-acls.ps1 and apply-firewall-rule.ps1.'
    exit 0
}

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no password will be requested and no account or group will be changed.'
    $null = $PSCmdlet.ShouldProcess($qualifiedName, 'Create a non-administrator local account with a non-expiring password')
    Write-Host 'Would retain ordinary Users membership because removing it can prevent local or batch launch.'
    Write-Host 'Would ensure the newly created account is not a member of Administrators.'
    Write-Host 'Would not grant Remote Desktop, interactive-logon, or other logon rights.'
    Write-Host 'Manual next steps: choose a launch mechanism, grant only the logon right it requires, configure the harness, then apply ACL and firewall policy.'
    exit 0
}

$securePassword = Read-Host "Enter the password for $qualifiedName" -AsSecureString
if (-not $PSCmdlet.ShouldProcess($qualifiedName, 'Create a non-administrator local account with a non-expiring password')) {
    Write-Host 'Result: cancelled; no account was created.'
    exit 0
}

New-LocalUser `
    -Name $AccountName `
    -Password $securePassword `
    -PasswordNeverExpires `
    -AccountNeverExpires `
    -Description 'Dedicated low-privilege account for AgentB' | Out-Null

$created = Get-LocalUser -Name $AccountName
$administrators = Get-BuiltinGroupName -SidType BuiltinAdministratorsSid
$adminMembership = Get-LocalGroupMember -Group $administrators -ErrorAction SilentlyContinue |
    Where-Object { $_.SID -eq $created.SID }
if ($adminMembership) {
    Remove-LocalGroupMember -Group $administrators -Member $created -Confirm:$false
}

Write-Host 'Result: created the local account with a non-expiring password and no Administrators membership.'
Write-Host 'Ordinary Users membership was retained so local or batch launch is not inadvertently broken.'
Write-Host 'No interactive-logon or task-scheduler rights were granted.'
Write-Host 'Manual next steps: choose a launch mechanism, grant only the logon right it requires, configure the harness, then run apply-acls.ps1 and apply-firewall-rule.ps1.'
