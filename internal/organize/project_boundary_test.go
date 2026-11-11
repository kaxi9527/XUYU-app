package organize

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectBoundaryProtectsEveryPlacementMode(t *testing.T) {
	for _, merge := range []bool{false, true} {
		for _, target := range []bool{false, true} {
			t.Run(map[bool]string{false: "whole", true: "merge"}[merge]+"/"+map[bool]string{false: "source", true: "target"}[target], func(t *testing.T) {
				cfg, h := fixture(t)
				source := filepath.Join(h, "Desktop", "app", "assets", "image.png")
				write(t, source, "image")
				req := mergeRequest(source)
				req.Merge = merge
				marker := filepath.Join(h, "Desktop", "app", "package.json")
				if target {
					marker = filepath.Join(cfg.Root, req.Project, "package.json")
				}
				write(t, marker, "{}")
				plan, err := Build(cfg, h, req)
				if err != nil {
					t.Fatal(err)
				}
				if len(selected(plan)) != 0 {
					t.Fatalf("partial project operation escaped protection: %+v", plan.Rows)
				}
				got, err := os.ReadFile(source)
				if err != nil || string(got) != "image" {
					t.Fatal("preview changed source")
				}
			})
		}
	}
}

func TestProjectBoundaryRecheckedBeforeEveryPlacementMode(t *testing.T) {
	for _, merge := range []bool{false, true} {
		for _, target := range []bool{false, true} {
			t.Run(map[bool]string{false: "whole", true: "merge"}[merge]+"/"+map[bool]string{false: "source", true: "target"}[target], func(t *testing.T) {
				cfg, h := fixture(t)
				source := filepath.Join(h, "Desktop", "app", "assets", "image.png")
				write(t, source, "image")
				req := mergeRequest(source)
				req.Merge = merge
				plan, err := Build(cfg, h, req)
				if err != nil || len(selected(plan)) != 1 {
					t.Fatalf("%+v %v", plan, err)
				}
				marker := filepath.Join(h, "Desktop", "app", ".git")
				if target {
					marker = filepath.Join(cfg.Root, req.Project, ".git")
				}
				write(t, marker, "gitdir: elsewhere")
				if _, err := plan.Apply(cfg, selected(plan)); err == nil {
					t.Fatal("project boundary changed but execution was accepted")
				}
				got, err := os.ReadFile(source)
				if err != nil || string(got) != "image" {
					t.Fatal("source moved")
				}
			})
		}
	}
}
