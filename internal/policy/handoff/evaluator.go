package handoff

import (
	"context"

	"znt/internal/contracts"
)

type Evaluator struct{}

type EvaluateRequest struct {
	Policy contracts.HandoffPolicy
	Mode   contracts.HandoffMode

	TenantID       contracts.TenantID
	TargetTenantID contracts.TenantID

	FromAgentID contracts.AgentID
	ToAgentID   contracts.AgentID

	ToAgentExists     bool
	CapabilityMatched bool

	ArtifactRefs []contracts.ArtifactRef
	MemoryRefs   []contracts.MemoryID
}

func New() Evaluator {
	return Evaluator{}
}

func (e Evaluator) Evaluate(ctx context.Context, policy contracts.HandoffPolicy, mode contracts.HandoffMode, artifactRefs []contracts.ArtifactRef) (contracts.PolicyDecision, error) {
	return e.EvaluateRequest(ctx, EvaluateRequest{
		Policy:            policy,
		Mode:              mode,
		FromAgentID:       "legacy_from_agent",
		ToAgentID:         "legacy_to_agent",
		ArtifactRefs:      artifactRefs,
		ToAgentExists:     true,
		CapabilityMatched: true,
	})
}

func (Evaluator) EvaluateRequest(_ context.Context, req EvaluateRequest) (contracts.PolicyDecision, error) {
	policy := req.Policy
	mode := req.Mode
	if mode == "" {
		mode = policy.DefaultMode
	}
	if mode == "" {
		mode = contracts.HandoffHybrid
	}
	if err := mode.Validate(); err != nil {
		return denied("invalid handoff mode", contracts.RiskMedium), contracts.NewRuntimeError(contracts.CodeHandoffDenied, err.Error(), nil)
	}
	if req.FromAgentID == "" {
		return denied("from_agent_id is required", contracts.RiskMedium), contracts.NewRuntimeError(contracts.CodeHandoffDenied, "from_agent_id is required", nil)
	}
	if req.ToAgentID == "" {
		return denied("to_agent_id is required", contracts.RiskMedium), contracts.NewRuntimeError(contracts.CodeHandoffDenied, "to_agent_id is required", nil)
	}
	if !req.ToAgentExists {
		return denied("target agent not found", contracts.RiskHigh), contracts.NewRuntimeError(contracts.CodeHandoffDenied, "target agent not found", map[string]any{"to_agent_id": req.ToAgentID})
	}
	if !req.CapabilityMatched {
		return denied("target agent capability does not match objective", contracts.RiskMedium), contracts.NewRuntimeError(contracts.CodeHandoffDenied, "target agent capability does not match objective", map[string]any{"to_agent_id": req.ToAgentID})
	}
	if req.TenantID != "" && req.TargetTenantID != "" && req.TenantID != req.TargetTenantID {
		return denied("cross-tenant handoff is not allowed", contracts.RiskHigh), contracts.NewRuntimeError(contracts.CodeHandoffDenied, "cross-tenant handoff is not allowed", map[string]any{"tenant_id": req.TenantID, "target_tenant_id": req.TargetTenantID})
	}
	if mode == contracts.HandoffFullContext && !policy.AllowFullContext {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "full_context is disabled", RiskLevel: contracts.RiskHigh}, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "full_context is disabled", nil)
	}
	if len(req.ArtifactRefs) > 0 && !policy.AllowArtifactRead {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "artifact refs are not allowed", RiskLevel: contracts.RiskMedium}, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "artifact refs are not allowed", nil)
	}
	if len(req.MemoryRefs) > 0 && !policy.AllowMemoryRead {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "memory refs are not allowed", RiskLevel: contracts.RiskMedium}, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "memory refs are not allowed", nil)
	}
	if len(req.ArtifactRefs) > 0 && policy.RequireApprovalForSensitiveArtifacts {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionApprovalRequired, Reason: "artifact handoff requires approval", RiskLevel: contracts.RiskHigh}, nil
	}
	if req.FromAgentID != req.ToAgentID && policy.RequireApprovalForCrossAgent {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionApprovalRequired, Reason: "cross-agent handoff requires approval", RiskLevel: contracts.RiskMedium}, nil
	}
	return contracts.PolicyDecision{Decision: contracts.PolicyDecisionAllowed, Reason: "handoff allowed", RiskLevel: contracts.RiskLow}, nil
}

func denied(reason string, risk contracts.RiskLevel) contracts.PolicyDecision {
	return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: reason, RiskLevel: risk}
}
