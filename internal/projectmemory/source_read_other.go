//go:build !go1.24 || !unix

package projectmemory

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

func readSource(context.Context, SourceGrant) (SourceRead, []sourceevidence.Claim, error) {
	return SourceRead{}, nil, errors.New("此平台暂不支持安全读取来源记录")
}
