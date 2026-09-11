[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$SourceDirectory,
    [string]$ApplicationDirectory = (Join-Path $env:ProgramFiles 'Agent_b'),
    [string]$DataDirectory,
    [string]$WorkspaceDirectory = (Join-Path $env:ProgramData 'Agent_b\workspace'),
    [string]$StartMenuDirectory = (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'),
    [string]$UninstallRegistryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Agent_b',
    [string]$OperatorSid,
    [string]$OperatorLocalAppData,
	[switch]$Alpha,
    [switch]$SkipBuild,
    [string]$SigningThumbprint,
    [switch]$TestMode,
    [string]$TranscriptPath
)

$ErrorActionPreference = 'Stop'
$displayVersion = '0.29.0'

function Get-FullPath {
    param([string]$Path)
    return [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Path)).TrimEnd('\')
}

function Stop-InstallTranscript {
    if (-not $script:installTranscriptStarted) { return }
    $savedWhatIf = $WhatIfPreference
    $WhatIfPreference = $false
    try { $null = Stop-Transcript } finally {
        $WhatIfPreference = $savedWhatIf
        $script:installTranscriptStarted = $false
    }
}

function Start-InstallTranscript {
    param([string]$Path)
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        $null = New-Item -ItemType Directory -Path $parent -Force
    }
    $savedWhatIf = $WhatIfPreference
    $WhatIfPreference = $false
    try {
        $null = Start-Transcript -LiteralPath $Path -Append
        $script:installTranscriptStarted = $true
    } finally { $WhatIfPreference = $savedWhatIf }
    Write-Host "Transcript: $Path"
}

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Add-CurrentUserCertificate {
    param(
        [Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [Security.Cryptography.X509Certificates.StoreName]$StoreName
    )
    $public = [Security.Cryptography.X509Certificates.X509Certificate2]::new($Certificate.RawData)
    $store = [Security.Cryptography.X509Certificates.X509Store]::new($StoreName, [Security.Cryptography.X509Certificates.StoreLocation]::CurrentUser)
    try {
        $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
        $store.Add($public)
    } finally {
        $store.Close()
        $public.Dispose()
    }
}

function Quote-ProcessArgument {
    param([string]$Value)
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Assert-SafeAgentBPath {
    param([string]$Path, [string]$Purpose)
    $full = Get-FullPath $Path
    $root = [IO.Path]::GetPathRoot($full).TrimEnd('\')
    if ($full -eq $root -or (Split-Path -Leaf $full) -notin @('Agent_b', 'workspace')) {
        throw "$Purpose must name a dedicated Agent_b or workspace directory: $full"
    }
    return $full
}

function Assert-SafeRegistryPath {
    param([string]$Path)
    $normalized = $Path.Replace('/', '\')
    $requiredPrefix = 'HKCU:\Software\'
    $leaf = $normalized.Substring($normalized.LastIndexOf('\') + 1)
    if (-not $normalized.StartsWith($requiredPrefix, [StringComparison]::OrdinalIgnoreCase) -or
        -not $leaf.StartsWith('Agent_b', [StringComparison]::Ordinal)) {
        throw "UninstallRegistryPath must be a dedicated Agent_b key below $requiredPrefix $Path"
    }
}

function Assert-TestPath {
    param([string]$Path)
    if (-not $TestMode) { return }
    $full = Get-FullPath $Path
    $temp = Get-FullPath ([IO.Path]::GetTempPath())
    if (-not $full.StartsWith($temp + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "TestMode paths must stay beneath the temporary directory: $full"
    }
}

function Test-PathInside {
    param([string]$Child, [string]$Parent)
    return $Child.StartsWith($Parent.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)
}

function Assert-DisjointRoots {
    param([string[]]$Roots)
    for ($left = 0; $left -lt $Roots.Count; $left++) {
        for ($right = $left + 1; $right -lt $Roots.Count; $right++) {
            if ($Roots[$left].Equals($Roots[$right], [StringComparison]::OrdinalIgnoreCase) -or
                (Test-PathInside $Roots[$left] $Roots[$right]) -or
                (Test-PathInside $Roots[$right] $Roots[$left])) {
                throw 'Application, operator-data, and workspace directories must be three disjoint trees.'
            }
        }
    }
}

function Find-Go {
    param([string]$Source)
    $local = Join-Path $Source '.tools\go\bin\go.exe'
    if (Test-Path -LiteralPath $local -PathType Leaf) { return $local }
    $command = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    return $null
}

function Get-InstalledProcesses {
    param([string]$Executable)
    $matches = @()
    foreach ($process in Get-Process -Name 'Agent_b' -ErrorAction SilentlyContinue) {
        try {
            if ((Get-FullPath $process.Path).Equals((Get-FullPath $Executable), [StringComparison]::OrdinalIgnoreCase)) {
                $matches += $process
            }
        } catch { }
    }
    return @($matches)
}

function Stop-InstalledProcesses {
    param([System.Diagnostics.Process[]]$Processes)
    if (-not $Processes.Count) { return }
    $taskkill = Join-Path $env:SystemRoot 'System32\taskkill.exe'
    foreach ($process in $Processes) {
        Write-Host "STOPPING: Agent_b PID $($process.Id)"
        & $taskkill /PID $process.Id
        if ($LASTEXITCODE -ne 0) {
            throw "Agent_b PID $($process.Id) could not be stopped gracefully (taskkill exit $LASTEXITCODE). Installation was not changed."
        }
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $remaining = @($Processes | Where-Object { Get-Process -Id $_.Id -ErrorAction SilentlyContinue })
        if (-not $remaining.Count) { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($remaining.Count) {
        throw "Agent_b PID(s) $(@($remaining.Id) -join ', ') did not exit after the graceful stop signal. Installation was not changed."
    }
    Write-Host "STOPPED: Agent_b PID(s) $(@($Processes.Id) -join ', ')"
}

function Copy-ProgramDirectory {
    param([string]$Name, [string]$Source, [string]$Destination)
    $from = Join-Path $Source $Name
    $to = Join-Path $Destination $Name
    if (-not (Test-Path -LiteralPath $from -PathType Container)) { throw "Required program directory is missing: $from" }
    if (-not (Test-Path -LiteralPath $to -PathType Container)) {
        $null = New-Item -ItemType Directory -Path $to -Force
    } else {
        foreach ($item in Get-ChildItem -LiteralPath $to -Force) {
            Remove-Item -LiteralPath $item.FullName -Recurse -Force
        }
    }
    foreach ($item in Get-ChildItem -LiteralPath $from -Force) {
        Copy-Item -LiteralPath $item.FullName -Destination $to -Recurse -Force
    }
}

function Set-PrivateDirectoryAcl {
    param([string]$Path, [Security.Principal.SecurityIdentifier]$Owner)
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagate = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    foreach ($sid in @(
        $Owner,
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::LocalSystemSid, $null),
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null)
    )) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $acl.SetOwner($Owner)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Set-ApplicationDirectoryAcl {
    param([string]$Path, [Security.Principal.SecurityIdentifier]$Owner)
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inherit = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $propagate = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow
    foreach ($sid in @(
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::LocalSystemSid, $null),
        [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null)
    )) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $users = [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinUsersSid, $null)
    $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($users, [Security.AccessControl.FileSystemRights]::ReadAndExecute, $inherit, $propagate, $allow))
    if ($TestMode) {
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($Owner, [Security.AccessControl.FileSystemRights]::FullControl, $inherit, $propagate, $allow))
    }
    $applicationOwner = if ($TestMode) { $Owner } else { [Security.Principal.SecurityIdentifier]::new([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null) }
    $acl.SetOwner($applicationOwner)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

$script:installTranscriptStarted = $false
$script:installTranscriptPath = $null
trap {
    $reason = $_.Exception.Message
    if (-not $script:installTranscriptStarted -and $script:installTranscriptPath) {
        try { Start-InstallTranscript -Path $script:installTranscriptPath } catch { }
    }
    Write-Host "INSTALLATION FAILED: $reason"
    if ($script:installTranscriptPath) { Write-Host "Transcript: $script:installTranscriptPath" }
    Stop-InstallTranscript
    exit 1
}

if ($env:OS -ne 'Windows_NT') { throw 'Agent_b installation is supported only on Windows.' }
if ([string]::IsNullOrWhiteSpace($SourceDirectory)) { $SourceDirectory = Split-Path -Parent $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($OperatorSid)) { $OperatorSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value }
if ([string]::IsNullOrWhiteSpace($OperatorLocalAppData)) { $OperatorLocalAppData = [Environment]::GetFolderPath('LocalApplicationData') }
if ([string]::IsNullOrWhiteSpace($DataDirectory)) { $DataDirectory = Join-Path $OperatorLocalAppData 'Agent_b' }
if ([string]::IsNullOrWhiteSpace($TranscriptPath)) {
    $TranscriptPath = Join-Path (Join-Path $DataDirectory 'logs') ("installer-{0}.log" -f [DateTime]::Now.ToString('yyyyMMdd-HHmmss-fff'))
}
$script:installTranscriptPath = Get-FullPath $TranscriptPath
Start-InstallTranscript -Path $script:installTranscriptPath

$sourceRoot = Get-FullPath $SourceDirectory
$applicationRoot = Assert-SafeAgentBPath $ApplicationDirectory 'ApplicationDirectory'
$dataRoot = Assert-SafeAgentBPath $DataDirectory 'DataDirectory'
$workspaceRoot = Assert-SafeAgentBPath $WorkspaceDirectory 'WorkspaceDirectory'
Assert-SafeRegistryPath $UninstallRegistryPath
Assert-TestPath $applicationRoot
Assert-TestPath $dataRoot
Assert-TestPath $workspaceRoot
Assert-DisjointRoots @($applicationRoot, $dataRoot, $workspaceRoot)
if ($sourceRoot.Equals($applicationRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'SourceDirectory and ApplicationDirectory must be different.' }
if (-not $TestMode) {
	if ($Alpha) {
		$expectedApplicationRoot = Get-FullPath 'C:\alpha\Program Files\Agent_b'
		$expectedDataRoot = Get-FullPath 'C:\alpha\LocalAppData\Agent_b'
		$expectedWorkspaceRoot = Get-FullPath 'C:\alpha\ProgramData\Agent_b\workspace'
	} else {
		$expectedApplicationRoot = Get-FullPath (Join-Path $env:ProgramFiles 'Agent_b')
		$expectedDataRoot = Get-FullPath (Join-Path $OperatorLocalAppData 'Agent_b')
		$expectedWorkspaceRoot = Get-FullPath (Join-Path $env:ProgramData 'Agent_b\workspace')
	}
    if (-not $applicationRoot.Equals($expectedApplicationRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "ApplicationDirectory must be the admin-protected Program Files location: $expectedApplicationRoot"
    }
    if (-not $dataRoot.Equals($expectedDataRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "DataDirectory must be the launching operator's LocalAppData Agent_b directory: $expectedDataRoot"
    }
    if (-not $workspaceRoot.Equals($expectedWorkspaceRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "WorkspaceDirectory must be the machine-scoped ProgramData location: $expectedWorkspaceRoot"
    }
}

try { $null = [Security.Principal.SecurityIdentifier]::new($OperatorSid) } catch {
    throw "OperatorSid is not a valid Windows SID: $OperatorSid"
}
$preflightConfigPath = Join-Path $dataRoot 'harness.json'
if (Test-Path -LiteralPath $preflightConfigPath -PathType Leaf) {
    try { $null = Get-Content -Raw -LiteralPath $preflightConfigPath | ConvertFrom-Json } catch {
        throw "Existing operator configuration is not valid JSON: $preflightConfigPath ($($_.Exception.Message))"
    }
}

$sourceBinary = Join-Path $sourceRoot 'Agent_b.exe'
$installedBinary = Join-Path $applicationRoot 'Agent_b.exe'
$go = Find-Go $sourceRoot
foreach ($directory in @('web', 'prompts', 'scripts', 'docs')) {
    $required = Join-Path $sourceRoot $directory
    if (-not (Test-Path -LiteralPath $required -PathType Container)) { throw "Required program directory is missing: $required" }
}
foreach ($file in @('harness.example.json', 'SECURITY.md', 'LICENSE', 'scripts\launch-installed.cmd')) {
    $required = Join-Path $sourceRoot $file
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "Required program file is missing: $required" }
}
if ($SkipBuild -and -not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
    throw "SkipBuild requires an existing binary: $sourceBinary"
}
if (-not $SkipBuild -and -not $go -and -not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
    throw 'Go 1.24 or newer was not found and Agent_b.exe has not already been built.'
}
$preflightProcesses = @(Get-InstalledProcesses $installedBinary)
if ($preflightProcesses.Count) {
    Write-Host "PREFLIGHT: running Agent_b PID(s) $(@($preflightProcesses.Id) -join ', ') will be stopped after elevation."
}
Write-Host 'PREFLIGHT COMPLETE'

if ((-not (Test-IsAdministrator) -or $PSVersionTable.PSEdition -ne 'Desktop') -and -not $WhatIfPreference -and -not $TestMode) {
    $arguments = @(
        '-NoLogo', '-NoProfile', '-File', $PSCommandPath,
        '-SourceDirectory', $sourceRoot,
        '-ApplicationDirectory', $applicationRoot,
        '-DataDirectory', $dataRoot,
        '-WorkspaceDirectory', $workspaceRoot,
        '-StartMenuDirectory', $StartMenuDirectory,
		'-UninstallRegistryPath', $UninstallRegistryPath,
		'-OperatorSid', $OperatorSid,
		'-OperatorLocalAppData', $OperatorLocalAppData,
        '-TranscriptPath', $script:installTranscriptPath
	)
	if ($Alpha) { $arguments += '-Alpha' }
	if ($SkipBuild) { $arguments += '-SkipBuild' }
    if ($SigningThumbprint) { $arguments += @('-SigningThumbprint', $SigningThumbprint) }
    $windowsPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    Stop-InstallTranscript
    if (Test-IsAdministrator) {
        & $windowsPowerShell @arguments
        exit $LASTEXITCODE
    }
    $process = Start-Process -FilePath $windowsPowerShell -ArgumentList (($arguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' ') -Verb RunAs -Wait -PassThru
    exit $process.ExitCode
}

$currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
if (-not $currentSid.Value.Equals($OperatorSid, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Installation refused: elevation changed identity from $OperatorSid to $($currentSid.Value). Use same-user UAC; over-the-shoulder administrator credentials would select the wrong LocalAppData and DPAPI owner."
}

Write-Host 'Agent_b admin-protected program installation with per-operator registration and data'
Write-Host "Application: $applicationRoot"
Write-Host "Operator data: $dataRoot"
Write-Host "Service workspace: $workspaceRoot"
Write-Host "Operator SID: $OperatorSid"

if ($WhatIfPreference) {
    Write-Host 'Mode: WhatIf; no build, file, ACL, shortcut, or registry change will be made.'
    $null = $PSCmdlet.ShouldProcess($sourceBinary, 'Build Agent_b')
    $null = $PSCmdlet.ShouldProcess($applicationRoot, 'Install or upgrade admin-only program files')
    $null = $PSCmdlet.ShouldProcess($dataRoot, 'Create or preserve private operator data')
    $null = $PSCmdlet.ShouldProcess($workspaceRoot, 'Create or preserve service workspace')
    Stop-InstallTranscript
    exit 0
}

$installedProcesses = @(Get-InstalledProcesses $installedBinary)
Stop-InstalledProcesses -Processes $installedProcesses

if ($SkipBuild) {
	Write-Host 'BUILD: using the commit-stamped binary supplied by deploy-alpha.ps1.'
} elseif ($go) {
	Write-Host "BUILD: $go"
	$git = Get-Command git.exe -ErrorAction SilentlyContinue
	$commit = $(if ($git) { & $git.Source -C $sourceRoot rev-parse HEAD 2>$null } else { '' })
	if ($git -and $LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($commit)) {
		$dirty = @(& $git.Source -C $sourceRoot status --porcelain --untracked-files=normal 2>$null).Count -gt 0
		$ldflags = "-X harness/internal/buildinfo.Commit=$($commit.Trim()) -X harness/internal/buildinfo.Dirty=$($dirty.ToString().ToLowerInvariant())"
		Push-Location $sourceRoot
		try { & $go build -ldflags $ldflags -o $sourceBinary ./cmd/harness } finally { Pop-Location }
	} else {
		Push-Location $sourceRoot
		try { & $go build -o $sourceBinary ./cmd/harness } finally { Pop-Location }
	}
    if ($LASTEXITCODE -ne 0) { throw "Agent_b build failed with exit code $LASTEXITCODE." }
} elseif (-not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
    throw 'Go 1.24 or newer was not found and Agent_b.exe has not already been built.'
} else {
    Write-Host 'BUILD: Go was not found; using the existing Agent_b.exe.'
}

$applicationCreated = -not (Test-Path -LiteralPath $applicationRoot -PathType Container)
$dataCreated = -not (Test-Path -LiteralPath $dataRoot -PathType Container)
$workspaceCreated = -not (Test-Path -LiteralPath $workspaceRoot -PathType Container)
$null = New-Item -ItemType Directory -Path $applicationRoot -Force
if ($applicationCreated) { Set-ApplicationDirectoryAcl -Path $applicationRoot -Owner $currentSid }
foreach ($directory in @('web', 'prompts', 'scripts', 'docs')) {
    Copy-ProgramDirectory -Name $directory -Source $sourceRoot -Destination $applicationRoot
}
foreach ($file in @('Agent_b.exe', 'harness.example.json', 'SECURITY.md', 'LICENSE')) {
    $from = Join-Path $sourceRoot $file
    if (-not (Test-Path -LiteralPath $from -PathType Leaf)) { throw "Required program file is missing: $from" }
    Copy-Item -LiteralPath $from -Destination (Join-Path $applicationRoot $file) -Force
}
Copy-Item -LiteralPath (Join-Path $sourceRoot 'scripts\launch-installed.cmd') -Destination (Join-Path $applicationRoot 'Agent_b.cmd') -Force

$null = New-Item -ItemType Directory -Path $dataRoot -Force
if ($dataCreated) { Set-PrivateDirectoryAcl -Path $dataRoot -Owner $currentSid }
foreach ($directory in @('logs', 'memory')) { $null = New-Item -ItemType Directory -Path (Join-Path $dataRoot $directory) -Force }
$null = New-Item -ItemType Directory -Path $workspaceRoot -Force
if ($workspaceCreated) { Set-PrivateDirectoryAcl -Path $workspaceRoot -Owner $currentSid }

$configPath = Join-Path $dataRoot 'harness.json'
$writeConfig = $false
if (Test-Path -LiteralPath $configPath -PathType Leaf) {
	Write-Host 'PRESERVED: existing operator configuration'
	$config = Get-Content -Raw -LiteralPath $configPath | ConvertFrom-Json
} else {
    $templatePath = Join-Path $applicationRoot 'harness.example.json'
    $config = Get-Content -Raw -LiteralPath $templatePath | ConvertFrom-Json
    $config.workspace = $workspaceRoot
    $config.log_dir = Join-Path $dataRoot 'logs'
	$config.memory.dir = Join-Path $dataRoot 'memory'
	$writeConfig = $true
	Write-Host 'CREATED: operator configuration from the installed template'
}
if ($Alpha) {
	$config.listen = '127.0.0.1:7337'
	$config.workspace = $workspaceRoot
	$config.log_dir = Join-Path $dataRoot 'logs'
	$config.memory.dir = Join-Path $dataRoot 'memory'
	$config.shell.service_account.enabled = $true
	$writeConfig = $true
}
if (-not $config.deliver) {
	$config | Add-Member -NotePropertyName deliver -NotePropertyValue ([pscustomobject]@{ mode = 'both'; exchange_folder = '%USERPROFILE%\Agent_b' })
	$writeConfig = $true
}
if ([string]::IsNullOrWhiteSpace([string]$config.deliver.mode)) { $config.deliver.mode = 'both'; $writeConfig = $true }
if ([string]::IsNullOrWhiteSpace([string]$config.deliver.exchange_folder)) { $config.deliver.exchange_folder = '%USERPROFILE%\Agent_b'; $writeConfig = $true }
$exchangeRoot = Get-FullPath ([string]$config.deliver.exchange_folder)
if (-not $TestMode -and -not $SigningThumbprint -and (-not $config.signing -or [string]::IsNullOrWhiteSpace([string]$config.signing.thumbprint))) {
	$SigningThumbprint = 'auto'
}
if ($SigningThumbprint -eq 'auto') {
	Import-Module PKI -ErrorAction Stop
	$certificate = Get-ChildItem -LiteralPath 'Cert:\LocalMachine\My' | Where-Object {
		$_.Subject -eq 'CN=Agent_b Operator Code Signing' -and $_.HasPrivateKey -and $_.NotAfter -gt [DateTime]::Now -and
		@($_.EnhancedKeyUsageList | Where-Object { ([string]$_.ObjectId) -eq '1.3.6.1.5.5.7.3.3' }).Count -gt 0
	} | Sort-Object NotAfter -Descending | Select-Object -First 1
	if ($certificate) {
		Write-Host "REUSED: administrator-gated signing certificate $($certificate.Thumbprint)"
	} else {
		$certificate = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=Agent_b Operator Code Signing' -CertStoreLocation 'Cert:\LocalMachine\My' -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 -KeyExportPolicy NonExportable -NotAfter ([DateTime]::Now.AddYears(3))
		Write-Host "CREATED: administrator-gated signing certificate $($certificate.Thumbprint)"
	}
	Add-CurrentUserCertificate -Certificate $certificate -StoreName TrustedPublisher
	Add-CurrentUserCertificate -Certificate $certificate -StoreName Root
	$SigningThumbprint = $certificate.Thumbprint
}
if ($SigningThumbprint) {
	if (-not $config.signing) { $config | Add-Member -NotePropertyName signing -NotePropertyValue ([pscustomobject]@{ thumbprint = ''; timestamp_url = 'http://timestamp.digicert.com' }) }
	$config.signing.thumbprint = ($SigningThumbprint -replace '[^0-9A-Fa-f]', '').ToUpperInvariant()
	$writeConfig = $true
}
if ($writeConfig) {
	[IO.File]::WriteAllText($configPath, ($config | ConvertTo-Json -Depth 100) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

# Re-sign every deployed signable artifact when the operator configured a
# Store-backed code-signing certificate. The PFX and private key never enter
# the installer; the certificate is resolved by thumbprint from the machine or user store.
if ($config.signing -and -not [string]::IsNullOrWhiteSpace([string]$config.signing.thumbprint)) {
	$thumbprint = ([string]$config.signing.thumbprint -replace '[^0-9A-Fa-f]', '').ToUpperInvariant()
	$certificate = Get-ChildItem -LiteralPath ("Cert:\LocalMachine\My\{0}" -f $thumbprint) -ErrorAction SilentlyContinue
	if (-not $certificate) { $certificate = Get-ChildItem -LiteralPath ("Cert:\CurrentUser\My\{0}" -f $thumbprint) -ErrorAction Stop }
	if (-not $certificate.HasPrivateKey -or @($certificate.EnhancedKeyUsageList | Where-Object { ([string]$_.ObjectId) -eq '1.3.6.1.5.5.7.3.3' }).Count -eq 0) {
		throw "Configured certificate $thumbprint is not a usable LocalMachine or CurrentUser code-signing certificate."
	}
	$signTargets = @($installedBinary) + @(Get-ChildItem -LiteralPath $applicationRoot -Filter '*.ps1' -File -Recurse | ForEach-Object FullName)
	foreach ($target in $signTargets) {
		$signature = Set-AuthenticodeSignature -LiteralPath $target -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer ([string]$config.signing.timestamp_url)
		if ($signature.Status -ne 'Valid') { throw "Signing failed for $target`: $($signature.Status) $($signature.StatusMessage)" }
	}
	Write-Host "SIGNED: Agent_b.exe and $($signTargets.Count - 1) PowerShell scripts with $thumbprint"
}

if ($Alpha -or ($config.shell.service_account -and [bool]$config.shell.service_account.enabled)) {
	$aclAccount = if ($config.shell.service_account.account) { [string]$config.shell.service_account.account } else { 'agentb-svc' }
	& (Join-Path $applicationRoot 'scripts\apply-acls.ps1') -AccountName $aclAccount -ApplicationDirectory $applicationRoot -DataDirectory $dataRoot -WorkspaceDirectory $workspaceRoot -ExchangeDirectory $exchangeRoot -NoPrompt -Confirm:$false
}

$iconPath = Join-Path $applicationRoot 'web\assets\Agent_b.ico'
if (-not (Test-Path -LiteralPath $iconPath -PathType Leaf)) { throw "Installed icon is missing: $iconPath" }
$null = New-Item -ItemType Directory -Path $StartMenuDirectory -Force
$shortcutPath = Join-Path $StartMenuDirectory 'Agent_b.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = Join-Path $applicationRoot 'Agent_b.cmd'
$shortcut.Arguments = $(if ($Alpha) { '-DataDirectory "' + $dataRoot + '"' } else { '' })
$shortcut.WorkingDirectory = $dataRoot
$shortcut.IconLocation = "$iconPath,0"
$shortcut.Description = 'Open Agent_b'
$shortcut.Save()

$uninstallScript = Join-Path $applicationRoot 'scripts\uninstall-Agent_b.ps1'
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$uninstallArguments = @(
    '-NoLogo', '-NoProfile', '-File', $uninstallScript,
    '-ApplicationDirectory', $applicationRoot,
    '-DataDirectory', $dataRoot,
    '-WorkspaceDirectory', $workspaceRoot,
    '-StartMenuDirectory', $StartMenuDirectory,
    '-UninstallRegistryPath', $UninstallRegistryPath,
    '-ExpectedOperatorSid', $OperatorSid,
    '-ExpectedOperatorLocalAppData', $OperatorLocalAppData
)
if ($Alpha) { $uninstallArguments += '-Alpha' }
if ($TestMode) { $uninstallArguments += '-TestMode' }
$uninstallCommand = (Quote-ProcessArgument $powershell) + ' ' + (($uninstallArguments | ForEach-Object { Quote-ProcessArgument $_ }) -join ' ')
$null = New-Item -Path $UninstallRegistryPath -Force
$properties = [ordered]@{
	DisplayName = $(if ($Alpha) { 'Agent_b Alpha' } else { 'Agent_b' })
    DisplayVersion = $displayVersion
    Publisher = 'rkclayton'
    DisplayIcon = $iconPath
    InstallLocation = $applicationRoot
    UninstallString = $uninstallCommand
    QuietUninstallString = "$uninstallCommand -Quiet"
    URLInfoAbout = 'https://github.com/rkclayton/AgentB'
    InstallDate = (Get-Date -Format 'yyyyMMdd')
    OperatorSid = $OperatorSid
    DataLocation = $dataRoot
    WorkspaceLocation = $workspaceRoot
}
foreach ($entry in $properties.GetEnumerator()) {
    $null = New-ItemProperty -Path $UninstallRegistryPath -Name $entry.Key -Value $entry.Value -PropertyType String -Force
}
$estimatedKB = [int][Math]::Ceiling(((Get-ChildItem -LiteralPath $applicationRoot -File -Recurse | Measure-Object Length -Sum).Sum) / 1KB)
$null = New-ItemProperty -Path $UninstallRegistryPath -Name EstimatedSize -Value $estimatedKB -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoModify -Value 1 -PropertyType DWord -Force
$null = New-ItemProperty -Path $UninstallRegistryPath -Name NoRepair -Value 1 -PropertyType DWord -Force

Write-Host ''
Write-Host 'INSTALLATION COMPLETE'
Write-Host "Start Menu: $shortcutPath"
Write-Host 'Registration: HKCU and the operator Start Menu, matching the LocalAppData configuration and user-scoped DPAPI owner.'
Write-Host 'Settings: created once in LocalAppData and preserved on upgrades'
Write-Host "Transcript: $script:installTranscriptPath"
Stop-InstallTranscript
