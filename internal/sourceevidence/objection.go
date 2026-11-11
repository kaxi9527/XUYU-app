package sourceevidence

import (
	"encoding/json"
	"strings"
)

// FeedObjection keeps message text in memory only, for matching or authorized model judgment.
func (p *Parser) FeedObjection(adapter string, line int64, data []byte, allowModel bool) (Claim, bool) {
	if p.Session == "" {
		return Claim{}, false
	}
	var outer struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
		Content   json.RawMessage `json:"content"`
		Message   struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &outer) != nil {
		return Claim{}, false
	}
	var content json.RawMessage
	switch adapter {
	case "codex":
		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if outer.Type != "response_item" || json.Unmarshal(outer.Payload, &payload) != nil || payload.Type != "message" || payload.Role != "user" {
			return Claim{}, false
		}
		content = payload.Content
	case "claude":
		if outer.Type != "user" || outer.Message.Role != "user" {
			return Claim{}, false
		}
		content = outer.Message.Content
	case "gemini":
		if outer.Type != "user" {
			return Claim{}, false
		}
		content = outer.Content
	default:
		return Claim{}, false
	}
	var text string
	if json.Unmarshal(content, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &parts) != nil {
			return Claim{}, false
		}
		for _, part := range parts {
			if part.Type == "" || part.Type == "text" || part.Type == "input_text" {
				text += part.Text
			}
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Claim{}, false
	}
	claim := Claim{Agent: adapter, Session: p.Session, WorkingDir: p.CWD, ClaimedAt: outer.Timestamp, Line: line, RecordSHA: Hash(data), Basis: "user_message"}
	normalized := strings.Trim(text, "。！! .")
	switch strings.ToLower(normalized) {
	case "撤销这次整理", "恢复这次整理", "我不同意这次整理", "我不同意这次整理，请恢复原状", "undo this organization", "restore this organization":
		claim.Action = "objection"
		return claim, true
	default:
		if strings.HasPrefix(normalized, "撤销") && strings.HasSuffix(normalized, "的整理") && len(normalized) > len("撤销的整理") {
			claim.Action, claim.Message = "objection", normalized
			return claim, true
		}
		if !allowModel {
			return Claim{}, false
		}
		claim.Action, claim.Message = "objection_candidate", text
		return claim, true
	}
}
