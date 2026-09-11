[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$OutputPath,
    [ValidateRange(1, 120)]
    [int]$DurationSeconds = 75,
    [ValidateRange(25, 1000)]
    [int]$CursorSampleMilliseconds = 100
)

$ErrorActionPreference = 'Stop'
if (Test-Path -LiteralPath $OutputPath) {
    throw "OutputPath already exists: $OutputPath"
}

Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public static class VisibleStallNative {
    public delegate bool EnumWindowsProc(IntPtr hwnd, IntPtr lParam);
    [StructLayout(LayoutKind.Sequential)] public struct CURSORINFO {
        public int cbSize;
        public int flags;
        public IntPtr hCursor;
        public POINT ptScreenPos;
    }
    [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc callback, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int count);
    [DllImport("user32.dll")] public static extern bool GetCursorInfo(ref CURSORINFO cursorInfo);
    [DllImport("user32.dll")] public static extern IntPtr LoadCursor(IntPtr instance, IntPtr cursorName);
    [DllImport("user32.dll", SetLastError=true)] public static extern IntPtr SendMessageTimeout(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam, uint flags, uint timeout, out UIntPtr result);
}
'@

function Get-AgentWindows {
    $found = [Collections.Generic.List[object]]::new()
    $callback = [VisibleStallNative+EnumWindowsProc]{
        param([IntPtr]$handle, [IntPtr]$unused)
        if ([VisibleStallNative]::IsWindowVisible($handle)) {
            $buffer = [Text.StringBuilder]::new(512)
            $null = [VisibleStallNative]::GetWindowText($handle, $buffer, $buffer.Capacity)
            if ($buffer.ToString() -like 'Agent_b*') {
                $found.Add([pscustomobject]@{ handle = $handle; title = $buffer.ToString() })
            }
        }
        return $true
    }
    $null = [VisibleStallNative]::EnumWindows($callback, [IntPtr]::Zero)
    return @($found)
}

$existing = @(Get-AgentWindows | ForEach-Object { $_.handle.ToInt64() })
$watchStartedAt = [DateTime]::UtcNow
$window = $null
$deadline = [DateTime]::UtcNow.AddSeconds(25)
while (-not $window -and [DateTime]::UtcNow -lt $deadline) {
    $window = Get-AgentWindows | Where-Object { $existing -notcontains $_.handle.ToInt64() } | Select-Object -First 1
    if (-not $window) { Start-Sleep -Milliseconds 25 }
}
if (-not $window) { throw 'No newly launched Agent_b app window appeared within 25 seconds.' }

$hwnd = [IntPtr]$window.handle
$waitCursor = [VisibleStallNative]::LoadCursor([IntPtr]::Zero, [IntPtr]32514)
$appStartingCursor = [VisibleStallNative]::LoadCursor([IntPtr]::Zero, [IntPtr]32650)
$samples = [Collections.Generic.List[object]]::new()
$responses = [Collections.Generic.List[object]]::new()
$clock = [Diagnostics.Stopwatch]::StartNew()
$nextResponseProbe = 0.0
while ($clock.Elapsed.TotalSeconds -lt $DurationSeconds) {
    $info = [VisibleStallNative+CURSORINFO]::new()
    $info.cbSize = [Runtime.InteropServices.Marshal]::SizeOf([type][VisibleStallNative+CURSORINFO])
    $cursorRead = [VisibleStallNative]::GetCursorInfo([ref]$info)
    $handle = if ($cursorRead) { $info.hCursor } else { [IntPtr]::Zero }
    $samples.Add([pscustomobject]@{
        observed_at = [DateTime]::UtcNow.ToString('o')
        offset_ms = [Math]::Round($clock.Elapsed.TotalMilliseconds, 3)
        cursor_handle = $handle.ToInt64()
        cursor_visible = [bool]($cursorRead -and (($info.flags -band 1) -ne 0))
        wait = [bool]($cursorRead -and $handle -eq $waitCursor)
        app_starting = [bool]($cursorRead -and $handle -eq $appStartingCursor)
        x = $info.ptScreenPos.X
        y = $info.ptScreenPos.Y
    })
    if ($clock.Elapsed.TotalMilliseconds -ge $nextResponseProbe) {
        $result = [UIntPtr]::Zero
        $probe = [Diagnostics.Stopwatch]::StartNew()
        $ok = [VisibleStallNative]::SendMessageTimeout($hwnd, 0, [UIntPtr]::Zero, [IntPtr]::Zero, 0x0002, 100, [ref]$result) -ne [IntPtr]::Zero
        $probe.Stop()
        $responses.Add([pscustomobject]@{
            observed_at = [DateTime]::UtcNow.ToString('o')
            offset_ms = [Math]::Round($clock.Elapsed.TotalMilliseconds, 3)
            responsive = [bool]$ok
            probe_ms = [Math]::Round($probe.Elapsed.TotalMilliseconds, 3)
        })
        $nextResponseProbe += 1000
    }
    Start-Sleep -Milliseconds $CursorSampleMilliseconds
}

$busy = @($samples | Where-Object { $_.wait -or $_.app_starting })
$unresponsive = @($responses | Where-Object { -not $_.responsive })
$output = [pscustomobject]@{
    schema = 1
    watch_started_at = $watchStartedAt.ToString('o')
    window_found_at = if ($samples.Count) { $samples[0].observed_at } else { $null }
    window = [pscustomobject]@{ handle = $hwnd.ToInt64(); title = $window.title; excluded_existing_handles = $existing }
    cursor_reference_handles = [pscustomobject]@{ wait = $waitCursor.ToInt64(); app_starting = $appStartingCursor.ToInt64() }
    sampling = [pscustomobject]@{ duration_seconds = $DurationSeconds; cursor_interval_ms = $CursorSampleMilliseconds; responsiveness_interval_ms = 1000; responsiveness_timeout_ms = 100 }
    summary = [pscustomobject]@{
        sample_count = $samples.Count
        busy_sample_count = $busy.Count
        first_busy_at = if ($busy.Count) { $busy[0].observed_at } else { $null }
        last_busy_at = if ($busy.Count) { $busy[-1].observed_at } else { $null }
        unresponsive_probe_count = $unresponsive.Count
        first_unresponsive_at = if ($unresponsive.Count) { $unresponsive[0].observed_at } else { $null }
        last_unresponsive_at = if ($unresponsive.Count) { $unresponsive[-1].observed_at } else { $null }
    }
    cursor_samples = @($samples)
    responsiveness = @($responses)
}
$json = $output | ConvertTo-Json -Depth 8
$parent = Split-Path -Parent $OutputPath
if ($parent) { $null = New-Item -ItemType Directory -Path $parent -Force }
[IO.File]::WriteAllText($OutputPath, $json, [Text.UTF8Encoding]::new($false))
$output.summary | ConvertTo-Json -Compress
