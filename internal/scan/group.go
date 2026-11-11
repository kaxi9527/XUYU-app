package scan

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// 重合度阈值：两个文件夹顶层条目的 Jaccard 重合度（交集/并集）≥ 0.5 且共同项 ≥ 3。
const (
	MinOverlap = 0.5
	MinCommon  = 3
)

type unionFind struct {
	parent []int
	ids    []map[string]bool // 每个集合里的 git 身份，防止两个不同仓库被副本「桥接」到一起
}

func newUF(n int) *unionFind {
	u := &unionFind{parent: make([]int, n), ids: make([]map[string]bool, n)}
	for i := range u.parent {
		u.parent[i] = i
		u.ids[i] = map[string]bool{}
	}
	return u
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

// union 合并两个集合；如果会把两个不同的 git 仓库合到一起就拒绝。
func (u *unionFind) union(a, b int) bool {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return true
	}
	if len(u.ids[ra]) > 0 && len(u.ids[rb]) > 0 {
		for k := range u.ids[ra] {
			if !u.ids[rb][k] {
				return false
			}
		}
		for k := range u.ids[rb] {
			if !u.ids[ra][k] {
				return false
			}
		}
	}
	u.parent[rb] = ra
	for k := range u.ids[rb] {
		u.ids[ra][k] = true
	}
	return true
}

// Overlap 计算两个顶层条目集合的 Jaccard 重合度和共同项数。
func Overlap(a, b map[string]bool) (float64, int) {
	common := 0
	for k := range a {
		if b[k] {
			common++
		}
	}
	union := len(a) + len(b) - common
	if union == 0 {
		return 0, 0
	}
	return float64(common) / float64(union), common
}

func groupProjects(res *Result) {
	ps := res.Projects
	n := len(ps)
	uf := newUF(n)
	reasons := map[[2]int]string{}
	for i := range ps {
		if ps[i].Git != nil {
			uf.ids[i][ps[i].Git.Identity] = true
		}
	}
	// 1. 同一个 git 身份（同一个 origin 或同一个共享 .git）→ 同一项目（worktree、第二个 clone）。
	byID := map[string][]int{}
	for i := range ps {
		if ps[i].Git != nil {
			byID[ps[i].Git.Identity] = append(byID[ps[i].Git.Identity], i)
		}
	}
	for _, idx := range byID {
		for _, j := range idx[1:] {
			uf.union(idx[0], j)
			reasons[[2]int{idx[0], j}] = "同一个 git 仓库"
		}
	}
	// 2. 去后缀后同名 + 至少一边不在 git 里 + 顶层重合度达标 → 手工副本。
	byName := map[string][]int{}
	for i := range ps {
		k := strings.ToLower(ps[i].BaseName)
		byName[k] = append(byName[k], i)
	}
	keys := make([]string, 0, len(byName))
	for k := range byName {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		idx := byName[k]
		for a := 0; a < len(idx); a++ {
			for b := a + 1; b < len(idx); b++ {
				i, j := idx[a], idx[b]
				if ps[i].Git != nil && ps[j].Git != nil {
					continue // 两个不同仓库绝不因为名字像就合并
				}
				ov, common := Overlap(ps[i].top, ps[j].top)
				if ov >= MinOverlap && common >= MinCommon {
					if uf.union(i, j) {
						reasons[[2]int{i, j}] = "名字去后缀后相同且顶层文件重合"
					}
				}
			}
		}
	}

	members := map[int][]int{}
	for i := range ps {
		r := uf.find(i)
		members[r] = append(members[r], i)
	}
	roots := make([]int, 0, len(members))
	for r := range members {
		roots = append(roots, r)
	}
	// 稳定顺序：按组长路径排序。
	leaders := map[int]int{}
	for _, r := range roots {
		leaders[r] = pickLeader(ps, members[r])
	}
	sort.Slice(roots, func(a, b int) bool { return ps[leaders[roots[a]]].Path < ps[leaders[roots[b]]].Path })

	res.Groups = nil
	for gid, r := range roots {
		idx := members[r]
		lead := leaders[r]
		sort.Slice(idx, func(a, b int) bool { return ps[idx[a]].Path < ps[idx[b]].Path })
		g := Group{ID: gid, Leader: ps[lead].Path, Name: groupName(&ps[lead])}
		seen := map[string]bool{}
		for _, i := range idx {
			ps[i].Group = gid
			g.Members = append(g.Members, ps[i].Path)
			switch {
			case len(idx) == 1:
				ps[i].Kind = "single"
			case i == lead:
				ps[i].Kind = "main"
			case ps[i].Git != nil && ps[i].Git.IsWorktree:
				ps[i].Kind = "worktree"
			case ps[i].Git != nil:
				ps[i].Kind = "clone"
			default:
				ps[i].Kind = "copy"
			}
			for _, j := range idx {
				if why, ok := reasons[[2]int{i, j}]; ok && !seen[why] {
					seen[why] = true
					g.Reasons = append(g.Reasons, why)
				}
			}
		}
		res.Groups = append(res.Groups, g)
	}
}

