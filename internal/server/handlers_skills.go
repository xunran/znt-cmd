package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func handleSkills(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	switch r.Method {
	case http.MethodGet:
		skills, total, limit, offset, err := globalSkillList(r.Context(), appCore, caller, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skills": skills, "total": total, "limit": limit, "offset": offset}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid skill json")
		if !ok {
			return
		}
		agentID := globalSkillOwnerAgentID(payload, r.URL.Query())
		if agentID == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill requires owner_agent_id", nil), http.StatusBadRequest)
			return
		}
		skill, err := upsertGlobalSkill(r.Context(), appCore, caller, agentID, "", payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skill": skill}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skills method", nil), http.StatusMethodNotAllowed)
	}
}

func handleSkillResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, rawPath string) {
	path := strings.Trim(rawPath, "/")
	if path == "" {
		handleSkills(w, r, appCore, caller)
		return
	}
	parts := strings.Split(path, "/")
	skillID := strings.TrimSpace(parts[0])
	if skillID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill_id is required", nil), http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skill versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		agentID, runtimeErr, status, err := resolveGlobalSkillOwner(r.Context(), appCore, caller, skillID, nil, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		versions, err := skillDefinitionVersionViews(r.Context(), appCore, caller.TenantID, agentID, skillID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skill_versions": versions}, http.StatusOK)
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
		agentID, runtimeErr, status, err := resolveGlobalSkillOwner(r.Context(), appCore, caller, skillID, payload, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		activateAgentSkillVersion(w, r, appCore, caller, agentID, skillID, payload)
		return
	}
	if len(parts) > 1 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown skill resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		agentID, runtimeErr, status, err := resolveGlobalSkillOwner(r.Context(), appCore, caller, skillID, nil, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		skill, found, _, err := skillDefinitionResourceView(r.Context(), appCore, caller.TenantID, agentID, "", "", skillID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill not found", map[string]any{"skill_id": skillID}), http.StatusNotFound)
			return
		}
		projection := agentpackage.SkillDefinitionProjection{TenantID: caller.TenantID, AgentID: agentID, SkillID: skillID, SkillVersion: skill.Card.Version, Definition: skill}
		if active, activeFound, err := appCore.Packages.GetActiveSkillDefinitionProjection(r.Context(), caller.TenantID, agentID, skillID); err != nil {
			writeRuntimeError(w, err)
			return
		} else if activeFound {
			projection = active
		}
		writeJSON(w, map[string]any{"skill": skillProjectionView(projection, agentNameForSkill(r.Context(), appCore, caller.TenantID, agentID))}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid skill json")
		if !ok {
			return
		}
		agentID, runtimeErr, status, err := resolveGlobalSkillOwner(r.Context(), appCore, caller, skillID, payload, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		skill, err := upsertGlobalSkill(r.Context(), appCore, caller, agentID, skillID, payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"skill": skill}, http.StatusOK)
	case http.MethodDelete:
		agentID, runtimeErr, status, err := resolveGlobalSkillOwner(r.Context(), appCore, caller, skillID, nil, r.URL.Query())
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		if err := appCore.Packages.DeleteActiveSkillDefinitionProjection(r.Context(), caller.TenantID, agentID, skillID, caller.CallerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported skill method", nil), http.StatusMethodNotAllowed)
	}
}

func globalSkillList(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, query url.Values) ([]map[string]any, int, int, int, error) {
	agentIDFilter := contracts.AgentID(strings.TrimSpace(query.Get("owner_agent_id")))
	if agentIDFilter == "" {
		agentIDFilter = contracts.AgentID(strings.TrimSpace(query.Get("agent_id")))
	}
	assets, err := appCore.Packages.ListAgentAssets(ctx, caller.TenantID)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	byID := map[contracts.AgentID]agentpackage.AgentAsset{}
	for _, asset := range assets {
		byID[asset.AgentID] = asset
	}
	for _, definition := range appCore.AgentRegistry.ListByTenant(caller.TenantID) {
		if _, ok := byID[definition.AgentID]; ok {
			continue
		}
		byID[definition.AgentID] = synthesizedAgentAsset(caller.TenantID, definition)
	}

	rows := make([]map[string]any, 0)
	for _, asset := range byID {
		if agentIDFilter != "" && asset.AgentID != agentIDFilter {
			continue
		}
		agentName := agentNameFromAsset(asset)
		versions, err := appCore.Packages.ListSkillDefinitionProjectionVersions(ctx, caller.TenantID, asset.AgentID, "")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		deleted := deletedSkillIDs(versions)
		activeSeen := map[string]bool{}
		if active, err := appCore.Packages.ListActiveSkillDefinitionProjections(ctx, caller.TenantID, asset.AgentID); err != nil {
			return nil, 0, 0, 0, err
		} else if len(active) > 0 || len(deleted) > 0 {
			for _, projection := range active {
				if skillProjectionDeleted(projection) {
					continue
				}
				activeSeen[projection.SkillID] = true
				rows = append(rows, skillProjectionView(projection, agentName))
			}
		}
		skills, found, err := skillDefinitionListResourceView(ctx, appCore, caller.TenantID, asset.AgentID, "", "")
		if err != nil {
			if isGlobalSkillListSkippableError(err) {
				continue
			}
			return nil, 0, 0, 0, err
		}
		if !found {
			continue
		}
		for _, skill := range skills {
			if deleted[skill.Card.SkillID] {
				continue
			}
			if activeSeen[skill.Card.SkillID] {
				continue
			}
			rows = append(rows, skillDefinitionView(skill, asset.AgentID, agentName, asset.UpdatedAt))
		}
	}

	q := strings.ToLower(strings.TrimSpace(query.Get("q")))
	status := strings.TrimSpace(query.Get("status"))
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if q != "" {
			text := strings.ToLower(skillListString(row, "name") + " " + skillListString(row, "skill_id") + " " + skillListString(row, "description"))
			if !strings.Contains(text, q) {
				continue
			}
		}
		if status != "" && status != "all" && skillListString(row, "status") != status {
			continue
		}
		filtered = append(filtered, row)
	}
	sortBy := strings.TrimSpace(query.Get("sort"))
	sort.SliceStable(filtered, func(i, j int) bool {
		switch sortBy {
		case "name":
			return skillListString(filtered[i], "name") < skillListString(filtered[j], "name")
		default:
			return skillListTime(filtered[i]).After(skillListTime(filtered[j]))
		}
	})

	total := len(filtered)
	limit := intQuery(query.Get("limit"), total)
	offset := intQuery(query.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = total
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return filtered[offset:end], total, limit, offset, nil
}

func globalSkillOwnerAgentID(payload map[string]any, query url.Values) contracts.AgentID {
	for _, value := range []string{
		payloadString(payload, "owner_agent_id"),
		payloadString(payload, "agent_id"),
		payloadString(payload, "agentId"),
		query.Get("owner_agent_id"),
		query.Get("agent_id"),
		query.Get("agentId"),
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return contracts.AgentID(trimmed)
		}
	}
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		if owner := strings.TrimSpace(payloadString(metadata, "owner_agent_id")); owner != "" {
			return contracts.AgentID(owner)
		}
	}
	return ""
}

