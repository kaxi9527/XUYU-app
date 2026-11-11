package modelhttp

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func (c *Client) Verify(ctx context.Context, target models.Target, secret *credentials.Secret) (models.Capabilities, error) {
	target, err := models.NormalizeTarget(target)
	if err != nil || target.ModelID == "" {
		return models.Capabilities{}, problem("invalid_request")
	}
	address, body, err := probeRequest(target)
	if err != nil {
		return models.Capabilities{}, err
	}
	data, err := c.request(ctx, http.MethodPost, address, target, secret, body)
	if err != nil {
		return models.Capabilities{}, err
	}
	if err = probeResponse(target.Protocol, data); err != nil {
		return models.Capabilities{}, err
	}
	// A plain text probe proves no tool, structured output or streaming ability.
	return models.Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
}

func probeRequest(t models.Target) (string, any, error) {
	message := []any{map[string]any{"role": "user", "content": probeText}}
	switch t.Protocol {
	case models.Chat:
		return t.BaseURL + "/chat/completions", map[string]any{"model": t.ModelID, "messages": message, "stream": false, t.Options.ChatTokenField: t.Options.ProbeTokens}, nil
	case models.Responses:
		return t.BaseURL + "/responses", map[string]any{"model": t.ModelID, "input": message, "stream": false, "store": false, "max_output_tokens": t.Options.ProbeTokens}, nil
	case models.Anthropic:
		return t.BaseURL + "/messages", map[string]any{"model": t.ModelID, "messages": message, "stream": false, "max_tokens": t.Options.ProbeTokens}, nil
	case models.Gemini:
		name := strings.TrimPrefix(t.ModelID, "models/")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return "", nil, problem("invalid_request")
		}
		return t.BaseURL + "/models/" + url.PathEscape(name) + ":generateContent", map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": probeText}}}},
			"generationConfig": map[string]any{"maxOutputTokens": t.Options.ProbeTokens},
		}, nil
	}
	return "", nil, problem("unsupported")
}

func probeResponse(protocol models.Protocol, data []byte) error {
	_, err := textResponse(protocol, data)
	return err
}

func textResponse(protocol models.Protocol, data []byte) (string, error) {
	var text strings.Builder
	switch protocol {
	case models.Chat:
		var response struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Role      string  `json:"role"`
					Content   string  `json:"content"`
					Refusal   *string `json:"refusal"`
					ToolCalls []any   `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := object(data, &response); err != nil {
			return "", err
		}
		if len(response.Choices) > 1 {
			return "", problem("invalid_response")
		}
		if len(response.Choices) == 0 {
			return "", problem("no_text")
		}
		choice := response.Choices[0]
		if choice.Message.Refusal != nil && *choice.Message.Refusal != "" || choice.FinishReason == "content_filter" {
			return "", problem("blocked")
		}
		if choice.FinishReason == "length" {
			return "", problem("truncated")
		}
		if len(choice.Message.ToolCalls) > 0 || choice.FinishReason == "tool_calls" || choice.FinishReason == "function_call" {
			return "", problem("unsupported")
		}
		if choice.FinishReason != "stop" || choice.Message.Role != "assistant" {
			return "", problem("invalid_response")
		}
		text.WriteString(choice.Message.Content)
	case models.Responses:
		var response struct {
			Status string `json:"status"`
			Output []struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Status  string `json:"status"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if err := object(data, &response); err != nil {
			return "", err
		}
		if response.Status == "incomplete" || response.Status == "in_progress" || response.Status == "queued" {
			return "", problem("truncated")
		}
		if response.Status != "completed" {
			return "", problem("invalid_response")
		}
		for _, item := range response.Output {
			if item.Type == "reasoning" {
				continue
			}
			if item.Type != "message" || item.Role != "assistant" {
				return "", problem("unsupported")
			}
			if item.Status != "completed" {
				return "", problem("truncated")
			}
			for _, part := range item.Content {
				if part.Type == "refusal" {
					return "", problem("blocked")
				}
				if part.Type != "output_text" {
					return "", problem("unsupported")
				}
				text.WriteString(part.Text)
			}
		}
	case models.Anthropic:
		var response struct {
			Type       string `json:"type"`
			Role       string `json:"role"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := object(data, &response); err != nil {
			return "", err
		}
		if response.StopReason == "max_tokens" || response.StopReason == "pause_turn" {
			return "", problem("truncated")
		}
		if response.StopReason == "refusal" {
			return "", problem("blocked")
		}
		if response.StopReason == "tool_use" {
			return "", problem("unsupported")
		}
		if response.Type != "message" || response.Role != "assistant" || response.StopReason != "end_turn" && response.StopReason != "stop_sequence" {
			return "", problem("invalid_response")
		}
		for _, part := range response.Content {
			if part.Type == "thinking" || part.Type == "redacted_thinking" {
				continue
			}
			if part.Type != "text" {
				return "", problem("unsupported")
			}
			text.WriteString(part.Text)
		}
	case models.Gemini:
		var response struct {
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
			Candidates []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text         string `json:"text"`
						Thought      bool   `json:"thought"`
						FunctionCall any    `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := object(data, &response); err != nil {
			return "", err
		}
		if response.PromptFeedback.BlockReason != "" {
			return "", problem("blocked")
		}
		if len(response.Candidates) > 1 {
			return "", problem("invalid_response")
		}
		if len(response.Candidates) == 0 {
			return "", problem("no_text")
		}
		candidate := response.Candidates[0]
		if candidate.FinishReason == "MAX_TOKENS" {
			return "", problem("truncated")
		}
		if candidate.FinishReason == "" || candidate.FinishReason == "FINISH_REASON_UNSPECIFIED" {
			return "", problem("invalid_response")
		}
		if candidate.FinishReason != "STOP" {
			return "", problem("blocked")
		}
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				return "", problem("unsupported")
			}
			if !part.Thought {
				text.WriteString(part.Text)
			}
		}
	default:
		return "", problem("unsupported")
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", problem("no_text")
	}
	return text.String(), nil
}
