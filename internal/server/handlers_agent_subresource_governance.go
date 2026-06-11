package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

const (
	agentSubresourcePromptProfile = "prompt_profile"
	agentSubresourceToolBinding   = "tool_binding"
	agentSubresourceSkill         = "skill"
	agentSubresourceCollaborator  = "collaborator"
	agentSubresourceExportedTool  = "exported_tool"
)

type agentSubresourceGovernanceView struct {
	TenantID       contracts.TenantID                   `json:"tenant_id,omitempty"`
	AgentID        contracts.AgentID                    `json:"agent_id"`
	ResourceType   string                               `json:"resource_type"`
	ResourceID     string                               `json:"resource_id,omitempty"`
	ActiveVersion  contracts.AgentVersion               `json:"active_version,omitempty"`
	DefaultVersion contracts.AgentVersion               `json:"default_version,omitempty"`
	Summary        agentSubresourceGovernanceSummary    `json:"summary"`
	Resources      []agentSubresourceGovernanceResource `json:"resources"`
}

type agentSubresourceGovernanceSummary struct {
	TotalResources   int            `json:"total_resources"`
	StandaloneActive int            `json:"standalone_active"`
	Drafts           int            `json:"drafts"`
	Releases         int            `json:"releases"`
	Runnable         int            `json:"runnable"`
	Active           int            `json:"active"`
	Default          int            `json:"default"`
	BySourceKind     map[string]int `json:"by_source_kind,omitempty"`
	ByStatus         map[string]int `json:"by_status,omitempty"`
	LatestUpdatedAt  *time.Time     `json:"latest_updated_at,omitempty"`
}

type agentSubresourceGovernanceResource struct {
	AgentID                 contracts.AgentID          `json:"agent_id"`
	ResourceType            string                     `json:"resource_type"`
	ResourceID              string                     `json:"resource_id,omitempty"`
	ResourceVersion         string                     `json:"resource_version,omitempty"`
	Version                 contracts.AgentVersion     `json:"version,omitempty"`
	SourceKind              string                     `json:"source_kind"`
	SourceID                string                     `json:"source_id,omitempty"`
	PackageVersionID        contracts.PackageVersionID `json:"package_version_id,omitempty"`
	DraftID                 string                     `json:"draft_id,omitempty"`
	Status                  contracts.ReleaseStatus    `json:"status,omitempty"`
	Standalone              bool                       `json:"standalone,omitempty"`
	RuntimeOverlay          bool                       `json:"runtime_overlay,omitempty"`
	Active                  bool                       `json:"active,omitempty"`
	Default                 bool                       `json:"default,omitempty"`
	Runnable                bool                       `json:"runnable,omitempty"`
	RiskLevel               contracts.RiskLevel        `json:"risk_level,omitempty"`
	RequiresApproval        bool                       `json:"requires_approval,omitempty"`
	Visibility              contracts.ToolVisibility   `json:"visibility,omitempty"`
	Operation               string                     `json:"operation,omitempty"`
	AllowedToolCount        int                        `json:"allowed_tool_count,omitempty"`
	AllowedGroupCount       int                        `json:"allowed_group_count,omitempty"`
	DeniedToolCount         int                        `json:"denied_tool_count,omitempty"`
	DeniedGroupCount        int                        `json:"denied_group_count,omitempty"`
	ExposedToolCount        int                        `json:"exposed_tool_count,omitempty"`
	RecommendedToolCount    int                        `json:"recommended_tool_count,omitempty"`
	RecommendedHandoffCount int                        `json:"recommended_handoff_count,omitempty"`
	UpdatedAt               *time.Time                 `json:"updated_at,omitempty"`
	Resource                any                        `json:"resource,omitempty"`
}

func handleAgentSubresourceGovernance(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, resourceType string, resourceID string) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent subresource governance method", map[string]any{"resource_type": resourceType}), http.StatusMethodNotAllowed)
		return
	}
	view, err := agentSubresourceGovernance(r.Context(), appCore, caller.TenantID, agentID, resourceType, strings.TrimSpace(resourceID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"governance": view}, http.StatusOK)
}

