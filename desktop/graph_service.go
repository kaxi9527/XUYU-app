package main

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/graph"
	"time"
)

// WorkGraph is a read-only view of the same catalog and access scope as Works.
func (s *ShelfService) WorkGraph(projectKey, workID string, offset int) (graph.WorkGraphResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	scope, _, catalog, project, err := s.versionProject(ctx, projectKey)
	if err != nil {
		return graph.WorkGraphResult{}, err
	}
	if project == nil {
		if workID != "" {
			return graph.WorkGraphResult{}, errors.New("项目尚未登记作品")
		}
		return graph.WorkGraphResult{Project: scope.project.Name, ProjectKey: projectKey, Revision: catalog.Revision, Nodes: []graph.Node{}, Edges: []graph.Edge{}, Limit: 30, Note: "这个项目尚未登记作品，可在成果页添加文件和版本。"}, nil
	}
	out, err := graph.Works(ctx, catalog, project.ID, workID, offset, scope.allow, versionReadLimit)
	out.ProjectKey = projectKey
	return out, err
}

// ProjectGraph 返回项目关系网（只读）。
func (s *ShelfService) ProjectGraph() (graph.ProjectGraphResult, error) {
	e, err := s.env()
	if err != nil {
		return graph.ProjectGraphResult{}, err
	}
	return graph.Projects(e), nil
}

// FileGraph 返回某项目的文件关系网（只读）。
// project 可空：自动从最近活跃切入。projectID 有值时优先按原地登记 ID 定位。
// expandAttach 为 true 时把桌面/下载等项目外挂靠文件也画进图。
func (s *ShelfService) FileGraph(project string, projectID string, expandAttach bool) (graph.FileGraphResult, error) {
	e, err := s.env()
	if err != nil {
		return graph.FileGraphResult{}, err
	}
	return graph.Files(e, graph.FileOpts{
		Project:      project,
		ProjectID:    projectID,
		ExpandAttach: expandAttach,
	}), nil
}
