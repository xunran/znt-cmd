package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	toolcatalog "znt/internal/tool/catalog"
)

func exportedToolResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, toolID string) (contracts.AgentExportedTool, bool, bool, error) {
	if version == "" && strings.TrimSpace(draftID) == "" {
		if tool, found, err := appCore.Packages.GetActiveExportedToolProjection(ctx, tenantID, agentID, toolID); err != nil {
			return contracts.AgentExportedTool{}, false, false, err
		} else if found {
			return tool.Tool, true, true, nil
		}
	}
	projectionVersion := agentSubresourceProjectionVersion(ctx, appCore, tenantID, agentID, version, draftID)
	if tool, found, err := appCore.Packages.GetExportedToolProjection(ctx, tenantID, agentID, projectionVersion, draftID, toolID); err != nil {
		return contracts.AgentExportedTool{}, false, false, err
	} else if found {
		return tool.Tool, true, true, nil
	}
	agent, _, found, err := agentSubresourceDefinition(ctx, appCore, tenantID, agentID, version, draftID)
	if err != nil || !found {
		return contracts.AgentExportedTool{}, false, found, err
	}
	for _, tool := range agent.Exports.Tools {
		if tool.ToolID == toolID {
			return tool, true, true, nil
		}
	}
	return contracts.AgentExportedTool{}, false, true, nil
}

func exportedToolVersionViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) ([]map[string]any, error) {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		if projections, usedProjection, err := appCore.Packages.ListExportedToolProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			for _, projection := range projections {
				if projection.ToolID != toolID {
					continue
				}
				out = append(out, map[string]any{
					"tool":    projection.Tool,
					"version": agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
				})
				break
			}
			continue
		}
		tool, found, _, err := exportedToolResourceView(ctx, appCore, tenantID, agentID, release.Version, "", toolID)
		if err != nil {
			var runtimeErr *contracts.RuntimeError
			if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeAgentVersionNotFound {
				continue
			}
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, map[string]any{
			"tool":    tool,
			"version": agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
		})
	}
	return out, nil
}

func handleAgentExportedTools(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 1 && parts[0] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceExportedTool, r.URL.Query().Get("tool_id"))
		return
	}
	if len(parts) == 2 && parts[1] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceExportedTool, parts[0])
		return
	}
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
			draftID := r.URL.Query().Get("draft_id")
			if version == "" && strings.TrimSpace(draftID) == "" {
				if active, err := appCore.Packages.ListActiveExportedToolProjections(r.Context(), caller.TenantID, agentID); err != nil {
					writeRuntimeError(w, err)
					return
				} else if len(active) > 0 {
					agent, _, found, err := agentSubresourceDefinition(r.Context(), appCore, caller.TenantID, agentID, "", "")
					if err != nil {
						writeRuntimeError(w, err)
						return
					}
					if !found {
						tools := make([]contracts.AgentExportedTool, 0, len(active))
						for _, projection := range active {
							tools = append(tools, projection.Tool)
						}
						writeJSON(w, map[string]any{"tools": tools}, http.StatusOK)
						return
					}
					writeJSON(w, map[string]any{"tools": agent.Exports.Tools}, http.StatusOK)
					return
				}
			}
			projectionVersion := agentSubresourceProjectionVersion(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
			if projections, usedProjection, err := appCore.Packages.ListExportedToolProjections(r.Context(), caller.TenantID, agentID, projectionVersion, draftID); err != nil {
				writeRuntimeError(w, err)
				return
			} else if usedProjection {
				tools := make([]contracts.AgentExportedTool, 0, len(projections))
				for _, projection := range projections {
					tools = append(tools, projection.Tool)
				}
				writeJSON(w, map[string]any{"tools": tools}, http.StatusOK)
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
			writeJSON(w, map[string]any{"tools": agent.Exports.Tools}, http.StatusOK)
		case http.MethodPost:
			payload, ok := decodeMapPayload(w, r, "invalid exported tool json")
			if !ok {
				return
			}
			upsertAgentExportedTool(w, r, appCore, caller, agentID, payload, "")
		case http.MethodPut, http.MethodPatch:
			payload, ok := decodeMapPayload(w, r, "invalid exported tools json")
			if !ok {
				return
			}
			replaceAgentExportedTools(w, r, appCore, caller, payload)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported exported-tools method", nil), http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported exported tool versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		tools, err := exportedToolVersionViews(r.Context(), appCore, caller.TenantID, agentID, parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"exported_tool_versions": tools}, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported exported tool activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid exported tool activate json")
		if !ok {
			return
		}
		version := contracts.AgentVersion(payloadString(payload, "agent_version"))
		if version == "" {
			version = contracts.AgentVersion(payloadString(payload, "version"))
		}
		if version == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool activate requires agent_version", nil), http.StatusBadRequest)
			return
		}
		release, runtimeErr, status := stableAgentVersionRelease(appCore, caller, agentID, version)
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		tool, found, _, err := exportedToolResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "", parts[0])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool not found in target agent version", map[string]any{"tool_id": parts[0], "agent_version": release.Version}), http.StatusNotFound)
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
			"tool":    tool,
		}, http.StatusOK)
		return
	}
	if len(parts) > 1 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown exported-tools resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	toolID := parts[0]
	switch r.Method {
	case http.MethodGet:
		tool, found, resourceFound, err := exportedToolResourceView(r.Context(), appCore, caller.TenantID, agentID, contracts.AgentVersion(r.URL.Query().Get("agent_version")), r.URL.Query().Get("draft_id"), toolID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !resourceFound {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": r.URL.Query().Get("draft_id")}), http.StatusNotFound)
			return
		}
		if found {
			writeJSON(w, map[string]any{"tool": tool}, http.StatusOK)
			return
		}
		writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "exported tool not found", map[string]any{"tool_id": toolID}), http.StatusNotFound)
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid exported tool json")
		if !ok {
			return
		}
		upsertAgentExportedTool(w, r, appCore, caller, agentID, payload, toolID)
	case http.MethodDelete:
		draftID := r.URL.Query().Get("draft_id")
		if draftID == "" && r.ContentLength != 0 {
			payload, ok := decodeMapPayload(w, r, "invalid exported tool delete json")
			if !ok {
				return
			}
			draftID = payloadString(payload, "draft_id")
		}
		if draftID == "" {
			if err := appCore.Packages.DeleteActiveExportedToolProjection(r.Context(), caller.TenantID, agentID, toolID, caller.CallerID); err != nil {
				writeRuntimeError(w, err)
				return
			}
			if err := disableAgentExportedTool(r.Context(), appCore, caller.TenantID, agentID, toolID, "", caller.CallerID); err != nil {
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
				response["tools"] = agent.Exports.Tools
			}
			writeJSON(w, response, http.StatusOK)
			return
		}
		updated, err := removeAgentExportedTool(r, appCore, caller, draftID, toolID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"draft": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported exported tool method", nil), http.StatusMethodNotAllowed)
	}
}

