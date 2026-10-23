@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
set "TARGET=%~dp0scripts\06_resume.cmd"
if not exist "%TARGET%" goto NOT_FOUND
cmd.exe /D /K call "%TARGET%"
echo.
echo Press any key to close this window.
pause >nul
exit /b 0
:NOT_FOUND
echo ERROR: %TARGET% missing. Extract full ZIP.
pause
exit /b 1
