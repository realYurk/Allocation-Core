@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
pushd "%~dp0.."
if errorlevel 1 goto PATH_ERROR
if not exist "logs" mkdir "logs"
set "EXE=alloc_windows_amd64.exe"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "EXE=alloc_windows_arm64.exe"
set "LATEST=logs\01_latest_result_folder.txt"
if not exist "%LATEST%" (echo No result recorded. Run 01_Windows_Run_Two_Stage.bat first. & popd & exit /b 1)
for /f "usebackq delims=" %%Q in ("%LATEST%") do set "RESULT_FOLDER=%%Q"
if not exist "%RESULT_FOLDER%\results.csv" (echo Cannot find results.csv in %RESULT_FOLDER% & popd & exit /b 1)
start "" "%RESULT_FOLDER%\results.csv"
echo Opened result CSV. The file is UTF-8 with Excel BOM.
popd
exit /b 0
