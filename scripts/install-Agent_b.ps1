[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$SourceDirectory,
    [string]$InstallDirectory = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\Agent_b'),
    [string]$StartMenuDirectory = (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'),
    [string]$UninstallRegistryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Agent_b'
)

$ErrorActionPreference = 'Stop'
$displayVersion = '0.1.0'
if ([string]::IsNullOrWhiteSpace($SourceDirectory)) {
    $SourceDirectory = Split-Path -Parent $PSScriptRoot
}

function Get-FullPath {
    param([string]$Path)
    return [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd('\')
}

function Assert-SafeAgentBPath {
    param([string]$Path, [string]$Purpose)
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full).TrimEnd('\')
    if ($full -eq $root -or (Split-Path -Leaf $full) -ne 'Agent_b') {
        throw "$Purpose must name a dedicated directory whose final component is Agent_b: $full"
    }
    return $full
}

function Assert-SafeRegistryPath {
    param([string]$Path)
    $normalized = $Path.Replace('/', '\')
    $leaf = $normalized.Substring($normalized.LastIndexOf('\') + 1)
    if (-not $normalized.StartsWith('HKCU:\Software\', [StringComparison]::OrdinalIgnoreCase) -or
        -not $leaf.StartsWith('Agent_b', [StringComparison]::Ordinal)) {
        throw "UninstallRegistryPath must be a dedicated Agent_b key below HKCU:\Software: $Path"
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
        # Keep the managed top-level directory itself: replacing it discards the
        # service-account deny ACE that protects the installed control surface.
        foreach ($item in Get-ChildItem -LiteralPath $to -Force) {
            Remove-Item -LiteralPath $item.FullName -Recurse -Force
        }
    }
    foreach ($item in Get-ChildItem -LiteralPath $from -Force) {
        Copy-Item -LiteralPath $item.FullName -Destination $to -Recurse -Force
    }
}

function Import-FirstInstallSettings {
    param([string]$Source, [string]$Destination)
    $destinationConfig = Join-Path $Destination 'harness.json'
    if (Test-Path -LiteralPath $destinationConfig) {
        Write-Host 'PRESERVED: existing installed connection settings'
        return
    }
    $sourceConfig = Join-Path $Source 'harness.json'
    if (-not (Test-Path -LiteralPath $sourceConfig -PathType Leaf)) {
        $sourceConfig = Join-Path $Source 'harness.example.json'
        Write-Host 'DEFAULT: no local connection settings were available to import'
    } else {
        Write-Host 'IMPORTED: local connection settings (secret values were not displayed)'
    }
    $config = Get-Content -Raw -LiteralPath $sourceConfig | ConvertFrom-Json
    $sourceWorkspace = Get-FullPath (Join-Path $Source 'workspace')
    if ([string]::IsNullOrWhiteSpace([string]$config.workspace) -or
        (Get-FullPath ([string]$config.workspace)).Equals($sourceWorkspace, [StringComparison]::OrdinalIgnoreCase)) {
        $config.workspace = Join-Path $Destination 'workspace'
    }
    $config.log_dir = Join-Path $Destination 'logs'
    $config.memory.dir = Join-Path $Destination 'memory'
    $config | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $destinationConfig -Encoding UTF8

    $sourceCredential = Join-Path $Source '.agentb-shell-credential.dpapi'
    $destinationCredential = Join-Path $Destination '.agentb-shell-credential.dpapi'
    if ((Test-Path -LiteralPath $sourceCredential -PathType Leaf) -and -not (Test-Path -LiteralPath $destinationCredential)) {
        Copy-Item -LiteralPath $sourceCredential -Destination $destinationCredential
        Write-Host 'IMPORTED: user-scoped service credential'
    }
}

if ($env:OS -ne 'Windows_NT') { throw 'Agent_b installation is supported only on Windows.' }
$sourceRoot = Get-FullPath $SourceDirectory
$installRoot = Assert-SafeAgentBPath $InstallDirectory 'InstallDirectory'
Assert-SafeRegistryPath $UninstallRegistryPath
if ($sourceRoot.Equals($installRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'SourceDirectory and InstallDirectory must be different.' }
$sourceBinary = Join-Path $sourceRoot 'Agent_b.exe'
$installedBinary = Join-Path $installRoot 'Agent_b.exe'

Write-Host 'Agent_b per-user installation'
Write-Host "Source: $sourceRoot"
Write-Host "Install: $installRoot"
Write-Host 'Elevation: not required'

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no build, file, shortcut, or registry change will be made.'
    $null = $PSCmdlet.ShouldProcess($sourceBinary, 'Build Agent_b')
    $null = $PSCmdlet.ShouldProcess($installRoot, 'Install or upgrade Agent_b while preserving installed settings')
    $null = $PSCmdlet.ShouldProcess((Join-Path $StartMenuDirectory 'Agent_b.lnk'), 'Create Start Menu shortcut')
    $null = $PSCmdlet.ShouldProcess($UninstallRegistryPath, 'Register Agent_b in Installed apps')
    exit 0
}

if (Test-InstalledProcess $installedBinary) {
    throw 'Agent_b is running from the install directory. Close its console window, then run the installer again.'
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

$null = New-Item -ItemType Directory -Path $installRoot -Force
foreach ($directory in @('web', 'prompts', 'scripts', 'docs')) {
    Copy-ProgramDirectory -Name $directory -Source $sourceRoot -Destination $installRoot
}
foreach ($file in @('Agent_b.exe', 'harness.example.json', 'SECURITY.md', 'LICENSE')) {
    $from = Join-Path $sourceRoot $file
    if (-not (Test-Path -LiteralPath $from -PathType Leaf)) { throw "Required program file is missing: $from" }
    Copy-Item -LiteralPath $from -Destination (Join-Path $installRoot $file) -Force
}
Copy-Item -LiteralPath (Join-Path $sourceRoot 'scripts\launch-installed.cmd') -Destination (Join-Path $installRoot 'Agent_b.cmd') -Force
foreach ($directory in @('logs', 'memory', 'workspace')) {
    $null = New-Item -ItemType Directory -Path (Join-Path $installRoot $directory) -Force
}
Import-FirstInstallSettings -Source $sourceRoot -Destination $installRoot

$iconPath = Join-Path $installRoot 'web\assets\Agent_b.ico'
if (-not (Test-Path -LiteralPath $iconPath -PathType Leaf)) { throw "Installed icon is missing: $iconPath" }
$null = New-Item -ItemType Directory -Path $StartMenuDirectory -Force
$shortcutPath = Join-Path $StartMenuDirectory 'Agent_b.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = Join-Path $installRoot 'Agent_b.cmd'
$shortcut.WorkingDirectory = $installRoot
$shortcut.IconLocation = "$iconPath,0"
$shortcut.Description = 'Open Agent_b'
$shortcut.Save()

$uninstallScript = Join-Path $installRoot 'scripts\uninstall-Agent_b.ps1'
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$uninstallCommand = '"{0}" -NoLogo -NoProfile -ExecutionPolicy Bypass -File "{1}"' -f $powershell, $uninstallScript
$null = New-Item -Path $UninstallRegistryPath -Force
$properties = [ordered]@{
    DisplayName = 'Agent_b'
    DisplayVersion = $displayVersion
    Publisher = 'rkclayton'
    DisplayIcon = $iconPath
    InstallLocation = $installRoot
    UninstallString = $uninstallCommand
    QuietUninstallString = "$uninstallCommand -Quiet"
    URLInfoAbout = 'https://github.com/rkclayton/AgentB'
    InstallDate = (Get-Date -Format 'yyyyMMdd')
}
foreach ($entry in $properties.GetEnumerator()) {
    $null = New-ItemProperty -Path $UninstallRegistryPath -Name $entry.Key -Value $entry.Value -PropertyType String -Force
}
$estimatedKB = [int][Math]::Ceiling(((Get-ChildItem -LiteralPath $installRoot -File -Recurse | Measure-Object Length -Sum).Sum) / 1KB)
$null = New-ItemProperty -Path $UninstallRegistryPath -Name EstimatedSize -Value $estimatedKB -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoModify -Value 1 -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoRepair -Value 1 -PropertyType DWord -Force

Write-Host ''
Write-Host 'INSTALLATION COMPLETE'
Write-Host "Start Menu: $shortcutPath"
Write-Host 'Installed apps: Agent_b'
Write-Host 'Settings: imported on first install; preserved on upgrades'
Write-Host 'Next: open Agent_b from Start, then verify Host protections in Settings > Security for this install location.'
