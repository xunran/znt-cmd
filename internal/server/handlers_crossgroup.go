package server

import (
	"net/http"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/crossgroup"
	"znt/internal/governance/approval"
)

func handleCrossGroupSharePolicies(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.CrossGroups == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "cross-group service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		policies, err := appCore.CrossGroups.ListSharePolicies(r.Context(), caller.TenantID, contracts.GroupID(r.URL.Query().Get("source_group_id")), contracts.GroupID(r.URL.Query().Get("target_group_id")))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policies": policies}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid cross-group share policy json")
		if !ok {
			return
		}
		policy := crossGroupSharePolicyFromPayload(payload, caller)
		if err := requireCrossGroupPolicyPermission(r, appCore, caller, policy); err != nil {
			writeRuntimeError(w, err)
			return
		}
		response, pending, err := crossGroupSharePolicyApprovalGate(r, appCore, caller, policy, contracts.ApprovalID(payloadString(payload, "approval_id")), contracts.TraceID(payloadString(payload, "trace_id")))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if pending {
			writeJSON(w, response, http.StatusOK)
			return
		}
		if approvalID := contracts.ApprovalID(payloadString(payload, "approval_id")); approvalID != "" {
			policy.ApprovalID = approvalID
			policy.ApprovedBy = caller.CallerID
		}
		created, err := appCore.CrossGroups.UpsertSharePolicy(r.Context(), policy)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": created}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported cross-group share policies method", nil), http.StatusMethodNotAllowed)
	}
}

func handleCrossGroupSharePolicyResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, policyID string) {
	if policyID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy_id is required", nil), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		policy, ok, err := appCore.CrossGroups.GetSharePolicy(r.Context(), caller.TenantID, contracts.CrossGroupSharePolicyID(policyID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "cross-group share policy not found", map[string]any{"policy_id": policyID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"policy": policy}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid cross-group share policy json")
		if !ok {
			return
		}
		policy := crossGroupSharePolicyFromPayload(payload, caller)
		policy.PolicyID = contracts.CrossGroupSharePolicyID(policyID)
		if err := requireCrossGroupPolicyPermission(r, appCore, caller, policy); err != nil {
			writeRuntimeError(w, err)
			return
		}
		updated, err := appCore.CrossGroups.UpsertSharePolicy(r.Context(), policy)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": updated}, http.StatusOK)
	case http.MethodDelete:
		policy, ok, err := appCore.CrossGroups.GetSharePolicy(r.Context(), caller.TenantID, contracts.CrossGroupSharePolicyID(policyID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "cross-group share policy not found", map[string]any{"policy_id": policyID}), http.StatusNotFound)
			return
		}
		policy.Status = contracts.CrossGroupShareDisabled
		policy.CreatedBy = firstNonEmpty(policy.CreatedBy, caller.CallerID)
		if err := requireCrossGroupPolicyPermission(r, appCore, caller, policy); err != nil {
			writeRuntimeError(w, err)
			return
		}
		updated, err := appCore.CrossGroups.UpsertSharePolicy(r.Context(), policy)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported cross-group share policy method", nil), http.StatusMethodNotAllowed)
	}
}

func requireCrossGroupPolicyPermission(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, policy contracts.CrossGroupSharePolicy) error {
	if appCore.GroupPermissions == nil {
		return nil
	}
	decision, err := appCore.GroupPermissions.Check(r.Context(), contracts.PermissionCheckInput{
		TenantID:      caller.TenantID,
		GroupID:       policy.SourceGroupID,
		ActorID:       caller.CallerID,
		ActorType:     caller.CallerType,
		Roles:         callerRoles(caller),
		Action:        contracts.PermissionActionCrossGroupPolicy,
		ResourceType:  "cross_group_share_policy",
		ResourceID:    string(policy.PolicyID),
		ResourceScope: string(policy.SourceGroupID) + ":" + string(policy.TargetGroupID),
	})
	if err != nil {
		return err
	}
	if decision.Decision != contracts.PermissionDecisionAllowed {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
	}
	return nil
}

func handleCrossGroupSearch(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported cross-group search method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid cross-group search json")
	if !ok {
		return
	}
	results, err := appCore.CrossGroups.Search(r.Context(), crossgroupSearchInputFromPayload(payload, caller))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"results": results}, http.StatusOK)
}

func crossGroupSharePolicyApprovalGate(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, policy contracts.CrossGroupSharePolicy, approvalID contracts.ApprovalID, traceID contracts.TraceID) (map[string]any, bool, error) {
	if !policy.RequiresApproval {
		return nil, false, nil
	}
	resourceID := string(policy.PolicyID)
	if resourceID == "" {
		resourceID = string(policy.SourceGroupID) + ":" + string(policy.TargetGroupID)
	}
	if approvalID != "" {
		if _, err := appCore.Approvals.Consume(r.Context(), caller.TenantID, approvalID, "cross_group_share_policy", resourceID, "cross_group.policy.upsert", caller.CallerID); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	approvalReq, err := appCore.Approvals.Request(r.Context(), approval.RequestInput{
		TenantID:     caller.TenantID,
		ResourceType: "cross_group_share_policy",
		ResourceID:   resourceID,
		Action:       "cross_group.policy.upsert",
		RiskLevel:    contracts.RiskHigh,
		Reason:       firstNonEmpty(policy.Reason, "cross-group share policy requires approval"),
		RequestedBy:  caller.CallerID,
		TraceID:      traceID,
	})
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"status": "approval_required", "approval_request": approvalReq}, true, nil
}

func crossGroupSharePolicyFromPayload(payload map[string]any, caller auth.CallerIdentity) contracts.CrossGroupSharePolicy {
	source := payload
	if raw, ok := payload["policy"].(map[string]any); ok {
		source = raw
	}
	return contracts.CrossGroupSharePolicy{
		PolicyID:         contracts.CrossGroupSharePolicyID(payloadString(source, "policy_id")),
		TenantID:         caller.TenantID,
		SourceGroupID:    contracts.GroupID(payloadString(source, "source_group_id")),
		TargetGroupID:    contracts.GroupID(payloadString(source, "target_group_id")),
		KnowledgeBaseIDs: knowledgeBaseIDsFromAny(source["knowledge_base_ids"]),
		RedactionPolicy:  firstNonEmpty(payloadString(source, "redaction_policy"), contracts.RedactionPolicySummaryOnly),
		RequiresApproval: payloadBool(source, "requires_approval"),
		Status:           firstNonEmpty(payloadString(source, "status"), contracts.CrossGroupShareEnabled),
		Reason:           payloadString(source, "reason"),
		CreatedBy:        firstNonEmpty(payloadString(source, "created_by"), caller.CallerID),
	}
}

func crossgroupSearchInputFromPayload(payload map[string]any, caller auth.CallerIdentity) crossgroup.SearchInput {
	return crossgroup.SearchInput{
		TenantID:       caller.TenantID,
		RequestGroupID: contracts.GroupID(payloadString(payload, "request_group_id")),
		SourceGroupID:  contracts.GroupID(payloadString(payload, "source_group_id")),
		RequestedBy:    firstNonEmpty(payloadString(payload, "requested_by"), caller.CallerID),
		Roles:          callerRoles(caller),
		Query:          payloadString(payload, "query"),
		Limit:          payloadInt(payload, "limit"),
		TraceID:        contracts.TraceID(payloadString(payload, "trace_id")),
	}
}
