@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
set "TARGET=%~dp0scripts\guard_26.cmd"
if not exist "%TARGET%" goto NOT_FOUND
cmd.exe /D /K call "%TARGET%"
echo Launcher finished. Press a key to close.
pause >nul
exit /b 0
:NOT_FOUND
echo ERROR: extract the whole ZIP first.
pause
exit /b 1
