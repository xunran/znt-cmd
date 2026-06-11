package server

import (
	"net/http"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	policyengine "znt/internal/policy/engine"
	toolcatalog "znt/internal/tool/catalog"
)

func packagePublish(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	payload := envelope.Payload
	if draftID, _ := payload["draft_id"].(string); draftID != "" {
		release, err := appCore.Packages.PublishDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
		if err != nil {
			return nil, err
		}
		return releaseAndRegisterDraft(r, appCore, release, draftID, caller)
	}
	agentID, _ := payload["agent_id"].(string)
	version, _ := payload["version"].(string)
	prompt, _ := payload["prompt"].(string)
	agentsMD, _ := payload["agents_md"].(string)
	if agentID == "" {
		agentID = string(envelope.Target.AgentID)
	}
	if agentID == "" || version == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.publish requires agent_id and version", nil)
	}
	source := agentPackageSourceFromPayload(payload)
	source.AgentsMD = agentsMD
	source.Prompt = prompt
	draft, err := appCore.Packages.CreateDraft(r.Context(), caller.TenantID, contracts.AgentID(agentID), contracts.AgentVersion(version), source, caller.CallerID)
	if err != nil {
		return nil, err
	}
	if _, err := appCore.Packages.ValidateDraft(r.Context(), draft.DraftID); err != nil {
		return nil, err
	}
	release, err := appCore.Packages.PublishDraft(r.Context(), draft.DraftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return releaseAndRegisterDraft(r, appCore, release, draft.DraftID, caller)
}

func releaseAndRegisterDraft(r *http.Request, appCore *core.Core, release contracts.AgentPackageVersion, draftID string, caller auth.CallerIdentity) (contracts.AgentPackageVersion, error) {
	draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if ok {
		compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
		if err != nil {
			return contracts.AgentPackageVersion{}, err
		}
		compiled.TenantID = caller.TenantID
		compiled.PackageVersionID = release.PackageVersionID
		appCore.AgentRegistry.Put(compiled)
		if appCore.ToolCatalog != nil {
			if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
				return contracts.AgentPackageVersion{}, err
			}
		}
	}
	return release, nil
}

func packageDraftCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	agentID, _ := envelope.Payload["agent_id"].(string)
	version, _ := envelope.Payload["version"].(string)
	if agentID == "" {
		agentID = string(envelope.Target.AgentID)
	}
	if agentID == "" || version == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.create requires agent_id and version", nil)
	}
	source := agentPackageSourceFromPayload(envelope.Payload)
	return appCore.Packages.CreateDraft(r.Context(), caller.TenantID, contracts.AgentID(agentID), contracts.AgentVersion(version), source, caller.CallerID)
}

func agentPackageSourceFromPayload(payload map[string]any) agentpackage.AgentPackageSource {
	return agentpackage.AgentPackageSource{
		AgentsMD:      payloadString(payload, "agents_md"),
		Prompt:        payloadString(payload, "prompt"),
		ToolBindings:  parseToolsPayload(payload["tool_bindings"]),
		Collaborators: parseCollaboratorsPayload(payload["collaborators"]),
		Exports:       parseAgentExportsPayload(payload["exports"]),
		RuntimeHooks:  parseRuntimeHooksPayload(payload["runtime_hooks"]),
		Metadata:      parseMetadata(payload["metadata"]),
	}
}

func packageCollaboratorUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator command requires draft_id", nil)
	}
	collaborator := parseCollaboratorPayload(envelope.Payload)
	if collaborator.AgentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator command requires agent_id", nil)
	}
	if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
		return nil, err
	}
	return appCore.Packages.UpsertCollaboratorForTenant(r.Context(), caller.TenantID, draftID, collaborator, caller.CallerID)
}

func packageCollaboratorReplace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator.replace requires draft_id", nil)
	}
	collaborators := parseCollaboratorsPayload(envelope.Payload["collaborators"])
	for _, collaborator := range collaborators {
		if collaborator.AgentID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator agent_id is required", nil)
		}
		if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
			return nil, err
		}
	}
	return appCore.Packages.PatchCollaboratorsForTenant(r.Context(), caller.TenantID, draftID, collaborators, caller.CallerID)
}

func packageCollaboratorRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	agentID := contracts.AgentID(payloadString(envelope.Payload, "agent_id"))
	if draftID == "" || agentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator.remove requires draft_id and agent_id", nil)
	}
	return appCore.Packages.RemoveCollaboratorForTenant(r.Context(), caller.TenantID, draftID, agentID, caller.CallerID)
}

func packageExportedToolUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool command requires draft_id", nil)
	}
	tool := parseExportedToolPayload(envelope.Payload)
	if tool.ToolID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool command requires tool_id", nil)
	}
	draft, err := appCore.Packages.UpsertExportedToolForTenant(r.Context(), caller.TenantID, draftID, tool, caller.CallerID)
	if err != nil {
		return nil, err
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return nil, err
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
			return nil, err
		}
	}
	return draft, nil
}

func packageExportedToolReplace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool.replace requires draft_id", nil)
	}
	existing, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if existing.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	exports := parseAgentExportsPayload(envelope.Payload["exports"])
	draft, err := appCore.Packages.PatchExportsForTenant(r.Context(), caller.TenantID, draftID, exports, caller.CallerID)
	if err != nil {
		return nil, err
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return nil, err
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		if err := disableRemovedAgentExportedTools(r.Context(), appCore, existing, compiled, caller.CallerID); err != nil {
			return nil, err
		}
		if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
			return nil, err
		}
	}
	return draft, nil
}

func packageExportedToolRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	toolID := payloadString(envelope.Payload, "tool_id")
	if draftID == "" || toolID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool.remove requires draft_id and tool_id", nil)
	}
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
	updated, err := appCore.Packages.RemoveExportedToolForTenant(r.Context(), caller.TenantID, draftID, toolID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	if appCore.ToolCatalog != nil {
		manifest := toolcatalog.ToolManifest{
			TenantID:    caller.TenantID,
			ToolID:      toolID,
			Name:        toolID,
			Description: "disabled exported tool",
			InputSchema: map[string]any{"type": "object"},
			Executor:    toolcatalog.ExecutorSpec{Type: toolcatalog.ExecutorTypeAgentTool, ProviderID: string(draft.AgentID), Operation: toolID},
			Status:      toolcatalog.StatusDisabled,
		}
		if existing, ok := appCore.ToolCatalog.GetManifest(caller.TenantID, toolID); ok {
			manifest = existing
			manifest.Status = toolcatalog.StatusDisabled
		}
		_, _ = appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
	}
	return updated, nil
}

func packageDraftPatchPrompt(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	prompt := payloadString(envelope.Payload, "prompt")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_prompt requires draft_id", nil)
	}
	draft, err := appCore.Packages.PatchPromptForTenant(r.Context(), caller.TenantID, draftID, prompt, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageDraftPatchDeveloperPrompt(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	developerPrompt := payloadString(envelope.Payload, "developer_prompt")
	if developerPrompt == "" {
		developerPrompt = payloadString(envelope.Payload, "prompt")
	}
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_developer_prompt requires draft_id", nil)
	}
	draft, err := appCore.Packages.PatchDeveloperPromptForTenant(r.Context(), caller.TenantID, draftID, developerPrompt, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageDraftPatchSystemPrompt(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	systemPrompt := payloadString(envelope.Payload, "system_prompt")
	if systemPrompt == "" {
		systemPrompt = payloadString(envelope.Payload, "prompt")
	}
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_system_prompt requires draft_id", nil)
	}
	draft, err := appCore.Packages.PatchSystemPromptForTenant(r.Context(), caller.TenantID, draftID, systemPrompt, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageDraftPatchAgentsMD(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	agentsMD := payloadString(envelope.Payload, "agents_md")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_agents_md requires draft_id", nil)
	}
	draft, err := appCore.Packages.PatchAgentsMDForTenant(r.Context(), caller.TenantID, draftID, agentsMD, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageToolBindingUpdate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.tool_binding.update requires draft_id", nil)
	}
	draft, err := appCore.Packages.UpdateToolBindingForTenant(r.Context(), caller.TenantID, draftID, parseToolsPayload(envelope.Payload["tool_bindings"]), caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageRuntimeHooksUpdate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.runtime_hooks.update requires draft_id", nil)
	}
	draft, err := appCore.Packages.PatchRuntimeHooksForTenant(r.Context(), caller.TenantID, draftID, parseRuntimeHooksPayload(envelope.Payload["runtime_hooks"]), caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageSkillUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.skill command requires draft_id", nil)
	}
	skill, err := skillDraftInput(envelope.Payload)
	if err != nil {
		return nil, err
	}
	draft, err := appCore.Packages.UpsertSkillForTenant(r.Context(), caller.TenantID, draftID, skill, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageSkillRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	skillID := payloadString(envelope.Payload, "skill_id")
	if draftID == "" || skillID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.skill.remove requires draft_id and skill_id", nil)
	}
	draft, err := appCore.Packages.RemoveSkillForTenant(r.Context(), caller.TenantID, draftID, skillID, payloadString(envelope.Payload, "version"), caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageProposalCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.create requires draft_id", nil)
	}
	return appCore.Packages.CreateProposalForTenant(
		r.Context(),
		caller.TenantID,
		draftID,
		payloadString(envelope.Payload, "proposal_type"),
		payloadString(envelope.Payload, "title"),
		payloadString(envelope.Payload, "reason"),
		parseMetadata(envelope.Payload["patch"]),
		caller.CallerID,
	)
}

func packageProposalSubmit(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.submit requires proposal_id", nil)
	}
	return appCore.Packages.SubmitProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
}

func packageProposalApprove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.approve requires proposal_id", nil)
	}
	return appCore.Packages.ApproveProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
}

