// Package ai implements the original OpenAI-compatible transport. Credentials
// are supplied in memory by the application, never by a view or saved snapshot.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"liberty.local/liberty/internal/domain"
	"net/http"
	"strings"
	"time"
)

var ErrConfiguration = errors.New("ai_request_configuration_invalid")
var ErrNetwork = errors.New("ai_network_failed")
var ErrTimeout = errors.New("ai_request_timeout")

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("ai_http_status_%d", e.Status) }

type Client struct{}

func (Client) Complete(ctx context.Context, input domain.SummaryRequest) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	base = strings.TrimRight(strings.TrimSuffix(base, "/chat/completions"), "/")
	if input.Credential.Empty() || strings.TrimSpace(input.Model) == "" {
		return "", ErrConfiguration
	}
	target, err := resolveTarget(ctx, base, defaultLookup)
	if err != nil {
		return "", err
	}
	transport := target.transport()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	input.Model = normalizeModel(target.base.Hostname(), input.Model)
	return completeWithRetry(ctx, client, target.endpoint(), input)
}
func completeWithRetry(ctx context.Context, client *http.Client, endpoint string, input domain.SummaryRequest) (string, error) {
	raw, retryFormat, err := completeOnce(ctx, client, endpoint, input, true)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil {
		return raw, nil
	}
	if retryFormat {
		raw, _, err = completeOnce(ctx, client, endpoint, input, false)
	} else if errors.Is(err, ErrNetwork) || errors.Is(err, ErrTimeout) {
		raw, _, err = completeOnce(ctx, client, endpoint, input, true)
	}
	return raw, err
}
func completeOnce(ctx context.Context, client *http.Client, endpoint string, input domain.SummaryRequest, format bool) (string, bool, error) {
	payload := map[string]any{"model": strings.TrimSpace(input.Model), "messages": []map[string]string{{"role": "system", "content": input.SystemPrompt}, {"role": "user", "content": input.UserPrompt}}}
	if format {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, ErrConfiguration
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", false, ErrConfiguration
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+input.Credential.Reveal())
	res, err := client.Do(req)
	if err != nil {
		return "", false, networkError(ctx, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, domain.MaxAIResponseBytes+1))
	if err != nil {
		return "", false, networkError(ctx, err)
	}
	if len(data) > domain.MaxAIResponseBytes {
		return "", false, domain.ErrAIResponse
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		fallback := false
		if format && (res.StatusCode == 400 || res.StatusCode == 404 || res.StatusCode == 422 || res.StatusCode == 415) {
			lower := strings.ToLower(string(data))
			for _, s := range []string{"response_format", "json_object", "unsupported parameter", "unknown parameter", "invalid parameter"} {
				fallback = fallback || strings.Contains(lower, s)
			}
		}
		return "", fallback, &HTTPError{Status: res.StatusCode}
	}
	return string(data), false, nil
}
func networkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ErrTimeout
	}
	return ErrNetwork
}
func normalizeModel(host, model string) string {
	model = strings.TrimSpace(model)
	// Provider aliases apply only on the actual provider host.
	if strings.EqualFold(host, "api.deepseek.com") {
		value := strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(model))
		switch value {
		case "deepseek-v4":
			return "deepseek-v4-pro"
		case "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner":
			return value
		}
	}
	return model
}
