@echo off
setlocal
cd /d "%~dp0"
echo.
echo Allocation Core V21.1 -- Capacity Preflight
 echo Core EXE needs no Python. Preflight SDK tool is for developer workstations.
echo.
echo Run from terminal with Python installed:
echo python integration\tools\capacity_preflight.py --pools 3600 --targets-per-pool 500000 --disk-path C:\AllocationRuns
 echo This is a conservative planning estimate, NOT an OS quota.
echo Read docs\05_V21_1_Audit_Fixes_CN.md before any large run.
echo.
pause
