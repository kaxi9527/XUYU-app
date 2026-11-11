#!/bin/bash
# 确保本机登录钥匙串里有「WorkShelf Local」代码签名证书（自签）。
# 已有同名身份就复用，不重复新建。给内测用：授权跟证书+固定安装路径走，不跟每次 adhoc CDHash 走。
set -euo pipefail
CERT_NAME="WorkShelf Local"
KEYCHAIN="${HOME}/Library/Keychains/login.keychain-db"

if security find-identity -v -p codesigning 2>/dev/null | grep -F "\"${CERT_NAME}\"" >/dev/null; then
  echo "已有代码签名身份：${CERT_NAME}"
  security find-identity -v -p codesigning | grep -F "\"${CERT_NAME}\"" || true
  exit 0
fi

TS=$(date +%Y%m%d%H%M%S)
TMP="${TMPDIR:-/tmp}/yy-workshelf-cert-${TS}"
mkdir -p "$TMP"
CNF="${TMP}/openssl.cnf"
CRT="${TMP}/workshelf-local.crt"
KEY="${TMP}/workshelf-local.key"
P12="${TMP}/workshelf-local.p12"

cat > "$CNF" <<'CNF'
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn
x509_extensions = v3_req

[dn]
CN = WorkShelf Local
O = WorkShelf Local Dev
OU = WorkShelf
C = US

[v3_req]
basicConstraints = critical,CA:FALSE
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
CNF

openssl req -new -x509 -days 3650 -nodes \
  -config "$CNF" \
  -keyout "$KEY" \
  -out "$CRT"

# 导出 PKCS12（固定口令只用于导入；导入后钥匙串里用）
openssl pkcs12 -export \
  -out "$P12" \
  -inkey "$KEY" \
  -in "$CRT" \
  -passout pass:workshelf-local-import \
  -name "$CERT_NAME"

security import "$P12" \
  -k "$KEYCHAIN" \
  -P workshelf-local-import \
  -T /usr/bin/codesign \
  -T /usr/bin/security \
  -T /usr/bin/productsign

# 允许 codesign 无弹窗取用（失败不致命：有的机器钥匙串有密码）
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "" "$KEYCHAIN" 2>/dev/null || \
  echo "提示：set-key-partition-list 没做成（钥匙串可能有密码）。第一次 codesign 若弹窗，勾选「始终允许」即可。"

# 信任此证书用于代码签名（用户级）
if ! security add-trusted-cert -d -r trustRoot -p codeSign -k "$KEYCHAIN" "$CRT" 2>/dev/null; then
  echo "提示：自动加信任没做成。请打开「钥匙串访问」→ 登录 → 证书「WorkShelf Local」→ 显示简介 → 信任 → 代码签名：始终信任。"
fi

# 临时文件按规矩不删（yy- 前缀+时间戳已在路径里；再拷一份带 yy- 前缀到任务目录旁）
KEEP="${HOME}/Work/WorkShelf/任务/yy-local-cert-${TS}"
mkdir -p "$KEEP"
cp "$CRT" "$KEY" "$P12" "$CNF" "$KEEP/" 2>/dev/null || true
echo "证书材料副本（不删）：$KEEP"

# 再试一次信任后查 valid
if ! security find-identity -v -p codesigning 2>/dev/null | grep -F "\"${CERT_NAME}\"" >/dev/null; then
  security add-trusted-cert -d -r trustRoot -p codeSign -k "$KEYCHAIN" "$CRT" 2>/dev/null || true
fi
if ! security find-identity -p codesigning 2>/dev/null | grep -F "\"${CERT_NAME}\"" >/dev/null; then
  echo "错误：导入后仍找不到「${CERT_NAME}」" >&2
  security find-identity -p codesigning >&2 || true
  exit 1
fi
echo "已创建并导入：${CERT_NAME}"
security find-identity -v -p codesigning | grep -F "\"${CERT_NAME}\"" || true
