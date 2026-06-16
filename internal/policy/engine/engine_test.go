package engine

import (
	"errors"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestFallbackPolicySetKeepsDefaultPolicyFamilies(t *testing.T) {
	policy := FallbackPolicySet("tenant_1", "policy_missing")
	if policy.TenantID != "tenant_1" || policy.PolicySetID != "policy_missing" {
		t.Fatalf("unexpected fallback identity: %#v", policy)
	}
	if policy.RuntimePolicy.MaxSteps == 0 || policy.PromptPolicy.MaxPromptTokens == 0 || policy.HandoffPolicy.MaxContextTokens == 0 {
		t.Fatalf("expected default policy families to be populated: %#v", policy)
	}
	if policy.RuntimePolicy.MaxSteps < 8 || policy.RuntimePolicy.MaxToolCalls < 8 {
		t.Fatalf("expected default policy to allow multi-step business flows, got max_steps=%d max_tool_calls=%d", policy.RuntimePolicy.MaxSteps, policy.RuntimePolicy.MaxToolCalls)
	}
	if !policy.ToolRepairPolicy.Enabled || policy.ToolRepairPolicy.MaxRepairAttempts == 0 {
		t.Fatalf("expected default tool repair policy, got %#v", policy.ToolRepairPolicy)
	}
	if policy.ApprovalPolicy != (contracts.ApprovalPolicy{RequireApprovalForHighRisk: true, RequireApprovalForExternalWrite: true}) {
		t.Fatalf("expected default approval policy, got %#v", policy.ApprovalPolicy)
	}
}

func TestEvaluateRepairAllowsRepairableToolFailure(t *testing.T) {
	policy := DefaultPolicySet()
	decision := EvaluateRepair(policy.ToolRepairPolicy, RepairRequest{
		Policy: policy,
		Tool:   contracts.ToolDefinition{ToolID: "echo", RiskLevel: contracts.RiskLow},
		Result: contracts.ToolResult{
			Status: contracts.ToolResultFailed,
			Error:  &contracts.ToolExecutionError{Code: contracts.CodeToolExecutionFailed, Message: "boom"},
		},
		FailureSeen: 1,
	})
	if decision.Action != string(RepairActionContinue) {
		t.Fatalf("expected repair continue, got %#v", decision)
	}
}

func TestEvaluateRepairStopsDeniedTool(t *testing.T) {
	policy := DefaultPolicySet()
	decision := EvaluateRepair(policy.ToolRepairPolicy, RepairRequest{
		Policy:      policy,
		Tool:        contracts.ToolDefinition{ToolID: "echo", RiskLevel: contracts.RiskLow},
		Result:      contracts.ToolResult{Status: contracts.ToolResultDenied},
		FailureSeen: 1,
	})
	if decision.Action != string(RepairActionStop) {
		t.Fatalf("expected repair stop, got %#v", decision)
	}
}

func TestEvaluateReleaseActionRequiresApprovalForLargeCanary(t *testing.T) {
	policy := contracts.ReleasePolicy{
		DefaultCanaryPercent:            10,
		MaxCanaryPercent:                100,
		MaxCanaryPercentWithoutApproval: 25,
	}
	decision, err := EvaluateReleaseAction(policy, ReleaseRequest{
		Action:        "canary",
		CurrentStatus: contracts.ReleaseEvaluated,
		CanaryPercent: 50,
		Now:           time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC),
	})
	if err == nil || decision.Decision != contracts.PolicyDecisionApprovalRequired {
		t.Fatalf("expected approval required, got decision=%#v err=%v", decision, err)
	}
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeToolApprovalRequired {
		t.Fatalf("unexpected error: %#v", err)
	}
	decision, err = EvaluateReleaseAction(policy, ReleaseRequest{
		Action:        "canary",
		CurrentStatus: contracts.ReleaseEvaluated,
		CanaryPercent: 50,
		Approved:      true,
		Now:           time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC),
	})
	if err != nil || decision.Decision != contracts.PolicyDecisionAllowed {
		t.Fatalf("expected approved large canary, got decision=%#v err=%v", decision, err)
	}
}

func TestEvaluateReleaseActionChecksWindowAndStablePolicy(t *testing.T) {
	policy := contracts.ReleasePolicy{
		RequireApprovalForStable:  true,
		RequireCanaryBeforeStable: true,
		AllowedWindowsUTC: []contracts.ReleaseWindow{{
			Days:         []string{"monday"},
			StartHourUTC: 9,
			EndHourUTC:   17,
		}},
	}
	decision, err := EvaluateReleaseAction(policy, ReleaseRequest{
		Action:        "stable",
		CurrentStatus: contracts.ReleaseCanary,
		Now:           time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC),
	})
	if err == nil || decision.Decision != contracts.PolicyDecisionApprovalRequired {
		t.Fatalf("expected outside-window approval, got decision=%#v err=%v", decision, err)
	}
	decision, err = EvaluateReleaseAction(policy, ReleaseRequest{
		Action:        "stable",
		CurrentStatus: contracts.ReleaseEvaluated,
		Approved:      true,
		Now:           time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	})
	if err == nil || decision.Decision != contracts.PolicyDecisionDenied {
		t.Fatalf("expected canary-before-stable denial, got decision=%#v err=%v", decision, err)
	}
}
