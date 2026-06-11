package server

import (
	"encoding/json"
	"net/http"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	policyengine "znt/internal/policy/engine"
)

func policyDraftCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	policy, err := policyFromPayload(envelope.Payload, caller.TenantID)
	if err != nil {
		return nil, err
	}
	return appCore.PolicyManager.CreateDraft(r.Context(), caller.TenantID, policy, caller.CallerID)
}

func policyDraftUpdate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy.update requires draft_id", nil)
	}
	if _, err := appCore.PolicyManager.DraftForTenant(r.Context(), caller.TenantID, draftID); err != nil {
		return nil, err
	}
	policy, err := policyFromPayload(envelope.Payload, caller.TenantID)
	if err != nil {
		return nil, err
	}
	draft, err := appCore.PolicyManager.UpdateDraft(r.Context(), draftID, policy, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func policyDraftValidate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy.draft.validate requires draft_id", nil)
	}
	draft, err := appCore.PolicyManager.ValidateDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func policyReview(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy.review requires draft_id", nil)
	}
	draft, err := appCore.PolicyManager.MarkReviewedForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func policyPublish(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy.publish requires draft_id", nil)
	}
	version, err := appCore.PolicyManager.PublishDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return version, nil
}

func policyReleaseAction(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, action string) (any, error) {
	policyVersionID := contracts.PolicyVersionID(payloadString(envelope.Payload, "policy_version_id"))
	if policyVersionID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy release command requires policy_version_id", nil)
	}
	version, _, ok, err := appCore.PolicyManager.GetVersion(r.Context(), policyVersionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "policy version not found", map[string]any{"policy_version_id": policyVersionID})
	}
	if version.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "policy version tenant does not match caller tenant", nil)
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, version.PolicySetID)
	resourceAction := "policy." + action
	approved, err := validateReleaseApproval(appCore, envelope, caller, "policy_version", string(policyVersionID), resourceAction)
	if err != nil {
		return nil, err
	}
	releaseReq := policyengine.ReleaseRequest{
		Action:        action,
		CurrentStatus: version.Status,
		CanaryPercent: payloadInt(envelope.Payload, "canary_percent"),
		Approved:      approved,
		Reason:        payloadString(envelope.Payload, "reason"),
		Now:           time.Now().UTC(),
	}
	if _, err := policyengine.EvaluateReleaseAction(policySet.ReleasePolicy, releaseReq); err != nil {
		return requestReleaseApprovalOnRequired(r, appCore, envelope, caller, err, "policy_version", string(policyVersionID), resourceAction, releaseReq)
	}
	if approved {
		if err := consumeReleaseApproval(r, appCore, envelope, caller, "policy_version", string(policyVersionID), resourceAction); err != nil {
			return nil, err
		}
	}
	switch action {
	case "canary":
		return appCore.PolicyManager.MarkCanary(r.Context(), policyVersionID, caller.CallerID)
	case "stable":
		return appCore.PolicyManager.MarkStable(r.Context(), policyVersionID, caller.CallerID)
	case "rollback":
		return appCore.PolicyManager.Rollback(r.Context(), policyVersionID, caller.CallerID, payloadString(envelope.Payload, "reason"))
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown policy release action", nil)
	}
}

func policyFromPayload(payload map[string]any, tenantID contracts.TenantID) (contracts.PolicySet, error) {
	rawPolicy := payload["policy"]
	if rawPolicy == nil {
		rawPolicy = payload["policy_set"]
	}
	var policy contracts.PolicySet
	if rawPolicy != nil {
		data, err := json.Marshal(rawPolicy)
		if err != nil {
			return contracts.PolicySet{}, err
		}
		if err := json.Unmarshal(data, &policy); err != nil {
			return contracts.PolicySet{}, err
		}
	} else {
		policy = policyengine.DefaultPolicySet()
	}
	if value := payloadString(payload, "policy_set_id"); value != "" {
		policy.PolicySetID = contracts.PolicySetID(value)
	}
	if value := payloadString(payload, "version"); value != "" {
		policy.Version = value
	}
	if policy.PolicySetID == "" {
		policy.PolicySetID = "policy_default"
	}
	policy.TenantID = tenantID
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = time.Now().UTC()
	}
	return policy, nil
}
