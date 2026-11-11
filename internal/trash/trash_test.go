package trash

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func setup(t *testing.T) (config.Config, string) {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".Trash"), 0o755)
	root := filepath.Join(home, "Work")
	os.MkdirAll(filepath.Join(root, "_总控", "整理日志"), 0o755)
	cfg := config.Default(home)
	cfg.Root = root
	return cfg, home
}

func TestDeleteFileToTrashAndUndo(t *testing.T) {
	cfg, home := setup(t)
	src := filepath.Join(cfg.Root, "海报", "a.txt")
	os.MkdirAll(filepath.Dir(src), 0o755)
	os.WriteFile(src, []byte("hello"), 0o644)

	sum, err := Apply(Options{Config: cfg, Home: home, Paths: []string{src}, Now: time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Trashed != 1 || sum.Failed != 0 {
		t.Fatalf("%+v items=%+v", sum, sum.Items)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be gone")
	}
	if _, err := os.Stat(sum.Items[0].Dest); err != nil {
		t.Fatal(err)
	}
	ur, err := Undo(cfg, sum.UndoPath)
	if err != nil || ur.Restored != 1 {
		t.Fatalf("undo=%+v err=%v", ur, err)
	}
	b, _ := os.ReadFile(src)
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
}

func TestDeleteFolderCountsAndMoves(t *testing.T) {
	cfg, home := setup(t)
	dir := filepath.Join(cfg.Root, "项目A", "资料", "旧稿")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "1.txt"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "2.txt"), []byte("22"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "3.txt"), []byte("333"), 0o644)

	pre, err := Preview(Options{Config: cfg, Home: home, Paths: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if pre.Preview[0].FileCount != 3 || !pre.Preview[0].IsDir {
		t.Fatalf("%+v", pre.Preview[0])
	}
	if pre.TotalFiles != 3 {
		t.Fatalf("total=%d", pre.TotalFiles)
	}
	sum, err := Apply(Options{Config: cfg, Home: home, Paths: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Trashed != 1 {
		t.Fatalf("%+v", sum)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("folder should be gone")
	}
}

func TestForbidRootAndControl(t *testing.T) {
	cfg, home := setup(t)
	pre, err := Preview(Options{Config: cfg, Home: home, Paths: []string{cfg.Root, filepath.Join(cfg.Root, "_总控"), filepath.Join(cfg.Root, "_总控", "整理日志")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range pre.Preview {
		if !it.Skip {
			t.Fatalf("should skip %s: %+v", it.Path, it)
		}
	}
}

func TestSelectAllPathList(t *testing.T) {
	cfg, home := setup(t)
	var paths []string
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		p := filepath.Join(cfg.Root, "P", n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(n), 0o644)
		paths = append(paths, p)
	}
	sum, err := Apply(Options{Config: cfg, Home: home, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Trashed != 3 {
		t.Fatalf("%+v", sum)
	}
}

func TestOutsideRootRequiresFlag(t *testing.T) {
	cfg, home := setup(t)
	desk := filepath.Join(home, "Desktop", "x.txt")
	os.MkdirAll(filepath.Dir(desk), 0o755)
	os.WriteFile(desk, []byte("x"), 0o644)
	pre, err := Preview(Options{Config: cfg, Home: home, Paths: []string{desk}, AllowOutsideRoot: false})
	if err != nil {
		t.Fatal(err)
	}
	if !pre.Preview[0].Skip {
		t.Fatal("should skip outside without flag")
	}
	sum, err := Apply(Options{Config: cfg, Home: home, Paths: []string{desk}, AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Trashed != 1 {
		t.Fatalf("%+v", sum)
	}
}

func TestStageFallbackWhenNoTrash(t *testing.T) {
	cfg, home := setup(t)
	// 故意不建 .Trash：换一个没有 .Trash 的 home
	home2 := t.TempDir()
	cfg.Root = filepath.Join(home2, "Work")
	os.MkdirAll(filepath.Join(cfg.Root, "_总控"), 0o755)
	src := filepath.Join(cfg.Root, "f.txt")
	os.WriteFile(src, []byte("z"), 0o644)
	sum, err := Apply(Options{Config: cfg, Home: home2, Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Staged != 1 || sum.Trashed != 0 {
		t.Fatalf("%+v items=%+v", sum, sum.Items)
	}
	if _, err := os.Stat(sum.Items[0].Dest); err != nil {
		t.Fatal(err)
	}
	ur, err := Undo(cfg, sum.UndoPath)
	if err != nil || ur.Restored != 1 {
		t.Fatalf("%+v %v", ur, err)
	}
	_ = home
}