func resolveGlobalSkillOwner(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, skillID string, payload map[string]any, query url.Values) (contracts.AgentID, *contracts.RuntimeError, int, error) {
	if owner := globalSkillOwnerAgentID(payload, query); owner != "" {
		return owner, nil, 0, nil
	}
	assets, err := appCore.Packages.ListAgentAssets(ctx, caller.TenantID)
	if err != nil {
		return "", nil, 0, err
	}
	for _, asset := range assets {
		if _, found, _, err := skillDefinitionResourceView(ctx, appCore, caller.TenantID, asset.AgentID, "", "", skillID); err != nil {
			if isGlobalSkillListSkippableError(err) {
				continue
			}
			return "", nil, 0, err
		} else if found {
			return asset.AgentID, nil, 0, nil
		}
	}
	for _, definition := range appCore.AgentRegistry.ListByTenant(caller.TenantID) {
		if _, found, _, err := skillDefinitionResourceView(ctx, appCore, caller.TenantID, definition.AgentID, "", "", skillID); err != nil {
			if isGlobalSkillListSkippableError(err) {
				continue
			}
			return "", nil, 0, err
		} else if found {
			return definition.AgentID, nil, 0, nil
		}
	}
	return "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill not found", map[string]any{"skill_id": skillID}), http.StatusNotFound, nil
}

