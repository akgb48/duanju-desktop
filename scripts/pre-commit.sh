#!/usr/bin/env bash
# 提交前钩子：安装到 .git/hooks/pre-commit
set -euo pipefail

cd "$(dirname "$0")/.."

unformatted=$(gofmt -l . | grep -v '^$' || true)
if [ -n "$unformatted" ]; then
  echo "以下文件未格式化，请运行 gofmt -w ." >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet ./... || exit 1
go test ./... || exit 1
node --test scripts/compat.test.mjs || exit 1

echo "pre-commit 通过"
