@echo off
setlocal EnableExtensions
chcp 65001 >nul
cd /d "%~dp0"
set "CORE_EXE=core\alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "CORE_EXE=core\alloc_windows_arm64.exe"
if not exist "%CORE_EXE%" (
 echo ERROR: missing %CORE_EXE%. Unzip the complete release first.
 pause
 exit /b 2
)
if not exist "runs" mkdir "runs"
echo Allocation Core native Windows launcher
if /I "%~1"=="serve" goto serve
if /I "%~1"=="help" (
 "%CORE_EXE%" --help
 goto end
)
if /I not "%~1"=="" (
 "%CORE_EXE%" %*
 goto end
)
echo Step 1 of 2: validate the sample rule
"%CORE_EXE%" doctor "core\examples\manufacturing\rule.json"
if errorlevel 1 goto failed
echo Step 2 of 2: calculate the complete two-stage allocation
"%CORE_EXE%" run "core\examples\manufacturing\rule.json" "runs\windows_smoke" --workers 2 --evidence-gzip
if errorlevel 1 goto failed
echo PASS. Results in runs\windows_smoke\results.csv
goto end
:serve
"%CORE_EXE%" serve --root "core\examples" --store "service_data" --listen "127.0.0.1:8787" --jobs 2
goto end
:failed
echo FAILED. Copy the text in this window for diagnosis.
:end
echo.
pause
ENDLOCAL
