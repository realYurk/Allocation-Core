@echo off
setlocal EnableExtensions DisableDelayedExpansion
cd /d "%~dp0"
chcp 65001 >nul
cmd.exe /D /K call "%~dp0scripts\12_demo.cmd"
