[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [switch]$Quiet,
    [switch]$PurgeData,
    [string]$InstallDirectory,
    [string]$StartMenuDirectory = (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'),
    [string]$UninstallRegistryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Agent_b'
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($InstallDirectory)) {
    $InstallDirectory = Split-Path -Parent $PSScriptRoot
}

function Get-FullPath {
    param([string]$Path)
    return [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd('\')
}

function Assert-SafeAgentBPath {
    param([string]$Path)
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full).TrimEnd('\')
    if ($full -eq $root -or (Split-Path -Leaf $full) -ne 'Agent_b') {
        throw "Refusing to uninstall from a directory not dedicated to Agent_b: $full"
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

function Test-InstalledProcess {
    param([string]$Executable)
    foreach ($process in Get-Process -Name 'Agent_b' -ErrorAction SilentlyContinue) {
        try {
            if ((Get-FullPath $process.Path).Equals((Get-FullPath $Executable), [StringComparison]::OrdinalIgnoreCase)) { return $true }
        } catch { }
    }
    return $false
}

$installRoot = Assert-SafeAgentBPath $InstallDirectory
Assert-SafeRegistryPath $UninstallRegistryPath
$installedBinary = Join-Path $installRoot 'Agent_b.exe'
$shortcutPath = Join-Path $StartMenuDirectory 'Agent_b.lnk'
$purge = $PurgeData.IsPresent

Write-Host 'Agent_b uninstall'
Write-Host "Install: $installRoot"

if (Test-InstalledProcess $installedBinary) {
    throw 'Agent_b is running. Close its console window, then uninstall again from Installed apps.'
}

if (-not $Quiet -and -not $WhatIfPreference -and -not $purge) {
    Add-Type -AssemblyName System.Windows.Forms
    $choice = [Windows.Forms.MessageBox]::Show(
        "Remove saved connection settings, credential, logs, memory, and workspace too?`n`nChoose No to keep them for a later reinstall.",
        'Uninstall Agent_b',
        [Windows.Forms.MessageBoxButtons]::YesNoCancel,
        [Windows.Forms.MessageBoxIcon]::Question,
        [Windows.Forms.MessageBoxDefaultButton]::Button2
    )
    if ($choice -eq [Windows.Forms.DialogResult]::Cancel) { Write-Host 'Uninstall canceled.'; exit 2 }
    $purge = $choice -eq [Windows.Forms.DialogResult]::Yes
}

if ($WhatIfPreference) {
    Write-Host "Mode: WhatIf; program files, shortcut, registry, and data will not be changed. PurgeData=$purge"
    $null = $PSCmdlet.ShouldProcess($shortcutPath, 'Remove Start Menu shortcut')
    $null = $PSCmdlet.ShouldProcess($UninstallRegistryPath, 'Remove Installed apps registration')
    $null = $PSCmdlet.ShouldProcess($installRoot, $(if ($purge) { 'Remove Agent_b and all local data' } else { 'Remove Agent_b program files and preserve local data' }))
    exit 0
}

if (Test-Path -LiteralPath $shortcutPath) { Remove-Item -LiteralPath $shortcutPath -Force }

if ($purge) {
    Set-Location ([IO.Path]::GetTempPath())
    if (Test-Path -LiteralPath $installRoot) { Remove-Item -LiteralPath $installRoot -Recurse -Force }
    Write-Host 'REMOVED: program files and local data'
} else {
    foreach ($directory in @('web', 'prompts', 'scripts', 'docs')) {
        $path = Join-Path $installRoot $directory
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force }
    }
    foreach ($file in @('Agent_b.exe', 'Agent_b.cmd', 'harness.example.json', 'SECURITY.md', 'LICENSE')) {
        $path = Join-Path $installRoot $file
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force }
    }
    Write-Host 'PRESERVED: connection settings, credential, logs, memory, and workspace'
}

if (Test-Path -LiteralPath $UninstallRegistryPath) { Remove-Item -LiteralPath $UninstallRegistryPath -Recurse -Force }
Write-Host 'REMOVED: Start Menu shortcut and Installed apps registration'
Write-Host 'UNCHANGED: Windows service account, ACL policy, and firewall policy; these may be shared and must be removed from Settings > Security before uninstall when no longer needed.'
Write-Host 'UNINSTALL COMPLETE'
