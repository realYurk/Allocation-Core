@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "v17_runs" mkdir "v17_runs"
set "OUT=v17_runs\audit_%RANDOM%_%RANDOM%"
set "PACK=%OUT%_audit.zip"
echo [1/3] Calculating manufacturing example...
"%EXE%" run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --disk-partitions 4
if errorlevel 1 goto FAILED
echo [2/3] Packing all calculation evidence without deleting originals...
"%EXE%" audit-pack "%OUT%" "%PACK%"
if errorlevel 1 goto FAILED
echo [3/3] Checking all archived original bytes with SHA-256...
"%EXE%" audit-verify "%PACK%"
if errorlevel 1 goto FAILED
echo SUCCESS: %PACK%
echo Original results were kept in %OUT%.
start "" explorer "%CD%\v17_runs"
goto DONE
:NO_EXE
echo ERROR: missing executable. Fully extract this ZIP first.
goto DONE
:FAILED
echo FAILED: see message above. Original result files were kept.
:DONE
pause
endlocal
