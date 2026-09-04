[CmdletBinding()]
param(
    [string]$RootDirectory,
    [ValidateRange(5, 300)]
    [int]$StartupTimeoutSeconds = 30,
    [switch]$NoBrowser,
    [switch]$Detached,
    [switch]$NoPause,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($RootDirectory)) {
    $RootDirectory = Split-Path -Parent $PSScriptRoot
}
$root = [IO.Path]::GetFullPath($RootDirectory).TrimEnd('\')
$executable = Join-Path $root 'Agent_b.exe'
$configPath = Join-Path $root 'harness.json'
$launcherErrorLog = Join-Path $root 'logs\launcher-errors.log'

trap {
    $message = ($_.Exception.Message -replace '[\r\n]+', ' ').Trim()
    $logged = $false
    try {
        $null = New-Item -ItemType Directory -Path (Split-Path -Parent $launcherErrorLog) -Force
        Add-Content -LiteralPath $launcherErrorLog -Encoding UTF8 -Value ('{0} {1}' -f [DateTime]::Now.ToString('yyyy-MM-dd HH:mm:ss zzz'), $message)
        $logged = $true
    } catch { }
    [Console]::Error.WriteLine("Agent_b launch failed: $message")
    if ($logged) {
        [Console]::Error.WriteLine("Details were appended to $launcherErrorLog")
    }
    exit 1
}

function Get-AgentBUrl {
    if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
        return 'http://127.0.0.1:8790/'
    }
    $config = Get-Content -Raw -LiteralPath $configPath | ConvertFrom-Json
    $listen = [string]$config.listen
    $separator = $listen.LastIndexOf(':')
    if ($separator -lt 0 -or $separator -eq $listen.Length - 1) {
        throw "The configured listen address is invalid: $listen"
    }
    $port = 0
    if (-not [int]::TryParse($listen.Substring($separator + 1), [ref]$port) -or $port -lt 1 -or $port -gt 65535) {
        throw "The configured listen port is invalid: $listen"
    }
    return "http://127.0.0.1:$port/"
}

function Test-AgentBEndpoint {
    param([string]$Url)
    try {
        $endpoint = [Uri]::new([Uri]$Url, 'api/state')
        $response = Invoke-WebRequest -UseBasicParsing -Uri $endpoint -TimeoutSec 1
        return $response.StatusCode -eq 200
    } catch {
        return $false
    }
}

function Get-AgentBProcesses {
    return @(Get-CimInstance Win32_Process -Filter "Name='Agent_b.exe'" -ErrorAction SilentlyContinue | Where-Object {
        $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath).Equals($executable, [StringComparison]::OrdinalIgnoreCase)
    })
}

function Show-AgentBWindow {
    param([string]$Url, [switch]$ReplaceExisting)
    if ($NoBrowser -or $env:AGENTB_NO_BROWSER) {
        Write-Host "UI ready: $Url"
        return
    }

    $shell = New-Object -ComObject WScript.Shell
    foreach ($name in @('msedge', 'chrome', 'firefox')) {
        foreach ($browser in Get-Process -Name $name -ErrorAction SilentlyContinue) {
            if ($browser.MainWindowTitle -like 'Agent_b*') {
                if ($ReplaceExisting) {
                    if ($browser.CloseMainWindow()) {
                        Write-Host 'CLOSED: stale Agent_b application window'
                        try { $browser.WaitForExit(3000) | Out-Null } catch { }
                    }
                } elseif ($shell.AppActivate($browser.Id)) {
                    Write-Host 'REUSED: existing Agent_b browser window'
                    return
                }
            }
        }
    }

    $edgeCandidates = @(
        $(if (${env:ProgramFiles(x86)}) { Join-Path ${env:ProgramFiles(x86)} 'Microsoft\Edge\Application\msedge.exe' }),
        $(if ($env:ProgramFiles) { Join-Path $env:ProgramFiles 'Microsoft\Edge\Application\msedge.exe' })
    ) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) }
    $edge = $edgeCandidates | Select-Object -First 1
    if (-not $edge) {
        $edgeCommand = Get-Command msedge.exe -ErrorAction SilentlyContinue
        if ($edgeCommand) { $edge = $edgeCommand.Source }
    }
    if ($edge) {
        Start-Process -FilePath $edge -ArgumentList "--app=$Url"
        Write-Host 'OPENED: Agent_b application window'
        return
    }
    Start-Process $Url
    Write-Host 'OPENED: Agent_b in the default browser'
}

