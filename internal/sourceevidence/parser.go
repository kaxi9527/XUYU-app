// Package sourceevidence decodes selected local tool records into claims.
// It never executes log contents, reads artifact files or certifies authorship.
package sourceevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type Claim struct {
	Message     string `json:"-"`
	Agent       string `json:"agent"`
	Project     string `json:"project,omitempty"`
	WorkType    string `json:"worktype,omitempty"`
	WorkingDir  string `json:"workingDir,omitempty"`
	ProjectHash string `json:"projectHash,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Session     string `json:"session,omitempty"`
	Task        string `json:"task,omitempty"`
	Call        string `json:"call,omitempty"`
	Path        string `json:"path"`
	BeforePath  string `json:"beforePath,omitempty"`
	Action      string `json:"action"`
	SHA256      string `json:"sha256,omitempty"`
	ClaimedAt   string `json:"claimedAt,omitempty"`
	Line        int64  `json:"line"`
	RecordSHA   string `json:"recordSHA"`
	RequestLine int64  `json:"requestLine,omitempty"`
	RequestSHA  string `json:"requestSHA,omitempty"`
	Basis       string `json:"basis"` // self_report or tool_result, both untrusted local records
}

type Pending struct {
	Claims       []Claim `json:"claims"`
	Shell        bool    `json:"shell,omitempty"`
	LiteralWrite bool    `json:"literalWrite,omitempty"`
	ExecPatch    bool    `json:"execPatch,omitempty"`
}

type Parser struct {
	Session           string             `json:"session,omitempty"`
	CWD               string             `json:"cwd,omitempty"`
	Task              string             `json:"task,omitempty"`
	Pending           map[string]Pending `json:"pending,omitempty"`
	GeminiSeen        map[string]bool    `json:"geminiSeen,omitempty"`
	GeminiProjectHash string             `json:"geminiProjectHash,omitempty"`
	GeminiProjectDirs map[string]string  `json:"-"`
}

type wire struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
	ID        string          `json:"id"`
	CWD       string          `json:"cwd"`
	SessionID string          `json:"sessionId"`
	Session   string          `json:"session_id"`
	Turn      string          `json:"turn_id"`
	Name      string          `json:"name"`
	Call      string          `json:"call_id"`
	Input     json.RawMessage `json:"input"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Hook         string          `json:"hook_event_name"`
	Tool         string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
	ToolResult   struct {
		Interrupted bool `json:"interrupted"`
	} `json:"toolUseResult"`
}

func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func Supported(adapter string) bool {
	return adapter == "codex" || adapter == "claude" || adapter == "gemini" || adapter == "workshelf"
}

