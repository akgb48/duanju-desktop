#!/usr/bin/env bash
# 与 Build.ps1 使用相同的资源、前端、Go 测试和静态检查。
set -euo pipefail
exec bash "$(dirname "$0")/build.sh" "$@"
