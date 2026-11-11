package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const enrollmentMaxBytes = 256 << 10

var ErrEnrollmentChanged = errors.New("接入预览后入口或正文已变化，请重新检查")

type EnrollmentPreview struct {
	Agent       string `json:"agent"`
	Home        string `json:"home"`
	Path        string `json:"path"`
	Status      string `json:"status"` // append, create, already_present, blocked
	Reason      string `json:"reason,omitempty"`
	BeforeSHA   string `json:"beforeSHA,omitempty"`
	AfterSHA    string `json:"afterSHA,omitempty"`
	SelectorSHA string `json:"selectorSHA,omitempty"`
	RuleSHA     string `json:"ruleSHA"`
	Instruction string `json:"instruction"`
	Digest      string `json:"digest"`
}

func AutomaticEnrollmentAvailable() bool { return enrollmentSupported() }

// Original client text stays private to the prepared plan and local backup.
type EnrollmentPlan struct {
	view   EnrollmentPreview
	before []byte
}

func (p *EnrollmentPlan) Preview() EnrollmentPreview { return p.view }
func (p *EnrollmentPlan) Format(state fmt.State, verb rune) {
	_, _ = io.WriteString(state, "EnrollmentPlan{client_text:redacted}")
}
func (p *EnrollmentPlan) MarshalJSON() ([]byte, error) { return json.Marshal(p.view) }

func AutomaticAgents() []string { return []string{"codex", "claude", "gemini"} }

func EnrollmentPathAllowed(home, agent, path string) bool {
	var names []string
	switch agent {
	case "codex":
		names = []string{".codex/AGENTS.md", ".codex/AGENTS.override.md"}
	case "claude":
		names = []string{".claude/CLAUDE.md"}
	case "gemini":
		names = []string{".gemini/GEMINI.md"}
	}
	for _, name := range names {
		if filepath.Clean(path) == filepath.Join(home, name) {
			return true
		}
	}
	return false
}
func EnrollmentRuleSHA(agent string) string { return sum(Segment(agent)) }