func cleanField(s string, max int) string {
	if len(s) > max || strings.ContainsAny(s, "\x00\r\n") {
		return ""
	}
	return s
}
func absolute(path, cwd string) string {
	if cleanField(path, 4096) == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(cwd) {
			return ""
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

// Feed consumes one complete JSONL record. Message text and arbitrary shell
// commands are deliberately not interpreted as file operations.
func (p *Parser) Feed(adapter, base string, line int64, data []byte) ([]Claim, int) {
	var v wire
	if !json.Valid(data) || json.Unmarshal(data, &v) != nil {
		return nil, 1
	}
	if adapter == "workshelf" {
		return parseMark(base, line, data)
	}
	if adapter == "gemini" {
		return p.gemini(line, data)
	}
	if adapter == "codex" {
		var payload wire
		if json.Unmarshal(v.Payload, &payload) != nil {
			return nil, 1
		}
		switch v.Type {
		case "session_meta":
			p.Session = cleanField(payload.ID, 256)
			p.CWD = absolute(payload.CWD, "")
			p.Pending = nil
			return nil, 0
		case "turn_context":
			if payload.CWD != "" {
				p.CWD = absolute(payload.CWD, "")
			}
			p.Task = cleanField(payload.Turn, 256)
			return nil, 0
		case "response_item":
			payload.Timestamp = v.Timestamp
			return p.codex(payload, line, data)
		default:
			return nil, 0
		}
	}
	if adapter == "claude" {
		session := v.SessionID
		if session == "" {
			session = v.Session
		}
		if session != "" && session != p.Session {
			p.Session = cleanField(session, 256)
			p.CWD = ""
			p.Pending = nil
		}
		if v.CWD != "" {
			p.CWD = absolute(v.CWD, "")
		}
		if v.Hook == "PostToolUse" {
			if v.Tool == "Read" {
				return nil, 0
			}
			claims := p.claudeCall(v.Tool, v.ToolUseID, v.ToolInput, line, data)
			var response struct {
				Path    string `json:"filePath"`
				Success *bool  `json:"success"`
				Error   bool   `json:"is_error"`
			}
			if json.Unmarshal(v.ToolResponse, &response) != nil || response.Error || response.Success != nil && !*response.Success || len(claims) != 1 || absolute(response.Path, p.CWD) != claims[0].Path {
				return nil, 1
			}
			for i := range claims {
				claims[i].Basis = "tool_result"
				claims[i].ClaimedAt = cleanField(v.Timestamp, 64)
			}
			return claims, 0
		}
		var blocks []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			UseID string          `json:"tool_use_id"`
			Error bool            `json:"is_error"`
		}
		if json.Unmarshal(v.Message.Content, &blocks) != nil {
			return nil, 0
		}
		var out []Claim
		gaps := 0
		for _, b := range blocks {
			if v.Type == "assistant" && b.Type == "tool_use" {
				if b.Name == "Read" {
					continue
				}
				claims := p.claudeCall(b.Name, b.ID, b.Input, line, data)
				if len(claims) > 0 {
					gaps += p.remember(b.ID, claims, b.Name == "Bash", false, false)
				} else {
					gaps++
				}
			}
			if v.Type == "user" && b.Type == "tool_result" {
				pending := p.Pending[b.UseID]
				success := !b.Error && !(pending.Shell && v.ToolResult.Interrupted)
				out = append(out, p.finish(b.UseID, success, line, data, v.Timestamp)...)
			}
		}
		return out, gaps
	}
	return nil, 1
}

func (p *Parser) remember(id string, claims []Claim, shell, literalWrite, execPatch bool) int {
	id = cleanField(id, 256)
	if id == "" {
		return 1
	}
	if p.Pending == nil {
		p.Pending = map[string]Pending{}
	}
	if _, exists := p.Pending[id]; exists {
		delete(p.Pending, id)
		return 1
	}
	if len(p.Pending) >= 32 || len(claims) > 16 {
		return 1
	}
	p.Pending[id] = Pending{Claims: claims, Shell: shell, LiteralWrite: literalWrite, ExecPatch: execPatch}
	return 0
}
func (p *Parser) finish(id string, success bool, line int64, data []byte, at string) []Claim {
	call, ok := p.Pending[id]
	delete(p.Pending, id)
	if !ok || !success {
		return nil
	}
	out := append([]Claim{}, call.Claims...)
	for i := range out {
		out[i].RequestLine, out[i].RequestSHA = out[i].Line, out[i].RecordSHA
		out[i].Line, out[i].RecordSHA = line, Hash(data)
		out[i].ClaimedAt = cleanField(at, 64)
		out[i].Basis = "tool_result"
	}
	return out
}
func (p *Parser) base(agent, call, path, action string, line int64, data []byte) Claim {
	return Claim{Agent: agent, WorkingDir: p.CWD, Session: p.Session, Task: p.Task, Call: cleanField(call, 256), Path: absolute(path, p.CWD), Action: action, Line: line, RecordSHA: Hash(data), Basis: "tool_result"}
}

func (p *Parser) claudeCall(tool, call string, input json.RawMessage, line int64, data []byte) []Claim {
	if p.Session == "" {
		return nil
	}
	var args struct {
		Path       string  `json:"file_path"`
		Content    *string `json:"content"`
		Notebook   string  `json:"notebook_path"`
		Command    string  `json:"command"`
		Background bool    `json:"run_in_background"`
	}
	if json.Unmarshal(input, &args) != nil {
		return nil
	}
	action := "edit"
	switch tool {
	case "Bash":
		if args.Background {
			return nil
		}
		path, content, ok := shellCat(args.Command)
		if !ok {
			return nil
		}
		c := p.base("claude", call, path, "write", line, data)
		if c.Path == "" {
			return nil
		}
		c.SHA256 = Hash([]byte(content))
		return []Claim{c}
	case "Write":
		action = "write"
	case "Edit", "MultiEdit":
	case "NotebookEdit":
		args.Path = args.Notebook
	default:
		return nil
	}
	c := p.base("claude", call, args.Path, action, line, data)
	if c.Path == "" {
		return nil
	}
	if tool == "Write" && args.Content != nil {
		c.SHA256 = Hash([]byte(*args.Content))
	}
	return []Claim{c}
}

var literalCatHeader = regexp.MustCompile(`^cat[ \t]+>[ \t]*(?:'([^']+)'|([A-Za-z0-9_./-]+))[ \t]+<<'([A-Za-z_][A-Za-z0-9_]*)'[ \t]*$`)
var literalExecPatch = regexp.MustCompile(`(?s)^\s*const\s+patch\s*=\s*("(\\.|[^"\\])*")\s*;\s*text\(await\s+tools\.apply_patch\(patch\)\)\s*;?\s*$`)
var literalExecInlinePatch = regexp.MustCompile(`(?s)^\s*text\(await\s+tools\.apply_patch\(("(\\.|[^"\\])*")\)\)\s*;?\s*$`)
var execPatchSuccess = regexp.MustCompile(`^Script completed\nWall time:? [^\n]+\nOutput:\n\{\}\s*$`)

func shellCat(command string) (string, string, bool) {
	command = strings.TrimLeft(command, " \t\r\n")
	command = strings.TrimRight(command, "\r\n")
	parts := strings.SplitN(command, "\n", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	header := literalCatHeader.FindStringSubmatch(parts[0])
	if header == nil {
		return "", "", false
	}
	lines := strings.Split(parts[1], "\n")
	if lines[len(lines)-1] != header[3] {
		return "", "", false
	}
	for _, line := range lines[:len(lines)-1] {
		if line == header[3] {
			return "", "", false
		}
	}
	path := header[1]
	if path == "" {
		path = header[2]
	}
	content := strings.Join(lines[:len(lines)-1], "\n")
	if len(lines) > 1 {
		content += "\n"
	}
	return path, content, true
}

func (p *Parser) codex(v wire, line int64, data []byte) ([]Claim, int) {
	switch v.Type {
	case "function_call", "custom_tool_call":
		shell := v.Name == "exec_command" || v.Name == "functions.exec_command"
		execPatch := v.Name == "exec" || v.Name == "functions.exec"
		if !shell && !execPatch && v.Name != "apply_patch" && v.Name != "functions.apply_patch" {
			return nil, 1
		}
		if p.Session == "" {
			return nil, 1
		}
		var patch string
		var claims []Claim
		literalWrite := false
		parser := *p
		if execPatch {
			var source string
			if json.Unmarshal(v.Input, &source) != nil {
				return nil, 1
			}
			match := literalExecPatch.FindStringSubmatch(source)
			if match == nil {
				match = literalExecInlinePatch.FindStringSubmatch(source)
			}
			if match == nil || json.Unmarshal([]byte(match[1]), &patch) != nil {
				return nil, 1
			}
		} else if shell {
			var args struct {
				Cmd     string `json:"cmd"`
				Workdir string `json:"workdir"`
			}
			if json.Unmarshal([]byte(v.Arguments), &args) != nil {
				return nil, 1
			}
			if args.Workdir != "" {
				parser.CWD = absolute(args.Workdir, p.CWD)
				if parser.CWD == "" {
					return nil, 1
				}
			}
			var ok bool
			patch, ok = shellPatch(args.Cmd)
			if !ok {
				path, content, matched := shellCat(args.Cmd)
				if !matched {
					return nil, 1
				}
				claim := parser.base("codex", v.Call, path, "write", line, data)
				if claim.Path == "" {
					return nil, 1
				}
				claim.SHA256 = Hash([]byte(content))
				claims = []Claim{claim}
				literalWrite = true
			}
		} else if json.Unmarshal(v.Input, &patch) != nil {
			var arg struct {
				Input string `json:"input"`
				Patch string `json:"patch"`
			}
			if json.Unmarshal([]byte(v.Arguments), &arg) == nil {
				patch = arg.Input
				if patch == "" {
					patch = arg.Patch
				}
			}
		}
		if !literalWrite {
			claims = parser.patchClaims(patch, v.Call, line, data)
		}
		if len(claims) == 0 {
			return nil, 1
		}
		return nil, p.remember(v.Call, claims, shell, literalWrite, execPatch)
	case "function_call_output", "custom_tool_call_output":
		text := outputText(v.Output)
		success := strings.HasPrefix(text, "Success. Updated the following files:\n")
		if pending, ok := p.Pending[v.Call]; ok && pending.ExecPatch {
			success = execPatchSuccess.MatchString(text)
		}
		if pending, ok := p.Pending[v.Call]; ok && pending.Shell {
			success = strings.Contains("\n"+text, "\nProcess exited with code 0\n") && (pending.LiteralWrite || strings.Contains("\n"+text, "\nSuccess. Updated the following files:\n"))
			if !success {
				p.finish(v.Call, false, line, data, v.Timestamp)
				return nil, 1
			}
		}
		return p.finish(v.Call, success, line, data, v.Timestamp), 0
	default:
		return nil, 0
	}
}

func shellPatch(command string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(command), "\n", 2)
	if len(parts) != 2 {
		return "", false
	}
	header := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(header, "apply_patch ") {
		return "", false
	}
	redirect := strings.TrimSpace(strings.TrimPrefix(header, "apply_patch"))
	if !strings.HasPrefix(redirect, "<<") {
		return "", false
	}
	delimiter := strings.TrimSpace(strings.TrimPrefix(redirect, "<<"))
	if len(delimiter) < 3 || (delimiter[0] != '\'' && delimiter[0] != '"') || delimiter[len(delimiter)-1] != delimiter[0] {
		return "", false
	}
	delimiter = delimiter[1 : len(delimiter)-1]
	for _, r := range delimiter {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "", false
		}
	}
	lines := strings.Split(parts[1], "\n")
	if len(lines) < 2 || lines[len(lines)-1] != delimiter {
		return "", false
	}
	for _, line := range lines[:len(lines)-1] {
		if line == delimiter {
			return "", false
		}
	}
	return strings.Join(lines[:len(lines)-1], "\n") + "\n", true
}

