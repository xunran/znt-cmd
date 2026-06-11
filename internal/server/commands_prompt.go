package server

import (
	"net/http"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/runtime/kernel"
)

func promptPreview(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if envelope.Context.TenantID == "" {
		envelope.Context.TenantID = caller.TenantID
	}
	input := payloadString(envelope.Payload, "input")
	if input == "" {
		input = payloadString(envelope.Payload, "task")
	}
	target := envelope.Target
	if raw, ok := envelope.Payload["target"].(map[string]any); ok {
		if agentID, _ := raw["agent_id"].(string); agentID != "" {
			target.AgentID = contracts.AgentID(agentID)
		}
		if version, _ := raw["version"].(string); version != "" {
			target.Version = contracts.AgentVersion(version)
		}
	}
	var draftDefinition *contracts.AgentDefinition
	if draftID := payloadString(envelope.Payload, "draft_id"); draftID != "" {
		draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
		}
		if draft.TenantID != caller.TenantID {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
		}
		compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
		if err != nil {
			return nil, err
		}
		compiled.TenantID = caller.TenantID
		draftDefinition = &compiled
		target.AgentID = draft.AgentID
		target.Version = draft.Version
	}
	if target.AgentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt.preview requires target.agent_id or draft_id", nil)
	}
	result, err := appCore.Coordinator.PreviewPromptBundle(r.Context(), kernel.PromptPreviewRequest{
		Target:  target,
		Input:   input,
		Context: envelope.Context,
		Draft:   draftDefinition,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"agent":              result.Agent,
		"policy_set_id":      result.PolicySet.PolicySetID,
		"policy_version":     result.PolicySet.Version,
		"work_view":          result.WorkView,
		"prompt_bundle":      result.PromptBundle,
		"prompt_bundle_hash": result.PromptBundle.Hash,
		"token_estimate":     result.TokenEstimate,
		"model_provider":     result.ModelProvider,
		"model_name":         result.ModelName,
	}, nil
}
