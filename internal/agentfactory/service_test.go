package agentfactory

import (
	"context"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
	"znt/internal/permission"
)

func TestAgentFactoryCreatesDraftBehindPermission(t *testing.T) {
	ctx := context.Background()
	permissions := permission.NewInMemoryService(nil, nil)
	if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:    "tenant",
		GroupID:     "group-a",
		SubjectType: contracts.PermissionSubjectRole,
		SubjectID:   "agent_admin",
		Actions:     []string{contracts.PermissionActionAgentPackageCreate},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(agentpackage.NewService(nil), permissions, nil, nil)
	result, err := svc.CreateDraft(ctx, CreateDraftInput{
		TenantID:    "tenant",
		GroupID:     "group-a",
		RequestedBy: "alice",
		Roles:       []string{"agent_admin"},
		AgentID:     "risk-agent",
		Name:        "风险分析智能体",
		Objective:   "分析上线风险",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.DraftID == "" || result.Draft == nil {
		t.Fatalf("expected draft result, got %#v", result)
	}
}
