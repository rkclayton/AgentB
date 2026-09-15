[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Assert-DisposableRoot {
    param([string]$Path, [string]$ExpectedParent)
    $full = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    $parent = [IO.Path]::GetFullPath($ExpectedParent).TrimEnd('\') + '\'
    if (-not $full.StartsWith($parent, [StringComparison]::OrdinalIgnoreCase) -or
        -not (Split-Path -Leaf $full).StartsWith('Agent_b-Step4-Test-', [StringComparison]::Ordinal)) {
        throw "Refusing to remove non-disposable verification root: $full"
    }
    return $full
}

function Get-ConfirmedPrivateSubnet {
    foreach ($configuration in Get-NetIPConfiguration) {
        if ($configuration.NetAdapter.Status -ne 'Up') { continue }
        foreach ($address in $configuration.IPv4Address) {
            $ip = [Net.IPAddress]::Parse($address.IPAddress)
            $bytes = $ip.GetAddressBytes()
            $private = $bytes[0] -eq 10 -or ($bytes[0] -eq 172 -and $bytes[1] -ge 16 -and $bytes[1] -le 31) -or ($bytes[0] -eq 192 -and $bytes[1] -eq 168)
            if (-not $private) { continue }
            $bits = [int]$address.PrefixLength
            $value = ([uint64]$bytes[0] * 16777216) + ([uint64]$bytes[1] * 65536) + ([uint64]$bytes[2] * 256) + [uint64]$bytes[3]
            $size = [math]::Pow(2, 32 - $bits)
            $network = [uint64]([math]::Floor($value / $size) * $size)
            return "$([math]::Floor($network / 16777216) % 256).$([math]::Floor($network / 65536) % 256).$([math]::Floor($network / 256) % 256).$($network % 256)/$bits"
        }
    }
    throw 'No active RFC1918 IPv4 subnet is available for the LAN-switch verification.'
}

if (-not (Test-IsAdministrator)) {
    throw 'Step 4 hardening verification must run in an elevated PowerShell owned by the operator.'
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $repositoryRoot '.tools\go\bin\go.exe'
if (-not (Test-Path -LiteralPath $go -PathType Leaf)) { $go = (Get-Command go.exe -ErrorAction Stop).Source }

$suffix = [Guid]::NewGuid().ToString('N').Substring(0, 12)
$account = 'ab4-' + $suffix
$testName = 'Agent_b-Step4-Test-' + $suffix
$applicationTestRoot = Assert-DisposableRoot (Join-Path $env:ProgramFiles $testName) $env:ProgramFiles
$dataTestRoot = Assert-DisposableRoot (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) $testName) ([Environment]::GetFolderPath('LocalApplicationData'))
$workspaceTestRoot = Assert-DisposableRoot (Join-Path $env:ProgramData $testName) $env:ProgramData
$exchangeTestRoot = Assert-DisposableRoot (Join-Path $env:USERPROFILE $testName) $env:USERPROFILE
$applicationRoot = Join-Path $applicationTestRoot 'Application\Agent_b'
$dataRoot = Join-Path $dataTestRoot 'Data\Agent_b'
$workspaceRoot = Join-Path $workspaceTestRoot 'Agent_b\workspace'
$exchangeRoot = Join-Path $exchangeTestRoot 'Agent_b'
$credentialPath = Join-Path $dataRoot '.agentb-shell-credential.dpapi'
$firewallRule = 'AgentB-Step4-Test-' + $suffix
$legacyRule = $firewallRule + '-Legacy'
$icmpRule = $firewallRule + '-LAN-ICMP'
$confirmedSubnet = Get-ConfirmedPrivateSubnet
$accountCreated = $false
$aclAttempted = $false
$firewallAttempted = $false

try {
    $null = New-Item -ItemType Directory -Path $applicationRoot -Force
    $null = New-Item -ItemType Directory -Path $dataRoot -Force
    $null = New-Item -ItemType Directory -Path $workspaceRoot -Force
    Set-Content -LiteralPath (Join-Path $applicationRoot 'application-marker.txt') -Value 'immutable application test'
    Set-Content -LiteralPath (Join-Path $dataRoot 'harness.json') -Value '{}'

    $passwordText = 'Aa9!' + [Guid]::NewGuid().ToString('N')
    $plain = [Text.Encoding]::UTF8.GetBytes($passwordText)
    $protected = $null
    try {
        Add-Type -AssemblyName System.Security
        $protected = [Security.Cryptography.ProtectedData]::Protect($plain, $null, [Security.Cryptography.DataProtectionScope]::CurrentUser)
        [IO.File]::WriteAllBytes($credentialPath, $protected)
    } finally {
        if ($plain) { [Array]::Clear($plain, 0, $plain.Length) }
        if ($protected) { [Array]::Clear($protected, 0, $protected.Length) }
        $passwordText = $null
    }

    Write-Host "VERIFY account creation: $account"
    & (Join-Path $PSScriptRoot 'setup-service-account.ps1') -AccountName $account -CredentialStore $credentialPath -NoPrompt -Confirm:$false
    $accountCreated = [bool](Get-LocalUser -Name $account -ErrorAction SilentlyContinue)
    if (-not $accountCreated) { throw 'Service-account helper returned without creating the disposable account.' }

    Write-Host 'VERIFY credential storage and decryption'
    & (Join-Path $PSScriptRoot 'setup-service-account.ps1') -AccountName $account -CredentialStore $credentialPath -ValidateCredentialStore

    Write-Host 'VERIFY upgraded-root ACL repair and shared apply/verify predicate'
    $aclAttempted = $true
    $accountSid = (Get-LocalUser -Name $account -ErrorAction Stop).SID
    $legacyAcl = Get-Acl -LiteralPath $dataRoot
    $legacyDataRule = [Security.AccessControl.FileSystemAccessRule]::new(
        $accountSid,
        [Security.AccessControl.FileSystemRights]::FullControl,
        ([Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit),
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Deny
    )
    $legacyAcl.SetAccessRule($legacyDataRule)
    Set-Acl -LiteralPath $dataRoot -AclObject $legacyAcl
    $upgradeOutput = (& (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -ExchangeDirectory $exchangeRoot -NoPrompt -Confirm:$false 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $upgradeOutput -notmatch 'APPLIED: deny service identity access to operator data except traversal') {
        throw "Upgraded-root policy did not replace the legacy FullControl deny ACE.`n$upgradeOutput"
    }
    & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -ExchangeDirectory $exchangeRoot -Verify
    if ($LASTEXITCODE -ne 0) { throw 'Upgraded-root policy failed verification after apply.' }
    $expectedDataRights = ([Security.AccessControl.FileSystemAccessRule]::new(
        $accountSid,
        [Security.AccessControl.FileSystemRights]([int][Security.AccessControl.FileSystemRights]::FullControl -band (-bnot [int][Security.AccessControl.FileSystemRights]::Traverse)),
        ([Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit),
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Deny
    )).FileSystemRights
    $dataRules = @((Get-Acl -LiteralPath $dataRoot).Access | Where-Object {
        try { $ruleSid = $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]) } catch { return $false }
        $ruleSid -eq $accountSid -and -not $_.IsInherited -and $_.AccessControlType -eq [Security.AccessControl.AccessControlType]::Deny -and
            $_.InheritanceFlags -eq ([Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit) -and
            $_.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None
    })
    if ($dataRules.Count -ne 1 -or $dataRules[0].FileSystemRights -ne $expectedDataRights) {
        throw 'Upgraded-root policy did not leave exactly the verifier-approved data-root deny ACE.'
    }

    Write-Host 'VERIFY root and exchange-folder ACL idempotence'
    & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -ExchangeDirectory $exchangeRoot -NoPrompt -Confirm:$false
    if ($LASTEXITCODE -ne 0) { throw 'Idempotent ACL reapply failed.' }

    Write-Host 'VERIFY service shell identity, workspace write, and application/data denial'
    $env:AGENTB_STEP4_LIVE_ACCOUNT = $account
    $env:AGENTB_STEP4_LIVE_APPLICATION_ROOT = $applicationRoot
    $env:AGENTB_STEP4_LIVE_DATA_ROOT = $dataRoot
    $env:AGENTB_STEP4_LIVE_PLANS_ROOT = Join-Path $dataRoot 'plans'
    $env:AGENTB_STEP4_LIVE_WORKSPACE_ROOT = $workspaceRoot
    & $go test -count=1 ./internal/tools -run '^TestStep4LiveThreeRootShell$' -v
    if ($LASTEXITCODE -ne 0) { throw "Live service-shell test failed with exit code $LASTEXITCODE." }

    Write-Host "VERIFY firewall apply and verify with LAN switch on: $confirmedSubnet"
    $firewallAttempted = $true
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -LANICMPRuleName $icmpRule -AllowLocalNetwork -LocalSubnet $confirmedSubnet -NoPrompt -Confirm:$false
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -LANICMPRuleName $icmpRule -AllowLocalNetwork -LocalSubnet $confirmedSubnet -Verify

	Write-Host 'VERIFY firewall apply and verify with LAN switch off'
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -LANICMPRuleName $icmpRule -NoPrompt -Confirm:$false
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -LANICMPRuleName $icmpRule -Verify

    Write-Host 'PASS: Step 4 live account, credential, ACL, shell, and firewall verification'
} finally {
    Remove-Item Env:\AGENTB_STEP4_LIVE_ACCOUNT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_APPLICATION_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_DATA_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_PLANS_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_WORKSPACE_ROOT -ErrorAction SilentlyContinue
    if ($firewallAttempted) {
        & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -LANICMPRuleName $icmpRule -Remove -NoPrompt -Confirm:$false
    }
    if ($aclAttempted -and $accountCreated) {
        & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -ExchangeDirectory $exchangeRoot -Remove -NoPrompt -Confirm:$false
    }
    if ($accountCreated -and (Get-LocalUser -Name $account -ErrorAction SilentlyContinue)) {
        Remove-LocalUser -Name $account
    }
    foreach ($root in @($applicationTestRoot, $dataTestRoot, $workspaceTestRoot, $exchangeTestRoot)) {
        if (Test-Path -LiteralPath $root) {
            if ($root -eq $applicationTestRoot) { $null = Assert-DisposableRoot $root $env:ProgramFiles }
            elseif ($root -eq $dataTestRoot) { $null = Assert-DisposableRoot $root ([Environment]::GetFolderPath('LocalApplicationData')) }
            elseif ($root -eq $exchangeTestRoot) { $null = Assert-DisposableRoot $root $env:USERPROFILE }
            else { $null = Assert-DisposableRoot $root $env:ProgramData }
            Remove-Item -LiteralPath $root -Recurse -Force
        }
    }
}
