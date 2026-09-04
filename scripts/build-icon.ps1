[CmdletBinding()]
param(
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'web\assets\Agent_b.ico'
}
Add-Type -AssemblyName System.Drawing

function New-RoundedPath {
    param([float]$X, [float]$Y, [float]$Width, [float]$Height, [float]$Radius)
    $path = [Drawing.Drawing2D.GraphicsPath]::new()
    $diameter = $Radius * 2
    $path.AddArc($X, $Y, $diameter, $diameter, 180, 90)
    $path.AddArc($X + $Width - $diameter, $Y, $diameter, $diameter, 270, 90)
    $path.AddArc($X + $Width - $diameter, $Y + $Height - $diameter, $diameter, $diameter, 0, 90)
    $path.AddArc($X, $Y + $Height - $diameter, $diameter, $diameter, 90, 90)
    $path.CloseFigure()
    return $path
}

function New-IconPng {
    param([int]$Size)
    $scale = $Size / 64.0
    $bitmap = [Drawing.Bitmap]::new($Size, $Size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.Clear([Drawing.Color]::Transparent)

    $bezel = [Drawing.SolidBrush]::new([Drawing.ColorTranslator]::FromHtml('#2A2E35'))
    $well = [Drawing.SolidBrush]::new([Drawing.ColorTranslator]::FromHtml('#15181C'))
    $mute = [Drawing.Pen]::new([Drawing.ColorTranslator]::FromHtml('#7D8794'), [Math]::Max(1.0, 3 * $scale))
    $trace = [Drawing.SolidBrush]::new([Drawing.ColorTranslator]::FromHtml('#F2B233'))
    $ink = [Drawing.Pen]::new([Drawing.ColorTranslator]::FromHtml('#D8DDE3'), [Math]::Max(1.0, 4 * $scale))
    $underscore = [Drawing.Pen]::new([Drawing.ColorTranslator]::FromHtml('#F2B233'), [Math]::Max(1.0, 4 * $scale))
    $ink.StartCap = $ink.EndCap = [Drawing.Drawing2D.LineCap]::Round
    $underscore.StartCap = $underscore.EndCap = [Drawing.Drawing2D.LineCap]::Round

    $outer = New-RoundedPath (3 * $scale) (3 * $scale) (58 * $scale) (58 * $scale) (13 * $scale)
    $screen = New-RoundedPath (9 * $scale) (12 * $scale) (46 * $scale) (38 * $scale) (6 * $scale)
    $leftEye = New-RoundedPath (16 * $scale) (21 * $scale) (10 * $scale) (10 * $scale) (2 * $scale)
    $rightEye = New-RoundedPath (38 * $scale) (21 * $scale) (10 * $scale) (10 * $scale) (2 * $scale)
    try {
        $graphics.FillPath($bezel, $outer)
        $graphics.FillPath($well, $screen)
        $graphics.DrawPath($mute, $screen)
        $graphics.FillPath($trace, $leftEye)
        $graphics.FillPath($trace, $rightEye)
        $graphics.DrawLine($ink, 18 * $scale, 40 * $scale, 46 * $scale, 40 * $scale)
        $graphics.DrawLine($underscore, 21 * $scale, 56 * $scale, 43 * $scale, 56 * $scale)
        $stream = [IO.MemoryStream]::new()
        $bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
        return $stream.ToArray()
    } finally {
        $outer.Dispose(); $screen.Dispose(); $leftEye.Dispose(); $rightEye.Dispose()
        $bezel.Dispose(); $well.Dispose(); $mute.Dispose(); $trace.Dispose(); $ink.Dispose(); $underscore.Dispose()
        $graphics.Dispose(); $bitmap.Dispose()
        if ($stream) { $stream.Dispose() }
    }
}

$sizes = @(16, 20, 24, 32, 40, 48, 64, 128, 256)
$images = @($sizes | ForEach-Object { [pscustomobject]@{ Size = $_; Bytes = New-IconPng $_ } })
$absoluteOutput = [IO.Path]::GetFullPath($OutputPath)
$null = New-Item -ItemType Directory -Path (Split-Path -Parent $absoluteOutput) -Force
$file = [IO.File]::Open($absoluteOutput, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
$writer = [IO.BinaryWriter]::new($file)
try {
    $writer.Write([UInt16]0)
    $writer.Write([UInt16]1)
    $writer.Write([UInt16]$images.Count)
    $offset = 6 + (16 * $images.Count)
    foreach ($image in $images) {
        $dimension = if ($image.Size -ge 256) { 0 } else { $image.Size }
        $writer.Write([byte]$dimension)
        $writer.Write([byte]$dimension)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]32)
        $writer.Write([UInt32]$image.Bytes.Length)
        $writer.Write([UInt32]$offset)
        $offset += $image.Bytes.Length
    }
    foreach ($image in $images) { $writer.Write([byte[]]$image.Bytes) }
} finally {
    $writer.Dispose()
    $file.Dispose()
}

Write-Host "Built Agent_b Windows icon: $absoluteOutput"
