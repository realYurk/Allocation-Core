@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
if errorlevel 1 (
  echo [ERROR] Cannot open application directory.
  pause
  exit /b 1
)
set "ARCH=amd64"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "ARCH=arm64"
set "STUDIO=studio_windows_%ARCH%.exe"
set "CORE=core\alloc_windows_%ARCH%.exe"
if not exist "%STUDIO%" goto missing
if not exist "%CORE%" goto missing

echo Allocation Studio / Allocation Core
echo Platform: Windows %ARCH%
echo Web: http://127.0.0.1:8866/designer
echo User data: %%LOCALAPPDATA%%\AllocationStudioData
echo.
echo Keep this window open while using Allocation Studio.
echo.
"%STUDIO%" -root "%CD%"
set "EXITCODE=%ERRORLEVEL%"
echo.
echo Allocation Studio exited, code %EXITCODE%.
if not "%EXITCODE%"=="0" echo [ERROR] Review the error above. Another application may occupy port 8866.
pause
exit /b %EXITCODE%

:missing
echo.
echo [ERROR] This installation is missing Windows executables:
if not exist "%STUDIO%" echo   Missing: %STUDIO%
if not exist "%CORE%" echo   Missing: %CORE%
echo.
echo This often means you downloaded GitHub source ZIP instead of compiled Portable ZIP.
echo Source code downloads do NOT contain compiled Windows EXE files.
echo Use the FULL PROJECT PACKAGE with executables, or download the Windows build artifact.
echo Install directory: %CD%
echo.
pause
exit /b 2
