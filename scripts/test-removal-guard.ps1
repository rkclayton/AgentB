[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'removal-guard.ps1')

$disposable = Join-Path ([IO.Path]::GetTempPath()) ('Agent_b-removal-guard-' + [Guid]::NewGuid().ToString('N'))
$allowed = Join-Path $disposable 'child.txt'
if ((Assert-RemovalWithinAllowedRoots -Path $allowed -AllowedRoots @($disposable) -Purpose 'test cleanup') -ne [IO.Path]::GetFullPath($allowed)) {
    throw 'Allowed disposable child did not resolve to its full path.'
}

$outside = Join-Path $env:ProgramFiles 'PowerShell\7'
$refused = $false
try {
    $null = Assert-RemovalWithinAllowedRoots -Path $outside -AllowedRoots @($disposable) -Purpose 'test cleanup'
} catch {
    $refused = $_.Exception.Message -eq "Refusing test cleanup outside allowed removal roots: $([IO.Path]::GetFullPath($outside))"
}
if (-not $refused) { throw "Outside removal path was not refused: $outside" }

Write-Host 'PASS: removal guard admits a disposable child and refuses Program Files outside the allow-list'
