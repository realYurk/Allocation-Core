@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "examples\split_merge\rule.json" goto NO_RULE
if not exist "v17_runs" mkdir "v17_runs"
set "OUT=v17_runs\trace_%RANDOM%_%RANDOM%"
echo [1/4] Running split-and-merge example...
"%EXE%" run "examples\split_merge\rule.json" "%OUT%" --workers 2 --disk-partitions 4
if errorlevel 1 goto FAILURE
echo [2/4] Building read-only trace index...
"%EXE%" trace-index "%OUT%"
if errorlevel 1 goto FAILURE
echo [3/4] Verifying full CSV and index SHA-256...
"%EXE%" trace-index-verify "%OUT%"
if errorlevel 1 goto FAILURE
echo [4/4] Tracing the first final row...
"%EXE%" trace-first "examples\split_merge\rule.json" "%OUT%" > "%OUT%\trace_first.json"
if errorlevel 1 goto FAILURE
echo SUCCESS: %OUT%\trace_first.json
start "" explorer "%CD%\%OUT%"
goto DONE
:NO_EXE
echo ERROR: missing executable. Fully extract this ZIP first.
goto DONE
:NO_RULE
echo ERROR: missing example rule. Fully extract this ZIP first.
goto DONE
:FAILURE
echo FAILED: see message above. No original input data was changed.
:DONE
pause
endlocal
