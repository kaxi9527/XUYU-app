package projectcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

const ScopePageSize = 50
const ScopeGroupFiles = 40

var ErrScopeChanged = errors.New("项目记录或目录规则已经变化，请重新选择分析范围")
var ErrScopeTooLarge = errors.New("项目规则或笔记过长，单份文件也超出分析限额；请先核对项目记录，不会自动删减内容")

type ScopeCrumb struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ScopeEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Files      int    `json:"files"`
	CanPreview bool   `json:"canPreview"`
}
type ScopePage struct {
	SHA                 string         `json:"sha"`
	ProjectName         string         `json:"projectName"`
	ID                  string         `json:"id"`
	Breadcrumbs         []ScopeCrumb   `json:"breadcrumbs"`
	Rows                []ScopeEntry   `json:"rows"`
	Offset              int            `json:"offset"`
	Total               int            `json:"total"`
	TotalFiles          int            `json:"totalFiles"`
	Files               int            `json:"files"`
	CanPreview          bool           `json:"canPreview"`
	Excluded            map[string]int `json:"excluded"`
	ObservedAt          string         `json:"observedAt"`
	ObservationComplete bool           `json:"observationComplete"`
}
type scopeNode struct {
	id, name string
	parent   *scopeNode
	children map[string]*scopeNode
	files    []File
	count    int
}

// ScopeIndex is a transient, local-only projection. No paths from the UI are
// accepted for filesystem access, and browsing never creates a request/receipt.
type ScopeIndex struct {
	SHA      string
	body     Body
	root     *scopeNode
	nodes    map[string]*scopeNode
	excluded map[string]int
	byFile   map[string]File
}

