package server

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func promptProfileGovernanceResourceFromProjection(profile agentpackage.PromptProfileProjection) agentSubresourceGovernanceResource {
	return agentSubresourceGovernanceResource{
		AgentID:          profile.AgentID,
		ResourceType:     agentSubresourcePromptProfile,
		Version:          profile.Version,
		SourceKind:       profile.SourceKind,
		SourceID:         profile.SourceID,
		PackageVersionID: profile.PackageVersionID,
		DraftID:          profile.DraftID,
		Status:           profile.Status,
		UpdatedAt:        governanceTimePtr(profile.UpdatedAt),
		Resource:         promptProfileProjectionView(profile),
	}
}

func promptProfileGovernanceResourceFromDefinition(definition contracts.AgentDefinition, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) agentSubresourceGovernanceResource {
	resource := map[string]any{
		"agent_id":         definition.AgentID,
		"version":          definition.Version,
		"source_kind":      sourceKind,
		"source_id":        sourceID,
		"identity_prompt":  definition.IdentityPrompt,
		"system_prompt":    definition.SystemPrompt,
		"developer_prompt": definition.DeveloperPrompt,
	}
	if packageVersionID != "" {
		resource["package_version_id"] = packageVersionID
	}
	if draftID != "" {
		resource["draft_id"] = draftID
	}
	return agentSubresourceGovernanceResource{
		AgentID:          definition.AgentID,
		ResourceType:     agentSubresourcePromptProfile,
		Version:          definition.Version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: contracts.PackageVersionID(packageVersionID),
		DraftID:          draftID,
		Status:           status,
		UpdatedAt:        governanceTimePtr(updatedAt),
		Resource:         resource,
	}
}

func toolBindingGovernanceResourceFromProjection(binding agentpackage.ToolBindingProjection) agentSubresourceGovernanceResource {
	row := agentSubresourceGovernanceResource{
		AgentID:          binding.AgentID,
		ResourceType:     agentSubresourceToolBinding,
		Version:          binding.Version,
		SourceKind:       binding.SourceKind,
		SourceID:         binding.SourceID,
		PackageVersionID: binding.PackageVersionID,
		DraftID:          binding.DraftID,
		Status:           binding.Status,
		UpdatedAt:        governanceTimePtr(binding.UpdatedAt),
		Resource:         binding.Bindings,
	}
	applyToolBindingCounts(&row, binding.Bindings)
	return row
}

func toolBindingGovernanceResourceFromDefinition(definition contracts.AgentDefinition, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) agentSubresourceGovernanceResource {
	row := agentSubresourceGovernanceResource{
		AgentID:          definition.AgentID,
		ResourceType:     agentSubresourceToolBinding,
		Version:          definition.Version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: contracts.PackageVersionID(packageVersionID),
		DraftID:          draftID,
		Status:           status,
		UpdatedAt:        governanceTimePtr(updatedAt),
		Resource:         definition.Tools,
	}
	applyToolBindingCounts(&row, definition.Tools)
	return row
}

func skillGovernanceResourceFromProjection(skill agentpackage.SkillDefinitionProjection) agentSubresourceGovernanceResource {
	return skillGovernanceResource(skill.AgentID, skill.Version, skill.SourceKind, skill.SourceID, string(skill.PackageVersionID), skill.DraftID, skill.Status, skill.UpdatedAt, skill.Definition)
}

func skillGovernanceResourceFromDefinition(definition contracts.AgentDefinition, skill contracts.SkillDefinition, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) agentSubresourceGovernanceResource {
	return skillGovernanceResource(definition.AgentID, definition.Version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, skill)
}

