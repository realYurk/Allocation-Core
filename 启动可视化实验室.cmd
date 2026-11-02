@echo off
cd /d "%~dp0"
if not exist "AllocationCore.exe" (
  echo AllocationCore.exe not found. Please extract the entire ZIP first.
  pause
  exit /b 1
)
"%~dp0AllocationCore.exe" serve -lab
echo.
echo The application has stopped. If the port is occupied, close the existing instance and retry.
pause
