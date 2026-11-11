package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectGraphCopyKeyAndMember(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	cfg := config.Default(home)
	cfg.Root = root
	cfg.Validate()

	// 总文件夹项目「官网」
	site := filepath.Join(root, "官网")
	writeFile(t, filepath.Join(site, cfg.Names.Readme), "# 官网\n\n工种：开发·前端\n")
	_ = os.MkdirAll(filepath.Join(site, cfg.Names.Tasks), 0o755)

	// 原地副本
	copyDir := filepath.Join(home, "Projects", "官网 副本")
	writeFile(t, filepath.Join(copyDir, "index.html"), "<html></html>")

	reg := &registry.Registry{
		GeneratedAt: time.Now(),
		Projects: []registry.Project{{
			ID:   "p1",
			Name: "官网",
			Path: copyDir,
			Members: []registry.Member{
				{Path: copyDir, Kind: registry.KindMain, IsDir: true},
				{Path: filepath.Join(home, "Downloads", "官网-交付.zip"), Kind: registry.KindArchive, IsDir: false},
			},
		}},
	}
	// 挂靠压缩包文件要存在
	writeFile(t, filepath.Join(home, "Downloads", "官网-交付.zip"), "PK\x03\x04fake")

	e := shelf.Env{Config: cfg, Home: home, Reg: reg, NoReg: false}
	g := Projects(e)

	if len(g.Nodes) < 2 {
		t.Fatalf("期望至少 2 个项目节点，得到 %d: %+v", len(g.Nodes), g.Nodes)
	}
	var hasCopyKey, hasMemberOrAttach bool
	for _, e := range g.Edges {
		if e.Type == EdgeCopyKey {
			hasCopyKey = true
		}
		if e.Type == EdgeMember {
			hasMemberOrAttach = true
		}
	}
	// 挂靠是徽章；副本若被 Projects() 列为原地项目，应有 copykey 边（总文件夹「官网」与「官网 副本」）
	if !hasCopyKey {
		t.Fatalf("期望有 copykey 边，edges=%+v nodes=%+v", g.Edges, nodeNames(g.Nodes))
	}
	// 附件徽章
	foundAttach := false
	for _, n := range g.Nodes {
		if n.AttachCount > 0 {
			foundAttach = true
			break
		}
	}
	if !foundAttach && !hasMemberOrAttach {
		t.Fatalf("期望挂靠徽章或成员边，nodes=%+v edges=%+v", g.Nodes, g.Edges)
	}
}

func TestProjectGraphDoesNotTreatLinksAsCopies(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	project := filepath.Join(cfg.Root, "项目")
	writeFile(t, filepath.Join(project, "说明.md"), "project")
	if err := os.Symlink(project, filepath.Join(cfg.Root, "项目 副本")); err != nil {
		t.Fatal(err)
	}
	g := Projects(shelf.Env{Config: cfg, Home: home, NoReg: true})
	if len(g.Nodes) != 1 || g.Nodes[0].Path != project || len(g.Edges) != 0 {
		t.Fatalf("link was represented as another project: %+v", g)
	}
}

func nodeNames(ns []Node) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Name+"@"+n.Path)
	}
	return out
}

