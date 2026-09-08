[CmdletBinding()]
param(
    [switch]$RealModel,
    [string]$RealModelUrl,
    [string]$RealModelName,
    [string]$EvidenceDirectory
)

$ErrorActionPreference = 'Stop'
$sourceRoot = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('Agent_b-chat-acceptance-' + [Guid]::NewGuid().ToString('N'))
$application = Join-Path $testRoot 'Application\Agent_b'
$data = Join-Path $testRoot 'LocalAppData\Agent_b'
$workspace = Join-Path $testRoot 'ProgramData\Agent_b\workspace'
$startMenu = Join-Path $testRoot 'Start Menu\Programs'
$registry = 'HKCU:\Software\Agent_bChatAcceptance-' + [Guid]::NewGuid().ToString('N') + '\Agent_b'
$evidence = if (-not [string]::IsNullOrWhiteSpace($EvidenceDirectory)) {
    $EvidenceDirectory
} elseif (-not [string]::IsNullOrWhiteSpace($env:AGENTB_CHAT_ACCEPTANCE_EVIDENCE)) {
    $env:AGENTB_CHAT_ACCEPTANCE_EVIDENCE
} else {
    Join-Path $sourceRoot 'logs\evidence\2026-09-08-v0.13.2-chat-suite'
}

try {
    & (Join-Path $PSScriptRoot 'install-Agent_b.ps1') `
        -SourceDirectory $sourceRoot `
        -ApplicationDirectory $application `
        -DataDirectory $data `
        -WorkspaceDirectory $workspace `
        -StartMenuDirectory $startMenu `
        -UninstallRegistryPath $registry `
        -TestMode
    if ($LASTEXITCODE -ne 0) { throw "Disposable install failed with exit code $LASTEXITCODE." }

    $arguments = @(
        (Join-Path $PSScriptRoot 'chat-acceptance.mjs'),
        '--app', $application,
        '--data', $data,
        '--workspace', $workspace,
        '--evidence', $evidence
    )
    if ($RealModel) {
        if ([string]::IsNullOrWhiteSpace($RealModelUrl) -or [string]::IsNullOrWhiteSpace($RealModelName)) {
            throw '-RealModel requires -RealModelUrl and -RealModelName.'
        }
        $arguments += @('--real-model-url', $RealModelUrl, '--real-model-name', $RealModelName)
    }
    & (Get-Command node.exe -ErrorAction Stop).Source @arguments
    if ($LASTEXITCODE -ne 0) { throw "Chat acceptance failed with exit code $LASTEXITCODE." }
} finally {
    if (Test-Path -LiteralPath $registry) { Remove-Item -LiteralPath $registry -Recurse -Force }
    $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolvedTest = [IO.Path]::GetFullPath($testRoot)
    if ($resolvedTest.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTest) -like 'Agent_b-chat-acceptance-*' -and
        (Test-Path -LiteralPath $resolvedTest)) {
        Remove-Item -LiteralPath $resolvedTest -Recurse -Force
    }
}
