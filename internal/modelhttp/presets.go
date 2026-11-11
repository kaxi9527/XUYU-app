package modelhttp

import "github.com/kaxi9527/WorkShelf/internal/models"

// Presets are editable starting points, not claims of account/model availability.
// No model ID is guessed; users can discover one or enter it themselves.
type Preset struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Protocol models.Protocol `json:"protocol"`
	BaseURL  string          `json:"baseURL"`
	Local    bool            `json:"local"`
	Options  models.Options  `json:"options"`
}

func Presets() []Preset {
	return []Preset{
		{ID: "openai", Name: "OpenAI", Protocol: models.Responses, BaseURL: "https://api.openai.com/v1"},
		{ID: "claude", Name: "Claude", Protocol: models.Anthropic, BaseURL: "https://api.anthropic.com/v1", Options: models.Options{AuthHeader: "Authorization", AnthropicVersion: "2026-10-21"}},
		{ID: "gemini", Name: "Google Gemini", Protocol: models.Gemini, BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
		{ID: "deepseek", Name: "DeepSeek", Protocol: models.Chat, BaseURL: "https://api.deepseek.com", Options: models.Options{ChatTokenField: "max_tokens"}},
		{ID: "ollama", Name: "Ollama", Protocol: models.Chat, BaseURL: "http://127.0.0.1:11434/v1", Local: true, Options: models.Options{ChatTokenField: "max_tokens"}},
		{ID: "lmstudio", Name: "LM Studio", Protocol: models.Chat, BaseURL: "http://127.0.0.1:1234/v1", Local: true, Options: models.Options{ChatTokenField: "max_tokens"}},
	}
}
