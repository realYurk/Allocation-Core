@echo off
cd /d "%~dp0.."
set "ALLOC_CAPACITY_POLICY=%CD%\examples\capacity_safe_budget.json"
set "OUT=%CD%\v_runs\protected_%RANDOM%_%RANDOM%"
alloc_windows_amd64.exe run "examples\manufacturing\rule.json" "%OUT%" --workers 4 --evidence-gzip
if errorlevel 1 (echo ERROR: calculation failed.& exit /b 1)
echo PASS: %OUT%
