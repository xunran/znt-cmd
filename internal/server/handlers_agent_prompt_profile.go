package server

import (
	"context"
	"net/http"
	"strings"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func handleAgentPromptProfile(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 1 && parts[0] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourcePromptProfile, "")
		return
	}
	if len(parts) == 1 && parts[0] == "preview" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported prompt-profile preview method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid prompt profile preview json")
		if !ok {
			return
		}
		payload["agent_id"] = string(agentID)
		envelope := contracts.AgentEnvelope{TraceID: contracts.TraceID(payloadString(payload, "trace_id")), Target: contracts.AgentTarget{AgentID: agentID, Version: contracts.AgentVersion(payloadString(payload, "agent_version"))}, Payload: payload}
		result, err := promptPreview(r, appCore, envelope, caller)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, result, http.StatusOK)
		return
	}
	if len(parts) == 1 && parts[0] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported prompt-profile versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		profiles, err := promptProfileVersionViews(r.Context(), appCore, caller.TenantID, agentID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"prompt_profiles": profiles}, http.StatusOK)
		return
	}
	if len(parts) == 1 && parts[0] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported prompt-profile activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid prompt profile activate json")
		if !ok {
			return
		}
		version := contracts.AgentVersion(payloadString(payload, "agent_version"))
		if version == "" {
			version = contracts.AgentVersion(payloadString(payload, "version"))
		}
		if version == "" {
			version = contracts.AgentVersion(r.URL.Query().Get("agent_version"))
		}
		if version == "" {
			version = contracts.AgentVersion(r.URL.Query().Get("version"))
		}
		if version == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt-profile activate requires agent_version", nil), http.StatusBadRequest)
			return
		}
		asset, release, runtimeErr, status, err := activateRunnableAgentVersion(r.Context(), appCore, caller, agentID, version)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		profile, found, err := promptProfileResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "")
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "prompt profile version not found", map[string]any{"agent_id": agentID, "agent_version": release.Version}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"agent":          agentResourceView(appCore, caller.TenantID, asset),
			"version":        agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release),
			"prompt_profile": profile,
		}, http.StatusOK)
		return
	}
	if len(parts) > 0 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown prompt-profile resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
		draftID := r.URL.Query().Get("draft_id")
		profile, found, err := promptProfileResourceView(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": draftID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"prompt_profile": profile}, http.StatusOK)
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid prompt profile json")
		if !ok {
			return
		}
		draftID := payloadString(payload, "draft_id")
		if draftID != "" {
			draft, err := patchPromptProfileDraft(r.Context(), appCore, caller, draftID, payload)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
			return
		}
		profile, err := upsertStandalonePromptProfile(r.Context(), appCore, caller, agentID, payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"prompt_profile": promptProfileProjectionView(profile)}, http.StatusOK)
	case http.MethodDelete:
		if err := appCore.Packages.DeleteActivePromptProfileProjection(r.Context(), caller.TenantID, agentID, caller.CallerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		response := map[string]any{"deleted": true}
		if profile, found, err := promptProfileResourceView(r.Context(), appCore, caller.TenantID, agentID, contracts.AgentVersion(r.URL.Query().Get("agent_version")), ""); err != nil {
			writeRuntimeError(w, err)
			return
		} else if found {
			response["prompt_profile"] = profile
		}
		writeJSON(w, response, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported prompt-profile method", nil), http.StatusMethodNotAllowed)
	}
}

func promptProfileVersionViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) ([]map[string]any, error) {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		profile, found, err := promptProfileResourceView(ctx, appCore, tenantID, agentID, release.Version, "")
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, map[string]any{
			"prompt_profile": profile,
			"version":        agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
		})
	}
	return out, nil
}

