package identity

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestInMemoryServiceResolveAndListMembers(t *testing.T) {
	ctx := context.Background()
	svc := NewInMemoryService()
	_, err := svc.UpsertMember(ctx, contracts.GroupMemberProfile{
		TenantID:       "tenant",
		GroupID:        "group-a",
		ExternalUserID: "u1",
		DisplayName:    "Alice",
		Roles:          []string{"admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	member, ok, err := svc.ResolveMember(ctx, "tenant", "group-a", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || member.DisplayName != "Alice" {
		t.Fatalf("expected Alice, got %#v ok=%v", member, ok)
	}
	members, err := svc.ListGroupMembers(ctx, "tenant", "group-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].ExternalUserID != "u1" {
		t.Fatalf("unexpected members %#v", members)
	}
}
