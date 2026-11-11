package projectmemory

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

// NewDirectorySource creates an independent checkpoint for an explicitly
// authorized session directory. It does not read the directory.
func NewDirectorySource(adapter, path string, scope Scope) (SourceGrant, error) {
	if !sourceevidence.Supported(adapter) || !pathOK(path) || filepath.Clean(path) != path {
		return SourceGrant{}, errors.New("请选择有效的会话目录")
	}
	return SourceGrant{ID: grantID(adapter, path, true), Adapter: adapter, Path: path, Directory: true, Enabled: true, ScopeSHA: scope.Digest()}, nil
}

// ScanDirectorySource reuses the project source adapters and their per-file
// checkpoints for cross-project discovery. It returns metadata, never messages.
func ScanDirectorySource(ctx context.Context, g SourceGrant, scope Scope) (SourceGrant, []sourceevidence.Claim, error) {
	if !g.Directory || !g.Enabled || g.ScopeSHA != scope.Digest() {
		return g, nil, errors.New("会话来源范围已变化，请重新设置")
	}
	r, located, err := readGrantedSource(ctx, g, scope)
	if err != nil {
		return g, nil, err
	}
	if err = mergeSourceDocuments(&g, r.Documents, r.Next); err != nil {
		return g, nil, err
	}
	g.Problem = r.Problem
	g.More = r.More
	g.Skipped += r.Skipped
	var claims []sourceevidence.Claim
	for _, entry := range located {
		claim := entry.Claim
		claim.SourcePath = entry.Path
		claims = append(claims, claim)
	}
	return g, claims, nil
}