func agentSubresourceGovernance(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, resourceType string, resourceID string) (agentSubresourceGovernanceView, error) {
	view := agentSubresourceGovernanceView{
		TenantID:     tenantID,
		AgentID:      agentID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Resources:    []agentSubresourceGovernanceResource{},
	}
	if asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID); err != nil {
		return view, err
	} else if ok {
		view.ActiveVersion = asset.ActiveVersion
		view.DefaultVersion = asset.DefaultVersion
	}
	if view.DefaultVersion == "" && appCore.AgentRegistry != nil {
		view.DefaultVersion = appCore.AgentRegistry.DefaultVersionForTenant(tenantID, agentID)
	}
	if view.ActiveVersion == "" {
		view.ActiveVersion = view.DefaultVersion
	}
	activeRows, err := agentSubresourceActiveGovernanceRows(ctx, appCore, tenantID, agentID, resourceType, resourceID)
	if err != nil {
		return view, err
	}
	view.Resources = append(view.Resources, activeRows...)
	drafts, err := appCore.Packages.ListDrafts(ctx, tenantID, agentID)
	if err != nil {
		return view, err
	}
	for _, draft := range drafts {
		rows, err := agentSubresourceDraftGovernanceRows(ctx, appCore, draft, resourceType, resourceID)
		if err != nil {
			return view, err
		}
		view.Resources = append(view.Resources, rows...)
	}
	for _, release := range sortedAgentReleases(appCore.Packages.ListReleases(), tenantID, agentID) {
		rows, err := agentSubresourceReleaseGovernanceRows(ctx, appCore, tenantID, agentID, release, resourceType, resourceID)
		if err != nil {
			return view, err
		}
		for i := range rows {
			annotateAgentSubresourceGovernanceResource(&rows[i], view.ActiveVersion, view.DefaultVersion)
		}
		view.Resources = append(view.Resources, rows...)
	}
	sortAgentSubresourceGovernanceResources(view.Resources)
	view.Summary = summarizeAgentSubresourceGovernance(view.Resources)
	return view, nil
}

func agentSubresourceActiveGovernanceRows(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, resourceType string, resourceID string) ([]agentSubresourceGovernanceResource, error) {
	switch resourceType {
	case agentSubresourcePromptProfile:
		profile, found, err := appCore.Packages.GetActivePromptProfileProjection(ctx, tenantID, agentID)
		if err != nil || !found {
			return nil, err
		}
		row := promptProfileGovernanceResourceFromProjection(profile)
		markStandaloneGovernanceResource(&row)
		return []agentSubresourceGovernanceResource{row}, nil
	case agentSubresourceToolBinding:
		binding, found, err := appCore.Packages.GetActiveToolBindingProjection(ctx, tenantID, agentID)
		if err != nil || !found {
			return nil, err
		}
		row := toolBindingGovernanceResourceFromProjection(binding)
		markStandaloneGovernanceResource(&row)
		return []agentSubresourceGovernanceResource{row}, nil
	case agentSubresourceSkill:
		skills, err := appCore.Packages.ListActiveSkillDefinitionProjections(ctx, tenantID, agentID)
		if err != nil {
			return nil, err
		}
		rows := make([]agentSubresourceGovernanceResource, 0, len(skills))
		for _, skill := range skills {
			if !agentGovernanceResourceIDMatches(resourceID, skill.SkillID) {
				continue
			}
			row := skillGovernanceResourceFromProjection(skill)
			markStandaloneGovernanceResource(&row)
			rows = append(rows, row)
		}
		return rows, nil
	case agentSubresourceCollaborator:
		collaborators, err := appCore.Packages.ListActiveCollaboratorProjections(ctx, tenantID, agentID)
		if err != nil {
			return nil, err
		}
		rows := make([]agentSubresourceGovernanceResource, 0, len(collaborators))
		for _, collaborator := range collaborators {
			if !agentGovernanceResourceIDMatches(resourceID, string(collaborator.CollaboratorAgentID)) {
				continue
			}
			row := collaboratorGovernanceResourceFromProjection(collaborator)
			markStandaloneGovernanceResource(&row)
			rows = append(rows, row)
		}
		return rows, nil
	case agentSubresourceExportedTool:
		tools, err := appCore.Packages.ListActiveExportedToolProjections(ctx, tenantID, agentID)
		if err != nil {
			return nil, err
		}
		rows := make([]agentSubresourceGovernanceResource, 0, len(tools))
		for _, tool := range tools {
			if !agentGovernanceResourceIDMatches(resourceID, tool.ToolID) {
				continue
			}
			row := exportedToolGovernanceResourceFromProjection(tool)
			markStandaloneGovernanceResource(&row)
			rows = append(rows, row)
		}
		return rows, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent subresource governance type", map[string]any{"resource_type": resourceType})
	}
}

