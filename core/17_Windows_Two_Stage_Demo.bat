@echo off
setlocal EnableExtensions
cd /d "%~dp0"
chcp 65001 >nul
if not exist "alloc_windows_amd64.exe" (
  echo ERROR: missing executable. Fully extract the ZIP.
  goto FAILED
)
if not exist "v16_runs" mkdir "v16_runs"
set "OUT=v16_runs\two_stage_%RANDOM%_%RANDOM%"
echo Running two-stage allocation with disk partitioning...
"alloc_windows_amd64.exe" run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --disk-partitions 4
if errorlevel 1 goto FAILED
"alloc_windows_amd64.exe" compare "examples\manufacturing\rule.json" "%OUT%" period,account
if errorlevel 1 goto FAILED
echo SUCCESS. Open comparison.csv, results.csv and reconciliation.csv in the output folder.
start "" explorer "%CD%\%OUT%"
goto END
:FAILED
echo Demo failed. Inspect the error above.
:END
pause
endlocal
