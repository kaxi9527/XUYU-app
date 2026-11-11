//go:build !unix

package models

import (
	"context"
	"errors"
	"os"
)

func lockFile(ctx context.Context, path string) (*os.File, error) {
	return nil, errors.New("当前平台尚未支持模型配置安全写入")
}
func unlockFile(f *os.File) {}
