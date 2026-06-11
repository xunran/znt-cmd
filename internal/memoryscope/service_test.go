package memoryscope

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestMemoryScopeDoesNotCrossGroupsUntilShared(t *testing.T) {
	ctx := context.Background()
	svc := NewInMemoryService(nil, nil)
	_, err := svc.SaveScope(ctx, contracts.MemoryScope{
		TenantID:     "tenant",
		MemoryID:     "mem-1",
		ScopeID:      "group-a",
		OwnerGroupID: "group-a",
		Visibility:   contracts.VisibilityGroup,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, _, err := svc.CanRead(ctx, AccessInput{TenantID: "tenant", GroupID: "group-b", MemoryID: "mem-1"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("expected group-b to be denied before sharing")
	}
	if _, err := svc.GrantShare(ctx, "tenant", "mem-1", "group-b", "admin"); err != nil {
		t.Fatal(err)
	}
	allowed, _, err = svc.CanRead(ctx, AccessInput{TenantID: "tenant", GroupID: "group-b", MemoryID: "mem-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected group-b to read shared memory")
	}
}
