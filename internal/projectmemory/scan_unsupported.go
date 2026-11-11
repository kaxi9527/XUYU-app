//go:build !go1.24 || !unix

package projectmemory

import (
	"context"
	"errors"
)

func Scan(context.Context, Scope, ScanOptions) (Observation, error) {
	return Observation{}, errors.New("安全文件观察需要 Go 1.24+ 和 Unix 目录句柄支持")
}
