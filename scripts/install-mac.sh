#!/bin/bash
# 把 WorkShelf.app 装到固定位置 ~/Applications/WorkShelf.app，并用「WorkShelf Local」重签。
# 用法：
#   scripts/install-mac.sh [源.app路径]
# 不给路径时：用仓库里最新的 desktop/bin/yy-arm64-*/WorkShelf.app
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CERT_NAME="WorkShelf Local"
DEST_DIR="${HOME}/Applications"
DEST="${DEST_DIR}/WorkShelf.app"

"$ROOT/scripts/ensure-local-cert.sh"

SRC="${1:-}"
if [[ -z "$SRC" ]]; then
  SRC=$(ls -dt "$ROOT"/desktop/bin/yy-arm64-*/WorkShelf.app 2>/dev/null | head -1 || true)
fi
if [[ -z "$SRC" || ! -d "$SRC" ]]; then
  echo "找不到源 .app。用法：scripts/install-mac.sh /path/to/WorkShelf.app" >&2
  exit 1
fi
SRC="$(cd "$SRC" && pwd)"

mkdir -p "$DEST_DIR"
TS=$(date +%Y%m%d%H%M%S)
if [[ -d "$DEST" ]]; then
  # 不 rm：挪走旧的
  mv "$DEST" "${DEST}.old-${TS}"
  echo "旧版已挪到：${DEST}.old-${TS}"
fi

# ditto 保留资源叉/签名结构，再重签
ditto "$SRC" "$DEST"
xattr -cr "$DEST" 2>/dev/null || true
xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true

codesign --force --deep --options runtime --sign "$CERT_NAME" "$DEST"
codesign --verify --deep --strict "$DEST"
echo "---- codesign -dv ----"
codesign -dv --verbose=2 "$DEST" 2>&1 | head -30
echo "已安装到：$DEST"
echo "请从「应用程序」里的 WorkShelf 打开（路径如上）。同一证书+同一路径，授权会保持。"
