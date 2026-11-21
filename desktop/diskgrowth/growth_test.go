package diskgrowth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSamples(t *testing.T) {
	root := t.TempDir()
	grow := filepath.Join(root, "Orchid", "node_modules")
	shrink := filepath.Join(root, "Downloads")
	store := History{Dir: filepath.Join(root, "history")}
	for _, p := range []string{grow, shrink, filepath.Join(root, ".npm")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, n int) {
		t.Helper()
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i * 31)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(grow, "one"), 65536)
	write(filepath.Join(shrink, "old"), 131072)
	cached := filepath.Join(root, ".npm", "cache")
	write(cached, 65536)
	if err := os.Link(cached, filepath.Join(grow, "alias")); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Root: root, Home: root, Projects: map[string]string{filepath.Dir(grow): "Orchid"}, HistoryDir: store.Dir}
	capture := func(trigger string) Sample {
		t.Helper()
		s, e := Scan(context.Background(), scope, time.Now(), trigger, "")
		if e != nil {
			t.Fatal(e)
		}
		if err := store.Write(Entry{Sample: s}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := capture("manual")
	records, e := store.Records(time.Now(), 14)
	if e != nil {
		t.Fatal(e)
	}
	_, prev, e := store.Pair(records, "")
	if prev != nil || e != nil {
		t.Fatal("First sample must be a baseline", e)
	}
	write(filepath.Join(grow, "new"), 262144)
	if err := os.Remove(filepath.Join(shrink, "old")); err != nil {
		t.Fatal(err)
	}
	second := capture("manual")
	records, e = store.Records(time.Now(), 14)
	if e != nil {
		t.Fatal(e)
	}
	now, prev, e := store.Pair(records, "")
	if e != nil || prev == nil || now == nil {
		t.Fatal("Missing baseline", e)
	}
	if !prev.Finished.Equal(first.Finished) || !now.Finished.Equal(second.Finished) || !now.Started.After(prev.Finished) || now.Free == 0 {
		t.Fatal("Incorrect sampling timestamps or capacity")
	}
	sizes := func(s *Entry) map[string]int64 {
		m := map[string]int64{}
		for _, b := range s.Buckets {
			m[b.Category] += b.Bytes
			if b.Category == "项目依赖与虚拟环境" && (b.Path != grow || b.Project != "Orchid") {
				t.Fatal(b)
			}
		}
		return m
	}
	a, b := sizes(prev), sizes(now)
	if b["项目依赖与虚拟环境"]-a["项目依赖与虚拟环境"] != 262144 || b["下载"]-a["下载"] != -131072 || a["共享包缓存"] != 65536 || b["共享包缓存"] != 65536 {
		t.Fatalf("Unexpected real byte totals: %v -> %v", a, b)
	}
	if dir := os.Getenv("XUYU_GROWTH_TEST_OUTPUT"); dir != "" {
		out := map[string]any{"Current": now, "Previous": prev, "root": root, "free": now.Free, "records": records, "prefs": Preferences{true, "09:00", 14}}
		encoded, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "growth-fixture.json"), encoded, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("Real samples: %s → %s; dependencies %+d bytes, downloads %+d bytes, shared store %d bytes", first.Finished.Format(time.RFC3339Nano), second.Finished.Format(time.RFC3339Nano), b["项目依赖与虚拟环境"]-a["项目依赖与虚拟环境"], b["下载"]-a["下载"], b["共享包缓存"])
}

func TestScheduleAndRetention(t *testing.T) {
	h := History{Dir: t.TempDir()}
	p := Preferences{Enabled: true, At: "09:00", Days: 7}
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.Local)
	if err := h.Save(p); err != nil {
		t.Fatal(err)
	}
	loaded, err := h.Preferences()
	if loaded != p || err != nil {
		t.Fatal(loaded, err)
	}
	if due, _ := Due(now.Add(-time.Minute), p, nil); due != "" {
		t.Fatal(due)
	}
	if due, _ := Due(now, p, nil); due != "daily" {
		t.Fatal(due)
	}
	if due, _ := Due(now.Add(time.Hour), p, nil); due != "catchup" {
		t.Fatal(due)
	}
	for _, offset := range []int{-15, -14, -7, -6, 0} {
		s := Entry{Sample: Sample{Started: now.AddDate(0, 0, offset), Finished: now.AddDate(0, 0, offset), Trigger: "daily", Root: h.Dir}}
		if err := h.Write(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct{ days, want int }{{15, 4}, {7, 2}} {
		rec, e := h.Records(now, v.days)
		if e != nil || len(rec) != v.want {
			t.Fatalf("%d-day retention: %d entries, %v", v.days, len(rec), e)
		}
		if due, _ := Due(now.Add(time.Hour), p, rec); due != "" {
			t.Fatal("repeated automatic sample after restart")
		}
	}
	for _, days := range []int{6, 16} {
		p.Days = days
		if err := h.Save(p); err == nil {
			t.Fatal("accepted invalid retention")
		}
	}
}
