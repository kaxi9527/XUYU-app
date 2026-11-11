// Package modelhttp implements explicit model connections. It never imports
// credentials from the environment, discovers local accounts or retries a probe.
package modelhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/credentials"
	"github.com/kaxi9527/WorkShelf/internal/models"
)

const maxResponseBytes = 2 << 20
const probeText = "Reply with the word OK."

type Client struct {
	http   *http.Client
	closed atomic.Bool
}

func NewClient() *Client {
	transport := &http.Transport{
		// Connect to the configured endpoint directly; a proxy is not an
		// implicitly authorized additional receiver of model credentials.
		Proxy:               nil,
		DialContext:         dialEndpoint,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, MaxIdleConns: 8, MaxIdleConnsPerHost: 2,
		MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true,
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func dialEndpoint(ctx context.Context, network, address string) (net.Conn, error) {
	connection, err := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		connection.Close()
		return nil, problem("network")
	}
	if strings.EqualFold(host, "localhost") {
		remote, ok := connection.RemoteAddr().(*net.TCPAddr)
		if !ok || !remote.IP.IsLoopback() {
			connection.Close()
			return nil, problem("network")
		}
	}
	return connection, nil
}

func (c *Client) Close()                            { c.closed.Store(true); c.http.CloseIdleConnections() }
func problem(code string) *models.ConnectionProblem { return &models.ConnectionProblem{Code: code} }

func (c *Client) request(ctx context.Context, method, address string, target models.Target, secret *credentials.Secret, body any) ([]byte, error) {
	if c.closed.Load() {
		return nil, models.ErrStopped
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, problem("invalid_request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(payload))
	if err != nil {
		return nil, problem("invalid_request")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if target.Protocol == models.Anthropic {
		req.Header.Set("Anthropic-Version", target.Options.AnthropicVersion)
	}
	if secret != nil {
		value, e := secret.Bytes()
		if e != nil {
			return nil, problem("credential")
		}
		defer credentials.Wipe(value)
		if len(value) > 16384 {
			return nil, problem("credential")
		}
		for _, ch := range value {
			if ch < 33 || ch > 126 {
				return nil, problem("credential")
			}
		}
		text := string(value)
		if target.Options.AuthHeader == "Authorization" {
			text = "Bearer " + text
		}
		req.Header.Set(target.Options.AuthHeader, text)
		// net/http may copy strings internally. This limits our own retention;
		// it does not promise wiping transport/runtime copies from memory.
		defer req.Header.Del(target.Options.AuthHeader)
	}
	// A generation POST is one attempt. HTTP/2 REFUSED_STREAM/GOAWAY recovery
	// otherwise uses the GetBody automatically installed by bytes.NewReader to
	// send the whole prompt again, independent of application retry settings.
	if method == http.MethodPost && len(payload) != 0 {
		req.GetBody = nil
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, networkProblem(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := statusProblem(response.StatusCode, response.Header.Get("Retry-After"))
		// Accept only known machine codes from a bounded error envelope. The
		// provider's message and response body never become diagnostic output.
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		defer credentials.Wipe(data)
		var envelope struct {
			Error struct {
				Code any    `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if response.StatusCode == 429 && json.Unmarshal(data, &envelope) == nil {
			for _, code := range []any{envelope.Error.Code, envelope.Error.Type} {
				if code == "insufficient_quota" || code == "billing_hard_limit_reached" {
					failure.Code = "quota"
				}
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, networkProblem(ctx, err)
	}
	if len(data) > maxResponseBytes {
		return nil, problem("response_too_large")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed.Load() {
		return nil, models.ErrStopped
	}
	return data, nil
}

func networkProblem(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	if errors.As(err, &certificate) || errors.As(err, &authority) || errors.As(err, &host) {
		return problem("tls")
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return problem("timeout")
	}
	return problem("network")
}

func statusProblem(status int, retry string) *models.ConnectionProblem {
	code := "invalid_request"
	switch {
	case status >= 300 && status < 400:
		code = "redirect"
	case status == 401:
		code = "authentication"
	case status == 402:
		code = "quota"
	case status == 403:
		code = "permission"
	case status == 404:
		code = "not_found"
	case status == 408 || status == 504:
		code = "timeout"
	case status == 429:
		code = "rate_limited"
	case status >= 500:
		code = "service"
	}
	result := &models.ConnectionProblem{Code: code, HTTPStatus: status}
	if code == "rate_limited" || code == "service" {
		seconds, err := strconv.Atoi(strings.TrimSpace(retry))
		if err != nil {
			if date, e := http.ParseTime(retry); e == nil {
				seconds = int(time.Until(date).Seconds())
			}
		}
		if seconds < 0 {
			seconds = 0
		}
		if seconds > 86400 {
			seconds = 86400
		}
		result.RetryAfterSeconds = seconds
	}
	return result
}

func object(data []byte, dest any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, dest) != nil {
		return problem("invalid_response")
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(trimmed, &envelope) != nil {
		return problem("invalid_response")
	}
	if len(envelope.Error) > 0 && !bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null")) {
		return problem("invalid_response")
	}
	return nil
}
