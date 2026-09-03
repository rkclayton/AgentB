[CmdletBinding()]
param(
    [string]$MODEL_PATH = 'E:\Models\Qwen3.8-27B\Qwen3.8-27B-UD-Q3_K_XL.gguf',
    [ValidateRange(1024, 1048576)]
    [int]$CTX = 32768,
    [ValidateSet('q8_0', 'q4_0', 'f16')]
    [string]$KV_TYPE = 'q8_0',
    [ValidateRange(1, 65535)]
    [int]$PORT = 8080,
    [string]$BIND_ADDRESS = '127.0.0.1',
    [ValidateSet('on', 'off')]
    [string]$MTP = 'off',
    [ValidateSet('on', 'off')]
    [string]$TOKEN_EMBEDDING_CPU = 'off',
    [ValidateSet('on', 'off')]
    [string]$FIT = 'off',
    [string]$LLAMA_SERVER = 'E:\llama\llama-server.exe',
    [string]$MTP_MODEL_PATH = 'E:\Models\Qwen3.8-27B\MTP\mtp-Qwen3.8-27B-Q4_0.gguf',
    [string]$LOG_FILE = ''
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $LLAMA_SERVER -PathType Leaf)) {
    throw "llama-server not found: $LLAMA_SERVER"
}
if (-not (Test-Path -LiteralPath $MODEL_PATH -PathType Leaf)) {
    throw "model not found: $MODEL_PATH"
}

$serverArgs = @(
    '-m', $MODEL_PATH,
    '--alias', 'qwen3.8-27b',
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
    if (Test-Path -LiteralPath $MTP_MODEL_PATH -PathType Leaf) {
        $serverArgs += @('-md', $MTP_MODEL_PATH)
    } else {
        Write-Warning "MTP requested but separate draft model is absent: $MTP_MODEL_PATH"
    }
}

Write-Host "Starting llama-server on http://${BIND_ADDRESS}:$PORT"
Write-Host "Model: $MODEL_PATH"
Write-Host "Context: $CTX; KV: $KV_TYPE; MTP: $MTP; token embedding on CPU: $TOKEN_EMBEDDING_CPU; fit: $FIT"
& $LLAMA_SERVER @serverArgs
exit $LASTEXITCODE
