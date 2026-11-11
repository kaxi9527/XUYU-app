//go:build !windows && ((!darwin && !linux) || !cgo)

package home

import "errors"

func renameExclusive(from, to string) error {
	return errors.New("当前构建不支持原子防覆盖移动；请使用启用 CGO 的 macOS/Linux 构建")
}
