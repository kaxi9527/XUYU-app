//go:build !unix

package versions

import (
	"context"
	"errors"
	"os"
)

func writesSupported() bool { return false }
func lockContext(context.Context, *os.File) error {
	return errors.New("此平台尚未支持作品版本文件锁")
}
func unlock(*os.File) {}
