package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
)

type ModelClient interface {
	Complete(ctx context.Context, request ModelRequest) (ModelResponse, error)
	Stream(ctx context.Context, request ModelRequest) (<-chan ModelStreamEvent, error)
}

type ModelCapabilityProvider interface {
	Capabilities() ModelCapabilityDescriptor
}

type ModelCapabilityDescriptor struct {
	Provider                 string `json:"provider"`
	Model                    string `json:"model"`
	APIStyle                 string `json:"api_style,omitempty"`
	StructuredOutput         bool   `json:"structured_output"`
	Streaming                bool   `json:"streaming"`
	OpenAICompatible         bool   `json:"openai_compatible,omitempty"`
	Thinking                 string `json:"thinking,omitempty"`
	ReasoningEffort          string `json:"reasoning_effort,omitempty"`
	MaxOutputTokens          int    `json:"max_output_tokens,omitempty"`
	SupportsJSONResponseMode bool   `json:"supports_json_response_mode,omitempty"`
}

type ModelRequest struct {
	RunID          contracts.AgentRunID   `json:"run_id"`
	PromptBundle   contracts.PromptBundle `json:"prompt_bundle"`
	OutputContract string                 `json:"output_contract,omitempty"`
	Timeout        time.Duration          `json:"timeout,omitempty"`
}

type ModelResponse struct {
	RawDecisionJSON []byte     `json:"raw_decision_json"`
	ModelProvider   string     `json:"model_provider,omitempty"`
	ModelName       string     `json:"model_name,omitempty"`
	Usage           ModelUsage `json:"usage,omitempty"`
}

type ModelStreamEvent struct {
	Type          string     `json:"type"`
	Delta         string     `json:"delta,omitempty"`
	RawDecision   []byte     `json:"raw_decision_json,omitempty"`
	ModelProvider string     `json:"model_provider,omitempty"`
	ModelName     string     `json:"model_name,omitempty"`
	Usage         ModelUsage `json:"usage,omitempty"`
	Err           error      `json:"-"`
}

type ModelUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

const (
	ModelStreamDelta     = "delta"
	ModelStreamCompleted = "completed"
	ModelStreamError     = "error"
)

type ModelErrorKind string

const (
	ModelErrorInvalidConfig       ModelErrorKind = "invalid_config"
	ModelErrorInvalidRequest      ModelErrorKind = "invalid_request"
	ModelErrorAuthentication      ModelErrorKind = "authentication"
	ModelErrorRateLimited         ModelErrorKind = "rate_limited"
	ModelErrorContextTooLarge     ModelErrorKind = "context_too_large"
	ModelErrorProviderUnavailable ModelErrorKind = "provider_unavailable"
	ModelErrorProviderResponse    ModelErrorKind = "invalid_provider_response"
	ModelErrorNetwork             ModelErrorKind = "network"
	ModelErrorTimeout             ModelErrorKind = "timeout"
)

type StubModelClient struct {
	Response ModelResponse
	Err      error
}

func (c StubModelClient) Capabilities() ModelCapabilityDescriptor {
	model := c.Response.ModelName
	if model == "" {
		model = "stub-decision"
	}
	provider := c.Response.ModelProvider
	if provider == "" {
		provider = "stub"
	}
	return ModelCapabilityDescriptor{
		Provider:                 provider,
		Model:                    model,
		APIStyle:                 "stub",
		StructuredOutput:         true,
		Streaming:                true,
		SupportsJSONResponseMode: true,
	}
}

