package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	var raw map[string]any
	if err := a.rpc(ctx, "tasks/get", map[string]any{"id": string(ref.ExternalTaskID)}, &raw); err != nil {
		return nil, err
	}
	return taskSummaryFromA2A(ref, raw), nil
}

func (a HTTPAdapter) GetParticipants(ctx context.Context, ref contracts.ExternalTaskRef) ([]contracts.ParticipantSummary, error) {
	card, err := a.agentCard(ctx)
	if err != nil {
		return nil, err
	}
	name := stringValue(card, "name")
	id := stringValue(card, "url")
	if id == "" {
		id = name
	}
	if id == "" {
		id = string(ref.Provider)
	}
	return []contracts.ParticipantSummary{{
		ID:   id,
		Type: "a2a_agent",
		Name: name,
	}}, nil
}

func (a HTTPAdapter) SendMessage(ctx context.Context, req contracts.SendExternalMessageRequest) error {
	return a.rpc(ctx, "message/send", map[string]any{
		"id":      string(req.Ref.ExternalTaskID),
		"taskId":  string(req.Ref.ExternalTaskID),
		"message": textMessage(req.Message),
	}, nil)
}

func (a HTTPAdapter) AttachArtifact(ctx context.Context, req contracts.AttachArtifactRequest) error {
	return a.rpc(ctx, "message/send", map[string]any{
		"id":     string(req.Ref.ExternalTaskID),
		"taskId": string(req.Ref.ExternalTaskID),
		"message": map[string]any{
			"role": "user",
			"parts": []map[string]any{{
				"kind": "data",
				"data": map[string]any{"artifact_ref": req.ArtifactRef},
			}},
		},
	}, nil)
}

func (a HTTPAdapter) CheckAccess(ctx context.Context, req contracts.CollaborationAccessRequest) (*contracts.AccessDecision, error) {
	if _, err := a.GetTask(ctx, req.Ref); err != nil {
		return nil, err
	}
	return &contracts.AccessDecision{Allowed: true, Reason: "a2a task is reachable"}, nil
}

func textMessage(text string) map[string]any {
	return map[string]any{
		"role": "user",
		"parts": []map[string]any{{
			"kind": "text",
			"text": text,
		}},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (a HTTPAdapter) rpc(ctx context.Context, method string, params any, out any) error {
	var response rpcResponse
	if err := a.do(ctx, http.MethodPost, "/"+strings.TrimLeft(method, "/"), rpcRequest{
		JSONRPC: "2.0",
		ID:      "a2a-" + strings.ReplaceAll(method, "/", "-"),
		Method:  method,
		Params:  params,
	}, &response); err != nil {
		return err
	}
	if response.Error != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, "a2a json-rpc error", map[string]any{
			"method":      method,
			"rpc_code":    response.Error.Code,
			"rpc_message": response.Error.Message,
			"rpc_data":    response.Error.Data,
		})
	}
	if out == nil {
		return nil
	}
	if len(response.Result) == 0 {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, "a2a json-rpc response result is empty", map[string]any{"method": method})
	}
	if err := json.Unmarshal(response.Result, out); err != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("decode a2a json-rpc result: %v", err), map[string]any{"method": method})
	}
	return nil
}

func (a HTTPAdapter) agentCard(ctx context.Context) (map[string]any, error) {
	var card map[string]any
	if err := a.do(ctx, http.MethodGet, "/.well-known/agent-card.json", nil, &card); err != nil {
		return nil, err
	}
	return card, nil
}

func (a HTTPAdapter) do(ctx context.Context, method string, path string, payload any, out any) error {
	if strings.TrimSpace(a.BaseURL) == "" {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, "a2a base_url is required", nil)
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("encode a2a request: %v", err), nil)
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
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("a2a bridge returned status %d", resp.StatusCode), map[string]any{
			"method": method,
			"path":   path,
			"body":   string(body),
		})
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return contracts.NewRuntimeError(contracts.CodeExternalBridgeError, fmt.Sprintf("decode a2a response: %v", err), map[string]any{"method": method, "path": path})
	}
	return nil
}

func taskSummaryFromA2A(ref contracts.ExternalTaskRef, raw map[string]any) *contracts.ExternalTaskSummary {
	if nested, ok := raw["task"].(map[string]any); ok {
		raw = nested
	}
	status := stringValue(raw, "status")
	if status == "" {
		if statusObject, ok := raw["status"].(map[string]any); ok {
			status = stringValue(statusObject, "state")
		}
	}
	if status == "" {
		status = "unknown"
	}
	metadata, _ := raw["metadata"].(map[string]any)
	title := firstNonEmpty(stringValue(raw, "title"), stringValue(raw, "name"), stringValue(metadata, "title"), stringValue(raw, "id"), string(ref.ExternalTaskID))
	summary := firstNonEmpty(stringValue(raw, "summary"), stringValue(metadata, "summary"), stringValue(raw, "description"))
	return &contracts.ExternalTaskSummary{
		Ref:     ref,
		Title:   title,
		Status:  status,
		Summary: summary,
	}
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
