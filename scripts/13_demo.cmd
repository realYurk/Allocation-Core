@echo off
setlocal EnableExtensions DisableDelayedExpansion
cd /d "%~dp0.."
chcp 65001 >nul
echo Allocation Core V15 -- 13 -- Two_Stages
if not exist "alloc_windows_amd64.exe" (echo ERROR: Missing EXE. & pause & exit /b 2)
if not exist "logs" mkdir "logs"
set "RULE=examples\manufacturing\rule.json"
set "OUT=v15_runs\manufacturing_%RANDOM%_%RANDOM%"
echo 1/4 Rule validation and CSV column checks...
"%~dp0..\alloc_windows_amd64.exe" doctor "%RULE%" >"logs\v15_latest.log" 2>&1
if errorlevel 1 goto failed
type "logs\v15_latest.log"
echo 2/4 Explain allocation chain...
"%~dp0..\alloc_windows_amd64.exe" explain "%RULE%" >>"logs\v15_latest.log" 2>&1
if errorlevel 1 goto failed
echo 3/4 Execute...
"%~dp0..\alloc_windows_amd64.exe" run "%RULE%" "%OUT%" --workers 2 >>"logs\v15_latest.log" 2>&1
if errorlevel 1 goto failed
echo 4/4 Create comparison.csv for account / period...
"%~dp0..\alloc_windows_amd64.exe" compare "%RULE%" "%OUT%" account,period >>"logs\v15_latest.log" 2>&1
if errorlevel 1 goto failed
echo PASS. The run finished without errors.
echo Files: "%OUT%\results.csv"  and  "%OUT%\comparison.csv"
echo Source results and ledger are in the same directory.
echo Detailed logs: logs\v15_latest.log
pause
exit /b 0
:failed
echo ERROR: Check logs\v15_latest.log
type "logs\v15_latest.log"
pause
exit /b 1
