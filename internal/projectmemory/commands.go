package projectmemory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (s *Store) Initialize(ctx context.Context, operationID string, scope Scope, now time.Time) (Result, error) {
	if now.IsZero() {
		return Result{}, errors.New("观察时间不能为空")
	}
	if err := scope.Validate(); err != nil {
		return Result{}, err
	}
	req := struct {
		Scope Scope
		At    string
	}{scope, stamp(now)}
	return s.update(ctx, 0, operationID, "initialize", scope, func(previous State) ([]Event, error) {
		if previous.Revision != 0 {
			return nil, ErrConflict
		}
		on := true
		return []Event{{Kind: "tracking_started", ObservedAt: req.At, Source: "local_user", Actor: "local_user", Reason: "用户明确启用该项目的文件管理记忆；尚未进行文件观察", Scope: &scope, Enabled: &on}}, nil
	})
}
func (s *Store) SetTracking(ctx context.Context, revision uint64, operationID string, scope Scope, enabled bool, now time.Time) (Result, error) {
	if err := scope.Validate(); err != nil {
		return Result{}, err
	}
	if now.IsZero() {
		return Result{}, errors.New("记录时间不能为空")
	}
	req := struct {
		Scope   Scope
		Enabled bool
		At      string
	}{scope, enabled, stamp(now)}
	intent := struct {
		Scope   Scope
		Enabled bool
	}{scope, enabled}
	return s.update(ctx, revision, operationID, "tracking", intent, func(previous State) ([]Event, error) {
		if previous.Revision == 0 {
			return nil, errors.New("项目记忆尚未启用")
		}
		kind := "tracking_state"
		if previous.Scope.Digest() != scope.Digest() {
			kind = "scope_changed"
		}
		events := []Event{{Kind: kind, ObservedAt: req.At, Source: "local_user", Actor: "local_user", Reason: "用户调整项目追踪范围或暂停状态；旧记录保留", Scope: &scope, Enabled: &enabled}}
		for _, f := range previous.Files {
			if f.Status != "untracked" && !scope.Allows(f.Fact.Path) {
				before, after := f, f
				after.Status = "untracked"
				events = append(events, Event{Kind: "tracking_removed", ObservedAt: req.At, Source: "local_user", Actor: "local_user", Reason: "此位置已移出追踪范围，不代表文件被删除", Before: &before, After: &after})
			}
		}
		return events, nil
	})
}

// RecordObservation accepts facts from trusted local scanners/adapters. Model
// text is not a filesystem adapter and must never be passed to this command.
func (s *Store) RecordObservation(ctx context.Context, revision uint64, operationID string, observation Observation) (Result, error) {
	if len(observation.Files) > maxObservations {
		return Result{}, ErrBudget
	}
	return s.update(ctx, revision, operationID, "observe", observation, func(previous State) ([]Event, error) {
		return s.observationEvents(previous, observation)
	})
}

