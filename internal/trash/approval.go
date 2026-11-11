package trash

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/put"
)

// ApprovalSnapshot is local, ephemeral evidence for a reviewed deletion.
// FileInfo is deliberately not serialized or accepted from a caller.
type ApprovalSnapshot struct {
	Fingerprint string
	HiddenCount int
	info        os.FileInfo
}

func hiddenPath(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

// CaptureApproval inspects the explicitly selected deletion tree without following
// links. Unlike organizer snapshots, it does not forbid deleting a whole Git project.
func CaptureApproval(path string) (ApprovalSnapshot, error) {
	var result ApprovalSnapshot
	rootInfo, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("所选路径是软链接，请保留原处")
	}
	digest := sha256.New()
	dirs := map[string]os.FileInfo{}
	count := 0
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 5000 {
			return fmt.Errorf("删除范围超过 5000 个条目，请缩小范围后重新预览")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if hiddenPath(p) {
			result.HiddenCount++
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		fmt.Fprintf(digest, "%q:%d:%d:%d\n", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		switch {
		case d.IsDir():
			dirs[p] = info
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			fmt.Fprintf(digest, "link:%q\n", target) // Link metadata only, never target contents.
		case info.Mode().IsRegular():
			sum, e := put.FileSHA256(p)
			if e != nil {
				return e
			}
			fmt.Fprintln(digest, sum)
		default:
			return fmt.Errorf("包含特殊文件，请保留原处")
		}
		after, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !sameApprovalInfo(info, after) {
			return fmt.Errorf("文件在检查期间发生变化，请重新预览")
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	for dir, before := range dirs {
		after, e := os.Lstat(dir)
		if e != nil {
			return result, e
		}
		if !sameApprovalInfo(before, after) {
			return result, fmt.Errorf("目录在检查期间发生变化，请重新预览")
		}
	}
	finalInfo, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if !sameApprovalInfo(rootInfo, finalInfo) {
		return result, fmt.Errorf("文件在检查期间发生变化，请重新预览")
	}
	result.Fingerprint = fmt.Sprintf("%x", digest.Sum(nil))
	result.info = rootInfo
	return result, nil
}

func sameApprovalInfo(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (s ApprovalSnapshot) matches(path string) error {
	current, err := CaptureApproval(path)
	if err != nil {
		return fmt.Errorf("删除预览已失效，请重新预览：%w", err)
	}
	if s.info == nil || !os.SameFile(s.info, current.info) || s.Fingerprint != current.Fingerprint {
		return fmt.Errorf("文件已变化，未执行删除，请重新预览：%s", filepath.Base(path))
	}
	return nil
}

func validateApproval(opt Options, sum *Summary) error {
	if opt.Approved == nil {
		return nil
	} // Explicit CLI operations keep their existing contract.
	count := 0
	for _, item := range sum.Preview {
		if item.Skip {
			return fmt.Errorf("删除条件已变化，请重新预览：%s", item.Reason)
		}
		approved, ok := opt.Approved[item.Path]
		if !ok {
			return fmt.Errorf("路径不在已确认的删除预览中")
		}
		if err := approved.matches(item.Path); err != nil {
			return err
		}
		count++
	}
	if count != len(opt.Approved) {
		return fmt.Errorf("删除范围已变化，请重新预览")
	}
	return nil
}