func agentSubresourceDraftGovernanceRows(ctx context.Context, appCore *core.Core, draft agentpackage.Draft, resourceType string, resourceID string) ([]agentSubresourceGovernanceResource, error) {
	switch resourceType {
	case agentSubresourcePromptProfile:
		if profile, found, err := appCore.Packages.GetPromptProfileProjection(ctx, draft.TenantID, draft.AgentID, "", draft.DraftID); err != nil {
			return nil, err
		} else if found {
			return []agentSubresourceGovernanceResource{promptProfileGovernanceResourceFromProjection(profile)}, nil
		}
	case agentSubresourceToolBinding:
		if binding, found, err := appCore.Packages.GetToolBindingProjection(ctx, draft.TenantID, draft.AgentID, "", draft.DraftID); err != nil {
			return nil, err
		} else if found {
			return []agentSubresourceGovernanceResource{toolBindingGovernanceResourceFromProjection(binding)}, nil
		}
	case agentSubresourceSkill:
		if skills, usedProjection, err := appCore.Packages.ListSkillDefinitionProjections(ctx, draft.TenantID, draft.AgentID, "", draft.DraftID); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(skills))
			for _, skill := range skills {
				if agentGovernanceResourceIDMatches(resourceID, skill.SkillID) {
					rows = append(rows, skillGovernanceResourceFromProjection(skill))
				}
			}
			return rows, nil
		}
	case agentSubresourceCollaborator:
		if collaborators, usedProjection, err := appCore.Packages.ListCollaboratorProjections(ctx, draft.TenantID, draft.AgentID, "", draft.DraftID); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(collaborators))
			for _, collaborator := range collaborators {
				if agentGovernanceResourceIDMatches(resourceID, string(collaborator.CollaboratorAgentID)) {
					rows = append(rows, collaboratorGovernanceResourceFromProjection(collaborator))
				}
			}
			return rows, nil
		}
	case agentSubresourceExportedTool:
		if tools, usedProjection, err := appCore.Packages.ListExportedToolProjections(ctx, draft.TenantID, draft.AgentID, "", draft.DraftID); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(tools))
			for _, tool := range tools {
				if agentGovernanceResourceIDMatches(resourceID, tool.ToolID) {
					rows = append(rows, exportedToolGovernanceResourceFromProjection(tool))
				}
			}
			return rows, nil
		}
	}
	definition, err := compileDraftDefinition(draft)
	if err != nil {
		return nil, err
	}
	return agentSubresourceGovernanceRowsFromDefinition(definition, resourceType, resourceID, "draft", draft.DraftID, "", draft.DraftID, draft.Status, draft.UpdatedAt)
}

