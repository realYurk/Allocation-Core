@echo off
setlocal EnableExtensions DisableDelayedExpansion
rem UTF-8 console for Chinese stage names and JSON logs on Windows 10/11.
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "LOG=%CD%\logs\00_diagnostic_latest.log"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
> "%LOG%" echo Allocation Core V14 Windows diagnostic
>> "%LOG%" echo Working directory: %CD%
>> "%LOG%" echo PROCESSOR_ARCHITECTURE: %PROCESSOR_ARCHITECTURE%
>> "%LOG%" echo PROCESSOR_ARCHITEW6432: %PROCESSOR_ARCHITEW6432%
>> "%LOG%" echo OS version:
ver >> "%LOG%" 2>&1
if not exist "%EXE%" goto NO_EXE
if not exist "examples\manufacturing\rule.json" goto NO_RULE
>> "%LOG%" echo Testing executable: %EXE%
"%EXE%" validate "examples\manufacturing\rule.json" >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
"%EXE%" graph "examples\manufacturing\rule.json" >> "%LOG%" 2>&1
if errorlevel 1 goto ERROR
>> "%LOG%" echo Diagnostic: PASS
call :PRINT
popd
exit /b 0
:NO_EXE
>> "%LOG%" echo ERROR: missing executable %EXE%. Extract ALL files from ZIP.
goto ERROR
:NO_RULE
>> "%LOG%" echo ERROR: missing examples\manufacturing\rule.json. Extract ALL files from ZIP.
goto ERROR
:ERROR
>> "%LOG%" echo Diagnostic: FAILED. Check software restriction policy, antivirus and Windows architecture.
call :PRINT
popd
exit /b 1
:PATH_ERROR
echo ERROR: cannot open the program folder. Extract ZIP to a normal local folder.
exit /b 1
:PRINT
echo.
echo ========== DIAGNOSTIC LOG ==========
type "%LOG%"
echo ====================================
echo.
echo Log file: %LOG%
echo If launch fails, please send this log for debugging.
exit /b 0
