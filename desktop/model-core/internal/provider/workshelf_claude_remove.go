package provider

import (
	"context"
	"errors"
	"github.com/yetone/magpie/internal/appdir"
	"os/exec"
	"time"
)

// Only this product's per-account Claude service is eligible. Tests replace
// this function; they never inspect or delete the user's real Keychain.
var workShelfDeleteClaudeKey = func(service, account string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "security", "delete-generic-password", "-s", service, "-a", account).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	} // item not found
	return err
}

func workShelfRemoveClaude(user string) error {
	dir := claudeAccountDir(user)
	if err := appdir.WorkShelfCheckDirectory(dir); err != nil {
		return err
	}
	claudeHandedOver(user)
	if claudeKeychain {
		if err := workShelfDeleteClaudeKey(claudeDirService(dir), claudeKeychainAccount()); err != nil {
			return errors.New("账号的本机授权未能清理，请重试；账号记录保留")
		}
	}
	return appdir.WorkShelfRemoveAll(dir)
}
