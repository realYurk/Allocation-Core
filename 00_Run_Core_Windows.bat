@echo off
setlocal
cd /d "%~dp0core"
call "01_Windows_Run_Two_Stage.bat"
echo.
echo If the calculation did not work, see the log and core documentation.
pause
