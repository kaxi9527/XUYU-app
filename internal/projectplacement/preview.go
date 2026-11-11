// Package projectplacement prepares project-scoped file placement. A preview
// is read-only evidence, not a capability or a file-movement authorization.
package projectplacement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/scan"
)

var ErrScope = errors.New("项目或文件范围已变化，请重新生成整理预览")
var ErrChanged = errors.New("预览期间目录发生变化，请重新生成预览")
var ErrUnsupported = errors.New("当前系统不支持受约束的文件整理预览")

const maxItems = 10000

// Authority is constructed from current registration and host policy. Never
// accept it, a Proposal, or a PreviewSnapshot directly from an RPC caller.
// Boundaries are host-approved bases, not extra members of Scope. Excluded
// contains control/journal directories which may never be placement targets.
type Authority struct {
	Project    projectmemory.Project `json:"project"`
	Scope      projectmemory.Scope   `json:"scope"`
	Boundaries []string              `json:"boundaries"`
	Excluded   []string              `json:"excluded"`
}

type PreviewLimits struct {
	MaxFileBytes int64
	MaxBytes     int64
	MaxEntries   int
}

type Row struct {
	FileID    string             `json:"fileID"`
	Source    string             `json:"source"`
	Target    string             `json:"target,omitempty"`
	Folder    string             `json:"folder"`
	Action    string             `json:"action"` // move, copy, keep; still a proposal
	Code      string             `json:"code"`
	Reason    string             `json:"reason"`
	Reference int                `json:"reference"`
	Before    projectmemory.Fact `json:"before"`
	Object    string             `json:"object,omitempty"`
	Renamed   bool               `json:"renamed,omitempty"`
}

type Directory struct {
	Path       string `json:"path"`
	Object     string `json:"object"`
	ModifiedAt string `json:"modifiedAt"`
	ChangedAt  string `json:"changedAt"`
	Device     uint64 `json:"device"`
}

type PreviewSnapshot struct {
	Schema      int                              `json:"schema"`
	CreatedAt   string                           `json:"createdAt"`
	ExpiresAt   string                           `json:"expiresAt"`
	Authority   Authority                        `json:"authority"`
	Proposal    projectcontext.PlacementProposal `json:"proposal"`
	Rows        []Row                            `json:"rows"`
	Directories []Directory                      `json:"directories"`
	HashedBytes int64                            `json:"hashedBytes"`
}

// Preview has no execution method. Its detached snapshot is suitable for a
// grouped UI; execution must independently confirm and durably journal it.
type Preview struct {
	snapshot PreviewSnapshot
	sha      string
}

func (p Preview) SHA256() string { return p.sha }
func (p Preview) Snapshot() PreviewSnapshot {
	data, _ := json.Marshal(p.snapshot)
	var out PreviewSnapshot
	_ = json.Unmarshal(data, &out)
	return out
}

func absolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && len(path) <= 4096 && utf8.ValidString(path) && !strings.ContainsAny(path, "\x00\r\n")
}

func within(base, path string) bool {
	r, err := filepath.Rel(base, path)
	return err == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r))
}

func (a Authority) boundary(path string) string {
	base := ""
	for _, b := range a.Boundaries {
		if within(b, path) && (base == "" || len(b) < len(base)) {
			base = b // Include protective engineering markers above a selected child.
		}
	}
	return base
}

func (a Authority) allowed(path string) bool {
	if !absolute(path) || !a.Scope.Allows(path) || a.boundary(path) == "" {
		return false
	}
	for _, x := range a.Excluded {
		// Exclusions are conservative even on case-sensitive filesystems. Exact
		// membership remains case-sensitive; this never expands Scope.
		xkey, pkey := pathKey(x), pathKey(path)
		if xkey == "" || pkey == "" || within(xkey, pkey) {
			return false
		}
	}
	return true
}

func validate(a Authority, p projectcontext.PlacementProposal) error {
	if a.Project != p.Project || a.Project.ID == "" || a.Scope.Validate() != nil || a.Scope.Digest() != p.Scope.Digest() || !a.allowed(a.Project.Path) || len(a.Boundaries) == 0 || len(a.Boundaries) > 100 || len(a.Excluded) > 100 || p.Profile.Validate() != nil || len(p.Suggestions)+len(p.UncertainIDs) > maxItems || len(p.References) == 0 || len(p.References) > projectcontext.MaxBatchParts {
		return ErrScope
	}
	for _, list := range [][]string{a.Boundaries, a.Excluded} {
		for _, path := range list {
			if !absolute(path) {
				return ErrScope
			}
		}
	}
	for _, path := range a.Excluded {
		if a.boundary(path) == "" {
			return ErrScope
		}
	}
	for _, list := range [][]string{a.Scope.Roots, a.Scope.Files} {
		for _, path := range list {
			if a.boundary(path) == "" {
				return ErrScope
			}
		}
	}
	folders := map[string]bool{}
	for _, f := range p.Profile.Folders {
		if !utf8.ValidString(f) {
			return ErrScope
		}
		folders[f] = true
	}
	seen, paths := map[string]bool{}, map[string]bool{}
	for _, s := range p.Suggestions {
		f := s.File
		if f.ID == "" || seen[f.ID] || paths[f.Fact.Path] || !a.allowed(f.Fact.Path) || f.Status != "present" || f.Fact.Kind != "file" || !folders[s.Folder] || s.Reference < 0 || s.Reference >= len(p.References) || s.SuggestedTarget != filepath.Join(a.Project.Path, s.Folder, filepath.Base(f.Fact.Path)) || !a.allowed(s.SuggestedTarget) || !within(a.Project.Path, s.SuggestedTarget) {
			return ErrScope
		}
		_, timeErr := time.Parse(time.RFC3339Nano, f.Fact.ModifiedAt)
		hashBytes, hashErr := hex.DecodeString(f.Fact.SHA256)
		if f.Fact.Size < 0 || timeErr != nil || len(f.Fact.ObjectKey) > 512 || f.Fact.HashState != "verified" && f.Fact.HashState != "metadata_only" && f.Fact.HashState != "unavailable" || f.Fact.HashState == "verified" && (hashErr != nil || len(hashBytes) != 32 || f.Fact.SHA256 != strings.ToLower(f.Fact.SHA256)) || f.Fact.HashState != "verified" && f.Fact.SHA256 != "" {
			return ErrScope
		}
		if folder, _ := p.Profile.Match(f.Fact.Path); folder != "" && folder != s.Folder {
			return ErrScope
		}
		seen[f.ID], paths[f.Fact.Path] = true, true
	}
	for _, id := range p.UncertainIDs {
		if id == "" || seen[id] {
			return ErrScope
		}
		seen[id] = true
	}
	return nil
}

