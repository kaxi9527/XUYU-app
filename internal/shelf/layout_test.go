package shelf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

func TestEveryCatalogWorkTypeSurvivesProjectReadback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "00-说明.md")
	for _, wt := range worktype.All() {
		if err := os.WriteFile(path, []byte("# 项目\n- 工种："+wt.Label()+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got := ProjectWorkTypes(path)
		if len(got) != 1 || got[0] != wt.Label() {
			t.Fatalf("label %q read as %#v", wt.Label(), got)
		}
	}
}
