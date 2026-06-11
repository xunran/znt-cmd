package server

import (
	"errors"
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/governance/approval"
	policyengine "znt/internal/policy/engine"
)

func permissionPolicyUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.GroupPermissions == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "permission service is unavailable", nil)
	}
	groupID := contracts.GroupID(payloadString(envelope.Payload, "group_id"))
	subjectType := payloadString(envelope.Payload, "subject_type")
	if subjectType == "" {
		subjectType = contracts.PermissionSubjectRole
	}
	policy := contracts.GroupPermissionPolicy{
		TenantID:         caller.TenantID,
		GroupID:          groupID,
		SubjectID:        payloadString(envelope.Payload, "subject_id"),
		SubjectType:      subjectType,
		Actions:          stringSlice(envelope.Payload["actions"]),
		ResourceScopes:   stringSlice(envelope.Payload["resource_scopes"]),
		RequiresApproval: payloadBool(envelope.Payload, "requires_approval"),
		Reason:           payloadString(envelope.Payload, "reason"),
	}
	if policy.GroupID == "" || policy.SubjectID == "" || policy.SubjectType == "" || len(policy.Actions) == 0 {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "permission.policy.upsert requires group_id, subject_id, subject_type, and actions", nil)
	}
	if err := appCore.GroupPermissions.UpsertPolicy(r.Context(), policy); err != nil {
		return nil, err
	}
	return map[string]any{"policy": policy}, nil
}

func approvalResolve(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, approved bool) (any, error) {
	approvalID := contracts.ApprovalID(payloadString(envelope.Payload, "approval_id"))
	if approvalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "approval command requires approval_id", nil)
	}
	if appCore.Approvals == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	if approved {
		return appCore.Approvals.Approve(r.Context(), caller.TenantID, approvalID, caller.CallerID)
	}
	return appCore.Approvals.Reject(r.Context(), caller.TenantID, approvalID, caller.CallerID)
}

func validateReleaseApproval(appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, resourceType string, resourceID string, action string) (bool, error) {
	approvalID := contracts.ApprovalID(strings.TrimSpace(payloadString(envelope.Payload, "approval_id")))
	if approvalID == "" {
		return false, nil
	}
	if appCore.Approvals == nil {
		return false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	if _, err := appCore.Approvals.Validate(caller.TenantID, approvalID, resourceType, resourceID, action); err != nil {
		return false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, err.Error(), map[string]any{"approval_id": approvalID})
	}
	return true, nil
}

func consumeReleaseApproval(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, resourceType string, resourceID string, action string) error {
	approvalID := contracts.ApprovalID(strings.TrimSpace(payloadString(envelope.Payload, "approval_id")))
	if approvalID == "" {
		return nil
	}
	if appCore.Approvals == nil {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	if _, err := appCore.Approvals.Consume(r.Context(), caller.TenantID, approvalID, resourceType, resourceID, action, caller.CallerID); err != nil {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, err.Error(), map[string]any{"approval_id": approvalID})
	}
	return nil
}

func requestReleaseApprovalOnRequired(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, err error, resourceType string, resourceID string, action string, req policyengine.ReleaseRequest) (any, error) {
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeToolApprovalRequired {
		return nil, err
	}
	if appCore.Approvals == nil {
		return nil, err
	}
	approval, approvalErr := appCore.Approvals.Request(r.Context(), approval.RequestInput{
		TenantID:     caller.TenantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		RiskLevel:    contracts.RiskHigh,
		Reason:       runtimeErr.Message,
		RequestedBy:  caller.CallerID,
		TraceID:      envelope.TraceID,
	})
	if approvalErr != nil {
		return nil, approvalErr
	}
	return map[string]any{
		"status":   "approval_required",
		"approval": approval,
		"release_request": map[string]any{
			"action":         req.Action,
			"current_status": req.CurrentStatus,
			"canary_percent": req.CanaryPercent,
			"reason":         req.Reason,
		},
		"error": runtimeErr,
	}, nil
}
