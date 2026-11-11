package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/trash"
)

type DeletePreview struct {
	trash.Summary
	Token       string `json:"token"`
	HiddenCount int    `json:"hiddenCount"`
}

type deletePlan struct {
	options   trash.Options
	preview   DeletePreview
	created   time.Time
	started   bool
	completed bool
	done      chan struct{}
	result    *trash.Summary
	err       error
}
type deletePlans struct {
	mu    sync.Mutex
	plans map[string]*deletePlan
}

const deletePreviewTTL = 15 * time.Minute

func (store *deletePlans) preview(opt trash.Options) (DeletePreview, error) {
	summary, err := trash.Preview(opt)
	if err != nil {
		return DeletePreview{}, err
	}
	pre := DeletePreview{Summary: *summary}
	approved := map[string]trash.ApprovalSnapshot{}
	paths := []string{}
	// Keep skipped paths in the preview, never widen the executed path list.
	for i := range pre.Preview {
		item := &pre.Preview[i]
		if item.Skip {
			continue
		}
		snapshot, e := trash.CaptureApproval(item.Path)
		if e != nil {
			item.Skip = true
			item.Action = "跳过"
			item.Reason = e.Error()
			item.Label = "保留原处：" + e.Error()
			pre.Skipped++
			pre.TotalFiles -= item.FileCount
			pre.TotalBytes -= item.ByteSize
			continue
		}
		approved[item.Path] = snapshot
		paths = append(paths, item.Path)
		pre.HiddenCount += snapshot.HiddenCount
	}
	if len(paths) == 0 {
		return pre, nil
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return DeletePreview{}, err
	}
	pre.Token = hex.EncodeToString(token[:])
	opt.Paths = paths
	opt.Approved = approved
	opt.DryRun = false
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.plans == nil {
		store.plans = map[string]*deletePlan{}
	}
	now := time.Now()
	for key, p := range store.plans {
		if now.Sub(p.created) > deletePreviewTTL && (!p.started || p.completed) {
			delete(store.plans, key)
		}
	}
	if len(store.plans) >= 100 {
		return DeletePreview{}, errors.New("删除预览过多，请稍后重新预览")
	}
	store.plans[pre.Token] = &deletePlan{options: opt, preview: pre, created: now}
	return pre, nil
}

func (store *deletePlans) apply(token string, confirmedHidden bool, cfg config.Config, home string) (trash.Summary, error) {
	store.mu.Lock()
	p := store.plans[token]
	if p == nil || (!p.started && time.Since(p.created) > deletePreviewTTL) {
		store.mu.Unlock()
		return trash.Summary{}, errors.New("删除预览不存在、已过期或应用已重启，请重新预览")
	}
	if !reflect.DeepEqual(p.options.Config, cfg) || p.options.Home != home {
		store.mu.Unlock()
		return trash.Summary{}, errors.New("删除位置或配置已变化，请重新预览")
	}
	if p.preview.HiddenCount > 0 && !confirmedHidden {
		store.mu.Unlock()
		return trash.Summary{}, errors.New("所选文件夹包含隐藏内容，可能有工具配置或会话记录，请确认隐藏文件删除提示")
	}
	if p.started {
		done := p.done
		store.mu.Unlock()
		<-done
		if p.result == nil {
			return trash.Summary{}, p.err
		}
		return *p.result, p.err
	}
	p.started = true
	p.done = make(chan struct{})
	store.mu.Unlock()
	result, err := trash.Apply(p.options)
	if result != nil {
		// The receipt includes the protected items that the user saw and did not delete.
		for _, item := range p.preview.Preview {
			if item.Skip {
				result.Skipped++
				result.Items = append(result.Items, trash.ResultItem{Path: item.Path, Action: "跳过", Error: item.Reason})
			}
		}
	}
	store.mu.Lock()
	p.result = result
	p.err = err
	p.completed = true
	close(p.done)
	store.mu.Unlock()
	if result == nil {
		return trash.Summary{}, err
	}
	return *result, err
}
