@echo off
set "ROOT=%~dp0"
set "GO=%ROOT%.go\go\bin\go.exe"
if not exist "%GO%" (
    echo Error: Local Go binary not found at %GO%
    pause
    exit /b 1
)
cd /d "%ROOT%debugging"
echo Running Part 3 Debugging with Race Detector...
"%GO%" run -race . events.jsonl > actual.txt
echo Comparing with expected_output.txt...
fc actual.txt expected_output.txt
if %ERRORLEVEL% equ 0 (
    echo.
    echo ========================================================
    echo SUCCESS: Output matches expected_output.txt exactly!
    echo ========================================================
) else (
    echo.
    echo Differences detected!
)
pause

