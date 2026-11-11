// Package graph 给出「项目关系网 / 文件关系网」只读图数据。
// 不自动合并、不自动搬家；边带类型、理由和置信度，供界面画图。
package graph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// 边类型（项目关系）
const (
	EdgeMember  = "member"  // 登记组内：主版本 ↔ 副本 / worktree / 相关包
	EdgeCopyKey = "copykey" // 同 CopyKey 合并建议
	EdgeGit     = "git"     // 同一 git origin / identity
	EdgeAttach  = "attach"  // 挂靠：文件归到某项目但还在项目外（默认用徽章，展开才出边）
)

// 边类型（文件关系）
const (
	EdgeSameDir   = "same_dir"      // 同任务目录 / 同成品目录（兄弟）
	EdgeParent    = "parent"        // 目录 ↔ 其下文件（可选，默认不画以免爆炸）
	EdgeDuplicate = "dup"           // 同 sha256 副本
	EdgeActivity  = "activity"      // 同 agent / 同 task / 同 workType
	EdgeCopyName  = "copyname"      // 文件名 StripVersion 后相同
	EdgeSource    = "source_record" // 来源记录中的工具关联，不证明当前字节的作者
)

// 置信度
const (
	ConfHigh = "较有把握"
	ConfMid  = "大致判断"
	ConfLow  = "待你定"
)

// Node 是图上的一个点。
type Node struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"` // project / file / folder
	Name           string   `json:"name"`
	Path           string   `json:"path,omitempty"`
	Source         string   `json:"source,omitempty"` // 总文件夹 / 原地
	WorkTypes      []string `json:"workTypes,omitempty"`
	MemberCount    int      `json:"memberCount,omitempty"`
	AttachCount    int      `json:"attachCount,omitempty"` // 挂靠文件数（徽章）
	ProjectID      string   `json:"projectId,omitempty"`
	Project        string   `json:"project,omitempty"`
	Size           int64    `json:"size,omitempty"`
	Extra          string   `json:"extra,omitempty"` // 如 Kind 文案、git identity 短标
	Agent          string   `json:"agent,omitempty"` // 来源记录的规范化工具名，不推断已安装/已加载
	WorkID         string   `json:"workId,omitempty"`
	VersionID      string   `json:"versionId,omitempty"`
	VersionCount   int      `json:"versionCount,omitempty"`
	State          string   `json:"state,omitempty"`
	Usable         bool     `json:"usable,omitempty"`
	RecordedUsable bool     `json:"recordedUsable,omitempty"`
	Reference      bool     `json:"reference,omitempty"` // 页外引用，未核对当前字节
}

// Edge 是一条关系。
type Edge struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Reason     string  `json:"reason"`
	Weight     float64 `json:"weight"`
	Confidence string  `json:"confidence"`
	Directed   bool    `json:"directed,omitempty"`
}

// ProjectGraphResult 项目关系网。
type ProjectGraphResult struct {
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
	Skipped string `json:"skipped,omitempty"` // 说明性备注
}

// FileGraphResult 文件关系网。
type FileGraphResult struct {
	Project      string `json:"project"`
	ProjectKey   string `json:"projectKey,omitempty"`
	ProjectID    string `json:"projectId,omitempty"`
	Nodes        []Node `json:"nodes"`
	Edges        []Edge `json:"edges"`
	Skipped      string `json:"skipped,omitempty"`
	Note         string `json:"note,omitempty"`
	AttachCount  int    `json:"attachCount,omitempty"`  // 登记里挂靠到本项目的外部文件数
	AttachShown  int    `json:"attachShown,omitempty"`  // 本次实际画进图的挂靠数
	AttachHidden int    `json:"attachHidden,omitempty"` // 因未展开或节点上限没画进来的挂靠数
}

// FileOpts 控制文件关系网范围与性能上限。
type FileOpts struct {
	Project       string // 总文件夹项目名；空则试 ProjectID / Recent
	ProjectID     string // 原地项目 ID（优先于同名）
	ExpandAttach  bool   // 是否把挂靠文件（桌面/下载等项目外相关文件）也扩进节点
	MaxFiles      int    // 最多纳入多少文件节点，默认 120
	MaxHash       int    // 最多算多少个文件的 sha256，默认 40
	MaxDupPairs   int    // 同哈希边最多对数，默认 60
	MaxEdges      int    // 总边数封顶，默认 400
	IncludeParent bool   // 是否加目录→文件 parent 边，默认 false
}

func (o *FileOpts) defaults() {
	if o.MaxFiles <= 0 {
		o.MaxFiles = 120
	}
	if o.MaxHash <= 0 {
		o.MaxHash = 40
	}
	if o.MaxDupPairs <= 0 {
		o.MaxDupPairs = 60
	}
	if o.MaxEdges <= 0 {
		o.MaxEdges = 400
	}
}

