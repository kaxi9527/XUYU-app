package projectcontext

import (
	"encoding/json"
	"errors"
	"sort"
)

const MaxBatchParts = 100

var ErrBatchTooLarge = errors.New("这个范围超过一次批次的分析上限，请选择较小的目录；没有发送资料")

// BatchPlan is an immutable preview of every request in one selected scope.
// Its packets retain the original rules and notes, not a lossy summary.
type BatchPlan struct {
	SHA        string   `json:"sha"`
	ScopeSHA   string   `json:"scopeSHA"`
	ScopeID    string   `json:"scopeID"`
	ScopeName  string   `json:"scopeName"`
	TotalFiles int      `json:"totalFiles"`
	Packets    []Packet `json:"packets"`
}

func (s *ScopeIndex) PlanBatch(expectedSHA, directoryID string) (BatchPlan, error) {
	if expectedSHA == "" || expectedSHA != s.SHA {
		return BatchPlan{}, ErrScopeChanged
	}
	n := s.nodes[directoryID]
	if n == nil || n.count == 0 {
		return BatchPlan{}, ErrContext
	}
	if n.count > MaxBatchParts*ScopeGroupFiles {
		return BatchPlan{}, ErrBatchTooLarge
	}
	ids := []string{}
	collectScopeFiles(n, &ids)
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.byFile[ids[i]], s.byFile[ids[j]]
		if a.Position.SourceID != b.Position.SourceID {
			return a.Position.SourceID < b.Position.SourceID
		}
		if a.Position.Relative != b.Position.Relative {
			return a.Position.Relative < b.Position.Relative
		}
		return ids[i] < ids[j]
	})
	plan := BatchPlan{ScopeSHA: s.SHA, ScopeID: directoryID, ScopeName: n.name, TotalFiles: len(ids), Packets: []Packet{}}
	for start := 0; start < len(ids); {
		size := min(ScopeGroupFiles, len(ids)-start)
		var packet Packet
		for {
			var err error
			packet, err = s.packet(ids[start : start+size])
			if err == nil {
				break
			}
			if !errors.Is(err, ErrBudget) {
				return BatchPlan{}, err
			}
			if size == 1 {
				return BatchPlan{}, ErrScopeTooLarge
			}
			size /= 2
		}
		plan.Packets = append(plan.Packets, packet)
		if len(plan.Packets) > MaxBatchParts {
			return BatchPlan{}, ErrBatchTooLarge
		}
		start += size
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return BatchPlan{}, err
	}
	plan.SHA = hash(data)
	return plan, nil
}

type BatchGroup struct {
	FolderID string   `json:"folderID"`
	FileIDs  []string `json:"fileIDs"`
	Reasons  []string `json:"reasons"`
}
type BatchSummary struct {
	Groups        []BatchGroup `json:"groups"`
	UncertainIDs  []string     `json:"uncertainIDs"`
	UnanalyzedIDs []string     `json:"unanalyzedIDs"`
	AnalyzedFiles int          `json:"analyzedFiles"`
	TotalFiles    int          `json:"totalFiles"`
}

// Summary only accepts a validated response for its exact packet. Missing parts
// stay unanalyzed; they never silently become an uncertain or completed result.
func (p BatchPlan) Summary(results map[int]json.RawMessage) (BatchSummary, error) {
	out := BatchSummary{Groups: []BatchGroup{}, UncertainIDs: []string{}, UnanalyzedIDs: []string{}}
	if len(p.Packets) == 0 || len(p.Packets) > MaxBatchParts {
		return out, ErrContext
	}
	for i := range results {
		if i < 0 || i >= len(p.Packets) {
			return out, ErrAdvice
		}
	}
	groups := map[string]int{}
	used := map[string]bool{}
	for i, packet := range p.Packets {
		for _, f := range packet.Body.Files {
			if f.Kind != "file" || f.Status != "present" || used[f.ID] {
				return BatchSummary{}, ErrContext
			}
			used[f.ID] = true
			out.TotalFiles++
		}
		data, ok := results[i]
		if !ok {
			for _, f := range packet.Body.Files {
				out.UnanalyzedIDs = append(out.UnanalyzedIDs, f.ID)
			}
			continue
		}
		advice, err := packet.ValidateAdvice(data)
		if err != nil {
			return BatchSummary{}, err
		}
		out.AnalyzedFiles += len(packet.Body.Files)
		out.UncertainIDs = append(out.UncertainIDs, advice.UncertainIDs...)
		for _, group := range advice.Groups {
			at, exists := groups[group.FolderID]
			if !exists {
				at = len(out.Groups)
				groups[group.FolderID] = at
				out.Groups = append(out.Groups, BatchGroup{FolderID: group.FolderID, FileIDs: []string{}, Reasons: []string{}})
			}
			out.Groups[at].FileIDs = append(out.Groups[at].FileIDs, group.FileIDs...)
			duplicate := false
			for _, reason := range out.Groups[at].Reasons {
				if reason == group.Reason {
					duplicate = true
					break
				}
			}
			if !duplicate {
				out.Groups[at].Reasons = append(out.Groups[at].Reasons, group.Reason)
			}
		}
	}
	if out.TotalFiles != p.TotalFiles {
		return BatchSummary{}, ErrContext
	}
	return out, nil
}
