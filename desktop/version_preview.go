package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/versions"
)

type VersionPreviewInput struct {
	ProjectKey    string   `json:"projectKey"`
	Paths         []string `json:"paths"`
	WorkID        string   `json:"workID"`
	Title         string   `json:"title"`
	Role          string   `json:"role"`
	IncludeHidden bool     `json:"includeHidden"`
	RequestID     string   `json:"requestID"`
}
type VersionPreview struct {
	Token     string                  `json:"token"`
	ExpiresAt string                  `json:"expiresAt"`
	Revision  uint64                  `json:"revision"`
	Project   versions.ProjectInput   `json:"project"`
	WorkID    string                  `json:"workID"`
	Title     string                  `json:"title"`
	Items     []versions.RegisterItem `json:"items"`
	Notice    string                  `json:"notice"`
	Warnings  []string                `json:"warnings"`
}
type versionRun struct {
	done      chan struct{}
	completed bool
	receipt   versions.Receipt
	err       error
}
type versionPreviewEntry struct {
	request       versions.RegisterRequest
	config        config.Config
	revision      uint64
	expires       time.Time
	includeHidden bool
	operationID   string
	run           *versionRun
}
type versionPreviews struct {
	mu      sync.Mutex
	entries map[string]*versionPreviewEntry
	pending map[string]context.CancelFunc
}

