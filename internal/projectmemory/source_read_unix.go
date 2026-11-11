//go:build go1.24 && unix

package projectmemory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

// Only an explicit SourceGrant may reach this reader. Normal file scanning
// still treats hidden tool folders as metadata-only.
func readSource(ctx context.Context, g SourceGrant) (SourceRead, []sourceevidence.Claim, error) {
	out := SourceRead{ID: g.ID, BeforeSHA: digestJSON(g), Cursor: cloneSourceCursor(g.Cursor)}
	fail := errors.New("来源记录暂不可读或正在变化；保留上次进度，下次再核对")
	parent, err := openDirectory(filepath.Dir(g.Path))
	if err != nil {
		return out, nil, fail
	}
	defer parent.Close()
	name := filepath.Base(g.Path)
	before, err := parent.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || objectKey(before) == "" {
		return out, nil, fail
	}
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return out, nil, fail
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) {
		return out, nil, fail
	}
	if err = ctx.Err(); err != nil {
		return out, nil, err
	}
	anchor := func(offset int64) (string, error) {
		start := offset - 4096
		if start < 0 {
			start = 0
		}
		b := make([]byte, offset-start)
		if len(b) > 0 {
			if _, e := f.ReadAt(b, start); e != nil {
				return "", e
			}
		}
		return digest(b), nil
	}
	c := &out.Cursor
	reset := c.ObjectKey != "" && (c.ObjectKey != objectKey(before) || c.Offset > before.Size())
	if !reset && c.Offset > 0 {
		sum, e := anchor(c.Offset)
		reset = e != nil || sum != c.Anchor
	}
	hash := sha256.New()
	if !reset && c.Offset > 0 {
		prefix := io.NewSectionReader(f, 0, c.Offset)
		buffer := make([]byte, 64<<10)
		for {
			if err = ctx.Err(); err != nil {
				return out, nil, err
			}
			n, e := prefix.Read(buffer)
			if n > 0 {
				hash.Write(buffer[:n])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return out, nil, fail
			}
		}
		reset = c.PrefixSHA == "" || hex.EncodeToString(hash.Sum(nil)) != c.PrefixSHA
	}
	if reset {
		*c = SourceCursor{}
		out.Reset = true
		hash.Reset()
	}
	c.ObjectKey = objectKey(before)
	c.Parser.GeminiProjectDirs = g.GeminiProjectDirs
	remaining := before.Size() - c.Offset
	if remaining < 0 {
		return out, nil, fail
	}
	limit := remaining
	if limit > 4<<20 {
		limit = 4 << 20
	}
	data, err := io.ReadAll(io.NewSectionReader(f, c.Offset, limit))
	if err != nil {
		return out, nil, fail
	}
	var claims []sourceevidence.Claim
	objectionsSince, _ := time.Parse(time.RFC3339Nano, g.ObjectionsSince)
	modelSince, _ := time.Parse(time.RFC3339Nano, g.ObjectionModelSince)
	consumed := 0
	for consumed < len(data) {
		if err = ctx.Err(); err != nil {
			return out, nil, err
		}
		n := bytes.IndexByte(data[consumed:], '\n')
		if n < 0 {
			if c.Skipping || len(data)-consumed > 1<<20 {
				if !c.Skipping {
					out.Skipped++
				}
				c.Skipping = true
				consumed = len(data)
			}
			break // An ordinary partial trailing line is retried on the next pass.
		}
		line := data[consumed : consumed+n]
		consumed += n + 1
		c.Line++
		if c.Skipping {
			c.Skipping = false
			continue
		}
		if len(line) > 1<<20 {
			out.Skipped++
			continue
		}
		got, skipped := c.Parser.Feed(g.Adapter, filepath.Dir(g.Path), c.Line, line)
		out.Skipped += skipped
		claims = append(claims, got...)
		if !objectionsSince.IsZero() {
			var stamp struct {
				Timestamp string `json:"timestamp"`
			}
			_ = json.Unmarshal(line, &stamp)
			at, err := time.Parse(time.RFC3339Nano, stamp.Timestamp)
			if err == nil && at.After(objectionsSince) {
				if objection, ok := c.Parser.FeedObjection(g.Adapter, c.Line, line, !modelSince.IsZero() && at.After(modelSince)); ok {
					claims = append(claims, objection)
				}
			}
		}
		if len(claims) >= 256 {
			break
		}
	}
	c.Offset += int64(consumed)
	hash.Write(data[:consumed])
	c.PrefixSHA = hex.EncodeToString(hash.Sum(nil))
	out.More = c.Offset < before.Size() && (limit < remaining || len(claims) >= 256 || c.Skipping)
	c.Anchor, err = anchor(c.Offset)
	if err != nil {
		return out, nil, fail
	}
	after, err := f.Stat()
	current, check := parent.Lstat(name)
	if err != nil || check != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || before.Size() != current.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) {
		return out, nil, fail
	}
	// Confirm that the pinned directory still occupies the granted path.
	reopened, err := openDirectory(filepath.Dir(g.Path))
	if err != nil {
		return out, nil, fail
	}
	defer reopened.Close()
	a, aerr := parent.Stat(".")
	b, berr := reopened.Stat(".")
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return out, nil, fail
	}
	return out, claims, nil
}
