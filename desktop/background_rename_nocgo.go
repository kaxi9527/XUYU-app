//go:build darwin && !cgo

package main

import (
	"errors"
	"os"
)

func backgroundPlatformSupported() bool { return false }
func renameBackgroundExclusive(*os.Root, string, string) error {
	return errors.New("当前构建不支持安全更新后台设置")
}
