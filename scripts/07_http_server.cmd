@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "LOG=%CD%\logs\07_http_latest.log"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "examples\manufacturing\rule.json" goto NO_RULE
echo.
echo Allocation Core V14 HTTP service (local machine only)
echo Open a SECOND window and run 08_Windows_Submit_HTTP_Demo.bat
echo GET http://127.0.0.1:8787/healthz
echo Stop the HTTP server with Ctrl+C.
echo.
"%EXE%" serve --root "examples" --store "v14_service_data" --listen "127.0.0.1:8787" --jobs 2 1>>"%LOG%" 2>&1
set "EXITCODE=%ERRORLEVEL%"
type "%LOG%"
echo.
echo HTTP server stopped (exit code %EXITCODE%). Log: %LOG%
popd
exit /b %EXITCODE%
:NO_EXE
echo ERROR: missing executable. Extract the entire ZIP.
pause
popd
exit /b 1
:NO_RULE
echo ERROR: missing examples. Extract the entire ZIP.
pause
popd
exit /b 1
:PATH_ERROR
echo ERROR: cannot change directory.
pause
exit /b 1
