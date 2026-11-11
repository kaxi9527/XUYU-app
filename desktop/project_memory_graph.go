package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/kaxi9527/WorkShelf/internal/graph"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"path/filepath"
)

type MemoryGraphResult struct {
	Project    string       `json:"project"`
	ProjectKey string       `json:"projectKey"`
	Revision   uint64       `json:"revision"`
	Next       string       `json:"next"`
	Nodes      []graph.Node `json:"nodes"`
	Edges      []graph.Edge `json:"edges"`
	Note       string       `json:"note"`
}

func (s *ShelfService) ProjectMemoryGraph(key string, revision uint64, cursor string) (MemoryGraphResult, error) {
	page, err := s.ProjectMemoryEvents(key, revision, cursor)
	if err != nil {
		return MemoryGraphResult{}, err
	}
	return memoryGraphPage(key, page), nil
}

func memoryGraphPage(key string, page projectmemory.EventPage) MemoryGraphResult {
	out := MemoryGraphResult{Project: page.Project.Name, ProjectKey: key, Revision: page.Revision, Next: page.Next, Nodes: []graph.Node{}, Edges: []graph.Edge{}, Note: "这页展示账本已记录的变化。位置是事件发生时的记录，不代表文件现在仍在原处；连线不证明操作者或因果关系。"}
	if len(page.Items) == 0 {
		return out
	}
	root := "memory-project:" + page.Project.ID
	out.Nodes = append(out.Nodes, graph.Node{ID: root, Kind: "memory_project", Name: page.Project.Name, Extra: "本项目文件管理记忆"})
	seen := map[string]bool{}
	labels := map[string]string{"first_observed": "首次观察", "content_changed": "内容变化", "metadata_changed": "信息更新", "location_changed": "位置变化", "no_longer_seen": "原位置未找到", "seen_again": "再次找到", "tracking_removed": "停止追踪", "tracking_started": "开始记录", "scope_changed": "范围变化", "tracking_state": "记录设置变化", "context_note": "记忆条目"}
	labels["placement_recorded"], labels["placement_reconciled"], labels["placement_undone"] = "整理文件", "核对整理结果", "撤销整理"
	labels["file_source_recorded"], labels["source_grant_changed"], labels["source_checked"] = "文件来源", "来源读取设置", "来源检查"
	labels["automatic_project_organized"], labels["automatic_project_restored"] = "自动整理项目", "反对后恢复"
	eventIDs := map[string]bool{}
	for _, e := range page.Items {
		eventIDs[e.ID] = true
	}
	for _, e := range page.Items {
		eventID := "memory-event:" + page.Project.ID + ":" + e.ID
		label := labels[e.Kind]
		if e.Kind == "automatic_changed" && e.Automatic != nil {
			label = "关闭自动检查"
			if *e.Automatic {
				label = "开启自动检查"
			}
		}
		if label == "" {
			label = "文件记录"
		}
		actor := "来源未确认"
		if e.Source == "local_user" {
			actor = "你的操作"
		}
		if e.Source == "agent_declaration" {
			actor = "Agent 声明，尚未核验"
		}
		if e.Source == "local_execution" && e.Actor == "workshelf" {
			actor = "序屿执行"
		}
		if e.Reconciliation != nil {
			actor = "你确认了现场；原操作来源未确认"
		}
		extra := e.ObservedAt + " · " + actor + " · " + e.Reason
		files, from, to := memoryEventFiles(e)
		if from != "" {
			extra += "；之前：" + from
		}
		if to != "" {
			extra += "；记录位置：" + to
		}
		if e.Before != nil {
			extra += "；之前：" + e.Before.Fact.Path
		}
		if e.After != nil {
			extra += "；记录位置：" + e.After.Fact.Path
		}
		out.Nodes = append(out.Nodes, graph.Node{ID: eventID, Kind: "memory_event", Name: label, Extra: extra})
		if o := e.Origin; o != nil {
			for _, source := range []struct{ kind, name, detail string }{{"agent", o.Claim.Agent, "来源记录中的工具名；不证明作者身份"}, {"session", o.Claim.Session, "该条记录关联的会话"}, {"task", o.Claim.Task, "该条记录声明的任务"}} {
				if source.name == "" {
					continue
				}
				id := fmt.Sprintf("memory-source:%s:%s:%x", page.Project.ID, source.kind, sha256.Sum256([]byte(o.Claim.Agent+"\x00"+source.name)))
				if !seen[id] {
					seen[id] = true
					out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: "memory_source", Name: source.name, Agent: o.Claim.Agent, Extra: source.detail})
				}
				out.Edges = append(out.Edges, graph.Edge{ID: id + ":" + eventID, From: id, To: eventID, Type: "memory_reference", Reason: fmt.Sprintf("来源记录 %s 第 %d 行；记录摘要 %s；内容核对 %s", o.SourcePath, o.Claim.Line, o.Claim.RecordSHA, o.Binding), Confidence: "来源声明", Weight: 0.6})
			}
		}
		if e.Auto != nil {
			if objection := e.Auto.Objection; objection != nil {
				id := fmt.Sprintf("memory-source:%s:session:%x", page.Project.ID, sha256.Sum256([]byte(objection.Adapter+"\x00"+objection.Session)))
				if !seen[id] {
					seen[id] = true
					out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: "memory_source", Name: objection.Session, Agent: objection.Adapter, Extra: "反对来自该会话的用户消息"})
				}
				out.Edges = append(out.Edges, graph.Edge{ID: id + ":" + eventID + ":objection", From: id, To: eventID, Type: "memory_reference", Reason: fmt.Sprintf("反对记录 %s 第 %d 行；记录摘要 %s", objection.SourcePath, objection.Line, objection.RecordSHA), Confidence: "来源声明", Weight: 0.6})
				if objection.Provider != "" || objection.Model != "" {
					name := objection.Provider + " / " + objection.Model
					modelID := fmt.Sprintf("memory-source:%s:model:%x", page.Project.ID, sha256.Sum256([]byte(name)))
					if !seen[modelID] {
						seen[modelID] = true
						out.Nodes = append(out.Nodes, graph.Node{ID: modelID, Kind: "memory_source", Name: name, Extra: "识别会话反对的模型"})
					}
					out.Edges = append(out.Edges, graph.Edge{ID: modelID + ":" + eventID + ":objection", From: modelID, To: eventID, Type: "memory_reference", Reason: "模型识别用户会话中的反对", Confidence: "模型判断", Weight: 0.6})
				}
			}
			for _, decision := range e.Auto.ModelDecisions {
				name := decision.Provider + " / " + decision.Model
				id := fmt.Sprintf("memory-source:%s:model:%x", page.Project.ID, sha256.Sum256([]byte(name)))
				if !seen[id] {
					seen[id] = true
					out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: "memory_source", Name: name, Extra: "本次项目归属与工种的模型判断"})
				}
				edge := id + ":" + eventID
				if !seen[edge] {
					seen[edge] = true
					out.Edges = append(out.Edges, graph.Edge{ID: edge, From: id, To: eventID, Type: "memory_reference", Reason: decision.Reason, Confidence: "模型判断", Weight: 0.6})
				}
			}
			if name := e.Auto.RequestedBy; name != "" && name != "用户" {
				id := fmt.Sprintf("memory-source:%s:objector:%x", page.Project.ID, sha256.Sum256([]byte(name)))
				if !seen[id] {
					seen[id] = true
					out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: "memory_source", Name: name, Extra: "提出反对的本地工作者声明"})
				}
				out.Edges = append(out.Edges, graph.Edge{ID: id + ":" + eventID, From: id, To: eventID, Type: "memory_reference", Reason: "该工作者对本次自动整理提出反对", Confidence: "来源声明", Weight: 0.6})
			}
			for _, item := range e.Auto.Items {
				for _, source := range []struct{ kind, name string }{{"agent", item.Agent}, {"session", item.Session}, {"task", item.Task}} {
					if source.name == "" {
						continue
					}
					id := fmt.Sprintf("memory-source:%s:%s:%x", page.Project.ID, source.kind, sha256.Sum256([]byte(item.Agent+"\x00"+source.name)))
					if !seen[id] {
						seen[id] = true
						out.Nodes = append(out.Nodes, graph.Node{ID: id, Kind: "memory_source", Name: source.name, Agent: item.Agent, Extra: "来源记录声明，作者身份未独立验证"})
					}
					edge := id + ":" + eventID
					if !seen[edge] {
						seen[edge] = true
						out.Edges = append(out.Edges, graph.Edge{ID: edge, From: id, To: eventID, Type: "memory_reference", Reason: fmt.Sprintf("来源记录 %s 第 %d 行，摘要 %s", item.SourcePath, item.Line, item.RecordSHA), Confidence: "来源声明", Weight: 0.6})
					}
				}
			}
		}
		for _, f := range files {
			parent := "memory-file:" + page.Project.ID + ":" + f.ID
			if !seen[parent] {
				seen[parent] = true
				out.Nodes = append(out.Nodes, graph.Node{ID: parent, Kind: "memory_file", Name: filepath.Base(f.Fact.Path), Extra: "这页记录中的历史位置：" + f.Fact.Path})
				out.Edges = append(out.Edges, graph.Edge{ID: root + ":" + parent, From: root, To: parent, Type: "memory_member", Reason: "文件身份登记在本项目记忆中，不保证当前位置仍可访问", Confidence: "账本记录", Weight: 1})
			}
			out.Edges = append(out.Edges, graph.Edge{ID: eventID + ":" + f.ID, From: parent, To: eventID, Type: "memory_event", Reason: e.Reason, Confidence: "账本记录", Weight: 1})
		}
		if len(files) == 0 {
			out.Edges = append(out.Edges, graph.Edge{ID: eventID + ":observed", From: root, To: eventID, Type: "memory_event", Reason: e.Reason, Confidence: "账本记录", Weight: 1})
		}
		if e.Undo != nil && eventIDs[e.Undo.Evidence.Original.EventID] {
			out.Edges = append(out.Edges, graph.Edge{ID: eventID + ":original", From: eventID, To: "memory-event:" + page.Project.ID + ":" + e.Undo.Evidence.Original.EventID, Type: "memory_reference", Reason: "这次撤销对应的原整理或核对记录", Confidence: "账本记录", Weight: 1})
		}
	}
	return out
}

