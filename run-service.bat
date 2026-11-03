@echo off
set "ROOT=%~dp0"
set "GO=%ROOT%.go\go\bin\go.exe"
if not exist "%GO%" (
    echo Error: Local Go binary not found at %GO%
    pause
    exit /b 1
)
cd /d "%ROOT%starter"
echo Starting Campaign Events Service on http://localhost:8080 ...
"%GO%" run .
pause

