@echo off
setlocal EnableExtensions
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto ERROR
if not exist "v20_demo\vault" goto ERROR
echo DRY-RUN ONLY - NOTHING WILL BE DELETED.
"%EXE%" vault prune "v20_demo\vault" --keep-drafts 1 --older-than-days 0
if errorlevel 1 goto ERROR
echo.
echo To apply a cleanup, read docs\V20_新公司零基础使用手册.md first.
goto END
:ERROR
echo Missing EXE or demo vault. Run 24_Windows_Vault_Lifecycle_Demo.bat first.
:END
pause
endlocal
