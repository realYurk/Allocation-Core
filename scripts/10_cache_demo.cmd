@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 (echo ERROR: invalid installation path & exit /b 1)
if not exist "logs" mkdir "logs"
set "LOG=%CD%\logs\10_cache_latest.log"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" (echo ERROR: missing executable & popd & exit /b 1)
set "CACHE=v14_cache"
set "OUT1=v14_runs\cache_cold_%RANDOM%_%RANDOM%"
set "OUT2=v14_runs\cache_warm_%RANDOM%_%RANDOM%"
> "%LOG%" echo V14 cached driver index reproducibility test
>> "%LOG%" echo ==== FIRST RUN: BUILD INDEX ====
"%EXE%" run "examples\manufacturing\rule.json" "%OUT1%" --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir "%CACHE%" >> "%LOG%" 2>&1
if errorlevel 1 goto FAIL
>> "%LOG%" echo ==== SECOND RUN: REUSE INDEX ====
"%EXE%" run "examples\manufacturing\rule.json" "%OUT2%" --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir "%CACHE%" >> "%LOG%" 2>&1
if errorlevel 1 goto FAIL
>> "%LOG%" echo PASS: reusable cache written to %CACHE%
>> "%LOG%" echo Outputs: %OUT1% and %OUT2%
type "%LOG%"
echo.
echo The first run builds the cache; the second can reuse it.
echo This small demo is NOT a speed benchmark.
popd
exit /b 0
:FAIL
type "%LOG%"
echo.
echo FAILED. Read the log above.
popd
exit /b 1
