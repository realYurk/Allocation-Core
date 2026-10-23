@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto NO_EXE
if not exist "examples\split_merge\rule.json" goto NO_RULE
if not exist "v18_runs" mkdir "v18_runs"
set "OUT=v18_runs\restore_%RANDOM%_%RANDOM%"
echo [1/4] Calculate compressed evidence...
"%EXE%" run "examples\split_merge\rule.json" "%OUT%" --workers 2 --disk-partitions 4 --evidence-gzip
if errorlevel 1 goto ERROR
echo [2/4] Verify and losslessly restore original CSV into a new folder...
"%EXE%" evidence-expand "%OUT%" "%OUT%_raw"
if errorlevel 1 goto ERROR
echo [3/4] Build on-disk trace index for the restored CSVs...
"%EXE%" trace-index "%OUT%_raw"
if errorlevel 1 goto ERROR
echo [4/4] Trace the first final item back to its sources...
"%EXE%" trace-first "examples\split_merge\rule.json" "%OUT%_raw" > "%OUT%_raw\trace_first.json"
if errorlevel 1 goto ERROR
echo SUCCESS: %OUT%_raw\trace_first.json
start "" explorer "%CD%\%OUT%_raw"
goto END
:NO_EXE
echo ERROR: Windows executable not found. Extract the ZIP completely.
goto END
:NO_RULE
echo ERROR: Example rule missing. Extract the ZIP completely.
goto END
:ERROR
echo ERROR: Operation failed. No input files were modified.
:END
 echo.
 echo The window will remain open. Press any key to close it.
pause >nul
endlocal
