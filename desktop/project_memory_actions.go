package main

import (
	"context"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
	"time"
)

type MemoryAction struct {
	Kind            string `json:"kind"`
	ProjectKey      string `json:"projectKey"`
	Revision        uint64 `json:"revision"`
	OperationID     string `json:"operationID"`
	ScopeSHA        string `json:"scopeSHA"`
	SourceAdapter   string `json:"sourceAdapter,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	SourceDirectory bool   `json:"sourceDirectory,omitempty"`
}
type MemoryActionResult struct {
	Receipt   *MemoryActionReceipt `json:"receipt,omitempty"`
	Error     string               `json:"error,omitempty"`
	Uncertain bool                 `json:"uncertain"`
}

type MemoryReceiptRecord struct {
	OperationID string `json:"operationID"`
	Revision    uint64 `json:"revision"`
}
type MemoryActionReceipt struct {
	Record            MemoryReceiptRecord `json:"record"`
	ProjectionPending bool                `json:"projectionPending"`
}
type MemoryOperation struct {
	Found  bool                 `json:"found"`
	Record *MemoryReceiptRecord `json:"record,omitempty"`
}

func (s *ShelfService) ProjectMemoryEvents(key string, revision uint64, cursor string) (projectmemory.EventPage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return projectmemory.EventPage{}, err
	}
	if e.store == nil {
		if revision != 0 || cursor != "" {
			return projectmemory.EventPage{}, projectmemory.ErrConflict
		}
		return projectmemory.EventPage{Project: projectmemory.Project{Name: e.scope.project.Name, Path: e.scope.project.Path}, Items: []projectmemory.Event{}}, nil
	}
	return e.store.RecentEvents(ctx, revision, cursor)
}

func (s *ShelfService) ProjectMemoryOperation(key, operationID string) (MemoryOperation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := s.memoryEnvironment(ctx, key)
	if err != nil {
		return MemoryOperation{}, err
	}
	if e.store == nil {
		return MemoryOperation{}, nil
	}
	status, err := e.store.Operation(ctx, operationID)
	if err != nil {
		return MemoryOperation{}, err
	}
	if !status.Found {
		return MemoryOperation{}, nil
	}
	return MemoryOperation{Found: true, Record: &MemoryReceiptRecord{OperationID: status.Record.OperationID, Revision: status.Record.Revision}}, nil
}

// Domain rejection is acknowledged explicitly. Transport failure leaves the
// frontend's durable request pending until it queries/retries the same ID.
func (s *ShelfService) ApplyProjectMemoryAction(in MemoryAction) MemoryActionResult {
	var result projectmemory.Result
	var err error
	switch in.Kind {
	case "enable", "pause":
		result, err = s.SetProjectMemoryTracking(in.ProjectKey, in.Kind == "enable", in.Revision, in.OperationID, in.ScopeSHA)
	case "scan":
		result, err = s.ObserveProjectMemory(in.ProjectKey, in.Revision, in.OperationID, in.ScopeSHA)
	case "automatic_on", "automatic_off":
		result, err = s.SetProjectMemoryAutomatic(in.ProjectKey, in.Kind == "automatic_on", in.Revision, in.OperationID, in.ScopeSHA)
	case "source_on", "source_off":
		result, err = s.setProjectMemorySource(in)
	default:
		return MemoryActionResult{Error: "不支持这个文件记录操作"}
	}
	if err == nil {
		// Return an acknowledgement, not a potentially 10,000-file event batch.
		return MemoryActionResult{Receipt: &MemoryActionReceipt{Record: MemoryReceiptRecord{OperationID: result.Record.OperationID, Revision: result.Record.Revision}, ProjectionPending: result.ProjectionPending}}
	}
	if errors.Is(err, projectmemory.ErrOperationReused) {
		return MemoryActionResult{Error: err.Error()}
	}
	status, readErr := s.ProjectMemoryOperation(in.ProjectKey, in.OperationID)
	if readErr != nil || status.Found {
		return MemoryActionResult{Error: err.Error(), Uncertain: true}
	}
	return MemoryActionResult{Error: err.Error()}
}