func patchPromptProfileDraft(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, draftID string, payload map[string]any) (agentpackage.Draft, error) {
	var draft agentpackage.Draft
	var err error
	changed := false
	if _, ok := payload["prompt"]; ok {
		draft, err = appCore.Packages.PatchPromptForTenant(ctx, caller.TenantID, draftID, payloadString(payload, "prompt"), caller.CallerID)
		if err != nil {
			return agentpackage.Draft{}, err
		}
		changed = true
	}
	if _, ok := payload["system_prompt"]; ok {
		draft, err = appCore.Packages.PatchSystemPromptForTenant(ctx, caller.TenantID, draftID, payloadString(payload, "system_prompt"), caller.CallerID)
		if err != nil {
			return agentpackage.Draft{}, err
		}
		changed = true
	}
	if _, ok := payload["developer_prompt"]; ok {
		draft, err = appCore.Packages.PatchDeveloperPromptForTenant(ctx, caller.TenantID, draftID, payloadString(payload, "developer_prompt"), caller.CallerID)
		if err != nil {
			return agentpackage.Draft{}, err
		}
		changed = true
	}
	if _, ok := payload["agents_md"]; ok {
		draft, err = appCore.Packages.PatchAgentsMDForTenant(ctx, caller.TenantID, draftID, payloadString(payload, "agents_md"), caller.CallerID)
		if err != nil {
			return agentpackage.Draft{}, err
		}
		changed = true
	}
	if !changed {
		existing, ok, err := appCore.Packages.GetDraft(ctx, draftID)
		if err != nil {
			return agentpackage.Draft{}, err
		}
		if !ok {
			return agentpackage.Draft{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
		}
		if existing.TenantID != caller.TenantID {
			return agentpackage.Draft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
		}
		return existing, nil
	}
	return draft, nil
}

func upsertStandalonePromptProfile(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, payload map[string]any) (agentpackage.PromptProfileProjection, error) {
	version, explicitVersion, err := standaloneAgentSubresourceVersion(ctx, appCore, caller.TenantID, agentID, payload)
	if err != nil {
		return agentpackage.PromptProfileProjection{}, err
	}
	identityPrompt, systemPrompt, developerPrompt, agentsMD := basePromptProfileFields(ctx, appCore, caller.TenantID, agentID, version)
	if existing, found, err := appCore.Packages.GetActivePromptProfileProjection(ctx, caller.TenantID, agentID); err != nil {
		return agentpackage.PromptProfileProjection{}, err
	} else if found && (!explicitVersion || existing.Version == version) {
		if version == "" {
			version = existing.Version
		}
		identityPrompt = existing.IdentityPrompt
		systemPrompt = existing.SystemPrompt
		developerPrompt = existing.DeveloperPrompt
		agentsMD = existing.AgentsMD
	}
	if _, ok := payload["prompt"]; ok {
		identityPrompt = payloadString(payload, "prompt")
	}
	if _, ok := payload["identity_prompt"]; ok {
		identityPrompt = payloadString(payload, "identity_prompt")
	}
	if _, ok := payload["system_prompt"]; ok {
		systemPrompt = payloadString(payload, "system_prompt")
	}
	if _, ok := payload["developer_prompt"]; ok {
		developerPrompt = payloadString(payload, "developer_prompt")
	}
	if _, ok := payload["agents_md"]; ok {
		agentsMD = payloadString(payload, "agents_md")
	}
	return appCore.Packages.UpsertPromptProfileProjection(ctx, caller.TenantID, agentID, version, identityPrompt, systemPrompt, developerPrompt, agentsMD, caller.CallerID)
}

func basePromptProfileFields(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (string, string, string, string) {
	if appCore.AgentRegistry != nil {
		if definition, err := appCore.AgentRegistry.Load(ctx, tenantID, agentID, version); err == nil {
			return definition.IdentityPrompt, definition.SystemPrompt, definition.DeveloperPrompt, ""
		}
	}
	if appCore.Agents != nil {
		if definition, err := appCore.Agents.Load(ctx, tenantID, agentID, version); err == nil {
			return definition.IdentityPrompt, definition.SystemPrompt, definition.DeveloperPrompt, ""
		}
	}
	return "", "", "", ""
}
