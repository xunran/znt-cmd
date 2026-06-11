package invoke

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/governance/trace"
	policyengine "znt/internal/policy/engine"
	"znt/internal/policy/toolpolicy"
	"znt/internal/tool/registry"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
)

func TestInvokeExposedToolIsIdempotent(t *testing.T) {
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	repo := toolrepo.NewInMemoryRepository()
	service := Service{
		Agents:      agents,
		ToolRepo:    repo,
		ToolRuntime: toolruntime.New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder()),
		Policies:    policyengine.NewInMemoryStore(policyengine.DefaultPolicySet()),
	}
	req := Request{
		Envelope: contracts.AgentEnvelope{
			TraceID: "trace_1",
			Target:  contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
			Payload: map[string]any{
				"tool_id":   "echo",
				"arguments": map[string]any{"message": "hello"},
			},
		},
		Caller:         contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		IdempotencyKey: "same-key",
	}
	first, err := service.Invoke(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Invoke(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ToolResultID != second.ToolResultID {
		t.Fatalf("expected idempotent result, got %s and %s", first.ToolResultID, second.ToolResultID)
	}
	call, ok, err := repo.GetCall(context.Background(), first.ToolCallID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected saved tool call")
	}
	if call.ToolVersion == "" || call.ExecutionProfile == "" {
		t.Fatalf("expected tool call execution snapshot, got %#v", call)
	}
	if call.TraceID != "trace_1" {
		t.Fatalf("expected trace on tool call, got %q", call.TraceID)
	}
	if _, ok := call.Arguments["trace_id"]; ok {
		t.Fatalf("trace_id should not be injected into tool arguments: %#v", call.Arguments)
	}
}

func TestInvokeRejectsNonExposedTool(t *testing.T) {
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	service := Service{
		Agents:      agents,
		ToolRepo:    toolrepo.NewInMemoryRepository(),
		ToolRuntime: toolruntime.New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder()),
		Policies:    policyengine.NewInMemoryStore(policyengine.DefaultPolicySet()),
	}
	_, err := service.Invoke(context.Background(), Request{
		Envelope: contracts.AgentEnvelope{
			Target:  contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
			Payload: map[string]any{"tool_id": "artifact.create", "arguments": map[string]any{"content": "x"}},
		},
		Caller: contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
	})
	if err == nil {
		t.Fatal("expected non-exposed tool to be denied")
	}
}
