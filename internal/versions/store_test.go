package versions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := newID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func testCatalog(t *testing.T) Catalog {
	t.Helper()
	c := emptyCatalog()
	p := Project{ID: testID(t, "project"), Name: "项目", Path: filepath.Join(t.TempDir(), "project")}
	w := Work{ID: testID(t, "work"), ProjectID: p.ID, Title: "作品", VersionIDs: []string{}}
	v := Version{ID: testID(t, "version"), WorkID: w.ID, Content: Content{SHA256: strings.Repeat("a", 64), Size: 5}, Role: "draft", RegisteredAt: time.Now().UTC().Format(time.RFC3339Nano)}
	w.VersionIDs = append(w.VersionIDs, v.ID)
	c.Projects = append(c.Projects, p)
	c.Works = append(c.Works, w)
	c.Versions = append(c.Versions, v)
	c.Locations = append(c.Locations, Location{ID: testID(t, "location"), VersionID: v.ID, Path: filepath.Join(p.Path, "draft.txt"), AuthorizedRoot: p.Path, RegisteredBy: "user"})
	return c
}
func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	if !writesSupported() {
		t.Skip("platform write lock not implemented")
	}
	dir := filepath.Join(t.TempDir(), "versions")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}
func seedStore(t *testing.T, s *Store) Receipt {
	t.Helper()
	c := testCatalog(t)
	receipt, err := s.update(context.Background(), 0, "register-1", "register", map[string]string{"file": "draft"}, func(next *Catalog) ([]string, error) { *next = c; return []string{c.Versions[0].ID}, nil })
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEmptyReadHasNoFilesystemEffects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent", "versions")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Read(context.Background())
	if err != nil || c.SchemaVersion != 1 || c.Revision != 0 {
		t.Fatalf("read: %#v %v", c, err)
	}
	if _, err = os.Stat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created directories", err)
	}
}

