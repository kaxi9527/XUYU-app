package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/modelhttp"
	"github.com/kaxi9527/WorkShelf/internal/models"
	"strings"
)

// API and subscription models share one catalog, verification and active-model
// selection. No ephemeral helper URL or bearer token is persisted as a provider.
type accountModelAdapter struct {
	direct  *modelhttp.Client
	request func(context.Context, string, string, any, any) error
}

func (a *accountModelAdapter) Generate(ctx context.Context, target models.Target, key *credentials.Secret, input models.GenerationRequest) (models.GenerationResult, error) {
	if target.Protocol != models.Subscription {
		return a.direct.Generate(ctx, target, key, input)
	}
	if models.SubscriptionAgent(target.BaseURL) == "" || models.SubscriptionAgent(target.BaseURL) != subscriptionAgent(target.ModelID) || key != nil {
		return models.GenerationResult{}, errors.New("订阅模型配置无效")
	}
	if agent := subscriptionAgent(target.ModelID); agent == "claude" || agent == "codex" && !input.AllowUncappedOutput {
		return models.GenerationResult{}, errors.New("此订阅尚不能保证输出限额，暂不用于项目分析；账号授权和连接测试仍可使用")
	}
	if a.request == nil || target.Subscription == nil || !target.Subscription.Valid() || target.Subscription.ModelID != target.ModelID || target.Subscription.Agent != models.SubscriptionAgent(target.BaseURL) {
		return models.GenerationResult{}, &models.ConnectionProblem{Code: "recipient_unverified"}
	}
	_, body, err := modelhttp.GenerationBody(models.Target{Protocol: models.Chat, BaseURL: models.SubscriptionEndpoint, ModelID: target.ModelID}, input)
	if err != nil {
		return models.GenerationResult{}, err
	}
	body.(map[string]any)["recipient"] = *target.Subscription
	var response json.RawMessage
	if err = a.request(ctx, "POST", "/control/generate-text", body, &response); err != nil {
		return models.GenerationResult{}, err
	}
	return modelhttp.DecodeGeneration(models.Chat, response)
}

func subscriptionAgent(id string) string {
	agent, name, ok := strings.Cut(id, "/")
	if ok && name != "" && validAccountAgent(agent) {
		return agent
	}
	return ""
}

func (a *accountModelAdapter) Verify(ctx context.Context, target models.Target, secret *credentials.Secret) (models.Capabilities, error) {
	if target.Protocol != models.Subscription {
		return a.direct.Verify(ctx, target, secret)
	}
	if a.request == nil {
		return models.Capabilities{}, &models.ConnectionProblem{Code: "recipient_unverified"}
	}
	if models.SubscriptionAgent(target.BaseURL) == "" || models.SubscriptionAgent(target.BaseURL) != subscriptionAgent(target.ModelID) || secret != nil {
		return models.Capabilities{}, errors.New("订阅模型配置无效")
	}
	if agent := subscriptionAgent(target.ModelID); agent != "claude" {
		var probe struct {
			Recipient models.SubscriptionRecipient `json:"recipient"`
			Result    json.RawMessage              `json:"result"`
		}
		if err := a.request(ctx, "POST", "/control/probe-text", map[string]string{"model": target.ModelID}, &probe); err != nil {
			return models.Capabilities{}, &models.ConnectionProblem{Code: "recipient_unverified"}
		}
		if !probe.Recipient.Valid() || probe.Recipient.ModelID != target.ModelID {
			return models.Capabilities{}, &models.ConnectionProblem{Code: "recipient_unverified"}
		}
		if _, err := modelhttp.DecodeGeneration(models.Chat, probe.Result); err != nil {
			return models.Capabilities{}, err
		}
		return models.Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	err := a.request(ctx, "POST", "/v1/chat/completions", map[string]any{"model": target.ModelID, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 64, "stream": false}, &response)
	if err != nil {
		return models.Capabilities{}, err
	}
	if len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return models.Capabilities{}, errors.New("订阅模型没有返回有效文本")
	}
	return models.Capabilities{Text: "supported", Tools: "unknown", Structured: "unknown", Streaming: "unknown"}, nil
}

