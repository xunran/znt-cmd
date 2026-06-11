package repository

import (
	"context"
	"testing"

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
