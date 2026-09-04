[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('Agent_b-installer-test-' + [Guid]::NewGuid().ToString('N'))
$testInstall = Join-Path $testRoot 'Agent_b'
$testStart = Join-Path $testRoot 'StartMenu'
$testRegistry = 'HKCU:\Software\Agent_b-Installer-Test-' + [Guid]::NewGuid().ToString('N')
$installer = Join-Path $PSScriptRoot 'install-Agent_b.ps1'
$uninstaller = Join-Path $PSScriptRoot 'uninstall-Agent_b.ps1'

function Assert-TemporaryTestPath {
    param([string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $full.StartsWith($temp, [StringComparison]::OrdinalIgnoreCase) -or
        -not (Split-Path -Leaf $full).StartsWith('Agent_b-installer-test-', [StringComparison]::Ordinal)) {
        throw "Refusing to clean unexpected test path: $full"
    }
}

try {
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry
    if ($LASTEXITCODE -ne 0) { throw "First install exited $LASTEXITCODE." }
    foreach ($path in @(
        (Join-Path $testInstall 'Agent_b.exe'),
        (Join-Path $testInstall 'Agent_b.cmd'),
        (Join-Path $testInstall 'web\assets\Agent_b.ico'),
        (Join-Path $testStart 'Agent_b.lnk')
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing installed file: $path" }
    }

    $shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path $testStart 'Agent_b.lnk'))
    if (-not $shortcut.TargetPath.Equals((Join-Path $testInstall 'Agent_b.cmd'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Shortcut target does not point to Agent_b.cmd.'
    }
    if (-not $shortcut.IconLocation.StartsWith((Join-Path $testInstall 'web\assets\Agent_b.ico'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Shortcut does not use the Agent_b icon.'
    }

    $registration = Get-ItemProperty -LiteralPath $testRegistry
    if ($registration.DisplayName -ne 'Agent_b' -or
        -not ([string]$registration.InstallLocation).Equals($testInstall, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Installed apps registration is incorrect.'
    }

    $configPath = Join-Path $testInstall 'harness.json'
    $configHash = (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash
    $credentialPath = Join-Path $testInstall '.agentb-shell-credential.dpapi'
    $credentialHash = if (Test-Path -LiteralPath $credentialPath) {
        (Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash
    } else { '' }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry
    if ($LASTEXITCODE -ne 0) { throw "Upgrade exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Upgrade changed the installed connection configuration.'
    }
    if ($credentialHash -and (Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash -ne $credentialHash) {
        throw 'Upgrade changed the installed credential.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $uninstaller -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -Quiet
    if ($LASTEXITCODE -ne 0) { throw "Preserving uninstall exited $LASTEXITCODE." }
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf) -or
        ($credentialHash -and -not (Test-Path -LiteralPath $credentialPath -PathType Leaf)) -or
        (Test-Path -LiteralPath (Join-Path $testInstall 'Agent_b.exe')) -or
        (Test-Path -LiteralPath (Join-Path $testStart 'Agent_b.lnk')) -or
        (Test-Path -LiteralPath $testRegistry)) {
        throw 'Preserving uninstall did not keep only local data.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry
    if ($LASTEXITCODE -ne 0) { throw "Reinstall exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Reinstall changed preserved connection configuration.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $uninstaller -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -Quiet -PurgeData
    if ($LASTEXITCODE -ne 0) { throw "Purging uninstall exited $LASTEXITCODE." }
    if ((Test-Path -LiteralPath $testInstall) -or
        (Test-Path -LiteralPath (Join-Path $testStart 'Agent_b.lnk')) -or
        (Test-Path -LiteralPath $testRegistry)) {
        throw 'Uninstall left a program, shortcut, or registration artifact.'
    }
    Write-Host 'PASS: isolated install, branded shortcut, registration, upgrade preservation, preserving uninstall, reinstall, and purging uninstall'
} finally {
    if (Test-Path -LiteralPath $testRegistry) { Remove-Item -LiteralPath $testRegistry -Recurse -Force }
    if (Test-Path -LiteralPath $testRoot) {
        Assert-TemporaryTestPath $testRoot
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