// Env 复用 shelf.Env。
type Env = shelf.Env

func nodeID(kind, path string) string {
	p := filepath.Clean(path)
	return kind + ":" + p
}

func edgeID(typ, a, b string) string {
	if a > b {
		a, b = b, a
	}
	return typ + ":" + a + "->" + b
}

func addEdge(edges *[]Edge, seen map[string]bool, e Edge) {
	if e.From == "" || e.To == "" || e.From == e.To {
		return
	}
	if e.ID == "" {
		e.ID = edgeID(e.Type, e.From, e.To)
	}
	if seen[e.ID] {
		return
	}
	seen[e.ID] = true
	if e.Weight <= 0 {
		e.Weight = 1
	}
	if e.Confidence == "" {
		e.Confidence = ConfMid
	}
	*edges = append(*edges, e)
}

func sortNodes(ns []Node) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Kind != ns[j].Kind {
			return ns[i].Kind < ns[j].Kind
		}
		return ns[i].Name < ns[j].Name
	})
}

func sortEdges(es []Edge) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Type != es[j].Type {
			return es[i].Type < es[j].Type
		}
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		return es[i].To < es[j].To
	})
}

func shortIdentity(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

func workTypesOf(cfg config.Config, path, name, source string) []string {
	if source == shelf.SourceRoot {
		readme := filepath.Join(cfg.Root, name, cfg.Names.Readme)
		return shelf.ProjectWorkTypes(readme)
	}
	// 原地：试说明文件
	for _, n := range []string{cfg.Names.Readme, "README.md", "readme.md"} {
		if wt := shelf.ProjectWorkTypes(filepath.Join(path, n)); len(wt) > 0 {
			return wt
		}
	}
	return nil
}

// 挂靠：登记成员里非目录，或不在主路径下的相关文件/交付包/压缩包。
func isAttachMember(mainPath string, m registry.Member) bool {
	if !m.IsDir {
		return true
	}
	switch m.Kind {
	case registry.KindFile, registry.KindArchive, registry.KindHandoff:
		clean := filepath.Clean(m.Path)
		main := filepath.Clean(mainPath)
		if clean == main {
			return false
		}
		if strings.HasPrefix(clean, main+string(filepath.Separator)) {
			return false
		}
		return true
	default:
		return false
	}
}

func fmtMemberReason(kind string) string {
	if kind == "" {
		return "登记为同一项目组成员"
	}
	return fmt.Sprintf("登记组成员：%s", kind)
}

// CliqueMax 是「小团全连接」的上限：组内不超过这么多个点就两两相连。
const CliqueMax = 8

// meshPairs 把一组相关节点变成「成对交叉边」，不是星形一对多。
//   - 组内 ≤ cliqueMax（且两两边数不超过 budget）：两两全连（小团），
//     例如同目录 3 个文件 → 3 条兄弟边（三角形）。
//   - 更大的组：环（i↔i+1）+ 跳二弦（i↔i+2）+ 对径弦（i↔i+n/2），
//     每个点约 3–5 度，依然是网状交叉，sampled=true 供调用方在 Skipped 里说明。
//
// 返回的 pair 已去重、顺序稳定（先按 id 排序）。budget<=0 表示不限。
func meshPairs(ids []string, cliqueMax, budget int) (pairs [][2]string, sampled bool) {
	uniq := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	sort.Strings(uniq)
	n := len(uniq)
	if n < 2 {
		return nil, false
	}
	full := n * (n - 1) / 2
	if n <= cliqueMax && (budget <= 0 || full <= budget) {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				pairs = append(pairs, [2]string{uniq[i], uniq[j]})
			}
		}
		return pairs, false
	}
	// 大团：环 + 弦
	have := map[[2]int]bool{}
	put := func(i, j int) {
		i, j = i%n, j%n
		if i == j {
			return
		}
		if i > j {
			i, j = j, i
		}
		k := [2]int{i, j}
		if have[k] {
			return
		}
		have[k] = true
		pairs = append(pairs, [2]string{uniq[i], uniq[j]})
	}
	for i := 0; i < n; i++ {
		put(i, i+1)
	}
	if n > 3 {
		for i := 0; i < n; i++ {
			put(i, i+2)
		}
	}
	if n > 5 {
		for i := 0; i < n/2; i++ {
			put(i, i+n/2)
		}
	}
	if budget > 0 && len(pairs) > budget {
		pairs = pairs[:budget]
	}
	return pairs, true
}

// sortedKeys 让 map 分组的遍历顺序稳定（边数封顶时结果可复现）。
func sortedKeys(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
