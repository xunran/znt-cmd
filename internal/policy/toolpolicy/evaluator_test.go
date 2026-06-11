package toolpolicy

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/governance/audit"
)

func TestEvaluateApprovalRequiredByRisk(t *testing.T) {
	logger := audit.NewInMemoryLogger()
	evaluator := New(logger)
	decision, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     loader.TestAgentDefinition(),
		Policy:    contracts.PolicySet{},
		Tool: contracts.ToolDefinition{
			ToolID:     "echo",
			Name:       "echo",
			RiskLevel:  contracts.RiskHigh,
			Visibility: contracts.ToolExposed,
		},
		Call: contracts.ToolCall{ToolID: "echo", Name: "echo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "approval_required" {
		t.Fatalf("expected approval_required, got %#v", decision)
	}
	events, err := logger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: contracts.AuditToolApprovalRequired})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected approval audit event, got %#v", events)
	}
}

func TestEvaluateDeniedTool(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.DeniedToolIDs = []string{"echo"}
	logger := audit.NewInMemoryLogger()
	evaluator := New(logger)
	_, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		Policy:    contracts.PolicySet{},
		Tool: contracts.ToolDefinition{
			ToolID:     "echo",
			Name:       "echo",
			RiskLevel:  contracts.RiskLow,
			Visibility: contracts.ToolExposed,
		},
		Call: contracts.ToolCall{ToolID: "echo", Name: "echo"},
	})
	if err == nil {
		t.Fatal("expected denied error")
	}
	events, err := logger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: contracts.AuditToolPolicyDenied})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected denied audit event, got %#v", events)
	}
}

func TestEvaluateAllowsAgentToolGroup(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolGroupIDs = []string{"crm"}
	evaluator := New(nil)
	decision, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		TenantID: "tenant_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Tool: contracts.ToolDefinition{
			ToolID:     "crm.lookup",
			GroupID:    "crm",
			Name:       "CRM lookup",
			RiskLevel:  contracts.RiskLow,
			Visibility: contracts.ToolProtected,
		},
		Call: contracts.ToolCall{ToolID: "crm.lookup", Name: "CRM lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != contracts.PolicyDecisionAllowed {
		t.Fatalf("expected allowed decision, got %#v", decision)
	}
}

func TestEvaluateDeniesToolGroup(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.DeniedToolGroupIDs = []string{"crm"}
	evaluator := New(nil)
	_, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		TenantID: "tenant_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Tool: contracts.ToolDefinition{
			ToolID:     "crm.lookup",
			GroupID:    "crm",
			Name:       "CRM lookup",
			RiskLevel:  contracts.RiskLow,
			Visibility: contracts.ToolProtected,
		},
		Call: contracts.ToolCall{ToolID: "crm.lookup", Name: "CRM lookup"},
	})
	if err == nil {
		t.Fatal("expected denied tool group to fail")
	}
}
