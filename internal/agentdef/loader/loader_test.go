package loader

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestStaticLoader(t *testing.T) {
	static := NewStaticLoader(TestAgentDefinition())
	def, err := static.Load(context.Background(), "tenant_1", "test-agent", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if def.AgentID != "test-agent" {
		t.Fatalf("unexpected definition: %#v", def)
	}
	_, err = static.Load(context.Background(), "tenant_1", "missing", "v1")
	var runtimeErr *contracts.RuntimeError
	if err == nil || !contractsErrorAs(err, &runtimeErr) || runtimeErr.Code != contracts.CodeAgentVersionNotFound {
		t.Fatalf("expected agent version not found runtime error, got %v", err)
	}
}

func TestStaticLoaderTenantOverride(t *testing.T) {
	global := TestAgentDefinition()
	global.Name = "global"
	tenant := TestAgentDefinition()
	tenant.TenantID = "tenant_1"
	tenant.Name = "tenant"
	tenant.Version = "v2"
	static := NewStaticLoader(global, tenant)

	def, err := static.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "tenant" || def.Version != "v2" {
		t.Fatalf("expected tenant definition default, got %#v", def)
	}

	def, err = static.Load(context.Background(), "tenant_2", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "global" || def.Version != "v1" {
		t.Fatalf("expected global fallback definition, got %#v", def)
	}
}

func contractsErrorAs(err error, target **contracts.RuntimeError) bool {
	if err == nil {
		return false
	}
	if runtimeErr, ok := err.(*contracts.RuntimeError); ok {
		*target = runtimeErr
		return true
	}
	return false
}
