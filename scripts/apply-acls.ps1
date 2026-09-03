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

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
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
        return
    }
    if ($PSCmdlet.ShouldProcess($Path, $Intent)) {
        $rule = New-ManagedRule -Identity $Identity -Rights $Rights -Inheritance $Inheritance -Type $Type
        $null = $acl.AddAccessRule($rule)
        Set-Acl -LiteralPath $Path -AclObject $acl
        Write-Host "APPLIED: $Intent :: $Path"
    }
}

if (-not (Test-IsAdministrator)) {
    [Console]::Error.WriteLine('Administrator elevation is required. Reopen PowerShell as Administrator and run this script again.')
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
Write-Warning "The harness and its shell run as the same identity. ACLs cannot deny shell writes to '$logs' while preserving harness append access; no logs deny ACE will be applied."
Write-Warning "The remember tool writes under '$memory'. No separate grant is added because the requested writable boundary is the workspace; disable memory or place its directory inside the writable workspace."
Write-Warning 'Denying harness.json writes also prevents startup full-probe saves and settings changes under the service identity. Pre-provision a runnable config and use probe_mode off before applying this policy.'

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
    Write-Host 'Verification after application: rerun this script with -Verify.'
    exit 0
}

$serviceSid = Resolve-ServiceIdentity -Name $AccountName

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
    Write-Host 'LIMITATION: logs separation is not verifiable by ACL because harness and shell share one identity; logs write access is intentionally preserved.'
    if ($drift -gt 0) {
        Write-Error "ACL verification found $drift drift item(s)." -ErrorAction Continue
        exit 1
    }
    Write-Host 'ACL verification passed for every enforceable intent.'
    exit 0
}

if (-not (Test-Path -LiteralPath $root -PathType Container)) {
    Write-Error "Harness directory does not exist: $root" -ErrorAction Continue
    exit 1
}

Set-ManagedRule -Path $root -Identity $serviceSid -Rights $denyRights -Inheritance $none -Type $deny -Intent 'Add explicit object-only deny ACE to the harness binary directory'
foreach ($path in $recursiveControlDirectories) {
    if (-not (Test-Path -LiteralPath $path -PathType Container)) {
        Write-Error "Required control directory does not exist: $path" -ErrorAction Continue
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
    if ($PSCmdlet.ShouldProcess($workspace, 'Create the workspace directory')) {
        $null = New-Item -ItemType Directory -Path $workspace
    }
}
Set-ManagedRule -Path $workspace -Identity $serviceSid -Rights $allowRights -Inheritance $recursive -Type $allow -Intent 'Add explicit inheritable Modify allow ACE for workspace files'

Write-Host 'ACL application complete. Run this script again with -Verify to detect drift.'
Write-Host 'No logs deny ACE was applied because shell and harness share the same Windows identity.'
