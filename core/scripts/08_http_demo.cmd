@echo off
setlocal EnableExtensions DisableDelayedExpansion
chcp 65001 >nul
where curl.exe >nul 2>&1
if errorlevel 1 goto NO_CURL
echo.
echo [1] Service health:
curl.exe --fail --silent --show-error http://127.0.0.1:8787/healthz
if errorlevel 1 goto NO_SERVER
echo.
echo.
echo [2] Submit a two-stage allocation job:
curl.exe --fail --silent --show-error -X POST -H "Content-Type: application/json" -d "{\"rule\":\"manufacturing/rule.json\",\"workers\":2,\"disk_partitions\":8,\"driver_disk_index\":true,\"driver_cache\":true}" http://127.0.0.1:8787/v1/jobs
if errorlevel 1 goto NO_SERVER
echo.
echo.
echo [3] Visit http://127.0.0.1:8787/v1/jobs in a browser to see IDs.
echo GET /v1/jobs/ID/results.csv to download the UTF-8 Excel CSV file.
exit /b 0
:NO_CURL
echo ERROR: curl.exe not found. See docs\02_HTTP_Service.md for Python/Java options.
exit /b 1
:NO_SERVER
echo ERROR: service not running. Run 07_Windows_Start_HTTP_Server.bat FIRST.
exit /b 1
