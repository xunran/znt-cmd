package compressor

import (
	"context"
	"strings"
	"testing"

	"znt/internal/contracts"
	modelclient "znt/internal/model/client"
)

func TestLocalCompressorTruncatesWhenBudgetExceeded(t *testing.T) {
	result, err := (LocalCompressor{}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(24),
			Compression: contracts.ContextCompressionStrategy{
				Enabled:      true,
				Mode:         "truncate",
				TargetTokens: 24,
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: strings.Repeat("context ", 80),
		},
		HardTokenLimit: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Applied || result.Report.SummaryHash == "" {
		t.Fatalf("expected compression report, got %#v", result.Report)
	}
	if result.Report.PromptProfileID != "context.compression.factual_v1" {
		t.Fatalf("expected default prompt profile id in report, got %#v", result.Report)
	}
	if !strings.Contains(result.PromptBundle.Context, "context truncated by compression policy") {
		t.Fatalf("expected truncated context, got %s", result.PromptBundle.Context)
	}
}

func TestLocalCompressorSkipsWhenUnderTrigger(t *testing.T) {
	result, err := (LocalCompressor{}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(100),
			Compression: contracts.ContextCompressionStrategy{
				Enabled: true,
				Mode:    "truncate",
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: "short context",
		},
		HardTokenLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Applied || result.PromptBundle.Context != "short context" {
		t.Fatalf("expected compression to be skipped, got %#v", result)
	}
}

func TestLocalCompressorLLMThenTruncateFallsBack(t *testing.T) {
	result, err := (LocalCompressor{}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(20),
			Compression: contracts.ContextCompressionStrategy{
				Enabled: true,
				Mode:    "llm_then_truncate",
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: strings.Repeat("context ", 80),
		},
		HardTokenLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Applied || result.Report.FailureReason == "" {
		t.Fatalf("expected fallback compression report, got %#v", result.Report)
	}
}

func TestLocalCompressorLLMUsesModelResponseAndSourceRefs(t *testing.T) {
	result, err := (LocalCompressor{Model: modelclient.StubModelClient{Response: modelclient.ModelResponse{
		RawDecisionJSON: []byte(`{"summary":"ACME renewal is active.","source_refs":["crm://account/42"],"open_questions":["confirm owner"]}`),
		ModelProvider:   "openai-compatible",
		ModelName:       "compressor-model",
	}}}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(20),
			Compression: contracts.ContextCompressionStrategy{
				Enabled:         true,
				Mode:            "llm",
				TargetTokens:    20,
				PromptProfileID: "context.compression.factual_v1",
				ModelProvider:   "openai-compatible",
				ModelName:       "compressor-model",
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: `<external_context source_ref="crm://account/42" trust_level="untrusted_external_context">` + strings.Repeat(" context", 80),
		},
		HardTokenLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Applied || result.Report.ModelName != "compressor-model" || result.Report.SummaryHash == "" {
		t.Fatalf("expected llm compression report, got %#v", result.Report)
	}
	if len(result.Report.SourceRefs) != 1 || result.Report.SourceRefs[0] != "crm://account/42" {
		t.Fatalf("expected source refs in compression report, got %#v", result.Report.SourceRefs)
	}
	if !strings.Contains(result.PromptBundle.Context, "ACME renewal is active.") ||
		!strings.Contains(result.PromptBundle.Context, "source_refs=crm://account/42") ||
		!strings.Contains(result.PromptBundle.Context, "prompt_profile_id=context.compression.factual_v1") {
		t.Fatalf("expected compressed context metadata, got %s", result.PromptBundle.Context)
	}
}

func TestLocalCompressorIncludesPreserveAndForbidInModelPrompt(t *testing.T) {
	model := &capturingCompressionModel{
		response: modelclient.ModelResponse{
			RawDecisionJSON: []byte(`{"summary":"summary","source_refs":["tool://call/1"]}`),
		},
	}
	_, err := (LocalCompressor{Model: model}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(20),
			Compression: contracts.ContextCompressionStrategy{
				Enabled:       true,
				Mode:          "llm",
				ModelProvider: "openai-compatible",
				ModelBaseURL:  "https://compressor.example.test/v1",
				ModelName:     "compressor-model",
				InlinePrompt: &contracts.CompressionPromptProfile{
					ProfileID:    "inline",
					SystemPrompt: "compress",
					Preserve:     []string{"inline preserve"},
					Forbid:       []string{"inline forbid"},
				},
				Preserve: []string{"creator preserve tool result"},
				Forbid:   []string{"creator forbid new facts"},
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: `<external_context source_ref="tool://call/1">` + strings.Repeat(" context", 80),
		},
		HardTokenLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := model.lastRequest.PromptBundle.Task
	for _, expected := range []string{"inline preserve", "creator preserve tool result", "inline forbid", "creator forbid new facts"} {
		if !strings.Contains(task, expected) {
			t.Fatalf("expected compression task to include %q, got %s", expected, task)
		}
	}
	if model.lastRequest.ModelProvider != "openai-compatible" || model.lastRequest.ModelBaseURL != "https://compressor.example.test/v1" || model.lastRequest.ModelName != "compressor-model" {
		t.Fatalf("expected compression model routing in request, got %#v", model.lastRequest)
	}
}

func TestLocalCompressorLLMThenTruncateFallsBackOnInvalidJSON(t *testing.T) {
	result, err := (LocalCompressor{Model: modelclient.StubModelClient{Response: modelclient.ModelResponse{
		RawDecisionJSON: []byte(`{"type":"reply","reply":{"text":"not the compression schema"}}`),
	}}}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(20),
			Compression: contracts.ContextCompressionStrategy{
				Enabled: true,
				Mode:    "llm_then_truncate",
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: strings.Repeat("context ", 80),
		},
		HardTokenLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Applied || !strings.Contains(result.Report.FailureReason, "local truncate fallback applied") {
		t.Fatalf("expected invalid model compression to fallback, got %#v", result.Report)
	}
	if !strings.Contains(result.PromptBundle.Context, "context truncated by compression policy") {
		t.Fatalf("expected truncated context, got %s", result.PromptBundle.Context)
	}
}

func TestLocalCompressorLLMRejectsWhenConfigured(t *testing.T) {
	_, err := (LocalCompressor{}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(20),
			Compression: contracts.ContextCompressionStrategy{
				Enabled:     true,
				Mode:        "llm",
				FailureMode: "reject",
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: strings.Repeat("context ", 80),
		},
		HardTokenLimit: 20,
	})
	if err == nil {
		t.Fatal("expected llm compression failure to reject when configured")
	}
}

func TestLocalCompressorUsesMaxTokensWhenTargetIsUnset(t *testing.T) {
	result, err := (LocalCompressor{}).Compress(context.Background(), Request{
		Strategy: contracts.ContextStrategy{
			ContextTokenBudget: contracts.IntPtr(100),
			Compression: contracts.ContextCompressionStrategy{
				Enabled:   true,
				Mode:      "truncate",
				MaxTokens: 20,
			},
		},
		PromptBundle: contracts.PromptBundle{
			System:  "system",
			Task:    "task",
			Context: strings.Repeat("context ", 80),
		},
		HardTokenLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Applied || result.Report.OutputTokens > 24 {
		t.Fatalf("expected max_tokens to constrain compression output, got %#v", result.Report)
	}
}

type capturingCompressionModel struct {
	lastRequest modelclient.ModelRequest
	response    modelclient.ModelResponse
}

func (m *capturingCompressionModel) Complete(_ context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	m.lastRequest = request
	return m.response, nil
}

func (m *capturingCompressionModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	response, err := m.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{
			Type:        modelclient.ModelStreamCompleted,
			RawDecision: response.RawDecisionJSON,
			Usage:       response.Usage,
		}
	}()
	return ch, nil
}
