package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/merge"
	"github.com/kaxi9527/WorkShelf/internal/registry"
	"github.com/kaxi9527/WorkShelf/internal/scan"
	"github.com/kaxi9527/WorkShelf/internal/shelf"
)

// Projects 构建项目关系网（只读）。
// 信号：登记组成员、同 CopyKey 合并建议、同一 git origin/identity；挂靠文件默认做徽章。
func Projects(e shelf.Env) ProjectGraphResult {
	e.Config.Validate()
	list := shelf.Projects(e)
	nodesByPath := map[string]*Node{}
	var nodes []Node
	seenEdge := map[string]bool{}
	var edges []Edge
	sampledGroups := 0

	ensure := func(p shelf.ProjectSummary) string {
		key := filepath.Clean(p.Path)
		if n, ok := nodesByPath[key]; ok {
			return n.ID
		}
		id := nodeID("project", key)
		n := Node{
			ID:          id,
			Kind:        "project",
			Name:        p.Name,
			Path:        key,
			Source:      p.Source,
			WorkTypes:   p.WorkTypes,
			MemberCount: p.Members,
			ProjectID:   p.ID,
			Project:     p.Name,
			Extra:       p.Origin,
		}
		if len(n.WorkTypes) == 0 {
			n.WorkTypes = workTypesOf(e.Config, key, p.Name, p.Source)
		}
		nodes = append(nodes, n)
		nodesByPath[key] = &nodes[len(nodes)-1]
		return id
	}

	for _, p := range list {
		if p.Linked {
			continue
		}
		ensure(p)
	}

	// 1) 登记组内成员边 + 挂靠计数
	if e.Reg != nil {
		for _, p := range e.Reg.Projects {
			main := filepath.Clean(p.Path)
			mainID := ""
			if n, ok := nodesByPath[main]; ok {
				mainID = n.ID
			} else {
				// 登记有、Projects 列表可能因路径不在而漏：补节点
				sum := shelf.ProjectSummary{
					Name: p.Name, Path: main, Source: shelf.SourceInPlace,
					ID: p.ID, Origin: p.Origin, Members: len(p.Members) - 1,
				}
				mainID = ensure(sum)
			}
			attach := 0
			var siblings []string // 同组非主版本成员（副本/worktree/clone…），彼此也连
			for _, m := range p.Members {
				mp := filepath.Clean(m.Path)
				if mp == main {
					continue
				}
				if isAttachMember(main, m) {
					attach++
					continue
				}
				// 成员若本身也是项目节点，连边；否则为目录成员也建轻量项目节点
				toID := ""
				if n, ok := nodesByPath[mp]; ok {
					toID = n.ID
				} else if m.IsDir {
					sum := shelf.ProjectSummary{
						Name: filepath.Base(mp), Path: mp, Source: shelf.SourceInPlace,
					}
					toID = ensure(sum)
					if nn := nodesByPath[mp]; nn != nil {
						nn.Extra = m.Kind
					}
				} else {
					attach++
					continue
				}
				addEdge(&edges, seenEdge, Edge{
					Type:       EdgeMember,
					From:       mainID,
					To:         toID,
					Reason:     fmtMemberReason(m.Kind),
					Weight:     memberWeight(m.Kind),
					Confidence: ConfHigh,
				})
				siblings = append(siblings, toID)
			}
			// 副本之间也互连（交叉），不是只有主版本一个中心
			pairs, _ := meshPairs(siblings, CliqueMax, 0)
			for _, pr := range pairs {
				addEdge(&edges, seenEdge, Edge{
					Type:       EdgeMember,
					From:       pr[0],
					To:         pr[1],
					Reason:     "同属登记组「" + p.Name + "」的另一份（副本/worktree 之间）",
					Weight:     0.6,
					Confidence: ConfMid,
				})
			}
			if n := nodesByPath[main]; n != nil {
				n.AttachCount = attach
				if n.MemberCount < len(p.Members)-1 {
					n.MemberCount = len(p.Members) - 1
				}
			}
		}
	}

	// 2) 同 CopyKey 合并建议
	for _, c := range merge.Suggest(e) {
		if len(c.Paths) < 2 {
			continue
		}
		reason := "名字去后缀后相同"
		if len(c.Reasons) > 0 {
			reason = strings.Join(c.Reasons, "；")
		}
		// 组内两两交叉相连（小团全连；大团环+弦抽样）
		var ids []string
		for _, p := range c.Paths {
			p = filepath.Clean(p)
			if n, ok := nodesByPath[p]; ok {
				ids = append(ids, n.ID)
				continue
			}
			sum := shelf.ProjectSummary{Name: filepath.Base(p), Path: p, Source: shelf.SourceInPlace}
			ids = append(ids, ensure(sum))
		}
		pairs, sampled := meshPairs(ids, CliqueMax, 0)
		if sampled {
			sampledGroups++
		}
		for _, pr := range pairs {
			addEdge(&edges, seenEdge, Edge{
				Type:       EdgeCopyKey,
				From:       pr[0],
				To:         pr[1],
				Reason:     "合并建议：" + reason,
				Weight:     0.9,
				Confidence: ConfMid,
			})
		}
	}

	// 3) 同一 git origin / identity
	byIdent := map[string][]string{}
	addIdent := func(ident, path string) {
		ident = strings.TrimSpace(ident)
		if ident == "" {
			return
		}
		path = filepath.Clean(path)
		if _, ok := nodesByPath[path]; !ok {
			return
		}
		byIdent[ident] = append(byIdent[ident], path)
	}
	if e.Reg != nil {
		for _, p := range e.Reg.Projects {
			ident := ""
			if p.Origin != "" {
				ident = scan.NormalizeRemote(p.Origin)
			}
			if ident == "" {
				// 尝试读主路径 git
				if g, ok := readGitQuiet(p.Path); ok {
					ident = g
				}
			}
			addIdent(ident, p.Path)
			for _, m := range p.Members {
				if !m.IsDir {
					continue
				}
				if g, ok := readGitQuiet(m.Path); ok {
					addIdent(g, m.Path)
				}
			}
		}
	}
	// 总文件夹项目也扫一层 git
	for _, p := range list {
		if p.Source != shelf.SourceRoot {
			continue
		}
		if g, ok := readGitQuiet(p.Path); ok {
			addIdent(g, p.Path)
		}
		// 代码/ 下一层若是独立仓且已在节点表，补 identity
		codeDir := filepath.Join(p.Path, e.Config.Names.Code)
		ents, err := os.ReadDir(codeDir)
		if err != nil {
			continue
		}
		for _, en := range ents {
			if !en.IsDir() {
				continue
			}
			sub := filepath.Join(codeDir, en.Name())
			if _, ok := nodesByPath[filepath.Clean(sub)]; !ok {
				continue
			}
			if g, ok := readGitQuiet(sub); ok {
				addIdent(g, sub)
			}
		}
	}
	for _, ident := range sortedKeys(byIdent) {
		uniq := uniqClean(byIdent[ident])
		if len(uniq) < 2 {
			continue
		}
		var ids []string
		for _, u := range uniq {
			if n := nodesByPath[u]; n != nil {
				ids = append(ids, n.ID)
			}
		}
		label := shortIdentity(ident)
		pairs, sampled := meshPairs(ids, CliqueMax, 0)
		if sampled {
			sampledGroups++
		}
		for _, pr := range pairs {
			addEdge(&edges, seenEdge, Edge{
				Type:       EdgeGit,
				From:       pr[0],
				To:         pr[1],
				Reason:     fmt.Sprintf("同一 git 仓库（%s）", label),
				Weight:     1.0,
				Confidence: ConfHigh,
			})
		}
	}

	// 把指针写回切片（AttachCount 等可能改过）
	outNodes := make([]Node, 0, len(nodesByPath))
	seenN := map[string]bool{}
	for _, n := range nodes {
		key := filepath.Clean(n.Path)
		if seenN[key] {
			continue
		}
		seenN[key] = true
		if cur := nodesByPath[key]; cur != nil {
			outNodes = append(outNodes, *cur)
		} else {
			outNodes = append(outNodes, n)
		}
	}
	sortNodes(outNodes)
	sortEdges(edges)
	res := ProjectGraphResult{Nodes: outNodes, Edges: edges}
	if sampledGroups > 0 {
		res.Skipped = fmt.Sprintf("有 %d 组大团（超过 %d 个项目）已抽样成环+弦交叉，完整两两交叉会太密", sampledGroups, CliqueMax)
	}
	return res
}

func memberWeight(kind string) float64 {
	switch kind {
	case registry.KindWorktree, registry.KindClone:
		return 1.0
	case registry.KindCopy, registry.KindMain:
		return 0.95
	case registry.KindHandoff, registry.KindArchive:
		return 0.7
	case registry.KindNested:
		return 0.6
	default:
		return 0.5
	}
}

func uniqClean(ps []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		p = filepath.Clean(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// readGitQuiet 只取 identity，失败返回 false（不报错）。
func readGitQuiet(dir string) (string, bool) {
	// 复用 scan 包的只读 git：通过探测 .git
	// scan 没导出 readGit，这里轻量读 origin/identity。
	return gitIdentity(dir)
}
