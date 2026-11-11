package autoorganize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/put"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
)

// ModelTask describes a group, never conversation contents or an absolute path.
type ModelTask struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Project string   `json:"project,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Count   int      `json:"count"`
	Files   []string `json:"files"`
}
type ModelDecision struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	WorkType  string `json:"workType"`
	Reason    string `json:"reason,omitempty"`
	Confident bool   `json:"confident"`
}

func modelTaskID(project string) string {
	sum := sha256.Sum256([]byte(project))
	return "project-" + hex.EncodeToString(sum[:16])
}
func CandidateModelTaskID(c Candidate) string {
	if c.ProjectKey == "" {
		return modelTaskID(c.Project)
	}
	return modelTaskID(c.Project + "\x00" + c.ProjectKey + "\x00" + c.TargetPath)
}
func modelRelative(path string, roots []string) string {
	best, label := "", ""
	for i, root := range roots {
		if within(root, path) && len(root) > len(best) {
			best = root
			label = fmt.Sprintf("位置%d", i+1)
		}
	}
	if best == "" {
		return ""
	}
	rel, err := filepath.Rel(best, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(filepath.Join(label, rel))
}

// ModelTasks groups unresolved files, using bounded examples for large groups.
func ModelTasks(cfg config.Config, policy Policy, records []activity.Record) []ModelTask {
	var out []ModelTask
	for _, group := range UnassignedSessions(cfg, policy, records) {
		task := ModelTask{ID: group.ID, Kind: "session", Agent: group.Agent, Count: group.Count}
		for _, path := range group.Examples {
			if p := modelRelative(path, policy.Roots); p != "" {
				task.Files = append(task.Files, p)
			}
		}
		if len(task.Files) > 0 {
			out = append(out, task)
		}
	}
	for _, group := range Candidates(cfg, policy, records) {
		if group.Reason == "" {
			continue
		}
		task := ModelTask{ID: CandidateModelTaskID(group), Kind: "project", Project: group.Project, Count: len(group.Files)}
		for _, path := range group.Files {
			if p := modelRelative(path, policy.Roots); p != "" {
				task.Files = append(task.Files, p)
			}
			if len(task.Files) == 3 {
				break
			}
		}
		if len(task.Files) > 0 {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ParseModelDecisions treats the response as data, not executable instructions.
func ParseModelDecisions(tasks []ModelTask, text string, existingProjects ...string) ([]ModelDecision, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if n := strings.IndexByte(text, '\n'); n >= 0 && strings.HasSuffix(text, "```") {
			text = strings.TrimSpace(text[n+1 : len(text)-3])
		}
	}
	var answer struct {
		Decisions []ModelDecision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil || answer.Decisions == nil {
		return nil, errors.New("模型未返回可识别的判断结果")
	}
	byID := map[string]ModelTask{}
	existing := map[string]bool{}
	for _, name := range existingProjects {
		existing[name] = true
	}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	seen := map[string]bool{}
	var accepted []ModelDecision
	for _, decision := range answer.Decisions {
		task, ok := byID[decision.ID]
		if !ok || seen[decision.ID] {
			return nil, errors.New("模型结果与待整理的文件组不一致")
		}
		seen[decision.ID] = true
		if !decision.Confident {
			continue
		}
		decision.Project = strings.TrimSpace(decision.Project)
		if task.Kind == "project" && decision.Project != task.Project {
			return nil, errors.New("模型不能更改已确定的项目")
		}
		if task.Kind != "project" && put.CheckName("项目", decision.Project) != nil && !existing[decision.Project] {
			return nil, errors.New("模型返回的项目名称不可用")
		}
		trade, err := worktype.Resolve(decision.WorkType)
		if err != nil {
			return nil, errors.New("模型返回的工种不可用")
		}
		decision.WorkType = trade.Label()
		decision.Reason = strings.TrimSpace(decision.Reason)
		if len([]rune(decision.Reason)) > 200 {
			decision.Reason = string([]rune(decision.Reason)[:200])
		}
		accepted = append(accepted, decision)
	}
	return accepted, nil
}
