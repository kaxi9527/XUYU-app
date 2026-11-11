package projectmemory

import (
	"context"
	"errors"
	"time"
)

type AutoEvidence struct {
	OperationID    string              `json:"operationID"`
	ReceiptPath    string              `json:"receiptPath"`
	Status         string              `json:"status"`
	Items          []AutoItem          `json:"items"`
	RequestedBy    string              `json:"requestedBy,omitempty"`
	Objection      *AutoObjection      `json:"objection,omitempty"`
	ModelDecisions []AutoModelDecision `json:"modelDecisions,omitempty"`
}
type AutoObjection struct {
	Adapter    string `json:"adapter"`
	Session    string `json:"session"`
	SourcePath string `json:"sourcePath"`
	Line       int64  `json:"line"`
	RecordSHA  string `json:"recordSHA"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}
type AutoModelDecision struct {
	TaskID   string `json:"taskID"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Reason   string `json:"reason,omitempty"`
	InputSHA string `json:"inputSHA"`
}
type AutoItem struct {
	Before          string `json:"before"`
	After           string `json:"after"`
	File            *File  `json:"file,omitempty"`
	Agent           string `json:"agent,omitempty"`
	Session         string `json:"session,omitempty"`
	Task            string `json:"task,omitempty"`
	RecordSHA       string `json:"recordSHA,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	Line            int64  `json:"line,omitempty"`
	PlacementReason string `json:"placementReason,omitempty"`
}

func (a AutoEvidence) validate() error {
	if !idOK(a.OperationID) || !pathOK(a.ReceiptPath) || len(a.RequestedBy) > 128 || !contains([]string{"organized", "partial", "restored", "restore_partial", "restore_failed"}, a.Status) || len(a.Items) > 10000 || len(a.ModelDecisions) > 1000 {
		return ErrCorrupt
	}
	if o := a.Objection; o != nil && (len(o.Adapter) > 128 || len(o.Session) > 256 || !pathOK(o.SourcePath) || o.Line < 1 || !hashOK(o.RecordSHA) || len(o.Provider) > 120 || len(o.Model) > 120) {
		return ErrCorrupt
	}
	for _, d := range a.ModelDecisions {
		if !idOK(d.TaskID) || len(d.Provider) > 120 || len(d.Model) > 120 || len(d.Reason) > 1000 || !hashOK(d.InputSHA) {
			return ErrCorrupt
		}
	}
	for _, item := range a.Items {
		if !pathOK(item.Before) || !pathOK(item.After) || len(item.Agent) > 128 || len(item.Session) > 256 || len(item.Task) > 256 || len(item.PlacementReason) > 1000 || item.RecordSHA != "" && !hashOK(item.RecordSHA) || item.SourcePath != "" && !pathOK(item.SourcePath) || item.Line < 0 {
			return ErrCorrupt
		}
	}
	return nil
}
func (s *Store) RecordAuto(ctx context.Context, revision uint64, operationID string, evidence AutoEvidence, now time.Time) (Result, error) {
	if evidence.validate() != nil || now.IsZero() {
		return Result{}, errors.New("自动整理回执无效")
	}
	return s.update(ctx, revision, operationID, "auto_project", evidence, func(previous State) ([]Event, error) {
		if previous.Revision == 0 {
			return nil, errors.New("项目记忆尚未启用")
		}
		kind, reason := "automatic_project_organized", "WorkShelf 根据已授权范围自动建立项目并整理文件；来源标记与会话记录属于声明"
		if evidence.Status == "restored" || evidence.Status == "restore_partial" || evidence.Status == "restore_failed" {
			kind = "automatic_project_restored"
			reason = "收到反对后按本次整理回执恢复文件；具体结果见回执"
		}
		return []Event{{Kind: kind, ObservedAt: stamp(now), Source: "local_execution", Actor: "workshelf", Reason: reason, Auto: &evidence}}, nil
	})
}