func (c StubModelClient) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	select {
	case <-ctx.Done():
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelTimeout, ModelErrorTimeout, ctx.Err().Error(), true, nil)
	default:
	}
	if c.Err != nil {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorProviderUnavailable, c.Err.Error(), true, nil)
	}
	if len(c.Response.RawDecisionJSON) == 0 {
		return ModelResponse{
			RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"ok"}}`),
			ModelProvider:   "stub",
			ModelName:       "stub-decision",
		}, nil
	}
	return c.Response, nil
}

func (c StubModelClient) Stream(ctx context.Context, request ModelRequest) (<-chan ModelStreamEvent, error) {
	resp, err := c.Complete(ctx, request)
	ch := make(chan ModelStreamEvent, 2)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: err}
			return
		}
		if len(resp.RawDecisionJSON) > 0 {
			ch <- ModelStreamEvent{
				Type:          ModelStreamDelta,
				Delta:         string(resp.RawDecisionJSON),
				ModelProvider: resp.ModelProvider,
				ModelName:     resp.ModelName,
			}
		}
		ch <- ModelStreamEvent{
			Type:          ModelStreamCompleted,
			RawDecision:   resp.RawDecisionJSON,
			ModelProvider: resp.ModelProvider,
			ModelName:     resp.ModelName,
			Usage:         resp.Usage,
		}
	}()
	return ch, nil
}

type ScriptedModelClient struct {
	mu        sync.Mutex
	Responses []ModelResponse
	Calls     int
}

func (c *ScriptedModelClient) Capabilities() ModelCapabilityDescriptor {
	provider := "scripted"
	model := "scripted-decision"
	if len(c.Responses) > 0 {
		if c.Responses[0].ModelProvider != "" {
			provider = c.Responses[0].ModelProvider
		}
		if c.Responses[0].ModelName != "" {
			model = c.Responses[0].ModelName
		}
	}
	return ModelCapabilityDescriptor{
		Provider:                 provider,
		Model:                    model,
		APIStyle:                 "scripted",
		StructuredOutput:         true,
		Streaming:                true,
		SupportsJSONResponseMode: true,
	}
}

func (c *ScriptedModelClient) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
		select {
		case <-ctx.Done():
			return ModelResponse{}, modelRuntimeError(contracts.CodeModelTimeout, ModelErrorTimeout, ctx.Err().Error(), true, nil)
		default:
		}
	}
	if len(c.Responses) == 0 {
		return (StubModelClient{}).Complete(ctx, request)
	}
	index := c.Calls
	if index >= len(c.Responses) {
		index = len(c.Responses) - 1
	}
	c.Calls++
	return c.Responses[index], nil
}

func (c *ScriptedModelClient) Stream(ctx context.Context, request ModelRequest) (<-chan ModelStreamEvent, error) {
	resp, err := c.Complete(ctx, request)
	ch := make(chan ModelStreamEvent, 2)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: err}
			return
		}
		if len(resp.RawDecisionJSON) > 0 {
			ch <- ModelStreamEvent{
				Type:          ModelStreamDelta,
				Delta:         string(resp.RawDecisionJSON),
				ModelProvider: resp.ModelProvider,
				ModelName:     resp.ModelName,
			}
		}
		ch <- ModelStreamEvent{
			Type:          ModelStreamCompleted,
			RawDecision:   resp.RawDecisionJSON,
			ModelProvider: resp.ModelProvider,
			ModelName:     resp.ModelName,
			Usage:         resp.Usage,
		}
	}()
	return ch, nil
}

type OpenAICompatibleClient struct {
	BaseURL         string
	APIKey          string
	Model           string
	MaxTokens       int
	Temperature     *float64
	Thinking        string
	ReasoningEffort string
	HTTPClient      *http.Client
}

func (c OpenAICompatibleClient) Capabilities() ModelCapabilityDescriptor {
	return ModelCapabilityDescriptor{
		Provider:                 "openai-compatible",
		Model:                    c.Model,
		APIStyle:                 "chat_completions",
		StructuredOutput:         true,
		Streaming:                true,
		OpenAICompatible:         true,
		Thinking:                 strings.TrimSpace(c.Thinking),
		ReasoningEffort:          strings.TrimSpace(c.ReasoningEffort),
		MaxOutputTokens:          c.MaxTokens,
		SupportsJSONResponseMode: true,
	}
}

func (c OpenAICompatibleClient) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Model) == "" {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidConfig, "openai-compatible client requires base_url and model", false, nil)
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	payload := c.chatCompletionPayload(request, false)
	body, err := json.Marshal(payload)
	if err != nil {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidRequest, err.Error(), false, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidConfig, err.Error(), false, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ModelResponse{}, modelRuntimeError(contracts.CodeModelTimeout, ModelErrorTimeout, ctx.Err().Error(), true, nil)
		}
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorNetwork, err.Error(), true, nil)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, err.Error(), true, nil)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ModelResponse{}, classifyHTTPStatus(resp.StatusCode, data)
	}
	var decoded chatCompletionResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, fmt.Sprintf("parse model response: %v", err), false, nil)
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return ModelResponse{}, modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, "model response did not contain a decision", false, nil)
	}
	return ModelResponse{
		RawDecisionJSON: []byte(decoded.Choices[0].Message.Content),
		ModelProvider:   "openai-compatible",
		ModelName:       decoded.Model,
		Usage: ModelUsage{
			PromptTokens:     decoded.Usage.PromptTokens,
			CompletionTokens: decoded.Usage.CompletionTokens,
		},
	}, nil
}

func (c OpenAICompatibleClient) Stream(ctx context.Context, request ModelRequest) (<-chan ModelStreamEvent, error) {
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Model) == "" {
		return nil, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidConfig, "openai-compatible client requires base_url and model", false, nil)
	}
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	payload := c.chatCompletionPayload(request, true)
	body, err := json.Marshal(payload)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidRequest, err.Error(), false, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatCompletionsURL(), bytes.NewReader(body))
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, modelRuntimeError(contracts.CodeModelError, ModelErrorInvalidConfig, err.Error(), false, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ch := make(chan ModelStreamEvent, 8)
	go func() {
		defer close(ch)
		if cancel != nil {
			defer cancel()
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelTimeout, ModelErrorTimeout, ctx.Err().Error(), true, nil)}
				return
			}
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorNetwork, err.Error(), true, nil)}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			if readErr != nil {
				ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, readErr.Error(), true, nil)}
				return
			}
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: classifyHTTPStatus(resp.StatusCode, data)}
			return
		}
		readOpenAICompatibleStream(resp.Body, c.Model, ch)
	}()
	return ch, nil
}

func (c OpenAICompatibleClient) chatCompletionPayload(request ModelRequest, stream bool) chatCompletionRequest {
	payload := chatCompletionRequest{
		Model:          c.Model,
		Messages:       promptBundleMessages(request.PromptBundle, request.OutputContract),
		ResponseFormat: map[string]string{"type": "json_object"},
		Stream:         stream,
	}
	if stream {
		payload.StreamOptions = &chatCompletionStreamOptions{IncludeUsage: true}
	}
	if c.MaxTokens > 0 {
		payload.MaxTokens = c.MaxTokens
	}
	if c.Temperature != nil {
		payload.Temperature = c.Temperature
	}
	if strings.TrimSpace(c.Thinking) != "" {
		payload.Thinking = map[string]string{"type": strings.TrimSpace(c.Thinking)}
	}
	if strings.TrimSpace(c.ReasoningEffort) != "" {
		payload.ReasoningEffort = strings.TrimSpace(c.ReasoningEffort)
	}
	return payload
}

func (c OpenAICompatibleClient) chatCompletionsURL() string {
	base := strings.TrimRight(c.BaseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func classifyHTTPStatus(statusCode int, body []byte) *contracts.RuntimeError {
	kind := ModelErrorInvalidRequest
	code := contracts.CodeModelError
	retryable := false
	switch {
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusGatewayTimeout:
		kind = ModelErrorTimeout
		code = contracts.CodeModelTimeout
		retryable = true
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		kind = ModelErrorAuthentication
	case statusCode == http.StatusTooManyRequests:
		kind = ModelErrorRateLimited
		retryable = true
	case statusCode == http.StatusRequestEntityTooLarge:
		kind = ModelErrorContextTooLarge
	case statusCode >= 500:
		kind = ModelErrorProviderUnavailable
		retryable = true
	}
	return modelRuntimeError(code, kind, fmt.Sprintf("model provider returned status %d", statusCode), retryable, map[string]any{
		"status": statusCode,
		"body":   trimProviderBody(string(body)),
	})
}

func modelRuntimeError(code contracts.ErrorCode, kind ModelErrorKind, message string, retryable bool, details map[string]any) *contracts.RuntimeError {
	if details == nil {
		details = map[string]any{}
	}
	details["kind"] = string(kind)
	err := contracts.NewRuntimeError(code, message, details)
	err.Retryable = retryable
	return err
}

func trimProviderBody(body string) string {
	const limit = 1024
	body = strings.TrimSpace(body)
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "...[truncated]"
}

type chatCompletionRequest struct {
	Model           string                       `json:"model"`
	Messages        []chatMessage                `json:"messages"`
	ResponseFormat  map[string]string            `json:"response_format,omitempty"`
	MaxTokens       int                          `json:"max_tokens,omitempty"`
	Temperature     *float64                     `json:"temperature,omitempty"`
	Thinking        map[string]string            `json:"thinking,omitempty"`
	ReasoningEffort string                       `json:"reasoning_effort,omitempty"`
	Stream          bool                         `json:"stream,omitempty"`
	StreamOptions   *chatCompletionStreamOptions `json:"stream_options,omitempty"`
}

type chatCompletionStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type chatCompletionStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *chatCompletionStreamError `json:"error,omitempty"`
}

type chatCompletionStreamError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func readOpenAICompatibleStream(body io.Reader, fallbackModel string, ch chan<- ModelStreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var raw strings.Builder
	modelName := fallbackModel
	usage := ModelUsage{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk chatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, fmt.Sprintf("parse model stream chunk: %v", err), false, nil)}
			return
		}
		if chunk.Error != nil {
			message := strings.TrimSpace(chunk.Error.Message)
			if message == "" {
				message = "model stream returned provider error"
			}
			ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, message, false, map[string]any{"provider_error_type": chunk.Error.Type, "provider_error_code": chunk.Error.Code})}
			return
		}
		if strings.TrimSpace(chunk.Model) != "" {
			modelName = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			raw.WriteString(choice.Delta.Content)
			ch <- ModelStreamEvent{
				Type:          ModelStreamDelta,
				Delta:         choice.Delta.Content,
				ModelProvider: "openai-compatible",
				ModelName:     modelName,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, err.Error(), true, nil)}
		return
	}
	if raw.Len() == 0 {
		ch <- ModelStreamEvent{Type: ModelStreamError, Err: modelRuntimeError(contracts.CodeModelError, ModelErrorProviderResponse, "model stream completed without a decision", false, nil)}
		return
	}
	ch <- ModelStreamEvent{
		Type:          ModelStreamCompleted,
		RawDecision:   []byte(raw.String()),
		ModelProvider: "openai-compatible",
		ModelName:     modelName,
		Usage:         usage,
	}
}

func promptBundleMessages(bundle contracts.PromptBundle, outputContract string) []chatMessage {
	if strings.TrimSpace(outputContract) == "" {
		outputContract = renderDecisionContract(bundle)
	}
	developer := joinNonEmpty(
		bundle.Developer,
		outputContract,
		renderDecisionConstraints(bundle.Constraints),
	)
	user := joinNonEmpty(bundle.Task, bundle.Context)
	messages := make([]chatMessage, 0, 3)
	if strings.TrimSpace(bundle.System) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: bundle.System})
	}
	if strings.TrimSpace(developer) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: developer})
	}
	if strings.TrimSpace(user) != "" {
		messages = append(messages, chatMessage{Role: "user", Content: user})
	}
	return messages
}

func renderDecisionContract(bundle contracts.PromptBundle) string {
	parts := []string{
		"<decision output contract>",
		"Return exactly one valid json object. Do not return Markdown, comments, code fences, or explanatory text outside the JSON object.",
		"Only use these decision types: reply, no_op, ask_clarification, tool_call, unsupported, error.",
		"Allowed Decision JSON examples:",
		`Reply: {"type":"reply","reason":"capability_question","confidence":0.8,"reply":{"kind":"answer","text":"..."}}`,
		`Ask clarification: {"type":"ask_clarification","reason":"missing_required_info","confidence":0.8,"ask":{"question":"...","required_fields":["..."]}}`,
		`Tool call: {"type":"tool_call","reason":"need_registered_tool","confidence":0.8,"tool_calls":[{"tool_id":"echo","arguments":{}}]}`,
		`Unsupported: {"type":"unsupported","reason":"capability_not_available","confidence":0.8}`,
		`No-op: {"type":"no_op","reason":"not_addressed_to_agent","confidence":0.8}`,
		`Error: {"type":"error","reason":"internal_error","confidence":0.1,"error":{"code":"model_error","message":"..."}}`,
		"Only call tools listed in the retrieved tool cards or tool definitions. Do not invent tool_id, agent_id, skill_id, memory, artifact, or context.",
	}
	if len(bundle.OutputSchema) > 0 {
		parts = append(parts, "Output JSON schema:", renderJSON(bundle.OutputSchema))
	}
	if len(bundle.ToolCards) > 0 {
		parts = append(parts, "Retrieved tool cards:", renderJSON(bundle.ToolCards))
	}
	if len(bundle.ToolDefinitions) > 0 {
		parts = append(parts, "Retrieved tool definitions:", renderJSON(bundle.ToolDefinitions))
	}
	parts = append(parts, "</decision output contract>")
	return strings.Join(parts, "\n")
}

func renderDecisionConstraints(constraints []string) string {
	if len(constraints) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<decision constraints>\n")
	for _, constraint := range constraints {
		constraint = strings.TrimSpace(constraint)
		if constraint == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(html.EscapeString(constraint))
		b.WriteByte('\n')
	}
	b.WriteString("</decision constraints>")
	return b.String()
}

func joinNonEmpty(values ...string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, "\n\n")
}

func renderJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
