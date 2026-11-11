package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestMarkAndRead(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	f := filepath.Join(home, "图.png")
	os.WriteFile(f, []byte("png"), 0o644)
	// 洛杉矶 11 月 5 日晚 = UTC 6 日
	now := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC)
	recs, err := Mark(MarkOptions{Config: cfg, Files: []string{f}, Agent: "yy", WorkType: "设计·生图出图", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].SHA256 == "" || recs[0].Source != SourceSelf || !strings.HasSuffix(recs[0].Time, "-08:00") {
		t.Fatalf("%+v", recs[0])
	}
	day := filepath.Join(Dir(cfg), "2026-11-05.jsonl")
	if _, err := os.Stat(day); err != nil {
		t.Fatal("按洛杉矶日期分文件", err)
	}
	// 再记一次是追加
	Mark(MarkOptions{Config: cfg, Files: []string{f}, Agent: "yy2", Now: now.Add(time.Minute)})
	got, err := ReadSince(Dir(cfg), now.Add(-time.Hour))
	if err != nil || len(got) != 2 || got[1].Agent != "yy2" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := Mark(MarkOptions{Config: cfg, Files: []string{f}}); err == nil && os.Getenv("WORKSHELF_AGENT") == "" {
		t.Fatal("没写智能体名应该拒绝")
	}
}

func TestSidecar(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, SidecarName), []byte(`{"time":"2026-11-05T08:30:00-08:00","file":"a.md","agent":"codex"}
坏行
{"file":"../x","agent":"bad"}
`), 0o644)
	got := ReadSidecar(dir)
	if len(got) != 1 || got[0].Path != filepath.Join(dir, "a.md") || got[0].Source != SourceSidecar {
		t.Fatalf("%+v", got)
	}
}
