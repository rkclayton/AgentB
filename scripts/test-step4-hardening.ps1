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
$applicationRoot = Join-Path $applicationTestRoot 'Application\Agent_b'
$dataRoot = Join-Path $dataTestRoot 'Data\Agent_b'
$workspaceRoot = Join-Path $workspaceTestRoot 'Agent_b\workspace'
$credentialPath = Join-Path $dataRoot '.agentb-shell-credential.dpapi'
$firewallRule = 'AgentB-Step4-Test-' + $suffix
$legacyRule = $firewallRule + '-Legacy'
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

    Write-Host 'VERIFY three-root ACL apply and verify'
    $aclAttempted = $true
    & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -NoPrompt -Confirm:$false
    & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -Verify

    Write-Host 'VERIFY service shell identity, workspace write, and application/data denial'
    $env:AGENTB_STEP4_LIVE_ACCOUNT = $account
    $env:AGENTB_STEP4_LIVE_APPLICATION_ROOT = $applicationRoot
    $env:AGENTB_STEP4_LIVE_DATA_ROOT = $dataRoot
    $env:AGENTB_STEP4_LIVE_WORKSPACE_ROOT = $workspaceRoot
    & $go test -count=1 ./internal/tools -run '^TestStep4LiveThreeRootShell$' -v
    if ($LASTEXITCODE -ne 0) { throw "Live service-shell test failed with exit code $LASTEXITCODE." }

    Write-Host 'VERIFY firewall apply and verify'
    $firewallAttempted = $true
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -NoPrompt -Confirm:$false
    & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -Verify

    Write-Host 'PASS: Step 4 live account, credential, ACL, shell, and firewall verification'
} finally {
    Remove-Item Env:\AGENTB_STEP4_LIVE_ACCOUNT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_APPLICATION_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_DATA_ROOT -ErrorAction SilentlyContinue
    Remove-Item Env:\AGENTB_STEP4_LIVE_WORKSPACE_ROOT -ErrorAction SilentlyContinue
    if ($firewallAttempted) {
        & (Join-Path $PSScriptRoot 'apply-firewall-rule.ps1') -AccountName $account -ModelAddress 127.0.0.1 -ModelPort 8080 -RuleName $firewallRule -LegacyAllowRuleName $legacyRule -Remove -NoPrompt -Confirm:$false
    }
    if ($aclAttempted -and $accountCreated) {
        & (Join-Path $PSScriptRoot 'apply-acls.ps1') -AccountName $account -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -Remove -NoPrompt -Confirm:$false
    }
    if ($accountCreated -and (Get-LocalUser -Name $account -ErrorAction SilentlyContinue)) {
        Remove-LocalUser -Name $account
    }
    foreach ($root in @($applicationTestRoot, $dataTestRoot, $workspaceTestRoot)) {
        if (Test-Path -LiteralPath $root) {
            if ($root -eq $applicationTestRoot) { $null = Assert-DisposableRoot $root $env:ProgramFiles }
            elseif ($root -eq $dataTestRoot) { $null = Assert-DisposableRoot $root ([Environment]::GetFolderPath('LocalApplicationData')) }
            else { $null = Assert-DisposableRoot $root $env:ProgramData }
            Remove-Item -LiteralPath $root -Recurse -Force
        }
    }
}
