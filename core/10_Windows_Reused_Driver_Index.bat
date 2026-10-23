@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
set "TARGET=%~dp0scripts\10_cache_demo.cmd"
if not exist "%TARGET%" goto ERROR
cmd.exe /D /K call "%TARGET%"
echo.
echo Log: logs\10_cache_latest.log
pause
exit /b 0
:ERROR
echo Extract the entire ZIP to a local folder before running.
pause
exit /b 1
