#!/usr/bin/env bash
# Bash 入口复用 Windows 主构建流程；首个参数为可选版本号。
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:-}"
if [ "$#" -gt 0 ]; then shift; fi
if command -v pwsh >/dev/null 2>&1; then
  powershell=pwsh
elif command -v powershell.exe >/dev/null 2>&1; then
  powershell=powershell.exe
else
  echo 'Windows 构建需要 PowerShell，请运行 Build.ps1。' >&2
  exit 1
fi
exec "$powershell" -NoProfile -File ./Build.ps1 -Version "$version" "$@"
