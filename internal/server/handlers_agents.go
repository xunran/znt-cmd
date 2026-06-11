package server

import (
	"net/http"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

type agentResourcePayload struct {
	AgentID        contracts.AgentID          `json:"agent_id"`
	Name           string                     `json:"name"`
	Description    string                     `json:"description"`
	OwnerID        string                     `json:"owner_id"`
	Status         string                     `json:"status"`
	ActiveVersion  contracts.AgentVersion     `json:"active_version"`
	DefaultVersion contracts.AgentVersion     `json:"default_version"`
	Version        contracts.AgentVersion     `json:"version"`
	Prompt         string                     `json:"prompt"`
	AgentsMD       string                     `json:"agents_md"`
	ToolBindings   contracts.AgentToolsConfig `json:"tool_bindings"`
	Metadata       map[string]any             `json:"metadata"`
}

func handleAgentCreate(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	var payload agentResourcePayload
	if !decodeJSONPayload(w, r, &payload, "invalid agent json") {
		return
	}
	if payload.AgentID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent_id is required", nil), http.StatusBadRequest)
		return
	}
	asset, err := appCore.Packages.CreateAgentAssetForTenant(r.Context(), caller.TenantID, payload.AgentID, payload.Name, payload.Description, payload.OwnerID, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	var draft any
	if payload.Version != "" || payload.Prompt != "" || payload.AgentsMD != "" {
		version := payload.Version
		if version == "" {
			version = "v1"
		}
		source := agentpackage.AgentPackageSource{
			AgentsMD:     payload.AgentsMD,
			Prompt:       payload.Prompt,
			ToolBindings: payload.ToolBindings,
			Metadata:     payload.Metadata,
		}
		if source.Prompt == "" && source.AgentsMD == "" {
			source.Prompt = "You are " + string(payload.AgentID) + "."
		}
		created, err := appCore.Packages.CreateDraft(r.Context(), caller.TenantID, payload.AgentID, version, source, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		draft = created
	}
	writeJSON(w, map[string]any{"agent": agentResourceView(appCore, caller.TenantID, asset), "draft": draft}, http.StatusCreated)
}

func handleAgentList(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	assets, err := appCore.Packages.ListAgentAssets(r.Context(), caller.TenantID)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
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
	views := make([]map[string]any, 0, len(byID))
	for _, asset := range byID {
		views = append(views, agentResourceView(appCore, caller.TenantID, asset))
	}
	writeJSON(w, map[string]any{"agents": views}, http.StatusOK)
}

func handleAgentGet(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID) {
	asset, ok, err := appCore.Packages.GetAgentAsset(r.Context(), caller.TenantID, agentID)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	if !ok {
		definition, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, agentID, "")
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		asset = synthesizedAgentAsset(caller.TenantID, definition)
	}
	writeJSON(w, map[string]any{"agent": agentResourceView(appCore, caller.TenantID, asset)}, http.StatusOK)
}

func handleAgentPatch(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID) {
	var payload agentResourcePayload
	if !decodeJSONPayload(w, r, &payload, "invalid agent patch json") {
		return
	}
	patch := agentpackage.AgentAssetPatch{}
	if payload.Name != "" {
		patch.Name = &payload.Name
	}
	if payload.Description != "" {
		patch.Description = &payload.Description
	}
	if payload.OwnerID != "" {
		patch.OwnerID = &payload.OwnerID
	}
	if payload.Status != "" {
		patch.Status = &payload.Status
	}
	if payload.ActiveVersion != "" {
		patch.ActiveVersion = &payload.ActiveVersion
	}
	if payload.DefaultVersion != "" {
		patch.DefaultVersion = &payload.DefaultVersion
	}
	asset, err := appCore.Packages.PatchAgentAssetForTenant(r.Context(), caller.TenantID, agentID, patch, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"agent": agentResourceView(appCore, caller.TenantID, asset)}, http.StatusOK)
}

func handleAgentDelete(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID) {
	asset, err := appCore.Packages.DeleteAgentAssetForTenant(r.Context(), caller.TenantID, agentID, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"agent": agentResourceView(appCore, caller.TenantID, asset)}, http.StatusOK)
}

func synthesizedAgentAsset(tenantID contracts.TenantID, definition contracts.AgentDefinition) agentpackage.AgentAsset {
	now := time.Now().UTC()
	return agentpackage.AgentAsset{
		TenantID:       tenantID,
		AgentID:        definition.AgentID,
		Name:           definition.Name,
		Description:    definition.Description,
		Status:         agentpackage.AgentAssetActive,
		ActiveVersion:  definition.Version,
		DefaultVersion: definition.Version,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func agentResourceView(appCore *core.Core, tenantID contracts.TenantID, asset agentpackage.AgentAsset) map[string]any {
	releases := make([]contracts.AgentPackageVersion, 0)
	for _, release := range appCore.Packages.ListReleases() {
		if release.TenantID == tenantID && release.AgentID == asset.AgentID {
			releases = append(releases, release)
		}
	}
	return map[string]any{
		"tenant_id":       asset.TenantID,
		"agent_id":        asset.AgentID,
		"name":            asset.Name,
		"description":     asset.Description,
		"owner_id":        asset.OwnerID,
		"status":          asset.Status,
		"active_version":  asset.ActiveVersion,
		"default_version": asset.DefaultVersion,
		"created_by":      asset.CreatedBy,
		"created_at":      asset.CreatedAt,
		"updated_at":      asset.UpdatedAt,
		"deleted_at":      asset.DeletedAt,
		"releases":        releases,
	}
}
