package versions

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

type ProjectInput struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Path string `json:"path"`
}
type RegisterItem struct {
	Path       string        `json:"path"`
	Root       string        `json:"root"`
	Content    Content       `json:"content"`
	Role       string        `json:"role"`
	Provenance []SourceClaim `json:"provenance,omitempty"`
}
type SourceClaim struct {
	Kind       string `json:"kind"`
	Tool       string `json:"tool,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Recorder   string `json:"recorder,omitempty"`
	Task       string `json:"task,omitempty"`
	Source     string `json:"source,omitempty"`
	Parameters string `json:"parameters,omitempty"`
	ClaimedAt  string `json:"claimedAt,omitempty"`
	Evidence   string `json:"evidence"`
	Binding    string `json:"binding"`
}
type RegisterRequest struct {
	Project ProjectInput   `json:"project"`
	WorkID  string         `json:"workID"`
	Title   string         `json:"title"`
	Items   []RegisterItem `json:"items"`
}
type UsableRequest struct {
	ProjectID string `json:"projectID"`
	WorkID    string `json:"workID"`
	VersionID string `json:"versionID"`
}
type UserNote struct {
	Tool       string `json:"tool"`
	Agent      string `json:"agent"`
	Task       string `json:"task"`
	Source     string `json:"source"`
	Parameters string `json:"parameters"`
}
type NoteRequest struct {
	ProjectID string   `json:"projectID"`
	VersionID string   `json:"versionID"`
	Note      UserNote `json:"note"`
}
type RegroupRequest struct {
	ProjectID         string   `json:"projectID"`
	VersionIDs        []string `json:"versionIDs"`
	DestinationWorkID string   `json:"destinationWorkID"`
	Title             string   `json:"title"`
}
type ExportRelationRequest struct {
	ProjectID       string `json:"projectID"`
	VersionID       string `json:"versionID"`
	ParentVersionID string `json:"parentVersionID"`
}

var ErrProjectLocationChanged = errors.New("项目登记位置已变化，请先核对并重新定位原项目")

// FindProject never derives identity from names or hashes. A changed path for an
// existing key requires explicit relocation; a key alias at the same path reuses ID.
func (c Catalog) FindProject(ref ProjectInput) (*Project, error) {
	var found *Project
	for _, project := range c.Projects {
		if ref.Key != "" && project.LegacyKey == ref.Key && project.Path != ref.Path {
			return nil, ErrProjectLocationChanged
		}
		if project.Path == ref.Path || ref.Key != "" && project.LegacyKey == ref.Key {
			if found != nil && found.ID != project.ID {
				return nil, errors.New("项目身份关联存在冲突，请先核对")
			}
			copy := project
			found = &copy
		}
	}
	return found, nil
}

func projectWork(c *Catalog, projectID, workID string) (*Work, error) {
	for i := range c.Works {
		if c.Works[i].ID == workID && c.Works[i].ProjectID == projectID {
			return &c.Works[i], nil
		}
	}
	return nil, errors.New("作品不属于当前项目或已不存在")
}
func projectVersion(c *Catalog, projectID, versionID string) (*Version, error) {
	for i := range c.Versions {
		v := &c.Versions[i]
		if v.ID == versionID {
			if _, err := projectWork(c, projectID, v.WorkID); err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	return nil, errors.New("版本不存在")
}
func nowString() string         { return time.Now().UTC().Format(time.RFC3339Nano) }
func titleOK(value string) bool { return strings.TrimSpace(value) != "" && len([]rune(value)) <= 300 }

// Register receives server-owned fingerprints from a read-only preview. No bytes
// are moved or copied. The caller grants roots; content is rechecked in the lock.
func (s *Store) Register(ctx context.Context, revision uint64, operationID string, req RegisterRequest, maxBytes int64) (Receipt, error) {
	if req.Project.Key == "" || !titleOK(req.Project.Name) || !validPath(req.Project.Path) || len(req.Items) == 0 || len(req.Items) > 200 || maxBytes <= 0 {
		return Receipt{}, errors.New("项目或登记文件范围无效")
	}
	if req.WorkID == "" && !titleOK(req.Title) {
		return Receipt{}, errors.New("请填写作品名称")
	}
	seen := map[string]bool{}
	for _, item := range req.Items {
		if !validPath(item.Root) || !validPath(item.Path) || !within(item.Path, item.Root) || !validHash(item.Content.SHA256) || item.Content.Size < 0 || seen[item.Path] || !oneOf(item.Role, "source", "draft", "export", "delivery") {
			return Receipt{}, errors.New("登记预览包含无效或重复的文件")
		}
		seen[item.Path] = true
		if len(item.Provenance) > 20 {
			return Receipt{}, errors.New("单个文件来源记录过多")
		}
		for _, claim := range item.Provenance {
			if !oneOf(claim.Kind, "tool_declaration", "index_record") || !oneOf(claim.Binding, "content_match", "path_only") || !validPath(claim.Evidence) || claim.ClaimedAt != "" && !validTime(claim.ClaimedAt) || len(claim.Tool) > 2000 || len(claim.Agent) > 2000 || len(claim.Recorder) > 2000 || len(claim.Task) > 2000 || len(claim.Source) > 4000 || len(claim.Parameters) > 16000 {
				return Receipt{}, errors.New("来源声明无效或过长")
			}
		}
	}
	return s.update(ctx, revision, operationID, "register", req, func(c *Catalog) ([]string, error) {
		remaining := maxBytes
		for _, item := range req.Items {
			if item.Content.Size > remaining {
				return nil, errors.New("登记内容超过本次读取限额")
			}
			limit := remaining
			if limit == 0 {
				limit = 1
			}
			actual, err := Fingerprint(ctx, item.Root, item.Path, limit)
			if err != nil {
				return nil, err
			}
			if actual != item.Content {
				return nil, ErrContentChanged
			}
			remaining -= actual.Size
		}
		project, err := c.FindProject(req.Project)
		if err != nil {
			return nil, err
		}
		if project == nil {
			if req.WorkID != "" {
				return nil, errors.New("当前项目尚未登记这个作品")
			}
			id, err := newID("project")
			if err != nil {
				return nil, err
			}
			p := Project{ID: id, Name: req.Project.Name, Path: req.Project.Path, LegacyKey: req.Project.Key}
			c.Projects = append(c.Projects, p)
			project = &p
		}
		var work *Work
		if req.WorkID == "" {
			id, err := newID("work")
			if err != nil {
				return nil, err
			}
			c.Works = append(c.Works, Work{ID: id, ProjectID: project.ID, Title: strings.TrimSpace(req.Title), VersionIDs: []string{}})
			work = &c.Works[len(c.Works)-1]
		} else {
			work, err = projectWork(c, project.ID, req.WorkID)
			if err != nil {
				return nil, err
			}
		}
		ids := []string{project.ID, work.ID}
		type existingKey struct {
			Path, Role string
			Content    Content
		}
		byVersion := map[string]Version{}
		for _, v := range c.Versions {
			if v.WorkID == work.ID {
				byVersion[v.ID] = v
			}
		}
		existingFiles := map[existingKey]string{}
		for _, loc := range c.Locations {
			if v, ok := byVersion[loc.VersionID]; ok {
				key := existingKey{loc.Path, v.Role, v.Content}
				if existingFiles[key] == "" {
					existingFiles[key] = v.ID
				}
			}
		}
		for _, item := range req.Items {
			existing := existingFiles[existingKey{item.Path, item.Role, item.Content}]
			if existing != "" {
				if err := appendSourceClaims(c, existing, item.Provenance); err != nil {
					return nil, err
				}
				ids = append(ids, existing)
				continue
			}
			id, err := newID("version")
			if err != nil {
				return nil, err
			}
			locationID, err := newID("location")
			if err != nil {
				return nil, err
			}
			c.Versions = append(c.Versions, Version{ID: id, WorkID: work.ID, Content: item.Content, Role: item.Role, RegisteredAt: nowString()})
			c.Locations = append(c.Locations, Location{ID: locationID, VersionID: id, Path: item.Path, AuthorizedRoot: item.Root, RegisteredBy: "user"})
			work.VersionIDs = append(work.VersionIDs, id)
			if err := appendSourceClaims(c, id, item.Provenance); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	})
}

// SetUsable checks actual bytes now. The injected policy checks current project
// membership and sensitive paths; a stored location is never lasting permission.
func (s *Store) SetUsable(ctx context.Context, revision uint64, operationID string, req UsableRequest, allow func(Location) error, maxBytes int64) (Receipt, error) {
	if allow == nil || maxBytes <= 0 {
		return Receipt{}, errors.New("缺少当前文件访问范围")
	}
	action := "set_usable"
	if req.VersionID == "" {
		action = "clear_usable"
	}
	return s.update(ctx, revision, operationID, action, req, func(c *Catalog) ([]string, error) {
		work, err := projectWork(c, req.ProjectID, req.WorkID)
		if err != nil {
			return nil, err
		}
		if req.VersionID == "" {
			work.CurrentUsableVersionID = ""
			return []string{work.ID}, nil
		}
		v, err := projectVersion(c, req.ProjectID, req.VersionID)
		if err != nil {
			return nil, err
		}
		if v.WorkID != work.ID {
			return nil, errors.New("不能确认另一个作品的版本")
		}
		remaining := maxBytes
		available := false
		last := errors.New("没有可读取的位置")
		for _, loc := range c.Locations {
			if loc.VersionID != v.ID {
				continue
			}
			if err = allow(loc); err != nil {
				last = err
				continue
			}
			fi, e := os.Lstat(loc.Path)
			if e != nil {
				last = e
				continue
			}
			if fi.Size() > remaining {
				last = errors.New("确认内容超过本次读取限额")
				continue
			}
			limit := remaining
			if limit == 0 {
				limit = 1
			}
			content, e := Fingerprint(ctx, loc.AuthorizedRoot, loc.Path, limit)
			remaining -= fi.Size()
			if e != nil {
				last = e
				continue
			}
			if content != v.Content {
				last = ErrContentChanged
				continue
			}
			available = true
			break
		}
		if !available {
			return nil, last
		}
		id, err := newID("confirmation")
		if err != nil {
			return nil, err
		}
		c.Confirmations = append(c.Confirmations, Confirmation{ID: id, VersionID: v.ID, Content: v.Content, ConfirmedAt: nowString(), Actor: "local_user"})
		work.CurrentUsableVersionID = v.ID
		return []string{work.ID, v.ID, id}, nil
	})
}

func (s *Store) AddUserNote(ctx context.Context, revision uint64, operationID string, req NoteRequest) (Receipt, error) {
	n := req.Note
	if len(n.Tool) > 2000 || len(n.Agent) > 2000 || len(n.Task) > 2000 || len(n.Source) > 4000 || len(n.Parameters) > 16000 || strings.TrimSpace(n.Tool+n.Agent+n.Task+n.Source+n.Parameters) == "" {
		return Receipt{}, errors.New("来源补充为空或过长")
	}
	return s.update(ctx, revision, operationID, "add_provenance", req, func(c *Catalog) ([]string, error) {
		if _, err := projectVersion(c, req.ProjectID, req.VersionID); err != nil {
			return nil, err
		}
		id, err := newID("provenance")
		if err != nil {
			return nil, err
		}
		c.Provenance = append(c.Provenance, Provenance{ID: id, VersionID: req.VersionID, Kind: "user_note", Tool: n.Tool, Agent: n.Agent, Task: n.Task, Source: n.Source, Parameters: n.Parameters, RecordedAt: nowString(), Binding: "user_supplied"})
		return []string{id, req.VersionID}, nil
	})
}

// Regroup changes catalog membership only. Moving a confirmed version clears its
// old work's current pointer; it never silently becomes the destination's usable.
func (s *Store) Regroup(ctx context.Context, revision uint64, operationID string, req RegroupRequest) (Receipt, error) {
	if len(req.VersionIDs) == 0 || len(req.VersionIDs) > 200 || req.DestinationWorkID == "" && !titleOK(req.Title) {
		return Receipt{}, errors.New("请选择版本和目标作品")
	}
	return s.update(ctx, revision, operationID, "change_relation", req, func(c *Catalog) ([]string, error) {
		chosen := map[string]bool{}
		for _, id := range req.VersionIDs {
			if chosen[id] {
				return nil, errors.New("版本选择重复")
			}
			if _, err := projectVersion(c, req.ProjectID, id); err != nil {
				return nil, err
			}
			chosen[id] = true
		}
		destination := req.DestinationWorkID
		if destination == "" {
			id, err := newID("work")
			if err != nil {
				return nil, err
			}
			destination = id
			c.Works = append(c.Works, Work{ID: id, ProjectID: req.ProjectID, Title: strings.TrimSpace(req.Title), VersionIDs: []string{}})
		} else {
			if _, err := projectWork(c, req.ProjectID, destination); err != nil {
				return nil, err
			}
		}
		for i := range c.Works {
			work := &c.Works[i]
			if work.ID == destination || work.ProjectID != req.ProjectID {
				continue
			}
			kept := []string{}
			for _, id := range work.VersionIDs {
				if !chosen[id] {
					kept = append(kept, id)
				}
			}
			work.VersionIDs = kept
			if chosen[work.CurrentUsableVersionID] {
				work.CurrentUsableVersionID = ""
			}
		}
		target, _ := projectWork(c, req.ProjectID, destination)
		for i := range c.Versions {
			v := &c.Versions[i]
			if chosen[v.ID] && v.WorkID != destination {
				v.WorkID = destination
				target.VersionIDs = append(target.VersionIDs, v.ID)
			}
		}
		return append([]string{destination}, req.VersionIDs...), nil
	})
}

func (s *Store) SetExportRelation(ctx context.Context, revision uint64, operationID string, req ExportRelationRequest) (Receipt, error) {
	return s.update(ctx, revision, operationID, "change_relation", req, func(c *Catalog) ([]string, error) {
		if _, err := projectVersion(c, req.ProjectID, req.VersionID); err != nil {
			return nil, err
		}
		if req.ParentVersionID != "" {
			if _, err := projectVersion(c, req.ProjectID, req.ParentVersionID); err != nil {
				return nil, err
			}
		}
		relations := []Relation{}
		for _, r := range c.Relations {
			if r.VersionID != req.VersionID || r.Kind != "exported_from" {
				relations = append(relations, r)
			}
		}
		ids := []string{req.VersionID}
		if req.ParentVersionID != "" {
			id, err := newID("relation")
			if err != nil {
				return nil, err
			}
			relations = append(relations, Relation{ID: id, VersionID: req.VersionID, ParentVersionID: req.ParentVersionID, Kind: "exported_from"})
			ids = append(ids, req.ParentVersionID)
		}
		c.Relations = relations
		return ids, nil
	})
}
