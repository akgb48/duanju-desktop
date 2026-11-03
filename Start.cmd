@echo off
cd /d "%~dp0"
if not exist "dist\HongguoDesktop.exe" (
  echo Please run Build.ps1 first.
  pause
  exit /b 1
)
start "" "dist\HongguoDesktop.exe"