func (s *Store) observationEvents(previous State, observation Observation) ([]Event, error) {
	if !previous.Enabled || previous.Revision == 0 {
		return nil, errors.New("该项目未开启文件观察")
	}
	if observation.ScopeSHA != previous.Scope.Digest() {
		return nil, ErrConflict
	}
	start, e1 := time.Parse(time.RFC3339Nano, observation.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, observation.EndedAt)
	last, _ := time.Parse(time.RFC3339Nano, previous.LastObservedAt)
	if e1 != nil || e2 != nil || end.Before(start) || start.Before(last) {
		return nil, errors.New("观察时间范围无效或早于已保存记录")
	}
	coverage := map[string]bool{}
	allComplete := true
	for _, c := range observation.Coverage {
		if !previous.Scope.Allows(c.Path) || !contains([]string{"complete", "offline", "error", "limited", "excluded"}, c.Status) || len(c.Reason) > 4000 {
			return nil, errors.New("观察覆盖信息无效")
		}
		coverage[c.Path] = true
		if c.Status != "complete" {
			allComplete = false
		}
	}
	for _, p := range append(append([]string{}, previous.Scope.Roots...), previous.Scope.Files...) {
		if !coverage[p] {
			return nil, errors.New("缺少追踪范围的覆盖说明")
		}
	}
	if observation.Complete && !allComplete {
		return nil, errors.New("不完整观察不能声明完整覆盖")
	}
	currentPaths := map[string]Fact{}
	newKeys := map[string]int{}
	for _, f := range observation.Files {
		if err := f.validate(previous.Scope); err != nil {
			return nil, err
		}
		if within(s.directory, platformPath(f.Path)) {
			return nil, errors.New("自身记忆输出不纳入文件观察")
		}
		if _, ok := currentPaths[f.Path]; ok {
			return nil, errors.New("观察包含重复位置")
		}
		currentPaths[f.Path] = f
		if f.ObjectKey != "" {
			newKeys[f.ObjectKey]++
		}
	}
	oldPaths := map[string]File{}
	oldKeys := map[string][]File{}
	for _, f := range previous.Files {
		if f.Status == "present" {
			oldPaths[f.Fact.Path] = f
		}
		if f.Fact.ObjectKey != "" {
			oldKeys[f.Fact.ObjectKey] = append(oldKeys[f.Fact.ObjectKey], f)
		}
	}
	facts := append([]Fact{}, observation.Files...)
	sort.Slice(facts, func(i, j int) bool { return facts[i].Path < facts[j].Path })
	events := []Event{}
	matched := map[string]bool{}
	add := func(kind, reason string, before *File, after File) {
		events = append(events, Event{Kind: kind, ObservedAt: observation.EndedAt, Source: "filesystem_observation", Actor: "unknown", Reason: reason, Before: before, After: &after})
	}
	for _, fact := range facts {
		old, ok := oldPaths[fact.Path]
		if matched[old.ID] {
			ok = false
		}
		if ok && old.Fact.ObjectKey != "" && fact.ObjectKey != "" && old.Fact.ObjectKey != fact.ObjectKey {
			// A displaced object may be observed later at another path (even
			// when two paths swap). Keep its identity available for that match.
			willMatchElsewhere := observation.Complete && newKeys[old.Fact.ObjectKey] == 1 && len(oldKeys[old.Fact.ObjectKey]) == 1
			if !willMatchElsewhere {
				before, after := old, old
				after.Status = "missing"
				add("no_longer_seen", "原路径现在是不同文件对象；无法据此断言旧对象已删除", &before, after)
				matched[old.ID] = true
			}
			ok = false
		}
		if !ok && fact.ObjectKey != "" && newKeys[fact.ObjectKey] == 1 && len(oldKeys[fact.ObjectKey]) == 1 {
			candidate := oldKeys[fact.ObjectKey][0]
			atOldPath, oldPathPresent := currentPaths[candidate.Fact.Path]
			oldObjectStillThere := oldPathPresent && (atOldPath.ObjectKey == "" || atOldPath.ObjectKey == candidate.Fact.ObjectKey)
			if !matched[candidate.ID] && (candidate.Fact.Path == fact.Path || observation.Complete && !oldObjectStillThere && previous.Scope.Allows(candidate.Fact.Path)) {
				old = candidate
				ok = true
			}
		}
		if !ok {
			id, err := newID("file")
			if err != nil {
				return nil, err
			}
			add("first_observed", "首次观察到这个位置的文件；创建时间与操作者未知", nil, File{ID: id, Fact: fact, Status: "present", FirstObservedAt: observation.EndedAt})
			continue
		}
		matched[old.ID] = true
		after := old
		after.Fact = fact
		after.Status = "present"
		before := old
		switch {
		case old.Fact.Path != fact.Path:
			add("location_changed", "同一唯一文件对象出现在新位置；具体移动或链接操作与操作者未知", &before, after)
		case old.Status != "present":
			add("seen_again", "先前记录的文件对象重新进入观察范围", &before, after)
		case old.Fact.HashState == "verified" && fact.HashState == "verified" && old.Fact.SHA256 != fact.SHA256:
			add("content_changed", "两次观察的内容摘要不同；未保存旧内容副本，操作者未知", &before, after)
		case digestJSON(old.Fact) != digestJSON(fact):
			add("metadata_changed", "文件元数据或内容核验覆盖发生变化，不单凭时间断言内容被改写", &before, after)
		}
	}
	if observation.Complete {
		for _, old := range previous.Files {
			if old.Status == "present" && !matched[old.ID] && previous.Scope.Allows(old.Fact.Path) {
				before, after := old, old
				after.Status = "missing"
				add("no_longer_seen", "本次完整观察未在原位置找到文件；可能移到范围外，不能断言删除或操作者", &before, after)
			}
		}
	}
	meta := observation
	meta.Files = nil
	reason := "文件观察完成，记录本次覆盖范围"
	if !observation.Complete {
		reason = "观察覆盖不完整，未对遗漏文件做缺失判定"
	}
	events = append(events, Event{Kind: "observation_completed", ObservedAt: observation.EndedAt, Source: "filesystem_observation", Actor: "unknown", Reason: reason, Observation: &meta})
	return events, nil
}

