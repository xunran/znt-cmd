package registry

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

type noopExecutor struct{}

func (noopExecutor) Execute(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return map[string]any{}, nil, nil
}

func TestRegisterRejectsDuplicateToolID(t *testing.T) {
	reg := NewInMemoryRegistry()
	tool := Tool{
		Definition: contracts.ToolDefinition{
			ToolID: "echo",
			Name:   "echo",
		},
		Executor: noopExecutor{},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(tool); err == nil {
		t.Fatal("expected duplicate tool registration to fail")
	}
}

func TestRegistryScopesTenantTools(t *testing.T) {
	reg := NewInMemoryRegistry()
	tenantTool := Tool{
		Definition: contracts.ToolDefinition{
			ToolID: "crm.lookup",
			Name:   "CRM lookup",
		},
		Executor: noopExecutor{},
		TenantID: "tenant_1",
	}
	if err := reg.Upsert(tenantTool); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("crm.lookup"); ok {
		t.Fatal("tenant-scoped tool should not be returned by global lookup")
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); !ok {
		t.Fatal("expected tenant lookup to find scoped tool")
	}
	if _, ok := reg.GetForTenant("tenant_2", "crm.lookup"); ok {
		t.Fatal("expected tenant lookup to hide scoped tool from other tenants")
	}
	if len(reg.CardsForTenant("tenant_1")) != 1 || len(reg.CardsForTenant("tenant_2")) != 0 {
		t.Fatalf("unexpected tenant cards: %#v %#v", reg.CardsForTenant("tenant_1"), reg.CardsForTenant("tenant_2"))
	}
	if err := reg.Upsert(Tool{
		Definition: contracts.ToolDefinition{
			ToolID: "crm.lookup",
			Name:   "global lookup",
		},
		Executor: noopExecutor{},
	}); err == nil {
		t.Fatal("expected global tool to conflict with existing tenant tool")
	}
}
