package versions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type hookContext struct {
	context.Context
	calls int
	hook  func(int)
}

func (c *hookContext) Err() error { c.calls++; c.hook(c.calls); return c.Context.Err() }

func TestFingerprintCompleteContentAndBoundaries(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "draft.txt")
	data := []byte("version one\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := Fingerprint(context.Background(), root, file, 1024)
	sum := sha256.Sum256(data)
	if err != nil || content.SHA256 != hex.EncodeToString(sum[:]) || content.Size != int64(len(data)) {
		t.Fatal(content, err)
	}
	cases := []struct {
		name, root, path string
		limit            int64
	}{{"outside", t.TempDir(), file, 1024}, {"directory", root, root, 1024}, {"size", root, file, 3}, {"limit", root, file, 0}, {"relative", "relative", file, 1024}, {"missing", root, filepath.Join(root, "absent"), 1024}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Fingerprint(context.Background(), tc.root, tc.path, tc.limit)
			if err == nil || got != (Content{}) {
				t.Fatal("partial or unauthorized identity", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Fingerprint(ctx, root, file, 1024); !errors.Is(err, context.Canceled) || got != (Content{}) {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(file, []byte("version two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Fingerprint(context.Background(), root, file, 1024)
	if err != nil || content == changed {
		t.Fatal("overwrite reused old content identity", changed, err)
	}
}

func TestFingerprintRejectsLinksAndMidReadChange(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "data")
	if err := os.WriteFile(file, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "parent")
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(parent, "data")} {
		if _, err := Fingerprint(context.Background(), root, path, 1024); err == nil {
			t.Fatal("symlink accepted", path)
		}
	}
	large := filepath.Join(root, "large")
	if err := os.WriteFile(large, make([]byte, 256*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &hookContext{Context: context.Background(), hook: func(call int) {
		if call == 3 {
			if err := os.WriteFile(large, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}}
	if content, err := Fingerprint(ctx, root, large, 512*1024); !errors.Is(err, ErrContentChanged) || content != (Content{}) {
		t.Fatal(content, err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = &hookContext{Context: base, hook: func(call int) {
		if call == 2 {
			cancel()
		}
	}}
	if content, err := Fingerprint(ctx, root, large, 1024); !errors.Is(err, context.Canceled) || content != (Content{}) {
		t.Fatal(content, err)
	}
}
