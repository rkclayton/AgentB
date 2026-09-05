[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$SourceDirectory,
    [string]$ApplicationDirectory = (Join-Path $env:ProgramFiles 'Agent_b'),
    [string]$DataDirectory,
    [string]$WorkspaceDirectory = (Join-Path $env:ProgramData 'Agent_b\workspace'),
    [string]$StartMenuDirectory = (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'),
    [string]$UninstallRegistryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Agent_b',
    [string]$OperatorSid,
    [string]$OperatorLocalAppData,
    [switch]$TestMode
)

$ErrorActionPreference = 'Stop'
$displayVersion = '0.1.0'

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

function Assert-SafeAgentBPath {
    param([string]$Path, [string]$Purpose)
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full).TrimEnd('\')
    if ($full -eq $root -or (Split-Path -Leaf $full) -notin @('Agent_b', 'workspace')) {
        throw "$Purpose must name a dedicated Agent_b or workspace directory: $full"
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

function Find-Go {
    param([string]$Source)
    $local = Join-Path $Source '.tools\go\bin\go.exe'
    if (Test-Path -LiteralPath $local -PathType Leaf) { return $local }
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    return $null
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

function Copy-ProgramDirectory {
    param([string]$Name, [string]$Source, [string]$Destination)
    $from = Join-Path $Source $Name
    $to = Join-Path $Destination $Name
    if (-not (Test-Path -LiteralPath $from -PathType Container)) { throw "Required program directory is missing: $from" }
    if (-not (Test-Path -LiteralPath $to -PathType Container)) {
        $null = New-Item -ItemType Directory -Path $to -Force
    } else {
        foreach ($item in Get-ChildItem -LiteralPath $to -Force) {
            Remove-Item -LiteralPath $item.FullName -Recurse -Force
        }
    }
    foreach ($item in Get-ChildItem -LiteralPath $from -Force) {
        Copy-Item -LiteralPath $item.FullName -Destination $to -Recurse -Force
    }
}

function Set-PrivateDirectoryAcl {
    param([string]$Path, [Security.Principal.SecurityIdentifier]$Owner)
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagate = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    foreach ($sid in @(
        $Owner,
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::LocalSystemSid, $null),
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null)
    )) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $acl.SetOwner($Owner)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Set-ApplicationDirectoryAcl {
    param([string]$Path, [Security.Principal.SecurityIdentifier]$Owner)
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagate = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    foreach ($sid in @(
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::LocalSystemSid, $null),
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null)
    )) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $users = [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinUsersSid, $null)
    $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($users, [Security.AccessControl.FileSystemRights]::ReadAndExecute, $inherit, $propagate, $allow))
    if ($TestMode) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($Owner, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null))
    Set-Acl -LiteralPath $Path -AclObject $acl
}

if ($env:OS -ne 'Windows_NT') { throw 'Agent_b installation is supported only on Windows.' }
if ([string]::IsNullOrWhiteSpace($SourceDirectory)) { $SourceDirectory = Split-Path -Parent $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($OperatorSid)) { $OperatorSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value }
if ([string]::IsNullOrWhiteSpace($OperatorLocalAppData)) { $OperatorLocalAppData = [Environment]::GetFolderPath('LocalApplicationData') }
if ([string]::IsNullOrWhiteSpace($DataDirectory)) { $DataDirectory = Join-Path $OperatorLocalAppData 'Agent_b' }

