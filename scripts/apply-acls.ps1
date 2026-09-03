[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',

    [string]$HarnessDirectory,

    [string]$WorkspaceDirectory,

    [string]$MemoryDirectory,

    [switch]$Verify
)

$ErrorActionPreference = 'Stop'
$script:appliedRules = 0
$script:unchangedRules = 0
$script:workspaceCreated = $false
$script:confirmationSuppressed = $PSBoundParameters.ContainsKey('Confirm') -and -not [bool]$PSBoundParameters['Confirm']

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-AclSummary {
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
    [Console]::Error.WriteLine('Run this command alone in an interactive console, or use -Confirm:$false for intentional non-interactive application.')
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('rerun safely after reviewing -WhatIf output')
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

function Resolve-ServiceIdentity {
    param([string]$Name)

    $localUser = Get-LocalUser -Name $Name -ErrorAction SilentlyContinue
    if (-not $localUser) {
        throw "Local account '$Name' does not exist. Run setup-service-account.ps1 first."
    }
    return $localUser.SID
}

function New-ManagedRule {
    param(
        [Security.Principal.SecurityIdentifier]$Identity,
        [Security.AccessControl.FileSystemRights]$Rights,
        [Security.AccessControl.InheritanceFlags]$Inheritance,
        [Security.AccessControl.AccessControlType]$Type
    )

    return [Security.AccessControl.FileSystemAccessRule]::new(
        $Identity,
        $Rights,
        $Inheritance,
        [Security.AccessControl.PropagationFlags]::None,
        $Type
    )
}

function Test-ManagedRule {
    param(
        [Security.AccessControl.FileSystemSecurity]$Acl,
        [Security.Principal.SecurityIdentifier]$Identity,
        [Security.AccessControl.FileSystemRights]$Rights,
        [Security.AccessControl.InheritanceFlags]$Inheritance,
        [Security.AccessControl.AccessControlType]$Type
    )

    foreach ($rule in $Acl.Access) {
        try {
            $ruleSid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier])
        } catch {
            continue
        }
        if ($ruleSid -eq $Identity -and
            -not $rule.IsInherited -and
            $rule.AccessControlType -eq $Type -and
            $rule.FileSystemRights -eq $Rights -and
            $rule.InheritanceFlags -eq $Inheritance -and
            $rule.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None) {
            return $true
        }
    }
    return $false
}

function Set-ManagedRule {
    param(
        [string]$Path,
        [Security.Principal.SecurityIdentifier]$Identity,
        [Security.AccessControl.FileSystemRights]$Rights,
        [Security.AccessControl.InheritanceFlags]$Inheritance,
        [Security.AccessControl.AccessControlType]$Type,
        [string]$Intent
    )

    $acl = Get-Acl -LiteralPath $Path
    if (Test-ManagedRule -Acl $acl -Identity $Identity -Rights $Rights -Inheritance $Inheritance -Type $Type) {
        Write-Host "UNCHANGED: $Intent :: $Path"
        $script:unchangedRules++
        return
    }
    if (Test-ConfirmationPromptExpected) {
        Assert-SafeConfirmationInput
    }
    if ($PSCmdlet.ShouldProcess($Path, $Intent)) {
        $rule = New-ManagedRule -Identity $Identity -Rights $Rights -Inheritance $Inheritance -Type $Type
        $null = $acl.AddAccessRule($rule)
        Set-Acl -LiteralPath $Path -AclObject $acl
        Write-Host "APPLIED: $Intent :: $Path"
        $script:appliedRules++
    }
}

trap {
    [Console]::Error.WriteLine("ACL SCRIPT FAILED: $($_.Exception.Message)")
    Write-AclSummary -Changed @("$script:appliedRules managed ACL rule(s) applied before failure", $(if ($script:workspaceCreated) { 'workspace directory was created' } else { 'no directory creation is claimed' })) -NotChanged @("$script:unchangedRules exact managed ACL rule(s)") -Next @('inspect the paths above, then rerun with -Verify')
    exit 1
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('reopen PowerShell as Administrator and rerun')
    exit 1
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($HarnessDirectory)) {
    $HarnessDirectory = $repositoryRoot
}
if ([string]::IsNullOrWhiteSpace($WorkspaceDirectory)) {
    $WorkspaceDirectory = Join-Path $HarnessDirectory 'workspace'
}
if ([string]::IsNullOrWhiteSpace($MemoryDirectory)) {
    $MemoryDirectory = Join-Path $HarnessDirectory 'memory'
}

