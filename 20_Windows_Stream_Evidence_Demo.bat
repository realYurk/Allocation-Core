@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "examples\split_merge\rule.json" goto NO_RULE
if not exist "v18_runs" mkdir "v18_runs"
set "OUT=v18_runs\stream_%RANDOM%_%RANDOM%"
echo [1/3] Run split / merge allocation with direct GZIP audit writing...
"%EXE%" run "examples\split_merge\rule.json" "%OUT%" --workers 4 --disk-partitions 4 --evidence-gzip
if errorlevel 1 goto ERROR
 echo [2/3] Verify ALL six result/evidence files and their SHA-256 hashes...
"%EXE%" evidence-verify "%OUT%"
if errorlevel 1 goto ERROR
 echo [3/3] Success. Check results.csv and ledger.csv.gz
 echo Output: %OUT%
start "" explorer "%CD%\%OUT%"
goto END
:NO_EXE
echo ERROR: Windows executable not found. Extract the ZIP completely.
goto END
:NO_RULE
echo ERROR: Example rule missing. Extract the ZIP completely.
goto END
:ERROR
echo ERROR: Calculation or audit verification failed. See details above.
:END
 echo.
 echo The window will remain open. Press any key to close it.
pause >nul
endlocal
