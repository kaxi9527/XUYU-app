package main

import (
	"context"
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/versions"
)

func (s *ShelfService) versionProject(ctx context.Context, key string) (versionScope, *versions.Store, versions.Catalog, *versions.Project, error) {
	scope, store, err := s.versionEnvironment(key)
	if err != nil {
		return scope, nil, versions.Catalog{}, nil, err
	}
	catalog, err := store.Read(ctx)
	if err != nil {
		return scope, store, catalog, nil, err
	}
	project, err := catalog.FindProject(scope.project)
	return scope, store, catalog, project, err
}

func (s *ShelfService) SetWorkUsable(projectKey, workID, versionID string, revision uint64, operationID string) (versions.Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, store, _, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return versions.Receipt{}, err
	}
	if project == nil {
		return versions.Receipt{}, errors.New("项目尚未登记作品")
	}
	return store.SetUsable(ctx, revision, operationID, versions.UsableRequest{ProjectID: project.ID, WorkID: workID, VersionID: versionID}, scope.allow, versionReadLimit)
}

func (s *ShelfService) AddVersionSource(projectKey, versionID string, note versions.UserNote, revision uint64, operationID string) (versions.Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, store, _, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return versions.Receipt{}, err
	}
	if project == nil {
		return versions.Receipt{}, errors.New("项目尚未登记作品")
	}
	return store.AddUserNote(ctx, revision, operationID, versions.NoteRequest{ProjectID: project.ID, VersionID: versionID, Note: note})
}

type RegroupImpact struct {
	Revision          uint64   `json:"revision"`
	Versions          []string `json:"versions"`
	ClearsUsable      []string `json:"clearsUsable"`
	DestinationWorkID string   `json:"destinationWorkID"`
	Notice            string   `json:"notice"`
}

func (s *ShelfService) PreviewVersionRegroup(projectKey string, versionIDs []string, destinationWorkID string) (RegroupImpact, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, catalog, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return RegroupImpact{}, err
	}
	if project == nil {
		return RegroupImpact{}, errors.New("项目尚未登记作品")
	}
	if len(versionIDs) == 0 || len(versionIDs) > 200 {
		return RegroupImpact{}, errors.New("请选择 1 到 200 个版本")
	}
	works := map[string]versions.Work{}
	for _, w := range catalog.Works {
		if w.ProjectID == project.ID {
			works[w.ID] = w
		}
	}
	if destinationWorkID != "" {
		if _, ok := works[destinationWorkID]; !ok {
			return RegroupImpact{}, errors.New("目标作品不属于当前项目")
		}
	}
	out := RegroupImpact{Revision: catalog.Revision, Versions: []string{}, ClearsUsable: []string{}, DestinationWorkID: destinationWorkID, Notice: "只调整作品归属，不移动或删除文件；移出的可用版会取消当前作品的可用标记，确认历史保留。"}
	byID := map[string]versions.Version{}
	for _, v := range catalog.Versions {
		byID[v.ID] = v
	}
	seen := map[string]bool{}
	for _, id := range versionIDs {
		v, ok := byID[id]
		work, owned := works[v.WorkID]
		if !ok || !owned || seen[id] {
			return RegroupImpact{}, errors.New("版本选择无效或不属于当前项目")
		}
		seen[id] = true
		out.Versions = append(out.Versions, id)
		if work.CurrentUsableVersionID == id && work.ID != destinationWorkID {
			out.ClearsUsable = append(out.ClearsUsable, work.ID)
		}
	}
	return out, nil
}

func (s *ShelfService) RegroupVersions(projectKey string, versionIDs []string, destinationWorkID, title string, revision uint64, operationID string) (versions.Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, store, _, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return versions.Receipt{}, err
	}
	if project == nil {
		return versions.Receipt{}, errors.New("项目尚未登记作品")
	}
	return store.Regroup(ctx, revision, operationID, versions.RegroupRequest{ProjectID: project.ID, VersionIDs: append([]string(nil), versionIDs...), DestinationWorkID: destinationWorkID, Title: title})
}

func (s *ShelfService) SetVersionExportSource(projectKey, versionID, parentVersionID string, revision uint64, operationID string) (versions.Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, store, _, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return versions.Receipt{}, err
	}
	if project == nil {
		return versions.Receipt{}, errors.New("项目尚未登记作品")
	}
	return store.SetExportRelation(ctx, revision, operationID, versions.ExportRelationRequest{ProjectID: project.ID, VersionID: versionID, ParentVersionID: parentVersionID})
}

type VersionOperationResult struct {
	Found   bool              `json:"found"`
	Receipt *versions.Receipt `json:"receipt,omitempty"`
}

// VersionOperation recovers a durable acknowledgement after a restart. It does
// not repeat the mutation and only returns receipts owned by the selected project.
func (s *ShelfService) VersionOperation(projectKey, operationID string) (VersionOperationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, c, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return VersionOperationResult{}, err
	}
	if project == nil {
		return VersionOperationResult{}, nil
	}
	owned := map[string]bool{project.ID: true}
	for _, w := range c.Works {
		if w.ProjectID == project.ID {
			owned[w.ID] = true
		}
	}
	for _, v := range c.Versions {
		if owned[v.WorkID] {
			owned[v.ID] = true
		}
	}
	for _, l := range c.Locations {
		if owned[l.VersionID] {
			owned[l.ID] = true
		}
	}
	for _, p := range c.Provenance {
		if owned[p.VersionID] {
			owned[p.ID] = true
		}
	}
	for _, p := range c.Confirmations {
		if owned[p.VersionID] {
			owned[p.ID] = true
		}
	}
	for _, receipt := range c.Operations {
		if receipt.OperationID != operationID {
			continue
		}
		if len(receipt.EntityIDs) == 0 {
			return VersionOperationResult{}, nil
		}
		for _, id := range receipt.EntityIDs {
			if !owned[id] {
				return VersionOperationResult{}, nil
			}
		}
		return VersionOperationResult{Found: true, Receipt: &receipt}, nil
	}
	return VersionOperationResult{}, nil
}
