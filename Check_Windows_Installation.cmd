@echo off
setlocal EnableExtensions
cd /d "%~dp0"
set "PASS=1"
for %%F in ("studio_windows_amd64.exe" "studio_windows_arm64.exe" "core\alloc_windows_amd64.exe" "core\alloc_windows_arm64.exe" "Start_Windows.cmd" "studio-src\main.go" "core\cmd\alloc\main.go") do (
  if exist "%%~F" (echo [OK] %%~F) else (echo [MISSING] %%~F & set "PASS=0")
)
if "%PASS%"=="1" (
  echo.
  echo [PASS] Full Windows launcher and source are present.
) else (
  echo.
  echo [FAIL] Incomplete project files. Use the FULL PROJECT release ZIP.
)
pause
if "%PASS%"=="1" exit /b 0
exit /b 1
