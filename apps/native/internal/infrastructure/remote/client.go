// Package remote implements Liberty Meeting V1. It intentionally exposes only
// the original client's supported routes, even if the server advertises more.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

const maxResponse = 16 << 20
const maxDiscard = 64 << 10

type Client struct{}
type session struct {
	client *http.Client
	target trustedTarget
	token  domain.Secret
}

func connect(ctx context.Context, connection application.RemoteConnection) (*session, func(), error) {
	if connection.Token.Empty() || strings.ContainsAny(connection.Token.Reveal(), "\r\n\x00") {
		return nil, nil, domain.ErrRemote
	}
	target, err := resolveTarget(ctx, connection.URL, defaultLookup)
	if err != nil {
		return nil, nil, domain.ErrRemote
	}
	transport := target.transport()
	return &session{client: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, target: target, token: connection.Token}, transport.CloseIdleConnections, nil
}
func (s *session) request(ctx context.Context, method string, path []string, payload any, discard bool) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, domain.ErrRemote
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.target.endpoint(path...), body)
	if err != nil {
		return nil, domain.ErrRemote
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token.Reveal())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, safeError(ctx)
	}
	defer res.Body.Close()
	limit := maxResponse
	if discard || res.StatusCode < 200 || res.StatusCode >= 300 {
		limit = maxDiscard
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(limit+1)))
	if err != nil || len(data) > limit || res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, safeError(ctx)
	}
	if discard {
		return nil, nil
	}
	contentType := strings.ToLower(res.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/json") && !strings.Contains(contentType, "+json") {
		return nil, domain.ErrRemote
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, domain.ErrRemote
	}
	return data, nil
}
func safeError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return domain.ErrRemote
}
func (s *session) capabilities(ctx context.Context) (domain.RemoteCapabilities, error) {
	data, err := s.request(ctx, http.MethodGet, []string{"api", "capabilities"}, nil, false)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	var caps domain.RemoteCapabilities
	if !decodeWire(data, &caps) || caps.Normalize() != nil {
		return domain.RemoteCapabilities{}, domain.ErrRemote
	}
	return caps, nil
}
func startOperation(ctx context.Context, connection application.RemoteConnection, operation string) (*session, func(), error) {
	s, close, err := connect(ctx, connection)
	if err != nil {
		return nil, nil, err
	}
	caps, err := s.capabilities(ctx)
	if err != nil {
		close()
		return nil, nil, err
	}
	if !caps.Supports(operation) {
		close()
		return nil, nil, domain.ErrRemote
	}
	return s, close, nil
}
func (Client) Capabilities(ctx context.Context, connection application.RemoteConnection) (domain.RemoteCapabilities, error) {
	s, close, err := connect(ctx, connection)
	if err != nil {
		return domain.RemoteCapabilities{}, err
	}
	defer close()
	return s.capabilities(ctx)
}
func (Client) List(ctx context.Context, connection application.RemoteConnection) ([]domain.Result, error) {
	s, close, err := startOperation(ctx, connection, "jobs.list")
	if err != nil {
		return nil, err
	}
	defer close()
	data, err := s.request(ctx, http.MethodGet, []string{"api", "jobs"}, nil, false)
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return nil, domain.ErrRemote
	}
	results := make([]domain.Result, len(raw))
	for i, item := range raw {
		results[i], err = parseJob(item)
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
func (Client) Read(ctx context.Context, c application.RemoteConnection, id string, result bool) (domain.Result, error) {
	path := []string{"api", "jobs", id}
	operation := "jobs.read"
	if result {
		path = append(path, "result")
		operation = "jobs.result.read"
	}
	return requestJob(ctx, c, operation, http.MethodGet, id, path, nil)
}
func (Client) Retry(ctx context.Context, c application.RemoteConnection, id string) (domain.Result, error) {
	return requestJob(ctx, c, "jobs.retry", http.MethodPost, id, []string{"api", "jobs", id, "retry"}, nil)
}
func (Client) Delete(ctx context.Context, c application.RemoteConnection, id string) error {
	id, err := domain.RemoteIdentifier(id)
	if err != nil {
		return err
	}
	s, close, err := startOperation(ctx, c, "jobs.delete")
	if err != nil {
		return err
	}
	defer close()
	_, err = s.request(ctx, http.MethodDelete, []string{"api", "jobs", id}, nil, true)
	return err
}
func (Client) Rename(ctx context.Context, c application.RemoteConnection, id, from, to string) (domain.Result, error) {
	from, err := domain.RemoteIdentifier(from)
	if err != nil {
		return domain.Result{}, err
	}
	to, err = domain.RemoteIdentifier(to)
	if err != nil {
		return domain.Result{}, err
	}
	return requestJob(ctx, c, "transcript.speakers.rename", http.MethodPost, id, []string{"api", "jobs", id, "speakers", "rename"}, map[string]string{"fromSpeaker": from, "toSpeaker": to})
}
func requestJob(ctx context.Context, c application.RemoteConnection, operation, method, id string, path []string, body any) (domain.Result, error) {
	id, err := domain.RemoteIdentifier(id)
	if err != nil {
		return domain.Result{}, err
	}
	path[2] = id
	s, close, err := startOperation(ctx, c, operation)
	if err != nil {
		return domain.Result{}, err
	}
	defer close()
	data, err := s.request(ctx, method, path, body, false)
	if err != nil {
		return domain.Result{}, err
	}
	result, err := parseJob(data)
	if err != nil || result.Job.ID != id {
		return domain.Result{}, domain.ErrRemote
	}
	return result, nil
}
