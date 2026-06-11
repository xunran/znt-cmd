package loader

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

type Loader interface {
	Load(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentDefinition, error)
}

type PromptProfileProvider interface {
	GetActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (agentpackage.PromptProfileProjection, bool, error)
}

type SkillDefinitionProvider interface {
	ListActiveSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.SkillDefinitionProjection, error)
}

type ToolBindingProvider interface {
	GetActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (agentpackage.ToolBindingProjection, bool, error)
}

type CollaboratorProvider interface {
	ListActiveCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.CollaboratorProjection, error)
}

type ExportedToolProvider interface {
	ListActiveExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.ExportedToolProjection, error)
}

type PromptProfileOverlayLoader struct {
	Base     Loader
	Profiles PromptProfileProvider
}

func NewPromptProfileOverlayLoader(base Loader, profiles PromptProfileProvider) PromptProfileOverlayLoader {
	return PromptProfileOverlayLoader{Base: base, Profiles: profiles}
}

func (l PromptProfileOverlayLoader) Load(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentDefinition, error) {
	if l.Base == nil {
		return contracts.AgentDefinition{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, fmt.Sprintf("agent %s version %s not found", agentID, version), nil)
	}
	definition, err := l.Base.Load(ctx, tenantID, agentID, version)
	if err != nil {
		return definition, err
	}
	if l.Profiles == nil {
		return definition, nil
	}
	profile, ok, err := l.Profiles.GetActivePromptProfileProjection(ctx, tenantID, agentID)
	if err != nil || !ok {
		if err != nil {
			return definition, err
		}
	} else if version == "" || profile.Version == "" || profile.Version == definition.Version {
		applyPromptProfile(&definition, profile)
	}
	if bindings, ok := l.Profiles.(ToolBindingProvider); ok {
		binding, found, err := bindings.GetActiveToolBindingProjection(ctx, tenantID, agentID)
		if err != nil {
			return definition, err
		}
		if found && (version == "" || binding.Version == "" || binding.Version == definition.Version) {
			definition.Tools = binding.Bindings
		}
	}
	if skills, ok := l.Profiles.(SkillDefinitionProvider); ok {
		projections, err := skills.ListActiveSkillDefinitionProjections(ctx, tenantID, agentID)
		if err != nil {
			return definition, err
		}
		applySkillDefinitions(&definition, projections, version)
	}
	if collaborators, ok := l.Profiles.(CollaboratorProvider); ok {
		projections, err := collaborators.ListActiveCollaboratorProjections(ctx, tenantID, agentID)
		if err != nil {
			return definition, err
		}
		applyCollaborators(&definition, projections, version)
	}
	if exportedTools, ok := l.Profiles.(ExportedToolProvider); ok {
		projections, err := exportedTools.ListActiveExportedToolProjections(ctx, tenantID, agentID)
		if err != nil {
			return definition, err
		}
		applyExportedTools(&definition, projections, version)
	}
	return definition, nil
}

type StaticLoader struct {
	mu          sync.RWMutex
	definitions map[string]contracts.AgentDefinition
	defaults    map[string]contracts.AgentVersion
}

func NewStaticLoader(definitions ...contracts.AgentDefinition) *StaticLoader {
	loader := &StaticLoader{definitions: map[string]contracts.AgentDefinition{}, defaults: map[string]contracts.AgentVersion{}}
	for _, definition := range definitions {
		loader.Put(definition)
	}
	return loader
}

func (l *StaticLoader) Put(definition contracts.AgentDefinition) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.definitions[key(definition.TenantID, definition.AgentID, definition.Version)] = definition
	defaultKey := defaultKey(definition.TenantID, definition.AgentID)
	if _, ok := l.defaults[defaultKey]; !ok {
		l.defaults[defaultKey] = definition.Version
	}
}