func TestFileGraphSameTaskAndDupAndCopyName(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	cfg := config.Default(home)
	cfg.Root = root
	cfg.Validate()

	proj := filepath.Join(root, "海报")
	task := filepath.Join(proj, cfg.Names.Tasks, "2026-11-05-封面")
	writeFile(t, filepath.Join(proj, cfg.Names.Readme), "# 海报\n\n工种：设计·主图\n")
	body := "same-bytes-for-dup-test-aaaa"
	writeFile(t, filepath.Join(task, "封面.png"), body)
	writeFile(t, filepath.Join(task, "封面 副本.png"), body) // 同内容 + StripVersion 同名
	writeFile(t, filepath.Join(task, "说明.md"), "# note")

	e := shelf.Env{Config: cfg, Home: home, NoReg: true}
	g := Files(e, FileOpts{Project: "海报", MaxFiles: 50, MaxHash: 20})

	if g.Project != "海报" {
		t.Fatalf("project=%q", g.Project)
	}
	if len(g.Nodes) < 3 {
		t.Fatalf("nodes=%d %+v", len(g.Nodes), g.Nodes)
	}
	var hasSame, hasDup, hasCopyName bool
	for _, e := range g.Edges {
		switch e.Type {
		case EdgeSameDir:
			hasSame = true
		case EdgeDuplicate:
			hasDup = true
		case EdgeCopyName:
			hasCopyName = true
		}
	}
	if !hasSame {
		t.Fatalf("期望同任务边，edges=%+v", g.Edges)
	}
	if !hasDup {
		t.Fatalf("期望同 sha256 边，edges=%+v skipped=%s", g.Edges, g.Skipped)
	}
	if !hasCopyName {
		t.Fatalf("期望 copyname 边，edges=%+v", g.Edges)
	}
	// 核对哈希真相同
	a := filepath.Join(task, "封面.png")
	b := filepath.Join(task, "封面 副本.png")
	sa, _ := put.FileSHA256(a)
	sb, _ := put.FileSHA256(b)
	if sa == "" || sa != sb {
		t.Fatalf("sha mismatch %s %s", sa, sb)
	}
}

func TestFileGraphEmptyProject(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	_ = os.MkdirAll(cfg.Root, 0o755)
	e := shelf.Env{Config: cfg, Home: home, NoReg: true}
	g := Files(e, FileOpts{Project: "不存在的项目"})
	if g.Skipped == "" && len(g.Nodes) != 0 {
		t.Fatalf("期望跳过或空图，got %+v", g)
	}
}

// 同目录 3 个文件应是三角形（3 条兄弟边），不是 hub 星形的 2 条。
func TestFileGraphSameDirIsMeshNotStar(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	cfg := config.Default(home)
	cfg.Root = root
	cfg.Validate()
	proj := filepath.Join(root, "网状")
	task := filepath.Join(proj, cfg.Names.Tasks, "2026-11-06-三角")
	writeFile(t, filepath.Join(proj, cfg.Names.Readme), "# 网状\n")
	writeFile(t, filepath.Join(task, "甲.md"), "a")
	writeFile(t, filepath.Join(task, "乙.md"), "bb")
	writeFile(t, filepath.Join(task, "丙.md"), "ccc")
	e := shelf.Env{Config: cfg, Home: home, NoReg: true}
	g := Files(e, FileOpts{Project: "网状"})

	deg := map[string]int{}
	same := 0
	for _, ed := range g.Edges {
		if ed.Type == EdgeSameDir {
			same++
			deg[ed.From]++
			deg[ed.To]++
		}
		if ed.Type == EdgeParent {
			t.Fatalf("默认不应有文件夹→文件 parent 边：%+v", ed)
		}
	}
	if same < 3 {
		t.Fatalf("同目录 3 文件期望 ≥3 条兄弟边（三角形），得到 %d：%+v", same, g.Edges)
	}
	for id, d := range deg {
		if d != 2 {
			t.Fatalf("三角形里每个文件度数应为 2，%s=%d", id, d)
		}
	}
	for _, n := range g.Nodes {
		if n.Kind == "folder" {
			t.Fatalf("默认不画文件夹节点（伪一对多噪声）：%+v", n)
		}
	}
}

