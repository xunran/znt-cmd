package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/pkg/idgen"
)

func handleAgentSubresource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 0 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent resource path", nil), http.StatusNotFound)
		return
	}
	switch parts[0] {
	case "runtime-hooks":
		handleAgentRuntimeHooks(w, r, appCore, caller, agentID, parts[1:])
	case "drafts":
		handleAgentDrafts(w, r, appCore, caller, agentID, parts[1:])
	case "versions":
		handleAgentVersions(w, r, appCore, caller, agentID, parts[1:])
	case "prompt-profile":
		handleAgentPromptProfile(w, r, appCore, caller, agentID, parts[1:])
	case "tool-bindings":
		handleAgentToolBindings(w, r, appCore, caller, agentID, parts[1:])
	case "skills":
		handleAgentSkills(w, r, appCore, caller, agentID, parts[1:])
	case "collaborators":
		handleAgentCollaborators(w, r, appCore, caller, agentID, parts[1:])
	case "exported-tools":
		handleAgentExportedTools(w, r, appCore, caller, agentID, parts[1:])
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent resource path", map[string]any{"path": strings.Join(append([]string{string(agentID)}, parts...), "/")}), http.StatusNotFound)
	}
}

func handleAgentDrafts(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	switch {
	case len(parts) == 0:
		switch r.Method {
		case http.MethodGet:
			drafts, err := appCore.Packages.ListDrafts(r.Context(), caller.TenantID, agentID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			sort.Slice(drafts, func(i, j int) bool {
				if drafts[i].UpdatedAt.Equal(drafts[j].UpdatedAt) {
					return drafts[i].DraftID < drafts[j].DraftID
				}
				return drafts[i].UpdatedAt.After(drafts[j].UpdatedAt)
			})
			writeJSON(w, map[string]any{"drafts": drafts}, http.StatusOK)
		case http.MethodPost:
			payload, ok := decodeMapPayload(w, r, "invalid agent draft json")
			if !ok {
				return
			}
			version := contracts.AgentVersion(payloadString(payload, "version"))
			if version == "" {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft create requires version", nil), http.StatusBadRequest)
				return
			}
			draft, err := appCore.Packages.CreateDraft(r.Context(), caller.TenantID, agentID, version, agentPackageSourceFromPayload(payload), caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"draft": draft}, http.StatusCreated)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent drafts method", nil), http.StatusMethodNotAllowed)
		}
	case len(parts) == 1:
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent draft method", nil), http.StatusMethodNotAllowed)
			return
		}
		draft, ok, err := agentDraftResource(r.Context(), appCore, caller.TenantID, agentID, parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": parts[0]}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
	case len(parts) == 2:
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent draft action method", nil), http.StatusMethodNotAllowed)
			return
		}
		draft, ok, err := agentDraftResource(r.Context(), appCore, caller.TenantID, agentID, parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": parts[0]}), http.StatusNotFound)
			return
		}
		switch parts[1] {
		case "validate":
			draft, err = appCore.Packages.ValidateDraftForTenant(r.Context(), caller.TenantID, draft.DraftID, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
		case "review":
			draft, err = appCore.Packages.MarkReviewedForTenant(r.Context(), caller.TenantID, draft.DraftID, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
		case "publish":
			release, err := appCore.Packages.PublishDraftForTenant(r.Context(), caller.TenantID, draft.DraftID, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			release, err = releaseAndRegisterDraft(r, appCore, release, draft.DraftID, caller)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"version": agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release)}, http.StatusOK)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent draft action", map[string]any{"action": parts[1]}), http.StatusNotFound)
		}
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent drafts resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
	}
}

func agentDraftResource(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, draftID string) (agentpackage.Draft, bool, error) {
	draft, ok, err := appCore.Packages.GetDraft(ctx, draftID)
	if err != nil || !ok {
		return draft, ok, err
	}
	if draft.TenantID != tenantID || draft.AgentID != agentID {
		return agentpackage.Draft{}, false, nil
	}
	return draft, true, nil
}

func agentSubresourceDefinition(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (contracts.AgentDefinition, *agentpackage.Draft, bool, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID != "" {
		draft, ok, err := agentDraftResource(ctx, appCore, tenantID, agentID, draftID)
		if err != nil || !ok {
			return contracts.AgentDefinition{}, nil, ok, err
		}
		compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
		if err != nil {
			return contracts.AgentDefinition{}, nil, true, err
		}
		compiled.TenantID = tenantID
		return compiled, &draft, true, nil
	}
	agent, err := appCore.Agents.Load(ctx, tenantID, agentID, version)
	if err != nil {
		return contracts.AgentDefinition{}, nil, false, err
	}
	return agent, nil, true, nil
}

func agentSubresourceProjectionVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) contracts.AgentVersion {
	if strings.TrimSpace(draftID) != "" {
		return ""
	}
	if version != "" {
		return version
	}
	agent, err := appCore.Agents.Load(ctx, tenantID, agentID, "")
	if err != nil {
		return ""
	}
	return agent.Version
}

