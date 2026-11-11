//go:build !unix

package agentdiscovery

import (
	"context"
	"errors"
	"os"
)

func writesSupported() bool { return false }
func lockContext(context.Context, *os.File) error {
	return errors.New("此平台尚未支持检查记录文件锁")
}
func unlock(*os.File) {}
