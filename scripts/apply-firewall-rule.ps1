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
$script:removedRules = 0
$script:confirmationSuppressed = $PSBoundParameters.ContainsKey('Confirm') -and -not [bool]$PSBoundParameters['Confirm']

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-FirewallSummary {
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
    [Console]::Error.WriteLine('Run this command alone in an interactive console, or use -Confirm:$false for intentional non-interactive removal.')
    Write-FirewallSummary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('rerun safely after reviewing -WhatIf output')
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
    if (-not $user) {
        throw "Local account '$Name' does not exist. Run setup-service-account.ps1 first."
    }
    return $user.SID.Value
}

trap {
    [Console]::Error.WriteLine("FIREWALL SCRIPT FAILED: $($_.Exception.Message)")
    Write-FirewallSummary -Changed @("$script:removedRules reserved firewall rule(s) removed before failure") -NotChanged @('firewall profile defaults') -Next @('inspect reserved rules and rerun with -WhatIf')
    exit 1
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
    Write-FirewallSummary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('reopen PowerShell as Administrator and rerun')
    exit 1
}

Write-Host 'AgentB service-account outbound firewall policy'
Write-Host "Account: $env:COMPUTERNAME\$AccountName"

if ($Remove) {
    $removed = 0
    $absent = 0
    foreach ($name in @($blockRuleName, $allowRuleName)) {
        $rule = Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue
        if (-not $rule) {
            Write-Host "UNCHANGED: firewall rule is already absent :: $name"
            $absent++
            continue
        }
        if (Test-ConfirmationPromptExpected) {
            Assert-SafeConfirmationInput
        }
        if ($PSCmdlet.ShouldProcess($name, 'Remove AgentB firewall rule')) {
            Remove-NetFirewallRule -Name $name
            Write-Host "REMOVED: $name"
            $removed++
            $script:removedRules++
        }
    }
    Write-FirewallSummary -Changed @("$removed reserved firewall rule(s) removed") -NotChanged @("$absent reserved firewall rule(s) were already absent", 'firewall profile defaults') -Next @('verify the service-account split before applying any replacement outbound policy')
    exit 0
}

$localUserSddl = if ($WhatIfPreference) {
    'D:(A;;CC;;;<resolved local-account SID>)'
} else {
    try {
        $sid = Resolve-LocalUserSid -Name $AccountName
    } catch {
        [Console]::Error.WriteLine("Firewall policy setup failed: $($_.Exception.Message)")
        Write-FirewallSummary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('create and verify the service account, then rerun')
        exit 1
    }
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
    Write-FirewallSummary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('verify the service-account split', 'complete the separate firewall redesign before applying a policy')
    exit 0
}

Write-Error 'No firewall rules were created. A correct deny-by-default policy with an allow-list requires a machine-wide outbound-default decision; this script will not make that decision or install a misleading block-plus-allow pair.' -ErrorAction Continue
Write-FirewallSummary -Changed @() -NotChanged @('firewall rules', 'firewall profile defaults') -Next @('complete the separate non-overlapping firewall redesign')
exit 2