func (l *StaticLoader) Load(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentDefinition, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if version == "" {
		version = l.defaults[defaultKey(tenantID, agentID)]
		if version == "" {
			version = l.defaults[defaultKey("", agentID)]
		}
		if version == "" {
			version = "v1"
		}
	}
	definition, ok := l.definitions[key(tenantID, agentID, version)]
	if !ok && tenantID != "" {
		definition, ok = l.definitions[key("", agentID, version)]
	}
	if !ok {
		return contracts.AgentDefinition{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, fmt.Sprintf("agent %s version %s not found", agentID, version), nil)
	}
	return definition, nil
}

func (l *StaticLoader) SetDefault(agentID contracts.AgentID, version contracts.AgentVersion) error {
	return l.SetDefaultForTenant("", agentID, version)
}

func (l *StaticLoader) SetDefaultForTenant(tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.definitions[key(tenantID, agentID, version)]; !ok {
		if tenantID != "" {
			if _, ok := l.definitions[key("", agentID, version)]; ok {
				l.defaults[defaultKey(tenantID, agentID)] = version
				return nil
			}
		}
		return contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, fmt.Sprintf("agent %s version %s not found", agentID, version), nil)
	}
	l.defaults[defaultKey(tenantID, agentID)] = version
	return nil
}

func (l *StaticLoader) DefaultVersion(agentID contracts.AgentID) contracts.AgentVersion {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.defaults[defaultKey("", agentID)]
}

func (l *StaticLoader) DefaultVersionForTenant(tenantID contracts.TenantID, agentID contracts.AgentID) contracts.AgentVersion {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if version := l.defaults[defaultKey(tenantID, agentID)]; version != "" {
		return version
	}
	return l.defaults[defaultKey("", agentID)]
}

func (l *StaticLoader) ListByTenant(tenantID contracts.TenantID) []contracts.AgentDefinition {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]contracts.AgentDefinition, 0)
	seen := map[string]struct{}{}
	for _, definition := range l.definitions {
		if definition.TenantID != "" && definition.TenantID != tenantID {
			continue
		}
		key := string(definition.AgentID) + "@" + string(definition.Version)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, definition)
	}
	return out
}

func TestAgentDefinition() contracts.AgentDefinition {
	return contracts.AgentDefinition{
		AgentID:         "test-agent",
		Version:         "v1",
		Name:            "Test Agent",
		Description:     "A static agent definition for Clean Core tests.",
		IdentityPrompt:  "You are Test Agent.",
		SystemPrompt:    "Return decisions as JSON.",
		DeveloperPrompt: "Be concise.",
		Tools: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo", "origin.agent.delegate"},
			ExposedToolIDs: []string{"echo"},
		},
		PolicyRefs: contracts.AgentPolicyRefs{PolicySetID: "policy_default"},
		Runtime: contracts.RuntimeLimits{
			MaxSteps:        4,
			MaxToolCalls:    2,
			MaxDuration:     time.Minute,
			MaxPromptTokens: 4000,
		},
		ContractVersion: "v1.0-alpha",
		CreatedAt:       time.Unix(1, 0).UTC(),
	}
}

func key(tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) string {
	return string(tenantID) + "\x00" + string(agentID) + "@" + string(version)
}

func defaultKey(tenantID contracts.TenantID, agentID contracts.AgentID) string {
	return string(tenantID) + "\x00" + string(agentID)
}

func applyPromptProfile(definition *contracts.AgentDefinition, profile agentpackage.PromptProfileProjection) {
	identity := strings.TrimSpace(profile.IdentityPrompt)
	if identity == "" {
		identity = strings.TrimSpace(profile.AgentsMD)
	}
	if identity != "" {
		definition.IdentityPrompt = identity
	}
	definition.SystemPrompt = profile.SystemPrompt
	definition.DeveloperPrompt = profile.DeveloperPrompt
}

