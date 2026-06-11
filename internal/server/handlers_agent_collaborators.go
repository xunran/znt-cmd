package server

import (
	"context"
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func collaboratorResourceView(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, collaboratorAgentID contracts.AgentID) (contracts.AgentCollaboratorRef, bool, bool, error) {
	if version == "" && strings.TrimSpace(draftID) == "" {
		if collaborator, found, err := appCore.Packages.GetActiveCollaboratorProjection(ctx, tenantID, agentID, collaboratorAgentID); err != nil {
			return contracts.AgentCollaboratorRef{}, false, false, err
		} else if found {
			return collaborator.Collaborator, true, true, nil
		}
	}
	projectionVersion := agentSubresourceProjectionVersion(ctx, appCore, tenantID, agentID, version, draftID)
	if collaborator, found, err := appCore.Packages.GetCollaboratorProjection(ctx, tenantID, agentID, projectionVersion, draftID, collaboratorAgentID); err != nil {
		return contracts.AgentCollaboratorRef{}, false, false, err
	} else if found {
		return collaborator.Collaborator, true, true, nil
	}
	agent, _, found, err := agentSubresourceDefinition(ctx, appCore, tenantID, agentID, version, draftID)
	if err != nil || !found {
		return contracts.AgentCollaboratorRef{}, false, found, err
	}
	for _, collaborator := range agent.Collaborators {
		if collaborator.AgentID == collaboratorAgentID {
			return collaborator, true, true, nil
		}
	}
	return contracts.AgentCollaboratorRef{}, false, true, nil
}

func collaboratorVersionViews(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) ([]map[string]any, error) {
	releases := sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID)
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		if projections, usedProjection, err := appCore.Packages.ListCollaboratorProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			for _, projection := range projections {
				if projection.CollaboratorAgentID != collaboratorAgentID {
					continue
				}
				out = append(out, map[string]any{
					"collaborator": projection.Collaborator,
					"version":      agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
				})
				break
			}
			continue
		}
		collaborator, found, _, err := collaboratorResourceView(ctx, appCore, tenantID, agentID, release.Version, "", collaboratorAgentID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, map[string]any{
			"collaborator": collaborator,
			"version":      agentVersionResourceView(ctx, appCore, tenantID, agentID, release),
		})
	}
	return out, nil
}