// PrepareEnrollment is read-only. Paths are derived from an allowlisted adapter,
// not from a browser, model, discovery hint, or arbitrary CLI target.
func PrepareEnrollment(ctx context.Context, home, agent string) (*EnrollmentPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(home) {
		return nil, errors.New("接入需要完整主目录")
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	v := EnrollmentPreview{Agent: agent, Home: home, Status: "blocked", RuleSHA: EnrollmentRuleSHA(agent), Instruction: string(Segment(agent))}
	p := &EnrollmentPlan{view: v}
	finish := func(reason string) (*EnrollmentPlan, error) {
		p.view.Reason = reason
		p.view.Digest = ""
		b, _ := json.Marshal(p.view)
		p.view.Digest = sum(b)
		return p, nil
	}
	profile, filename := "", ""
	switch agent {
	case "codex":
		profile, filename = ".codex", "AGENTS.md"
	case "claude":
		profile, filename = ".claude", "CLAUDE.md"
	case "gemini":
		profile, filename = ".gemini", "GEMINI.md"
	default:
		return finish("adapter_requires_manual_setup")
	}
	dir := filepath.Join(home, profile)
	if err := enrollmentDirectory(dir); err != nil {
		return finish("profile_missing_or_inaccessible")
	}
	if agent == "codex" {
		b, exists, err := readEnrollment(ctx, filepath.Join(dir, "AGENTS.override.md"))
		if err != nil {
			return finish("override_inaccessible")
		}
		p.view.SelectorSHA = sum(b)
		if exists && len(bytes.TrimSpace(b)) > 0 {
			filename = "AGENTS.override.md"
		}
	}
	if agent == "gemini" {
		b, exists, err := readEnrollment(ctx, filepath.Join(dir, "settings.json"))
		if err != nil {
			return finish("context_settings_inaccessible")
		}
		p.view.SelectorSHA = sum(b)
		if exists {
			var settings struct {
				Context struct {
					FileName json.RawMessage `json:"fileName"`
				} `json:"context"`
			}
			if json.Unmarshal(b, &settings) != nil {
				return finish("context_settings_invalid")
			}
			name := settings.Context.FileName
			if len(name) > 0 && string(name) != "null" {
				var one string
				var many []string
				if json.Unmarshal(name, &one) == nil {
					many = []string{one}
				} else if json.Unmarshal(name, &many) != nil {
					return finish("context_filename_invalid")
				}
				found := false
				for _, n := range many {
					found = found || n == "GEMINI.md"
				}
				if !found {
					return finish("custom_context_filename")
				}
			}
		}
	}
	p.view.Path = filepath.Join(dir, filename)
	b, exists, err := readEnrollment(ctx, p.view.Path)
	if err != nil {
		return finish("rule_inaccessible_or_oversize")
	}
	p.before = b
	if exists {
		p.view.BeforeSHA = sum(b)
	}
	segment := Segment(agent)
	if bytes.Count(b, []byte(Begin)) == 1 && bytes.Count(b, []byte(End)) == 1 && bytes.Contains(b, []byte(Block(agent))) {
		p.view.Status = "already_present"
		p.view.AfterSHA = sum(b)
		return finish("")
	}
	if bytes.Contains(b, []byte(Begin)) || bytes.Contains(b, []byte(End)) {
		return finish("existing_rule_needs_review")
	}
	if len(b)+len(segment) > enrollmentMaxBytes || (agent == "codex" && len(b)+len(segment) > 32<<10) {
		return finish("instruction_budget")
	}
	p.view.Status = "create"
	if exists {
		p.view.Status = "append"
	}
	p.view.AfterSHA = sum(append(append([]byte{}, b...), segment...))
	return finish("")
}

func readEnrollment(ctx context.Context, path string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	f, err := openEnrollment(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	b, err := readEnrollmentHandle(f)
	return b, true, err
}

func readEnrollmentHandle(f *os.File) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || !singleEnrollmentLink(fi) || fi.Size() > enrollmentMaxBytes {
		return nil, errors.New("规则不是独立普通文件或超过预算")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, enrollmentMaxBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || len(b) > enrollmentMaxBytes || after.Size() != fi.Size() || !after.ModTime().Equal(fi.ModTime()) {
		return nil, ErrEnrollmentChanged
	}
	return b, nil
}

type EnrollmentIntent struct {
	Agent     string `json:"agent"`
	Path      string `json:"path"`
	BeforeSHA string `json:"beforeSHA,omitempty"`
	AfterSHA  string `json:"afterSHA"`
	Backup    string `json:"backup"`
	Created   bool   `json:"created"`
}
type EnrollmentOutcome struct {
	EnrollmentIntent
	Status string `json:"status"`
}

// ApplyEnrollment appends one fixed segment, or creates a missing file
// exclusively. It never truncates/replaces client content. The caller must
// durably record the intent before the callback permits the target write.
func ApplyEnrollment(ctx context.Context, plan *EnrollmentPlan, backupRoot string, beforeWrite func(EnrollmentIntent) error) (out EnrollmentOutcome, err error) {
	if plan == nil || beforeWrite == nil {
		return out, errors.New("接入需要有效预览和持久写入意图")
	}
	fresh, err := PrepareEnrollment(ctx, plan.view.Home, plan.view.Agent)
	if err != nil {
		return out, err
	}
	if fresh.view.Digest != plan.view.Digest {
		return out, ErrEnrollmentChanged
	}
	v := fresh.view
	out.EnrollmentIntent = EnrollmentIntent{Agent: v.Agent, Path: v.Path, BeforeSHA: v.BeforeSHA, AfterSHA: v.AfterSHA, Created: v.Status == "create"}
	if v.Status == "already_present" {
		out.Status = "already_present"
		return out, nil
	}
	if v.Status != "append" && v.Status != "create" {
		return out, errors.New("此入口尚不能自动接入：" + v.Reason)
	}
	var f *os.File
	if v.Status == "append" {
		f, err = openEnrollment(v.Path, os.O_RDWR|os.O_APPEND)
		if err != nil {
			return out, err
		}
		defer f.Close()
		if err = lockEnrollment(ctx, f); err != nil {
			return out, err
		}
		defer unlockEnrollment(f)
		b, e := readEnrollmentHandle(f)
		if e != nil || sum(b) != v.BeforeSHA {
			return out, ErrEnrollmentChanged
		}
	}
	if err = enrollmentDirectory(backupRoot); err != nil {
		return out, err
	}
	backupDir, err := os.MkdirTemp(backupRoot, "rule-")
	if err != nil {
		return out, err
	}
	out.Backup = filepath.Join(backupDir, "before.bin")
	bf, err := os.OpenFile(out.Backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return out, err
	}
	_, err = bf.Write(fresh.before)
	if err == nil {
		err = bf.Sync()
	}
	closeErr := bf.Close()
	if err != nil {
		return out, err
	}
	if closeErr != nil {
		return out, closeErr
	}
	if err = syncEnrollmentDirectory(backupDir); err != nil {
		return out, err
	}
	if err = syncEnrollmentDirectory(backupRoot); err != nil {
		return out, err
	}
	if err = beforeWrite(out.EnrollmentIntent); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// The callback can persist policy and takes time: re-check selectors and data.
	current, err := PrepareEnrollment(ctx, v.Home, v.Agent)
	if err != nil || current.view.Digest != v.Digest {
		return out, ErrEnrollmentChanged
	}
	if f == nil {
		f, err = openEnrollment(v.Path, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return out, err
		}
		defer f.Close()
		if err = lockEnrollment(ctx, f); err != nil {
			return out, err
		}
		defer unlockEnrollment(f)
	} else {
		fi, e := f.Stat()
		pathInfo, pathErr := os.Lstat(v.Path)
		if e != nil || pathErr != nil || !os.SameFile(fi, pathInfo) {
			return out, ErrEnrollmentChanged
		}
		b, e := readEnrollmentHandle(f)
		if e != nil || sum(b) != v.BeforeSHA {
			return out, ErrEnrollmentChanged
		}
	}
	segment := Segment(v.Agent)
	n, err := f.Write(segment)
	if err != nil {
		return out, err
	}
	if n != len(segment) {
		return out, io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return out, err
	}
	after, err := readEnrollmentHandle(f)
	if err != nil {
		return out, err
	}
	if sum(after) != v.AfterSHA {
		return out, ErrEnrollmentChanged
	}
	actual, exists, err := readEnrollment(ctx, v.Path)
	if err != nil || !exists || sum(actual) != v.AfterSHA {
		return out, ErrEnrollmentChanged
	}
	if err = syncEnrollmentDirectory(filepath.Dir(v.Path)); err != nil {
		return out, err
	}
	out.Status = "rule_written_pending_load"
	return out, nil
}

// ObserveEnrollment checks an interrupted write without changing its target.
func ObserveEnrollment(ctx context.Context, i EnrollmentIntent) (string, error) {
	b, exists, err := readEnrollment(ctx, i.Path)
	if err != nil {
		return "", err
	}
	if exists && sum(b) == i.AfterSHA {
		return "rule_present_pending_load", nil
	}
	if (!exists && i.Created) || (exists && i.BeforeSHA != "" && sum(b) == i.BeforeSHA) {
		return "not_written", nil
	}
	return "needs_review", nil
}

func enrollmentDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("规则路径需要完整目录")
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("规则目录包含链接或不是文件夹")
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
func syncEnrollmentDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
