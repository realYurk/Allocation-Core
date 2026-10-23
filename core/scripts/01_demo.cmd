@echo off
setlocal EnableExtensions DisableDelayedExpansion
rem UTF-8 console for Chinese stage names and JSON logs on Windows 10/11.
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "LOG=%CD%\logs\01_demo_latest.log"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
> "%LOG%" echo Allocation Core V14 two-stage, disk partitioned demo
if not exist "%EXE%" goto NO_EXE
if not exist "examples\manufacturing\rule.json" goto NO_RULE
"%EXE%" validate "examples\manufacturing\rule.json" >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
set "OUT=v14_runs\manufacturing_%RANDOM%_%RANDOM%"
"%EXE%" run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --disk-partitions 8 >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
>> "%LOG%" echo SUCCESS: output folder is %OUT%
> "logs\01_latest_result_folder.txt" echo %CD%\%OUT%
call :PRINT
popd
exit /b 0
:NO_EXE
>> "%LOG%" echo ERROR: %EXE% not found. Extract the whole ZIP before running.
goto ERROR
:NO_RULE
>> "%LOG%" echo ERROR: demo rule not found. Extract the whole ZIP before running.
goto ERROR
:ERROR
>> "%LOG%" echo FAILED: inspect this log and run 00_Windows_Diagnostic.bat.
call :PRINT
popd
exit /b 1
:PATH_ERROR
echo ERROR: cannot access the project folder.
exit /b 1
:PRINT
echo.
echo ========== PROGRAM OUTPUT ==========
type "%LOG%"
echo ===================================
echo.
echo Log: %LOG%
exit /b 0
