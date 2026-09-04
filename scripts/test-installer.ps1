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
        (Join-Path $testInstall 'scripts\launch-Agent_b.ps1'),
        (Join-Path $testInstall 'web\assets\Agent_b.ico'),
        (Join-Path $testStart 'Agent_b.lnk')
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing installed file: $path" }
    }
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File (Join-Path $testInstall 'scripts\launch-Agent_b.ps1') -Check
    if ($LASTEXITCODE -ne 0) { throw "Installed launcher check exited $LASTEXITCODE." }

    $launcherSource = Get-Content -Raw -LiteralPath (Join-Path $testInstall 'scripts\launch-Agent_b.ps1')
    if ($launcherSource -notmatch "'chat'" -or $launcherSource -notmatch 'Show-AgentBWindow -Url \$appUrl -ReplaceExisting') {
        throw 'Installed launcher is not configured to open the Chat-first application view.'
    }
    $indexSource = Get-Content -Raw -LiteralPath (Join-Path $testInstall 'web\index.html')
    $chatSource = Get-Content -Raw -LiteralPath (Join-Path $testInstall 'web\chat.html')
    if ($indexSource -match 'target=' -or $chatSource -match 'target=') {
        throw 'Installed application still contains second-window navigation.'
    }
    foreach ($required in @('id="chat-console"', 'id="chat-settings"', 'id="chat-stop"', '/static/assets/Agent_b.ico', '/static/app.webmanifest')) {
        if ($chatSource -notmatch [regex]::Escape($required)) { throw "Installed Chat view is missing: $required" }
    }
    $manifestPath = Join-Path $testInstall 'web\app.webmanifest'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw 'Installed application manifest is missing.'
    }
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    if ($manifest.display_override[0] -ne 'window-controls-overlay') {
        throw 'Installed application does not request native Window Controls Overlay.'
    }
    if ($indexSource -match 'id="state-filters"' -or $indexSource -notmatch '>Activity<' -or $indexSource -notmatch '>Context<' -or $indexSource -notmatch '>History<') {
        throw 'Installed Console is not using the simplified instrument layout.'
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

    $webDirectory = Join-Path $testInstall 'web'
    $webAcl = Get-Acl -LiteralPath $webDirectory
    $aclMarker = [Security.AccessControl.FileSystemAccessRule]::new(
        [Security.Principal.WindowsIdentity]::GetCurrent().User,
        [Security.AccessControl.FileSystemRights]::ReadPermissions,
        [Security.AccessControl.InheritanceFlags]::None,
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow
    )
    $null = $webAcl.AddAccessRule($aclMarker)
    Set-Acl -LiteralPath $webDirectory -AclObject $webAcl
    $aclBeforeUpgrade = (Get-Acl -LiteralPath $webDirectory).Sddl
    $staleFile = Join-Path $webDirectory 'stale-upgrade-test.txt'
    Set-Content -LiteralPath $staleFile -Value 'removed by upgrade'

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -InstallDirectory $testInstall -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry
    if ($LASTEXITCODE -ne 0) { throw "Upgrade exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Upgrade changed the installed connection configuration.'
    }
    if ($credentialHash -and (Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash -ne $credentialHash) {
        throw 'Upgrade changed the installed credential.'
    }
    if ((Get-Acl -LiteralPath $webDirectory).Sddl -ne $aclBeforeUpgrade) {
        throw 'Upgrade replaced the protected web directory or changed its ACL.'
    }
    if (Test-Path -LiteralPath $staleFile) {
        throw 'Upgrade retained a stale program file.'
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
    Write-Host 'PASS: isolated Chat-first install, compact same-window navigation, native-controls manifest, simplified Console, branded app and shortcut icons, registration, settings and ACL-preserving upgrade, stale-file cleanup, preserving uninstall, reinstall, and purging uninstall'
} finally {
    if (Test-Path -LiteralPath $testRegistry) { Remove-Item -LiteralPath $testRegistry -Recurse -Force }
    if (Test-Path -LiteralPath $testRoot) {
        Assert-TemporaryTestPath $testRoot
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
