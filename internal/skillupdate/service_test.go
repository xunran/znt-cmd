package skillupdate

import (
	"context"
	"testing"

	"znt/internal/contracts"
	"znt/internal/permission"
)

func TestSkillUpdateRequiresPermission(t *testing.T) {
	ctx := context.Background()
	permissions := permission.NewInMemoryService(nil, nil)
	svc := NewService(permissions, nil, nil)
	_, decision, err := svc.Propose(ctx, ProposeInput{
		TenantID:    "tenant",
		GroupID:     "group-a",
		AgentID:     "origin-coordinator",
		RequestedBy: "user-1",
		Objective:   "优化接话判断",
	})
	if err == nil {
		t.Fatal("expected permission error")
	}
	if decision.Decision != contracts.PermissionDecisionDenied {
		t.Fatalf("expected denied decision, got %#v", decision)
	}
	if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:         "tenant",
		GroupID:          "group-a",
		SubjectType:      contracts.PermissionSubjectRole,
		SubjectID:        "prompt_trainer",
		Actions:          []string{contracts.PermissionActionSkillProposeUpdate},
		RequiresApproval: true,
	}); err != nil {
		t.Fatal(err)
	}
	request, decision, err := svc.Propose(ctx, ProposeInput{
		TenantID:    "tenant",
		GroupID:     "group-a",
		AgentID:     "origin-coordinator",
		RequestedBy: "user-1",
		Roles:       []string{"prompt_trainer"},
		Objective:   "优化接话判断",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != contracts.PermissionDecisionApprovalRequired || request.Status != contracts.SkillUpdateWaitingApproval {
		t.Fatalf("unexpected request %#v decision %#v", request, decision)
	}
}
