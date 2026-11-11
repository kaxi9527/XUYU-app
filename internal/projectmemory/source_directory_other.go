//go:build !go1.24 || !unix

package projectmemory

import (
	"context"
	"errors"
)

func discoverSourceDirectory(context.Context, string) ([]sourceCandidate, []string, error) {
	return nil, nil, errors.New("当前平台尚不支持安全读取会话目录")
}
