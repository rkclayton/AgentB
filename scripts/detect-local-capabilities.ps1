[CmdletBinding()]
param([string]$ServiceAccount = 'agentb-svc')

$ErrorActionPreference = 'Stop'

function Get-Vendor([string]$Text) {
    if ($Text -match 'NVIDIA') { return 'NVIDIA' }
    if ($Text -match 'AMD|Advanced Micro Devices|Radeon') { return 'AMD' }
    if ($Text -match 'Intel') { return 'Intel' }
    if ($Text -match 'Apple') { return 'Apple' }
    return $(if ([string]::IsNullOrWhiteSpace($Text)) { 'Unknown' } else { $Text.Trim() })
}

function Find-Executable([string]$Name, [string[]]$Candidates) {
    $found = [Collections.Generic.List[string]]::new()
    foreach ($command in @(Get-Command $Name -All -ErrorAction SilentlyContinue)) {
        if ($command.Source -and $command.Source -notlike '*\Microsoft\WindowsApps\*' -and (Test-Path -LiteralPath $command.Source -PathType Leaf)) { $found.Add([IO.Path]::GetFullPath($command.Source)) }
    }
    foreach ($candidate in $Candidates) {
        foreach ($item in @(Get-Item -Path $candidate -ErrorAction SilentlyContinue)) {
            if (-not $item.PSIsContainer -and $item.FullName -notlike '*\Microsoft\WindowsApps\*') { $found.Add($item.FullName) }
        }
    }
    return @($found | Sort-Object -Unique)
}

$serviceSids = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
$serviceAccountFound = $false
try {
    $serviceSid = ([Security.Principal.NTAccount]::new($ServiceAccount)).Translate([Security.Principal.SecurityIdentifier]).Value
    $null = $serviceSids.Add($serviceSid)
    $serviceAccountFound = $true
} catch { }
foreach ($sid in @('S-1-1-0', 'S-1-5-11', 'S-1-5-32-545')) { $null = $serviceSids.Add($sid) }

function Test-ServiceExecute([string]$Path) {
    if (-not $serviceAccountFound) { return $false }
    try {
        $required = [Security.AccessControl.FileSystemRights]::ReadAndExecute
        $allowed = [Security.AccessControl.FileSystemRights]0
        $rules = (Get-Acl -LiteralPath $Path).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])
        foreach ($rule in $rules) {
            if (-not $serviceSids.Contains($rule.IdentityReference.Value)) { continue }
            $covered = $rule.FileSystemRights -band $required
            if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Deny -and $covered -ne 0) { return $false }
            if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow) { $allowed = $allowed -bor $covered }
        }
        return (($allowed -band $required) -eq $required)
    } catch { return $false }
}

$systemMemory = [uint64](Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory
$gpus = [Collections.Generic.List[object]]::new()
foreach ($adapter in @(Get-CimInstance Win32_VideoController -ErrorAction SilentlyContinue)) {
    $vendor = Get-Vendor ([string]$adapter.AdapterCompatibility + ' ' + [string]$adapter.Name)
    $vram = if ($null -ne $adapter.AdapterRAM) { [uint64]$adapter.AdapterRAM } else { [uint64]0 }
    $unified = [uint64]0
    if ($vendor -eq 'Apple' -or ($vendor -eq 'Intel' -and $vram -le 2GB) -or ($vendor -eq 'AMD' -and [string]$adapter.Name -match 'Radeon Graphics' -and $vram -le 4GB)) {
        $unified = $systemMemory
    }
    $gpus.Add([ordered]@{ vendor=$vendor; name=[string]$adapter.Name; vram_bytes=$vram; unified_memory_bytes=$unified })
}

$nvidiaSmi = @(Find-Executable 'nvidia-smi.exe' @(
    "$env:ProgramFiles\NVIDIA Corporation\NVSMI\nvidia-smi.exe",
    "$env:SystemRoot\System32\nvidia-smi.exe"
)) | Select-Object -First 1
if ($nvidiaSmi) {
    $nonNvidia = @($gpus | Where-Object { $_.vendor -ne 'NVIDIA' })
    $gpus.Clear()
    foreach ($item in $nonNvidia) { $gpus.Add($item) }
    foreach ($line in @(& $nvidiaSmi --query-gpu=name,memory.total --format=csv,noheader,nounits 2>$null)) {
        $name, $memoryMiB = $line -split ',', 2
        $parsed = [uint64]0
        $null = [uint64]::TryParse($memoryMiB.Trim(), [ref]$parsed)
        $gpus.Add([ordered]@{ vendor='NVIDIA'; name=$name.Trim(); vram_bytes=($parsed * 1MB); unified_memory_bytes=[uint64]0 })
    }
}

$serverSpecs = @(
    [ordered]@{ name='Ollama'; processes=@('ollama'); install_url='https://ollama.com/download'; default_url='http://127.0.0.1:11434/v1' },
    [ordered]@{ name='LM Studio'; processes=@('LM Studio','lmstudio','llmster'); install_url='https://lmstudio.ai/download'; default_url='http://127.0.0.1:1234/v1' },
    [ordered]@{ name='llama-server'; processes=@('llama-server'); install_url='https://github.com/ggml-org/llama.cpp/releases'; default_url='http://127.0.0.1:8080' }
)
$servers = foreach ($spec in $serverSpecs) {
    $running = $false
    foreach ($processName in $spec.processes) { if (Get-Process -Name $processName -ErrorAction SilentlyContinue) { $running = $true } }
    [ordered]@{ name=$spec.name; running=$running; install_url=$spec.install_url; default_url=$spec.default_url }
}

$programFilesX86 = ${env:ProgramFiles(x86)}
$interpreterSpecs = @(
    [ordered]@{ name='python'; command='python.exe'; candidates=@("$env:LOCALAPPDATA\Programs\Python\Python*\python.exe", "$env:ProgramFiles\Python*\python.exe") },
    [ordered]@{ name='node'; command='node.exe'; candidates=@("$env:ProgramFiles\nodejs\node.exe", "$env:LOCALAPPDATA\Programs\nodejs\node.exe") },
    [ordered]@{ name='go'; command='go.exe'; candidates=@("$env:ProgramFiles\Go\bin\go.exe", "$env:LOCALAPPDATA\Programs\Go\bin\go.exe") },
    [ordered]@{ name='dotnet'; command='dotnet.exe'; candidates=@("$env:ProgramFiles\dotnet\dotnet.exe", $(if ($programFilesX86) { "$programFilesX86\dotnet\dotnet.exe" })) }
)
$interpreters = foreach ($spec in $interpreterSpecs) {
    $candidates = @($spec.candidates | Where-Object { $_ })
    foreach ($path in @(Find-Executable $spec.command $candidates)) {
        $service = Test-ServiceExecute $path
        [ordered]@{ name=$spec.name; path=$path; service_access=$service; availability=$(if ($service) { 'runs as service' } else { 'operator-only' }) }
    }
}

$recommendationMemory = [uint64]0
foreach ($gpu in $gpus) {
    $available = [Math]::Max([uint64]$gpu.vram_bytes, [uint64]$gpu.unified_memory_bytes)
    if ($available -gt $recommendationMemory) { $recommendationMemory = $available }
}

[ordered]@{
    supported=$true
    service_account=$ServiceAccount
    service_account_found=$serviceAccountFound
    system_memory_bytes=$systemMemory
    recommendation_memory_bytes=$recommendationMemory
    gpus=@($gpus)
    servers=@($servers)
    interpreters=@($interpreters)
} | ConvertTo-Json -Depth 8 -Compress