function Wait-AgentBEndpoint {
    param([string]$Url, [Diagnostics.Process]$Process, [int]$Seconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (Test-AgentBEndpoint -Url $Url) { return 'ready' }
        if ($Process -and $Process.HasExited) { return 'exited' }
        Start-Sleep -Milliseconds 250
    }
    return 'timeout'
}

if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    throw "Agent_b executable is missing: $executable"
}
$url = Get-AgentBUrl
$appUrl = [Uri]::new([Uri]$url, 'chat').AbsoluteUri

if ($Check) {
    Write-Host 'Agent_b launcher check'
    Write-Host "Executable: $executable"
    Write-Host "Configuration: $configPath"
    Write-Host "API base: $url"
    Write-Host "Application: $appUrl"
    Write-Host "Running instances: $((Get-AgentBProcesses).Count)"
    Write-Host "Endpoint ready: $(Test-AgentBEndpoint -Url $url)"
    Write-Host 'CHECK COMPLETE: nothing was started or stopped'
    exit 0
}

$created = $false
$mutex = [Threading.Mutex]::new($false, 'Local\Agent_b-Launch', [ref]$created)
$locked = $false
try {
    try { $locked = $mutex.WaitOne([TimeSpan]::FromSeconds(15)) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) {
        throw 'Another Agent_b launch is still being resolved. Wait a moment and try again.'
    }

    if (Test-AgentBEndpoint -Url $url) {
        Write-Host 'Agent_b is already running; no new server was started.'
        Show-AgentBWindow -Url $appUrl
        exit 0
    }

    $existing = Get-AgentBProcesses
    if ($existing.Count) {
        Write-Host "Agent_b process $($existing[0].ProcessId) exists; waiting for its UI instead of starting another instance."
        $state = Wait-AgentBEndpoint -Url $url -Seconds $StartupTimeoutSeconds
        if ($state -eq 'ready') {
            Show-AgentBWindow -Url $appUrl
            exit 0
        }
        $existing = Get-AgentBProcesses
        if ($existing.Count) {
            throw "Agent_b process $($existing[0].ProcessId) is still running but $url is not responding. It was not closed because it may own an active task. Close its console or end that process, then launch Agent_b again."
        }
        Write-Host 'The earlier Agent_b process exited; starting a fresh instance.'
    }

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Agent_b must be launched normally, not from an elevated terminal or Run as administrator.'
    }

    if ($Detached) {
        Write-Host 'Starting Agent_b in the background.'
    } else {
        Write-Host 'Starting Agent_b. Close this window or press Ctrl+C to stop it.'
    }
    $configArgument = '-config "' + $configPath.Replace('"', '\"') + '"'
    $start = @{
        FilePath = $executable
        ArgumentList = $configArgument
        WorkingDirectory = $root
        PassThru = $true
    }
    if ($Detached) {
        $start.WindowStyle = 'Hidden'
    } else {
        $start.NoNewWindow = $true
    }
    $process = Start-Process @start
    $state = Wait-AgentBEndpoint -Url $url -Process $process -Seconds $StartupTimeoutSeconds
    if ($state -eq 'exited') {
        $process.WaitForExit()
        throw "Agent_b exited before its UI became ready (exit code $($process.ExitCode)). Review the startup output above."
    }
    if ($state -eq 'timeout') {
        Write-Warning "Agent_b process $($process.Id) is running, but $url did not become ready within $StartupTimeoutSeconds seconds. No browser was opened."
        Write-Host $(if ($Detached) { 'The process is being left running in the background; inspect logs or use -Check to confirm readiness.' } else { 'The process is being left running in this console so delayed startup remains visible.' })
    } else {
        Write-Host "Agent_b is ready at $appUrl"
        Show-AgentBWindow -Url $appUrl -ReplaceExisting
    }
} finally {
    if ($locked) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}

if ($Detached) {
    Write-Host "Agent_b is running in the background as process $($process.Id)."
    exit 0
}

$process.WaitForExit()
$exitCode = $process.ExitCode
if ($exitCode -ne 0) {
    throw "Agent_b stopped with exit code $exitCode."
}
Write-Host 'Agent_b stopped.'
exit 0
