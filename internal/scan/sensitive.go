package scan

import (
	"path/filepath"
	"strings"
)

// IsSensitiveName 判断文件名像不像密钥/凭据文件。命中的只计数，不读内容，不进清单正文。
func IsSensitiveName(name string) bool {
	n := strings.ToLower(name)
	if n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".env") {
		return true
	}
	if strings.HasPrefix(n, "id_rsa") || strings.HasPrefix(n, "id_ed25519") || strings.HasPrefix(n, "id_ecdsa") || strings.HasPrefix(n, "id_dsa") {
		return true
	}
	switch n {
	case ".netrc", ".npmrc", ".pypirc", ".git-credentials", "credentials":
		return true
	}
	switch strings.ToLower(filepath.Ext(n)) {
	case ".pem", ".key", ".p12", ".pfx", ".keystore", ".jks", ".ppk", ".asc", ".gpg", ".mobileprovision":
		return true
	}
	for _, w := range []string{"credential", "token", "secret", "password", "passwd", ".keychain", "apikey", "api_key", "api-key"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}
