function Assert-RemovalWithinAllowedRoots {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string[]]$AllowedRoots,
        [string]$Purpose = 'removal'
    )
    if ([string]::IsNullOrWhiteSpace($Path) -or -not $AllowedRoots.Count) {
        throw "Refusing $Purpose without a path and explicit allowed removal roots."
    }
    $full = [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd('\')
    foreach ($rootPath in $AllowedRoots) {
        if ([string]::IsNullOrWhiteSpace($rootPath)) { continue }
        $root = [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($rootPath)).TrimEnd('\')
        if ($full.Equals($root, [StringComparison]::OrdinalIgnoreCase) -or
            $full.StartsWith($root + '\', [StringComparison]::OrdinalIgnoreCase)) {
            return $full
        }
    }
    throw "Refusing $Purpose outside allowed removal roots: $full"
}
