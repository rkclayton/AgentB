[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [ValidatePattern('^[A-Za-z0-9._-]+$')]
    [string]$AccountName = 'agentb-svc',
    [Parameter(Mandatory = $true)]
    [string]$ApplicationDirectory,
    [Parameter(Mandatory = $true)]
    [string]$DataDirectory,
    [Parameter(Mandatory = $true)]
    [string]$WorkspaceDirectory,
    [switch]$Verify,
    [switch]$Remove,
	[switch]$Inspect,
	[switch]$NoPrompt
)

$ErrorActionPreference = 'Stop'
$statusMarker = 'AGENTB_ACL_STATUS='
$script:changed = 0
$script:unchanged = 0
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
    Write-Summary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('rerun safely after reviewing -WhatIf output')
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
        throw "Local account '$Name' does not exist. Create and verify it in Agent_b Settings first."
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

function Get-ExactManagedRules {
    param(
        [Security.AccessControl.FileSystemSecurity]$Acl,
        [Security.Principal.SecurityIdentifier]$Identity,
        [Security.AccessControl.FileSystemRights]$Rights,
        [Security.AccessControl.InheritanceFlags]$Inheritance,
        [Security.AccessControl.AccessControlType]$Type
    )
    # FileSystemAccessRule normalizes composite rights (notably Modify) by
    # adding Synchronize. Compare against a constructed rule's effective
    # rights rather than the unnormalized enum supplied by the caller.
    $expected = New-ManagedRule -Identity $Identity -Rights $Rights -Inheritance $Inheritance -Type $Type
    return @($Acl.Access | Where-Object {
        try { $ruleSid = $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]) } catch { return $false }
        return $ruleSid -eq $Identity -and
            -not $_.IsInherited -and
            $_.AccessControlType -eq $Type -and
            $_.FileSystemRights -eq $expected.FileSystemRights -and
            $_.InheritanceFlags -eq $Inheritance -and
            $_.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None
    })
}

function Set-ManagedRule {
    param([pscustomobject]$Target, [Security.Principal.SecurityIdentifier]$Identity)
    $acl = Get-Acl -LiteralPath $Target.Path
    if ((Get-ExactManagedRules -Acl $acl -Identity $Identity -Rights $Target.Rights -Inheritance $Target.Inheritance -Type $Target.Type).Count -gt 0) {
        Write-Host "UNCHANGED: $($Target.Intent) :: $($Target.Path)"
        $script:unchanged++
        return
    }
    if (Test-ConfirmationPromptExpected) { Assert-SafeConfirmationInput }
    if ($PSCmdlet.ShouldProcess($Target.Path, $Target.Intent)) {
        if ($Target.NativeTraverse) {
            # Set-Acl can trigger a costly inheritance recalculation across a
            # large user-profile tree. icacls adds this non-inheriting ACE to
            # the directory itself without walking its descendants.
            & icacls.exe $Target.Path /grant ("*$($Identity.Value):(X,S)") | Out-Host
            if ($LASTEXITCODE -ne 0) { throw "icacls failed for parent traverse path: $($Target.Path)" }
        } else {
            $null = $acl.AddAccessRule((New-ManagedRule -Identity $Identity -Rights $Target.Rights -Inheritance $Target.Inheritance -Type $Target.Type))
            Set-Acl -LiteralPath $Target.Path -AclObject $acl
        }
        Write-Host "APPLIED: $($Target.Intent) :: $($Target.Path)"
        $script:changed++
    }
}

function Remove-ManagedRule {
    param([pscustomobject]$Target, [Security.Principal.SecurityIdentifier]$Identity)
    $acl = Get-Acl -LiteralPath $Target.Path
    $rules = Get-ExactManagedRules -Acl $acl -Identity $Identity -Rights $Target.Rights -Inheritance $Target.Inheritance -Type $Target.Type
    if ($rules.Count -eq 0) {
        Write-Host "UNCHANGED: managed ACE already absent :: $($Target.Path)"
        $script:unchanged++
        return
    }
    if (Test-ConfirmationPromptExpected) { Assert-SafeConfirmationInput }
    if ($PSCmdlet.ShouldProcess($Target.Path, 'Remove Agent_b managed ACL rule')) {
        foreach ($rule in $rules) { $acl.RemoveAccessRuleSpecific($rule) }
        Set-Acl -LiteralPath $Target.Path -AclObject $acl
        Write-Host "REMOVED: Agent_b managed ACL rule :: $($Target.Path)"
        $script:changed++
    }
}

