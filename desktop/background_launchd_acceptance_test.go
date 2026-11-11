//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
	"github.com/kaxi9527/WorkShelf/internal/config"
)

// This opt-in test uses a temporary launchd job, never the user's login item.
func TestLaunchdBackgroundSessionRestart(t *testing.T) {
	app := os.Getenv("WORKSHELF_LAUNCHD_TEST_APP")
	if app == "" {
		t.Skip("isolated launchd acceptance only")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "settings", "config.json")
	if err = os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSHELF_CONFIG", configPath)
	cfg := config.Default(home)
	cfg.Root = filepath.Join(home, "Projects")
	cfg.ScanPaths = []string{home}
	writeVersionConfig(t, configPath, cfg)
	if err = os.WriteFile(filepath.Join(home, "settings", "background.json"), []byte(`{"version":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(home, "sessions")
	if err = os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	s := &ShelfService{}
	if _, err = s.SetAutoOrganize(true, []string{home}, []autoorganize.SessionSource{{Adapter: "codex", Path: sessions}}); err != nil {
		t.Fatal(err)
	}
	policy, err := autoorganize.ReadPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	label := fmt.Sprintf("%s.acceptance.%d", backgroundLabel, os.Getpid())
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	job := domain + "/" + label
	f := backgroundFiles{home: home, config: configPath, app: app}
	plist := filepath.Join(home, "launchd.plist")
	if err = os.WriteFile(plist, []byte(strings.Replace(string(f.loginDocument()), backgroundLabel, label, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	start := func() {
		t.Helper()
		if output, e := exec.Command("/bin/launchctl", "bootstrap", domain, plist).CombinedOutput(); e != nil {
			t.Fatalf("launchd bootstrap failed: %s: %v", output, e)
		}
	}
	stop := func() {
		t.Helper()
		if output, e := exec.Command("/bin/launchctl", "bootout", job).CombinedOutput(); e != nil {
			t.Errorf("launchd bootout failed: %s: %v", output, e)
		}
	}
	running := false
	t.Cleanup(func() {
		if running {
			stop()
		}
	})
	wait := func(label string, ready func() bool) {
		t.Helper()
		for deadline := time.Now().Add(55 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
			if ready() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", label)
	}
	writeSession := func(name string) string {
		t.Helper()
		file := filepath.Join(home, name+".txt")
		if e := os.WriteFile(file, []byte("fixture\n"), 0600); e != nil {
			t.Fatal(e)
		}
		patch := "*** Begin Patch\n*** Add File: " + file + "\n+fixture\n*** End Patch\n"
		rows := []any{map[string]any{"type": "session_meta", "payload": map[string]any{"id": name, "cwd": home}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": name, "input": patch}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": name, "output": "Success. Updated the following files:\nA " + file + "\n"}}}
		var data []byte
		for _, row := range rows {
			b, _ := json.Marshal(row)
			data = append(data, append(b, '\n')...)
		}
		if e := os.WriteFile(filepath.Join(sessions, name+".jsonl"), data, 0600); e != nil {
			t.Fatal(e)
		}
		return file
	}
	start()
	running = true
	wait("initial scan", func() bool {
		_, e := os.Stat(filepath.Join(cfg.Root, cfg.Names.Control, "自动整理", "会话进度.json"))
		return e == nil
	})
	first := writeSession("first")
	wait("first session", func() bool {
		records, _, e := autoorganize.ReadSessionEvidence(cfg, policy)
		return e == nil && len(records) == 1 && records[0].Path == first
	})
	if output, e := exec.Command("/bin/launchctl", "kickstart", "-k", job).CombinedOutput(); e != nil {
		t.Fatalf("launchd restart failed: %s: %v", output, e)
	}
	second := writeSession("second")
	wait("session after launchd restart", func() bool {
		records, _, e := autoorganize.ReadSessionEvidence(cfg, policy)
		if e != nil || len(records) != 2 {
			return false
		}
		return (records[0].Path == first && records[1].Path == second) || (records[1].Path == first && records[0].Path == second)
	})
}
