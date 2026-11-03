#!/usr/bin/env bash
# 生成并签名更新索引（需要 HGUPD_SIGN_KEY）
# 独立工具，当前 EXE 发布流程不生成自动更新索引。
set -euo pipefail

cd "$(dirname "$0")/.."

VER="${1:?用法: sign-index.sh <version> <sha256> <size>}"
SHA="${2:?缺少 sha256}"
SIZE="${3:?缺少 size}"

if [ -z "${SIGN_KEY:-}" ]; then
  echo "错误：未设置 HGUPD_SIGN_KEY 环境变量" >&2
  exit 1
fi

mkdir -p dist

INDEX="dist/hgupd-index-v1.json"
cat > "$INDEX" <<EOF
{
  "version": "${VER}",
  "minVersion": "1.0.0",
  "pubDate": $(date +%s),
  "notes": "自动化发布 ${VER}",
  "packages": [
    {
      "platform": "windows",
      "arch": "amd64",
      "url": "hgplayer-${VER}-windows-amd64.exe",
      "sha256": "${SHA}",
      "size": ${SIZE}
    }
  ]
}
EOF

echo "已生成 $INDEX"

# 用私钥对索引签名，产出 hgupd-manifest-<payload>.<sig>
if command -v openssl >/dev/null; then
  payload=$(base64 -w0 < "$INDEX")
  sig=$(printf '%s' "$payload" | openssl pkeyutl -sign -inkey <(printf '%s' "$SIGN_KEY" | base64 -d) -rawin 2>/dev/null | base64 -w0)
  printf 'hgupd-manifest-%s.%s' "$payload" "$sig" > "dist/hgupd-manifest-${VER}.txt"
  echo "已生成 dist/hgupd-manifest-${VER}.txt"
else
  echo "警告：未找到 openssl，跳过清单签名" >&2
fi
