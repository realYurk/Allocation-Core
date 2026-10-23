@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "v19_runs\latest.txt" goto NO_RUN
for /f "usebackq delims=" %%A in ("v19_runs\latest.txt") do set "RUN_DIR=%%A"
if not defined RUN_DIR goto NO_RUN
echo Verifying latest compressed trace index: %RUN_DIR%
"%EXE%" trace-pack-verify "%RUN_DIR%"
if errorlevel 1 goto FAIL
echo VERIFIED: original evidence and V19 index are intact.
goto END
:NO_EXE
echo ERROR: Windows executable not found.
goto END
:NO_RUN
echo ERROR: Please run 22_Windows_Compressed_Trace_Demo.bat first.
goto END
:FAIL
echo ERROR: Integrity verification failed; do not trust this index.
:END
echo.
echo Press any key to close this window.
pause >nul
endlocal
