@echo off
cd /d "%~dp0.."
set "ALLOC_CAPACITY_POLICY=%CD%\examples\capacity_safe_budget.json"
alloc_windows_amd64.exe serve --root "examples" --store "service_data_guarded" --listen "127.0.0.1:8788" --budget-file "%ALLOC_CAPACITY_POLICY%" --jobs 2