// pickLeader：主 checkout 优先 → 在 git 里的优先 → 路径最短 → 字典序。
func pickLeader(ps []Project, idx []int) int {
	best := idx[0]
	score := func(i int) int {
		switch {
		case ps[i].Git != nil && !ps[i].Git.IsWorktree:
			return 0
		case ps[i].Git != nil:
			return 1
		}
		return 2
	}
	for _, i := range idx[1:] {
		si, sb := score(i), score(best)
		if si < sb || (si == sb && (len(ps[i].Path) < len(ps[best].Path) || (len(ps[i].Path) == len(ps[best].Path) && ps[i].Path < ps[best].Path))) {
			best = i
		}
	}
	return best
}

// groupName 用主版本的文件夹名（去掉副本/版本后缀）当组名，人看着最熟；
// 文件夹名去完后缀是空的，才退回用 git 仓库名。
func groupName(p *Project) string {
	if strings.TrimSpace(p.BaseName) != "" {
		return p.BaseName
	}
	if p.Git != nil && p.Git.Origin != "" {
		if n := path.Base(p.Git.Identity); n != "" && n != "." && n != "/" {
			return StripVersion(n)
		}
	}
	return p.Name
}

// matchLoose 把散落文件按「去后缀后的名字」对到项目组（例如交付包 zip 对到同名项目）。
// 依次比：成员文件夹原名、组名、成员文件夹去后缀后的名字；同一层有两个组都对得上就算「说不清」，看下一层。
func matchLoose(res *Result) {
	const ambiguous = -2
	exactKeys := map[string]int{}
	nameKeys := map[string]int{}
	memberKeys := map[string]int{}
	add := func(m map[string]int, k string, id int) {
		if k == "" {
			return
		}
		if old, ok := m[k]; ok && old != id {
			m[k] = ambiguous
			return
		}
		m[k] = id
	}
	for _, g := range res.Groups {
		add(nameKeys, CopyKey(g.Name), g.ID)
		for _, m := range g.Members {
			add(exactKeys, strings.ToLower(filepath.Base(m)), g.ID)
			add(memberKeys, CopyKey(filepath.Base(m)), g.ID)
		}
	}
	for i := range res.Loose {
		k := FileKey(filepath.Base(res.Loose[i].Path))
		for _, m := range []map[string]int{exactKeys, nameKeys, memberKeys} {
			if id, ok := m[k]; ok && id >= 0 {
				res.Loose[i].Group = id
				break
			}
		}
		if res.Loose[i].Group < 0 {
			// Project-prefixed deliverables such as Acme-需求.docx are related
			// candidates; a unique, delimited project name is required.
			stem := strings.ToLower(strings.TrimSuffix(filepath.Base(res.Loose[i].Path), filepath.Ext(res.Loose[i].Path)))
			match := -1
			for _, g := range res.Groups {
				key := strings.ToLower(g.Name)
				if utf8.RuneCountInString(key) < 3 {
					continue
				}
				hit := false
				for _, sep := range []string{"-", "_", " ", "·", "—"} {
					if strings.HasPrefix(stem, key+sep) {
						hit = true
					}
				}
				if hit {
					if match >= 0 && match != g.ID {
						match = -2
						break
					}
					match = g.ID
				}
			}
			if match >= 0 {
				res.Loose[i].Group = match
			}
		}
	}
}