$sourceRoot = Get-FullPath $SourceDirectory
$applicationRoot = Assert-SafeAgentBPath $ApplicationDirectory 'ApplicationDirectory'
$dataRoot = Assert-SafeAgentBPath $DataDirectory 'DataDirectory'
$workspaceRoot = Assert-SafeAgentBPath $WorkspaceDirectory 'WorkspaceDirectory'
Assert-SafeRegistryPath $UninstallRegistryPath
Assert-TestPath $applicationRoot
Assert-TestPath $dataRoot
Assert-TestPath $workspaceRoot
Assert-DisjointRoots @($applicationRoot, $dataRoot, $workspaceRoot)
if ($sourceRoot.Equals($applicationRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'SourceDirectory and ApplicationDirectory must be different.' }
if (-not $TestMode) {
    $expectedApplicationRoot = Get-FullPath (Join-Path $env:ProgramFiles 'Agent_b')
    $expectedDataRoot = Get-FullPath (Join-Path $OperatorLocalAppData 'Agent_b')
    $expectedWorkspaceRoot = Get-FullPath (Join-Path $env:ProgramData 'Agent_b\workspace')
    if (-not $applicationRoot.Equals($expectedApplicationRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "ApplicationDirectory must be the admin-protected Program Files location: $expectedApplicationRoot"
    }
    if (-not $dataRoot.Equals($expectedDataRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "DataDirectory must be the launching operator's LocalAppData Agent_b directory: $expectedDataRoot"
    }
    if (-not $workspaceRoot.Equals($expectedWorkspaceRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "WorkspaceDirectory must be the machine-scoped ProgramData location: $expectedWorkspaceRoot"
    }
}

if (-not (Test-IsAdministrator) -and -not $WhatIfPreference -and -not $TestMode) {
    $arguments = @(
        '-NoLogo', '-NoProfile', '-File', $PSCommandPath,
        '-SourceDirectory', $sourceRoot,
        '-ApplicationDirectory', $applicationRoot,
        '-DataDirectory', $dataRoot,
        '-WorkspaceDirectory', $workspaceRoot,
        '-StartMenuDirectory', $StartMenuDirectory,
        '-UninstallRegistryPath', $UninstallRegistryPath,
        '-OperatorSid', $OperatorSid,
        '-OperatorLocalAppData', $OperatorLocalAppData
    )
    $process = Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -ArgumentList (($arguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' ') -Verb RunAs -Wait -PassThru
    exit $process.ExitCode
}

$currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
if (-not $currentSid.Value.Equals($OperatorSid, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Installation refused: elevation changed identity from $OperatorSid to $($currentSid.Value). Use same-user UAC; over-the-shoulder administrator credentials would select the wrong LocalAppData and DPAPI owner."
}

$sourceBinary = Join-Path $sourceRoot 'Agent_b.exe'
$installedBinary = Join-Path $applicationRoot 'Agent_b.exe'
Write-Host 'Agent_b admin-protected program installation with per-operator registration and data'
Write-Host "Application: $applicationRoot"
Write-Host "Operator data: $dataRoot"
Write-Host "Service workspace: $workspaceRoot"
Write-Host "Operator SID: $OperatorSid"

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no build, file, ACL, shortcut, or registry change will be made.'
    $null = $PSCmdlet.ShouldProcess($sourceBinary, 'Build Agent_b')
    $null = $PSCmdlet.ShouldProcess($applicationRoot, 'Install or upgrade admin-only program files')
    $null = $PSCmdlet.ShouldProcess($dataRoot, 'Create or preserve private operator data')
    $null = $PSCmdlet.ShouldProcess($workspaceRoot, 'Create or preserve service workspace')
    exit 0
}

if (Test-InstalledProcess $installedBinary) {
    throw 'Agent_b is running from the application directory. Close it, then run the installer again.'
}

$go = Find-Go $sourceRoot
if ($go) {
    Write-Host "BUILD: $go"
    & $go build -o $sourceBinary ./cmd/harness
    if ($LASTEXITCODE -ne 0) { throw "Agent_b build failed with exit code $LASTEXITCODE." }
} elseif (-not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
    throw 'Go 1.24 or newer was not found and Agent_b.exe has not already been built.'
} else {
    Write-Host 'BUILD: Go was not found; using the existing Agent_b.exe.'
}

$applicationCreated = -not (Test-Path -LiteralPath $applicationRoot -PathType Container)
$dataCreated = -not (Test-Path -LiteralPath $dataRoot -PathType Container)
$workspaceCreated = -not (Test-Path -LiteralPath $workspaceRoot -PathType Container)
$null = New-Item -ItemType Directory -Path $applicationRoot -Force
if ($applicationCreated) { Set-ApplicationDirectoryAcl -Path $applicationRoot -Owner $currentSid }
foreach ($directory in @('web', 'prompts', 'scripts', 'docs')) {
    Copy-ProgramDirectory -Name $directory -Source $sourceRoot -Destination $applicationRoot
}
foreach ($file in @('Agent_b.exe', 'harness.example.json', 'SECURITY.md', 'LICENSE')) {
    $from = Join-Path $sourceRoot $file
    if (-not (Test-Path -LiteralPath $from -PathType Leaf)) { throw "Required program file is missing: $from" }
    Copy-Item -LiteralPath $from -Destination (Join-Path $applicationRoot $file) -Force
}
Copy-Item -LiteralPath (Join-Path $sourceRoot 'scripts\launch-installed.cmd') -Destination (Join-Path $applicationRoot 'Agent_b.cmd') -Force

$null = New-Item -ItemType Directory -Path $dataRoot -Force
if ($dataCreated) { Set-PrivateDirectoryAcl -Path $dataRoot -Owner $currentSid }
foreach ($directory in @('logs', 'memory')) { $null = New-Item -ItemType Directory -Path (Join-Path $dataRoot $directory) -Force }
$null = New-Item -ItemType Directory -Path $workspaceRoot -Force
if ($workspaceCreated) { Set-PrivateDirectoryAcl -Path $workspaceRoot -Owner $currentSid }

$configPath = Join-Path $dataRoot 'harness.json'
if (Test-Path -LiteralPath $configPath -PathType Leaf) {
    Write-Host 'PRESERVED: existing operator configuration'
} else {
    $templatePath = Join-Path $applicationRoot 'harness.example.json'
    $config = Get-Content -Raw -LiteralPath $templatePath | ConvertFrom-Json
    $config.workspace = $workspaceRoot
    $config.log_dir = Join-Path $dataRoot 'logs'
    $config.memory.dir = Join-Path $dataRoot 'memory'
    [IO.File]::WriteAllText($configPath, ($config | ConvertTo-Json -Depth 100) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    Write-Host 'CREATED: operator configuration from the installed template'
}

$iconPath = Join-Path $applicationRoot 'web\assets\Agent_b.ico'
if (-not (Test-Path -LiteralPath $iconPath -PathType Leaf)) { throw "Installed icon is missing: $iconPath" }
$null = New-Item -ItemType Directory -Path $StartMenuDirectory -Force
$shortcutPath = Join-Path $StartMenuDirectory 'Agent_b.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = Join-Path $applicationRoot 'Agent_b.cmd'
$shortcut.WorkingDirectory = $dataRoot
$shortcut.IconLocation = "$iconPath,0"
$shortcut.Description = 'Open Agent_b'
$shortcut.Save()

$uninstallScript = Join-Path $applicationRoot 'scripts\uninstall-Agent_b.ps1'
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$uninstallArguments = @(
    '-NoLogo', '-NoProfile', '-File', $uninstallScript,
    '-ApplicationDirectory', $applicationRoot,
    '-DataDirectory', $dataRoot,
    '-WorkspaceDirectory', $workspaceRoot,
    '-StartMenuDirectory', $StartMenuDirectory,
    '-UninstallRegistryPath', $UninstallRegistryPath,
    '-ExpectedOperatorSid', $OperatorSid,
    '-ExpectedOperatorLocalAppData', $OperatorLocalAppData
)
if ($TestMode) { $uninstallArguments += '-TestMode' }
$uninstallCommand = (Quote-ProcessArgument $powershell) + ' ' + (($uninstallArguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' ')
$null = New-Item -Path $UninstallRegistryPath -Force
$properties = [ordered]@{
    DisplayName = 'Agent_b'
    DisplayVersion = $displayVersion
    Publisher = 'rkclayton'
    DisplayIcon = $iconPath
    InstallLocation = $applicationRoot
    UninstallString = $uninstallCommand
    QuietUninstallString = "$uninstallCommand -Quiet"
    URLInfoAbout = 'https://github.com/rkclayton/AgentB'
    InstallDate = (Get-Date -Format 'yyyyMMdd')
    OperatorSid = $OperatorSid
    DataLocation = $dataRoot
    WorkspaceLocation = $workspaceRoot
}
foreach ($entry in $properties.GetEnumerator()) {
    $null = New-ItemProperty -Path $UninstallRegistryPath -Name $entry.Key -Value $entry.Value -PropertyType String -Force
}
$estimatedKB = [int][Math]::Ceiling(((Get-ChildItem -LiteralPath $applicationRoot -File -Recurse | Measure-Object Length -Sum).Sum) / 1KB)
$null = New-ItemProperty -Path $UninstallRegistryPath -Name EstimatedSize -Value $estimatedKB -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoModify -Value 1 -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoRepair -Value 1 -PropertyType DWord -Force

Write-Host ''
Write-Host 'INSTALLATION COMPLETE'
Write-Host "Start Menu: $shortcutPath"
Write-Host 'Registration: HKCU and the operator Start Menu, matching the LocalAppData configuration and user-scoped DPAPI owner.'
Write-Host 'Settings: created once in LocalAppData and preserved on upgrades'
Write-Host 'Next: open Agent_b from Start, then apply and verify Host protections in Settings > Security.'
