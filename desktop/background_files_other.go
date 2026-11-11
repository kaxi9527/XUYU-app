//go:build !darwin

package main

import (
	"errors"
	"os"
)

func lockBackgroundDirectory(string) (*os.Root, func(), error) {
	return nil, nil, errors.New("当前系统不支持后台设置")
}
func writeBackgroundPreferences(*os.Root, string, []byte, []byte) error {
	return errors.New("当前系统不支持后台设置")
}
func readBackgroundFile(string) ([]byte, error) { return nil, nil }
func backgroundPlatformSupported() bool         { return false }
func (f backgroundFiles) setLogin(bool) error   { return errors.New("当前系统不支持登录启动") }
