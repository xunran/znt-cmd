package permission

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestPermissionDefaultDenyAndAdminAllow(t *testing.T) {
	ctx := context.Background()
	svc := NewInMemoryService(nil, nil)
	denied, err := svc.Check(ctx, contracts.PermissionCheckInput{
		TenantID: "tenant",
		GroupID:  "group-a",
		ActorID:  "user-1",
		Action:   contracts.PermissionActionSkillPublish,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Decision != contracts.PermissionDecisionDenied {
		t.Fatalf("expected denied, got %#v", denied)
	}
	allowed, err := svc.Check(ctx, contracts.PermissionCheckInput{
		TenantID: "tenant",
		GroupID:  "group-a",
		ActorID:  "user-2",
		Roles:    []string{"group_admin"},
		Action:   contracts.PermissionActionSkillPublish,
	})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Decision != contracts.PermissionDecisionAllowed {
		t.Fatalf("expected admin allowed, got %#v", allowed)
	}
}

func TestPermissionPolicyCanRequireApproval(t *testing.T) {
	ctx := context.Background()
	svc := NewInMemoryService(nil, nil)
	if err := svc.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:         "tenant",
		GroupID:          "group-a",
		SubjectType:      contracts.PermissionSubjectRole,
		SubjectID:        "prompt_trainer",
		Actions:          []string{contracts.PermissionActionSkillProposeUpdate},
		RequiresApproval: true,
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Check(ctx, contracts.PermissionCheckInput{
		TenantID: "tenant",
		GroupID:  "group-a",
		ActorID:  "user-1",
		Roles:    []string{"prompt_trainer"},
		Action:   contracts.PermissionActionSkillProposeUpdate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != contracts.PermissionDecisionApprovalRequired {
		t.Fatalf("expected approval required, got %#v", decision)
	}
}
