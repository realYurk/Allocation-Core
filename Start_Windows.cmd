@echo off
setlocal
chcp 65001 >nul
cd /d "%~dp0"
set "EXEC=studio_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXEC=studio_windows_arm64.exe"
if not exist "%EXEC%" (
 echo [ERROR] Missing studio launcher: %EXEC%
 echo Please extract the entire ZIP to a folder before running.
 pause
 exit /b 1
)
if not exist "core\alloc_windows_amd64.exe" if not exist "core\alloc_windows_arm64.exe" (
 echo [ERROR] The Allocation Core executable was not found.
 pause
 exit /b 1
)
echo [Allocation Studio] Starting local dashboard...
echo [Allocation Studio] Default URL: http://127.0.0.1:8866/designer
echo [Allocation Studio] Keep this window open while using the dashboard.
"%EXEC%"
echo.
echo The dashboard has stopped. See the message above if it closed unexpectedly.
pause
