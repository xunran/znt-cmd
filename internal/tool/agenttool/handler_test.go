package agenttool

import (
	"context"
	"strings"
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
	toolAgentResult, ok := output["tool_agent_result"].(map[string]any)
	if !ok {
		t.Fatalf("expected tool_agent_result, got %#v", output)
	}
	if toolAgentResult["result_summary"] != "customer found" || toolAgentResult["safe_for_user"] != false {
		t.Fatalf("unexpected tool_agent_result %#v", toolAgentResult)
	}
	if !stringSliceAnyContains(toolAgentResult["risk_flags"], "requires_main_agent_review") {
		t.Fatalf("expected main-agent review flag, got %#v", toolAgentResult["risk_flags"])
	}
}

func TestHandlerPassesParentConversationToProviderAgent(t *testing.T) {
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
	var got contracts.AgentEnvelope
	handler := Handler{
		Agents: loader.NewStaticLoader(provider),
		StartAgentRun: func(_ context.Context, envelope contracts.AgentEnvelope) (RunResult, error) {
			got = envelope
			return RunResult{RunID: "run_provider", TaskID: "task_provider", Status: contracts.RunCompleted, Reply: &contracts.DecisionReply{Text: "ok"}}, nil
		},
	}
	_, _, err := handler.ExecuteAgentTool(context.Background(), contracts.ToolCall{
		TenantID:       "tenant_1",
		ToolID:         "customer.lookup",
		Arguments:      map[string]any{"input": "find ACME"},
		RuntimeContext: map[string]any{"parent_context": map[string]any{"conversation": map[string]any{"provider": "znt-cmd", "kind": "group", "conversation_id": "chat_1", "thread_id": "chat_1", "current_message": map[string]any{"message_id": "msg_1", "speaker_id": "user_1", "speaker_type": "user", "text": "上下文"}}}},
		TraceID:        "trace_agent_tool",
		RunID:          "run_source",
		TaskID:         "task_source",
	}, catalog.ToolManifest{
		TenantID: "tenant_1",
		ToolID:   "customer.lookup",
		Executor: catalog.ExecutorSpec{Type: catalog.ExecutorTypeAgentTool, ProviderID: "provider-agent", Operation: "lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Context.Conversation == nil || got.Context.Conversation.ConversationID != "chat_1" || got.Context.Conversation.CurrentMessage.Text != "上下文" {
		t.Fatalf("expected parent conversation context, got %#v", got.Context.Conversation)
	}
	if got.Context.Conversation.ExternalRefs["chat_allowed_tool_ids"] != "parent_context.read" {
		t.Fatalf("expected provider run to allow parent_context.read, got %#v", got.Context.Conversation.ExternalRefs)
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

func TestHandlerSanitizesToolAgentInternalText(t *testing.T) {
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
	handler := Handler{
		Agents: loader.NewStaticLoader(provider),
		StartAgentRun: func(_ context.Context, _ contracts.AgentEnvelope) (RunResult, error) {
			return RunResult{
				RunID:  "run_provider",
				TaskID: "task_provider",
				Status: contracts.RunCompleted,
				Reply:  &contracts.DecisionReply{Kind: contracts.ReplyAnswer, Text: "capability_not_available"},
			}, nil
		},
	}

	output, _, err := handler.ExecuteAgentTool(context.Background(), contracts.ToolCall{
		TenantID:  "tenant_1",
		ToolID:    "customer.lookup",
		Arguments: map[string]any{"input": "find ACME"},
		TraceID:   "trace_agent_tool",
		RunID:     "run_source",
		TaskID:    "task_source",
	}, catalog.ToolManifest{
		TenantID: "tenant_1",
		ToolID:   "customer.lookup",
		Executor: catalog.ExecutorSpec{Type: catalog.ExecutorTypeAgentTool, ProviderID: "provider-agent", Operation: "lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(output["reply_text"].(string))
	if body == "capability_not_available" || body != "tool agent returned no user-ready content" {
		t.Fatalf("expected sanitized reply_text, got %#v", output)
	}
	toolAgentResult, _ := output["tool_agent_result"].(map[string]any)
	if toolAgentResult["result_summary"] != body || !stringSliceAnyContains(toolAgentResult["risk_flags"], "tool_agent_output_sanitized") {
		t.Fatalf("expected sanitized tool_agent_result, got %#v", toolAgentResult)
	}
}

func stringSliceAnyContains(value any, expected string) bool {
	switch current := value.(type) {
	case []string:
		for _, item := range current {
			if item == expected {
				return true
			}
		}
	case []any:
		for _, item := range current {
			if text, ok := item.(string); ok && text == expected {
				return true
			}
		}
	}
	return false
}
