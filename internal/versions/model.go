// Package versions records local work identities. It never stores file bytes or
// treats a filename, modification time, or old task approval as a usable version.
package versions

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const SchemaVersion = 1

type Content struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	LegacyKey string `json:"legacyKey,omitempty"`
}

type Work struct {
	ID                     string   `json:"id"`
	ProjectID              string   `json:"projectID"`
	Title                  string   `json:"title"`
	VersionIDs             []string `json:"versionIDs"`
	CurrentUsableVersionID string   `json:"currentUsableVersionID,omitempty"`
}

type Version struct {
	ID           string  `json:"id"`
	WorkID       string  `json:"workID"`
	Content      Content `json:"content"`
	Role         string  `json:"role"` // source, draft, export, delivery
	RegisteredAt string  `json:"registeredAt"`
}

type Location struct {
	ID             string `json:"id"`
	VersionID      string `json:"versionID"`
	Path           string `json:"path"`
	AuthorizedRoot string `json:"authorizedRoot"`
	RegisteredBy   string `json:"registeredBy"` // user, tool, index
}

type Relation struct {
	ID              string `json:"id"`
	VersionID       string `json:"versionID"`
	ParentVersionID string `json:"parentVersionID"`
	Kind            string `json:"kind"` // exported_from; work membership is separate
}

type Provenance struct {
	ID         string `json:"id"`
	VersionID  string `json:"versionID"`
	Kind       string `json:"kind"` // tool_declaration, index_record, user_note
	Tool       string `json:"tool,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Recorder   string `json:"recorder,omitempty"`
	Task       string `json:"task,omitempty"`
	Source     string `json:"source,omitempty"`
	Parameters string `json:"parameters,omitempty"`
	RecordedAt string `json:"recordedAt"`
	ClaimedAt  string `json:"claimedAt,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Binding    string `json:"binding,omitempty"` // content_match, path_only, user_supplied
}

type Confirmation struct {
	ID          string  `json:"id"`
	VersionID   string  `json:"versionID"`
	Content     Content `json:"content"`
	ConfirmedAt string  `json:"confirmedAt"`
	Actor       string  `json:"actor"` // local_user, not client approval or authenticity
}

type Receipt struct {
	OperationID string   `json:"operationID"`
	RequestHash string   `json:"requestHash"`
	Action      string   `json:"action"`
	Revision    uint64   `json:"revision"`
	EntityIDs   []string `json:"entityIDs"`
}

type Catalog struct {
	SchemaVersion int            `json:"schemaVersion"`
	Revision      uint64         `json:"revision"`
	Projects      []Project      `json:"projects"`
	Works         []Work         `json:"works"`
	Versions      []Version      `json:"versions"`
	Locations     []Location     `json:"locations"`
	Relations     []Relation     `json:"relations"`
	Provenance    []Provenance   `json:"provenance"`
	Confirmations []Confirmation `json:"confirmations"`
	Operations    []Receipt      `json:"operations"`
}

func emptyCatalog() Catalog {
	return Catalog{SchemaVersion: SchemaVersion, Projects: []Project{}, Works: []Work{}, Versions: []Version{}, Locations: []Location{}, Relations: []Relation{}, Provenance: []Provenance{}, Confirmations: []Confirmation{}, Operations: []Receipt{}}
}

func newID(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(random[:]), nil
}

func validHash(hash string) bool {
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 32 && hash == strings.ToLower(hash)
}

func validTime(value string) bool { _, err := time.Parse(time.RFC3339Nano, value); return err == nil }
func validPath(path string) bool  { return filepath.IsAbs(path) && filepath.Clean(path) == path }
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

