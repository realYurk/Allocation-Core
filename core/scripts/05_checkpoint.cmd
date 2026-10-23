@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
set "LOG=%CD%\logs\05_checkpoint_latest.log"
> "%LOG%" echo V12: start NEW durable stage checkpoints
if not exist "%EXE%" (>> "%LOG%" echo ERROR: missing EXE & goto ERROR)
if exist "v14_state\manufacturing_checkpoint\identity.json" (>> "%LOG%" echo ERROR: checkpoint already exists. Use 06_Windows_Resume_Checkpoint.bat. & goto ERROR)
set "OUT=v14_runs\checkpoint_demo_%RANDOM%_%RANDOM%"
"%EXE%" run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --disk-partitions 8 --checkpoint-dir "v14_state\manufacturing_checkpoint" >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
>> "%LOG%" echo SUCCESS: %OUT%
call :PRINT
popd
exit /b 0
:ERROR
>> "%LOG%" echo FAILED. Review log. Keep rule and inputs unchanged while resuming.
call :PRINT
popd
exit /b 1
:PATH_ERROR
echo ERROR: cannot access project folder. Fully extract ZIP to local directory.
exit /b 1
:PRINT
echo.
echo ========== EXECUTION LOG (UTF-8) ==========
type "%LOG%"
echo ==========================================
echo.
echo Saved: %LOG%
exit /b 0
