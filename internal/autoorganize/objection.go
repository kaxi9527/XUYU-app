package autoorganize

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/config"
	"github.com/kaxi9527/WorkShelf/internal/sourceevidence"
)

type ObjectionSource struct {
	Adapter    string `json:"adapter"`
	Session    string `json:"session"`
	SourcePath string `json:"sourcePath"`
	Line       int64  `json:"line"`
	RecordSHA  string `json:"recordSHA"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

type Objection struct {
	ID          string           `json:"id"`
	OperationID string           `json:"operationID"`
	Agent       string           `json:"agent"`
	At          time.Time        `json:"at"`
	Completed   bool             `json:"completed"`
	Source      *ObjectionSource `json:"source,omitempty"`
}

func objectionDir(cfg config.Config) string { return filepath.Join(dir(cfg), "反对记录") }
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// RequestObjection is an explicit local worker action, not text inferred from
// an arbitrary conversation. The request survives the app being closed.
func RequestObjection(cfg config.Config, operationID, agent string) (Objection, error) {
	return requestObjection(cfg, operationID, agent, nil)
}

func RequestSessionObjection(cfg config.Config, operationID string, claim sourceevidence.Claim, provider, model string) (Objection, error) {
	source := &ObjectionSource{Adapter: claim.Agent, Session: claim.Session, SourcePath: claim.SourcePath, Line: claim.Line, RecordSHA: claim.RecordSHA, Provider: provider, Model: model}
	return requestObjection(cfg, operationID, "用户", source)
}

func requestObjection(cfg config.Config, operationID, agent string, source *ObjectionSource) (Objection, error) {
	var req Objection
	agent = strings.TrimSpace(agent)
	if !validID(operationID) || agent == "" || len(agent) > 128 || strings.ContainsAny(agent, "\x00\r\n") {
		return req, errors.New("请填写有效的整理记录和工作者名称")
	}
	if _, err := read(cfg, operationID); err != nil {
		return req, err
	}
	if err := os.MkdirAll(objectionDir(cfg), 0700); err != nil {
		return req, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return req, err
	}
	req = Objection{ID: hex.EncodeToString(random[:]), OperationID: operationID, Agent: agent, At: time.Now().UTC(), Source: source}
	b, _ := json.Marshal(req)
	f, err := os.OpenFile(filepath.Join(objectionDir(cfg), req.ID+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return req, err
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	return req, err
}
func PendingObjections(cfg config.Config) ([]Objection, error) {
	entries, err := os.ReadDir(objectionDir(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Objection
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, er := os.ReadFile(filepath.Join(objectionDir(cfg), e.Name()))
		if er != nil {
			return nil, er
		}
		var r Objection
		if json.Unmarshal(b, &r) != nil || !validID(r.ID) || !validID(r.OperationID) || e.Name() != r.ID+".json" {
			return nil, errors.New("反对记录无法读取")
		}
		if !r.Completed {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
func CompleteObjection(cfg config.Config, req Objection) error {
	if !validID(req.ID) || !validID(req.OperationID) {
		return errors.New("反对记录无效")
	}
	req.Completed = true
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(objectionDir(cfg), ".response-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(objectionDir(cfg), req.ID+".json"))
}