// 大团不退回星形：环+弦，且没有一个点连满全部。
func TestMeshPairsLargeGroupNotStar(t *testing.T) {
	var ids []string
	for i := 0; i < 12; i++ {
		ids = append(ids, string(rune('a'+i)))
	}
	pairs, sampled := meshPairs(ids, CliqueMax, 0)
	if !sampled {
		t.Fatal("12 个点应标记为抽样")
	}
	deg := map[string]int{}
	for _, p := range pairs {
		deg[p[0]]++
		deg[p[1]]++
	}
	for id, d := range deg {
		if d < 2 {
			t.Fatalf("%s 度数 %d，应至少在环上", id, d)
		}
		if d >= len(ids)-1 {
			t.Fatalf("%s 连满全部，退回了星形", id)
		}
	}
	if len(deg) != len(ids) {
		t.Fatalf("每个点都应出现，got %d", len(deg))
	}
	small, s2 := meshPairs([]string{"x", "y", "z", "w"}, CliqueMax, 0)
	if s2 || len(small) != 6 {
		t.Fatalf("4 点小团应两两全连 6 条，got %d sampled=%v", len(small), s2)
	}
	cut, s3 := meshPairs([]string{"x", "y", "z", "w"}, CliqueMax, 4)
	if !s3 || len(cut) > 4 {
		t.Fatalf("预算 4 时应抽样且不超预算，got %d", len(cut))
	}
}

// 同一登记组里两个副本之间也要有边，不是只有主版本一个中心。
func TestProjectGraphMembersCross(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	cfg.Validate()
	_ = os.MkdirAll(cfg.Root, 0o755)
	main := filepath.Join(home, "Projects", "App")
	c1 := filepath.Join(home, "Projects", "App-wt1")
	c2 := filepath.Join(home, "Projects", "App-wt2")
	for _, d := range []string{main, c1, c2} {
		writeFile(t, filepath.Join(d, "x.txt"), "x")
	}
	reg := &registry.Registry{GeneratedAt: time.Now(), Projects: []registry.Project{{
		ID: "p9", Name: "App", Path: main,
		Members: []registry.Member{
			{Path: main, Kind: registry.KindMain, IsDir: true},
			{Path: c1, Kind: registry.KindWorktree, IsDir: true},
			{Path: c2, Kind: registry.KindWorktree, IsDir: true},
		},
	}}}
	g := Projects(shelf.Env{Config: cfg, Home: home, Reg: reg})
	id1, id2 := nodeID("project", c1), nodeID("project", c2)
	found := false
	members := 0
	for _, e := range g.Edges {
		if e.Type == EdgeMember {
			members++
			if (e.From == id1 && e.To == id2) || (e.From == id2 && e.To == id1) {
				found = true
			}
		}
	}
	if !found || members < 3 {
		t.Fatalf("期望主↔两副本 + 副本↔副本共 ≥3 条成员边，got %d edges=%+v", members, g.Edges)
	}
}