func applySkillDefinitions(definition *contracts.AgentDefinition, projections []agentpackage.SkillDefinitionProjection, requestedVersion contracts.AgentVersion) {
	if len(projections) == 0 {
		return
	}
	definitions := append([]contracts.SkillDefinition(nil), definition.SkillDefinitions...)
	refs := append([]contracts.SkillDefinitionRef(nil), definition.Skills...)
	for _, projection := range projections {
		if requestedVersion != "" && projection.Version != "" && projection.Version != definition.Version {
			continue
		}
		skill := projection.Definition
		if skill.Card.SkillID == "" {
			skill.Card.SkillID = projection.SkillID
		}
		if skill.Card.Version == "" {
			skill.Card.Version = projection.SkillVersion
		}
		if skill.Instruction.SkillID == "" {
			skill.Instruction.SkillID = skill.Card.SkillID
		}
		definitions = upsertSkillDefinition(definitions, skill)
		refs = upsertSkillRef(refs, contracts.SkillDefinitionRef{SkillID: skill.Card.SkillID, Version: skill.Card.Version})
	}
	definition.SkillDefinitions = definitions
	definition.Skills = refs
}

func upsertSkillDefinition(definitions []contracts.SkillDefinition, skill contracts.SkillDefinition) []contracts.SkillDefinition {
	for i, current := range definitions {
		if current.Card.SkillID == skill.Card.SkillID {
			definitions[i] = skill
			return definitions
		}
	}
	return append(definitions, skill)
}

func upsertSkillRef(refs []contracts.SkillDefinitionRef, ref contracts.SkillDefinitionRef) []contracts.SkillDefinitionRef {
	for i, current := range refs {
		if current.SkillID == ref.SkillID {
			refs[i] = ref
			return refs
		}
	}
	return append(refs, ref)
}

func applyCollaborators(definition *contracts.AgentDefinition, projections []agentpackage.CollaboratorProjection, requestedVersion contracts.AgentVersion) {
	if len(projections) == 0 {
		return
	}
	collaborators := append([]contracts.AgentCollaboratorRef(nil), definition.Collaborators...)
	for _, projection := range projections {
		if requestedVersion != "" && projection.Version != "" && projection.Version != definition.Version {
			continue
		}
		collaborator := projection.Collaborator
		if collaborator.AgentID == "" {
			collaborator.AgentID = projection.CollaboratorAgentID
		}
		if collaborator.Version == "" {
			collaborator.Version = projection.CollaboratorVersion
		}
		collaborators = upsertCollaborator(collaborators, collaborator)
	}
	definition.Collaborators = collaborators
}

func upsertCollaborator(collaborators []contracts.AgentCollaboratorRef, collaborator contracts.AgentCollaboratorRef) []contracts.AgentCollaboratorRef {
	for i, current := range collaborators {
		if current.AgentID == collaborator.AgentID {
			collaborators[i] = collaborator
			return collaborators
		}
	}
	return append(collaborators, collaborator)
}

func applyExportedTools(definition *contracts.AgentDefinition, projections []agentpackage.ExportedToolProjection, requestedVersion contracts.AgentVersion) {
	if len(projections) == 0 {
		return
	}
	tools := append([]contracts.AgentExportedTool(nil), definition.Exports.Tools...)
	for _, projection := range projections {
		if requestedVersion != "" && projection.Version != "" && projection.Version != definition.Version {
			continue
		}
		tool := projection.Tool
		if tool.ToolID == "" {
			tool.ToolID = projection.ToolID
		}
		if tool.Version == "" {
			tool.Version = projection.ToolVersion
		}
		tools = upsertExportedTool(tools, tool)
	}
	definition.Exports.Tools = tools
}

func upsertExportedTool(tools []contracts.AgentExportedTool, tool contracts.AgentExportedTool) []contracts.AgentExportedTool {
	for i, current := range tools {
		if current.ToolID == tool.ToolID {
			tools[i] = tool
			return tools
		}
	}
	return append(tools, tool)
}
