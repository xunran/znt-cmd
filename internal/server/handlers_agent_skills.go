package server

import (
	"context"
	"net/http"
	"sort"
	"strings"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func skillProjectionDeleted(projection agentpackage.SkillDefinitionProjection) bool {
	return projection.Status == contracts.ReleaseDeprecated || projection.Definition.Card.Status == "deleted"
}

func deletedSkillIDs(projections []agentpackage.SkillDefinitionProjection) map[string]bool {
	out := make(map[string]bool)
	for _, projection := range projections {
		if skillProjectionDeleted(projection) {
			out[projection.SkillID] = true
		}
	}
	return out
}

func skillDefinitionBySkillVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, skillVersion string) (contracts.SkillDefinition, bool, error) {
	skillID = strings.TrimSpace(skillID)
	skillVersion = strings.TrimSpace(skillVersion)
	if skillID == "" || skillVersion == "" {
		return contracts.SkillDefinition{}, false, nil
	}
	if versions, err := appCore.Packages.ListSkillDefinitionProjectionVersions(ctx, tenantID, agentID, skillID); err != nil {
		return contracts.SkillDefinition{}, false, err
	} else {
		for _, projection := range versions {
			if projection.SkillID == skillID && projection.SkillVersion == skillVersion && !skillProjectionDeleted(projection) {
				return projection.Definition, true, nil
			}
		}
	}
	for _, release := range sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID) {
		if projections, usedProjection, err := appCore.Packages.ListSkillDefinitionProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return contracts.SkillDefinition{}, false, err
		} else if usedProjection {
			for _, projection := range projections {
				if projection.SkillID == skillID && projection.SkillVersion == skillVersion && !skillProjectionDeleted(projection) {
					return projection.Definition, true, nil
				}
			}
			continue
		}
		skill, found, _, err := skillDefinitionResourceView(ctx, appCore, tenantID, agentID, release.Version, "", skillID)
		if err != nil {
			return contracts.SkillDefinition{}, false, err
		}
		if found && skill.Card.Version == skillVersion {
			return skill, true, nil
		}
	}
	return contracts.SkillDefinition{}, false, nil
}

func activateSkillDefinitionVersion(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, skillID string, skillVersion string) (agentpackage.SkillDefinitionProjection, error) {
	projection, err := appCore.Packages.ActivateSkillDefinitionProjection(ctx, caller.TenantID, agentID, skillID, skillVersion, caller.CallerID)
	if err == nil {
		return projection, nil
	}
	if !isGlobalSkillListSkippableError(err) {
		return agentpackage.SkillDefinitionProjection{}, err
	}
	definition, found, lookupErr := skillDefinitionBySkillVersion(ctx, appCore, caller.TenantID, agentID, skillID, skillVersion)
	if lookupErr != nil {
		return agentpackage.SkillDefinitionProjection{}, lookupErr
	}
	if !found {
		return agentpackage.SkillDefinitionProjection{}, err
	}
	if definition.Card.Version == "" {
		definition.Card.Version = skillVersion
	}
	version, _, versionErr := standaloneAgentSubresourceVersion(ctx, appCore, caller.TenantID, agentID, map[string]any{})
	if versionErr != nil {
		return agentpackage.SkillDefinitionProjection{}, versionErr
	}
	return appCore.Packages.UpsertSkillDefinitionProjection(ctx, caller.TenantID, agentID, version, definition, caller.CallerID)
}

func skillDefinitionResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, skillID string) (contracts.SkillDefinition, bool, bool, error) {
	if version == "" && strings.TrimSpace(draftID) == "" {
		if skill, found, err := appCore.Packages.GetActiveSkillDefinitionProjection(ctx, tenantID, agentID, skillID); err != nil {
			return contracts.SkillDefinition{}, false, false, err
		} else if found {
			if skillProjectionDeleted(skill) {
				return contracts.SkillDefinition{}, false, true, nil
			}
			return skill.Definition, true, true, nil
		}
	}
	projectionVersion := agentSubresourceProjectionVersion(ctx, appCore, tenantID, agentID, version, draftID)
	if skill, found, err := appCore.Packages.GetSkillDefinitionProjection(ctx, tenantID, agentID, projectionVersion, draftID, skillID); err != nil {
		return contracts.SkillDefinition{}, false, false, err
	} else if found {
		if skillProjectionDeleted(skill) {
			return contracts.SkillDefinition{}, false, true, nil
		}
		return skill.Definition, true, true, nil
	}
	agent, _, found, err := agentSubresourceDefinition(ctx, appCore, tenantID, agentID, version, draftID)
	if err != nil || !found {
		return contracts.SkillDefinition{}, false, found, err
	}
	for _, skill := range agent.SkillDefinitions {
		if skill.Card.SkillID == skillID {
			return skill, true, true, nil
		}
	}
	return contracts.SkillDefinition{}, false, true, nil
}

func skillDefinitionVersionViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) ([]map[string]any, error) {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	if versions, err := appCore.Packages.ListSkillDefinitionProjectionVersions(ctx, tenantID, agentID, skillID); err != nil {
		return nil, err
	} else {
		for _, projection := range versions {
			if skillProjectionDeleted(projection) {
				continue
			}
			out = append(out, skillDefinitionVersionView(ctx, appCore, tenantID, agentID, projection, projection.Status == contracts.ReleaseStable))
		}
	}
	for _, release := range releases {
		if projections, usedProjection, err := appCore.Packages.ListSkillDefinitionProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			for _, projection := range projections {
				if projection.SkillID != skillID {
					continue
				}
				if skillProjectionDeleted(projection) {
					break
				}
				row := skillDefinitionVersionView(ctx, appCore, tenantID, agentID, projection, projection.Status == contracts.ReleaseStable)
				row["agent_version"] = agentVersionResourceView(ctx, appCore, tenantID, agentID, release)
				out = append(out, row)
				break
			}
			continue
		}
		skill, found, _, err := skillDefinitionResourceView(ctx, appCore, tenantID, agentID, release.Version, "", skillID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, map[string]any{
			"skill":         skill,
			"skill_version": skill.Card.Version,
			"version":       skill.Card.Version,
			"agent_version": agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
			"active":        false,
			"default":       false,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, _ := out[i]["active"].(bool)
		right, _ := out[j]["active"].(bool)
		if left != right {
			return left
		}
		return payloadString(out[i], "skill_version") > payloadString(out[j], "skill_version")
	})
	return out, nil
}

func skillDefinitionVersionView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, projection agentpackage.SkillDefinitionProjection, active bool) map[string]any {
	return map[string]any{
		"skill":                  skillProjectionView(projection, agentNameForSkill(ctx, appCore, tenantID, agentID)),
		"skill_version":          projection.SkillVersion,
		"version":                projection.SkillVersion,
		"agent_version":          projection.Version,
		"status":                 projection.Status,
		"active":                 active,
		"default":                active,
		"updated_at":             projection.UpdatedAt.UTC(),
		"owner_agent_id":         string(projection.AgentID),
		"owner_agent_name":       agentNameForSkill(ctx, appCore, tenantID, agentID),
		"skill_definition_state": projection.Status,
	}
}

