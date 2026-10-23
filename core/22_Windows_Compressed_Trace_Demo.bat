@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
if not exist "v19_runs" mkdir "v19_runs"
if not exist "logs" mkdir "logs"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "examples\split_merge\rule.json" goto NO_RULE
set "RUN_DIR=v19_runs\trace_%RANDOM%_%RANDOM%"
echo [1/4] Allocating split-and-merge case with compressed evidence...
"%EXE%" run "examples\split_merge\rule.json" "%RUN_DIR%" --workers 4 --disk-partitions 4 --evidence-gzip
if errorlevel 1 goto FAIL
echo [2/4] Building V19 compressed trace index without writing plaintext ledger...
"%EXE%" trace-pack-index "%RUN_DIR%" > "%RUN_DIR%\packed_receipt.json"
if errorlevel 1 goto FAIL
echo [3/4] Querying complete trace directly from compressed blocks...
"%EXE%" trace-first "examples\split_merge\rule.json" "%RUN_DIR%" > "%RUN_DIR%\trace_first.json"
if errorlevel 1 goto FAIL
echo [4/4] Verifying hashes and compressed evidence integrity...
"%EXE%" trace-pack-verify "%RUN_DIR%"
if errorlevel 1 goto FAIL
> "v19_runs\latest.txt" echo %RUN_DIR%
echo.
echo SUCCESS. Open trace_first.json to see BOTH parent routes.
echo Output directory: %RUN_DIR%
start "" explorer "%CD%\%RUN_DIR%"
goto END
:NO_EXE
echo ERROR: No Windows EXE. Extract the entire ZIP before running.
goto END
:NO_RULE
echo ERROR: Example files missing. Extract the entire ZIP.
goto END
:FAIL
echo ERROR: A command failed. See the error above. No claim of success.
:END
echo.
echo This window stays open. Press any key to close it.
pause >nul
endlocal
