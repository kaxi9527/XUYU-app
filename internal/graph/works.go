package graph

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/versions"
)

const (
	EdgeWorkVersion      = "work_version"
	EdgeExport           = "exported_from"
	workPage             = 30
	versionPage          = 20
	maxVersionReferences = 20
	maxExportEdges       = 40
)

type WorkGraphResult struct {
	Project    string `json:"project"`
	ProjectKey string `json:"projectKey"`
	WorkID     string `json:"workId,omitempty"`
	WorkTitle  string `json:"workTitle,omitempty"`
	Revision   uint64 `json:"revision"`
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
	Total      int    `json:"total"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Skipped    string `json:"skipped,omitempty"`
	Note       string `json:"note"`
}

// Works projects one catalog revision. Membership and registered export edges
// are distinct; neither filenames nor registration order imply derivation.
func Works(ctx context.Context, c versions.Catalog, projectID, workID string, offset int, allow func(versions.Location) error, readLimit int64) (WorkGraphResult, error) {
	out := WorkGraphResult{Revision: c.Revision, WorkID: workID, Nodes: []Node{}, Edges: []Edge{}, Limit: workPage,
		Note: "按作品收起版本；导出箭头仅来自已登记关系。内容状态为本次核对结果，尚未保存历史文件副本。"}
	found := false
	for _, p := range c.Projects {
		if p.ID == projectID {
			out.Project = p.Name
			out.ProjectKey = p.LegacyKey
			found = true
			break
		}
	}
	if !found {
		return out, errors.New("项目尚未登记作品")
	}
	works := map[string]versions.Work{}
	byWork := map[string][]versions.Version{}
	byVersion := map[string]versions.Version{}
	latest := map[string]time.Time{}
	names := map[string]string{}
	for _, w := range c.Works {
		if w.ProjectID == projectID {
			works[w.ID] = w
		}
	}
	for _, v := range c.Versions {
		if _, ok := works[v.WorkID]; !ok {
			continue
		}
		byWork[v.WorkID] = append(byWork[v.WorkID], v)
		byVersion[v.ID] = v
		t, _ := time.Parse(time.RFC3339Nano, v.RegisteredAt)
		if t.After(latest[v.WorkID]) {
			latest[v.WorkID] = t
		}
	}
	for _, loc := range c.Locations {
		if _, ok := byVersion[loc.VersionID]; ok && names[loc.VersionID] == "" {
			names[loc.VersionID] = filepath.Base(loc.Path)
		}
	}
	budget := &versions.ReadBudget{Remaining: readLimit}
	states := map[string]versions.VersionState{}
	inspect := func(id string) (versions.VersionState, error) {
		if v, ok := states[id]; ok {
			return v, nil
		}
		v, err := c.InspectVersion(ctx, projectID, id, allow, budget)
		if err == nil {
			states[id] = v
		}
		return v, err
	}
	workNode := func(w versions.Work) (Node, error) {
		n := Node{ID: "work:" + w.ID, Kind: "work", Name: w.Title, WorkID: w.ID, VersionCount: len(byWork[w.ID]), State: "not_confirmed"}
		if w.CurrentUsableVersionID != "" {
			v, err := inspect(w.CurrentUsableVersionID)
			if err != nil {
				return n, err
			}
			n.State = v.State
			n.Usable = v.Usable
			n.RecordedUsable = v.RecordedUsable
		}
		return n, nil
	}
	pageStart := func(total, limit int) int {
		if total == 0 || offset < 0 {
			return 0
		}
		if offset >= total {
			return (total - 1) / limit * limit
		}
		return offset / limit * limit
	}
	if workID == "" {
		ordered := make([]versions.Work, 0, len(works))
		for _, w := range works {
			ordered = append(ordered, w)
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := ordered[i], ordered[j]
			if latest[a.ID].Equal(latest[b.ID]) {
				return a.ID < b.ID
			}
			return latest[a.ID].After(latest[b.ID])
		})
		out.Total = len(ordered)
		out.Offset = pageStart(out.Total, out.Limit)
		for i := out.Offset; i < out.Total && i < out.Offset+out.Limit; i++ {
			n, err := workNode(ordered[i])
			if err != nil {
				return out, err
			}
			out.Nodes = append(out.Nodes, n)
		}
	} else {
		w, ok := works[workID]
		if !ok {
			return out, errors.New("作品不属于当前项目")
		}
		out.WorkTitle = w.Title
		out.Limit = versionPage
		items := byWork[workID]
		sort.Slice(items, func(i, j int) bool {
			a, _ := time.Parse(time.RFC3339Nano, items[i].RegisteredAt)
			b, _ := time.Parse(time.RFC3339Nano, items[j].RegisteredAt)
			if a.Equal(b) {
				return items[i].ID < items[j].ID
			}
			return a.After(b)
		})
		out.Total = len(items)
		out.Offset = pageStart(out.Total, out.Limit)
		n, err := workNode(w)
		if err != nil {
			return out, err
		}
		out.Nodes = append(out.Nodes, n)
		page := map[string]bool{}
		included := map[string]bool{}
		versionNode := func(v versions.Version, reference bool) (Node, error) {
			name := names[v.ID]
			if name == "" {
				name = "未记录文件名"
			}
			n := Node{ID: "version:" + v.ID, Kind: "version", Name: name, WorkID: v.WorkID, VersionID: v.ID, State: "unchecked", Reference: reference, Source: v.Role, Extra: works[v.WorkID].Title}
			if !reference {
				state, err := inspect(v.ID)
				if err != nil {
					return n, err
				}
				n.State = state.State
				n.Usable = state.Usable
				n.RecordedUsable = state.RecordedUsable
			}
			return n, nil
		}
		for i := out.Offset; i < out.Total && i < out.Offset+out.Limit; i++ {
			v := items[i]
			n, err := versionNode(v, false)
			if err != nil {
				return out, err
			}
			out.Nodes = append(out.Nodes, n)
			page[v.ID] = true
			included[v.ID] = true
			out.Edges = append(out.Edges, Edge{ID: "membership:" + v.ID, Type: EdgeWorkVersion, From: "work:" + workID, To: n.ID, Reason: "登记在同一作品下，不表示版本先后或内容演变", Weight: 1, Confidence: "账本记录"})
		}
		refs, exports, omitted := 0, 0, 0
		for _, r := range c.Relations {
			if r.Kind != EdgeExport || (!page[r.VersionID] && !page[r.ParentVersionID]) {
				continue
			}
			child, childOK := byVersion[r.VersionID]
			parent, parentOK := byVersion[r.ParentVersionID]
			if !childOK || !parentOK {
				continue
			} // Never disclose another project.
			needed := 0
			for _, v := range []versions.Version{parent, child} {
				if !included[v.ID] {
					needed++
				}
			}
			if exports >= maxExportEdges || refs+needed > maxVersionReferences {
				omitted++
				continue
			}
			for _, v := range []versions.Version{parent, child} {
				if !included[v.ID] {
					n, _ := versionNode(v, true)
					out.Nodes = append(out.Nodes, n)
					included[v.ID] = true
					refs++
				}
			}
			out.Edges = append(out.Edges, Edge{ID: "export:" + r.ID, Type: EdgeExport, From: "version:" + parent.ID, To: "version:" + child.ID, Directed: true, Reason: "账本登记：由起点版本导出到终点版本；不代表已验证导出过程", Weight: 2, Confidence: "账本记录"})
			exports++
		}
		if omitted > 0 {
			out.Skipped = fmt.Sprintf("还有 %d 条关联未展示（页外引用或导出关系上限），可从成果详情继续查看。", omitted)
		}
	}
	unchecked := 0
	for _, state := range states {
		if state.State == "unchecked" {
			unchecked++
		}
	}
	if unchecked > 0 {
		out.Skipped = strings.TrimSpace(out.Skipped + fmt.Sprintf(" %d 个版本本次未完成内容核对；不会显示为可用版。", unchecked))
	}
	return out, nil
}
