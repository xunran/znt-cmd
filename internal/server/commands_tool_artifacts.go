package server

import (
	"fmt"
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/governance/approval"
	toolinvoke "znt/internal/tool/invoke"
	"znt/pkg/hash"
)

func externalToolInvoke(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	toolID, _ := envelope.Payload["tool_id"].(string)
	toolName, _ := envelope.Payload["tool_name"].(string)
	if toolID == "" {
		toolID = toolName
	}
	approvalGranted, err := consumeToolInvokeApproval(r, appCore, envelope, caller, toolID)
	if err != nil {
		return nil, err
	}
	result, err := appCore.ExternalTools.Invoke(r.Context(), toolinvoke.Request{
		Envelope: envelope,
		Caller: contracts.AgentCaller{
			CallerID:   caller.CallerID,
			CallerType: caller.CallerType,
			TenantID:   caller.TenantID,
		},
		IdempotencyKey:  idempotencyFromRequest(r, envelope, toolID),
		ApprovalGranted: approvalGranted,
	})
	if err != nil {
		return nil, err
	}
	if result.Status == contracts.ToolResultPendingApproval {
		return requestToolInvokeApproval(r, appCore, envelope, caller, toolID, result)
	}
	return result, nil
}

func consumeToolInvokeApproval(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, toolID string) (bool, error) {
	approvalID := contracts.ApprovalID(strings.TrimSpace(payloadString(envelope.Payload, "approval_id")))
	if approvalID == "" {
		return false, nil
	}
	if appCore.Approvals == nil {
		return false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	if _, err := appCore.Approvals.Consume(r.Context(), caller.TenantID, approvalID, "tool", toolID, "tools.invoke", caller.CallerID); err != nil {
		return false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, err.Error(), map[string]any{"approval_id": approvalID})
	}
	return true, nil
}

func requestToolInvokeApproval(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, toolID string, result contracts.ToolResult) (any, error) {
	if appCore.Approvals == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	riskLevel := contracts.RiskHigh
	if appCore.ToolRuntime.Registry != nil {
		if definition, ok := appCore.ToolRuntime.Definition(caller.TenantID, toolID); ok && definition.RiskLevel != "" {
			riskLevel = definition.RiskLevel
		}
	}
	approvalReq, err := appCore.Approvals.Request(r.Context(), approval.RequestInput{
		TenantID:     caller.TenantID,
		ResourceType: "tool",
		ResourceID:   toolID,
		Action:       "tools.invoke",
		RiskLevel:    riskLevel,
		Reason:       fmt.Sprintf("tool risk level %s requires approval", riskLevel),
		RequestedBy:  caller.CallerID,
		TraceID:      envelope.TraceID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":      "approval_required",
		"approval":    approvalReq,
		"tool_result": result,
	}, nil
}

func artifactRead(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	artifactID := contracts.ArtifactID(payloadString(envelope.Payload, "artifact_id"))
	if artifactID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "artifact.read requires artifact_id", nil)
	}
	policySetID := contracts.PolicySetID(payloadString(envelope.Payload, "policy_set_id"))
	if policySetID == "" {
		policySetID = "policy_default"
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, policySetID)
	if !policySet.ArtifactPolicy.AllowRead && !caller.HasRole(auth.RoleAdmin) {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "artifact read is denied by policy", map[string]any{"artifact_id": artifactID})
	}
	artifact, err := appCore.Artifacts.ReadArtifact(r.Context(), caller.TenantID, artifactID, caller.CallerID, caller.CallerType, envelope.TraceID)
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

func artifactDelete(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	artifactID := contracts.ArtifactID(payloadString(envelope.Payload, "artifact_id"))
	if artifactID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "artifact.delete requires artifact_id", nil)
	}
	reason := strings.TrimSpace(payloadString(envelope.Payload, "reason"))
	if reason == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "artifact.delete requires reason", nil)
	}
	policySetID := contracts.PolicySetID(payloadString(envelope.Payload, "policy_set_id"))
	if policySetID == "" {
		policySetID = "policy_default"
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, policySetID)
	if !policySet.ArtifactPolicy.AllowDelete && !caller.HasRole(auth.RoleAdmin) {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "artifact delete is denied by policy", map[string]any{"artifact_id": artifactID})
	}
	if err := appCore.Artifacts.DeleteArtifact(r.Context(), caller.TenantID, artifactID, caller.CallerID, caller.CallerType, envelope.TraceID, reason); err != nil {
		return nil, err
	}
	return map[string]any{"artifact_id": artifactID, "deleted": true}, nil
}

func idempotencyFromRequest(r *http.Request, envelope contracts.AgentEnvelope, toolID string) string {
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		return key
	}
	requestHash, err := hash.StableJSON(map[string]any{
		"target":    envelope.Target,
		"tenant_id": envelope.Context.TenantID,
		"task_id":   envelope.Context.TaskID,
		"tool_id":   toolID,
		"arguments": envelope.Payload["arguments"],
	})
	if err != nil {
		requestHash = "unhashed"
	}
	parts := []string{
		string(envelope.Context.TenantID),
		string(envelope.Context.TaskID),
		string(envelope.TraceID),
		toolID,
		requestHash,
	}
	return strings.Join(parts, ":")
}
