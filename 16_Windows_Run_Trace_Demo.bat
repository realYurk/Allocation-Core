@echo off
setlocal EnableExtensions
cd /d "%~dp0"
chcp 65001 >nul
if not exist "alloc_windows_amd64.exe" (
  echo ERROR: missing alloc_windows_amd64.exe. Please fully extract the ZIP.
  goto FAILED
)
if not exist "examples\split_merge\rule.json" (
  echo ERROR: demo rule not found. Please fully extract the ZIP.
  goto FAILED
)
if not exist "v16_runs" mkdir "v16_runs"
set "OUT=v16_runs\trace_%RANDOM%_%RANDOM%"
echo Running allocation: two driver paths merge to SKU...
"alloc_windows_amd64.exe" run "examples\split_merge\rule.json" "%OUT%" --workers 2 --disk-partitions 4
if errorlevel 1 goto FAILED
echo.
echo Tracing the first output SKU back to original source rows...
"alloc_windows_amd64.exe" trace-first "examples\split_merge\rule.json" "%OUT%" > "%OUT%\first_sku_trace.json"
if errorlevel 1 goto FAILED
echo.
echo SUCCESS: open "%OUT%\first_sku_trace.json" to inspect complete upstream connections.
echo Also review lineage_links.csv, ledger.csv, source_to_pool.csv and results.csv.
start "" explorer "%CD%\%OUT%"
goto END
:FAILED
echo Allocation demo failed. See the ERROR above.
:END
pause
endlocal
