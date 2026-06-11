package audit

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestInMemoryLoggerSearch(t *testing.T) {
	logger := NewInMemoryLogger()
	event := contracts.AuditEvent{
		AuditID:      "audit_1",
		TenantID:     contracts.TenantID("tenant_1"),
		ActorID:      "agent_1",
		ActorType:    "agent",
		Action:       contracts.AuditToolApprovalRequired,
		ResourceType: "tool",
		ResourceID:   "tool_1",
		Decision:     "approval_required",
		CreatedAt:    time.Unix(1, 0).UTC(),
	}
	if err := logger.Log(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	found, err := logger.Search(context.Background(), Filter{
		TenantID:   contracts.TenantID("tenant_1"),
		Action:     contracts.AuditToolApprovalRequired,
		ResourceID: "tool_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Decision != "approval_required" {
		t.Fatalf("unexpected audit search result: %#v", found)
	}
}
