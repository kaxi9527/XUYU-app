package projectmemory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type EventPage struct {
	Project  Project `json:"project"`
	Revision uint64  `json:"revision"`
	Items    []Event `json:"items"`
	Next     string  `json:"next"`
}

// RecentEvents bounds both returned events and historical segments inspected.
// A cursor identifies an immutable segment/index, with a pinned current revision.
func (s *Store) RecentEvents(ctx context.Context, revision uint64, cursor string) (EventPage, error) {
	v, err := s.Read(ctx)
	if err != nil {
		return EventPage{}, err
	}
	out := EventPage{Project: s.project, Revision: v.State.Revision, Items: []Event{}}
	if revision != v.State.Revision {
		return out, ErrConflict
	}
	r, index := revision, -1
	read := func(rev uint64) (Record, error) {
		record, bytes, err := s.readRecord(ctx, rev)
		if err != nil {
			return Record{}, err
		}
		if rev < revision {
			next, _, err := s.readRecord(ctx, rev+1)
			if err != nil {
				return Record{}, err
			}
			if next.PreviousSHA != digest(bytes) {
				return Record{}, ErrCorrupt
			}
		}
		return record, nil
	}
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 {
			return out, errors.New("事件分页位置无效")
		}
		var a, b error
		r, a = strconv.ParseUint(parts[0], 10, 64)
		index, b = strconv.Atoi(parts[1])
		if a != nil || b != nil || r == 0 || r > revision || index < 0 {
			return out, errors.New("事件分页位置无效")
		}
	}
	for segments := 0; r > 0 && segments < 10; segments++ {
		record, err := read(r)
		if err != nil {
			return out, err
		}
		if index == -1 {
			index = len(record.Events) - 1
		}
		if index >= len(record.Events) {
			return out, errors.New("事件分页位置无效")
		}
		for ; index >= 0; index-- {
			e := record.Events[index]
			// Directory timestamps and completion bookkeeping are available in
			// the full journal, not repeated as thousands of user-facing changes.
			if e.Kind != "observation_completed" && e.Kind != "source_checked" && !(e.After != nil && e.After.Fact.Kind == "directory") {
				out.Items = append(out.Items, e)
			}
			if len(out.Items) == 30 {
				if index > 0 {
					out.Next = fmt.Sprintf("%d:%d", r, index-1)
				} else if r > 1 {
					prev, err := read(r - 1)
					if err != nil {
						return out, err
					}
					out.Next = fmt.Sprintf("%d:%d", r-1, len(prev.Events)-1)
				}
				return out, nil
			}
		}
		r--
		index = -1
	}
	if r > 0 {
		record, err := read(r)
		if err != nil {
			return out, err
		}
		out.Next = fmt.Sprintf("%d:%d", r, len(record.Events)-1)
	}
	return out, nil
}

type OperationStatus struct {
	Found  bool    `json:"found"`
	Record *Record `json:"record,omitempty"`
}

func (s *Store) Operation(ctx context.Context, id string) (OperationStatus, error) {
	if !textOK(id, 128) {
		return OperationStatus{}, errors.New("缺少有效操作标识")
	}
	v, err := s.Read(ctx)
	if err != nil {
		return OperationStatus{}, err
	}
	for _, op := range v.State.Operations {
		if op.ID != id {
			continue
		}
		r, _, err := s.readRecord(ctx, op.Revision)
		if err != nil {
			return OperationStatus{}, err
		}
		if r.OperationID != op.ID || r.RequestSHA != op.RequestSHA {
			return OperationStatus{}, ErrCorrupt
		}
		return OperationStatus{Found: true, Record: &r}, nil
	}
	return OperationStatus{}, nil
}