func skillGovernanceResource(agentID contracts.AgentID, version contracts.AgentVersion, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time, skill contracts.SkillDefinition) agentSubresourceGovernanceResource {
	row := agentSubresourceGovernanceResource{
		AgentID:                 agentID,
		ResourceType:            agentSubresourceSkill,
		ResourceID:              skill.Card.SkillID,
		ResourceVersion:         skill.Card.Version,
		Version:                 version,
		SourceKind:              sourceKind,
		SourceID:                sourceID,
		PackageVersionID:        contracts.PackageVersionID(packageVersionID),
		DraftID:                 draftID,
		Status:                  status,
		RiskLevel:               skill.Card.RiskLevel,
		AllowedToolCount:        len(skill.AllowedTools),
		RecommendedToolCount:    len(skill.RecommendedTools),
		RecommendedHandoffCount: len(skill.RecommendedHandoffs),
		UpdatedAt:               governanceTimePtr(updatedAt),
		Resource:                skill,
	}
	return row
}

func collaboratorGovernanceResourceFromProjection(collaborator agentpackage.CollaboratorProjection) agentSubresourceGovernanceResource {
	return collaboratorGovernanceResource(collaborator.AgentID, collaborator.Version, collaborator.SourceKind, collaborator.SourceID, string(collaborator.PackageVersionID), collaborator.DraftID, collaborator.Status, collaborator.UpdatedAt, collaborator.Collaborator)
}

func collaboratorGovernanceResourceFromDefinition(definition contracts.AgentDefinition, collaborator contracts.AgentCollaboratorRef, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) agentSubresourceGovernanceResource {
	return collaboratorGovernanceResource(definition.AgentID, definition.Version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, collaborator)
}

func collaboratorGovernanceResource(agentID contracts.AgentID, version contracts.AgentVersion, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time, collaborator contracts.AgentCollaboratorRef) agentSubresourceGovernanceResource {
	return agentSubresourceGovernanceResource{
		AgentID:          agentID,
		ResourceType:     agentSubresourceCollaborator,
		ResourceID:       string(collaborator.AgentID),
		ResourceVersion:  string(collaborator.Version),
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: contracts.PackageVersionID(packageVersionID),
		DraftID:          draftID,
		Status:           status,
		RequiresApproval: collaborator.RequiresApproval,
		UpdatedAt:        governanceTimePtr(updatedAt),
		Resource:         collaborator,
	}
}

func exportedToolGovernanceResourceFromProjection(tool agentpackage.ExportedToolProjection) agentSubresourceGovernanceResource {
	return exportedToolGovernanceResource(tool.AgentID, tool.Version, tool.SourceKind, tool.SourceID, string(tool.PackageVersionID), tool.DraftID, tool.Status, tool.UpdatedAt, tool.Tool)
}

func exportedToolGovernanceResourceFromDefinition(definition contracts.AgentDefinition, tool contracts.AgentExportedTool, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time) agentSubresourceGovernanceResource {
	return exportedToolGovernanceResource(definition.AgentID, definition.Version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, tool)
}

func exportedToolGovernanceResource(agentID contracts.AgentID, version contracts.AgentVersion, sourceKind string, sourceID string, packageVersionID string, draftID string, status contracts.ReleaseStatus, updatedAt time.Time, tool contracts.AgentExportedTool) agentSubresourceGovernanceResource {
	return agentSubresourceGovernanceResource{
		AgentID:          agentID,
		ResourceType:     agentSubresourceExportedTool,
		ResourceID:       tool.ToolID,
		ResourceVersion:  tool.Version,
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: contracts.PackageVersionID(packageVersionID),
		DraftID:          draftID,
		Status:           status,
		RiskLevel:        tool.RiskLevel,
		Visibility:       tool.Visibility,
		Operation:        tool.Operation,
		UpdatedAt:        governanceTimePtr(updatedAt),
		Resource:         tool,
	}
}

func compileDraftDefinition(draft agentpackage.Draft) (contracts.AgentDefinition, error) {
	definition, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition.TenantID = draft.TenantID
	return definition, nil
}

func loadBaseAgentDefinition(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentDefinition, bool, error) {
	if appCore.AgentRegistry != nil {
		definition, err := appCore.AgentRegistry.Load(ctx, tenantID, agentID, version)
		if err == nil {
			return definition, true, nil
		}
		var runtimeErr *contracts.RuntimeError
		if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeAgentVersionNotFound {
			return contracts.AgentDefinition{}, false, nil
		}
		return contracts.AgentDefinition{}, false, err
	}
	definition, err := appCore.Agents.Load(ctx, tenantID, agentID, version)
	if err == nil {
		return definition, true, nil
	}
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeAgentVersionNotFound {
		return contracts.AgentDefinition{}, false, nil
	}
	return contracts.AgentDefinition{}, false, err
}