// Validate is deliberately filesystem-free: a disconnected volume must not erase
// history. Availability and hash identity are checked separately at action time.
func (c Catalog) Validate() error {
	if c.Revision > (1<<53)-1 {
		return errors.New("作品记录修订号超出可安全表示的范围")
	}
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的作品版本格式 %d；未修改记录", c.SchemaVersion)
	}
	ids := map[string]bool{}
	add := func(id, prefix string) error {
		if !strings.HasPrefix(id, prefix+"_") {
			return errors.New("作品记录身份格式无效")
		}
		bytes, err := hex.DecodeString(strings.TrimPrefix(id, prefix+"_"))
		if err != nil || len(bytes) != 16 || ids[id] {
			return errors.New("作品记录身份无效或重复")
		}
		ids[id] = true
		return nil
	}
	projects := map[string]Project{}
	works := map[string]Work{}
	versions := map[string]Version{}
	for _, p := range c.Projects {
		if err := add(p.ID, "project"); err != nil {
			return err
		}
		if strings.TrimSpace(p.Name) == "" || !validPath(p.Path) {
			return errors.New("项目名称或位置无效")
		}
		projects[p.ID] = p
	}
	for _, w := range c.Works {
		if err := add(w.ID, "work"); err != nil {
			return err
		}
		if _, ok := projects[w.ProjectID]; !ok || strings.TrimSpace(w.Title) == "" {
			return errors.New("作品所属项目或标题无效")
		}
		works[w.ID] = w
	}
	for _, v := range c.Versions {
		if err := add(v.ID, "version"); err != nil {
			return err
		}
		if _, ok := works[v.WorkID]; !ok || !validHash(v.Content.SHA256) || v.Content.Size < 0 || !validTime(v.RegisteredAt) || !oneOf(v.Role, "source", "draft", "export", "delivery") {
			return errors.New("版本内容或所属作品无效")
		}
		versions[v.ID] = v
	}
	owned := map[string]bool{}
	for _, w := range c.Works {
		for _, id := range w.VersionIDs {
			v, ok := versions[id]
			if !ok || v.WorkID != w.ID || owned[id] {
				return errors.New("作品版本列表与所属关系不一致")
			}
			owned[id] = true
		}
	}
	if len(owned) != len(versions) {
		return errors.New("存在没有所属列表的版本")
	}
	for _, l := range c.Locations {
		if err := add(l.ID, "location"); err != nil {
			return err
		}
		if _, ok := versions[l.VersionID]; !ok || !validPath(l.Path) || !validPath(l.AuthorizedRoot) || !within(l.Path, l.AuthorizedRoot) || !oneOf(l.RegisteredBy, "user", "tool", "index") {
			return errors.New("版本位置或授权范围无效")
		}
	}
	parents := map[string][]string{}
	edges := map[string]bool{}
	for _, r := range c.Relations {
		if err := add(r.ID, "relation"); err != nil {
			return err
		}
		v, vok := versions[r.VersionID]
		p, pok := versions[r.ParentVersionID]
		key := r.VersionID + ":" + r.ParentVersionID
		if !vok || !pok || v.ID == p.ID || r.Kind != "exported_from" || edges[key] || works[v.WorkID].ProjectID != works[p.WorkID].ProjectID {
			return errors.New("导出关系无效")
		}
		edges[key] = true
		parents[v.ID] = append(parents[v.ID], p.ID)
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		for _, parent := range parents[id] {
			if !visit(parent) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range parents {
		if !visit(id) {
			return errors.New("导出关系不能循环")
		}
	}
	for _, p := range c.Provenance {
		if err := add(p.ID, "provenance"); err != nil {
			return err
		}
		if _, ok := versions[p.VersionID]; !ok || !oneOf(p.Kind, "tool_declaration", "index_record", "user_note") || !validTime(p.RecordedAt) {
			return errors.New("来源记录无效")
		}
		if p.ClaimedAt != "" && !validTime(p.ClaimedAt) || p.Evidence != "" && !validPath(p.Evidence) || p.Binding != "" && !oneOf(p.Binding, "content_match", "path_only", "user_supplied") {
			return errors.New("来源依据或关联方式无效")
		}
	}
	confirmed := map[string]bool{}
	for _, confirmation := range c.Confirmations {
		if err := add(confirmation.ID, "confirmation"); err != nil {
			return err
		}
		v, ok := versions[confirmation.VersionID]
		if !ok || v.Content != confirmation.Content || confirmation.Actor != "local_user" || !validTime(confirmation.ConfirmedAt) {
			return errors.New("可用确认与版本内容不一致")
		}
		confirmed[v.ID] = true
	}
	for _, w := range c.Works {
		if id := w.CurrentUsableVersionID; id != "" {
			v, ok := versions[id]
			if !ok || v.WorkID != w.ID || !confirmed[id] {
				return errors.New("当前可用版缺少有效的所属关系或历史确认")
			}
		}
	}
	ops := map[string]bool{}
	for _, op := range c.Operations {
		if strings.TrimSpace(op.OperationID) == "" || len(op.OperationID) > 128 || ops[op.OperationID] || !validHash(op.RequestHash) || op.Revision == 0 || op.Revision > c.Revision || !oneOf(op.Action, "register", "ensure_project", "set_usable", "clear_usable", "change_relation", "locate", "add_provenance") {
			return errors.New("操作回执无效")
		}
		ops[op.OperationID] = true
		for _, id := range op.EntityIDs {
			if !ids[id] {
				return errors.New("操作回执引用不存在的身份")
			}
		}
	}
	return nil
}
