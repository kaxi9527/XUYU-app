package put

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// SecretScanner 在入库前扫描文件内容里的密钥。
// 返回的 warn 是给人看的提醒（例如没装扫描器）；返回 err 表示拒绝入库。
type SecretScanner interface {
	Scan(paths []string) (warn string, err error)
}

// ErrSecretFound 表示扫描器发现了疑似密钥。
var ErrSecretFound = errors.New("gitleaks 发现疑似密钥，拒绝入库（没有复制任何文件）")

// Gitleaks 调用 PATH 上的 gitleaks。没装就只提醒，不拦。扫描器自己出错则拒绝（宁可不收）。
type Gitleaks struct {
	Bin     string // 默认 "gitleaks"
	Timeout time.Duration
}

// Scan 实现 SecretScanner。
func (g Gitleaks) Scan(paths []string) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gitleaks"
	}
	full, err := exec.LookPath(bin)
	if err != nil {
		return "⚠️ 没找到 gitleaks，本次没做密钥扫描（只按文件名拦了明显的密钥文件）。建议安装：brew install gitleaks", nil
	}
	to := g.Timeout
	if to == 0 {
		to = 60 * time.Second
	}
	for _, p := range paths {
		code, out, err := run(full, to, "dir", "--no-banner", "--redact", "--exit-code", "99", p)
		if err == nil && code != 0 && code != 99 {
			// 老版本 gitleaks 没有 dir 子命令。
			code, out, err = run(full, to, "detect", "--no-git", "--no-banner", "--redact", "--exit-code", "99", "--source", p)
		}
		if err != nil {
			return "", fmt.Errorf("gitleaks 运行失败，为安全起见拒绝入库：%w", err)
		}
		switch code {
		case 0:
		case 99:
			return "", fmt.Errorf("%w：%s", ErrSecretFound, p)
		default:
			return "", fmt.Errorf("gitleaks 退出码 %d，为安全起见拒绝入库：%s", code, lastLine(out))
		}
	}
	return "", nil
}

func run(bin string, to time.Duration, args ...string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return 0, string(out), ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out), nil
	}
	if err != nil {
		return 0, string(out), err
	}
	return 0, string(out), nil
}

func lastLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return s[i+1:]
		}
	}
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}
