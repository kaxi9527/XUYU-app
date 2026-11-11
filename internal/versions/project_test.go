package versions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectIdentityWithoutWorksAndAlias(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "ledger"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Read(context.Background())
	if err != nil || len(c.Projects) != 0 {
		t.Fatal(c, err)
	}
	ref := ProjectInput{Key: "root:项目", Name: "项目", Path: dir}
	r, err := store.EnsureProject(context.Background(), 0, "first", ref)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.EnsureProject(context.Background(), 0, "first", ref)
	if err != nil || retry.Revision != r.Revision || retry.EntityIDs[0] != r.EntityIDs[0] {
		t.Fatal(retry, err)
	}
	ref.Key = "place:alias"
	r2, err := store.EnsureProject(context.Background(), r.Revision, "alias", ref)
	if err != nil || r2.EntityIDs[0] != r.EntityIDs[0] {
		t.Fatal(r2, err)
	}
	c, err = store.Read(context.Background())
	if err != nil || len(c.Works) != 0 || len(c.Projects) != 1 {
		t.Fatal(c, err)
	}
	ref.Key = "root:项目"
	ref.Path = filepath.Join(dir, "renamed")
	if err = os.Mkdir(ref.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnsureProject(context.Background(), c.Revision, "relocate", ref); !errors.Is(err, ErrProjectLocationChanged) {
		t.Fatal(err)
	}
	ref.Path = dir
	ref.Name = "changed request"
	if _, err = store.EnsureProject(context.Background(), c.Revision, "first", ref); !errors.Is(err, ErrOperationReused) {
		t.Fatal(err)
	}
}