func promptProfileProjectionView(profile agentpackage.PromptProfileProjection) map[string]any {
	out := map[string]any{
		"agent_id":           profile.AgentID,
		"version":            profile.Version,
		"source_kind":        profile.SourceKind,
		"source_id":          profile.SourceID,
		"status":             profile.Status,
		"identity_prompt":    profile.IdentityPrompt,
		"system_prompt":      profile.SystemPrompt,
		"developer_prompt":   profile.DeveloperPrompt,
		"agents_md":          profile.AgentsMD,
		"package_version_id": profile.PackageVersionID,
	}
	if profile.DraftID != "" {
		out["draft_id"] = profile.DraftID
		delete(out, "package_version_id")
	}
	if profile.PackageVersionID == "" {
		delete(out, "package_version_id")
	}
	if profile.AgentsMD == "" {
		delete(out, "agents_md")
	}
	return out
}

func promptProfileDefinitionView(agent contracts.AgentDefinition, draft *agentpackage.Draft) map[string]any {
	profile := map[string]any{
		"agent_id":           agent.AgentID,
		"version":            agent.Version,
		"identity_prompt":    agent.IdentityPrompt,
		"system_prompt":      agent.SystemPrompt,
		"developer_prompt":   agent.DeveloperPrompt,
		"package_version_id": agent.PackageVersionID,
	}
	if draft != nil {
		profile["draft_id"] = draft.DraftID
		profile["status"] = draft.Status
		delete(profile, "package_version_id")
	}
	return profile
}

func promptProfileResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (map[string]any, bool, error) {
	if version == "" && strings.TrimSpace(draftID) == "" {
		if profile, found, err := appCore.Packages.GetActivePromptProfileProjection(ctx, tenantID, agentID); err != nil {
			return nil, false, err
		} else if found {
			return promptProfileProjectionView(profile), true, nil
		}
	}
	projectionVersion := agentSubresourceProjectionVersion(ctx, appCore, tenantID, agentID, version, draftID)
	if profile, found, err := appCore.Packages.GetPromptProfileProjection(ctx, tenantID, agentID, projectionVersion, draftID); err != nil {
		return nil, false, err
	} else if found {
		return promptProfileProjectionView(profile), true, nil
	}
	agent, draft, found, err := agentSubresourceDefinition(ctx, appCore, tenantID, agentID, version, draftID)
	if err != nil || !found {
		return nil, found, err
	}
	return promptProfileDefinitionView(agent, draft), true, nil
}

func handleAgentVersions(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	switch {
	case len(parts) == 0:
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"versions": agentVersionResourceViews(r.Context(), appCore, caller.TenantID, agentID)}, http.StatusOK)
	case len(parts) == 1:
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent version method", nil), http.StatusMethodNotAllowed)
			return
		}
		release, ok := releaseForAgentVersion(appCore.Packages.ListReleases(), caller.TenantID, agentID, contracts.AgentVersion(parts[0]))
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent package version not found", map[string]any{"agent_id": agentID, "version": parts[0]}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"version": agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release)}, http.StatusOK)
	case len(parts) == 2 && (parts[1] == "activate" || parts[1] == "restore"):
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent version "+parts[1]+" method", nil), http.StatusMethodNotAllowed)
			return
		}
		reason := ""
		if parts[1] == "restore" && r.Body != nil && r.ContentLength != 0 {
			if payload, ok := decodeMapPayload(w, r, "invalid agent version restore json"); ok {
				reason = payloadString(payload, "reason")
			} else {
				return
			}
		}
		fromVersion := contracts.AgentVersion("")
		if parts[1] == "restore" {
			asset, ok, err := appCore.Packages.GetAgentAsset(r.Context(), caller.TenantID, agentID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			if ok {
				fromVersion = asset.ActiveVersion
				if fromVersion == "" {
					fromVersion = asset.DefaultVersion
				}
			}
		}
		asset, release, runtimeErr, status, err := activateStableAgentVersion(r.Context(), appCore, caller, agentID, contracts.AgentVersion(parts[0]))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		traceID := contracts.TraceID("")
		if parts[1] == "restore" {
			traceID = recordAgentVersionRestored(r, appCore, caller, agentID, fromVersion, release, reason)
		}
		response := map[string]any{
			"agent":   agentResourceView(appCore, caller.TenantID, asset),
			"version": agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release),
		}
		if traceID != "" {
			response["meta"] = map[string]any{"trace_id": traceID}
		}
		writeJSON(w, response, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent versions resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
	}
}