func protectedPath(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") || scan.IsSensitiveName(part) {
			return true
		}
		switch strings.ToLower(part) {
		case "_总控", "node_modules", "venv", "vendor", "__pycache__", "library":
			return true
		}
		switch strings.ToLower(filepath.Ext(part)) {
		case ".app", ".bundle", ".framework", ".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm", ".sqlite-wal", ".sqlite-shm", ".fcpbundle", ".logicx", ".band":
			return true
		}
	}
	return false
}

// BuildPreview never creates a directory, moves a file, or records permission.
// Limits affect eligibility explicitly (one keep row per omitted file), never
// silently truncate the proposal. Caller cancellation discards the preview.
func BuildPreview(ctx context.Context, authority Authority, proposal projectcontext.PlacementProposal, limits PreviewLimits) (Preview, error) {
	if !previewSupported() {
		return Preview{}, ErrUnsupported
	}
	if err := validate(authority, proposal); err != nil {
		return Preview{}, err
	}
	// Own all mutable input before reading disk or computing the fingerprint.
	data, err := json.Marshal(struct {
		A Authority
		P projectcontext.PlacementProposal
	}{authority, proposal})
	if err != nil {
		return Preview{}, ErrScope
	}
	var owned struct {
		A Authority
		P projectcontext.PlacementProposal
	}
	if json.Unmarshal(data, &owned) != nil {
		return Preview{}, ErrScope
	}
	if limits.MaxFileBytes <= 0 || limits.MaxFileBytes > 256<<20 {
		limits.MaxFileBytes = 256 << 20
	}
	if limits.MaxBytes <= 0 || limits.MaxBytes > 1<<30 {
		limits.MaxBytes = 1 << 30
	}
	if limits.MaxEntries <= 0 || limits.MaxEntries > 20000 {
		limits.MaxEntries = 20000
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	b := previewBuilder{ctx: bounded, authority: owned.A, limits: limits, directories: map[string]Directory{}, checks: map[string]directoryCheck{}, reserved: map[string]bool{}, excludedObjects: map[string]bool{}, excludedSlots: map[string]bool{}, exclusionChecks: map[string]string{}}
	if err := b.loadExclusions(); err != nil {
		return Preview{}, err
	}
	// Reserve template directories before assigning file names. Otherwise a
	// file called "drafts" could occupy another group's required drafts/ path.
	for _, folder := range owned.P.Profile.Folders {
		for path := filepath.Join(owned.A.Project.Path, folder); within(owned.A.Project.Path, path); path = filepath.Dir(path) {
			key := pathKey(path)
			if key == "" {
				return Preview{}, ErrScope
			}
			b.reserved[key] = true
			if path == owned.A.Project.Path {
				break
			}
		}
	}
	rows, err := b.rows(owned.P)
	if err != nil {
		return Preview{}, err
	}
	if err = b.checkDirectories(); err != nil {
		return Preview{}, err
	}
	if err = bounded.Err(); err != nil {
		return Preview{}, err
	}
	dirs := make([]Directory, 0, len(b.directories))
	for _, d := range b.directories {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Path < dirs[j].Path })
	now := time.Now().UTC()
	out := PreviewSnapshot{Schema: 1, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(30 * time.Minute).Format(time.RFC3339Nano), Authority: owned.A, Proposal: owned.P, Rows: rows, Directories: dirs, HashedBytes: b.hashed}
	data, err = json.Marshal(out)
	if err != nil {
		return Preview{}, err
	}
	h := sha256.Sum256(data)
	return Preview{snapshot: out, sha: hex.EncodeToString(h[:])}, nil
}

type previewBuilder struct {
	ctx             context.Context
	authority       Authority
	limits          PreviewLimits
	directories     map[string]Directory
	checks          map[string]directoryCheck
	reserved        map[string]bool
	excludedObjects map[string]bool
	excludedSlots   map[string]bool
	exclusionChecks map[string]string
	hashed          int64
	entries         int
}

type directoryCheck struct{ code, reason string }

func uniqueName(path string, index int) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s-%02d%s", strings.TrimSuffix(path, ext), index, ext)
}
