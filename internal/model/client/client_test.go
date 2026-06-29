package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestStubModelClientDefaultDecision(t *testing.T) {
	resp, err := (StubModelClient{}).Complete(context.Background(), ModelRequest{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.RawDecisionJSON) == 0 || resp.ModelProvider != "stub" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestStubModelClientStream(t *testing.T) {
	events, err := (StubModelClient{}).Stream(context.Background(), ModelRequest{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	seenDelta := false
	seenCompleted := false
	for event := range events {
		switch event.Type {
		case ModelStreamDelta:
			seenDelta = true
			if event.Delta == "" {
				t.Fatal("expected delta content")
			}
		case ModelStreamCompleted:
			seenCompleted = true
			if len(event.RawDecision) == 0 {
				t.Fatal("expected completed decision")
			}
		}
	}
	if !seenDelta || !seenCompleted {
		t.Fatalf("expected delta and completed events, got delta=%v completed=%v", seenDelta, seenCompleted)
	}
}

func TestOpenAICompatibleClientComplete(t *testing.T) {
	var received chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
			"usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3},
		})
	}))
	defer server.Close()

	resp, err := OpenAICompatibleClient{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"}.Complete(context.Background(), ModelRequest{
		PromptBundle: contracts.PromptBundle{
			System:       "system",
			Developer:    "developer",
			Task:         "task",
			Context:      "context",
			OutputSchema: map[string]any{"type": "object"},
			Constraints:  []string{"repair attempt 1: return valid decision json"},
			ToolCards: []contracts.ToolCard{{
				ToolID:      "echo",
				Name:        "echo",
				Description: "Returns input.",
				RiskLevel:   contracts.RiskLow,
				Visibility:  contracts.ToolExposed,
				Version:     "v1",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ModelProvider != "openai-compatible" || resp.ModelName != "test-model" || resp.Usage.PromptTokens != 2 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if received.ResponseFormat["type"] != "json_object" {
		t.Fatalf("expected json_object response format, got %#v", received.ResponseFormat)
	}
	combined := requestMessagesText(received.Messages)
	for _, expected := range []string{
		"Return exactly one valid json object",
		`"type":"reply"`,
		"Output JSON schema",
		"repair attempt 1",
		"Retrieved tool cards",
		"task",
		"context",
	} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("expected rendered prompt to contain %q, got %s", expected, combined)
		}
	}
}

func TestOpenAICompatibleClientUsesRequestBaseURL(t *testing.T) {
	fallbackHits := make(chan struct{}, 1)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case fallbackHits <- struct{}{}:
		default:
		}
		http.Error(w, "fallback should not be used", http.StatusInternalServerError)
	}))
	defer fallback.Close()
	overrideHits := make(chan string, 1)
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case overrideHits <- r.URL.Path:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "override-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
		})
	}))
	defer override.Close()

	resp, err := OpenAICompatibleClient{BaseURL: fallback.URL, Model: "default-model"}.Complete(context.Background(), ModelRequest{
		ModelBaseURL: override.URL + "/chat/completions",
		ModelName:    "override-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	fallbackCalled := false
	select {
	case <-fallbackHits:
		fallbackCalled = true
	default:
	}
	overridePath := ""
	select {
	case overridePath = <-overrideHits:
	default:
	}
	overrideCalled := overridePath != ""
	if fallbackCalled || !overrideCalled {
		t.Fatalf("expected request base URL override, fallback_called=%v override_called=%v", fallbackCalled, overrideCalled)
	}
	if overridePath != "/chat/completions" {
		t.Fatalf("unexpected override path %s", overridePath)
	}
	if resp.ModelName != "override-model" {
		t.Fatalf("expected override model response, got %#v", resp)
	}
}

func TestOpenAICompatibleClientStreamSSE(t *testing.T) {
	var received chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE := func(value any) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
				t.Fatal(err)
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		writeSSE(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"delta": map[string]any{"content": `{"type":"reply",`}},
			},
		})
		writeSSE(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"delta": map[string]any{"content": `"reply":{"kind":"answer","text":"ok"}}`}},
			},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 7},
		})
		if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	events, err := OpenAICompatibleClient{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"}.Stream(context.Background(), ModelRequest{
		PromptBundle: contracts.PromptBundle{System: "system", Task: "task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var completed ModelStreamEvent
	for event := range events {
		switch event.Type {
		case ModelStreamDelta:
			deltas = append(deltas, event.Delta)
			if event.ModelProvider != "openai-compatible" || event.ModelName != "test-model" {
				t.Fatalf("unexpected delta metadata: %#v", event)
			}
		case ModelStreamCompleted:
			completed = event
		case ModelStreamError:
			t.Fatalf("unexpected stream error: %v", event.Err)
		default:
			t.Fatalf("unexpected event: %#v", event)
		}
	}
	decision := `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`
	if strings.Join(deltas, "") != decision {
		t.Fatalf("unexpected deltas: %q", strings.Join(deltas, ""))
	}
	if string(completed.RawDecision) != decision || completed.Usage.PromptTokens != 5 || completed.Usage.CompletionTokens != 7 {
		t.Fatalf("unexpected completed event: %#v", completed)
	}
	if !received.Stream || received.StreamOptions == nil || !received.StreamOptions.IncludeUsage {
		t.Fatalf("expected streaming request with usage, got %#v", received)
	}
	if received.ResponseFormat["type"] != "json_object" {
		t.Fatalf("expected json_object response format, got %#v", received.ResponseFormat)
	}
}

func TestOpenAICompatibleClientStreamClassifiesProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	events, err := OpenAICompatibleClient{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"}.Stream(context.Background(), ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var runtimeErr *contracts.RuntimeError
	for event := range events {
		if event.Type != ModelStreamError {
			t.Fatalf("expected only error event, got %#v", event)
		}
		if !errors.As(event.Err, &runtimeErr) {
			t.Fatalf("expected runtime error, got %T", event.Err)
		}
	}
	if runtimeErr == nil {
		t.Fatal("expected runtime error")
	}
	if runtimeErr.Code != contracts.CodeModelError || !runtimeErr.Retryable || runtimeErr.Details["kind"] != string(ModelErrorRateLimited) {
		t.Fatalf("unexpected runtime error: %#v", runtimeErr)
	}
}

func TestOpenAICompatibleClientClassifiesProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{BaseURL: server.URL, APIKey: "bad-key", Model: "test-model"}.Complete(context.Background(), ModelRequest{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Retryable {
		t.Fatalf("expected auth error to be non-retryable: %#v", runtimeErr)
	}
	if runtimeErr.Details["kind"] != string(ModelErrorAuthentication) || runtimeErr.Details["status"] != http.StatusUnauthorized {
		t.Fatalf("unexpected classification: %#v", runtimeErr.Details)
	}
}

func TestOpenAICompatibleClientSendsRequestOptions(t *testing.T) {
	temperature := 0.1
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
		})
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{
		BaseURL:         server.URL,
		Model:           "test-model",
		MaxTokens:       1024,
		Temperature:     &temperature,
		Thinking:        "enabled",
		ReasoningEffort: "low",
	}.Complete(context.Background(), ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, expected := range []string{
		`"max_tokens":1024`,
		`"temperature":0.1`,
		`"thinking":{"type":"enabled"}`,
		`"reasoning_effort":"low"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected request body to contain %s, got %s", expected, body)
		}
	}
}

func TestOpenAICompatibleClientRequestOverridesModelOptions(t *testing.T) {
	clientTemperature := 0.7
	requestTemperature := 0.2
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "agent-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
		})
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{
		BaseURL:         server.URL,
		Model:           "default-model",
		MaxTokens:       1024,
		Temperature:     &clientTemperature,
		Thinking:        "enabled",
		ReasoningEffort: "medium",
	}.Complete(context.Background(), ModelRequest{
		ModelName:       "agent-model",
		MaxOutputTokens: 256,
		Temperature:     &requestTemperature,
		Thinking:        "disabled",
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, expected := range []string{
		`"model":"agent-model"`,
		`"max_tokens":256`,
		`"temperature":0.2`,
		`"thinking":{"type":"disabled"}`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected request body to contain %s, got %s", expected, body)
		}
	}
	if strings.Contains(body, `"reasoning_effort"`) {
		t.Fatalf("expected reasoning_effort to be omitted when thinking is disabled, got %s", body)
	}
}

func TestOpenAICompatibleClientDefaultsDeepSeekThinkingDisabled(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "deepseek-v4-flash",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
		})
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{
		BaseURL: server.URL + "/deepseek",
		Model:   "deepseek-v4-flash",
	}.Complete(context.Background(), ModelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"thinking":{"type":"disabled"}`) {
		t.Fatalf("expected DeepSeek request to disable thinking by default, got %s", body)
	}
	if strings.Contains(body, `"reasoning_effort"`) {
		t.Fatalf("expected reasoning_effort to be omitted when DeepSeek thinking is disabled by default, got %s", body)
	}
}

func TestOpenAICompatibleClientGlobalThinkingDisabledOverridesRequest(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"type":"reply","reply":{"kind":"answer","text":"ok"}}`}},
			},
		})
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{
		BaseURL:         server.URL,
		Model:           "test-model",
		Thinking:        "disabled",
		ReasoningEffort: "max",
	}.Complete(context.Background(), ModelRequest{
		Thinking:        "enabled",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"thinking":{"type":"disabled"}`) {
		t.Fatalf("expected global disabled thinking to override request thinking, got %s", body)
	}
	if strings.Contains(body, `"reasoning_effort"`) {
		t.Fatalf("expected reasoning_effort to be omitted when global thinking is disabled, got %s", body)
	}
}

func TestOpenAICompatibleClientUsesCustomOutputContract(t *testing.T) {
	var received chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{"message": map[string]any{"content": `{"addressed_to_agent":true,"confidence":0.9}`}},
			},
		})
	}))
	defer server.Close()

	_, err := OpenAICompatibleClient{BaseURL: server.URL, Model: "test-model"}.Complete(context.Background(), ModelRequest{
		PromptBundle:   contracts.PromptBundle{System: "system", Developer: "developer", Task: "task"},
		OutputContract: "<judge contract>Return addressing json only.</judge contract>",
	})
	if err != nil {
		t.Fatal(err)
	}
	combined := requestMessagesText(received.Messages)
	if !strings.Contains(combined, "Return addressing json only") {
		t.Fatalf("expected custom contract in request, got %s", combined)
	}
	if strings.Contains(combined, "Only use these decision types") {
		t.Fatalf("did not expect main decision contract when custom contract is set: %s", combined)
	}
}

func requestMessagesText(messages []chatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}
