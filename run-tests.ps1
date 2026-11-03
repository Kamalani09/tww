$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# Find project root directory
if (Test-Path (Join-Path $ScriptDir ".go")) {
    $RootDir = $ScriptDir
} else {
    $RootDir = Split-Path -Parent $ScriptDir
}

$Go = Join-Path $RootDir ".go\go\bin\go.exe"
$StarterDir = Join-Path $RootDir "starter"

if (-not (Test-Path $Go)) {
    Write-Error "Local Go binary not found at $Go"
    exit 1
}

Set-Location $StarterDir
Write-Host "Running test suite with race detector in $StarterDir ..." -ForegroundColor Cyan
& $Go test -v -race ./...