func activateStableAgentVersion(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, version contracts.AgentVersion) (agentpackage.AgentAsset, contracts.AgentPackageVersion, *contracts.RuntimeError, int, error) {
	release, runtimeErr, status := stableAgentVersionRelease(appCore, caller, agentID, version)
	if runtimeErr != nil {
		return agentpackage.AgentAsset{}, contracts.AgentPackageVersion{}, runtimeErr, status, nil
	}
	asset, err := appCore.Packages.EnsureAgentAssetVersionForTenant(ctx, caller.TenantID, agentID, release.Version, caller.CallerID)
	if err != nil {
		return agentpackage.AgentAsset{}, contracts.AgentPackageVersion{}, nil, 0, err
	}
	if appCore.AgentRegistry != nil {
		if err := appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, agentID, release.Version); err != nil {
			return agentpackage.AgentAsset{}, contracts.AgentPackageVersion{}, nil, 0, err
		}
	}
	return asset, release, nil, 0, nil
}

func recordAgentVersionRestored(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, fromVersion contracts.AgentVersion, release contracts.AgentPackageVersion, reason string) contracts.TraceID {
	now := time.Now().UTC()
	if appCore.Audit != nil {
		_ = appCore.Audit.Log(r.Context(), contracts.AuditEvent{
			TenantID:     caller.TenantID,
			ActorID:      caller.CallerID,
			ActorType:    caller.CallerType,
			Action:       contracts.AuditAgentVersionRestored,
			ResourceType: "agent_package_version",
			ResourceID:   string(release.PackageVersionID),
			Decision:     "allowed",
			Reason:       reason,
			CreatedAt:    now,
		})
	}
	if appCore.Trace != nil {
		traceID := contracts.TraceID(r.URL.Query().Get("trace_id"))
		if traceID == "" {
			traceID = contracts.TraceID(idgen.New("trace"))
		}
		_ = appCore.Trace.Record(r.Context(), contracts.TraceEvent{
			TraceID:   traceID,
			TenantID:  caller.TenantID,
			SpanID:    contracts.SpanID(idgen.New("span")),
			Type:      contracts.TraceAgentVersionRestored,
			Payload:   restoreTracePayload(agentID, fromVersion, release, caller.CallerID, reason),
			CreatedAt: now,
		})
		return traceID
	}
	return ""
}

func restoreTracePayload(agentID contracts.AgentID, fromVersion contracts.AgentVersion, release contracts.AgentPackageVersion, actorID string, reason string) map[string]any {
	payload := map[string]any{
		"agent_id":           agentID,
		"from_version":       fromVersion,
		"to_version":         release.Version,
		"package_version_id": release.PackageVersionID,
		"actor_id_hash":      hashTraceSensitiveValue(actorID),
		"reason_present":     strings.TrimSpace(reason) != "",
	}
	if reasonHash := hashTraceSensitiveValue(reason); reasonHash != "" {
		payload["reason_hash"] = reasonHash
	}
	return payload
}

func hashTraceSensitiveValue(value string) string {
	return hashRouteAssignmentKey(strings.TrimSpace(value))
}

func stableAgentVersionRelease(appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentPackageVersion, *contracts.RuntimeError, int) {
	release, ok := releaseForAgentVersion(appCore.Packages.ListReleases(), caller.TenantID, agentID, version)
	if !ok {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent package version not found", map[string]any{"agent_id": agentID, "version": version}), http.StatusNotFound
	}
	if release.Status != contracts.ReleaseStable {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent package version must be stable before activation", map[string]any{
			"agent_id":           agentID,
			"agent_version":      release.Version,
			"package_version_id": release.PackageVersionID,
			"release_status":     release.Status,
		}), http.StatusBadRequest
	}
	return release, nil, 0
}

func agentVersionResourceViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) []map[string]any {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		out = append(out, agentVersionResourceView(ctx, appCore, tenantID, agentID, release))
	}
	return out
}

func sortedAgentReleases(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID) []contracts.AgentPackageVersion {
	filtered := make([]contracts.AgentPackageVersion, 0)
	for _, release := range releases {
		if release.TenantID == tenantID && release.AgentID == agentID {
			filtered = append(filtered, release)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		left := filtered[i].CreatedAt
		right := filtered[j].CreatedAt
		if filtered[i].PublishedAt != nil {
			left = *filtered[i].PublishedAt
		}
		if filtered[j].PublishedAt != nil {
			right = *filtered[j].PublishedAt
		}
		if left.Equal(right) {
			return filtered[i].Version < filtered[j].Version
		}
		return left.Before(right)
	})
	return filtered
}

func agentVersionResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, release contracts.AgentPackageVersion) map[string]any {
	view := map[string]any{
		"version": release,
		"runnable": release.Status == contracts.ReleaseCanary ||
			release.Status == contracts.ReleaseStable,
	}
	if asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID); err == nil && ok {
		view["active"] = asset.ActiveVersion == release.Version
		view["default"] = asset.DefaultVersion == release.Version
	}
	return view
}
