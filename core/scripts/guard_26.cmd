@echo off
cd /d "%~dp0.."
alloc_windows_amd64.exe capacity "examples\capacity_safe_budget.json" "%CD%\v_runs"
echo.
echo Admission check finished.
