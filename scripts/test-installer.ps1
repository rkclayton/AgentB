[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('Agent_b-installer-test-' + [Guid]::NewGuid().ToString('N'))
$testApplication = Join-Path $testRoot 'Application\Agent_b'
$testData = Join-Path $testRoot 'Data\Agent_b'
$testWorkspace = Join-Path $testRoot 'ProgramData\Agent_b\workspace'
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
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode
    if ($LASTEXITCODE -ne 0) { throw "First install exited $LASTEXITCODE." }
    foreach ($path in @(
        (Join-Path $testApplication 'Agent_b.exe'),
        (Join-Path $testApplication 'Agent_b.cmd'),
        (Join-Path $testApplication 'scripts\launch-Agent_b.ps1'),
        (Join-Path $testApplication 'web\assets\Agent_b.ico'),
        (Join-Path $testStart 'Agent_b.lnk')
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing installed file: $path" }
    }
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File (Join-Path $testApplication 'scripts\launch-Agent_b.ps1') -ApplicationDirectory $testApplication -DataDirectory $testData -ConfigPath (Join-Path $testData 'harness.json') -Check
    if ($LASTEXITCODE -ne 0) { throw "Installed launcher check exited $LASTEXITCODE." }

    $launcherSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'scripts\launch-Agent_b.ps1')
    if ($launcherSource -notmatch "'chat'" -or $launcherSource -notmatch 'Show-AgentBWindow -Url \$appUrl -ReplaceExisting') {
        throw 'Installed launcher is not configured to open the Chat-first application view.'
    }
    if ($launcherSource -notmatch '\[switch\]\$Detached' -or
        $launcherSource -notmatch '\[switch\]\$NoPause' -or
        $launcherSource -notmatch "WindowStyle = 'Hidden'" -or
        $launcherSource -notmatch 'launcher-errors\.log') {
        throw 'Installed PowerShell launcher is missing detached automation or durable failure logging.'
    }
    $batchLauncherSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'Agent_b.cmd')
    if ($batchLauncherSource -match '(?im)^\s*pause\s*$' -or
        $batchLauncherSource -notmatch 'timeout /t 10 /nobreak' -or
        $batchLauncherSource -notmatch 'AGENT_B_AUTO_CLOSE') {
        throw 'Installed batch launcher can still wait indefinitely after a failure.'
    }
    $indexSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\index.html')
    $chatSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\chat.html')
    if ($indexSource -match 'target=' -or $chatSource -match 'target=') {
        throw 'Installed application still contains second-window navigation.'
    }
    if ($indexSource -match 'class="brand"' -or $chatSource -match 'class="chat-brand"') {
        throw 'Installed application still contains redundant in-page Agent_b branding.'
    }
    if ($indexSource -notmatch '<header class="header window-titlebar">\s+<nav id="tabs"' -or
        $indexSource.IndexOf('id="stop"') -gt $indexSource.IndexOf('id="chat-launch"') -or
        $chatSource.IndexOf('id="chat-stop"') -gt $chatSource.IndexOf('id="chat-current"') -or
        $indexSource -notmatch 'class="stop-sign"' -or $chatSource -notmatch 'class="stop-sign"') {
        throw 'Installed application is missing the consolidated tab header or leading stop-sign control.'
    }
    foreach ($required in @('id="chat-console"', 'id="chat-settings"', 'id="chat-stop"', '/static/assets/Agent_b.ico', '/static/app.webmanifest')) {
        if ($chatSource -notmatch [regex]::Escape($required)) { throw "Installed Chat view is missing: $required" }
    }
    foreach ($link in @(
        @{ Source = $indexSource; Pattern = '<a id="chat-launch"[^>]+href="/chat"'; Name = 'Console-to-Chat link' },
        @{ Source = $indexSource; Pattern = '<a id="console-launch"[^>]+href="/"'; Name = 'Console selector' },
        @{ Source = $chatSource; Pattern = '<a id="chat-console"[^>]+href="/"'; Name = 'Chat-to-Console link' },
        @{ Source = $chatSource; Pattern = '<a id="chat-settings"[^>]+href="/#settings/servers"'; Name = 'Chat-to-Settings link' }
    )) {
        if ($link.Source -notmatch $link.Pattern) { throw "Installed application is missing its native $($link.Name)." }
    }
    $chatCSS = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\css\chat.css')
    foreach ($required in @('.chat-identity-alarm { grid-row: 2; }', '.chat-budget { grid-row: 3; }', '.chat-log { grid-row: 4; }', '.chat-composer { grid-row: 5; }', '#chat-send {')) {
        if ($chatCSS -notmatch [regex]::Escape($required)) { throw "Installed Chat layout is missing: $required" }
    }
    $chatScript = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\js\chat.js')
    $settingsScript = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\js\settings.js')
    if ($chatScript -notmatch 'chatCurrent\.addEventListener\("click", \(event\) => event\.preventDefault\(\)\);' -or
        $settingsScript -notmatch 'consoleLaunch\.addEventListener\("click", \(event\) => \{\s+event\.preventDefault\(\);\s+if \(open\) closeSettings\(\);') {
        throw 'Installed application allows its selected view control to reload the page.'
    }
    $manifestPath = Join-Path $testApplication 'web\app.webmanifest'
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
    if (-not $shortcut.TargetPath.Equals((Join-Path $testApplication 'Agent_b.cmd'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Shortcut target does not point to Agent_b.cmd.'
    }
    if (-not $shortcut.IconLocation.StartsWith((Join-Path $testApplication 'web\assets\Agent_b.ico'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Shortcut does not use the Agent_b icon.'
    }

    $registration = Get-ItemProperty -LiteralPath $testRegistry
    if ($registration.DisplayName -ne 'Agent_b' -or
        -not ([string]$registration.InstallLocation).Equals($testApplication, [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$registration.DataLocation).Equals($testData, [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$registration.WorkspaceLocation).Equals($testWorkspace, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Installed apps registration is incorrect.'
    }

    $configPath = Join-Path $testData 'harness.json'
    $configHash = (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash
    $installedConfig = Get-Content -Raw -LiteralPath $configPath | ConvertFrom-Json
    if (-not ([string]$installedConfig.workspace).Equals($testWorkspace, [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$installedConfig.log_dir).Equals((Join-Path $testData 'logs'), [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$installedConfig.memory.dir).Equals((Join-Path $testData 'memory'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Installed configuration does not use the three-root layout.'
    }
    $credentialPath = Join-Path $testData '.agentb-shell-credential.dpapi'
    [IO.File]::WriteAllBytes($credentialPath, [byte[]](1, 2, 3, 4))
    $credentialHash = (Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash
    $workspaceMarker = Join-Path $testWorkspace 'preserve-me.txt'
    Set-Content -LiteralPath $workspaceMarker -Value 'preserve'

    $savedErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid 'S-1-5-18' -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -PurgeData -TestMode 2>$null
    $wrongPurgeExit = $LASTEXITCODE
    $ErrorActionPreference = $savedErrorAction
    if ($wrongPurgeExit -eq 0 -or -not (Test-Path -LiteralPath $configPath -PathType Leaf) -or -not (Test-Path -LiteralPath $workspaceMarker -PathType Leaf)) {
        throw 'Wrong-operator purge was not refused before changing data.'
    }

    $webDirectory = Join-Path $testApplication 'web'
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

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Upgrade exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Upgrade changed the installed connection configuration.'
    }
    if ((Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash -ne $credentialHash) {
        throw 'Upgrade changed the installed credential.'
    }
    if ((Get-Acl -LiteralPath $webDirectory).Sddl -ne $aclBeforeUpgrade) {
        throw 'Upgrade replaced the protected web directory or changed its ACL.'
    }
    if (Test-Path -LiteralPath $staleFile) {
        throw 'Upgrade retained a stale program file.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value) -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Preserving uninstall exited $LASTEXITCODE." }
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $credentialPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $workspaceMarker -PathType Leaf) -or
        (Test-Path -LiteralPath (Join-Path $testApplication 'Agent_b.exe')) -or
        (Test-Path -LiteralPath (Join-Path $testStart 'Agent_b.lnk')) -or
        (Test-Path -LiteralPath $testRegistry)) {
        throw 'Preserving uninstall did not keep only local data.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $installer -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Reinstall exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Reinstall changed preserved connection configuration.'
    }

    & powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value) -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -PurgeData -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Purging uninstall exited $LASTEXITCODE." }
    if ((Test-Path -LiteralPath $testApplication) -or
        (Test-Path -LiteralPath $testData) -or
        (Test-Path -LiteralPath $testWorkspace) -or
        (Test-Path -LiteralPath (Join-Path $testStart 'Agent_b.lnk')) -or
        (Test-Path -LiteralPath $testRegistry)) {
        throw 'Uninstall left a program, shortcut, or registration artifact.'
    }
    Write-Host 'PASS: isolated three-root install, upgrade preservation, preserve-data uninstall, reinstall, and owner-checked purge uninstall'
} finally {
    if (Test-Path -LiteralPath $testRegistry) { Remove-Item -LiteralPath $testRegistry -Recurse -Force }
    if (Test-Path -LiteralPath $testRoot) {
        Assert-TemporaryTestPath $testRoot
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