// Observe scans only after checking the revision and idempotent request under
// the project lock. A lost acknowledgement never causes a second observation.
func (s *Store) Observe(ctx context.Context, revision uint64, operationID, scopeSHA string) (Result, error) {
	return s.update(ctx, revision, operationID, "scan", scopeSHA, func(previous State) ([]Event, error) {
		if !previous.Enabled || previous.Revision == 0 {
			return nil, errors.New("该项目未开启文件观察")
		}
		if previous.Scope.Digest() != scopeSHA {
			return nil, ErrConflict
		}
		observation, err := Scan(ctx, previous.Scope, ScanOptions{Exclude: []string{s.directory}})
		if err != nil {
			return nil, err
		}
		events, err := s.observationEvents(previous, observation)
		if err != nil {
			return nil, err
		}
		return s.sourceEvents(ctx, previous, events)
	})
}

func (s *Store) AddNote(ctx context.Context, revision uint64, operationID string, note Note, now time.Time) (Result, error) {
	return s.addNoteChecked(ctx, revision, operationID, note, now, nil)
}

func (s *Store) addNoteChecked(ctx context.Context, revision uint64, operationID string, note Note, now time.Time, check func() error) (Result, error) {
	if note.ID != "" || note.EventID != "" || !textOK(note.Text, 12000) || !contains([]string{"user_decision", "agent_proposal"}, note.Kind) || len(note.Agent) > 128 || len(note.Model) > 256 || len(note.Evidence) > 4000 || now.IsZero() {
		return Result{}, errors.New("项目记忆条目无效")
	}
	if note.Kind == "user_decision" && (note.Agent != "" || note.Model != "") {
		return Result{}, errors.New("模型声明不能登记为用户决定")
	}
	req := struct {
		Note Note
		At   string
	}{note, stamp(now)}
	return s.update(ctx, revision, operationID, "note", note, func(previous State) ([]Event, error) {
		if check != nil {
			if err := check(); err != nil {
				return nil, err
			}
		}
		if previous.Revision == 0 {
			return nil, errors.New("项目记忆尚未建立")
		}
		id, err := newID("note")
		if err != nil {
			return nil, err
		}
		n := note
		n.ID = id
		source, actor := "local_user", "local_user"
		if n.Kind == "agent_proposal" {
			source = "agent_declaration"
			actor = "unverified"
		}
		return []Event{{Kind: "context_note", ObservedAt: req.At, Source: source, Actor: actor, Reason: "追加项目记忆条目；不修改追踪授权或已执行状态", Note: &n}}, nil
	})
}
