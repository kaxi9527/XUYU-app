package autoorganize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaxi9527/WorkShelf/internal/activity"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

func TestModelTasksGroupFilesWithoutAbsolutePaths(t *testing.T) {
	h := t.TempDir()
	cfg := config.Default(h)
	files := []string{filepath.Join(h, "Desktop", "首页.psd"), filepath.Join(h, "Downloads", "封面.psd")}
	var records []activity.Record
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		records = append(records, activity.Record{Path: file, Agent: "codex", Source: "会话文件操作", Note: `{"session":"one","sourcePath":"/session/one.jsonl"}`})
	}
	tasks := ModelTasks(cfg, Policy{Roots: []string{h}}, records)
	if len(tasks) != 1 || tasks[0].Count != 2 || len(tasks[0].Files) != 2 {
		t.Fatal(tasks)
	}
	for _, file := range tasks[0].Files {
		if filepath.IsAbs(file) || strings.Contains(file, h) {
			t.Fatal("absolute path leaked", file)
		}
	}
}

func TestModelDecisionsOnlyChooseProvidedGroups(t *testing.T) {
	tasks := []ModelTask{{ID: "group-1", Kind: "session"}, {ID: "project-1", Kind: "project", Project: "已有项目"}}
	answer := `{"decisions":[{"id":"group-1","project":"品牌改版","workType":"设计·UI原型","confident":true,"reason":"设计稿"},{"id":"project-1","project":"已有项目","workType":"开发·功能开发","confident":true}]}`
	got, err := ParseModelDecisions(tasks, answer)
	if err != nil || len(got) != 2 || got[0].Project != "品牌改版" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{
		`{"decisions":[{"id":"other","project":"项目","workType":"设计·UI原型","confident":true}]}`,
		`{"decisions":[{"id":"project-1","project":"另一个项目","workType":"开发·功能开发","confident":true}]}`,
		`{"decisions":[{"id":"group-1","project":"../outside","workType":"设计·UI原型","confident":true}]}`,
	} {
		if _, err := ParseModelDecisions(tasks, bad); err == nil {
			t.Fatal("invalid decision accepted", bad)
		}
	}
}

func TestModelEvidenceDoesNotChangePreviouslyRejectedOperationID(t *testing.T) {
	base := Candidate{Project: "项目", WorkType: "设计·UI原型", Files: []string{"/tmp/file.psd"}, Signals: []FileSignal{{Path: "/tmp/file.psd", Agent: "codex"}}}
	withEvidence := base
	withEvidence.Signals = append([]FileSignal(nil), base.Signals...)
	withEvidence.Signals[0].SessionKey = "session-id"
	withEvidence.ModelDecisions = []ModelDecisionEvidence{{TaskID: "session-id", Provider: "fixture", Model: "model"}}
	if operationID(base) != operationID(withEvidence) {
		t.Fatal("new evidence changed operation identity")
	}
}
