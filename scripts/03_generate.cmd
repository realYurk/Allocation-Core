@echo off
setlocal EnableExtensions DisableDelayedExpansion
rem UTF-8 console for Chinese stage names and JSON logs on Windows 10/11.
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "LOG=%CD%\logs\03_generate_latest.log"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
> "%LOG%" echo Allocation Core V14 new-company starter generation
if not exist "%EXE%" goto NO_EXE
set "OUT=v14_runs\starter_%RANDOM%_%RANDOM%"
"%EXE%" generate "examples\manufacturing\rule.json" "%OUT%" >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
>> "%LOG%" echo SUCCESS: starter folder is %OUT%
call :PRINT
popd
exit /b 0
:NO_EXE
>> "%LOG%" echo ERROR: missing %EXE%.
goto ERROR
:ERROR
>> "%LOG%" echo FAILED: check the log and run 00_Windows_Diagnostic.bat.
call :PRINT
popd
exit /b 1
:PATH_ERROR
echo ERROR: cannot access project folder.
exit /b 1
:PRINT
echo.
echo ========== PROGRAM OUTPUT ==========
type "%LOG%"
echo ===================================
echo.
echo Log: %LOG%
exit /b 0
