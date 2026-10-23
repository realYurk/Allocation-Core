@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
set "TARGET=%~dp0scripts\07_http_server.cmd"
if not exist "%TARGET%" goto NOT_FOUND
cmd.exe /D /K call "%TARGET%"
echo Server launcher exited. Press any key to close this window.
pause >nul
exit /b 0
:NOT_FOUND
echo ERROR: missing script. Extract whole ZIP.
pause
exit /b 1
