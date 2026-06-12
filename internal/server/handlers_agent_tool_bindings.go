package server

import (
	"context"
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func toolBindingResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (contracts.AgentToolsConfig, bool, error) {
	if version == "" && strings.TrimSpace(draftID) == "" {
		if binding, found, err := appCore.Packages.GetActiveToolBindingProjection(ctx, tenantID, agentID); err != nil {
			return contracts.AgentToolsConfig{}, false, err
		} else if found {
			return binding.Bindings, true, nil
		}
	}
	projectionVersion := agentSubresourceProjectionVersion(ctx, appCore, tenantID, agentID, version, draftID)
	if binding, found, err := appCore.Packages.GetToolBindingProjection(ctx, tenantID, agentID, projectionVersion, draftID); err != nil {
		return contracts.AgentToolsConfig{}, false, err
	} else if found {
		return binding.Bindings, true, nil
	}
	agent, _, found, err := agentSubresourceDefinition(ctx, appCore, tenantID, agentID, version, draftID)
	if err != nil || !found {
		return contracts.AgentToolsConfig{}, found, err
	}
	return agent.Tools, true, nil
}

func toolBindingVersionViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) ([]map[string]any, error) {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		bindings, found, err := toolBindingResourceView(ctx, appCore, tenantID, agentID, release.Version, "")
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, map[string]any{
			"tool_bindings": bindings,
			"version":       agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
		})
	}
	return out, nil
}

func handleAgentToolBindings(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 1 && parts[0] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceToolBinding, "")
		return
	}
	if len(parts) == 1 && parts[0] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool-bindings versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		bindings, err := toolBindingVersionViews(r.Context(), appCore, caller.TenantID, agentID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tool_binding_versions": bindings}, http.StatusOK)
		return
	}
	if len(parts) == 1 && parts[0] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool-bindings activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid tool bindings activate json")
		if !ok {
			return
		}
		version := contracts.AgentVersion(payloadString(payload, "agent_version"))
		if version == "" {
			version = contracts.AgentVersion(payloadString(payload, "version"))
		}
		if version == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool-bindings activate requires agent_version", nil), http.StatusBadRequest)
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
		bindings, found, err := toolBindingResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "")
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "tool bindings version not found", map[string]any{"agent_id": agentID, "agent_version": release.Version}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"agent":         agentResourceView(appCore, caller.TenantID, asset),
			"version":       agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release),
			"tool_bindings": bindings,
		}, http.StatusOK)
		return
	}
	if len(parts) > 0 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown tool-bindings resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
		draftID := r.URL.Query().Get("draft_id")
		bindings, found, err := toolBindingResourceView(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": draftID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"tool_bindings": bindings}, http.StatusOK)
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool bindings json")
		if !ok {
			return
		}
		draftID := payloadString(payload, "draft_id")
		bindings := parseToolsPayload(payload["tool_bindings"])
		if payload["tool_bindings"] == nil {
			bindings = parseToolsConfig(payload)
		}
		if draftID == "" {
			version, _, err := standaloneAgentSubresourceVersion(r.Context(), appCore, caller.TenantID, agentID, payload)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			projection, err := appCore.Packages.UpsertToolBindingProjection(r.Context(), caller.TenantID, agentID, version, bindings, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"tool_bindings": projection.Bindings}, http.StatusOK)
			return
		}
		draft, err := appCore.Packages.UpdateToolBindingForTenant(r.Context(), caller.TenantID, draftID, bindings, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
	case http.MethodDelete:
		if err := appCore.Packages.DeleteActiveToolBindingProjection(r.Context(), caller.TenantID, agentID, caller.CallerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		response := map[string]any{"deleted": true}
		if bindings, found, err := toolBindingResourceView(r.Context(), appCore, caller.TenantID, agentID, contracts.AgentVersion(r.URL.Query().Get("agent_version")), ""); err != nil {
			writeRuntimeError(w, err)
			return
		} else if found {
			response["tool_bindings"] = bindings
		}
		writeJSON(w, response, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool-bindings method", nil), http.StatusMethodNotAllowed)
	}
}