func markStandaloneGovernanceResource(row *agentSubresourceGovernanceResource) {
	row.Standalone = true
	row.RuntimeOverlay = true
	row.Active = true
	if row.Status == "" {
		row.Status = contracts.ReleaseStable
	}
	row.Runnable = row.Status == contracts.ReleaseCanary || row.Status == contracts.ReleaseStable
}

func annotateAgentSubresourceGovernanceResource(row *agentSubresourceGovernanceResource, activeVersion contracts.AgentVersion, defaultVersion contracts.AgentVersion) {
	row.Runnable = row.Status == contracts.ReleaseCanary || row.Status == contracts.ReleaseStable
	if row.SourceKind != "release" {
		return
	}
	row.Active = activeVersion != "" && row.Version == activeVersion
	row.Default = defaultVersion != "" && row.Version == defaultVersion
}

func applyToolBindingCounts(row *agentSubresourceGovernanceResource, bindings contracts.AgentToolsConfig) {
	row.AllowedToolCount = len(bindings.AllowedToolIDs)
	row.AllowedGroupCount = len(bindings.AllowedToolGroupIDs)
	row.DeniedToolCount = len(bindings.DeniedToolIDs)
	row.DeniedGroupCount = len(bindings.DeniedToolGroupIDs)
	row.ExposedToolCount = len(bindings.ExposedToolIDs)
}

func summarizeAgentSubresourceGovernance(rows []agentSubresourceGovernanceResource) agentSubresourceGovernanceSummary {
	summary := agentSubresourceGovernanceSummary{
		TotalResources: len(rows),
		BySourceKind:   map[string]int{},
		ByStatus:       map[string]int{},
	}
	for _, row := range rows {
		if row.Standalone {
			summary.StandaloneActive++
		}
		if row.SourceKind == "draft" {
			summary.Drafts++
		}
		if row.SourceKind == "release" {
			summary.Releases++
		}
		if row.Runnable {
			summary.Runnable++
		}
		if row.Active {
			summary.Active++
		}
		if row.Default {
			summary.Default++
		}
		if row.SourceKind != "" {
			summary.BySourceKind[row.SourceKind]++
		}
		if row.Status != "" {
			summary.ByStatus[string(row.Status)]++
		}
		if row.UpdatedAt != nil && (summary.LatestUpdatedAt == nil || row.UpdatedAt.After(*summary.LatestUpdatedAt)) {
			latest := row.UpdatedAt.UTC()
			summary.LatestUpdatedAt = &latest
		}
	}
	if len(summary.BySourceKind) == 0 {
		summary.BySourceKind = nil
	}
	if len(summary.ByStatus) == 0 {
		summary.ByStatus = nil
	}
	return summary
}

func sortAgentSubresourceGovernanceResources(rows []agentSubresourceGovernanceResource) {
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		if rank := agentSubresourceGovernanceRank(left); rank != agentSubresourceGovernanceRank(right) {
			return rank < agentSubresourceGovernanceRank(right)
		}
		leftUpdated := governanceSortTime(left.UpdatedAt)
		rightUpdated := governanceSortTime(right.UpdatedAt)
		if !leftUpdated.Equal(rightUpdated) {
			return leftUpdated.After(rightUpdated)
		}
		if left.ResourceID != right.ResourceID {
			return left.ResourceID < right.ResourceID
		}
		if left.Version != right.Version {
			return left.Version < right.Version
		}
		return left.SourceID < right.SourceID
	})
}

func agentSubresourceGovernanceRank(row agentSubresourceGovernanceResource) int {
	if row.Standalone {
		return 0
	}
	switch row.SourceKind {
	case "draft":
		return 1
	case "release":
		return 2
	default:
		return 3
	}
}

func governanceSortTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func governanceTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func agentGovernanceResourceIDMatches(filter string, resourceID string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return filter == strings.TrimSpace(resourceID)
}
