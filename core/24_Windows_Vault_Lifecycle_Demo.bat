@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if not exist "%EXE%" goto ERROR
if not exist "v20_demo" mkdir "v20_demo"
echo [1/7] Run synthetic two-stage allocation...
"%EXE%" run "examples\manufacturing\rule.json" "v20_demo\source_run" --workers 2 --disk-partitions 4 --evidence-gzip
if errorlevel 1 goto ERROR
echo [2/7] Archive scenario FC-10+2 WIP-01...
"%EXE%" vault add "v20_demo\vault" "FC-10+2" "WIP-01" draft "v20_demo\source_run" > "v20_demo\draft_01.json"
if errorlevel 1 goto ERROR
echo [3/7] Archive another draft using the same data...
"%EXE%" vault add "v20_demo\vault" "FC-10+2" "WIP-02" draft "v20_demo\source_run" > "v20_demo\draft_02.json"
if errorlevel 1 goto ERROR
echo [4/7] Archive protected official version...
"%EXE%" vault add "v20_demo\vault" "FC-10+2" "FINAL" official "v20_demo\source_run" > "v20_demo\official.json"
if errorlevel 1 goto ERROR
echo [5/7] Inspect physical versus logical disk usage...
"%EXE%" vault stats "v20_demo\vault"
if errorlevel 1 goto ERROR
echo [6/7] PREVIEW cleanup only (NO DELETE)...
"%EXE%" vault prune "v20_demo\vault" --keep-drafts 1 --older-than-days 0
if errorlevel 1 goto ERROR
echo [7/7] List the archived versions...
"%EXE%" vault list "v20_demo\vault" > "v20_demo\versions.json"
if errorlevel 1 goto ERROR
echo.
echo SUCCESS. All run records are stored in v20_demo\versions.json.
echo No original results were deleted. No database was accessed.
start "" explorer "%CD%\v20_demo"
goto END
:ERROR
echo FAILED. Please check the error shown above.
:END
echo.
echo Press any key to close.
pause >nul
endlocal