func handleAgentSkills(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 1 && parts[0] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceSkill, r.URL.Query().Get("skill_id"))
		return
	}
	if len(parts) == 2 && parts[1] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceSkill, parts[0])
		return
	}
	if len(parts) == 0 {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skills method", nil), http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodGet {
			version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
			draftID := r.URL.Query().Get("draft_id")
			if version == "" && strings.TrimSpace(draftID) == "" {
				if active, err := appCore.Packages.ListActiveSkillDefinitionProjections(r.Context(), caller.TenantID, agentID); err != nil {
					writeRuntimeError(w, err)
					return
				} else if len(active) > 0 {
					agent, _, found, err := agentSubresourceDefinition(r.Context(), appCore, caller.TenantID, agentID, "", "")
					if err != nil {
						writeRuntimeError(w, err)
						return
					}
					activeByID := make(map[string]contracts.SkillDefinition, len(active))
					for _, projection := range active {
						if skillProjectionDeleted(projection) {
							continue
						}
						activeByID[projection.SkillID] = projection.Definition
					}
					if !found {
						skills := make([]contracts.SkillDefinition, 0, len(activeByID))
						for _, skill := range activeByID {
							skills = append(skills, skill)
						}
						writeJSON(w, map[string]any{"skills": skills}, http.StatusOK)
						return
					}
					deleted := deletedSkillIDs(active)
					merged := make([]contracts.SkillDefinition, 0, len(agent.SkillDefinitions)+len(active))
					seen := make(map[string]bool, len(agent.SkillDefinitions)+len(active))
					for _, skill := range agent.SkillDefinitions {
						skillID := skill.Card.SkillID
						if deleted[skillID] {
							seen[skillID] = true
							continue
						}
						if activeSkill, ok := activeByID[skillID]; ok {
							merged = append(merged, activeSkill)
						} else {
							merged = append(merged, skill)
						}
						seen[skillID] = true
					}
					for _, projection := range active {
						if seen[projection.SkillID] {
							continue
						}
						if skillProjectionDeleted(projection) {
							continue
						}
						merged = append(merged, projection.Definition)
					}
					writeJSON(w, map[string]any{"skills": merged}, http.StatusOK)
					return
				}
			}
			projectionVersion := agentSubresourceProjectionVersion(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
			if projections, usedProjection, err := appCore.Packages.ListSkillDefinitionProjections(r.Context(), caller.TenantID, agentID, projectionVersion, draftID); err != nil {
				writeRuntimeError(w, err)
				return
			} else if usedProjection {
				skills := make([]contracts.SkillDefinition, 0, len(projections))
				for _, projection := range projections {
					if skillProjectionDeleted(projection) {
						continue
					}
					skills = append(skills, projection.Definition)
				}
				writeJSON(w, map[string]any{"skills": skills}, http.StatusOK)
				return
			}
			agent, _, found, err := agentSubresourceDefinition(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			if !found {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": draftID}), http.StatusNotFound)
				return
			}
			writeJSON(w, map[string]any{"skills": agent.SkillDefinitions}, http.StatusOK)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid skill json")
		if !ok {
			return
		}
		upsertAgentSkill(w, r, appCore, caller, agentID, payload, "")
		return
	}
	if len(parts) == 2 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skill versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		skills, err := skillDefinitionVersionViews(r.Context(), appCore, caller.TenantID, agentID, parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skill_versions": skills}, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skill activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid skill activate json")
		if !ok {
			return
		}
		skillVersion := strings.TrimSpace(payloadString(payload, "skill_version"))
		if skillVersion != "" {
			projection, err := activateSkillDefinitionVersion(r.Context(), appCore, caller, agentID, parts[0], skillVersion)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"skill": skillProjectionView(projection, agentNameForSkill(r.Context(), appCore, caller.TenantID, agentID)), "skill_version": projection.SkillVersion, "active": true, "default": true}, http.StatusOK)
			return
		}
		version := contracts.AgentVersion(payloadString(payload, "agent_version"))
		if version == "" {
			version = contracts.AgentVersion(payloadString(payload, "version"))
		}
		if version == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill activate requires agent_version", nil), http.StatusBadRequest)
			return
		}
		release, runtimeErr, status := stableAgentVersionRelease(appCore, caller, agentID, version)
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		skill, found, _, err := skillDefinitionResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "", parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill not found in target agent version", map[string]any{"skill_id": parts[0], "agent_version": release.Version}), http.StatusNotFound)
			return
		}
		asset, release, runtimeErr, status, err := activateStableAgentVersion(r.Context(), appCore, caller, agentID, release.Version)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		writeJSON(w, map[string]any{
			"agent":   agentResourceView(appCore, caller.TenantID, asset),
			"version": agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release),
			"skill":   skill,
		}, http.StatusOK)
		return
	}
	if len(parts) > 1 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown skills resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	skillID := parts[0]
	switch r.Method {
	case http.MethodGet:
		version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
		draftID := r.URL.Query().Get("draft_id")
		skill, found, resourceFound, err := skillDefinitionResourceView(r.Context(), appCore, caller.TenantID, agentID, version, draftID, skillID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !resourceFound {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": draftID}), http.StatusNotFound)
			return
		}
		if found {
			writeJSON(w, map[string]any{"skill": skill}, http.StatusOK)
			return
		}
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill not found", map[string]any{"skill_id": skillID}), http.StatusNotFound)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid skill json")
		if !ok {
			return
		}
		upsertAgentSkill(w, r, appCore, caller, agentID, payload, skillID)
	case http.MethodDelete:
		draftID := r.URL.Query().Get("draft_id")
		version := r.URL.Query().Get("version")
		if draftID == "" && r.ContentLength != 0 {
			payload, ok := decodeMapPayload(w, r, "invalid skill delete json")
			if !ok {
				return
			}
			draftID = payloadString(payload, "draft_id")
			version = payloadString(payload, "version")
		}
		if draftID == "" {
			if err := appCore.Packages.DeleteActiveSkillDefinitionProjection(r.Context(), caller.TenantID, agentID, skillID, caller.CallerID); err != nil {
				writeRuntimeError(w, err)
				return
			}
			agent, _, found, err := agentSubresourceDefinition(r.Context(), appCore, caller.TenantID, agentID, "", "")
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			response := map[string]any{"deleted": true}
			if found {
				response["skills"] = agent.SkillDefinitions
			}
			writeJSON(w, response, http.StatusOK)
			return
		}
		draft, err := appCore.Packages.RemoveSkillForTenant(r.Context(), caller.TenantID, draftID, skillID, version, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skill method", nil), http.StatusMethodNotAllowed)
	}
}

func upsertAgentSkill(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, payload map[string]any, skillID string) {
	draftID := payloadString(payload, "draft_id")
	if skillID != "" {
		payload["skill_id"] = skillID
	}
	skill, err := skillDraftInput(payload)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if draftID == "" {
		version, _, err := standaloneSkillAgentVersion(r.Context(), appCore, caller.TenantID, agentID, payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		definition, err := skillDefinitionFromDraftInput(skill)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		projection, err := appCore.Packages.UpsertSkillDefinitionProjection(r.Context(), caller.TenantID, agentID, version, definition, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skill": projection.Definition}, http.StatusOK)
		return
	}
	draft, err := appCore.Packages.UpsertSkillForTenant(r.Context(), caller.TenantID, draftID, skill, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
}
