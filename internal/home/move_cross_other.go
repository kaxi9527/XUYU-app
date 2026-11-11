//go:build !darwin || !cgo

package home

import "errors"

func moveCrossDevice(source, target string) error {
	return errors.New("当前平台暂不支持跨磁盘搬迁，文件保留原处")
}