func upsertAgentExportedTool(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, payload map[string]any, toolID string) {
	draftID := payloadString(payload, "draft_id")
	if toolID != "" {
		payload["tool_id"] = toolID
	}
	tool := parseExportedToolPayload(payload)
	if tool.ToolID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool upsert requires tool_id", nil), http.StatusBadRequest)
		return
	}
	if draftID == "" {
		version, _, err := standaloneAgentSubresourceVersion(r.Context(), appCore, caller.TenantID, agentID, payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		projection, err := appCore.Packages.UpsertExportedToolProjection(r.Context(), caller.TenantID, agentID, version, tool, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if appCore.ToolCatalog != nil {
			if err := syncAgentExportedTools(r.Context(), appCore, contracts.AgentDefinition{
				TenantID: caller.TenantID,
				AgentID:  agentID,
				Version:  version,
				Exports:  contracts.AgentExports{Tools: []contracts.AgentExportedTool{projection.Tool}},
			}, caller.CallerID); err != nil {
				writeRuntimeError(w, err)
				return
			}
		}
		writeJSON(w, map[string]any{"tool": projection.Tool}, http.StatusOK)
		return
	}
	draft, err := appCore.Packages.UpsertExportedToolForTenant(r.Context(), caller.TenantID, draftID, tool, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if err := syncDraftExportedTools(r.Context(), appCore, caller, draft); err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
}

func replaceAgentExportedTools(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, payload map[string]any) {
	draftID := payloadString(payload, "draft_id")
	if draftID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tools replace requires draft_id", nil), http.StatusBadRequest)
		return
	}
	existing, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if !ok {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID}), http.StatusBadRequest)
		return
	}
	if existing.TenantID != caller.TenantID {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil))
		return
	}
	exports := parseAgentExportsPayload(payload["exports"])
	if payload["exports"] == nil {
		exports = parseAgentExportsPayload(map[string]any{"tools": payload["tools"]})
	}
	draft, err := appCore.Packages.PatchExportsForTenant(r.Context(), caller.TenantID, draftID, exports, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		if err := disableRemovedAgentExportedTools(r.Context(), appCore, existing, compiled, caller.CallerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
}

func removeAgentExportedTool(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, draftID string, toolID string) (agentpackage.Draft, error) {
	draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return agentpackage.Draft{}, err
	}
	if !ok {
		return agentpackage.Draft{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if draft.TenantID != caller.TenantID {
		return agentpackage.Draft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	updated, err := appCore.Packages.RemoveExportedToolForTenant(r.Context(), caller.TenantID, draftID, toolID, caller.CallerID)
	if err != nil {
		return agentpackage.Draft{}, err
	}
	if appCore.ToolCatalog != nil {
		if err := disableAgentExportedTool(r.Context(), appCore, caller.TenantID, draft.AgentID, toolID, toolID, caller.CallerID); err != nil {
			return agentpackage.Draft{}, err
		}
	}
	return updated, nil
}

func syncDraftExportedTools(ctx context.Context, appCore *core.Core, caller auth.CallerIdentity, draft agentpackage.Draft) error {
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return err
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		return syncAgentExportedTools(ctx, appCore, compiled, caller.CallerID)
	}
	return nil
}

func disableAgentExportedTool(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, providerAgentID contracts.AgentID, toolID string, operation string, actorID string) error {
	if appCore.ToolCatalog == nil {
		return nil
	}
	if strings.TrimSpace(operation) == "" {
		operation = toolID
	}
	manifest := toolcatalog.ToolManifest{
		TenantID:    tenantID,
		ToolID:      toolID,
		Name:        toolID,
		Description: "disabled exported tool",
		InputSchema: map[string]any{"type": "object"},
		Executor:    toolcatalog.ExecutorSpec{Type: toolcatalog.ExecutorTypeAgentTool, ProviderID: string(providerAgentID), Operation: operation},
		Status:      toolcatalog.StatusDisabled,
	}
	if existing, ok := appCore.ToolCatalog.GetManifest(tenantID, toolID); ok {
		manifest = existing
		manifest.Status = toolcatalog.StatusDisabled
	}
	_, err := appCore.ToolCatalog.UpsertManifest(ctx, manifest, actorID)
	return err
}

func syncAgentExportedTools(ctx context.Context, appCore *core.Core, provider contracts.AgentDefinition, actorID string) error {
	for _, exported := range provider.Exports.Tools {
		if exported.Status == "disabled" {
			continue
		}
		manifest := toolcatalog.ToolManifest{
			TenantID:     provider.TenantID,
			ToolID:       exported.ToolID,
			GroupID:      exported.GroupID,
			Name:         exported.Name,
			Description:  exported.Description,
			WhenToUse:    exported.WhenToUse,
			InputSchema:  exported.InputSchema,
			OutputSchema: exported.OutputSchema,
			RiskLevel:    exported.RiskLevel,
			Visibility:   exported.Visibility,
			Status:       exported.Status,
			Version:      exported.Version,
			Executor: toolcatalog.ExecutorSpec{
				Type:       toolcatalog.ExecutorTypeAgentTool,
				ProviderID: string(provider.AgentID),
				Operation:  exported.Operation,
			},
		}
		if _, err := appCore.ToolCatalog.UpsertManifest(ctx, manifest, actorID); err != nil {
			return err
		}
	}
	return nil
}

func disableRemovedAgentExportedTools(ctx context.Context, appCore *core.Core, before agentpackage.Draft, after contracts.AgentDefinition, actorID string) error {
	current := map[string]struct{}{}
	for _, exported := range after.Exports.Tools {
		if exported.Status != "disabled" {
			current[exported.ToolID] = struct{}{}
		}
	}
	for _, exported := range before.Source.Exports.Tools {
		if exported.ToolID == "" {
			continue
		}
		if _, ok := current[exported.ToolID]; ok {
			continue
		}
		manifest := toolcatalog.ToolManifest{
			TenantID:    before.TenantID,
			ToolID:      exported.ToolID,
			Name:        exported.ToolID,
			Description: "disabled exported tool",
			InputSchema: map[string]any{"type": "object"},
			Executor:    toolcatalog.ExecutorSpec{Type: toolcatalog.ExecutorTypeAgentTool, ProviderID: string(before.AgentID), Operation: exported.Operation},
			Status:      toolcatalog.StatusDisabled,
		}
		if existing, ok := appCore.ToolCatalog.GetManifest(before.TenantID, exported.ToolID); ok {
			manifest = existing
			manifest.Status = toolcatalog.StatusDisabled
		}
		if _, err := appCore.ToolCatalog.UpsertManifest(ctx, manifest, actorID); err != nil {
			return err
		}
	}
	return nil
}