func packageProposalReject(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.reject requires proposal_id", nil)
	}
	return appCore.Packages.RejectProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID, payloadString(envelope.Payload, "reason"))
}

func packageProposalPublish(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.publish requires proposal_id", nil)
	}
	proposal, ok, err := appCore.Packages.GetProposal(r.Context(), proposalID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "proposal not found", nil)
	}
	release, err := appCore.Packages.PublishProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return releaseAndRegisterDraft(r, appCore, release, proposal.DraftID, caller)
}

func packageDraftValidate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.validate requires draft_id", nil)
	}
	draft, err := appCore.Packages.ValidateDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageReview(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.review requires draft_id", nil)
	}
	draft, err := appCore.Packages.MarkReviewedForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageReleaseAction(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, action string) (any, error) {
	packageVersionID, _ := envelope.Payload["package_version_id"].(string)
	if packageVersionID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "package release command requires package_version_id", nil)
	}
	current, err := ensurePackageReleaseTenant(appCore, contracts.PackageVersionID(packageVersionID), caller.TenantID)
	if err != nil {
		return nil, err
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, "policy_default")
	resourceAction := "agent.package." + action
	approved, err := validateReleaseApproval(appCore, envelope, caller, "agent_package_version", packageVersionID, resourceAction)
	if err != nil {
		return nil, err
	}
	releaseReq := policyengine.ReleaseRequest{
		Action:        action,
		CurrentStatus: current.Status,
		CanaryPercent: payloadInt(envelope.Payload, "canary_percent"),
		Approved:      approved,
		Reason:        payloadString(envelope.Payload, "reason"),
		Now:           time.Now().UTC(),
	}
	if _, err := policyengine.EvaluateReleaseAction(policySet.ReleasePolicy, releaseReq); err != nil {
		return requestReleaseApprovalOnRequired(r, appCore, envelope, caller, err, "agent_package_version", packageVersionID, resourceAction, releaseReq)
	}
	if approved {
		if err := consumeReleaseApproval(r, appCore, envelope, caller, "agent_package_version", packageVersionID, resourceAction); err != nil {
			return nil, err
		}
	}
	switch action {
	case "canary":
		return appCore.Packages.MarkCanaryWithRule(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID, payloadInt(envelope.Payload, "canary_percent"), stringSlice(envelope.Payload["canary_scope"]))
	case "stable":
		release, err := appCore.Packages.MarkStable(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID)
		if err != nil {
			return nil, err
		}
		if _, err := appCore.Packages.EnsureAgentAssetVersionForTenant(r.Context(), caller.TenantID, release.AgentID, release.Version, caller.CallerID); err != nil {
			return nil, err
		}
		if err := appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, release.AgentID, release.Version); err != nil {
			return nil, err
		}
		return release, nil
	case "rollback":
		reason, _ := envelope.Payload["reason"].(string)
		release, err := appCore.Packages.Rollback(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID, reason)
		if err != nil {
			return nil, err
		}
		fallback := fallbackStableVersion(appCore.Packages.ListReleases(), current.TenantID, release.AgentID, release.Version)
		if err := appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, release.AgentID, fallback); err != nil {
			return nil, err
		}
		return release, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown release action", nil)
	}
}

func fallbackStableVersion(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID, rolledBack contracts.AgentVersion) contracts.AgentVersion {
	fallback := contracts.AgentVersion("v1")
	var fallbackAt time.Time
	for _, release := range releases {
		if release.TenantID != tenantID || release.AgentID != agentID || release.Version == rolledBack || release.Status != contracts.ReleaseStable {
			continue
		}
		at := release.CreatedAt
		if release.PublishedAt != nil {
			at = *release.PublishedAt
		}
		if fallbackAt.IsZero() || at.After(fallbackAt) {
			fallback = release.Version
			fallbackAt = at
		}
	}
	return fallback
}
