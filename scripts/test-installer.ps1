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
$installerWrapper = Join-Path (Split-Path -Parent $PSScriptRoot) 'install-Agent_b.cmd'

function Assert-TemporaryTestPath {
    param([string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $full.StartsWith($temp, [StringComparison]::OrdinalIgnoreCase) -or
        -not (Split-Path -Leaf $full).StartsWith('Agent_b-installer-test-', [StringComparison]::Ordinal)) {
        throw "Refusing to clean unexpected test path: $full"
    }
}

function Get-FreeTcpPort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    } finally { $listener.Stop() }
}

function Get-AgentBProcessesAtPath {
    param([string]$Executable)
    return @(Get-Process -Name 'Agent_b' -ErrorAction SilentlyContinue | Where-Object {
        try { [IO.Path]::GetFullPath($_.Path).Equals([IO.Path]::GetFullPath($Executable), [StringComparison]::OrdinalIgnoreCase) } catch { $false }
    })
}

function Get-FilePrefixHash {
    param([string]$Path, [long]$Length)
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $buffer = New-Object byte[] 65536
        $remaining = $Length
        while ($remaining -gt 0) {
            $read = $stream.Read($buffer, 0, [Math]::Min($buffer.Length, $remaining))
            if ($read -le 0) { throw "File became shorter while hashing its prefix: $Path" }
            $null = $sha.TransformBlock($buffer, 0, $read, $buffer, 0)
            $remaining -= $read
        }
        $null = $sha.TransformFinalBlock([byte[]]::new(0), 0, 0)
        return ([BitConverter]::ToString($sha.Hash) -replace '-', '')
    } finally {
        $sha.Dispose()
        $stream.Dispose()
    }
}