if (($Verify.IsPresent -and $Remove.IsPresent) -or ($Inspect.IsPresent -and ($Verify.IsPresent -or $Remove.IsPresent))) {
    [Console]::Error.WriteLine('Choose only one of -Verify, -Remove, or -Inspect.')
    exit 2
}

$application = [IO.Path]::GetFullPath($ApplicationDirectory).TrimEnd('\')
$data = [IO.Path]::GetFullPath($DataDirectory).TrimEnd('\')
$workspace = [IO.Path]::GetFullPath($WorkspaceDirectory).TrimEnd('\')

foreach ($required in @(
    @{ Name = 'application'; Path = $application },
    @{ Name = 'operator data'; Path = $data }
)) {
    if (-not (Test-Path -LiteralPath $required.Path -PathType Container)) {
        [Console]::Error.WriteLine("Agent_b $($required.Name) directory does not exist: $($required.Path)")
        exit 1
    }
}

function Test-PathInside {
    param([string]$Child, [string]$Parent)
    return $Child.StartsWith($Parent.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)
}
if ($application.Equals($data, [StringComparison]::OrdinalIgnoreCase) -or
    $application.Equals($workspace, [StringComparison]::OrdinalIgnoreCase) -or
    $data.Equals($workspace, [StringComparison]::OrdinalIgnoreCase) -or
    (Test-PathInside -Child $application -Parent $data) -or
    (Test-PathInside -Child $data -Parent $application) -or
    (Test-PathInside -Child $workspace -Parent $application) -or
    (Test-PathInside -Child $workspace -Parent $data) -or
    (Test-PathInside -Child $application -Parent $workspace) -or
    (Test-PathInside -Child $data -Parent $workspace)) {
    [Console]::Error.WriteLine('Application, operator-data, and workspace directories must be three disjoint trees.')
    exit 1
}

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
$denyDataRights = [Security.AccessControl.FileSystemRights]::FullControl
$traverseRights = [Security.AccessControl.FileSystemRights]::ReadAndExecute
$parentTraverseRights = [Security.AccessControl.FileSystemRights]::Traverse
$none = [Security.AccessControl.InheritanceFlags]::None
$recursive = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
$deny = [Security.AccessControl.AccessControlType]::Deny
$allow = [Security.AccessControl.AccessControlType]::Allow

$targets = @()
$traversePaths = @{}
$sharedAnchors = @(
    [IO.Path]::GetFullPath($env:ProgramFiles).TrimEnd('\'),
    [IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\')
)
foreach ($reachable in @($application, $workspace)) {
    $parent = [IO.DirectoryInfo]$reachable
    while ($parent.Parent -and $parent.Parent.Parent) {
        $parent = $parent.Parent
        $parentPath = $parent.FullName.TrimEnd('\')
        if ($sharedAnchors -contains $parentPath) { continue }
        if (-not $traversePaths.ContainsKey($parentPath)) {
            $traversePaths[$parentPath] = $true
            $targets += [pscustomobject]@{ Path = $parentPath; Rights = $parentTraverseRights; Inheritance = $none; Type = $allow; Intent = 'grant parent-directory traverse'; NativeTraverse = $true }
        }
    }
}
$targets += @(
    [pscustomobject]@{ Path = $application; Rights = $denyRights; Inheritance = $recursive; Type = $deny; Intent = 'deny application-tree mutation' },
    [pscustomobject]@{ Path = $application; Rights = $traverseRights; Inheritance = $recursive; Type = $allow; Intent = 'grant application-tree read and execute' },
    [pscustomobject]@{ Path = $data; Rights = $denyDataRights; Inheritance = $recursive; Type = $deny; Intent = 'deny service identity access to operator data' }
)

if (-not (Test-Path -LiteralPath $workspace -PathType Container) -and -not $WhatIfPreference) {
    if ($Verify -or $Inspect -or $Remove) {
        [Console]::Error.WriteLine("Workspace directory does not exist: $workspace")
        exit 1
    }
    if (Test-ConfirmationPromptExpected) { Assert-SafeConfirmationInput }
    if ($PSCmdlet.ShouldProcess($workspace, 'Create workspace directory')) { $null = New-Item -ItemType Directory -Path $workspace }
}
$targets += [pscustomobject]@{ Path = $workspace; Rights = $allowRights; Inheritance = $recursive; Type = $allow; Intent = 'grant workspace Modify' }

Write-Host 'Agent_b three-root ACL policy'
Write-Host "Identity: $env:COMPUTERNAME\$AccountName"
Write-Host "Application: $application"
Write-Host "Operator data: $data"
Write-Host "Service workspace: $workspace"
Write-Host 'The service identity can read/execute but not mutate the application tree, cannot access operator data, and can modify only the workspace.'

if (-not (Test-IsAdministrator) -and -not $WhatIfPreference -and -not $Verify -and -not $Inspect) {
    [Console]::Error.WriteLine('Administrator elevation is required to apply or remove ACLs.')
    Write-Summary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('use Agent_b Settings or reopen PowerShell as Administrator')
    exit 1
}

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no directory or ACL will be changed.'
    foreach ($target in $targets) { $null = $PSCmdlet.ShouldProcess($target.Path, $target.Intent) }
    Write-Summary -Changed @() -NotChanged @('directories', 'files', 'ACLs') -Next @('apply from Agent_b Settings, then verify')
    exit 0
}

$serviceSid = $null
try { $serviceSid = Resolve-ServiceIdentity -Name $AccountName } catch {
    if ($Inspect) {
        $status = [ordered]@{ supported = $true; account_exists = $false; applied = $false; drift = $targets.Count; summary = $_.Exception.Message }
        Write-Output ($statusMarker + ($status | ConvertTo-Json -Compress))
        exit 0
    }
    [Console]::Error.WriteLine("ACL policy failed: $($_.Exception.Message)")
    exit 1
}

if ($Verify -or $Inspect) {
    $drift = 0
    foreach ($target in $targets) {
        if (-not (Test-Path -LiteralPath $target.Path)) {
            if ($Verify) { Write-Host "DRIFT: missing path :: $($target.Path)" }
            $drift++
            continue
        }
        $acl = Get-Acl -LiteralPath $target.Path
        $present = (Get-ExactManagedRules -Acl $acl -Identity $serviceSid -Rights $target.Rights -Inheritance $target.Inheritance -Type $target.Type).Count -gt 0
        if (-not $present) { $drift++ }
        if ($Verify) { Write-Host "$(if ($present) { 'PASS' } else { 'DRIFT' }): $($target.Intent) :: $($target.Path)" }
    }
    if ($Inspect) {
        $status = [ordered]@{ supported = $true; account_exists = $true; applied = ($drift -eq 0); drift = $drift; summary = $(if ($drift -eq 0) { 'three-root ACL policy verified' } else { "$drift ACL drift item(s)" }) }
        Write-Output ($statusMarker + ($status | ConvertTo-Json -Compress))
        exit 0
    }
    Write-Summary -Changed @() -NotChanged @('ACLs') -Next @($(if ($drift -eq 0) { 'ACL verification complete' } else { 'apply again to repair drift' }))
    if ($drift -gt 0) { exit 1 }
    exit 0
}

if ($Remove) {
    foreach ($target in $targets) {
        if (Test-Path -LiteralPath $target.Path) { Remove-ManagedRule -Target $target -Identity $serviceSid }
    }
    Write-Summary -Changed @("$script:changed managed ACL rule(s) removed") -NotChanged @("$script:unchanged already absent") -Next @('disable the service identity before removing the account')
    exit 0
}

foreach ($target in $targets) {
    if (-not (Test-Path -LiteralPath $target.Path)) {
        [Console]::Error.WriteLine("Required path does not exist: $($target.Path)")
        exit 1
    }
    Set-ManagedRule -Target $target -Identity $serviceSid
}
Write-Summary -Changed @("$script:changed managed ACL rule(s) applied") -NotChanged @("$script:unchanged exact managed rule(s)") -Next @('verify from Settings', 'run the RBAC checks')
