//go:build !unix

package projectmemory

import (
	"context"
	"errors"
	"os"
)

func writesSupported() bool { return false }
func lockContext(context.Context, *os.File) error {
	return errors.New("此平台尚未支持项目记忆安全写入")
}
func unlock(*os.File) {}
func openRegular(path string, create bool) (*os.File, error) {
	if create {
		return nil, errors.New("此平台尚未支持项目记忆安全写入")
	}
	return os.Open(path)
}
