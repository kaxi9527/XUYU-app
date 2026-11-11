package versions

import (
	"context"
	"errors"
	"os"
)

type LocationState struct {
	Location Location `json:"location"`
	State    string   `json:"state"`
	Reason   string   `json:"reason,omitempty"`
}
type VersionState struct {
	Version        Version         `json:"version"`
	Locations      []LocationState `json:"locations"`
	Provenance     []Provenance    `json:"provenance"`
	Confirmations  []Confirmation  `json:"confirmations"`
	State          string          `json:"state"`
	RecordedUsable bool            `json:"recordedUsable"`
	Usable         bool            `json:"usable"`
}
type ReadBudget struct{ Remaining int64 }

// InspectVersion derives availability from current bytes. A historical usable
// pointer is retained, but cannot be shown as usable when its content is changed.
func (c Catalog) InspectVersion(ctx context.Context, projectID, versionID string, allow func(Location) error, budget *ReadBudget) (VersionState, error) {
	v, err := projectVersion(&c, projectID, versionID)
	if err != nil {
		return VersionState{}, err
	}
	work, _ := projectWork(&c, projectID, v.WorkID)
	out := VersionState{Version: *v, Locations: []LocationState{}, Provenance: []Provenance{}, Confirmations: []Confirmation{}, RecordedUsable: work.CurrentUsableVersionID == v.ID, State: "missing"}
	for _, p := range c.Provenance {
		if p.VersionID == v.ID {
			out.Provenance = append(out.Provenance, p)
		}
	}
	for _, p := range c.Confirmations {
		if p.VersionID == v.ID {
			out.Confirmations = append(out.Confirmations, p)
		}
	}
	priority := map[string]int{"missing": 0, "unreadable": 1, "restricted": 2, "unchecked": 3, "changed": 4, "available": 5}
	for _, loc := range c.Locations {
		if loc.VersionID != v.ID {
			continue
		}
		state := LocationState{Location: loc, State: "unchecked", Reason: "尚未核对内容"}
		switch {
		case allow == nil:
			state.State = "restricted"
			state.Reason = "缺少当前文件访问范围"
		case out.State == "available": // One matching position is sufficient; other locations remain explicitly unchecked.
		default:
			if err = allow(loc); err != nil {
				state.State = "restricted"
				state.Reason = err.Error()
				break
			}
			fi, e := os.Lstat(loc.Path)
			if errors.Is(e, os.ErrNotExist) {
				state.State = "missing"
				state.Reason = "原位置不存在"
				break
			}
			if e != nil {
				state.State = "unreadable"
				state.Reason = "无法读取原位置"
				break
			}
			if !fi.Mode().IsRegular() {
				state.State = "restricted"
				state.Reason = "原位置不是普通文件"
				break
			}
			if budget == nil || budget.Remaining < fi.Size() || ctx.Err() != nil {
				state.Reason = "达到本次内容核对限额，尚未验证"
				break
			}
			limit := budget.Remaining
			if limit == 0 {
				limit = 1
			}
			actual, e := Fingerprint(ctx, loc.AuthorizedRoot, loc.Path, limit)
			budget.Remaining -= fi.Size()
			switch {
			case errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded):
				state.Reason = "内容核对未完成"
			case errors.Is(e, ErrContentChanged):
				state.State = "changed"
				state.Reason = e.Error()
			case e != nil:
				state.State = "unreadable"
				state.Reason = "无法完成内容核对"
			case actual != v.Content:
				state.State = "changed"
				state.Reason = "原文件内容已改变，旧版本内容未保存"
			default:
				state.State = "available"
				state.Reason = ""
			}
		}
		out.Locations = append(out.Locations, state)
		if priority[state.State] > priority[out.State] {
			out.State = state.State
		}
	}
	out.Usable = out.RecordedUsable && out.State == "available"
	return out, nil
}
