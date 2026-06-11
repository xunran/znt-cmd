package toolpolicy

import (
	"context"
	"fmt"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/pkg/idgen"
)

type Evaluator struct {
	Audit audit.Logger
}

func New(auditLogger audit.Logger) Evaluator {
	return Evaluator{Audit: auditLogger}
}

func (e Evaluator) Evaluate(ctx context.Context, req EvaluateRequest) (contracts.PolicyDecision, error) {
	decision := e.evaluate(req)
	e.audit(ctx, req, "tool.policy_checked", decision)
	switch decision.Decision {
	case contracts.PolicyDecisionDenied:
		e.audit(ctx, req, contracts.AuditToolPolicyDenied, decision)
	case contracts.PolicyDecisionApprovalRequired:
		e.audit(ctx, req, contracts.AuditToolApprovalRequired, decision)
	case contracts.PolicyDecisionAllowed:
		if req.Tool.RiskLevel == contracts.RiskHigh || req.Tool.RiskLevel == contracts.RiskCritical {
			e.audit(ctx, req, contracts.AuditToolHighRiskInvoked, decision)
		}
	}
	if decision.Decision == contracts.PolicyDecisionDenied {
		return decision, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, nil)
	}
	return decision, nil
}

func (e Evaluator) audit(ctx context.Context, req EvaluateRequest, action string, decision contracts.PolicyDecision) {
	if e.Audit == nil {
		return
	}
	_ = e.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     req.TenantID,
		ActorID:      req.ActorID,
		ActorType:    req.ActorType,
		Action:       action,
		ResourceType: "tool",
		ResourceID:   req.Tool.ToolID,
		Decision:     string(decision.Decision),
		Reason:       decision.Reason,
		TraceID:      req.TraceID,
		TaskID:       req.Call.TaskID,
		RunID:        req.Call.RunID,
	})
}

type EvaluateRequest struct {
	TenantID  contracts.TenantID
	TraceID   contracts.TraceID
	ActorID   string
	ActorType string
	Agent     contracts.AgentDefinition
	Policy    contracts.PolicySet
	Tool      contracts.ToolDefinition
	Call      contracts.ToolCall
	Approved  bool
}

func (e Evaluator) evaluate(req EvaluateRequest) contracts.PolicyDecision {
	if contains(req.Agent.Tools.DeniedToolIDs, req.Tool.ToolID) || contains(req.Policy.ToolPolicy.DeniedToolIDs, req.Tool.ToolID) {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "tool is explicitly denied", RiskLevel: req.Tool.RiskLevel}
	}
	if contains(req.Agent.Tools.DeniedToolGroupIDs, req.Tool.GroupID) || contains(req.Policy.ToolPolicy.DeniedToolGroupIDs, req.Tool.GroupID) {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "tool group is explicitly denied", RiskLevel: req.Tool.RiskLevel}
	}
	if (len(req.Agent.Tools.AllowedToolIDs) > 0 || len(req.Agent.Tools.AllowedToolGroupIDs) > 0) && !allowedByAgent(req.Agent.Tools, req.Tool) {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "tool is not in agent allowed tools or groups", RiskLevel: req.Tool.RiskLevel}
	}
	if (len(req.Policy.ToolPolicy.AllowedToolIDs) > 0 || len(req.Policy.ToolPolicy.AllowedToolGroupIDs) > 0) && !allowedByPolicy(req.Policy.ToolPolicy, req.Tool) {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "tool is not in policy allowed tools or groups", RiskLevel: req.Tool.RiskLevel}
	}
	if req.Tool.Visibility == contracts.ToolPrivate {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionDenied, Reason: "private tool cannot be invoked by runtime candidate set", RiskLevel: req.Tool.RiskLevel}
	}
	if !req.Approved && (req.Tool.RiskLevel == contracts.RiskHigh || req.Tool.RiskLevel == contracts.RiskCritical || riskAtLeast(req.Tool.RiskLevel, req.Policy.ToolPolicy.RequireApprovalAtRiskLevel)) {
		return contracts.PolicyDecision{Decision: contracts.PolicyDecisionApprovalRequired, Reason: fmt.Sprintf("tool risk level %s requires approval", req.Tool.RiskLevel), RiskLevel: req.Tool.RiskLevel}
	}
	return contracts.PolicyDecision{Decision: contracts.PolicyDecisionAllowed, Reason: "tool allowed", RiskLevel: req.Tool.RiskLevel}
}

func contains(values []string, target string) bool {
	if target == "" {
		return false
	}
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func allowedByAgent(binding contracts.AgentToolsConfig, tool contracts.ToolDefinition) bool {
	if contains(binding.AllowedToolIDs, tool.ToolID) {
		return true
	}
	return contains(binding.AllowedToolGroupIDs, tool.GroupID)
}

func allowedByPolicy(binding contracts.ToolPolicy, tool contracts.ToolDefinition) bool {
	if contains(binding.AllowedToolIDs, tool.ToolID) {
		return true
	}
	return contains(binding.AllowedToolGroupIDs, tool.GroupID)
}

func riskAtLeast(actual contracts.RiskLevel, threshold contracts.RiskLevel) bool {
	if threshold == "" {
		return false
	}
	rank := map[contracts.RiskLevel]int{
		contracts.RiskLow:      1,
		contracts.RiskMedium:   2,
		contracts.RiskHigh:     3,
		contracts.RiskCritical: 4,
	}
	return rank[actual] >= rank[threshold]
}
