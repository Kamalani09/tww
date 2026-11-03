$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

if (Test-Path (Join-Path $ScriptDir ".go")) {
    $RootDir = $ScriptDir
} else {
    $RootDir = Split-Path -Parent $ScriptDir
}

$Go = Join-Path $RootDir ".go\go\bin\go.exe"
$DebugDir = Join-Path $RootDir "debugging"

if (-not (Test-Path $Go)) {
    Write-Error "Local Go binary not found at $Go"
    exit 1
}

Set-Location $DebugDir
Write-Host "Running Part 3 Debugging with Race Detector in $DebugDir ..." -ForegroundColor Cyan
& $Go run -race . events.jsonl > actual.txt
$diff = Compare-Object (Get-Content actual.txt) (Get-Content expected_output.txt)
if ($null -eq $diff) {
    Write-Host "`n========================================================" -ForegroundColor Green
    Write-Host "SUCCESS: Output matches expected_output.txt exactly!" -ForegroundColor Green
    Write-Host "========================================================`n" -ForegroundColor Green
} else {
    Write-Host "Differences found:" -ForegroundColor Red
    $diff
}