func agentSubresourceReleaseGovernanceRows(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, release contracts.AgentPackageVersion, resourceType string, resourceID string) ([]agentSubresourceGovernanceResource, error) {
	switch resourceType {
	case agentSubresourcePromptProfile:
		if profile, found, err := appCore.Packages.GetPromptProfileProjection(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if found {
			return []agentSubresourceGovernanceResource{promptProfileGovernanceResourceFromProjection(profile)}, nil
		}
	case agentSubresourceToolBinding:
		if binding, found, err := appCore.Packages.GetToolBindingProjection(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if found {
			return []agentSubresourceGovernanceResource{toolBindingGovernanceResourceFromProjection(binding)}, nil
		}
	case agentSubresourceSkill:
		if skills, usedProjection, err := appCore.Packages.ListSkillDefinitionProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(skills))
			for _, skill := range skills {
				if agentGovernanceResourceIDMatches(resourceID, skill.SkillID) {
					rows = append(rows, skillGovernanceResourceFromProjection(skill))
				}
			}
			return rows, nil
		}
	case agentSubresourceCollaborator:
		if collaborators, usedProjection, err := appCore.Packages.ListCollaboratorProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(collaborators))
			for _, collaborator := range collaborators {
				if agentGovernanceResourceIDMatches(resourceID, string(collaborator.CollaboratorAgentID)) {
					rows = append(rows, collaboratorGovernanceResourceFromProjection(collaborator))
				}
			}
			return rows, nil
		}
	case agentSubresourceExportedTool:
		if tools, usedProjection, err := appCore.Packages.ListExportedToolProjections(ctx, tenantID, agentID, release.Version, ""); err != nil {
			return nil, err
		} else if usedProjection {
			rows := make([]agentSubresourceGovernanceResource, 0, len(tools))
			for _, tool := range tools {
				if agentGovernanceResourceIDMatches(resourceID, tool.ToolID) {
					rows = append(rows, exportedToolGovernanceResourceFromProjection(tool))
				}
			}
			return rows, nil
		}
	}
	definition, found, err := loadBaseAgentDefinition(ctx, appCore, tenantID, agentID, release.Version)
	if err != nil || !found {
		return nil, err
	}
	updatedAt := release.CreatedAt
	if release.PublishedAt != nil {
		updatedAt = *release.PublishedAt
	}
	return agentSubresourceGovernanceRowsFromDefinition(definition, resourceType, resourceID, "release", string(release.PackageVersionID), string(release.PackageVersionID), "", release.Status, updatedAt)
}

func agentSubresourceGovernanceRowsFromDefinition(definition contracts.AgentDefinition, resourceType string, resourceID string, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) ([]agentSubresourceGovernanceResource, error) {
	switch resourceType {
	case agentSubresourcePromptProfile:
		return []agentSubresourceGovernanceResource{promptProfileGovernanceResourceFromDefinition(definition, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt)}, nil
	case agentSubresourceToolBinding:
		return []agentSubresourceGovernanceResource{toolBindingGovernanceResourceFromDefinition(definition, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt)}, nil
	case agentSubresourceSkill:
		rows := make([]agentSubresourceGovernanceResource, 0, len(definition.SkillDefinitions))
		for _, skill := range definition.SkillDefinitions {
			if agentGovernanceResourceIDMatches(resourceID, skill.Card.SkillID) {
				rows = append(rows, skillGovernanceResourceFromDefinition(definition, skill, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt))
			}
		}
		return rows, nil
	case agentSubresourceCollaborator:
		rows := make([]agentSubresourceGovernanceResource, 0, len(definition.Collaborators))
		for _, collaborator := range definition.Collaborators {
			if agentGovernanceResourceIDMatches(resourceID, string(collaborator.AgentID)) {
				rows = append(rows, collaboratorGovernanceResourceFromDefinition(definition, collaborator, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt))
			}
		}
		return rows, nil
	case agentSubresourceExportedTool:
		rows := make([]agentSubresourceGovernanceResource, 0, len(definition.Exports.Tools))
		for _, tool := range definition.Exports.Tools {
			if agentGovernanceResourceIDMatches(resourceID, tool.ToolID) {
				rows = append(rows, exportedToolGovernanceResourceFromDefinition(definition, tool, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt))
			}
		}
		return rows, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent subresource governance type", map[string]any{"resource_type": resourceType})
	}
}
