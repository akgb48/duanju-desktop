@echo off
cd /d "%~dp0"
start "" "dist\HongguoDesktop.exe" --web --port 34116
start "" "http://127.0.0.1:34116"
