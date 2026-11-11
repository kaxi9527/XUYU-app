package sourceevidence

import "encoding/json"

// gemini reads Gemini CLI's append-only JSONL session records. A message may
// be appended more than once as tool results arrive, so completed tool IDs are
// retained in the per-session cursor. Conversation text is never retained.
func (p *Parser) gemini(line int64, data []byte) ([]Claim, int) {
	var record struct {
		SessionID   string `json:"sessionId"`
		ProjectHash string `json:"projectHash"`
		Type        string `json:"type"`
		Timestamp   string `json:"timestamp"`
		CWD         string `json:"cwd"`
		ToolCalls   []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Timestamp string `json:"timestamp"`
			Args      struct {
				Path string `json:"file_path"`
			} `json:"args"`
		} `json:"toolCalls"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, 1
	}
	if session := cleanField(record.SessionID, 256); session != "" && session != p.Session {
		p.Session, p.CWD, p.GeminiSeen, p.GeminiProjectHash = session, "", nil, ""
	}
	if len(record.ProjectHash) == 64 {
		p.GeminiProjectHash = cleanField(record.ProjectHash, 64)
		if p.CWD == "" {
			p.CWD = p.GeminiProjectDirs[p.GeminiProjectHash]
		}
	}
	if record.CWD != "" {
		p.CWD = absolute(record.CWD, "")
	}
	if record.Type != "gemini" || p.Session == "" {
		return nil, 0
	}
	var claims []Claim
	gaps := 0
	for _, call := range record.ToolCalls {
		if call.Status != "success" {
			continue
		}
		id := cleanField(call.ID, 256)
		if id == "" {
			gaps++
			continue
		}
		if p.GeminiSeen[id] {
			continue
		}
		action := ""
		switch call.Name {
		case "write_file":
			action = "write"
		case "replace":
			action = "edit"
		default:
			continue
		}
		claim := p.base("gemini", id, call.Args.Path, action, line, data)
		if claim.Path == "" {
			gaps++
			continue
		}
		claim.ClaimedAt = cleanField(call.Timestamp, 64)
		if claim.ClaimedAt == "" {
			claim.ClaimedAt = cleanField(record.Timestamp, 64)
		}
		claim.ProjectHash = p.GeminiProjectHash
		if p.GeminiSeen == nil {
			p.GeminiSeen = map[string]bool{}
		}
		p.GeminiSeen[id] = true
		claims = append(claims, claim)
	}
	return claims, gaps
}