func upsertGlobalSkill(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, skillID string, payload map[string]any) (map[string]any, error) {
	if skillID != "" {
		payload["skill_id"] = skillID
	}
	if payloadString(payload, "draft_id") != "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "global skill endpoint does not accept draft_id", nil)
	}
	skill, err := skillDraftInput(payload)
	if err != nil {
		return nil, err
	}
	version, _, err := standaloneSkillAgentVersion(ctx, appCore, caller.TenantID, agentID, payload)
	if err != nil {
		return nil, err
	}
	definition, err := skillDefinitionFromDraftInput(skill)
	if err != nil {
		return nil, err
	}
	if shouldCreateSkillVersion(payload) && skillPayloadVersion(payload) == "" {
		definition.Card.Version = nextSkillDefinitionVersion(ctx, appCore, caller.TenantID, agentID, definition.Card.SkillID)
	}
	projection, err := appCore.Packages.UpsertSkillDefinitionProjection(ctx, caller.TenantID, agentID, version, definition, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return skillProjectionView(projection, agentNameForSkill(ctx, appCore, caller.TenantID, agentID)), nil
}

func standaloneSkillAgentVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, payload map[string]any) (contracts.AgentVersion, bool, error) {
	if agentVersion := strings.TrimSpace(payloadString(payload, "agent_version")); agentVersion != "" {
		return contracts.AgentVersion(agentVersion), true, nil
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	delete(cloned, "version")
	delete(cloned, "skill_version")
	return standaloneAgentSubresourceVersion(ctx, appCore, tenantID, agentID, cloned)
}

func skillPayloadVersion(payload map[string]any) string {
	if version := strings.TrimSpace(payloadString(payload, "skill_version")); version != "" {
		return version
	}
	return strings.TrimSpace(payloadString(payload, "version"))
}

func shouldCreateSkillVersion(payload map[string]any) bool {
	action := strings.TrimSpace(payloadString(payload, "action"))
	if action == "update_version" || action == "publish_version" {
		return true
	}
	if payloadBool(payload, "update_version") || payloadBool(payload, "publish_version") {
		return true
	}
	return false
}

func nextSkillDefinitionVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) string {
	versions, err := appCore.Packages.ListSkillDefinitionProjectionVersions(ctx, tenantID, agentID, skillID)
	if err != nil || len(versions) == 0 {
		return "v1"
	}
	maxVersion := 0
	for _, version := range versions {
		raw := strings.TrimPrefix(strings.TrimSpace(version.SkillVersion), "v")
		if parsed := intQuery(raw, 0); parsed > maxVersion {
			maxVersion = parsed
		}
	}
	return "v" + strconv.Itoa(maxVersion+1)
}

func activateAgentSkillVersion(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, skillID string, payload map[string]any) {
	skillVersion := strings.TrimSpace(payloadString(payload, "skill_version"))
	if skillVersion == "" {
		skillVersion = strings.TrimSpace(r.URL.Query().Get("skill_version"))
	}
	if skillVersion != "" {
		projection, err := activateSkillDefinitionVersion(r.Context(), appCore, caller, agentID, skillID, skillVersion)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"skill":          skillProjectionView(projection, agentNameForSkill(r.Context(), appCore, caller.TenantID, agentID)),
			"skill_version":  projection.SkillVersion,
			"owner_agent_id": string(agentID),
			"active":         true,
			"default":        true,
		}, http.StatusOK)
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
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill activate requires agent_version", nil), http.StatusBadRequest)
		return
	}
	release, runtimeErr, status := stableAgentVersionRelease(appCore, caller, agentID, version)
	if runtimeErr != nil {
		writeError(w, runtimeErr, status)
		return
	}
	skill, found, _, err := skillDefinitionResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "", skillID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if !found {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill not found in target agent version", map[string]any{"skill_id": skillID, "agent_version": release.Version}), http.StatusNotFound)
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
		"skill":   skillDefinitionView(skill, agentID, agentNameFromAsset(asset), time.Now().UTC()),
	}, http.StatusOK)
}

