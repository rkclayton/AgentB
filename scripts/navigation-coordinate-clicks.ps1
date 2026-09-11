[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Url,
    [Parameter(Mandatory)]
    [string]$OffsetsJson,
    [ValidateRange(1, 90)]
    [int]$PostCadenceSeconds = 70
)

$ErrorActionPreference = 'Stop'
$hwnd = [IntPtr]::Zero
trap {
    $caught = $_
    if ($hwnd -ne [IntPtr]::Zero) {
        try { $null = [CoordinateInputNative]::PostMessage($hwnd, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) } catch { }
    }
    throw $caught
}

Add-Type @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public static class CoordinateInputNative {
    public delegate bool EnumWindowsProc(IntPtr hwnd, IntPtr lParam);
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc callback, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int count);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hwnd);
    [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr hwnd, int x, int y, int width, int height, bool repaint);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hwnd, int command);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hwnd, IntPtr insertAfter, int x, int y, int width, int height, uint flags);
    [DllImport("user32.dll")] public static extern IntPtr WindowFromPoint(POINT point);
    [DllImport("user32.dll")] public static extern IntPtr GetAncestor(IntPtr hwnd, uint flags);
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extraInfo);
    [DllImport("user32.dll", SetLastError=true)] public static extern IntPtr SendMessageTimeout(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam, uint flags, uint timeout, out UIntPtr result);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd, uint message, IntPtr wParam, IntPtr lParam);
}
'@

function Get-VisibleWindows {
    $found = [Collections.Generic.List[object]]::new()
    $callback = [CoordinateInputNative+EnumWindowsProc]{
        param([IntPtr]$handle, [IntPtr]$unused)
        if ([CoordinateInputNative]::IsWindowVisible($handle)) {
            $buffer = [Text.StringBuilder]::new(512)
            $null = [CoordinateInputNative]::GetWindowText($handle, $buffer, $buffer.Capacity)
            if ($buffer.Length -gt 0) {
                $found.Add([pscustomobject]@{ Handle = $handle; Title = $buffer.ToString() })
            }
        }
        return $true
    }
    $null = [CoordinateInputNative]::EnumWindows($callback, [IntPtr]::Zero)
    return @($found)
}

function Get-AgentTabBounds([IntPtr]$WindowHandle) {
    Add-Type -AssemblyName UIAutomationClient
    Add-Type -AssemblyName UIAutomationTypes
    $root = [Windows.Automation.AutomationElement]::FromHandle($WindowHandle)
    $all = $root.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
    $candidates = [Collections.Generic.List[object]]::new()
    for ($index = 0; $index -lt $all.Count; $index++) {
        $element = $all.Item($index)
        $name = [string]$element.Current.Name
        if ($name -like '*agent*') {
            $candidateRect = $element.Current.BoundingRectangle
            $candidates.Add([pscustomobject]@{ name = $name; left = $candidateRect.Left; top = $candidateRect.Top; right = $candidateRect.Right; bottom = $candidateRect.Bottom; control_type = [string]$element.Current.ControlType.ProgrammaticName })
        }
        if ($name -clike 'agent_b*' -and $element.Current.ControlType.ProgrammaticName -eq 'ControlType.Button') {
            $rect = $element.Current.BoundingRectangle
            if ($rect.Right -gt $rect.Left -and $rect.Bottom -gt $rect.Top -and ($rect.Right - $rect.Left) -le 150 -and ($rect.Bottom - $rect.Top) -le 50) {
                return [pscustomobject]@{
                    method = 'uia-bounding-rectangle'
                    name = $name
                    left = [int]$rect.Left
                    top = [int]$rect.Top
                    right = [int]$rect.Right
                    bottom = [int]$rect.Bottom
                }
            }
        }
    }
    throw "UI Automation did not expose the agent_b tab. Candidates: $($candidates | ConvertTo-Json -Compress -Depth 4)"
}

$edgeCandidates = @(
    $(if (${env:ProgramFiles(x86)}) { Join-Path ${env:ProgramFiles(x86)} 'Microsoft\Edge\Application\msedge.exe' }),
    $(if ($env:ProgramFiles) { Join-Path $env:ProgramFiles 'Microsoft\Edge\Application\msedge.exe' })
) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) }
$edge = $edgeCandidates | Select-Object -First 1
if (-not $edge) { throw 'Microsoft Edge executable was not found.' }

$before = @(Get-VisibleWindows | ForEach-Object { $_.Handle.ToInt64() })
$startedAt = [DateTime]::UtcNow
Start-Process -FilePath $edge -ArgumentList "--app=$Url"

$window = $null
$deadline = [DateTime]::UtcNow.AddSeconds(20)
while (-not $window -and [DateTime]::UtcNow -lt $deadline) {
    $window = Get-VisibleWindows | Where-Object {
        $_.Title -like 'Agent_b*' -and $before -notcontains $_.Handle.ToInt64()
    } | Select-Object -First 1
    if (-not $window) { Start-Sleep -Milliseconds 50 }
}
if (-not $window) { throw 'The newly launched Agent_b Edge app window was not found.' }

