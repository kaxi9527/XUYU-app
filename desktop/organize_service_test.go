package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/organize"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func TestOrganizerDoesNotOfferLinkedProjectAsDestination(t *testing.T) {
	svc, cfg, project, _ := versionFixture(t)
	if err := os.Symlink(project, filepath.Join(cfg.Root, "链接项目")); err != nil {
		t.Fatal(err)
	}
	context, err := svc.OrganizeContext("")
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range context.Projects {
		if choice.Key == "root:链接项目" {
			t.Fatal("linked project was offered as an organizer target")
		}
	}
}

func TestOrganizerCancellationDoesNotPublishOrMove(t *testing.T) {
	h := t.TempDir()
	sourceDir := filepath.Join(h, "Desktop")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := os.WriteFile(filepath.Join(sourceDir, fmt.Sprintf("image-%d.png", i)), []byte("image"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	svc := &ShelfService{}
	finished := make(chan error, 1)
	go func() {
		_, err := svc.organizer.preview(config.Default(h), h, organize.Request{Project: "取消验收", Sources: []string{sourceDir}, WorkTypes: []string{"设计·UI原型"}, Merge: true, PreviewID: "cancel-acceptance"})
		finished <- err
	}()
	deadline := time.After(3 * time.Second)
	for {
		if svc.OrganizeCancelPreview("cancel-acceptance") {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("preview ended before cancellation: %v", err)
		case <-deadline:
			t.Fatal("preview never registered")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(svc.organizer.plans) != 0 || len(svc.organizer.pending) != 0 {
		t.Fatal("cancelled preview was retained")
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil || len(entries) != 1000 {
		t.Fatal("cancel changed fixture", err, len(entries))
	}
}

func TestOrganizerPreviewTokenAndCatalogReadback(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	src := filepath.Join(h, "brief.md")
	if e := os.WriteFile(src, []byte("requirements"), 0644); e != nil {
		t.Fatal(e)
	}
	store := organizePlans{}
	p, e := store.preview(cfg, h, organize.Request{Project: "设计项目", Sources: []string{src}, WorkTypes: []string{"设计·UI原型"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Token) != 32 || len(p.Rows) != 1 {
		t.Fatalf("%+v", p)
	}
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	var wire map[string]any
	if e := json.Unmarshal(b, &wire); e != nil || wire["token"] != p.Token || wire["rows"] == nil {
		t.Fatal("wire contract", e)
	}
	taken, e := store.take(p.Token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := store.take(p.Token); e == nil {
		t.Fatal("replayed token")
	}
	sum, e := taken.Apply(cfg, []string{p.Rows[0].ID})
	if e != nil || sum.Moved != 1 {
		t.Fatalf("%+v %v", sum, e)
	}
	if e := recordLayout(cfg, p); e != nil {
		t.Fatal(e)
	}
	d, e := shelf.Project(shelf.Env{Config: cfg, Home: h, NoReg: true}, p.Project)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.WorkTypes) != 1 || len(d.Folders) != 5 {
		t.Fatalf("layout lost on readback: %+v", d)
	}
	items := shelf.Recent(shelf.Env{Config: cfg, Home: h, NoReg: true}, 1, 100)
	found := false
	for _, it := range items {
		if it.Path == p.Rows[0].Target {
			found = true
			if it.Placement != shelf.PlacedOK {
				t.Fatalf("organized file misclassified: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("organized file missing from recent")
	}
}