func NewScopeIndex(view projectmemory.View, profile worktype.Profile) (*ScopeIndex, error) {
	body, err := buildBody(view, profile, nil)
	if err != nil {
		return nil, err
	}
	if len(body.Files) > 20000 {
		return nil, ErrBudget
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrContext
	}
	var detached Body
	if json.Unmarshal(raw, &detached) != nil {
		return nil, ErrContext
	}
	body = detached
	common := body
	common.Files = []File{}
	limits, _ := (Limits{}).normalized()
	if _, err := finishPacket(common, limits); err != nil {
		return nil, ErrScopeTooLarge
	}
	idx := &ScopeIndex{SHA: hash(raw), body: body, nodes: map[string]*scopeNode{}, excluded: map[string]int{}, byFile: map[string]File{}}
	for k, v := range body.Excluded {
		idx.excluded[k] = v
	}
	idx.root = idx.newNode(nil, "", "整个项目")
	sources := map[string]bool{}
	for _, f := range body.Files {
		idx.byFile[f.ID] = f
		if f.Kind != "file" || f.Status != "present" {
			if f.Status != "present" {
				idx.excluded["not_present"]++
			}
			continue
		}
		source := f.Position.SourceID
		if strings.HasPrefix(source, "item-") {
			source = "loose"
		}
		sources[source] = true
	}
	for _, f := range body.Files {
		if f.Kind != "file" || f.Status != "present" {
			continue
		}
		source := f.Position.SourceID
		if strings.HasPrefix(source, "item-") {
			source = "loose"
		}
		current := idx.root
		prefix := source
		if len(sources) > 1 || source == "loose" {
			label := "目录来源 " + strings.TrimPrefix(source, "root-")
			if source == "loose" {
				label = "单独登记的文件"
			}
			current = idx.child(current, prefix, label)
		}
		dir := path.Dir(f.Position.Relative)
		if dir != "." {
			parts := strings.Split(dir, "/")
			if len(parts) > 128 {
				return nil, ErrContext
			}
			for _, name := range parts {
				prefix += "/" + name
				current = idx.child(current, prefix, name)
				if len(idx.nodes) > 50000 {
					return nil, ErrBudget
				}
			}
		}
		current.files = append(current.files, f)
		for node := current; node != nil; node = node.parent {
			node.count++
		}
	}
	for _, node := range idx.nodes {
		sort.Slice(node.files, func(i, j int) bool {
			a, b := node.files[i], node.files[j]
			if a.Position.Relative == b.Position.Relative {
				return a.ID < b.ID
			}
			return a.Position.Relative < b.Position.Relative
		})
	}
	return idx, nil
}
func (s *ScopeIndex) newNode(parent *scopeNode, key, name string) *scopeNode {
	n := &scopeNode{id: "dir-" + hash([]byte(s.SHA+"\n"+key)), name: name, parent: parent, children: map[string]*scopeNode{}}
	s.nodes[n.id] = n
	return n
}
func (s *ScopeIndex) child(parent *scopeNode, key, name string) *scopeNode {
	if n := parent.children[key]; n != nil {
		return n
	}
	n := s.newNode(parent, key, name)
	parent.children[key] = n
	return n
}
func collectScopeFiles(n *scopeNode, ids *[]string) {
	for _, f := range n.files {
		*ids = append(*ids, f.ID)
	}
	for _, child := range n.children {
		collectScopeFiles(child, ids)
	}
}
func (s *ScopeIndex) packet(ids []string) (Packet, error) {
	if len(ids) == 0 || len(ids) > ScopeGroupFiles {
		return Packet{}, ErrBudget
	}
	body := s.body
	body.Files = []File{}
	body.SelectedIDs = append([]string(nil), ids...)
	sort.Strings(body.SelectedIDs)
	body.Excluded = map[string]int{}
	for k, v := range s.body.Excluded {
		body.Excluded[k] = v
	}
	seen := map[string]bool{}
	for _, id := range body.SelectedIDs {
		f, ok := s.byFile[id]
		if !ok || seen[id] || f.Status != "present" || f.Kind != "file" {
			return Packet{}, ErrContext
		}
		seen[id] = true
		body.Files = append(body.Files, f)
	}
	if omitted := len(s.body.Files) - len(body.Files); omitted > 0 {
		body.Excluded["not_selected"] = omitted
	}
	limits, _ := (Limits{}).normalized()
	return finishPacket(body, limits)
}
func (s *ScopeIndex) nodePacket(n *scopeNode) (Packet, error) {
	if n.count == 0 || n.count > ScopeGroupFiles {
		return Packet{}, ErrBudget
	}
	ids := []string{}
	collectScopeFiles(n, &ids)
	return s.packet(ids)
}
func (s *ScopeIndex) parts(n *scopeNode) ([]ScopeEntry, map[string][]string, error) {
	rows := []ScopeEntry{}
	groups := map[string][]string{}
	for start := 0; start < len(n.files); {
		size := ScopeGroupFiles
		if len(n.files)-start < size {
			size = len(n.files) - start
		}
		var ids []string
		for {
			ids = []string{}
			for _, f := range n.files[start : start+size] {
				ids = append(ids, f.ID)
			}
			if _, err := s.packet(ids); err == nil {
				break
			} else if !errors.Is(err, ErrBudget) {
				return nil, nil, err
			}
			if size == 1 {
				return nil, nil, ErrScopeTooLarge
			}
			size /= 2
		}
		id := "part-" + strings.TrimPrefix(n.id, "dir-") + "-" + strconv.Itoa(start)
		name := "本目录文件"
		if size < len(n.files) {
			name = fmt.Sprintf("本目录文件 · 第 %d 组", len(rows)+1)
		}
		rows = append(rows, ScopeEntry{id, name, "group", size, true})
		groups[id] = ids
		start += size
	}
	return rows, groups, nil
}
func (s *ScopeIndex) Page(parentID, expectedSHA string, offset int) (ScopePage, error) {
	if expectedSHA != "" && expectedSHA != s.SHA {
		return ScopePage{}, ErrScopeChanged
	}
	n := s.root
	if parentID != "" {
		n = s.nodes[parentID]
	}
	if n == nil {
		return ScopePage{}, ErrScopeChanged
	}
	rows, _, err := s.parts(n)
	if err != nil {
		return ScopePage{}, err
	}
	for _, child := range n.children {
		_, err := s.nodePacket(child)
		rows = append(rows, ScopeEntry{child.id, child.name, "directory", child.count, err == nil})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind == "directory"
		}
		if rows[i].Kind == "group" {
			return false
		}
		return rows[i].Name < rows[j].Name
	})
	if offset < 0 || offset > len(rows) {
		return ScopePage{}, ErrContext
	}
	end := offset + ScopePageSize
	if end > len(rows) {
		end = len(rows)
	}
	crumbs := []ScopeCrumb{}
	for current := n; current != nil; current = current.parent {
		crumbs = append(crumbs, ScopeCrumb{current.id, current.name})
	}
	for i, j := 0, len(crumbs)-1; i < j; i, j = i+1, j-1 {
		crumbs[i], crumbs[j] = crumbs[j], crumbs[i]
	}
	_, previewErr := s.nodePacket(n)
	return ScopePage{SHA: s.SHA, ProjectName: s.body.ProjectName, ID: n.id, Breadcrumbs: crumbs, Rows: rows[offset:end], Offset: offset, Total: len(rows), TotalFiles: s.root.count, Files: n.count, CanPreview: previewErr == nil, Excluded: s.excluded, ObservedAt: s.body.ObservedAt, ObservationComplete: s.body.ObservationComplete}, nil
}
func (s *ScopeIndex) Select(expectedSHA, groupID string) (Packet, error) {
	if expectedSHA != s.SHA {
		return Packet{}, ErrScopeChanged
	}
	if n := s.nodes[groupID]; n != nil {
		return s.nodePacket(n)
	}
	raw := strings.TrimPrefix(groupID, "part-")
	nodeKey, _, ok := strings.Cut(raw, "-")
	if !ok || !strings.HasPrefix(groupID, "part-") {
		return Packet{}, ErrScopeChanged
	}
	n := s.nodes["dir-"+nodeKey]
	if n == nil {
		return Packet{}, ErrScopeChanged
	}
	_, groups, err := s.parts(n)
	if err != nil {
		return Packet{}, err
	}
	ids, ok := groups[groupID]
	if !ok {
		return Packet{}, ErrScopeChanged
	}
	return s.packet(ids)
}
