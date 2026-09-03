[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',
    [string]$ModelAddress = '127.0.0.1',
    [ValidateRange(1, 65535)]
    [int]$ModelPort = 8080,
    [switch]$Verify,
    [switch]$Remove,
	[switch]$Inspect,
	[switch]$NoPrompt
)

$ErrorActionPreference = 'Stop'
$ruleName = 'AgentB-Svc-Outbound-Block'
$legacyAllowRuleName = 'AgentB-Svc-Model-Allow'
$statusMarker = 'AGENTB_FIREWALL_STATUS='
$blockedRanges = @(
    '0.0.0.0-100.63.255.255',
    '100.128.0.0-126.255.255.255',
    '128.0.0.0-255.255.255.255',
    '::/128',
    '::2-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff'
)
$script:confirmationSuppressed = $NoPrompt -or ($PSBoundParameters.ContainsKey('Confirm') -and -not [bool]$PSBoundParameters['Confirm'])
if ($NoPrompt) { $ConfirmPreference = 'None' }

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Summary {
    param([string[]]$Changed, [string[]]$NotChanged, [string[]]$Next)
    Write-Host ''
    Write-Host 'SUMMARY'
    Write-Host "Changed: $(if ($Changed.Count) { $Changed -join '; ' } else { 'nothing' })"
    Write-Host "Not changed: $(if ($NotChanged.Count) { $NotChanged -join '; ' } else { 'nothing' })"
    Write-Host "Next: $(if ($Next.Count) { $Next -join '; ' } else { 'no further action requested' })"
}

function Stop-UnsafeConfirmation {
    param([string]$Message)
    [Console]::Error.WriteLine("PROMPT REFUSED: $Message")
    [Console]::Error.WriteLine('Run this command alone in an interactive console, or use -Confirm:$false only after reviewing -WhatIf output.')
    Write-Summary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('rerun safely after reviewing -WhatIf output')
    exit 2
}

function Assert-SafeConfirmationInput {
    $redirected = $true
    try { $redirected = [Console]::IsInputRedirected } catch {
        Stop-UnsafeConfirmation -Message 'a usable console input stream could not be established.'
    }
    if ($redirected -or $Host.Name -ne 'ConsoleHost') {
        Stop-UnsafeConfirmation -Message 'input is redirected or the current PowerShell host has no usable interactive console.'
    }
    $buffered = $false
    try {
        while ([Console]::KeyAvailable) {
            $buffered = $true
            $null = [Console]::ReadKey($true)
        }
    } catch {
        Stop-UnsafeConfirmation -Message 'console input availability could not be verified safely.'
    }
    if ($buffered) {
        Stop-UnsafeConfirmation -Message 'queued console input was detected and drained. This commonly happens when a multi-line command block is pasted.'
    }
}

function Test-ConfirmationPromptExpected {
    return (-not $WhatIfPreference -and
        -not $script:confirmationSuppressed -and
        $ConfirmPreference -ne [Management.Automation.ConfirmImpact]::None -and
        [int][Management.Automation.ConfirmImpact]::High -ge [int]$ConfirmPreference)
}

function Resolve-LocalUserSid {
    param([string]$Name)
    $user = Get-LocalUser -Name $Name -ErrorAction SilentlyContinue
    if (-not $user) { throw "Local account '$Name' does not exist. Create and verify it in AgentB Settings first." }
    return $user.SID.Value
}

function Test-AllowedModelAddress {
    param([string]$Address)
    try { $ip = [Net.IPAddress]::Parse($Address) } catch { return $false }
    if ($ip.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetworkV6) {
        return $ip.Equals([Net.IPAddress]::IPv6Loopback)
    }
    $bytes = $ip.GetAddressBytes()
    return $bytes[0] -eq 127 -or ($bytes[0] -eq 100 -and $bytes[1] -ge 64 -and $bytes[1] -le 127)
}

function Test-RuleIntent {
    param([string]$LocalUserSddl)
    $rule = Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue
    if (-not $rule) { return $false }
    if ($rule.Direction -ne 'Outbound' -or $rule.Action -ne 'Block' -or $rule.Enabled -ne 'True' -or $rule.Profile -ne 'Any') { return $false }
    if ($rule.LocalUser -ne $LocalUserSddl) { return $false }
    $actual = @((Get-NetFirewallAddressFilter -AssociatedNetFirewallRule $rule).RemoteAddress | Sort-Object)
    $expected = @($blockedRanges | Sort-Object)
    return ($actual.Count -eq $expected.Count -and -not (Compare-Object -ReferenceObject $expected -DifferenceObject $actual))
}

if (($Verify.IsPresent -and $Remove.IsPresent) -or ($Inspect.IsPresent -and ($Verify.IsPresent -or $Remove.IsPresent))) {
    [Console]::Error.WriteLine('Choose only one of -Verify, -Remove, or -Inspect.')
    exit 2
}
if (-not (Test-AllowedModelAddress -Address $ModelAddress)) {
    [Console]::Error.WriteLine("ModelAddress must be an IP inside 127.0.0.0/8, 100.64.0.0/10, or IPv6 loopback. '$ModelAddress' would be blocked by this policy.")
    exit 2
}

