package models

import (
	"errors"
	"net/textproto"
	"strings"
	"time"
)

// Options contains only non-secret, explicit protocol compatibility choices.
type Options struct {
	AuthHeader       string `json:"authHeader,omitempty"`
	AnthropicVersion string `json:"anthropicVersion,omitempty"`
	ChatTokenField   string `json:"chatTokenField,omitempty"`
	ProbeTokens      int    `json:"probeTokens,omitempty"`
}

func NormalizeOptions(protocol Protocol, in Options) (Options, error) {
	if !validProtocol(protocol) {
		return Options{}, errors.New("不支持所选模型协议")
	}
	if in.AuthHeader == "" {
		in.AuthHeader = "Authorization"
		if protocol == Anthropic {
			in.AuthHeader = "X-Api-Key"
		}
		if protocol == Gemini {
			in.AuthHeader = "X-Goog-Api-Key"
		}
	}
	if len(in.AuthHeader) > 80 {
		return Options{}, errors.New("鉴权请求头无效")
	}
	for _, c := range in.AuthHeader {
		if c > 127 || !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return Options{}, errors.New("鉴权请求头无效")
		}
	}
	in.AuthHeader = textproto.CanonicalMIMEHeaderKey(in.AuthHeader)
	lower := strings.ToLower(in.AuthHeader)
	for _, prefix := range []string{"content-", "proxy-", "sec-", "x-forwarded-", "x-http-method"} {
		if strings.HasPrefix(lower, prefix) {
			return Options{}, errors.New("这个请求头不能用作模型鉴权")
		}
	}
	switch lower {
	case "host", "connection", "cookie", "set-cookie", "accept", "accept-encoding", "user-agent", "transfer-encoding", "trailer", "te", "upgrade", "expect", "range", "referer", "origin", "anthropic-version":
		return Options{}, errors.New("这个请求头不能用作模型鉴权")
	}
	if protocol == Anthropic {
		if in.AnthropicVersion == "" {
			in.AnthropicVersion = "2023-06-01"
		}
		if _, err := time.Parse("2006-01-02", in.AnthropicVersion); err != nil {
			return Options{}, errors.New("Claude API 版本应为有效日期")
		}
	} else if in.AnthropicVersion != "" {
		return Options{}, errors.New("只有 Claude 协议使用 API 日期版本")
	}
	if protocol == Chat {
		if in.ChatTokenField == "" {
			in.ChatTokenField = "max_completion_tokens"
		}
		if in.ChatTokenField != "max_tokens" && in.ChatTokenField != "max_completion_tokens" {
			return Options{}, errors.New("Chat 输出上限字段无效")
		}
	} else if in.ChatTokenField != "" {
		return Options{}, errors.New("只有 Chat 协议使用此输出上限字段")
	}
	if in.ProbeTokens == 0 {
		in.ProbeTokens = 256
	}
	if in.ProbeTokens < 16 || in.ProbeTokens > 4096 {
		return Options{}, errors.New("连接测试输出上限应为 16 到 4096 token")
	}
	return in, nil
}

func NormalizeTarget(in Target) (Target, error) {
	endpoint, err := normalizeEndpoint(in.BaseURL)
	if err != nil {
		return Target{}, err
	}
	if in.ModelID != "" && !label(in.ModelID, 256) {
		return Target{}, errors.New("模型 ID 无效")
	}
	options, err := NormalizeOptions(in.Protocol, in.Options)
	if err != nil {
		return Target{}, err
	}
	in.BaseURL, in.Options = endpoint, options
	return in, nil
}

// ConnectionProblem carries no upstream error body, URL, header or reply text.
type ConnectionProblem struct {
	Code              string `json:"code"`
	HTTPStatus        int    `json:"httpStatus,omitempty"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

var connectionMessages = map[string]string{
	"invalid_request":      "服务拒绝了请求，请检查模型 ID、协议版本和兼容选项",
	"authentication":       "模型凭据无效，请检查后重新连接",
	"permission":           "当前凭据没有访问这个模型的权限",
	"quota":                "模型账户额度不足，请在供应商处检查",
	"not_found":            "模型或接口不存在，请检查地址与模型 ID",
	"rate_limited":         "模型服务限流，请稍后再试",
	"service":              "模型服务暂时不可用",
	"network":              "无法连接模型服务，请检查网络和地址",
	"tls":                  "模型服务证书验证失败，未向该服务发送凭据",
	"timeout":              "模型服务响应超时",
	"redirect":             "模型服务要求跳转，请直接填写最终服务地址",
	"invalid_response":     "模型服务返回的内容不符合所选协议",
	"response_too_large":   "模型服务返回内容超过本地读取上限",
	"no_text":              "模型没有返回可用文本，请检查模型类型或测试输出上限",
	"truncated":            "测试回复未完成，请检查或提高测试输出上限后重试",
	"blocked":              "模型拒绝了测试消息或触发内容限制",
	"unsupported":          "测试请求返回了未支持的响应类型",
	"list_unsupported":     "这个服务没有可用的模型列表，请手动填写模型 ID",
	"credential":           "鉴权内容无法用于 HTTP 请求，请检查凭据",
	"verification_failed":  "模型连接验证未通过",
	"recipient_unverified": "订阅尚无可核对的实际发送地址，或发送证明已失效；请到模型与账号重新测试连接后预览",
}

func (p ConnectionProblem) Valid() bool {
	_, ok := connectionMessages[p.Code]
	return ok && (p.HTTPStatus == 0 || p.HTTPStatus >= 100 && p.HTTPStatus <= 599) && p.RetryAfterSeconds >= 0 && p.RetryAfterSeconds <= 86400
}
func (p ConnectionProblem) Error() string {
	if !p.Valid() {
		return connectionMessages["verification_failed"]
	}
	return connectionMessages[p.Code]
}