func handleAgentCollaborators(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if len(parts) == 1 && parts[0] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceCollaborator, r.URL.Query().Get("collaborator_agent_id"))
		return
	}
	if len(parts) == 2 && parts[1] == "governance" {
		handleAgentSubresourceGovernance(w, r, appCore, caller, agentID, agentSubresourceCollaborator, parts[0])
		return
	}
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
			draftID := r.URL.Query().Get("draft_id")
			if version == "" && strings.TrimSpace(draftID) == "" {
				if active, err := appCore.Packages.ListActiveCollaboratorProjections(r.Context(), caller.TenantID, agentID); err != nil {
					writeRuntimeError(w, err)
					return
				} else if len(active) > 0 {
					agent, _, found, err := agentSubresourceDefinition(r.Context(), appCore, caller.TenantID, agentID, "", "")
					if err != nil {
						writeRuntimeError(w, err)
						return
					}
					if !found {
						collaborators := make([]contracts.AgentCollaboratorRef, 0, len(active))
						for _, projection := range active {
							collaborators = append(collaborators, projection.Collaborator)
						}
						writeJSON(w, map[string]any{"collaborators": collaborators}, http.StatusOK)
						return
					}
					writeJSON(w, map[string]any{"collaborators": agent.Collaborators}, http.StatusOK)
					return
				}
			}
			projectionVersion := agentSubresourceProjectionVersion(r.Context(), appCore, caller.TenantID, agentID, version, draftID)
			if projections, usedProjection, err := appCore.Packages.ListCollaboratorProjections(r.Context(), caller.TenantID, agentID, projectionVersion, draftID); err != nil {
				writeRuntimeError(w, err)
				return
			} else if usedProjection {
				collaborators := make([]contracts.AgentCollaboratorRef, 0, len(projections))
				for _, projection := range projections {
					collaborators = append(collaborators, projection.Collaborator)
				}
				writeJSON(w, map[string]any{"collaborators": collaborators}, http.StatusOK)
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
			writeJSON(w, map[string]any{"collaborators": agent.Collaborators}, http.StatusOK)
		case http.MethodPost:
			payload, ok := decodeMapPayload(w, r, "invalid collaborator json")
			if !ok {
				return
			}
			upsertAgentCollaborator(w, r, appCore, caller, agentID, payload, "")
		case http.MethodPut, http.MethodPatch:
			payload, ok := decodeMapPayload(w, r, "invalid collaborators json")
			if !ok {
				return
			}
			draftID := payloadString(payload, "draft_id")
			if draftID == "" {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborators replace requires draft_id", nil), http.StatusBadRequest)
				return
			}
			collaborators := parseCollaboratorsPayload(payload["collaborators"])
			for _, collaborator := range collaborators {
				if collaborator.AgentID == "" {
					writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator agent_id is required", nil), http.StatusBadRequest)
					return
				}
				if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
					writeRuntimeError(w, err)
					return
				}
			}
			draft, err := appCore.Packages.PatchCollaboratorsForTenant(r.Context(), caller.TenantID, draftID, collaborators, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported collaborators method", nil), http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported collaborator versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		collaborators, err := collaboratorVersionViews(r.Context(), appCore, caller.TenantID, agentID, contracts.AgentID(parts[0]))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"collaborator_versions": collaborators}, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported collaborator activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid collaborator activate json")
		if !ok {
			return
		}
		version := contracts.AgentVersion(payloadString(payload, "agent_version"))
		if version == "" {
			version = contracts.AgentVersion(payloadString(payload, "version"))
		}
		if version == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator activate requires agent_version", nil), http.StatusBadRequest)
			return
		}
		release, runtimeErr, status := stableAgentVersionRelease(appCore, caller, agentID, version)
		if runtimeErr != nil {
			writeError(w, runtimeErr, status)
			return
		}
		collaborator, found, _, err := collaboratorResourceView(r.Context(), appCore, caller.TenantID, agentID, release.Version, "", contracts.AgentID(parts[0]))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !found {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator not found in target agent version", map[string]any{"collaborator_agent_id": parts[0], "agent_version": release.Version}), http.StatusNotFound)
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
			"agent":        agentResourceView(appCore, caller.TenantID, asset),
			"version":      agentVersionResourceView(r.Context(), appCore, caller.TenantID, agentID, release),
			"collaborator": collaborator,
		}, http.StatusOK)
		return
	}
	if len(parts) > 1 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown collaborators resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	collaboratorID := parts[0]
	switch r.Method {
	case http.MethodGet:
		version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
		draftID := r.URL.Query().Get("draft_id")
		collaborator, found, resourceFound, err := collaboratorResourceView(r.Context(), appCore, caller.TenantID, agentID, version, draftID, contracts.AgentID(collaboratorID))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !resourceFound {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent draft not found", map[string]any{"draft_id": draftID}), http.StatusNotFound)
			return
		}
		if found {
			writeJSON(w, map[string]any{"collaborator": collaborator}, http.StatusOK)
			return
		}
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator not found", map[string]any{"agent_id": collaboratorID}), http.StatusNotFound)
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid collaborator json")
		if !ok {
			return
		}
		upsertAgentCollaborator(w, r, appCore, caller, agentID, payload, collaboratorID)
	case http.MethodDelete:
		draftID := r.URL.Query().Get("draft_id")
		if draftID == "" && r.ContentLength != 0 {
			payload, ok := decodeMapPayload(w, r, "invalid collaborator delete json")
			if !ok {
				return
			}
			draftID = payloadString(payload, "draft_id")
		}
		if draftID == "" {
			if err := appCore.Packages.DeleteActiveCollaboratorProjection(r.Context(), caller.TenantID, agentID, contracts.AgentID(collaboratorID), caller.CallerID); err != nil {
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
				response["collaborators"] = agent.Collaborators
			}
			writeJSON(w, response, http.StatusOK)
			return
		}
		draft, err := appCore.Packages.RemoveCollaboratorForTenant(r.Context(), caller.TenantID, draftID, contracts.AgentID(collaboratorID), caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported collaborator method", nil), http.StatusMethodNotAllowed)
	}
}

func upsertAgentCollaborator(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, payload map[string]any, collaboratorID string) {
	draftID := payloadString(payload, "draft_id")
	if collaboratorID != "" {
		payload["agent_id"] = collaboratorID
	}
	collaborator := parseCollaboratorPayload(payload)
	if collaborator.AgentID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator upsert requires agent_id", nil), http.StatusBadRequest)
		return
	}
	if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
		writeRuntimeError(w, err)
		return
	}
	if draftID == "" {
		version, _, err := standaloneAgentSubresourceVersion(r.Context(), appCore, caller.TenantID, agentID, payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		projection, err := appCore.Packages.UpsertCollaboratorProjection(r.Context(), caller.TenantID, agentID, version, collaborator, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"collaborator": projection.Collaborator}, http.StatusOK)
		return
	}
	draft, err := appCore.Packages.UpsertCollaboratorForTenant(r.Context(), caller.TenantID, draftID, collaborator, caller.CallerID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"draft": draft}, http.StatusOK)
}
