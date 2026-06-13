package artifact

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestInMemoryMemoryStoreRequiresTenant(t *testing.T) {
	store := NewInMemoryMemoryStore(nil)
	if _, err := store.WriteMemory(context.Background(), contracts.MemoryEvent{Content: "remember"}, "user_1", "user", "trace_1"); err == nil {
		t.Fatal("expected memory write without tenant to fail")
	}
	written, err := store.WriteMemory(context.Background(), contracts.MemoryEvent{
		TenantID: "tenant_1",
		Content:  "remember",
		Summary:  "remember",
	}, "user_1", "user", "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMemory(context.Background(), "tenant_2", written.MemoryID); err == nil {
		t.Fatal("expected cross-tenant memory read to fail")
	}
}

func TestInMemoryMemoryStoreListMemoryLimitFiltersScopes(t *testing.T) {
	store := NewInMemoryMemoryStore(nil)
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	events := []contracts.MemoryEvent{
		{MemoryID: "memory_1", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "user", Summary: "old user", CreatedAt: base},
		{MemoryID: "memory_2", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "agent", Summary: "agent memory", CreatedAt: base.Add(time.Minute)},
		{MemoryID: "memory_3", TenantID: "tenant_1", AgentID: "agent_1", UserID: "user_1", Scope: "user", Summary: "new user", CreatedAt: base.Add(2 * time.Minute)},
		{MemoryID: "memory_4", TenantID: "tenant_1", AgentID: "agent_other", UserID: "user_1", Scope: "user", Summary: "other agent", CreatedAt: base.Add(3 * time.Minute)},
	}
	for _, event := range events {
		event.Content = event.Summary
		if _, err := store.WriteMemory(context.Background(), event, "test", "test", "trace_1"); err != nil {
			t.Fatal(err)
		}
	}

	limited, err := store.ListMemoryLimit(context.Background(), "tenant_1", "agent_1", "user_1", []string{"user"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].MemoryID != "memory_3" {
		t.Fatalf("expected latest scoped memory, got %#v", limited)
	}
	all, err := store.ListMemoryLimit(context.Background(), "tenant_1", "agent_1", "user_1", []string{"user", "agent"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].MemoryID != "memory_1" || all[2].MemoryID != "memory_3" {
		t.Fatalf("expected unlimited scoped memories in ascending order, got %#v", all)
	}
}