func versionToken() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *ShelfService) PreviewRegisterVersion(input VersionPreviewInput) (VersionPreview, error) {
	if len(input.Paths) == 0 || len(input.Paths) > 200 {
		return VersionPreview{}, errors.New("每次请选择 1 到 200 个文件")
	}
	if input.Role == "" {
		input.Role = "draft"
	}
	if input.Role != "source" && input.Role != "draft" && input.Role != "export" && input.Role != "delivery" {
		return VersionPreview{}, errors.New("请选择源文件、草稿、导出件或交付件")
	}
	if len(input.RequestID) > 128 {
		return VersionPreview{}, errors.New("预览请求标识过长")
	}
	requestID := input.RequestID
	if requestID == "" {
		var err error
		requestID, err = versionToken()
		if err != nil {
			return VersionPreview{}, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.versions.mu.Lock()
	if s.versions.pending == nil {
		s.versions.pending = map[string]context.CancelFunc{}
	}
	if _, exists := s.versions.pending[requestID]; exists || len(s.versions.pending) >= 8 {
		s.versions.mu.Unlock()
		return VersionPreview{}, errors.New("预览请求重复或已有多个预览在进行")
	}
	s.versions.pending[requestID] = cancel
	s.versions.mu.Unlock()
	defer func() { s.versions.mu.Lock(); delete(s.versions.pending, requestID); s.versions.mu.Unlock() }()
	scope, store, err := s.versionEnvironment(input.ProjectKey)
	if err != nil {
		return VersionPreview{}, err
	}
	catalog, err := store.Read(ctx)
	if err != nil {
		return VersionPreview{}, err
	}
	project, err := catalog.FindProject(scope.project)
	if err != nil {
		return VersionPreview{}, err
	}
	if input.WorkID != "" {
		found := false
		if project != nil {
			for _, work := range catalog.Works {
				if work.ID == input.WorkID && work.ProjectID == project.ID {
					found = true
					break
				}
			}
		}
		if !found {
			return VersionPreview{}, errors.New("所选作品不属于当前项目")
		}
	}
	title := strings.TrimSpace(input.Title)
	if input.WorkID == "" && title == "" && len(input.Paths) == 1 {
		title = strings.TrimSuffix(filepath.Base(input.Paths[0]), filepath.Ext(input.Paths[0]))
	}
	if input.WorkID == "" && (title == "" || len([]rune(title)) > 300) {
		return VersionPreview{}, errors.New("请为这一组版本填写作品名称（不超过 300 字）")
	}
	paths := append([]string(nil), input.Paths...)
	sort.Strings(paths)
	req := versions.RegisterRequest{Project: scope.project, WorkID: input.WorkID, Title: title, Items: []versions.RegisterItem{}}
	remaining := versionReadLimit
	for index, path := range paths {
		if index > 0 && paths[index-1] == path {
			return VersionPreview{}, errors.New("选择中包含重复文件")
		}
		root, err := scope.rootFor(path, input.IncludeHidden)
		if err != nil {
			return VersionPreview{}, err
		}
		limit := remaining
		if limit == 0 {
			limit = 1
		}
		content, err := versions.Fingerprint(ctx, root, path, limit)
		if err != nil {
			return VersionPreview{}, err
		}
		if content.Size > remaining {
			return VersionPreview{}, errors.New("本次文件总量超过 1 GiB，请分批登记")
		}
		remaining -= content.Size
		req.Items = append(req.Items, versions.RegisterItem{Path: path, Root: root, Content: content, Role: input.Role})
	}
	warnings := loadVersionSources(ctx, scope, req.Items)
	if err := ctx.Err(); err != nil {
		return VersionPreview{}, err
	}
	token, err := versionToken()
	if err != nil {
		return VersionPreview{}, err
	}
	expires := time.Now().Add(10 * time.Minute)
	s.versions.mu.Lock()
	defer s.versions.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return VersionPreview{}, err
	}
	if s.versions.entries == nil {
		s.versions.entries = map[string]*versionPreviewEntry{}
	}
	for key, entry := range s.versions.entries {
		if time.Now().After(entry.expires) && (entry.run == nil || entry.run.completed) {
			delete(s.versions.entries, key)
		}
	}
	if len(s.versions.entries) >= 64 {
		return VersionPreview{}, errors.New("已有较多登记预览，请稍后重试")
	}
	delete(s.versions.pending, requestID)
	s.versions.entries[token] = &versionPreviewEntry{request: req, config: scope.config, revision: catalog.Revision, expires: expires, includeHidden: input.IncludeHidden}
	items := append([]versions.RegisterItem(nil), req.Items...)
	for i := range items {
		items[i].Provenance = append([]versions.SourceClaim(nil), items[i].Provenance...)
	}
	return VersionPreview{Token: token, ExpiresAt: expires.Format(time.RFC3339), Revision: catalog.Revision, Project: scope.project, WorkID: req.WorkID, Title: req.Title, Items: items, Notice: "只登记当前内容，不移动文件、不保存旧版副本。来源来自本地声明或索引，不代表真实性认证；登记后仍需自行确认可用版。", Warnings: warnings}, nil
}

func (s *ShelfService) CancelVersionPreview(requestID string) bool {
	s.versions.mu.Lock()
	defer s.versions.mu.Unlock()
	cancel := s.versions.pending[requestID]
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// DiscardRegisterPreview releases a cancelled confirmation without touching files.
func (s *ShelfService) DiscardRegisterPreview(token string) bool {
	s.versions.mu.Lock()
	defer s.versions.mu.Unlock()
	entry := s.versions.entries[token]
	if entry == nil || entry.operationID != "" || entry.run != nil {
		return false
	}
	delete(s.versions.entries, token)
	return true
}

func (s *ShelfService) RegisterVersion(token, operationID string) (versions.Receipt, error) {
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return versions.Receipt{}, errors.New("缺少有效的登记操作标识")
	}
	s.versions.mu.Lock()
	entry := s.versions.entries[token]
	s.versions.mu.Unlock()
	if entry == nil {
		return versions.Receipt{}, errors.New("登记预览不存在或应用已重启，请重新预览")
	}
	scope, store, err := s.versionEnvironment(entry.request.Project.Key)
	if err != nil {
		return versions.Receipt{}, err
	}
	if !reflect.DeepEqual(scope.config, entry.config) || scope.project != entry.request.Project {
		return versions.Receipt{}, errors.New("项目位置或配置已变化，请重新预览")
	}
	for _, item := range entry.request.Items {
		root, err := scope.rootFor(item.Path, entry.includeHidden)
		if err != nil {
			return versions.Receipt{}, err
		}
		if root != item.Root {
			return versions.Receipt{}, errors.New("项目允许位置已变化，请重新预览")
		}
	}
	s.versions.mu.Lock()
	if entry.operationID != "" && entry.operationID != operationID {
		s.versions.mu.Unlock()
		return versions.Receipt{}, errors.New("此预览已绑定另一次操作，请重新预览")
	}
	if run := entry.run; run != nil && (!run.completed || run.err == nil) {
		done := run.done
		s.versions.mu.Unlock()
		<-done
		return run.receipt, run.err
	}
	if time.Now().After(entry.expires) {
		s.versions.mu.Unlock()
		return versions.Receipt{}, errors.New("登记预览已过期，请重新预览；可用操作标识查询此前回执")
	}
	run := &versionRun{done: make(chan struct{})}
	entry.operationID = operationID
	entry.run = run
	s.versions.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	receipt, err := store.Register(ctx, entry.revision, operationID, entry.request, versionReadLimit)
	s.versions.mu.Lock()
	run.receipt = receipt
	run.err = err
	run.completed = true
	close(run.done)
	s.versions.mu.Unlock()
	return receipt, err
}
