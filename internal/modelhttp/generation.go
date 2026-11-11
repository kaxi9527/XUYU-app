package modelhttp

import (
	"context"
	"net/http"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

func (c *Client) Generate(ctx context.Context, target models.Target, key *credentials.Secret, request models.GenerationRequest) (models.GenerationResult, error) {
	address, body, err := GenerationBody(target, request)
	if err != nil {
		return models.GenerationResult{}, err
	}
	target, err = models.NormalizeTarget(target)
	if err != nil {
		return models.GenerationResult{}, problem("invalid_request")
	}
	data, err := c.request(ctx, http.MethodPost, address, target, key, body)
	if err != nil {
		return models.GenerationResult{}, err
	}
	result, err := DecodeGeneration(target.Protocol, data)
	if err == nil {
		result.Usage.OpenRouterDebit = openRouterDebit(target, data)
	}
	return result, err
}

// GenerationBody reuses verified endpoint/token-field compatibility. The
// subscription bridge uses the same Chat body with its private transport.
func GenerationBody(target models.Target, request models.GenerationRequest) (string, any, error) {
	if err := request.Validate(); err != nil {
		return "", nil, err
	}
	t, err := models.NormalizeTarget(target)
	if err != nil || t.ModelID == "" {
		return "", nil, problem("invalid_request")
	}
	address, raw, err := probeRequest(t)
	if err != nil {
		return "", nil, err
	}
	body := raw.(map[string]any)
	user := map[string]any{"role": "user", "content": request.Input}
	messages := []any{}
	if request.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": request.System})
	}
	messages = append(messages, user)
	switch t.Protocol {
	case models.Chat:
		body["messages"] = messages
		body[t.Options.ChatTokenField] = request.MaxOutputTokens
	case models.Responses:
		body["input"] = messages
		body["max_output_tokens"] = request.MaxOutputTokens
	case models.Anthropic:
		body["messages"] = []any{user}
		body["max_tokens"] = request.MaxOutputTokens
		if request.System != "" {
			body["system"] = request.System
		}
	case models.Gemini:
		body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": request.Input}}}}
		body["generationConfig"] = map[string]any{"maxOutputTokens": request.MaxOutputTokens}
		if request.System != "" {
			body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": request.System}}}
		}
	default:
		return "", nil, problem("unsupported")
	}
	return address, body, nil
}

// DecodeGeneration accepts completed text only, never executable tool calls or
// hidden reasoning. It is shared with the subscription transport.
func DecodeGeneration(protocol models.Protocol, data []byte) (models.GenerationResult, error) {
	if len(data) > maxResponseBytes {
		return models.GenerationResult{}, problem("response_too_large")
	}
	text, err := textResponse(protocol, data)
	if err != nil {
		return models.GenerationResult{}, err
	}
	var r struct {
		Usage struct {
			Input        *int64 `json:"input_tokens"`
			Output       *int64 `json:"output_tokens"`
			Total        *int64 `json:"total_tokens"`
			Prompt       *int64 `json:"prompt_tokens"`
			Completion   *int64 `json:"completion_tokens"`
			CacheRead    *int64 `json:"cache_read_input_tokens"`
			CacheWrite   *int64 `json:"cache_creation_input_tokens"`
			InputDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			PromptDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			OutputDetails struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
			CompletionDetails struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Gemini struct {
			Input     *int64 `json:"promptTokenCount"`
			Output    *int64 `json:"candidatesTokenCount"`
			Total     *int64 `json:"totalTokenCount"`
			Cache     *int64 `json:"cachedContentTokenCount"`
			Reasoning *int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err = object(data, &r); err != nil {
		return models.GenerationResult{}, err
	}
	u := models.Usage{}
	switch protocol {
	case models.Chat:
		u = models.Usage{InputTokens: r.Usage.Prompt, OutputTokens: r.Usage.Completion, TotalTokens: r.Usage.Total, CachedInputTokens: r.Usage.PromptDetails.Cached, ReasoningTokens: r.Usage.CompletionDetails.Reasoning}
	case models.Responses:
		u = models.Usage{InputTokens: r.Usage.Input, OutputTokens: r.Usage.Output, TotalTokens: r.Usage.Total, CachedInputTokens: r.Usage.InputDetails.Cached, ReasoningTokens: r.Usage.OutputDetails.Reasoning}
	case models.Anthropic:
		u = models.Usage{InputTokens: r.Usage.Input, OutputTokens: r.Usage.Output, CachedInputTokens: r.Usage.CacheRead, CacheWriteTokens: r.Usage.CacheWrite}
	case models.Gemini:
		u = models.Usage{InputTokens: r.Gemini.Input, OutputTokens: r.Gemini.Output, TotalTokens: r.Gemini.Total, CachedInputTokens: r.Gemini.Cache, ReasoningTokens: r.Gemini.Reasoning}
	}
	result := models.GenerationResult{Text: text, Usage: u}
	if err = result.Validate(); err != nil {
		return models.GenerationResult{}, err
	}
	return result, nil
}