func TestStoreReplayConflictAndImmutableContent(t *testing.T) {
	s, dir := testStore(t)
	receipt := seedStore(t, s)
	original := mustReadFile(t, filepath.Join(dir, "catalog.json"))
	replay, err := s.update(context.Background(), 0, "register-1", "register", map[string]string{"file": "draft"}, func(*Catalog) ([]string, error) { t.Fatal("replay executed mutation"); return nil, nil })
	if err != nil || replay.Revision != receipt.Revision || replay.EntityIDs[0] != receipt.EntityIDs[0] {
		t.Fatal(replay, err)
	}
	_, err = s.update(context.Background(), 1, "register-1", "register", map[string]string{"file": "other"}, func(*Catalog) ([]string, error) { return nil, nil })
	if !errors.Is(err, ErrOperationReused) {
		t.Fatal(err)
	}
	_, err = s.update(context.Background(), 0, "new-op", "register", nil, func(*Catalog) ([]string, error) { return nil, nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	_, err = s.update(context.Background(), 1, "overwrite", "register", nil, func(c *Catalog) ([]string, error) { c.Versions[0].Content.Size++; return nil, nil })
	if err == nil {
		t.Fatal("content identity overwritten")
	}
	if string(original) != string(mustReadFile(t, filepath.Join(dir, "catalog.json"))) {
		t.Fatal("failed mutation changed catalog")
	}
	fi, err := os.Stat(filepath.Join(dir, "catalog.json"))
	if err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatal("catalog not private", err)
	}
}

func TestNewDraftDoesNotReplaceUsableAndConfirmationIsAppendOnly(t *testing.T) {
	s, _ := testStore(t)
	seedStore(t, s)
	old, _ := s.Read(context.Background())
	v1 := old.Versions[0]
	_, err := s.update(context.Background(), 1, "confirm", "set_usable", v1.ID, func(c *Catalog) ([]string, error) {
		c.Confirmations = append(c.Confirmations, Confirmation{ID: testID(t, "confirmation"), VersionID: v1.ID, Content: v1.Content, ConfirmedAt: time.Now().Format(time.RFC3339Nano), Actor: "local_user"})
		c.Works[0].CurrentUsableVersionID = v1.ID
		return []string{v1.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.update(context.Background(), 2, "new-draft", "register", nil, func(c *Catalog) ([]string, error) {
		v := v1
		v.ID = testID(t, "version")
		v.Content.SHA256 = strings.Repeat("b", 64)
		c.Versions = append(c.Versions, v)
		c.Works[0].VersionIDs = append(c.Works[0].VersionIDs, v.ID)
		return []string{v.ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Read(context.Background())
	if c.Works[0].CurrentUsableVersionID != v1.ID || len(c.Versions) != 2 || len(c.Provenance) != 0 {
		t.Fatal("draft replaced usable or provenance invented", c)
	}
	_, err = s.update(context.Background(), 3, "change-confirmation", "set_usable", nil, func(c *Catalog) ([]string, error) {
		c.Confirmations[0].ConfirmedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		return nil, nil
	})
	if err == nil {
		t.Fatal("history rewritten")
	}
}

func TestCorruptUnknownAndInvalidReferencesAreNeverOverwritten(t *testing.T) {
	for _, kind := range []string{"truncated", "unknown-schema", "unknown-field", "missing-work", "cycle", "wrong-confirmation", "large-revision"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			c := testCatalog(t)
			switch kind {
			case "unknown-schema":
				c.SchemaVersion = 2
			case "large-revision":
				c.Revision = 1 << 53
			case "missing-work":
				c.Versions[0].WorkID = testID(t, "work")
			case "cycle":
				v := c.Versions[0]
				v.ID = testID(t, "version")
				c.Versions = append(c.Versions, v)
				c.Works[0].VersionIDs = append(c.Works[0].VersionIDs, v.ID)
				c.Relations = []Relation{{ID: testID(t, "relation"), VersionID: v.ID, ParentVersionID: c.Versions[0].ID, Kind: "exported_from"}, {ID: testID(t, "relation"), VersionID: c.Versions[0].ID, ParentVersionID: v.ID, Kind: "exported_from"}}
			case "wrong-confirmation":
				c.Works[0].CurrentUsableVersionID = c.Versions[0].ID
			}
			data, _ := json.Marshal(c)
			if kind == "truncated" {
				data = []byte("{")
			}
			if kind == "unknown-field" {
				data = append([]byte(`{"future":true,`), data[1:]...)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "catalog.json")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Read(context.Background()); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
			_, err := s.update(context.Background(), 0, "bad", "register", nil, func(*Catalog) ([]string, error) { t.Fatal("corrupt store reached mutation"); return nil, nil })
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
			if string(data) != string(mustReadFile(t, file)) {
				t.Fatal("corrupt file overwritten")
			}
		})
	}
}

func TestWriteFailureAndExternalEditKeepOriginal(t *testing.T) {
	s, dir := testStore(t)
	seedStore(t, s)
	file := filepath.Join(dir, "catalog.json")
	original := mustReadFile(t, file)
	s.replace = func(string, string) error { return errors.New("simulated disk failure") }
	_, err := s.update(context.Background(), 1, "write-fail", "add_provenance", nil, func(*Catalog) ([]string, error) { return nil, nil })
	if err == nil {
		t.Fatal("failure reported success")
	}
	if string(original) != string(mustReadFile(t, file)) {
		t.Fatal("original lost")
	}
	temps, _ := filepath.Glob(filepath.Join(dir, ".catalog-*.tmp"))
	if len(temps) != 0 {
		t.Fatal("temporary files leaked")
	}
	s.replace = os.Rename
	external := append(append([]byte(nil), original...), byte('\n'))
	_, err = s.update(context.Background(), 1, "external-edit", "add_provenance", nil, func(*Catalog) ([]string, error) { return nil, os.WriteFile(file, external, 0o600) })
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if string(external) != string(mustReadFile(t, file)) {
		t.Fatal("external write clobbered")
	}
}

func TestConcurrentIdenticalRequestsRunOnce(t *testing.T) {
	s, dir := testStore(t)
	var mu sync.Mutex
	calls := 0
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			other, err := NewStore(dir)
			if err != nil {
				t.Error(err)
				return
			}
			r, err := other.update(context.Background(), 0, "one-op", "register", nil, func(*Catalog) ([]string, error) { mu.Lock(); calls++; mu.Unlock(); return nil, nil })
			if err != nil || r.Revision != 1 {
				t.Errorf("%+v %v", r, err)
			}
		}()
	}
	group.Wait()
	c, err := s.Read(context.Background())
	if err != nil || calls != 1 || c.Revision != 1 || len(c.Operations) != 1 {
		t.Fatal(c, calls, err)
	}
}

func TestReceiptWithEmptyIDsRemainsMutableButHistoryCannotChange(t *testing.T) {
	s, _ := testStore(t)
	_, err := s.update(context.Background(), 0, "empty", "register", nil, func(*Catalog) ([]string, error) { return []string{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.update(context.Background(), 1, "next", "register", nil, func(*Catalog) ([]string, error) { return []string{}, nil })
	if err != nil {
		t.Fatal("empty receipt prevented next transaction", err)
	}
	seed := testCatalog(t)
	_, err = s.update(context.Background(), 2, "with-version", "register", nil, func(c *Catalog) ([]string, error) {
		c.Projects = seed.Projects
		c.Works = seed.Works
		c.Versions = seed.Versions
		return []string{seed.Versions[0].ID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.update(context.Background(), 3, "rewrite-receipt", "register", nil, func(c *Catalog) ([]string, error) { c.Operations[2].EntityIDs[0] = seed.Works[0].ID; return nil, nil })
	if err == nil {
		t.Fatal("receipt entity changed")
	}
}

func TestLockWaitRespectsCancellation(t *testing.T) {
	s, dir := testStore(t)
	if _, err := s.directoryReady(true); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "catalog.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = lockContext(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	defer unlock(f)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = s.update(ctx, 0, "cancelled", "register", nil, func(*Catalog) ([]string, error) { t.Fatal("cancelled lock acquired"); return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "catalog.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel wrote catalog", err)
	}
}

func TestCatalogAndDirectoryLinksRejected(t *testing.T) {
	s, dir := testStore(t)
	seedStore(t, s)
	file := filepath.Join(dir, "catalog.json")
	outside := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); err == nil {
		t.Fatal("catalog link read")
	}
	_, err := s.update(context.Background(), 0, "link", "register", nil, func(*Catalog) ([]string, error) { return nil, nil })
	if err == nil {
		t.Fatal("catalog link replaced")
	}
	if string(mustReadFile(t, outside)) != "keep" {
		t.Fatal("outside modified")
	}
	other := filepath.Join(t.TempDir(), "new-parent")
	pending, err := NewStore(filepath.Join(other, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(t.TempDir(), other); err != nil {
		t.Fatal(err)
	}
	_, err = pending.update(context.Background(), 0, "parent-link", "register", nil, func(*Catalog) ([]string, error) { return nil, nil })
	if err == nil {
		t.Fatal("new ancestor symlink accepted")
	}
}

func TestStoreProcessHelper(t *testing.T) {
	dir := os.Getenv("WORKSHELF_VERSION_TEST_DIR")
	if dir == "" {
		return
	}
	name := os.Getenv("WORKSHELF_VERSION_TEST_NAME")
	s, err := NewStore(dir)
	if err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(dir), name+"-ready"), nil, 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = s.update(ctx, 0, name, "register", nil, func(*Catalog) ([]string, error) {
		if e := os.WriteFile(filepath.Join(filepath.Dir(dir), name+"-entered"), nil, 0o600); e != nil {
			return nil, e
		}
		for {
			if _, e := os.Stat(filepath.Join(filepath.Dir(dir), "release")); e == nil {
				break
			}
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil, nil
	})
	if errors.Is(err, ErrConflict) {
		os.Exit(3)
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestSeparateProcessesCannotLoseUpdates(t *testing.T) {
	_, dir := testStore(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var processes []*exec.Cmd
	defer func() {
		for _, p := range processes {
			if p.ProcessState == nil {
				_ = p.Process.Kill()
				_ = p.Wait()
			}
		}
	}()
	for _, name := range []string{"a", "b"} {
		cmd := exec.Command(exe, "-test.run=^TestStoreProcessHelper$")
		cmd.Env = append(os.Environ(), "WORKSHELF_VERSION_TEST_DIR="+dir, "WORKSHELF_VERSION_TEST_NAME="+name)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, cmd)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "*-ready"))
		entered, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "*-entered"))
		if len(ready) == 2 && len(entered) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process startup timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	entered, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "*-entered"))
	if len(entered) != 1 {
		t.Fatal("two processes entered mutation concurrently", entered)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	codes := map[int]int{}
	for _, p := range processes {
		_ = p.Wait()
		codes[p.ProcessState.ExitCode()]++
	}
	if codes[0] != 1 || codes[3] != 1 {
		t.Fatal("expected one commit and one revision conflict", codes)
	}
}