func skillProjectionView(projection agentpackage.SkillDefinitionProjection, agentName string) map[string]any {
	return skillDefinitionView(projection.Definition, projection.AgentID, agentName, projection.UpdatedAt)
}

func skillDefinitionView(skill contracts.SkillDefinition, agentID contracts.AgentID, agentName string, updatedAt time.Time) map[string]any {
	card := map[string]any{
		"skill_id":         skill.Card.SkillID,
		"version":          skill.Card.Version,
		"name":             skill.Card.Name,
		"description":      skill.Card.Description,
		"tags":             skill.Card.Tags,
		"status":           skill.Card.Status,
		"when_to_use":      skill.Card.WhenToUse,
		"risk_level":       skill.Card.RiskLevel,
		"resource_refs":    skill.Card.ResourceRefs,
		"owner_agent_id":   string(agentID),
		"owner_agent_name": agentName,
	}
	if !updatedAt.IsZero() {
		card["updated_at"] = updatedAt.UTC()
	}
	return map[string]any{
		"skill_id":               skill.Card.SkillID,
		"version":                skill.Card.Version,
		"name":                   skill.Card.Name,
		"description":            skill.Card.Description,
		"status":                 skill.Card.Status,
		"instruction_text":       skill.Instruction.Content,
		"owner_agent_id":         string(agentID),
		"owner_agent_name":       agentName,
		"agent_refs":             []string{string(agentID)},
		"updated_at":             updatedAt.UTC(),
		"skill_updated_at":       updatedAt.UTC(),
		"card":                   card,
		"instruction_resource":   skill.Instruction,
		"instruction_definition": skill.Instruction,
		"instruction": map[string]any{
			"skill_id":            skill.Instruction.SkillID,
			"content":             skill.Instruction.Content,
			"text":                skill.Instruction.Content,
			"summary":             skill.Instruction.Content,
			"output_requirements": skill.Instruction.OutputRequirements,
			"constraints":         skill.Instruction.Constraints,
		},
		"resources":                 skill.Resources,
		"recommended_tools":         skill.RecommendedTools,
		"allowed_tools":             skill.AllowedTools,
		"recommended_memory_reads":  skill.RecommendedMemoryReads,
		"recommended_memory_writes": skill.RecommendedMemoryWrites,
		"recommended_handoffs":      skill.RecommendedHandoffs,
		"completion_criteria":       skill.CompletionCriteria,
		"output_schema":             skill.OutputSchema,
	}
}

func agentNameForSkill(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) string {
	if asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID); err == nil && ok {
		return agentNameFromAsset(asset)
	}
	return string(agentID)
}

func agentNameFromAsset(asset agentpackage.AgentAsset) string {
	if strings.TrimSpace(asset.Name) != "" {
		return asset.Name
	}
	return string(asset.AgentID)
}

func skillListString(view map[string]any, key string) string {
	if value, ok := view[key].(string); ok {
		return value
	}
	if card, ok := view["card"].(map[string]any); ok {
		if value, ok := card[key].(string); ok {
			return value
		}
	}
	return ""
}

func skillListTime(view map[string]any) time.Time {
	for _, key := range []string{"skill_updated_at", "updated_at"} {
		switch value := view[key].(type) {
		case time.Time:
			return value
		case string:
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func isGlobalSkillListSkippableError(err error) bool {
	var runtimeErr *contracts.RuntimeError
	return errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeAgentVersionNotFound
}
