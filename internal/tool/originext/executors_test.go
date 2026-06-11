package originext

import (
	"context"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/agentfactory"
	"znt/internal/contracts"
	"znt/internal/knowledge"
	"znt/internal/permission"
	"znt/internal/skillupdate"
	"znt/internal/tool/registry"
)

func TestRegisterOriginExtensionTools(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	permissions := permission.NewInMemoryService(nil, nil)
	knowledgeSvc := knowledge.NewInMemoryService(permissions, nil, nil)
	err := Register(reg, Services{
		Permissions:  permissions,
		SkillUpdates: skillupdate.NewService(permissions, nil, nil),
		Knowledge:    knowledgeSvc,
		AgentFactory: agentfactory.NewService(agentpackage.NewService(nil), permissions, nil, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("origin.permission.check"); !ok {
		t.Fatal("expected origin.permission.check to be registered")
	}
	if _, ok := reg.Get("origin.agent.create_draft"); !ok {
		t.Fatal("expected origin.agent.create_draft to be registered")
	}
}

func TestPermissionToolExecutes(t *testing.T) {
	ctx := context.Background()
	reg := registry.NewInMemoryRegistry()
	permissions := permission.NewInMemoryService(nil, nil)
	if err := Register(reg, Services{Permissions: permissions}); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.Get("origin.permission.check")
	if !ok {
		t.Fatal("tool not found")
	}
	out, _, err := tool.Executor.Execute(ctx, contracts.ToolCall{
		TenantID: "tenant",
		ToolID:   "origin.permission.check",
		Arguments: map[string]any{
			"group_id": "group-a",
			"actor_id": "alice",
			"action":   contracts.PermissionActionCrossGroupSearch,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := out["decision"].(contracts.PermissionDecision)
	if !ok {
		t.Fatalf("unexpected output %#v", out)
	}
	if decision.Decision != contracts.PermissionDecisionDenied {
		t.Fatalf("expected denied by default, got %#v", decision)
	}
}