func (a *accountModelAdapter) SubscriptionRecipient(ctx context.Context, target models.Target) (models.SubscriptionRecipient, error) {
	if target.Protocol != models.Subscription || models.SubscriptionAgent(target.BaseURL) != subscriptionAgent(target.ModelID) || a.request == nil {
		return models.SubscriptionRecipient{}, &models.ConnectionProblem{Code: "recipient_unverified"}
	}
	var out models.SubscriptionRecipient
	if err := a.request(ctx, "POST", "/control/generation-recipient", map[string]string{"model": target.ModelID}, &out); err != nil || !out.Valid() || out.ModelID != target.ModelID {
		return models.SubscriptionRecipient{}, &models.ConnectionProblem{Code: "recipient_unverified"}
	}
	return out, nil
}

func (a *accountModelAdapter) ListModels(ctx context.Context, target models.Target, secret *credentials.Secret, cursor string) (models.ModelPage, error) {
	if target.Protocol != models.Subscription {
		return a.direct.ListModels(ctx, target, secret, cursor)
	}
	if models.SubscriptionAgent(target.BaseURL) == "" || secret != nil {
		return models.ModelPage{}, errors.New("订阅模型配置无效")
	}
	return a.list(ctx, models.SubscriptionAgent(target.BaseURL))
}

func (a *accountModelAdapter) list(ctx context.Context, agent string) (models.ModelPage, error) {
	if !validAccountAgent(agent) {
		return models.ModelPage{}, errors.New("订阅类型无效")
	}
	var reply struct {
		Models   []string `json:"models"`
		Warnings []string `json:"warnings"`
	}
	if err := a.request(ctx, "GET", "/control/models?agent="+agent, nil, &reply); err != nil {
		return models.ModelPage{}, err
	}
	page := models.ModelPage{Models: []models.AvailableModel{}}
	for _, id := range reply.Models {
		if subscriptionAgent(id) == agent {
			page.Models = append(page.Models, models.AvailableModel{ID: id, Name: strings.TrimPrefix(id, agent+"/")})
		}
	}
	if len(page.Models) == 0 && len(reply.Warnings) > 0 {
		return page, errors.New("尚未读取到订阅模型，请检查账号授权与套餐权限")
	}
	return page, nil
}

func (s *ShelfService) ListSubscriptionModels(agent string) (models.ModelPage, error) {
	if err := s.claimAccountOwnership(); err != nil {
		return models.ModelPage{}, err
	}
	a := accountModelAdapter{request: s.connections.accounts.requestContext}
	return a.list(context.Background(), agent)
}

func (s *ShelfService) PreviewSubscriptionModel(agent, modelID string, revision uint64) (ModelConnectionPreview, error) {
	if subscriptionAgent(modelID) != agent || !validAccountAgent(agent) {
		return ModelConnectionPreview{}, errors.New("请选择该订阅的模型")
	}
	state, err := s.ModelAccounts()
	if err != nil {
		return ModelConnectionPreview{}, err
	}
	available := false
	for _, account := range state.Accounts {
		if account.Agent == agent && account.On && account.Lapsed == "" {
			available = true
		}
	}
	if !available {
		return ModelConnectionPreview{}, errors.New("请先启用一个有效订阅账号")
	}
	names := map[string]string{"codex": "ChatGPT / Codex 订阅", "claude": "Claude 订阅", "copilot": "GitHub Copilot 订阅", "gemini": "Gemini Code Assist 订阅", "antigravity": "Antigravity 订阅"}
	input := ModelConnectionInput{Revision: revision, ProviderName: names[agent], Protocol: models.Subscription, BaseURL: models.SubscriptionEndpoint + "/" + agent, ModelID: modelID, ModelName: strings.TrimPrefix(modelID, agent+"/")}
	current, err := s.ModelConnections()
	if err != nil {
		return ModelConnectionPreview{}, err
	}
	for _, p := range current.State.Providers {
		if p.Protocol == models.Subscription && p.Name == input.ProviderName {
			input.ProviderID = p.ID
		}
	}
	for _, m := range current.State.Models {
		if m.ProviderID == input.ProviderID && m.ModelID == modelID {
			input.ModelRecordID = m.ID
		}
	}
	return s.PreviewModelConnection(input)
}
