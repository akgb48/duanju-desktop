@echo off
chcp 65001 >nul
cd /d "%~dp0"
start "" "dist\短剧电脑版.exe" --web --port 34116
start "" "http://127.0.0.1:34116"