$hwnd = [IntPtr]$window.Handle
$null = [CoordinateInputNative]::ShowWindow($hwnd, 9)
$null = [CoordinateInputNative]::MoveWindow($hwnd, 40, 40, 1250, 975, $true)
$null = [CoordinateInputNative]::SetWindowPos($hwnd, [IntPtr](-1), 40, 40, 1250, 975, 0x0040)
$null = [CoordinateInputNative]::SetWindowPos($hwnd, [IntPtr](-2), 40, 40, 1250, 975, 0x0040)
$foregrounded = [CoordinateInputNative]::SetForegroundWindow($hwnd)
Start-Sleep -Milliseconds 300
$bounds = $null
$boundsError = $null
$boundsDeadline = [DateTime]::UtcNow.AddSeconds(10)
while (-not $bounds -and [DateTime]::UtcNow -lt $boundsDeadline) {
    try { $bounds = Get-AgentTabBounds $hwnd } catch { $boundsError = $_; Start-Sleep -Milliseconds 100 }
}
if (-not $bounds) { throw "UI Automation did not expose the agent_b tab within 10 seconds. Last result: $boundsError" }
$x = [int](($bounds.left + $bounds.right) / 2)
$y = [int](($bounds.top + $bounds.bottom) / 2)
$point = [CoordinateInputNative+POINT]::new()
$point.X = $x
$point.Y = $y
$rootAtPoint = [CoordinateInputNative]::GetAncestor([CoordinateInputNative]::WindowFromPoint($point), 2)
if ($rootAtPoint -ne $hwnd) { throw "Resolved coordinate belongs to window $($rootAtPoint.ToInt64()), expected $($hwnd.ToInt64())." }
$parsedOffsets = ConvertFrom-Json -InputObject $OffsetsJson
$offsets = @()
foreach ($value in $parsedOffsets) { $offsets += [double]$value }
if (-not $offsets.Count) { throw 'At least one dispatch offset is required.' }

$dispatches = [Collections.Generic.List[object]]::new()
Start-Sleep -Seconds 1
$clock = [Diagnostics.Stopwatch]::StartNew()
foreach ($intended in $offsets) {
    while ($clock.Elapsed.TotalMilliseconds -lt $intended) {
        $remaining = $intended - $clock.Elapsed.TotalMilliseconds
        if ($remaining -gt 2) { [Threading.Thread]::Sleep(1) }
    }
    $began = $clock.Elapsed.TotalMilliseconds
    $cursor = [CoordinateInputNative]::SetCursorPos($x, $y)
    [CoordinateInputNative]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)
    [CoordinateInputNative]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)
    $finished = $clock.Elapsed.TotalMilliseconds
    $dispatches.Add([pscustomobject]@{
        intended_offset_ms = [Math]::Round($intended, 3)
        dispatch_started_offset_ms = [Math]::Round($began, 3)
        dispatch_finished_offset_ms = [Math]::Round($finished, 3)
        lag_ms = [Math]::Round($began - $intended, 3)
        cursor_positioned = [bool]$cursor
    })
}

$responsiveness = [Collections.Generic.List[object]]::new()
$settleClock = [Diagnostics.Stopwatch]::StartNew()
while ($settleClock.Elapsed.TotalSeconds -lt $PostCadenceSeconds) {
    $result = [UIntPtr]::Zero
    $probe = [Diagnostics.Stopwatch]::StartNew()
    $ok = [CoordinateInputNative]::SendMessageTimeout($hwnd, 0, [UIntPtr]::Zero, [IntPtr]::Zero, 0x0002, 1000, [ref]$result) -ne [IntPtr]::Zero
    $probe.Stop()
    $responsiveness.Add([pscustomobject]@{
        offset_after_cadence_ms = [Math]::Round($settleClock.Elapsed.TotalMilliseconds, 3)
        responsive = $ok
        probe_ms = [Math]::Round($probe.Elapsed.TotalMilliseconds, 3)
    })
    Start-Sleep -Seconds 1
}

$null = [CoordinateInputNative]::PostMessage($hwnd, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
[pscustomobject]@{
    schema = 1
    launched_at = $startedAt.ToString('o')
    launch = [pscustomobject]@{ executable = $edge; arguments = @("--app=$Url"); remote_debugging = $false }
    window = [pscustomobject]@{ handle = $hwnd.ToInt64(); initial_title = $window.Title; moved_to = @(40, 40, 1250, 975); foreground_request_succeeded = $foregrounded; coordinate_root_handle = $rootAtPoint.ToInt64() }
    coordinate = [pscustomobject]@{ x = $x; y = $y; source = $bounds }
    dispatches = @($dispatches)
    responsiveness = @($responsiveness)
} | ConvertTo-Json -Depth 8 -Compress
