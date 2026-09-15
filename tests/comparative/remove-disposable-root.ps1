[CmdletBinding()]
param([Parameter(Mandatory)][string]$Path)

$ErrorActionPreference = 'Stop'
. (Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'scripts\removal-guard.ps1')

$full = [IO.Path]::GetFullPath($Path).TrimEnd('\')
$temporary = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
if (-not $full.StartsWith($temporary + '\Agent_b-eval-', [StringComparison]::OrdinalIgnoreCase)) {
    throw "Comparative cleanup refused a non-eval temporary root: $full"
}
$removalPath = Assert-RemovalWithinAllowedRoots -Path $full -AllowedRoots @($temporary) -Purpose 'comparative disposable cleanup'
if (Test-Path -LiteralPath $removalPath) {
    Remove-Item -LiteralPath $removalPath -Recurse -Force
}