func outputText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" || b.Type == "input_text" {
				text += b.Text
			}
		}
	}
	return text
}

func (p *Parser) patchClaims(patch, call string, line int64, data []byte) []Claim {
	if !strings.HasPrefix(patch, "*** Begin Patch\n") || !strings.Contains(patch, "\n*** End Patch") {
		return nil
	}
	var out []Claim
	var added strings.Builder
	index := -1
	adding := false
	flush := func() {
		if adding && index >= 0 {
			out[index].SHA256 = Hash([]byte(added.String()))
		}
		added.Reset()
		adding = false
	}
	for _, text := range strings.Split(patch, "\n") {
		if strings.HasPrefix(text, "*** ") {
			if strings.HasPrefix(text, "*** Move to: ") && index >= 0 {
				out[index].BeforePath = out[index].Path
				out[index].Path = absolute(strings.TrimPrefix(text, "*** Move to: "), p.CWD)
				out[index].Action = "rename"
				continue
			}
			flush()
			action, path := "", ""
			for _, pair := range [][2]string{{"*** Add File: ", "write"}, {"*** Update File: ", "edit"}, {"*** Delete File: ", "remove"}} {
				if strings.HasPrefix(text, pair[0]) {
					action, path = pair[1], strings.TrimPrefix(text, pair[0])
					break
				}
			}
			if action != "" {
				c := p.base("codex", call, path, action, line, data)
				if c.Path == "" {
					return nil
				}
				out = append(out, c)
				index = len(out) - 1
				adding = action == "write"
			}
		} else if adding {
			if !strings.HasPrefix(text, "+") {
				return nil
			}
			added.WriteString(strings.TrimPrefix(text, "+"))
			added.WriteByte('\n')
		}
		if len(out) > 16 {
			return nil
		}
	}
	flush()
	return out
}

func parseMark(base string, line int64, data []byte) ([]Claim, int) {
	var m struct {
		File     string `json:"file"`
		Path     string `json:"path"`
		Agent    string `json:"agent"`
		Project  string `json:"project"`
		WorkType string `json:"worktype"`
		Task     string `json:"task"`
		Session  string `json:"session"`
		SHA      string `json:"sha256"`
		Time     string `json:"time"`
		Action   string `json:"action"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil, 1
	}
	path := m.Path
	if path == "" {
		path = m.File
	}
	path = absolute(path, base)
	agent := cleanField(m.Agent, 128)
	if path == "" || agent == "" {
		return nil, 1
	}
	sha := strings.ToLower(m.SHA)
	decoded, err := hex.DecodeString(sha)
	if err != nil || len(decoded) != 32 {
		sha = ""
	}
	return []Claim{{Agent: agent, Project: cleanField(m.Project, 256), WorkType: cleanField(m.WorkType, 256), Session: cleanField(m.Session, 256), Task: cleanField(m.Task, 256), Path: path, Action: "mark", SHA256: sha, ClaimedAt: cleanField(m.Time, 64), Line: line, RecordSHA: Hash(data), Basis: "self_report"}}, 0
}