// ExpandAttach=true：项目目录外的挂靠副本要进节点，并和项目内同名/同内容文件有边；
// false：只计数，Skipped 提示未展开。
func TestFileGraphExpandAttachOutsideCopy(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Work")
	cfg := config.Default(home)
	cfg.Root = root
	cfg.Validate()

	proj := filepath.Join(root, "海报")
	task := filepath.Join(proj, cfg.Names.Tasks, "2026-11-06-封面")
	writeFile(t, filepath.Join(proj, cfg.Names.Readme), "# 海报\n")
	body := "attach-dup-bytes-zzzz"
	inner := filepath.Join(task, "封面终稿.png")
	writeFile(t, inner, body)

	outside := filepath.Join(home, "Desktop", "封面终稿 副本.png")
	writeFile(t, outside, body)
	zipPath := filepath.Join(home, "Downloads", "海报-交付.zip")
	writeFile(t, zipPath, "PK\x03\x04fakezip")

	reg := &registry.Registry{GeneratedAt: time.Now(), Projects: []registry.Project{{
		ID: "place-海报", Name: "海报", Path: proj,
		Members: []registry.Member{
			{Path: proj, Kind: registry.KindMain, IsDir: true},
			{Path: outside, Kind: registry.KindFile, IsDir: false},
			{Path: zipPath, Kind: registry.KindArchive, IsDir: false},
		},
	}}}
	e := shelf.Env{Config: cfg, Home: home, Reg: reg}

	off := Files(e, FileOpts{Project: "海报", ExpandAttach: false})
	if off.AttachCount != 2 {
		t.Fatalf("AttachCount want 2 got %d skipped=%s", off.AttachCount, off.Skipped)
	}
	if off.AttachShown != 0 || off.AttachHidden != 2 {
		t.Fatalf("未展开时 shown=%d hidden=%d", off.AttachShown, off.AttachHidden)
	}
	for _, n := range off.Nodes {
		if n.Path == outside || n.Path == zipPath {
			t.Fatalf("ExpandAttach=false 不应出现挂靠节点：%+v", n)
		}
	}
	if !strings.Contains(off.Skipped, "未展开") && !strings.Contains(off.Skipped, "其他文件夹") {
		t.Fatalf("期望 Skipped 提示未展开，got %q", off.Skipped)
	}

	on := Files(e, FileOpts{Project: "海报", ProjectID: "place-海报", ExpandAttach: true})
	if on.ProjectID != "place-海报" {
		t.Fatalf("ProjectID=%q", on.ProjectID)
	}
	if on.AttachShown < 2 {
		t.Fatalf("展开后 AttachShown=%d nodes=%+v skipped=%s", on.AttachShown, nodeNames(on.Nodes), on.Skipped)
	}
	var hasOutside, hasZip bool
	for _, n := range on.Nodes {
		if n.Path == outside {
			hasOutside = true
			if n.Extra == "" || !strings.Contains(n.Extra, "项目外") {
				t.Fatalf("挂靠节点应标 Extra：%+v", n)
			}
		}
		if n.Path == zipPath {
			hasZip = true
		}
	}
	if !hasOutside || !hasZip {
		t.Fatalf("展开后应有桌面副本和下载 zip：outside=%v zip=%v nodes=%v", hasOutside, hasZip, nodeNames(on.Nodes))
	}
	idInner, idOut := nodeID("file", inner), nodeID("file", outside)
	var hasDupOrCopy bool
	var attachReason bool
	for _, ed := range on.Edges {
		pair := (ed.From == idInner && ed.To == idOut) || (ed.From == idOut && ed.To == idInner)
		if !pair {
			continue
		}
		if ed.Type == EdgeDuplicate || ed.Type == EdgeCopyName {
			hasDupOrCopy = true
		}
		if strings.Contains(ed.Reason, "在项目外的相关文件") {
			attachReason = true
		}
	}
	if !hasDupOrCopy {
		t.Fatalf("期望项目内封面与桌面副本有 dup/copyname 边，edges=%+v", on.Edges)
	}
	if !attachReason {
		t.Fatalf("期望边理由注明项目外相关文件，edges=%+v", on.Edges)
	}
}

func TestResolveProjectPrefersID(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Work")
	cfg.Validate()
	_ = os.MkdirAll(cfg.Root, 0o755)
	a := filepath.Join(home, "Projects", "同名-A")
	b := filepath.Join(home, "Projects", "同名-B")
	writeFile(t, filepath.Join(a, "x.txt"), "a")
	writeFile(t, filepath.Join(b, "x.txt"), "b")
	reg := &registry.Registry{GeneratedAt: time.Now(), Projects: []registry.Project{
		{ID: "id-a", Name: "同名", Path: a},
		{ID: "id-b", Name: "同名", Path: b},
	}}
	e := shelf.Env{Config: cfg, Home: home, Reg: reg}
	name, dir, note, id := resolveProject(e, FileOpts{Project: "同名", ProjectID: "id-b"})
	if name != "同名" || dir != b || id != "id-b" {
		t.Fatalf("got name=%s dir=%s id=%s note=%s", name, dir, id, note)
	}
	// 无 ID、总文件夹也没有：按名仍能落到登记（歧义取可得的）
	name2, dir2, _, id2 := resolveProject(e, FileOpts{Project: "同名"})
	if name2 != "同名" || dir2 == "" || id2 == "" {
		t.Fatalf("按名回退失败 name=%s dir=%s id=%s", name2, dir2, id2)
	}
}
