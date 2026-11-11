package graph

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func TestFileGraphSourcesAndCustomTemplate(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	cfg.Validate()
	project := filepath.Join(cfg.Root, "设计项目")
	dir := filepath.Join(project, "自定模板", "源文件", "插画", "首屏")
	file := filepath.Join(dir, "海报.svg")
	writeFile(t, filepath.Join(project, cfg.Names.Tasks, "brief.txt"), "brief")
	writeFile(t, file, "source")
	writeFile(t, filepath.Join(dir, ".workshelf-origin.jsonl"), "{\"file\":\"海报.svg\",\"agent\":\"codex\"}\n{\"file\":\"海报.svg\",\"agent\":\"claude\"}\n{\"file\":\"海报.svg\",\"agent\":\"codex\"}\n")
	writeFile(t, filepath.Join(cfg.Root, "另一个项目", "other.txt"), "other")
	writeFile(t, filepath.Join(project, "node_modules", "a-package", "index.js"), "dependency")
	g := Files(shelf.Env{Config: cfg, Home: home, NoReg: true}, FileOpts{Project: "设计项目"})
	if g.ProjectKey != "root:设计项目" {
		t.Fatal(g.ProjectKey)
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		if strings.Contains(n.Path, "node_modules") {
			t.Fatal("dependency entered project graph")
		}
		if strings.Contains(n.Path, "另一个项目") {
			t.Fatal("cross-project node")
		}
	}
	if nodes[nodeID("file", file)].Path != file {
		t.Fatal("custom template disappeared beside standard folder")
	}
	count := 0
	for _, edge := range g.Edges {
		if nodes[edge.From].ID == "" || nodes[edge.To].ID == "" {
			t.Fatal("dangling edge", edge)
		}
		if edge.Type == EdgeSource {
			count++
			if edge.Confidence != "来源记录" || !strings.Contains(edge.Reason, "不证明当前内容作者") {
				t.Fatal("unverified claim upgraded", edge)
			}
		}
	}
	if count != 2 {
		t.Fatalf("wanted two agents, without duplicate edge; got %d %+v", count, g)
	}
	if !strings.Contains(g.Skipped, "扫描规则跳过") {
		t.Fatal("missing coverage note", g.Skipped)
	}
}

func TestGraphSourceBudgetAndUnknownTools(t *testing.T) {
	s := newSourceRecords()
	for i := 0; i < 20; i++ {
		s.add("/project/file", fmt.Sprintf("custom-%02d", i), "侧边标记")
	}
	s.add("/outside/file", "outsider", "资产索引")
	base := []Node{{ID: "file:one", Kind: "file", Path: "/project/file"}}
	nodes, edges, clipped := s.project(base, nil, map[string]string{"/project/file": "file:one"}, 400)
	if !clipped || len(nodes) != 17 || len(edges) != 16 {
		t.Fatalf("budget %d %d %v", len(nodes), len(edges), clipped)
	}
	for _, n := range nodes {
		if n.Agent == "outsider" {
			t.Fatal("outside claim admitted")
		}
	}
	_, edges, clipped = s.project(base, []Edge{{ID: "old", From: "a", To: "b", Type: EdgeCopyName}}, map[string]string{"/project/file": "file:one"}, 2)
	if !clipped || len(edges) != 2 || edges[0].Type != EdgeSource {
		t.Fatal("source priority / total budget")
	}
}
