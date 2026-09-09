[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ReplayPath,
    [Parameter(Mandatory = $true)]
    [string]$EvidenceDirectory,
    [string]$SourceDirectory,
    [switch]$SkipBuild,
    [ValidateSet('reproduced', 'fixed')]
    [string]$Expected = 'reproduced'
)

$ErrorActionPreference = 'Stop'
$sourceRoot = Split-Path -Parent $PSScriptRoot
$applicationSource = if ([string]::IsNullOrWhiteSpace($SourceDirectory)) { $sourceRoot } else { $SourceDirectory }
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('Agent_b-v0170-menu-' + [Guid]::NewGuid().ToString('N'))
$application = Join-Path $testRoot 'Application\Agent_b'
$data = Join-Path $testRoot 'LocalAppData\Agent_b'
$workspace = Join-Path $testRoot 'ProgramData\Agent_b\workspace'
$startMenu = Join-Path $testRoot 'Start Menu\Programs'
$registry = 'HKCU:\Software\Agent_bV0170Menu-' + [Guid]::NewGuid().ToString('N') + '\Agent_b'

if (Test-Path -LiteralPath $EvidenceDirectory) {
    throw "EvidenceDirectory already exists: $EvidenceDirectory"
}

try {
    $installArguments = @{
        SourceDirectory = $applicationSource
        ApplicationDirectory = $application
        DataDirectory = $data
        WorkspaceDirectory = $workspace
        StartMenuDirectory = $startMenu
        UninstallRegistryPath = $registry
        TestMode = $true
    }
    if ($SkipBuild) { $installArguments.SkipBuild = $true }
    & (Join-Path $PSScriptRoot 'install-Agent_b.ps1') @installArguments
    if ($null -ne $LASTEXITCODE -and $LASTEXITCODE -ne 0) {
        throw "Disposable install failed with exit code $LASTEXITCODE."
    }

    & (Get-Command node.exe -ErrorAction Stop).Source `
        (Join-Path $PSScriptRoot 'chat-menu-replay-diagnostic.mjs') `
        '--app' $application `
        '--data' $data `
        '--replay' $ReplayPath `
        '--evidence' $EvidenceDirectory `
        '--expected' $Expected
    if ($LASTEXITCODE -ne 0) { throw "Chat menu replay diagnostic failed with exit code $LASTEXITCODE." }
} finally {
    if (Test-Path -LiteralPath $registry) { Remove-Item -LiteralPath $registry -Recurse -Force }
    $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    $resolvedTest = [IO.Path]::GetFullPath($testRoot)
    if ($resolvedTest.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedTest) -like 'Agent_b-v0170-menu-*' -and
        (Test-Path -LiteralPath $resolvedTest)) {
        Remove-Item -LiteralPath $resolvedTest -Recurse -Force
    }
}
