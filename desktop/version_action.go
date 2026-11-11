package main

import (
	"context"
	"errors"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/versions"
)

// A completed domain failure is data, not a transport error. The frontend can
// release its durable request only on this acknowledgement (or a saved receipt).
type VersionActionInput struct {
	Kind            string            `json:"kind"`
	ProjectKey      string            `json:"projectKey"`
	OperationID     string            `json:"operationID"`
	Revision        uint64            `json:"revision"`
	Token           string            `json:"token"`
	WorkID          string            `json:"workID"`
	VersionID       string            `json:"versionID"`
	ParentVersionID string            `json:"parentVersionID"`
	VersionIDs      []string          `json:"versionIDs"`
	Title           string            `json:"title"`
	Note            versions.UserNote `json:"note"`
}
type VersionActionResult struct {
	Receipt   *versions.Receipt `json:"receipt,omitempty"`
	Error     string            `json:"error,omitempty"`
	Uncertain bool              `json:"uncertain"`
}

func (s *ShelfService) ApplyVersionAction(in VersionActionInput) VersionActionResult {
	var receipt versions.Receipt
	var err error
	switch in.Kind {
	case "register":
		s.versions.mu.Lock()
		entry := s.versions.entries[in.Token]
		wrongProject := entry != nil && entry.request.Project.Key != in.ProjectKey
		s.versions.mu.Unlock()
		if wrongProject {
			return VersionActionResult{Error: "登记预览不属于当前项目"}
		}
		receipt, err = s.RegisterVersion(in.Token, in.OperationID)
	case "usable":
		receipt, err = s.SetWorkUsable(in.ProjectKey, in.WorkID, in.VersionID, in.Revision, in.OperationID)
	case "source":
		receipt, err = s.AddVersionSource(in.ProjectKey, in.VersionID, in.Note, in.Revision, in.OperationID)
	case "regroup":
		receipt, err = s.RegroupVersions(in.ProjectKey, in.VersionIDs, in.WorkID, in.Title, in.Revision, in.OperationID)
	case "export":
		receipt, err = s.SetVersionExportSource(in.ProjectKey, in.VersionID, in.ParentVersionID, in.Revision, in.OperationID)
	default:
		return VersionActionResult{Error: "不支持这个版本操作"}
	}
	if err == nil {
		return VersionActionResult{Receipt: &receipt}
	}
	// The command has finished. A subsequent successful read proves whether it
	// committed, including an uncertain fsync acknowledgement or a restart retry.
	status, checkErr := s.VersionOperation(in.ProjectKey, in.OperationID)
	if checkErr != nil {
		return VersionActionResult{Error: err.Error(), Uncertain: true}
	}
	if status.Found {
		if receipt.OperationID == in.OperationID {
			return VersionActionResult{Receipt: &receipt}
		}
		// An operation ID reused for different arguments must not acknowledge
		// the new request as successful. Recovery uses VersionOperation directly.
		return VersionActionResult{Error: err.Error(), Uncertain: true}
	}
	return VersionActionResult{Error: err.Error()}
}

type VersionDetailResult struct {
	Revision uint64                `json:"revision"`
	Item     versions.VersionState `json:"item"`
}

// The current usable version can be older than the visible history page.
func (s *ShelfService) VersionDetail(projectKey, versionID string) (VersionDetailResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, _, catalog, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return VersionDetailResult{}, err
	}
	if project == nil {
		return VersionDetailResult{}, errors.New("项目尚未登记作品")
	}
	item, err := catalog.InspectVersion(ctx, project.ID, versionID, scope.allow, &versions.ReadBudget{Remaining: versionReadLimit})
	return VersionDetailResult{Revision: catalog.Revision, Item: item}, err
}
