[CmdletBinding()]
param(
    [string]$Go,
    [string]$C1Model,
    [string]$C2Model,
    [string]$BaseUrl = 'http://127.0.0.1:8080',
    [int]$Port = 8080
)

$ErrorActionPreference = 'Stop'
$Go = if ($Go) { $Go } elseif ($env:GO) { $env:GO } else { 'go' }
$C1Model = if ($C1Model) { $C1Model } else { $env:C1_MODEL }
$C2Model = if ($C2Model) { $C2Model } else { $env:C2_MODEL }
if (-not $C1Model) { throw 'C1_MODEL is unset; provide the first candidate GGUF path.' }
if (-not $C2Model) { throw 'C2_MODEL is unset; provide the second candidate GGUF path.' }
$startedAt = Get-Date
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$startScript = Join-Path $repoRoot 'serve\start.ps1'
$runsRoot = Join-Path $PSScriptRoot 'runs'
$serverProcess = $null
$c2Offload = 'not-needed'

function Stop-CanaryServer {
    if ($null -ne $script:serverProcess -and -not $script:serverProcess.HasExited) {
        & taskkill.exe /T /F /PID $script:serverProcess.Id 2>$null | Out-Null
        try { $script:serverProcess.WaitForExit(15000) | Out-Null } catch {}
    }
    $script:serverProcess = $null
}

function Wait-Health {
    param([Diagnostics.Process]$Process, [int]$Seconds = 240)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if ($Process.HasExited) { return $false }
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/health" -TimeoutSec 3
            if ([int]$response.StatusCode -eq 200) { return $true }
        } catch {}
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Start-CanaryServer {
    param([string]$Name, [string]$Model, [int]$Context, [string]$Fit = 'off')
    Stop-CanaryServer
    $log = Join-Path $runsRoot "$Name-server.log"
    $arguments = @(
        '-NoProfile', '-NonInteractive',
        '-File', $startScript,
        '-MODEL_PATH', $Model,
        '-CTX', [string]$Context,
        '-PORT', [string]$Port,
        '-FIT', $Fit,
        '-LOG_FILE', $log
    )
    $script:serverProcess = Start-Process -FilePath 'powershell.exe' -ArgumentList $arguments -PassThru -WindowStyle Hidden
    if (-not (Wait-Health -Process $script:serverProcess)) {
        Stop-CanaryServer
        return $false
    }
    $props = Invoke-RestMethod -Uri "$BaseUrl/props" -TimeoutSec 10
    $actualContext = [int]$props.default_generation_settings.n_ctx
    if ($actualContext -ne $Context) {
        Stop-CanaryServer
        throw "$Name loaded context $actualContext instead of $Context"
    }
    return $true
}

function Invoke-Trials {
    param([string]$Name)
    $runDir = Join-Path $runsRoot $Name
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    Get-ChildItem -LiteralPath $runDir -Filter '*.jsonl' -File | Remove-Item -Force
    foreach ($task in @('fix', 'add')) {
        1..3 | ForEach-Object {
            $trial = "${task}-$($_)"
            $workspace = Join-Path $runDir "workspace-$trial"
            $out = Join-Path $runDir "$trial.jsonl"
            & $Go run ./cmd/fixture $workspace
            if ($LASTEXITCODE -ne 0) { throw "fixture failed for $Name/$trial" }
            & $Go run ./cmd/loop --base-url $BaseUrl --model 'qwen3.8-27b' --workspace $workspace --task $task --out $out --temperature 0.6 --effort medium --max-turns 12
            if ($LASTEXITCODE -ne 0) { throw "loop failed for $Name/$trial" }
        }
    }
    $score = (& $Go run ./cmd/score $runDir | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { throw "score failed for $Name" }
    [IO.File]::WriteAllText((Join-Path $runDir 'score.md'), $score + [Environment]::NewLine, (New-Object Text.UTF8Encoding($false)))
    Write-Host $score
}

try {
    if (-not (Get-Command $Go -ErrorAction SilentlyContinue)) { throw "Go not found: $Go" }
    New-Item -ItemType Directory -Force -Path $runsRoot | Out-Null
    Push-Location $PSScriptRoot
    try {
        & $Go build ./...
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
        & $Go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

        if (-not (Start-CanaryServer -Name 'c1' -Model $C1Model -Context 32768)) { throw 'C1 server failed to start' }
        Invoke-Trials -Name 'c1'
        Stop-CanaryServer

        if (-not (Test-Path -LiteralPath $C2Model -PathType Leaf)) {
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $C2Model) | Out-Null
            $url = 'https://huggingface.co/unsloth/Qwen3.8-27B-GGUF/resolve/main/Qwen3.8-27B-UD-IQ4_XS.gguf?download=true'
            & curl.exe -fL --retry 5 --retry-delay 5 -C - -o $C2Model $url
            if ($LASTEXITCODE -ne 0) { throw "C2 download failed with curl exit $LASTEXITCODE" }
        }
        if (-not (Start-CanaryServer -Name 'c2' -Model $C2Model -Context 16384)) {
            $script:c2Offload = 'fit-on'
            if (-not (Start-CanaryServer -Name 'c2' -Model $C2Model -Context 16384 -Fit 'on')) { throw 'C2 server failed to start with fit off and fit on' }
        }
        Invoke-Trials -Name 'c2'
    } finally {
        Pop-Location
    }
} finally {
    Stop-CanaryServer
    $summary = [ordered]@{
        started_at = $startedAt.ToUniversalTime().ToString('o')
        finished_at = (Get-Date).ToUniversalTime().ToString('o')
        wall_time_minutes = [Math]::Round(((Get-Date) - $startedAt).TotalMinutes, 2)
        c2_offload = $c2Offload
    }
    $summary | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $runsRoot 'summary.json') -Encoding UTF8
}
