package array

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"znt/internal/contracts"
)

type HTTPAdapter struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewHTTPAdapter(baseURL string, token string) HTTPAdapter {
	return HTTPAdapter{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (a HTTPAdapter) GetTask(ctx context.Context, ref contracts.ExternalTaskRef) (*contracts.ExternalTaskSummary, error) {
	var out contracts.ExternalTaskSummary
	if err := a.do(ctx, http.MethodGet, a.taskPath(ref, ""), nil, &out); err != nil {
		return nil, err
	}
	if out.Ref.Provider == "" {
		out.Ref = ref
	}
	return &out, nil
}

func (a HTTPAdapter) GetParticipants(ctx context.Context, ref contracts.ExternalTaskRef) ([]contracts.ParticipantSummary, error) {
	var out []contracts.ParticipantSummary
	if err := a.do(ctx, http.MethodGet, a.taskPath(ref, "/participants"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (a HTTPAdapter) SendMessage(ctx context.Context, req contracts.SendExternalMessageRequest) error {
	return a.do(ctx, http.MethodPost, a.taskPath(req.Ref, "/messages"), req, nil)
}

func (a HTTPAdapter) AttachArtifact(ctx context.Context, req contracts.AttachArtifactRequest) error {
	return a.do(ctx, http.MethodPost, a.taskPath(req.Ref, "/artifacts"), req, nil)
}

func (a HTTPAdapter) CheckAccess(ctx context.Context, req contracts.CollaborationAccessRequest) (*contracts.AccessDecision, error) {
	var out contracts.AccessDecision
	if err := a.do(ctx, http.MethodPost, "/access-check", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a HTTPAdapter) taskPath(ref contracts.ExternalTaskRef, suffix string) string {
	path := "/tasks/" + url.PathEscape(string(ref.ExternalTaskID)) + suffix
	if ref.Provider == "" {
		return path
	}
	return path + "?provider=" + url.QueryEscape(ref.Provider)
}

func (a HTTPAdapter) do(ctx context.Context, method string, path string, payload any, out any) error {
	if strings.TrimSpace(a.BaseURL) == "" {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, "external bridge base_url is required", nil)
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("encode external bridge request: %v", err), nil)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+path, body)
	if err != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, err.Error(), nil)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, err.Error(), map[string]any{"method": method, "path": path})
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("external bridge returned status %d", resp.StatusCode), map[string]any{
			"method": method,
			"path":   path,
			"body":   string(body),
		})
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("decode external bridge response: %v", err), map[string]any{"method": method, "path": path})
	}
	return nil
}
