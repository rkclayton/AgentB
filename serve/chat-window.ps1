param([string]$SessionId = 'main')

$url = "http://127.0.0.1:8790/chat?session=$([Uri]::EscapeDataString($SessionId))"
$candidates = @(
  "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
  "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
  "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe",
  "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe"
)
foreach ($browser in $candidates) {
  if (Test-Path -LiteralPath $browser) {
    Start-Process -FilePath $browser -ArgumentList "--app=$url", '--window-size=520,760'
    exit 0
  }
}
Start-Process $url