// Typed execution records deliberately have no generic Before/After payload.
// Resolve their historical file identities without treating paths as live links.
func memoryEventFiles(e projectmemory.Event) ([]projectmemory.File, string, string) {
	files := []projectmemory.File{}
	indexes := map[string]int{}
	add := func(id string, fact projectmemory.Fact) {
		if id == "" || fact.Path == "" {
			return
		}
		if i, ok := indexes[id]; ok {
			files[i].Fact = fact
			return
		}
		indexes[id] = len(files)
		files = append(files, projectmemory.File{ID: id, Fact: fact})
	}
	if e.Auto != nil {
		for _, item := range e.Auto.Items {
			if item.File != nil {
				add(item.File.ID, item.File.Fact)
			}
		}
		return files, "", ""
	}
	if p := e.Placement; p != nil {
		x := p.Evidence
		add(x.ExpectedFile.ID, x.Before)
		add(p.TargetFileID, x.After)
		return files, x.Before.Path, x.After.Path
	}
	if o := e.Origin; o != nil {
		if o.Snapshot != nil {
			add(o.FileID, *o.Snapshot)
		}
		return files, o.Claim.BeforePath, o.Claim.Path
	}
	if r := e.Reconciliation; r != nil {
		x := r.Evidence
		add(x.ExpectedFile.ID, x.Before)
		if x.Source != nil {
			add(x.ExpectedFile.ID, *x.Source)
		}
		to := ""
		if x.Target != nil {
			add(r.TargetFileID, *x.Target)
			to = x.Target.Path
		}
		return files, x.Before.Path, to
	}
	if u := e.Undo; u != nil {
		x := u.Evidence
		add(x.Original.FileID, x.After)
		return files, x.Before.Path, x.After.Path
	}
	f := e.After
	if f == nil {
		f = e.Before
	}
	if f != nil {
		add(f.ID, f.Fact)
	}
	return files, "", ""
}
