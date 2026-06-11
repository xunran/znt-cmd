package artifact

import (
	"context"
	"testing"

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
