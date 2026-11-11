package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/versions"
)

type WorkCard struct {
	ID                     string `json:"id"`
	Title                  string `json:"title"`
	Versions               int    `json:"versions"`
	CurrentUsableVersionID string `json:"currentUsableVersionID,omitempty"`
	Usable                 bool   `json:"usable"`
	UsableState            string `json:"usableState"`
}
type WorkList struct {
	ProjectID string     `json:"projectID"`
	Revision  uint64     `json:"revision"`
	Total     int        `json:"total"`
	Offset    int        `json:"offset"`
	Limit     int        `json:"limit"`
	Items     []WorkCard `json:"items"`
}
type WorkVersionList struct {
	Work      versions.Work           `json:"work"`
	Revision  uint64                  `json:"revision"`
	Total     int                     `json:"total"`
	Offset    int                     `json:"offset"`
	Limit     int                     `json:"limit"`
	Items     []versions.VersionState `json:"items"`
	Relations []versions.Relation     `json:"relations"`
}

func pageOffset(offset, total int) int {
	if offset < 0 {
		return 0
	}
	if offset > total {
		return total
	}
	return offset
}

func (s *ShelfService) ProjectWorks(projectKey, query string, offset int) (WorkList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, _, c, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return WorkList{}, err
	}
	out := WorkList{Revision: c.Revision, Limit: 50, Items: []WorkCard{}}
	if project == nil {
		return out, nil
	}
	out.ProjectID = project.ID
	text := map[string]*strings.Builder{}
	latest := map[string]time.Time{}
	byVersion := map[string]versions.Version{}
	for _, w := range c.Works {
		if w.ProjectID == project.ID {
			builder := &strings.Builder{}
			builder.WriteString(w.Title)
			text[w.ID] = builder
		}
	}
	for _, v := range c.Versions {
		byVersion[v.ID] = v
		registered, _ := time.Parse(time.RFC3339Nano, v.RegisteredAt)
		if registered.After(latest[v.WorkID]) {
			latest[v.WorkID] = registered
		}
	}
	for _, loc := range c.Locations {
		v := byVersion[loc.VersionID]
		if builder, ok := text[v.WorkID]; ok {
			builder.WriteByte('\n')
			builder.WriteString(loc.Path)
		}
	}
	for _, source := range c.Provenance {
		v := byVersion[source.VersionID]
		if builder, ok := text[v.WorkID]; ok {
			for _, value := range []string{source.Tool, source.Agent, source.Recorder, source.Task, source.Source} {
				builder.WriteByte('\n')
				builder.WriteString(value)
			}
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	works := []versions.Work{}
	for _, w := range c.Works {
		if w.ProjectID == project.ID && strings.Contains(strings.ToLower(text[w.ID].String()), query) {
			works = append(works, w)
		}
	}
	sort.SliceStable(works, func(i, j int) bool {
		if !latest[works[i].ID].Equal(latest[works[j].ID]) {
			return latest[works[i].ID].After(latest[works[j].ID])
		}
		return works[i].Title < works[j].Title
	})
	out.Total = len(works)
	out.Offset = pageOffset(offset, len(works))
	end := out.Offset + out.Limit
	if end > len(works) {
		end = len(works)
	}
	budget := &versions.ReadBudget{Remaining: versionReadLimit}
	for _, w := range works[out.Offset:end] {
		card := WorkCard{ID: w.ID, Title: w.Title, Versions: len(w.VersionIDs), CurrentUsableVersionID: w.CurrentUsableVersionID, UsableState: "not_confirmed"}
		if w.CurrentUsableVersionID != "" {
			state, err := c.InspectVersion(ctx, project.ID, w.CurrentUsableVersionID, scope.allow, budget)
			if err != nil {
				return WorkList{}, err
			}
			card.Usable = state.Usable
			card.UsableState = state.State
		}
		out.Items = append(out.Items, card)
	}
	return out, nil
}

func (s *ShelfService) WorkVersions(projectKey, workID string, offset int) (WorkVersionList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, _, c, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return WorkVersionList{}, err
	}
	if project == nil {
		return WorkVersionList{}, errors.New("项目尚未登记作品")
	}
	var work *versions.Work
	for _, w := range c.Works {
		if w.ID == workID && w.ProjectID == project.ID {
			copy := w
			work = &copy
			break
		}
	}
	if work == nil {
		return WorkVersionList{}, errors.New("作品不属于当前项目")
	}
	items := []versions.Version{}
	owned := map[string]bool{}
	for _, v := range c.Versions {
		if v.WorkID == workID {
			items = append(items, v)
			owned[v.ID] = true
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, items[i].RegisteredAt)
		b, _ := time.Parse(time.RFC3339Nano, items[j].RegisteredAt)
		return a.After(b)
	})
	out := WorkVersionList{Work: *work, Revision: c.Revision, Total: len(items), Offset: pageOffset(offset, len(items)), Limit: 20, Items: []versions.VersionState{}, Relations: []versions.Relation{}}
	end := out.Offset + out.Limit
	if end > len(items) {
		end = len(items)
	}
	budget := &versions.ReadBudget{Remaining: versionReadLimit}
	for _, v := range items[out.Offset:end] {
		state, err := c.InspectVersion(ctx, project.ID, v.ID, scope.allow, budget)
		if err != nil {
			return WorkVersionList{}, err
		}
		out.Items = append(out.Items, state)
	}
	for _, r := range c.Relations {
		if owned[r.VersionID] || owned[r.ParentVersionID] {
			out.Relations = append(out.Relations, r)
		}
	}
	return out, nil
}

// VersionFile must be requested immediately before opening a registered version;
// it never substitutes different bytes at the recorded path for old content.
func (s *ShelfService) VersionFile(projectKey, versionID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, _, c, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return "", err
	}
	if project == nil {
		return "", errors.New("项目尚未登记作品")
	}
	state, err := c.InspectVersion(ctx, project.ID, versionID, scope.allow, &versions.ReadBudget{Remaining: versionReadLimit})
	if err != nil {
		return "", err
	}
	for _, loc := range state.Locations {
		if loc.State == "available" {
			return loc.Location.Path, nil
		}
	}
	return "", errors.New("这个版本的原内容目前不可用，请检查位置或登记现在的内容为新版本")
}