$root = [IO.Path]::GetFullPath($HarnessDirectory)
$workspace = [IO.Path]::GetFullPath($WorkspaceDirectory)
$memory = [IO.Path]::GetFullPath($MemoryDirectory)
$logs = Join-Path $root 'logs'
$qualifiedName = "$env:COMPUTERNAME\$AccountName"

$denyRights = [Security.AccessControl.FileSystemRights]::WriteData `
    -bor [Security.AccessControl.FileSystemRights]::CreateFiles `
    -bor [Security.AccessControl.FileSystemRights]::AppendData `
    -bor [Security.AccessControl.FileSystemRights]::CreateDirectories `
    -bor [Security.AccessControl.FileSystemRights]::WriteExtendedAttributes `
    -bor [Security.AccessControl.FileSystemRights]::WriteAttributes `
    -bor [Security.AccessControl.FileSystemRights]::Delete `
    -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles `
    -bor [Security.AccessControl.FileSystemRights]::ChangePermissions `
    -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
$allowRights = [Security.AccessControl.FileSystemRights]::Modify
$none = [Security.AccessControl.InheritanceFlags]::None
$recursive = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
$deny = [Security.AccessControl.AccessControlType]::Deny
$allow = [Security.AccessControl.AccessControlType]::Allow

$recursiveControlDirectories = @('prompts', '.git', 'scripts', 'serve') | ForEach-Object { Join-Path $root $_ }
$fileTargets = @(
    (Join-Path $root 'harness.exe'),
    (Join-Path $root 'harness'),
    (Join-Path $root 'harness.json')
)
$fileTargets += Get-ChildItem -LiteralPath $root -File -Filter 'harness.*.json' -ErrorAction SilentlyContinue |
    Select-Object -ExpandProperty FullName
$fileTargets = $fileTargets | Sort-Object -Unique

Write-Host 'AgentB control-surface ACL policy'
Write-Host "Identity: $qualifiedName"
Write-Host "Harness directory: $root"
Write-Host "Workspace write grant: $workspace"
Write-Host 'DENY intent: write, create, append, delete, permission changes, and ownership changes; read/execute are not denied.'
Write-Warning "No deny ACE is applied to '$logs'. The verified identity split makes log separation possible, but this Prompt 16 ACL policy has not yet added that rule."
Write-Warning "No deny ACE is applied to '$memory'. The harness-owned remember tool still works, but service-account shell processes can reach this path unless another policy denies it."
Write-Host 'The harness remains under the operator identity, so service-account denies on harness.json do not block normal harness config saves.'

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no directory or ACL will be changed.'
    $null = $PSCmdlet.ShouldProcess($root, 'Add explicit object-only deny ACE to the harness binary directory')
    foreach ($path in $recursiveControlDirectories) {
        $null = $PSCmdlet.ShouldProcess($path, 'Add explicit inheritable deny ACE to the control directory')
    }
    foreach ($path in $fileTargets) {
        $exists = Test-Path -LiteralPath $path -PathType Leaf
        Write-Host "File target (exists=$($exists.ToString().ToLowerInvariant())): $path"
        if ($exists) {
            $null = $PSCmdlet.ShouldProcess($path, 'Add explicit object-only deny ACE to the control file')
        }
    }
    if (-not (Test-Path -LiteralPath $workspace -PathType Container)) {
        $null = $PSCmdlet.ShouldProcess($workspace, 'Create the workspace directory')
    }
    $null = $PSCmdlet.ShouldProcess($workspace, 'Add explicit inheritable Modify allow ACE for workspace files')
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('rerun without -WhatIf to apply', 'then rerun with -Verify')
    exit 0
}

try {
    $serviceSid = Resolve-ServiceIdentity -Name $AccountName
} catch {
    [Console]::Error.WriteLine("ACL setup failed: $($_.Exception.Message)")
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('create the service account, then rerun')
    exit 1
}

if ($Verify) {
    $drift = 0
    $checks = @(
        [pscustomobject]@{ Path = $root; Rights = $denyRights; Inheritance = $none; Type = $deny; Intent = 'harness directory deny' }
    )
    foreach ($path in $recursiveControlDirectories) {
        $checks += [pscustomobject]@{ Path = $path; Rights = $denyRights; Inheritance = $recursive; Type = $deny; Intent = 'recursive control deny' }
    }
    foreach ($path in $fileTargets) {
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            $checks += [pscustomobject]@{ Path = $path; Rights = $denyRights; Inheritance = $none; Type = $deny; Intent = 'control file deny' }
        } else {
            Write-Host "ABSENT: $path (the harness-directory deny prevents this identity from creating it)"
        }
    }
    $checks += [pscustomobject]@{ Path = $workspace; Rights = $allowRights; Inheritance = $recursive; Type = $allow; Intent = 'workspace Modify allow' }

    foreach ($check in $checks) {
        if (-not (Test-Path -LiteralPath $check.Path)) {
            Write-Host "DRIFT: missing path :: $($check.Path)"
            $drift++
            continue
        }
        $acl = Get-Acl -LiteralPath $check.Path
        if (Test-ManagedRule -Acl $acl -Identity $serviceSid -Rights $check.Rights -Inheritance $check.Inheritance -Type $check.Type) {
            Write-Host "PASS: $($check.Intent) :: $($check.Path)"
        } else {
            Write-Host "DRIFT: missing exact explicit ACE for $($check.Intent) :: $($check.Path)"
            $drift++
        }
    }
    Write-Host 'LIMITATION: this policy does not manage a logs deny ACE; verification cannot report log separation.'
    if ($drift -gt 0) {
        Write-Error "ACL verification found $drift drift item(s)." -ErrorAction Continue
        Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('review drift above and rerun without -Verify to repair it')
        exit 1
    }
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs; every enforceable intent is already correct') -Next @('verify the service-account shell identity before relying on these ACLs')
    exit 0
}

if (-not (Test-Path -LiteralPath $root -PathType Container)) {
    Write-Error "Harness directory does not exist: $root" -ErrorAction Continue
    Write-AclSummary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('correct -HarnessDirectory and rerun')
    exit 1
}

Set-ManagedRule -Path $root -Identity $serviceSid -Rights $denyRights -Inheritance $none -Type $deny -Intent 'Add explicit object-only deny ACE to the harness binary directory'
foreach ($path in $recursiveControlDirectories) {
    if (-not (Test-Path -LiteralPath $path -PathType Container)) {
        Write-Error "Required control directory does not exist: $path" -ErrorAction Continue
        Write-AclSummary -Changed @("$script:appliedRules managed ACL rule(s) before failure") -NotChanged @('missing control directory', 'remaining ACL intents') -Next @('restore or correct the control directory, then rerun and verify')
        exit 1
    }
    Set-ManagedRule -Path $path -Identity $serviceSid -Rights $denyRights -Inheritance $recursive -Type $deny -Intent 'Add explicit inheritable deny ACE to the control directory'
}
foreach ($path in $fileTargets) {
    if (Test-Path -LiteralPath $path -PathType Leaf) {
        Set-ManagedRule -Path $path -Identity $serviceSid -Rights $denyRights -Inheritance $none -Type $deny -Intent 'Add explicit object-only deny ACE to the control file'
    } else {
        Write-Host "ABSENT: $path (protected against creation by the harness-directory deny)"
    }
}
if (-not (Test-Path -LiteralPath $workspace -PathType Container)) {
    if (Test-ConfirmationPromptExpected) {
        Assert-SafeConfirmationInput
    }
    if ($PSCmdlet.ShouldProcess($workspace, 'Create the workspace directory')) {
        $null = New-Item -ItemType Directory -Path $workspace
        $script:workspaceCreated = $true
    }
}
Set-ManagedRule -Path $workspace -Identity $serviceSid -Rights $allowRights -Inheritance $recursive -Type $allow -Intent 'Add explicit inheritable Modify allow ACE for workspace files'

$directoryResult = if ($script:workspaceCreated) { 'workspace directory was created' } else { 'no directory creation was required' }
Write-AclSummary -Changed @("$script:appliedRules managed ACL rule(s) applied", $directoryResult) -NotChanged @("$script:unchangedRules exact managed ACL rule(s)", 'inheritance', 'logs ACL', 'memory ACL') -Next @('rerun with -Verify', 'verify the service-account shell identity before relying on these ACLs')
