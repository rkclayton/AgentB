[CmdletBinding()]
param(
    [string]$MODEL_PATH,
    [string]$CTX,
    [string]$KV_TYPE,
    [string]$PORT,
    [string]$BIND_ADDRESS,
    [string]$MTP,
    [string]$TOKEN_EMBEDDING_CPU,
    [string]$FIT,
    [string]$LLAMA_SERVER,
    [string]$MODEL_ALIAS,
    [string]$MTP_MODEL_PATH,
    [string]$LOG_FILE
)

$ErrorActionPreference = 'Stop'
$boundParameters = $PSBoundParameters
$localEnvPath = Join-Path $PSScriptRoot 'local.env'

if (Test-Path -LiteralPath $localEnvPath -PathType Leaf) {
    foreach ($line in Get-Content -LiteralPath $localEnvPath) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
        $parts = $trimmed.Split('=', 2)
        if ($parts.Count -ne 2) { throw "invalid serve/local.env line: $line" }
        $name = $parts[0].Trim()
        $value = $parts[1].Trim().Trim('"').Trim("'")
        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}

function Get-StartSetting {
    param([string]$Name, [string]$Value, [string]$Default)
    if ($boundParameters.ContainsKey($Name)) { return $Value }
    $environmentValue = [Environment]::GetEnvironmentVariable($Name)
    if ($null -ne $environmentValue -and $environmentValue.Trim() -ne '') { return $environmentValue }
    return $Default
}

$MODEL_PATH = Get-StartSetting 'MODEL_PATH' $MODEL_PATH ''
$LLAMA_SERVER = Get-StartSetting 'LLAMA_SERVER' $LLAMA_SERVER 'llama-server'
$MODEL_ALIAS = Get-StartSetting 'MODEL_ALIAS' $MODEL_ALIAS 'local'
$CTX = [int](Get-StartSetting 'CTX' $CTX '32768')
$KV_TYPE = Get-StartSetting 'KV_TYPE' $KV_TYPE 'q8_0'
$PORT = [int](Get-StartSetting 'PORT' $PORT '8080')
$BIND_ADDRESS = Get-StartSetting 'BIND_ADDRESS' $BIND_ADDRESS '127.0.0.1'
$MTP = Get-StartSetting 'MTP' $MTP 'off'
$MTP_MODEL_PATH = Get-StartSetting 'MTP_MODEL_PATH' $MTP_MODEL_PATH ''
$TOKEN_EMBEDDING_CPU = Get-StartSetting 'TOKEN_EMBEDDING_CPU' $TOKEN_EMBEDDING_CPU 'off'
$FIT = Get-StartSetting 'FIT' $FIT 'off'
$LOG_FILE = Get-StartSetting 'LOG_FILE' $LOG_FILE ''

if (-not $MODEL_PATH) {
    throw 'MODEL_PATH is unset; copy serve/local.env.example to serve/local.env and set MODEL_PATH.'
}
if ($CTX -lt 1024 -or $CTX -gt 1048576) { throw 'CTX must be between 1024 and 1048576.' }
if ($PORT -lt 1 -or $PORT -gt 65535) { throw 'PORT must be between 1 and 65535.' }
if ($KV_TYPE -notin @('q8_0', 'q4_0', 'f16')) { throw 'KV_TYPE must be q8_0, q4_0, or f16.' }
foreach ($setting in @(@('MTP', $MTP), @('TOKEN_EMBEDDING_CPU', $TOKEN_EMBEDDING_CPU), @('FIT', $FIT))) {
    if ($setting[1] -notin @('on', 'off')) { throw "$($setting[0]) must be on or off." }
}
if (-not (Test-Path -LiteralPath $MODEL_PATH -PathType Leaf)) {
    throw "model not found: $MODEL_PATH"
}

$serverCommand = Get-Command $LLAMA_SERVER -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $serverCommand -and (Test-Path -LiteralPath $LLAMA_SERVER -PathType Leaf)) {
    $serverExecutable = (Resolve-Path -LiteralPath $LLAMA_SERVER).Path
} elseif ($serverCommand) {
    $serverExecutable = $serverCommand.Source
} else {
    throw "llama-server not found: $LLAMA_SERVER"
}

$serverArgs = @(
    '-m', $MODEL_PATH,
    '--alias', $MODEL_ALIAS,
    '--host', $BIND_ADDRESS,
    '--port', [string]$PORT,
    '-c', [string]$CTX,
    '-ngl', '99',
    '-fa', 'on',
    '-ctk', $KV_TYPE,
    '-ctv', $KV_TYPE,
    '--fit', $FIT,
    '--no-mmproj',
    '--parallel', '1',
    '--jinja',
    '--reasoning-format', 'auto',
    '--slots',
    '--metrics',
    '--temp', '0.6',
    '--top-p', '0.95',
    '--top-k', '20',
    '--min-p', '0.0'
)

if ($TOKEN_EMBEDDING_CPU -eq 'on') {
    $serverArgs += @('-ot', 'token_embd.weight=CPU')
}

if ($LOG_FILE) {
    $logDirectory = Split-Path -Parent $LOG_FILE
    if ($logDirectory) { New-Item -ItemType Directory -Force -Path $logDirectory | Out-Null }
    $serverArgs += @('--log-file', $LOG_FILE, '--log-colors', 'off')
}

if ($MTP -eq 'on') {
    $serverArgs += @(
        '--spec-type', 'draft-mtp',
        '--spec-draft-n-max', '2',
        '--spec-draft-type-k', $KV_TYPE,
        '--spec-draft-type-v', $KV_TYPE
    )
    if ($MTP_MODEL_PATH -and (Test-Path -LiteralPath $MTP_MODEL_PATH -PathType Leaf)) {
        $serverArgs += @('-md', $MTP_MODEL_PATH)
    } elseif ($MTP_MODEL_PATH) {
        Write-Warning "MTP requested but separate draft model is absent: $MTP_MODEL_PATH"
    }
}

Write-Host "Starting llama-server on http://${BIND_ADDRESS}:$PORT"
Write-Host "Model: $MODEL_PATH"
Write-Host "Context: $CTX; KV: $KV_TYPE; MTP: $MTP; token embedding on CPU: $TOKEN_EMBEDDING_CPU; fit: $FIT"
& $serverExecutable @serverArgs
exit $LASTEXITCODE
