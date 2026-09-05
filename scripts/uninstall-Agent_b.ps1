[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [switch]$Quiet,
    [switch]$PurgeData,
    [string]$ApplicationDirectory,
    [string]$DataDirectory,
    [string]$WorkspaceDirectory,
    [string]$StartMenuDirectory = (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'),
    [string]$UninstallRegistryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Agent_b',
    [string]$ExpectedOperatorSid,
    [string]$ExpectedOperatorLocalAppData,
    [switch]$TestMode
)

$ErrorActionPreference = 'Stop'

function Get-FullPath {
    param([string]$Path)
    return [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd('\')
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Quote-ProcessArgument {
    param([string]$Value)
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Assert-SafePath {
    param([string]$Path, [string[]]$AllowedLeaves, [string]$Purpose)
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full).TrimEnd('\')
    if ($full -eq $root -or (Split-Path -Leaf $full) -notin $AllowedLeaves) {
        throw "Refusing $Purpose outside a dedicated Agent_b path: $full"
    }
    return $full
}

function Assert-SafeRegistryPath {
    param([string]$Path)
    $normalized = $Path.Replace('/', '\')
    $requiredPrefix = 'HKCU:\Software\'
    $leaf = $normalized.Substring($normalized.LastIndexOf('\') + 1)
    if (-not $normalized.StartsWith($requiredPrefix, [StringComparison]::OrdinalIgnoreCase) -or
        -not $leaf.StartsWith('Agent_b', [StringComparison]::Ordinal)) {
        throw "UninstallRegistryPath must be a dedicated Agent_b key below $requiredPrefix $Path"
    }
}

function Assert-TestPath {
    param([string]$Path)
    if (-not $TestMode) { return }
    $full = Get-FullPath $Path
    $temp = Get-FullPath ([IO.Path]::GetTempPath())
    if (-not $full.StartsWith($temp + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "TestMode paths must stay beneath the temporary directory: $full"
    }
}

function Test-PathInside {
    param([string]$Child, [string]$Parent)
    return $Child.StartsWith($Parent.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)
}

function Assert-DisjointRoots {
    param([string[]]$Roots)
    for ($left = 0; $left -lt $Roots.Count; $left++) {
        for ($right = $left + 1; $right -lt $Roots.Count; $right++) {
            if ($Roots[$left].Equals($Roots[$right], [StringComparison]::OrdinalIgnoreCase) -or
                (Test-PathInside $Roots[$left] $Roots[$right]) -or
                (Test-PathInside $Roots[$right] $Roots[$left])) {
                throw 'Application, operator-data, and workspace directories must be three disjoint trees.'
            }
        }
    }
}

function Test-InstalledProcess {
    param([string]$Executable)
    foreach ($process in Get-Process -Name 'Agent_b' -ErrorAction SilentlyContinue) {
        try {
            if ((Get-FullPath $process.Path).Equals((Get-FullPath $Executable), [StringComparison]::OrdinalIgnoreCase)) { return $true }
        } catch { }
    }
    return $false
}

if ([string]::IsNullOrWhiteSpace($ApplicationDirectory)) { $ApplicationDirectory = Split-Path -Parent $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($ExpectedOperatorSid)) { $ExpectedOperatorSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value }
if ([string]::IsNullOrWhiteSpace($ExpectedOperatorLocalAppData)) { $ExpectedOperatorLocalAppData = [Environment]::GetFolderPath('LocalApplicationData') }
if ([string]::IsNullOrWhiteSpace($DataDirectory)) { $DataDirectory = Join-Path $ExpectedOperatorLocalAppData 'Agent_b' }
if ([string]::IsNullOrWhiteSpace($WorkspaceDirectory)) { $WorkspaceDirectory = Join-Path $env:ProgramData 'Agent_b\workspace' }

$applicationRoot = Assert-SafePath $ApplicationDirectory @('Agent_b') 'application removal'
$dataRoot = Assert-SafePath $DataDirectory @('Agent_b') 'operator-data removal'
$workspaceRoot = Assert-SafePath $WorkspaceDirectory @('workspace') 'workspace removal'
Assert-SafeRegistryPath $UninstallRegistryPath
Assert-TestPath $applicationRoot
Assert-TestPath $dataRoot
Assert-TestPath $workspaceRoot
Assert-DisjointRoots @($applicationRoot, $dataRoot, $workspaceRoot)

$launchingSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if (-not $launchingSid.Equals($ExpectedOperatorSid, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Uninstall refused: this install belongs to operator SID $ExpectedOperatorSid, but the launching identity is $launchingSid. No root was changed."
}
if (-not $TestMode) {
    $expectedApplicationRoot = Get-FullPath (Join-Path $env:ProgramFiles 'Agent_b')
    $expectedDataRoot = Get-FullPath (Join-Path $ExpectedOperatorLocalAppData 'Agent_b')
    $expectedWorkspaceRoot = Get-FullPath (Join-Path $env:ProgramData 'Agent_b\workspace')
    if (-not $applicationRoot.Equals($expectedApplicationRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Uninstall refused: application root is not $expectedApplicationRoot."
    }
    if (-not $dataRoot.Equals($expectedDataRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Uninstall refused: operator data is not the recorded LocalAppData root $expectedDataRoot."
    }
    if (-not $workspaceRoot.Equals($expectedWorkspaceRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Uninstall refused: workspace root is not $expectedWorkspaceRoot."
    }
}

if (-not (Test-IsAdministrator) -and -not $WhatIfPreference -and -not $TestMode) {
    $arguments = @(
        '-NoLogo', '-NoProfile', '-File', $PSCommandPath,
        '-ApplicationDirectory', $applicationRoot,
        '-DataDirectory', $dataRoot,
        '-WorkspaceDirectory', $workspaceRoot,
        '-StartMenuDirectory', $StartMenuDirectory,
        '-UninstallRegistryPath', $UninstallRegistryPath,
        '-ExpectedOperatorSid', $ExpectedOperatorSid,
        '-ExpectedOperatorLocalAppData', $ExpectedOperatorLocalAppData
    )
    if ($Quiet) { $arguments += '-Quiet' }
    if ($PurgeData) { $arguments += '-PurgeData' }
    $process = Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -ArgumentList (($arguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' ') -Verb RunAs -Wait -PassThru
    exit $process.ExitCode
}

$currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if (-not $currentSid.Equals($ExpectedOperatorSid, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Uninstall refused: elevation changed identity from $ExpectedOperatorSid to $currentSid. Over-the-shoulder administrator credentials cannot remove another operator's data."
}

$installedBinary = Join-Path $applicationRoot 'Agent_b.exe'
$shortcutPath = Join-Path $StartMenuDirectory 'Agent_b.lnk'
$purge = $PurgeData.IsPresent
Write-Host 'Agent_b uninstall'
Write-Host "Application: $applicationRoot"
Write-Host "Operator data: $dataRoot"
Write-Host "Service workspace: $workspaceRoot"

if (Test-InstalledProcess $installedBinary) {
    throw 'Agent_b is running. Close it, then uninstall again.'
}

if (-not $Quiet -and -not $WhatIfPreference -and -not $purge) {
    Add-Type -AssemblyName System.Windows.Forms
    $choice = [Windows.Forms.MessageBox]::Show(
        "Remove operator configuration, credential, logs, memory, and the service workspace too?`n`nChoose No to keep them for a later reinstall.",
        'Uninstall Agent_b',
        [Windows.Forms.MessageBoxButtons]::YesNoCancel,
        [Windows.Forms.MessageBoxIcon]::Question,
        [Windows.Forms.MessageBoxDefaultButton]::Button2
    )
    if ($choice -eq [Windows.Forms.DialogResult]::Cancel) { Write-Host 'Uninstall canceled.'; exit 2 }
    $purge = $choice -eq [Windows.Forms.DialogResult]::Yes
}

if ($purge -and (Test-Path -LiteralPath $dataRoot -PathType Container)) {
    $owner = (Get-Acl -LiteralPath $dataRoot).GetOwner([Security.Principal.SecurityIdentifier]).Value
    if (-not $owner.Equals($ExpectedOperatorSid, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Purge refused: operator data owner is $owner, expected $ExpectedOperatorSid. No root was changed."
    }
}

if ($WhatIfPreference) {
    Write-Host "Mode: WhatIf; application, shortcut, registration, and data will not be changed. PurgeData=$purge"
    $null = $PSCmdlet.ShouldProcess($applicationRoot, 'Remove Agent_b program files')
    if ($purge) {
        $null = $PSCmdlet.ShouldProcess($dataRoot, 'Remove operator data')
        $null = $PSCmdlet.ShouldProcess($workspaceRoot, 'Remove service workspace')
    }
    exit 0
}

if (Test-Path -LiteralPath $shortcutPath) { Remove-Item -LiteralPath $shortcutPath -Force }
if (Test-Path -LiteralPath $UninstallRegistryPath) { Remove-Item -LiteralPath $UninstallRegistryPath -Recurse -Force }
Set-Location ([IO.Path]::GetTempPath())
if (Test-Path -LiteralPath $applicationRoot) { Remove-Item -LiteralPath $applicationRoot -Recurse -Force }

if ($purge) {
    if (Test-Path -LiteralPath $dataRoot) { Remove-Item -LiteralPath $dataRoot -Recurse -Force }
    if (Test-Path -LiteralPath $workspaceRoot) { Remove-Item -LiteralPath $workspaceRoot -Recurse -Force }
    $workspaceParent = Split-Path -Parent $workspaceRoot
    if ((Split-Path -Leaf $workspaceParent) -eq 'Agent_b' -and (Test-Path -LiteralPath $workspaceParent) -and -not (Get-ChildItem -LiteralPath $workspaceParent -Force | Select-Object -First 1)) {
        Remove-Item -LiteralPath $workspaceParent -Force
    }
    Write-Host 'REMOVED: program files, operator data, and service workspace'
} else {
    Write-Host 'PRESERVED: operator configuration, credential, logs, memory, and service workspace'
}

Write-Host 'REMOVED: operator Start Menu shortcut and HKCU Installed apps registration'
Write-Host 'UNCHANGED: Windows service account, managed ACLs outside removed trees, and firewall policy; remove Host protections first when no longer needed.'
Write-Host 'UNINSTALL COMPLETE'
