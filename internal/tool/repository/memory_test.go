package repository

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestToolRepositoryIdempotency(t *testing.T) {
	repo := NewInMemoryRepository()
	call := contracts.ToolCall{
		ToolCallID:     "call_1",
		ToolID:         "echo",
		TaskID:         "task_1",
		IdempotencyKey: "same",
	}
	_, duplicate, err := repo.SaveCall(context.Background(), call)
	if err != nil || duplicate {
		t.Fatalf("unexpected first save: duplicate=%v err=%v", duplicate, err)
	}
	again := call
	again.ToolCallID = "call_2"
	existing, duplicate, err := repo.SaveCall(context.Background(), again)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate || existing.ToolCallID != call.ToolCallID {
		t.Fatalf("expected duplicate existing call, got %#v duplicate=%v", existing, duplicate)
	}
}

func TestToolRepositoryEmptyIdempotencyKeyDoesNotDeduplicate(t *testing.T) {
	repo := NewInMemoryRepository()
	first := contracts.ToolCall{ToolCallID: "call_1", TenantID: "tenant_1", ToolID: "echo"}
	second := contracts.ToolCall{ToolCallID: "call_2", TenantID: "tenant_1", ToolID: "echo"}
	if _, duplicate, err := repo.SaveCall(context.Background(), first); err != nil || duplicate {
		t.Fatalf("unexpected first save: duplicate=%v err=%v", duplicate, err)
	}
	if _, duplicate, err := repo.SaveCall(context.Background(), second); err != nil || duplicate {
		t.Fatalf("expected empty idempotency key to skip dedupe, duplicate=%v err=%v", duplicate, err)
	}
}

func TestToolRepositoryListResultsByRunLimit(t *testing.T) {
	repo := NewInMemoryRepository()
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	for i, callID := range []contracts.ToolCallID{"call_1", "call_2", "call_3"} {
		if _, _, err := repo.SaveCall(context.Background(), contracts.ToolCall{ToolCallID: callID, TenantID: "tenant_1", ToolID: "echo", RunID: "run_1"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveResult(context.Background(), contracts.ToolResult{
			ToolResultID: contracts.ToolResultID("result_" + string(callID)),
			ToolCallID:   callID,
			Status:       contracts.ToolResultSucceeded,
			CompletedAt:  base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repo.SaveCall(context.Background(), contracts.ToolCall{ToolCallID: "call_other", TenantID: "tenant_1", ToolID: "echo", RunID: "run_other"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResult(context.Background(), contracts.ToolResult{ToolResultID: "result_other", ToolCallID: "call_other", Status: contracts.ToolResultSucceeded}); err != nil {
		t.Fatal(err)
	}
	limited, err := repo.ListResultsByRunLimit(context.Background(), "run_1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ToolResultID != "result_call_2" || limited[1].ToolResultID != "result_call_3" {
		t.Fatalf("expected limited results, got %#v", limited)
	}
	all, err := repo.ListResultsByRunLimit(context.Background(), "run_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected unlimited results for limit 0, got %#v", all)
	}
}

func TestToolRepositoryListResultsByTaskLimit(t *testing.T) {
	repo := NewInMemoryRepository()
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	for i, callID := range []contracts.ToolCallID{"call_1", "call_2", "call_3"} {
		if _, _, err := repo.SaveCall(context.Background(), contracts.ToolCall{
			ToolCallID: callID,
			TenantID:   "tenant_1",
			ToolID:     "echo",
			TaskID:     "task_1",
			RunID:      "run_1",
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveResult(context.Background(), contracts.ToolResult{
			ToolResultID: contracts.ToolResultID("result_" + string(callID)),
			ToolCallID:   callID,
			Status:       contracts.ToolResultSucceeded,
			CompletedAt:  base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repo.SaveCall(context.Background(), contracts.ToolCall{ToolCallID: "call_other", TenantID: "tenant_1", ToolID: "echo", TaskID: "task_other"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveResult(context.Background(), contracts.ToolResult{ToolResultID: "result_other", ToolCallID: "call_other", Status: contracts.ToolResultSucceeded, CompletedAt: base.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	limited, err := repo.ListResultsByTaskLimit(context.Background(), "tenant_1", "task_1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ToolResultID != "result_call_2" || limited[1].ToolResultID != "result_call_3" {
		t.Fatalf("expected latest two task results in ascending order, got %#v", limited)
	}
	all, err := repo.ListResultsByTaskLimit(context.Background(), "tenant_1", "task_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected unlimited task results for limit 0, got %#v", all)
	}
}

func TestToolRepositoryListArtifactRefsByRunLimit(t *testing.T) {
	repo := NewInMemoryRepository()
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	results := []struct {
		callID contracts.ToolCallID
		refs   []contracts.ArtifactRef
	}{
		{callID: "call_1", refs: []contracts.ArtifactRef{{ArtifactID: "artifact_1", Type: "text"}}},
		{callID: "call_2", refs: []contracts.ArtifactRef{{ArtifactID: "artifact_2", Type: "text"}}},
		{callID: "call_3", refs: []contracts.ArtifactRef{{ArtifactID: "artifact_1", Type: "text", Summary: "newer duplicate"}, {ArtifactID: "artifact_3", Type: "text"}}},
	}
	for i, item := range results {
		if _, _, err := repo.SaveCall(context.Background(), contracts.ToolCall{
			ToolCallID: item.callID,
			TenantID:   "tenant_1",
			ToolID:     "echo",
			RunID:      "run_1",
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveResult(context.Background(), contracts.ToolResult{
			ToolResultID: contracts.ToolResultID("result_" + string(item.callID)),
			ToolCallID:   item.callID,
			Status:       contracts.ToolResultSucceeded,
			ArtifactRefs: item.refs,
			CompletedAt:  base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	limited, err := repo.ListArtifactRefsByRunLimit(context.Background(), "run_1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ArtifactID != "artifact_1" || limited[0].Summary != "newer duplicate" || limited[1].ArtifactID != "artifact_3" {
		t.Fatalf("expected latest two unique artifact refs, got %#v", limited)
	}
	all, err := repo.ListArtifactRefsByRunLimit(context.Background(), "run_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected unlimited unique artifact refs for limit 0, got %#v", all)
	}
}
