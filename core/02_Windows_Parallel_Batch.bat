@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
rem Keep a second interactive command shell open, even after errors.
set "TARGET=%~dp0scripts\02_batch.cmd"
if not exist "%TARGET%" goto NOT_FOUND
cmd.exe /D /K call "%TARGET%"
echo.
echo Launcher finished. Press a key to close this original window.
pause >nul
exit /b 0
:NOT_FOUND
echo ERROR: %TARGET% does not exist. Extract the entire ZIP first.
pause
exit /b 1