try {
    & powershell.exe -NoLogo -NoProfile -File $installer -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode
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
    & powershell.exe -NoLogo -NoProfile -File (Join-Path $testApplication 'scripts\launch-Agent_b.ps1') -ApplicationDirectory $testApplication -DataDirectory $testData -ConfigPath (Join-Path $testData 'harness.json') -Check
    if ($LASTEXITCODE -ne 0) { throw "Installed launcher check exited $LASTEXITCODE." }

    $launcherSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'scripts\launch-Agent_b.ps1')
    if ($launcherSource -notmatch "'chat'" -or $launcherSource -notmatch 'Show-AgentBWindow -Url \$appUrl -ReplaceExisting') {
        throw 'Installed launcher is not configured to open the Chat-first application view.'
    }
    if ($launcherSource -notmatch '\[switch\]\$Detached' -or
        $launcherSource -notmatch '\[switch\]\$NoPause' -or
        $launcherSource -notmatch '\[switch\]\$Console' -or
        $launcherSource -notmatch "WindowStyle = 'Hidden'" -or
        $launcherSource -notmatch 'launcher-errors\.log') {
        throw 'Installed PowerShell launcher is missing hidden-default/Console-opt-in launch behavior, detached automation, or durable failure logging.'
    }
    $batchLauncherSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'Agent_b.cmd')
    if ($batchLauncherSource -match '(?im)^\s*pause\s*$' -or
        $batchLauncherSource -notmatch 'AGENTB_HIDDEN_REENTRY' -or
        $batchLauncherSource -notmatch '"-Console"' -or
        $batchLauncherSource -notmatch 'timeout /t 10 /nobreak' -or
        $batchLauncherSource -notmatch 'AGENT_B_AUTO_CLOSE') {
        throw 'Installed batch launcher does not hide by default with -Console opt-in, or can still wait indefinitely after a failure.'
    }
    $sourceBatchLauncher = Get-Content -Raw -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'start-Agent_b.cmd')
    if ($sourceBatchLauncher -notmatch 'AGENTB_HIDDEN_REENTRY' -or
        $sourceBatchLauncher -notmatch '"-Console"' -or
        $sourceBatchLauncher -notmatch 'launcher-errors\.log') {
        throw 'Source launcher does not hide by default with -Console opt-in and durable failure logging.'
    }
    $indexSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\index.html')
    $chatSource = $indexSource
    $planSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\plan.html')
    $shellSource = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\js\shell.js')
    if ($indexSource -match 'target=' -or $chatSource -match 'target=' -or $planSource -match 'target=') {
        throw 'Installed application still contains second-window navigation.'
    }
    if ($indexSource -match 'class="brand"' -or $chatSource -match 'class="chat-brand"') {
        throw 'Installed application still contains redundant in-page Agent_b branding.'
    }
    foreach ($page in @(
        @{ Source = $indexSource; Name = 'console' },
        @{ Source = $planSource; Name = 'plan' }
    )) {
        if ($page.Source -notmatch ('id="app-shell"[^>]+data-page="' + $page.Name + '"')) {
            throw "Installed $($page.Name) page is missing the shared shell slot."
        }
    }
    if ($shellSource -notmatch 'root\.append\(left, right\)' -or
        $shellSource -notmatch 'right\.append\(pages, settings\)' -or
        $shellSource -match 'shell-operator-status' -or
        $shellSource -match 'all:\s*true') {
        throw 'Installed shared shell does not preserve agent-tabs/right-controls ownership.'
    }
    foreach ($required in @('id="chat-attach"', 'id="chat-expand"', 'rows="5"', '/static/assets/Agent_b.ico', '/static/app.webmanifest')) {
        if ($chatSource -notmatch [regex]::Escape($required)) { throw "Installed Chat view is missing: $required" }
    }
    if ($chatSource -match 'chat-clear-conversation|chat-attachment-controls') {
        throw 'Installed Chat view still contains removed Clear or attachment-pane chrome.'
    }
    foreach ($link in @('Plan", "/plan"')) {
        if ($shellSource -notmatch [regex]::Escape($link)) { throw "Installed application is missing page switch $link." }
    }
    foreach ($removed in @('Chat", "/chat"', 'Console", "/"')) {
        if ($shellSource -match [regex]::Escape($removed)) { throw "Installed application retains removed page switch $removed." }
    }
    $chatCSS = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\css\chat.css')
    foreach ($required in @('.chat-budget { grid-row: 2; }', '.chat-log { grid-row: 3; }', '.chat-composer { grid-row: 4; }', '#chat-send {')) {
        if ($chatCSS -notmatch [regex]::Escape($required)) { throw "Installed Chat layout is missing: $required" }
    }
    $chatScript = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\js\chat.js')
    $settingsScript = Get-Content -Raw -LiteralPath (Join-Path $testApplication 'web\js\settings.js')
    if ($shellSource -notmatch 'link\.onclick = \(event\) => event\.preventDefault\(\);' -or
        $settingsScript -notmatch 'gear\.addEventListener\("click", \(event\) => \{\s+event\.preventDefault\(\);' -or
        $settingsScript -match 'consoleLaunch') {
        throw 'Installed application does not preserve selected-Plan or Settings in-place navigation.'
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
    if ($indexSource -match 'id="composer"' -or $indexSource -match 'id="task"' -or $indexSource -match '>Send</button>') {
        throw 'Installed Console still contains the removed task composer.'
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
    $installedConfig = Get-Content -Raw -LiteralPath $configPath | ConvertFrom-Json
    if (-not ([string]$installedConfig.workspace).Equals($testWorkspace, [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$installedConfig.log_dir).Equals((Join-Path $testData 'logs'), [StringComparison]::OrdinalIgnoreCase) -or
        -not ([string]$installedConfig.memory.dir).Equals((Join-Path $testData 'memory'), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Installed configuration does not use the three-root layout.'
    }
    $testPort = Get-FreeTcpPort
    $installedConfig.listen = "127.0.0.1:$testPort"
    [IO.File]::WriteAllText($configPath, ($installedConfig | ConvertTo-Json -Depth 100) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    $configHash = (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash

    $portOwner = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $testPort)
    $portOwner.Start()
    try {
        $savedErrorAction = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        $portFailure = (& powershell.exe -NoLogo -NoProfile -File (Join-Path $testApplication 'scripts\launch-Agent_b.ps1') -ApplicationDirectory $testApplication -DataDirectory $testData -ConfigPath $configPath -Detached -NoBrowser -NoPause -StartupTimeoutSeconds 5 2>&1 | Out-String)
        $portFailureExit = $LASTEXITCODE
        $ErrorActionPreference = $savedErrorAction
    } finally {
        $portOwner.Stop()
    }
    if ($portFailureExit -eq 0 -or $portFailure -notmatch [regex]::Escape("listen port $testPort is already in use") -or
        $portFailure -notmatch 'Diagnostics:' -or $portFailure -notmatch 'startup-' -or $portFailure -notmatch '\.log') {
        throw "Port-conflict launch did not name its cause and diagnostic files.`n$portFailure"
    }
    if ($launcherSource -notmatch 'Configuration error in' -or $launcherSource -notmatch 'Permission error') {
        throw 'Installed launcher does not classify configuration and permission startup failures.'
    }

    $credentialPath = Join-Path $testData '.agentb-shell-credential.dpapi'
    [IO.File]::WriteAllBytes($credentialPath, [byte[]](1, 2, 3, 4))
    $credentialHash = (Get-FileHash -LiteralPath $credentialPath -Algorithm SHA256).Hash
    $workspaceMarker = Join-Path $testWorkspace 'preserve-me.txt'
    Set-Content -LiteralPath $workspaceMarker -Value 'preserve'

    $savedErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & powershell.exe -NoLogo -NoProfile -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid 'S-1-5-18' -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -PurgeData -TestMode 2>$null
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

    $installedBinary = Join-Path $testApplication 'Agent_b.exe'
    $beforeStdout = Join-Path $testRoot 'running-before-stdout.log'
    $beforeStderr = Join-Path $testRoot 'running-before-stderr.log'
    $beforeArguments = '-config "' + $configPath + '" -app-root "' + $testApplication + '" -data-root "' + $testData + '"'
    $beforeProcess = Start-Process -FilePath $installedBinary -ArgumentList $beforeArguments -WorkingDirectory $testData -WindowStyle Hidden -RedirectStandardOutput $beforeStdout -RedirectStandardError $beforeStderr -PassThru
    $ready = $false
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        Start-Sleep -Milliseconds 200
        try {
            $beforeState = Invoke-RestMethod -Uri "http://127.0.0.1:$testPort/api/state" -TimeoutSec 1
            $ready = $true
        } catch { }
    } while (-not $ready -and -not $beforeProcess.HasExited -and [DateTime]::UtcNow -lt $deadline)
    if (-not $ready) { throw 'Installed Agent_b did not become ready before the running-instance upgrade.' }

    $dataBefore = @(Get-ChildItem -LiteralPath $testData -File -Recurse | Where-Object { -not $_.FullName.StartsWith((Join-Path $testData 'logs') + '\', [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object {
        [pscustomobject]@{ Path = $_.FullName; SHA256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }
    })
    $workspaceBefore = @(Get-ChildItem -LiteralPath $testWorkspace -File -Recurse | ForEach-Object {
        [pscustomobject]@{ Path = $_.FullName; SHA256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }
    })
    $logsBefore = @(Get-ChildItem -LiteralPath (Join-Path $testData 'logs') -File -Recurse | ForEach-Object {
        [pscustomobject]@{ Path = $_.FullName; Length = $_.Length; PrefixSHA256 = Get-FilePrefixHash -Path $_.FullName -Length $_.Length }
    })

    $upgradeTranscriptPath = Join-Path $testData 'logs\running-upgrade-transcript.log'
    $savedInstallLog = $env:AGENT_B_INSTALL_LOG
    $savedNoPause = $env:AGENT_B_INSTALL_NO_PAUSE
    $savedNoBrowser = $env:AGENT_B_INSTALL_NO_BROWSER
    $env:AGENT_B_INSTALL_LOG = $upgradeTranscriptPath
    $env:AGENT_B_INSTALL_NO_PAUSE = '1'
    $env:AGENT_B_INSTALL_NO_BROWSER = '1'
    try {
        $upgradeOutput = (& $installerWrapper -SourceDirectory (Split-Path -Parent $PSScriptRoot) -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode -SkipBuild 2>&1 | Out-String)
        $upgradeExit = $LASTEXITCODE
    } finally {
        $env:AGENT_B_INSTALL_LOG = $savedInstallLog
        $env:AGENT_B_INSTALL_NO_PAUSE = $savedNoPause
        $env:AGENT_B_INSTALL_NO_BROWSER = $savedNoBrowser
    }
    if ($upgradeExit -ne 0) { throw "Running-instance wrapper upgrade exited $upgradeExit.`n$upgradeOutput" }
    if ($upgradeOutput -notmatch 'STOPPING: Agent_b PID' -or $upgradeOutput -notmatch 'STOPPED: Agent_b PID' -or $upgradeOutput -notmatch "Agent_b is ready at http://127\.0\.0\.1:$testPort/chat") {
        throw "Running-instance upgrade did not report stop and restart lifecycle.`n$upgradeOutput"
    }
    $strictUtf8 = [Text.UTF8Encoding]::new($false, $true)
    $upgradeTranscript = $strictUtf8.GetString([IO.File]::ReadAllBytes($upgradeTranscriptPath))
    if ($upgradeTranscript.Contains([char]0) -or $upgradeTranscript.Contains([char]0xfffd)) {
        throw 'Running-instance transcript is not one continuous UTF-8 encoding.'
    }
    if ($upgradeTranscript -notmatch 'AUTOSTART COMPLETE: Agent_b started through ' -or
        $upgradeTranscript -notmatch "Agent_b is ready at http://127\.0\.0\.1:$testPort/chat" -or
        $upgradeTranscript -match 'Next: open Agent_b from Start' -or
        $upgradeTranscript -notmatch [regex]::Escape("Transcript: $upgradeTranscriptPath")) {
        throw 'Running-instance transcript is missing its UTF-8 autostart record/path or retains contradictory closing guidance.'
    }
    $beforeProcess.WaitForExit(15000) | Out-Null
    if (-not $beforeProcess.HasExited) { throw 'The pre-upgrade Agent_b process did not exit.' }
    if ((Get-Content -LiteralPath $beforeStdout -Raw) -notmatch 'stopping on terminated') {
        throw 'The pre-upgrade Agent_b process did not record graceful signal shutdown.'
    }
    $afterProcesses = @(Get-AgentBProcessesAtPath -Executable $installedBinary)
    if ($afterProcesses.Count -ne 1 -or $afterProcesses[0].Id -eq $beforeProcess.Id) {
        throw "Running-instance upgrade did not finish with exactly one restarted instance: $(@($afterProcesses.Id) -join ', ')"
    }
    $afterState = Invoke-RestMethod -Uri "http://127.0.0.1:$testPort/api/state" -TimeoutSec 5
    if ([bool]$afterState.build.dirty -ne [bool]$beforeState.build.dirty -or $afterState.build.commit -ne $beforeState.build.commit) {
        throw 'Restarted Agent_b identity does not match the installed build.'
    }
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
    foreach ($entry in $dataBefore) {
        if (-not (Test-Path -LiteralPath $entry.Path -PathType Leaf)) { throw "Upgrade removed production data: $($entry.Path)" }
    }
    foreach ($entry in $workspaceBefore) {
        if (-not (Test-Path -LiteralPath $entry.Path -PathType Leaf) -or (Get-FileHash -LiteralPath $entry.Path -Algorithm SHA256).Hash -ne $entry.SHA256) {
            throw "Upgrade changed workspace evidence: $($entry.Path)"
        }
    }
    foreach ($entry in $logsBefore) {
        if (-not (Test-Path -LiteralPath $entry.Path -PathType Leaf)) { throw "Upgrade removed log evidence: $($entry.Path)" }
        $afterLength = (Get-Item -LiteralPath $entry.Path).Length
        if ($afterLength -lt $entry.Length -or (Get-FilePrefixHash -Path $entry.Path -Length $entry.Length) -ne $entry.PrefixSHA256) {
            throw "Upgrade changed an existing log prefix: $($entry.Path)"
        }
    }
    & (Join-Path $env:SystemRoot 'System32\taskkill.exe') /PID $afterProcesses[0].Id | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not stop the restarted disposable Agent_b.' }
    $afterProcesses[0].WaitForExit(15000) | Out-Null
    if (-not $afterProcesses[0].HasExited) { throw 'Restarted disposable Agent_b did not exit.' }

    & powershell.exe -NoLogo -NoProfile -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value) -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Preserving uninstall exited $LASTEXITCODE." }
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $credentialPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $workspaceMarker -PathType Leaf) -or
        (Test-Path -LiteralPath (Join-Path $testApplication 'Agent_b.exe')) -or
        (Test-Path -LiteralPath (Join-Path $testStart 'Agent_b.lnk')) -or
        (Test-Path -LiteralPath $testRegistry)) {
        throw 'Preserving uninstall did not keep only local data.'
    }

    & powershell.exe -NoLogo -NoProfile -File $installer -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Reinstall exited $LASTEXITCODE." }
    if ((Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $configHash) {
        throw 'Reinstall changed preserved connection configuration.'
    }

    & powershell.exe -NoLogo -NoProfile -File $uninstaller -ApplicationDirectory $testApplication -DataDirectory $testData -WorkspaceDirectory $testWorkspace -StartMenuDirectory $testStart -UninstallRegistryPath $testRegistry -ExpectedOperatorSid ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value) -ExpectedOperatorLocalAppData ([Environment]::GetFolderPath('LocalApplicationData')) -Quiet -PurgeData -TestMode
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

& powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'test-chat-acceptance.ps1')
if ($LASTEXITCODE -ne 0) { throw "Chat acceptance release gate exited $LASTEXITCODE." }
