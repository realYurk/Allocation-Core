@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto ERROR
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
set "OUT=v14_runs\driver_index_%RANDOM%_%RANDOM%"
"%EXE%" run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --disk-partitions 8 --driver-disk-index
if errorlevel 1 goto ERROR
echo.
echo PASS: result folder: %OUT%
echo Check results.csv and reconciliation.csv
popd
exit /b 0
:ERROR
echo ERROR: run failed. Check all ZIP contents and logs.
pause
popd
exit /b 1
