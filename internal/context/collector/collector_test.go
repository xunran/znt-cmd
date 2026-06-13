package collector

import (
	"context"
	"testing"
	"time"

	"znt/internal/asset/artifact"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	toolrepo "znt/internal/tool/repository"
)

func TestCollectorCollectsEnabledSourcesWithStrategyLimits(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	taskEvents := taskrepo.NewInMemoryStore()
	tasks := taskruntime.NewService(taskEvents, taskEvents)
	if err := taskEvents.Append(ctx, contracts.TaskEvent{
		EventID:   "event_1",
		TaskID:    "task_1",
		TenantID:  "tenant_1",
		Type:      "conversation.input",
		Payload:   map[string]any{"input": "old"},
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	memory := artifact.NewInMemoryMemoryStore(nil)
	for _, event := range []contracts.MemoryEvent{
		{MemoryID: "memory_user_old", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "user", Content: "old user", Summary: "old user", CreatedAt: now},
		{MemoryID: "memory_agent", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "agent", Content: "agent", Summary: "agent", CreatedAt: now.Add(time.Minute)},
		{MemoryID: "memory_user_new", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "user", Content: "new user", Summary: "new user", CreatedAt: now.Add(2 * time.Minute)},
	} {
		if _, err := memory.WriteMemory(ctx, event, "test", "test", "trace_1"); err != nil {
			t.Fatal(err)
		}
	}

	tools := toolrepo.NewInMemoryRepository()
	for i, callID := range []contracts.ToolCallID{"call_1", "call_2"} {
		if _, _, err := tools.SaveCall(ctx, contracts.ToolCall{ToolCallID: callID, TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", ToolID: "echo"}); err != nil {
			t.Fatal(err)
		}
		if err := tools.SaveResult(ctx, contracts.ToolResult{
			ToolResultID: contracts.ToolResultID("result_" + string(callID)),
			ToolCallID:   callID,
			Status:       contracts.ToolResultSucceeded,
			ArtifactRefs: []contracts.ArtifactRef{{ArtifactID: contracts.ArtifactID("artifact_" + string(callID)), Type: "text"}},
			CompletedAt:  now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (Collector{Tasks: tasks, Memory: memory, ToolRepo: tools}).Collect(ctx, Input{
		TenantID: "tenant_1",
		TaskID:   "task_1",
		RunID:    "run_1",
		AgentID:  "agent_1",
		UserID:   "user_1",
		ContextStrategy: contracts.ContextStrategy{
			EnabledSources:      []string{SourceMemorySummary, SourceToolResults, SourceArtifactRefs},
			MemoryMaxItems:      contracts.IntPtr(1),
			ToolResultMaxItems:  contracts.IntPtr(1),
			ArtifactRefMaxItems: contracts.IntPtr(1),
			TaskHistoryMaxItems: contracts.IntPtr(1),
		},
		MemoryStrategy: contracts.MemoryUseStrategy{
			ReadEnabled:    contracts.BoolPtr(true),
			ReadScopes:     []string{"user"},
			MaxMemoryItems: contracts.IntPtr(2),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TaskEvents) != 0 || len(result.TaskHistory) != 0 {
		t.Fatalf("expected disabled task sources to be skipped, got events=%#v history=%#v", result.TaskEvents, result.TaskHistory)
	}
	if len(result.Memory) != 1 || result.Memory[0].MemoryID != "memory_user_new" {
		t.Fatalf("expected latest scoped memory, got %#v", result.Memory)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].ToolCallID != "call_2" {
		t.Fatalf("expected latest tool summary, got %#v", result.ToolResults)
	}
	if len(result.ArtifactRefs) != 1 || result.ArtifactRefs[0].ArtifactID != "artifact_call_2" {
		t.Fatalf("expected latest artifact ref, got %#v", result.ArtifactRefs)
	}
}

func TestFilterRuntimeHookContextBlocksReportsPluginMetadataAndBudget(t *testing.T) {
	blocks := []runtimehook.ContextBlock{
		{
			ID:      "ctx_1",
			Title:   "crm",
			Content: "ok",
			Metadata: map[string]any{
				"source_type": SourceAgentPluginContext,
				"source_ref":  "crm://account/42",
				"provider_id": "crm-plugin",
				"hook_id":     "crm-context",
			},
		},
		{
			ID:      "ctx_2",
			Title:   "crm",
			Content: "too much",
			Metadata: map[string]any{
				"source_type": SourceAgentPluginContext,
				"source_ref":  "crm://account/42",
				"provider_id": "crm-plugin",
				"hook_id":     "crm-context",
			},
		},
	}

	selected, reports := FilterRuntimeHookContextBlocks(blocks, contracts.ContextStrategy{
		EnabledSources: []string{SourceAgentPluginContext},
		SourceBudgets:  map[string]int{SourceAgentPluginContext: 3},
	})
	if len(selected) != 1 || selected[0].ID != "ctx_1" {
		t.Fatalf("expected first plugin context block within budget, got %#v", selected)
	}
	if len(reports) != 1 {
		t.Fatalf("expected one source report, got %#v", reports)
	}
	report := reports[0]
	if report.SourceType != SourceAgentPluginContext || report.SourceRef != "crm://account/42" || report.ProviderID != "crm-plugin" || report.HookID != "crm-context" {
		t.Fatalf("expected plugin source metadata, got %#v", report)
	}
	if report.TrustLevel != "untrusted_external_context" || report.CandidateCount != 2 || report.SelectedCount != 1 || report.DroppedCount != 1 || report.Reason != "source_budget_exceeded" {
		t.Fatalf("expected budgeted plugin source report, got %#v", report)
	}
}
