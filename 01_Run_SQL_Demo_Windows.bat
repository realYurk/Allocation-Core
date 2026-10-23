@echo off
setlocal
cd /d "%~dp0"
where py >nul 2>&1
if %errorlevel%==0 (
  py -3 integration\python\sqlite_full_run.py --workspace demo_workspace --reset-demo
) else (
  where python >nul 2>&1
  if %errorlevel%==0 (
    python integration\python\sqlite_full_run.py --workspace demo_workspace --reset-demo
  ) else (
    echo Python was not found. This SQL teaching demo needs Python stdlib.
    echo The core calculator still runs without Python: 00_Run_Core_Windows.bat
  )
)
echo.
pause
