package provider

import (
	"errors"
	"net/url"
	"strings"
)

// WorkShelfSelected intentionally does not use All, Stored, Resolve, overlays,
// groups, stand-ins or provider fallbacks. It constructs the native endpoint
// from exactly one enabled account in WorkShelf's private account store.
func WorkShelfSelected(agent string) (Provider, error) {
	if !UXQASupported(agent) {
		return Provider{}, errors.New("不支持的订阅来源")
	}
	if err := WorkShelfAccountStorageError(); err != nil {
		return Provider{}, err
	}
	accounts := uxqaAccountPool(agent)
	if len(accounts) != 1 {
		return Provider{}, errors.New("请明确选择一个可用订阅账号")
	}
	p := accounts[0]
	if p.ID != agent || p.Account == nil || p.Account.Agent != agent || p.Account.User == "" || p.Account.standIn || p.Account.transport != nil || p.Account.clientFor != nil {
		return Provider{}, errors.New("订阅接收方无法核实")
	}
	return p, nil
}

// WorkShelfDestination permits only the native service for the selected
// subscription. Configured aliases, proxies as endpoints and redirects are not
// destinations. Codex does not support an output token cap; Claude runs a CLI.
func WorkShelfDestination(agent string, u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.ForceQuery || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch agent {
	case "codex":
		return host == "chatgpt.com" && u.Path == "/backend-api/codex/responses" && u.RawQuery == ""
	case "copilot":
		return u.RawQuery == "" && (u.Path == "/chat/completions" || u.Path == "/responses" || u.Path == "/v1/messages") && (host == "api.githubcopilot.com" || host == "api.individual.githubcopilot.com" || host == "api.business.githubcopilot.com" || host == "api.enterprise.githubcopilot.com")
	case "gemini", "antigravity":
		return (u.Path == "/v1internal:generateContent" && u.RawQuery == "" || u.Path == "/v1internal:streamGenerateContent" && u.RawQuery == "alt=sse") && (host == "cloudcode-pa.googleapis.com" || host == "daily-cloudcode-pa.googleapis.com")
	}
	return false
}
