package main

import (
	"context"
	"encoding/json"
	"github.com/kaxi9527/WorkShelf/internal/projectcontext"
	"github.com/kaxi9527/WorkShelf/internal/worktype"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectAnalysisTemplateChangeWhilePublicationBlocked(t *testing.T) {
	response := make(chan struct{})
	var responseOnce sync.Once
	entered := make(chan struct{})
	s, project, calls := analysisServiceFixture(t, func(w http.ResponseWriter, r *http.Request, p projectcontext.Packet) {
		close(entered)
		select {
		case <-response:
			writeAnalysisFixture(w, p)
		case <-r.Context().Done():
		}
	})
	unlock := make(chan struct{})
	var unlockOnce sync.Once
	t.Cleanup(func() {
		responseOnce.Do(func() { close(response) })
		unlockOnce.Do(func() { close(unlock) })
	})
	p, err := s.PreviewProjectAnalysis("root:品牌项目", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmProjectAnalysis("root:品牌项目", p.Token, "publication-window", true, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no upstream")
	}
	holder, err := s.connections.store.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	locked := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.Publish(context.Background(), func() error {
			close(locked)
			<-unlock
			return nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("no publication lock")
	}
	responseOnce.Do(func() { close(response) })
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data := make([]byte, 1<<20)
		n := runtime.Stack(data, true)
		for _, stack := range strings.Split(string(data[:n]), "\n\n") {
			if strings.Contains(stack, "runProjectAnalysis(") && strings.Contains(stack, "(*Lease).Publish(") {
				blocked = true
				break
			}
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("did not observe publication waiting on held model lock")
	}
	profile, _ := json.Marshal(worktype.Profile{Name: "新规则", Folders: []string{"新目录"}})
	if err = os.WriteFile(filepath.Join(project, worktype.ProfileFile), profile, 0600); err != nil {
		t.Fatal(err)
	}
	unlockOnce.Do(func() { close(unlock) })
	if err = <-holderDone; err != nil {
		t.Fatal(err)
	}
	result := waitAnalysis(t, s, "publication-window", false)
	t.Logf("publicationBlocked=%v changedTemplate=true upstreamCalls=%d finalStatus=%s adviceBytes=%d", blocked, calls.Load(), result.Status, len(result.Advice))
	if result.Status == "succeeded" || len(result.Advice) > 0 {
		t.Fatal("stale template advice published after template changed during publication wait")
	}
}