Write-Host 'AgentB service-account outbound firewall policy'
Write-Host "Account: $env:COMPUTERNAME\$AccountName"
Write-Host "Model endpoint confirmed inside the spared local/Tailscale ranges: $ModelAddress`:$ModelPort"
Write-Host 'Policy: one user-scoped outbound Block rule; spare IPv4 loopback 127.0.0.0/8, Tailscale 100.64.0.0/10, and IPv6 loopback ::1.'
Write-Host 'No Allow rule is created and machine-wide DefaultOutboundAction is not changed.'

if (-not (Test-IsAdministrator) -and -not $WhatIfPreference -and -not $Verify -and -not $Inspect) {
    [Console]::Error.WriteLine('Administrator elevation is required to apply or remove the firewall rule.')
    Write-Summary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('use AgentB Settings or reopen PowerShell as Administrator')
    exit 1
}

if ($Remove) {
    $present = @(Get-NetFirewallRule -Name $ruleName, $legacyAllowRuleName -ErrorAction SilentlyContinue)
    if ($present.Count -eq 0) {
        Write-Host 'UNCHANGED: AgentB reserved firewall rules are already absent.'
        Write-Summary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('remove ACLs before deleting the service account if rolling back fully')
        exit 0
    }
    if (Test-ConfirmationPromptExpected) { Assert-SafeConfirmationInput }
    if ($PSCmdlet.ShouldProcess(($present.Name -join ', '), 'Remove AgentB firewall rules')) {
        $present | Remove-NetFirewallRule
        Write-Host "REMOVED: $($present.Name -join ', ')"
    }
    Write-Summary -Changed @('reserved AgentB firewall rules removed') -NotChanged @('firewall profile defaults') -Next @('remove ACLs before deleting the service account if rolling back fully')
    exit 0
}

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no firewall rule or profile setting will be changed.'
    $null = $PSCmdlet.ShouldProcess($ruleName, "Create or repair user-scoped outbound Block over: $($blockedRanges -join ', ')")
    Write-Summary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('apply from AgentB Settings, then verify')
    exit 0
}

$sid = $null
try { $sid = Resolve-LocalUserSid -Name $AccountName } catch {
    if ($Inspect) {
        $status = [ordered]@{ supported = $true; account_exists = $false; applied = $false; summary = $_.Exception.Message }
        Write-Output ($statusMarker + ($status | ConvertTo-Json -Compress))
        exit 0
    }
    [Console]::Error.WriteLine("Firewall policy failed: $($_.Exception.Message)")
    exit 1
}
$localUserSddl = "D:(A;;CC;;;$sid)"
$correct = Test-RuleIntent -LocalUserSddl $localUserSddl
$legacyPresent = [bool](Get-NetFirewallRule -Name $legacyAllowRuleName -ErrorAction SilentlyContinue)

if ($Inspect) {
    $status = [ordered]@{ supported = $true; account_exists = $true; applied = ($correct -and -not $legacyPresent); summary = $(if ($correct -and -not $legacyPresent) { 'user-scoped outbound policy verified' } else { 'firewall rule missing or drifted' }) }
    Write-Output ($statusMarker + ($status | ConvertTo-Json -Compress))
    exit 0
}
if ($Verify) {
    Write-Host "$(if ($correct) { 'PASS' } else { 'DRIFT' }): $ruleName"
    Write-Host "$(if (-not $legacyPresent) { 'PASS' } else { 'DRIFT' }): no conflicting legacy Allow rule"
    Write-Summary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @($(if ($correct -and -not $legacyPresent) { 'firewall verification complete' } else { 'apply again to repair drift' }))
    if (-not $correct -or $legacyPresent) { exit 1 }
    exit 0
}

if ($correct -and -not $legacyPresent) {
    Write-Host "UNCHANGED: exact firewall policy already exists :: $ruleName"
    Write-Summary -Changed @() -NotChanged @('firewall rule', 'firewall profile defaults') -Next @('run the RBAC network check')
    exit 0
}

if (Test-ConfirmationPromptExpected) { Assert-SafeConfirmationInput }
if ($PSCmdlet.ShouldProcess($ruleName, 'Create or repair AgentB user-scoped outbound Block rule')) {
    Get-NetFirewallRule -Name $ruleName, $legacyAllowRuleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    $null = New-NetFirewallRule -Name $ruleName -DisplayName $ruleName -Description 'Blocks AgentB service-account egress except loopback and Tailscale address ranges.' -Direction Outbound -Action Block -Enabled True -Profile Any -LocalUser $localUserSddl -RemoteAddress $blockedRanges
    Write-Host "APPLIED: $ruleName"
}
Write-Summary -Changed @('user-scoped outbound Block rule applied') -NotChanged @('firewall profile defaults') -Next @('verify from Settings', 'run the RBAC network check')
