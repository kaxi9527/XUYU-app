// Package models owns WorkShelf's model configuration. It does not read another
// agent's configuration, download a runtime or perform file-organizing actions.
package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
)

var (
	ErrConflict   = errors.New("模型配置已变化，请重新读取")
	ErrCorrupt    = errors.New("模型配置无法读取，原配置已保留")
	ErrUnverified = errors.New("请先验证所选模型的连接")
	ErrStopped    = errors.New("Agent 已停止或连接已变化")
	ErrReused     = errors.New("同一操作标识不能用于不同的模型配置")
)

type Protocol string

const (
	Chat         Protocol = "chat"
	Responses    Protocol = "responses"
	Anthropic    Protocol = "anthropic"
	Gemini       Protocol = "gemini"
	Subscription Protocol = "subscription"
)

// A logical destination resolved only by the desktop's private account adapter.
const SubscriptionEndpoint = "https://accounts.workshelf.invalid"

func SubscriptionAgent(endpoint string) string {
	if !strings.HasPrefix(endpoint, SubscriptionEndpoint+"/") {
		return ""
	}
	agent := strings.TrimPrefix(endpoint, SubscriptionEndpoint+"/")
	switch agent {
	case "codex", "claude", "copilot", "gemini", "antigravity":
		return agent
	}
	return ""
}

type Provider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Protocol  Protocol `json:"protocol"`
	BaseURL   string   `json:"baseURL"`
	Version   uint64   `json:"version"`
	SecretRef string   `json:"secretRef,omitempty"`
	Options   Options  `json:"options,omitempty"`
}
type Capabilities struct {
	Text       string `json:"text"`
	Tools      string `json:"tools"`
	Structured string `json:"structured"`
	Streaming  string `json:"streaming"`
}
type Verification struct {
	ProviderVersion uint64       `json:"providerVersion"`
	At              string       `json:"at"`
	Capabilities    Capabilities `json:"capabilities"`
}
type Model struct {
	ID           string        `json:"id"`
	ProviderID   string        `json:"providerID"`
	ModelID      string        `json:"modelID"`
	Name         string        `json:"name"`
	Verification *Verification `json:"verification,omitempty"`
}
type Settings struct {
	Mode          string `json:"mode"`
	ActiveModelID string `json:"activeModelID,omitempty"`
	Epoch         uint64 `json:"epoch"`
}
type Receipt struct {
	OperationID string             `json:"operationID"`
	Digest      string             `json:"digest"`
	Revision    uint64             `json:"revision"`
	ProviderID  string             `json:"providerID,omitempty"`
	ModelID     string             `json:"modelID,omitempty"`
	Verified    *bool              `json:"verified,omitempty"`
	Problem     *ConnectionProblem `json:"problem,omitempty"`
}
type catalog struct {
	Schema     int        `json:"schema"`
	Revision   uint64     `json:"revision"`
	Providers  []Provider `json:"providers"`
	Models     []Model    `json:"models"`
	Settings   Settings   `json:"settings"`
	Operations []Receipt  `json:"operations"`
}
type PublicProvider struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Protocol      Protocol `json:"protocol"`
	BaseURL       string   `json:"baseURL"`
	HasCredential bool     `json:"hasCredential"`
	Options       Options  `json:"options"`
}
type State struct {
	SessionReady             bool             `json:"sessionReady"`
	PendingCredentialCleanup int              `json:"pendingCredentialCleanup"`
	Revision                 uint64           `json:"revision"`
	Providers                []PublicProvider `json:"providers"`
	Models                   []Model          `json:"models"`
	Settings                 Settings         `json:"settings"`
}
type ConnectionInput struct {
	ProviderID       string
	ModelRecordID    string
	ProviderName     string
	Protocol         Protocol
	BaseURL          string
	ModelID          string
	ModelName        string
	Credential       *credentials.Secret
	RemoveCredential bool
	Options          Options
}

// The server owns this opaque prepared request, including an immutable copy of
// its secret. A browser can only retain a preview token, never this object.
type Prepared struct {
	options                                                                 Options
	owner                                                                   string
	revision                                                                uint64
	providerID, modelID, name, baseURL, upstreamModel, modelName, secretRef string
	protocol                                                                Protocol
	secret                                                                  *credentials.Secret
	removeSecret                                                            bool
}

func (p *Prepared) Close() {
	if p != nil {
		p.secret.Destroy()
	}
}

// ConnectionPreview deliberately omits the secret and its storage reference.
type ConnectionPreview struct {
	Revision         uint64   `json:"revision"`
	ProviderID       string   `json:"providerID"`
	ModelRecordID    string   `json:"modelRecordID"`
	ProviderName     string   `json:"providerName"`
	Protocol         Protocol `json:"protocol"`
	BaseURL          string   `json:"baseURL"`
	ModelID          string   `json:"modelID"`
	ModelName        string   `json:"modelName"`
	CredentialAction string   `json:"credentialAction"`
	Options          Options  `json:"options"`
}

func (p *Prepared) Preview() ConnectionPreview {
	action := "keep"
	if p.secret != nil {
		action = "replace"
	} else if p.removeSecret {
		action = "remove"
	}
	return ConnectionPreview{p.revision, p.providerID, p.modelID, p.name, p.protocol,
		p.baseURL, p.upstreamModel, p.modelName, action, p.options}
}

type Target struct {
	Protocol         Protocol
	BaseURL, ModelID string
	Options          Options
	Subscription     *SubscriptionRecipient
}
type Verifier interface {
	Verify(context.Context, Target, *credentials.Secret) (Capabilities, error)
}

func identifier(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(data[:]), nil
}
func label(value string, limit int) bool {
	if strings.TrimSpace(value) != value || value == "" || len([]rune(value)) > limit {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}
func normalizeEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("请输入不含凭据、查询参数或片段的模型服务地址")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("模型地址必须使用 HTTPS 或本机 HTTP")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if u.Scheme == "http" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("远端模型必须使用 HTTPS；HTTP 仅用于本机服务")
	}
	if strings.ContainsAny(host, "\\% \t\r\n") || host == "" {
		return "", errors.New("模型服务主机无效")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("模型服务端口无效")
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." || strings.ContainsAny(part, "\\\r\n\x00") {
			return "", errors.New("模型服务路径无效")
		}
	}
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") {
		return "", errors.New("模型服务路径不能包含转义的目录分隔符")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}
func validProtocol(protocol Protocol) bool {
	return protocol == Chat || protocol == Responses || protocol == Anthropic || protocol == Gemini || protocol == Subscription
}
func validCapabilities(c Capabilities) bool {
	for _, v := range []string{c.Text, c.Tools, c.Structured, c.Streaming} {
		if v != "supported" && v != "unsupported" && v != "unknown" {
			return false
		}
	}
	return true
}
