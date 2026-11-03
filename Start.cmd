@echo off
chcp 65001 >nul
cd /d "%~dp0"
if not exist "dist\短剧电脑版.exe" (
  echo Please run Build.ps1 first.
  pause
  exit /b 1
)
start "" "dist\短剧电脑版.exe"
