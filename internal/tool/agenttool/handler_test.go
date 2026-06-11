package agenttool

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/tool/catalog"
)

func TestHandlerRunsProviderAgentForExportedTool(t *testing.T) {
	provider := loader.TestAgentDefinition()
	provider.TenantID = "tenant_1"
	provider.AgentID = "provider-agent"
	provider.Exports = contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
		ToolID:      "customer.lookup",
		Operation:   "lookup",
		Name:        "Customer lookup",
		Description: "Look up customer context.",
		InputSchema: map[string]any{"type": "object"},
		Status:      "enabled",
	}}}
	agents := loader.NewStaticLoader(provider)

	var got contracts.AgentEnvelope
	handler := Handler{
		Agents: agents,
		StartAgentRun: func(_ context.Context, envelope contracts.AgentEnvelope) (RunResult, error) {
			got = envelope
			return RunResult{
				RunID:  "run_provider",
				TaskID: "task_provider",
				Status: contracts.RunCompleted,
				Reply:  &contracts.DecisionReply{Kind: contracts.ReplyAnswer, Text: "customer found"},
				ArtifactRefs: []contracts.ArtifactRef{{
					ArtifactID: "artifact_1",
					Type:       "application/json",
				}},
			}, nil
		},
	}

	output, artifacts, err := handler.ExecuteAgentTool(context.Background(), contracts.ToolCall{
		ToolCallID:     "toolcall_1",
		TenantID:       "tenant_1",
		ToolID:         "customer.lookup",
		Arguments:      map[string]any{"input": "find ACME", "customer_id": "cust_1"},
		TraceID:        "trace_agent_tool",
		RunID:          "run_source",
		TaskID:         "task_source",
		IdempotencyKey: "idem_1",
	}, catalog.ToolManifest{
		TenantID: "tenant_1",
		ToolID:   "customer.lookup",
		Executor: catalog.ExecutorSpec{
			Type:       catalog.ExecutorTypeAgentTool,
			ProviderID: "provider-agent",
			Operation:  "lookup",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "agent.run" || got.Target.AgentID != "provider-agent" {
		t.Fatalf("expected provider agent run envelope, got %#v", got)
	}
	if got.Context.TenantID != "tenant_1" || got.Context.TaskID != "" || got.Context.RequestID != "idem_1" {
		t.Fatalf("unexpected runtime context %#v", got.Context)
	}
	if got.Payload["input"] != "find ACME" || got.Payload["operation"] != "lookup" || got.Payload["source_run_id"] != "run_source" {
		t.Fatalf("unexpected payload %#v", got.Payload)
	}
	args, _ := got.Payload["arguments"].(map[string]any)
	if args["customer_id"] != "cust_1" {
		t.Fatalf("expected original arguments in provider payload, got %#v", got.Payload["arguments"])
	}
	if output["reply_text"] != "customer found" || output["run_id"] != contracts.AgentRunID("run_provider") || len(artifacts) != 1 {
		t.Fatalf("unexpected output %#v artifacts %#v", output, artifacts)
	}
}

func TestHandlerRequiresAgentToolRunner(t *testing.T) {
	provider := loader.TestAgentDefinition()
	provider.TenantID = "tenant_1"
	provider.AgentID = "provider-agent"
	provider.Exports = contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
		ToolID:      "customer.lookup",
		Name:        "Customer lookup",
		Description: "Look up customer context.",
		InputSchema: map[string]any{"type": "object"},
	}}}
	handler := Handler{Agents: loader.NewStaticLoader(provider)}

	_, _, err := handler.ExecuteAgentTool(context.Background(), contracts.ToolCall{
		TenantID:  "tenant_1",
		ToolID:    "customer.lookup",
		Arguments: map[string]any{"input": "find ACME"},
	}, catalog.ToolManifest{
		TenantID: "tenant_1",
		ToolID:   "customer.lookup",
		Executor: catalog.ExecutorSpec{
			Type:       catalog.ExecutorTypeAgentTool,
			ProviderID: "provider-agent",
		},
	})
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeExecutionDomainUnavailable {
		t.Fatalf("expected execution domain unavailable, got %T %v", err, err)
	}
}
