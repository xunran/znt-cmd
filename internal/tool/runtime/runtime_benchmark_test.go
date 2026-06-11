package runtime

import (
	"context"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/internal/policy/toolpolicy"
	"znt/internal/tool/registry"
)

func BenchmarkToolRuntimeInvokeLocal(b *testing.B) {
	ctx := context.Background()
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		b.Fatal(err)
	}
	rt := New(reg, toolpolicy.New(audit.NewInMemoryLogger()), trace.NewInMemoryRecorder())
	rt.Audit = audit.NewInMemoryLogger()
	rt.Now = func() time.Time { return time.Unix(1, 0).UTC() }
	req := InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_bench",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     loader.TestAgentDefinition(),
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID:     "toolcall_bench",
			ToolID:         "echo",
			Name:           "echo",
			Arguments:      map[string]any{"message": "hello"},
			RunID:          "run_bench",
			TaskID:         "task_bench",
			IdempotencyKey: "idem_bench",
			CreatedAt:      time.Unix(1, 0).UTC(),
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := rt.Invoke(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		if result.Status != contracts.ToolResultSucceeded {
			b.Fatalf("unexpected result: %#v", result)
		}
	}
}
