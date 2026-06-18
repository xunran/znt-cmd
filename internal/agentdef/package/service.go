package agentpackage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type AgentPackageSource struct {
	SourceKind       contracts.AgentSourceKind        `json:"source_kind,omitempty"`
	ProviderID       string                           `json:"provider_id,omitempty"`
	ManifestVersion  string                           `json:"manifest_version,omitempty"`
	AgentsMD         string                           `json:"agents_md"`
	Prompt           string                           `json:"prompt"`
	Strategies       contracts.AgentStrategies        `json:"strategies,omitempty"`
	ToolBindings     contracts.AgentToolsConfig       `json:"tool_bindings"`
	Skills           []contracts.SkillDefinitionRef   `json:"skills,omitempty"`
	SkillDefinitions []contracts.SkillDefinition      `json:"skill_definitions,omitempty"`
	Collaborators    []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
	Exports          contracts.AgentExports           `json:"exports,omitempty"`
	RuntimeHooks     contracts.AgentRuntimeHooks      `json:"runtime_hooks,omitempty"`
	Metadata         map[string]any                   `json:"metadata,omitempty"`
}

type AgentPluginSource struct {
	ProviderID       string                           `json:"provider_id"`
	ManifestVersion  string                           `json:"manifest_version,omitempty"`
	AgentsMD         string                           `json:"agents_md,omitempty"`
	Prompt           string                           `json:"prompt,omitempty"`
	Strategies       contracts.AgentStrategies        `json:"strategies,omitempty"`
	ToolBindings     contracts.AgentToolsConfig       `json:"tool_bindings,omitempty"`
	Skills           []contracts.SkillDefinitionRef   `json:"skills,omitempty"`
	SkillDefinitions []contracts.SkillDefinition      `json:"skill_definitions,omitempty"`
	Collaborators    []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
	Exports          contracts.AgentExports           `json:"exports,omitempty"`
	RuntimeHooks     contracts.AgentRuntimeHooks      `json:"runtime_hooks,omitempty"`
	Metadata         map[string]any                   `json:"metadata,omitempty"`
}

func PackageSourceFromPlugin(plugin AgentPluginSource) AgentPackageSource {
	return AgentPackageSource{
		SourceKind:       contracts.AgentSourceKindPlugin,
		ProviderID:       plugin.ProviderID,
		ManifestVersion:  plugin.ManifestVersion,
		AgentsMD:         plugin.AgentsMD,
		Prompt:           plugin.Prompt,
		Strategies:       plugin.Strategies,
		ToolBindings:     plugin.ToolBindings,
		Skills:           plugin.Skills,
		SkillDefinitions: plugin.SkillDefinitions,
		Collaborators:    plugin.Collaborators,
		Exports:          plugin.Exports,
		RuntimeHooks:     plugin.RuntimeHooks,
		Metadata:         plugin.Metadata,
	}
}

const (
	AgentAssetActive   = "active"
	AgentAssetDisabled = "disabled"
	AgentAssetDeleted  = "deleted"

	PromptProfileSourceKind   = "profile"
	SkillDefinitionSourceKind = "skill"
	ToolBindingSourceKind     = "tool_binding"
	CollaboratorSourceKind    = "collaborator"
	ExportedToolSourceKind    = "exported_tool"
)

type AgentAsset struct {
	TenantID          contracts.TenantID                 `json:"tenant_id"`
	AgentID           contracts.AgentID                  `json:"agent_id"`
	Name              string                             `json:"name,omitempty"`
	Description       string                             `json:"description,omitempty"`
	OwnerID           string                             `json:"owner_id,omitempty"`
	Status            string                             `json:"status"`
	ActiveVersion     contracts.AgentVersion             `json:"active_version,omitempty"`
	DefaultVersion    contracts.AgentVersion             `json:"default_version,omitempty"`
	CarrierKind       contracts.AgentCarrierKind         `json:"carrier_kind,omitempty"`
	RuntimeContract   contracts.RuntimeContractKind      `json:"runtime_contract,omitempty"`
	SourceKind        contracts.AgentSourceKind          `json:"source_kind,omitempty"`
	SourceProviderID  string                             `json:"source_provider_id,omitempty"`
	ManifestHash      string                             `json:"manifest_hash,omitempty"`
	ConformanceStatus contracts.RuntimeConformanceStatus `json:"conformance_status,omitempty"`
	CreatedBy         string                             `json:"created_by,omitempty"`
	CreatedAt         time.Time                          `json:"created_at"`
	UpdatedAt         time.Time                          `json:"updated_at"`
	DeletedAt         *time.Time                         `json:"deleted_at,omitempty"`
}

type AgentAssetPatch struct {
	Name           *string
	Description    *string
	OwnerID        *string
	Status         *string
	ActiveVersion  *contracts.AgentVersion
	DefaultVersion *contracts.AgentVersion
}

type SkillDraftInput struct {
	SkillID                 string                       `json:"skill_id"`
	Version                 string                       `json:"version,omitempty"`
	Name                    string                       `json:"name,omitempty"`
	Description             string                       `json:"description,omitempty"`
	Instruction             string                       `json:"instruction,omitempty"`
	Status                  string                       `json:"status,omitempty"`
	RiskLevel               contracts.RiskLevel          `json:"risk_level,omitempty"`
	Tags                    []string                     `json:"tags,omitempty"`
	WhenToUse               []string                     `json:"when_to_use,omitempty"`
	OutputRequirements      []string                     `json:"output_requirements,omitempty"`
	Constraints             []string                     `json:"constraints,omitempty"`
	Resources               []contracts.SkillResourceRef `json:"resources,omitempty"`
	RecommendedTools        []string                     `json:"recommended_tools,omitempty"`
	AllowedTools            []string                     `json:"allowed_tools,omitempty"`
	RecommendedMemoryReads  []string                     `json:"recommended_memory_reads,omitempty"`
	RecommendedMemoryWrites []string                     `json:"recommended_memory_writes,omitempty"`
	RecommendedHandoffs     []string                     `json:"recommended_handoffs,omitempty"`
	CompletionCriteria      []string                     `json:"completion_criteria,omitempty"`
	OutputSchema            map[string]any               `json:"output_schema,omitempty"`
}

type Draft struct {
	DraftID   string                  `json:"draft_id"`
	TenantID  contracts.TenantID      `json:"tenant_id"`
	AgentID   contracts.AgentID       `json:"agent_id"`
	Version   contracts.AgentVersion  `json:"version"`
	Source    AgentPackageSource      `json:"source"`
	Status    contracts.ReleaseStatus `json:"status"`
	CreatedBy string                  `json:"created_by"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type Proposal struct {
	ProposalID contracts.ProposalID     `json:"proposal_id"`
	TenantID   contracts.TenantID       `json:"tenant_id"`
	DraftID    string                   `json:"draft_id"`
	AgentID    contracts.AgentID        `json:"agent_id"`
	Version    contracts.AgentVersion   `json:"version"`
	Type       string                   `json:"type"`
	Title      string                   `json:"title,omitempty"`
	Reason     string                   `json:"reason,omitempty"`
	Patch      map[string]any           `json:"patch,omitempty"`
	Status     contracts.ProposalStatus `json:"status"`
	CreatedBy  string                   `json:"created_by"`
	ReviewedBy string                   `json:"reviewed_by,omitempty"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
}

type PromptProfileProjection struct {
	TenantID         contracts.TenantID         `json:"tenant_id"`
	AgentID          contracts.AgentID          `json:"agent_id"`
	Version          contracts.AgentVersion     `json:"version"`
	SourceKind       string                     `json:"source_kind"`
	SourceID         string                     `json:"source_id"`
	PackageVersionID contracts.PackageVersionID `json:"package_version_id,omitempty"`
	DraftID          string                     `json:"draft_id,omitempty"`
	Status           contracts.ReleaseStatus    `json:"status"`
	IdentityPrompt   string                     `json:"identity_prompt,omitempty"`
	SystemPrompt     string                     `json:"system_prompt,omitempty"`
	DeveloperPrompt  string                     `json:"developer_prompt,omitempty"`
	AgentsMD         string                     `json:"agents_md,omitempty"`
	UpdatedAt        time.Time                  `json:"updated_at"`
}

type ToolBindingProjection struct {
	TenantID         contracts.TenantID         `json:"tenant_id"`
	AgentID          contracts.AgentID          `json:"agent_id"`
	Version          contracts.AgentVersion     `json:"version"`
	SourceKind       string                     `json:"source_kind"`
	SourceID         string                     `json:"source_id"`
	PackageVersionID contracts.PackageVersionID `json:"package_version_id,omitempty"`
	DraftID          string                     `json:"draft_id,omitempty"`
	Status           contracts.ReleaseStatus    `json:"status"`
	Bindings         contracts.AgentToolsConfig `json:"bindings"`
	UpdatedAt        time.Time                  `json:"updated_at"`
}

type SkillDefinitionProjection struct {
	TenantID         contracts.TenantID         `json:"tenant_id"`
	AgentID          contracts.AgentID          `json:"agent_id"`
	Version          contracts.AgentVersion     `json:"version"`
	SourceKind       string                     `json:"source_kind"`
	SourceID         string                     `json:"source_id"`
	PackageVersionID contracts.PackageVersionID `json:"package_version_id,omitempty"`
	DraftID          string                     `json:"draft_id,omitempty"`
	Status           contracts.ReleaseStatus    `json:"status"`
	SkillID          string                     `json:"skill_id"`
	SkillVersion     string                     `json:"skill_version,omitempty"`
	Definition       contracts.SkillDefinition  `json:"definition"`
	UpdatedAt        time.Time                  `json:"updated_at"`
}

type CollaboratorProjection struct {
	TenantID            contracts.TenantID             `json:"tenant_id"`
	AgentID             contracts.AgentID              `json:"agent_id"`
	Version             contracts.AgentVersion         `json:"version"`
	SourceKind          string                         `json:"source_kind"`
	SourceID            string                         `json:"source_id"`
	PackageVersionID    contracts.PackageVersionID     `json:"package_version_id,omitempty"`
	DraftID             string                         `json:"draft_id,omitempty"`
	Status              contracts.ReleaseStatus        `json:"status"`
	CollaboratorAgentID contracts.AgentID              `json:"collaborator_agent_id"`
	CollaboratorVersion contracts.AgentVersion         `json:"collaborator_version,omitempty"`
	Collaborator        contracts.AgentCollaboratorRef `json:"collaborator"`
	UpdatedAt           time.Time                      `json:"updated_at"`
}

type ExportedToolProjection struct {
	TenantID         contracts.TenantID          `json:"tenant_id"`
	AgentID          contracts.AgentID           `json:"agent_id"`
	Version          contracts.AgentVersion      `json:"version"`
	SourceKind       string                      `json:"source_kind"`
	SourceID         string                      `json:"source_id"`
	PackageVersionID contracts.PackageVersionID  `json:"package_version_id,omitempty"`
	DraftID          string                      `json:"draft_id,omitempty"`
	Status           contracts.ReleaseStatus     `json:"status"`
	ToolID           string                      `json:"tool_id"`
	ToolVersion      string                      `json:"tool_version,omitempty"`
	Tool             contracts.AgentExportedTool `json:"tool"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

type Service struct {
	mu             sync.RWMutex
	assets         map[string]AgentAsset
	drafts         map[string]Draft
	proposals      map[contracts.ProposalID]Proposal
	releases       map[contracts.PackageVersionID]contracts.AgentPackageVersion
	evalPass       map[contracts.PackageVersionID]bool
	promptProfiles map[string]PromptProfileProjection
	skills         map[string]SkillDefinitionProjection
	toolBindings   map[string]ToolBindingProjection
	collaborators  map[string]CollaboratorProjection
	exportedTools  map[string]ExportedToolProjection
	audit          audit.Logger
	store          Store
	now            func() time.Time
}

type Store interface {
	SaveAgentAsset(ctx context.Context, asset AgentAsset) error
	GetAgentAsset(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (AgentAsset, bool, error)
	ListAgentAssets(ctx context.Context, tenantID contracts.TenantID) ([]AgentAsset, error)
	SaveDraft(ctx context.Context, draft Draft) error
	GetDraft(ctx context.Context, draftID string) (Draft, bool, error)
	ListDrafts(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]Draft, error)
	SaveProposal(ctx context.Context, proposal Proposal) error
	GetProposal(ctx context.Context, proposalID contracts.ProposalID) (Proposal, bool, error)
	SaveRelease(ctx context.Context, release contracts.AgentPackageVersion, source AgentPackageSource, compiled contracts.AgentDefinition) error
	UpdateReleaseStatus(ctx context.Context, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus) error
	UpdateReleaseCanary(ctx context.Context, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus, percent int, scope []string) error
	MarkEvalResult(ctx context.Context, packageVersionID contracts.PackageVersionID, passed bool) error
	GetRelease(ctx context.Context, packageVersionID contracts.PackageVersionID) (contracts.AgentPackageVersion, bool, error)
	ListReleases(ctx context.Context) ([]contracts.AgentPackageVersion, error)
	RecordCanaryHit(ctx context.Context, hit contracts.CanaryHit) error
	ListCanaryHits(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]contracts.CanaryHit, error)
}

type DefinitionRestoreStore interface {
	ListAgentDefinitions(ctx context.Context) ([]contracts.AgentDefinition, error)
	ListAgentAssets(ctx context.Context, tenantID contracts.TenantID) ([]AgentAsset, error)
}

type ProjectionStore interface {
	GetPromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (PromptProfileProjection, bool, error)
	GetToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (ToolBindingProjection, bool, error)
	ListSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]SkillDefinitionProjection, error)
	GetSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, skillID string) (SkillDefinitionProjection, bool, error)
	ListCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]CollaboratorProjection, error)
	GetCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, collaboratorAgentID contracts.AgentID) (CollaboratorProjection, bool, error)
	ListExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]ExportedToolProjection, error)
	GetExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, toolID string) (ExportedToolProjection, bool, error)
}

type PromptProfileStore interface {
	GetActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (PromptProfileProjection, bool, error)
	UpsertPromptProfileProjection(ctx context.Context, profile PromptProfileProjection) error
	DeleteActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error
}

type SkillDefinitionStore interface {
	ListActiveSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]SkillDefinitionProjection, error)
	GetActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) (SkillDefinitionProjection, bool, error)
	UpsertSkillDefinitionProjection(ctx context.Context, skill SkillDefinitionProjection) error
	DeleteActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) error
}

type SkillDefinitionVersionStore interface {
	ListSkillDefinitionProjectionVersions(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) ([]SkillDefinitionProjection, error)
	ActivateSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, skillVersion string) error
}

type ToolBindingStore interface {
	GetActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (ToolBindingProjection, bool, error)
	UpsertToolBindingProjection(ctx context.Context, binding ToolBindingProjection) error
	DeleteActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error
}

type CollaboratorStore interface {
	ListActiveCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]CollaboratorProjection, error)
	GetActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) (CollaboratorProjection, bool, error)
	UpsertCollaboratorProjection(ctx context.Context, collaborator CollaboratorProjection) error
	DeleteActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) error
}

type ExportedToolStore interface {
	ListActiveExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]ExportedToolProjection, error)
	GetActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) (ExportedToolProjection, bool, error)
	UpsertExportedToolProjection(ctx context.Context, tool ExportedToolProjection) error
	DeleteActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) error
}

func NewService(auditLogger audit.Logger) *Service {
	return NewServiceWithStore(auditLogger, nil)
}

func NewServiceWithStore(auditLogger audit.Logger, store Store) *Service {
	return &Service{
		assets:         map[string]AgentAsset{},
		drafts:         map[string]Draft{},
		proposals:      map[contracts.ProposalID]Proposal{},
		releases:       map[contracts.PackageVersionID]contracts.AgentPackageVersion{},
		evalPass:       map[contracts.PackageVersionID]bool{},
		promptProfiles: map[string]PromptProfileProjection{},
		skills:         map[string]SkillDefinitionProjection{},
		toolBindings:   map[string]ToolBindingProjection{},
		collaborators:  map[string]CollaboratorProjection{},
		exportedTools:  map[string]ExportedToolProjection{},
		audit:          auditLogger,
		store:          store,
		now:            func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateAgentAssetForTenant(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, name string, description string, ownerID string, actorID string) (AgentAsset, error) {
	if tenantID == "" || agentID == "" {
		return AgentAsset{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent asset requires tenant_id and agent_id", nil)
	}
	if existing, ok, err := s.GetAgentAsset(ctx, tenantID, agentID); err != nil {
		return AgentAsset{}, err
	} else if ok && existing.Status != AgentAssetDeleted {
		return AgentAsset{}, contracts.NewRuntimeError(contracts.CodePackageVersionConflict, "agent asset already exists", map[string]any{"agent_id": agentID})
	}
	now := s.now()
	asset := AgentAsset{
		TenantID:          tenantID,
		AgentID:           agentID,
		Name:              name,
		Description:       description,
		OwnerID:           ownerID,
		Status:            AgentAssetActive,
		CarrierKind:       contracts.AgentCarrierKindNativeAgent,
		RuntimeContract:   contracts.RuntimeContractManaged,
		SourceKind:        contracts.AgentSourceKindPackage,
		ConformanceStatus: contracts.RuntimeConformanceUnknown,
		CreatedBy:         actorID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if asset.Name == "" {
		asset.Name = string(agentID)
	}
	if err := s.saveAgentAsset(ctx, asset); err != nil {
		return AgentAsset{}, err
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.asset.create", string(agentID), "allowed", "")
	return asset, nil
}

func (s *Service) GetAgentAsset(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (AgentAsset, bool, error) {
	s.mu.RLock()
	asset, ok := s.assets[agentAssetKey(tenantID, agentID)]
	s.mu.RUnlock()
	if ok || s.store == nil {
		return asset, ok, nil
	}
	return s.store.GetAgentAsset(ctx, tenantID, agentID)
}

func (s *Service) ListAgentAssets(ctx context.Context, tenantID contracts.TenantID) ([]AgentAsset, error) {
	if s.store != nil {
		return s.store.ListAgentAssets(ctx, tenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AgentAsset, 0)
	for _, asset := range s.assets {
		if tenantID != "" && asset.TenantID != tenantID {
			continue
		}
		out = append(out, asset)
	}
	return out, nil
}

func (s *Service) PatchAgentAssetForTenant(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, patch AgentAssetPatch, actorID string) (AgentAsset, error) {
	asset, ok, err := s.GetAgentAsset(ctx, tenantID, agentID)
	if err != nil {
		return AgentAsset{}, err
	}
	if !ok {
		return AgentAsset{}, contracts.NewRuntimeError(contracts.CodeAgentNotFound, "agent asset not found", map[string]any{"agent_id": agentID})
	}
	if patch.Name != nil {
		asset.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		asset.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.OwnerID != nil {
		asset.OwnerID = strings.TrimSpace(*patch.OwnerID)
	}
	if patch.ActiveVersion != nil {
		asset.ActiveVersion = *patch.ActiveVersion
	}
	if patch.DefaultVersion != nil {
		asset.DefaultVersion = *patch.DefaultVersion
	}
	if patch.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*patch.Status))
		if !validAgentAssetStatus(status) {
			return AgentAsset{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid agent asset status", map[string]any{"status": status})
		}
		asset.Status = status
		if status == AgentAssetDeleted {
			now := s.now()
			asset.DeletedAt = &now
		} else {
			asset.DeletedAt = nil
		}
	}
	asset.UpdatedAt = s.now()
	if err := s.saveAgentAsset(ctx, asset); err != nil {
		return AgentAsset{}, err
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.asset.update", string(agentID), "allowed", "")
	return asset, nil
}

func (s *Service) DeleteAgentAssetForTenant(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, actorID string) (AgentAsset, error) {
	status := AgentAssetDeleted
	asset, err := s.PatchAgentAssetForTenant(ctx, tenantID, agentID, AgentAssetPatch{Status: &status}, actorID)
	if err != nil {
		return AgentAsset{}, err
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.asset.delete", string(agentID), "allowed", "")
	return asset, nil
}

func (s *Service) EnsureAgentAssetVersionForTenant(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, actorID string) (AgentAsset, error) {
	asset, ok, err := s.GetAgentAsset(ctx, tenantID, agentID)
	if err != nil {
		return AgentAsset{}, err
	}
	if !ok {
		asset, err = s.CreateAgentAssetForTenant(ctx, tenantID, agentID, string(agentID), "", "", actorID)
		if err != nil {
			return AgentAsset{}, err
		}
	}
	asset.ActiveVersion = version
	asset.DefaultVersion = version
	s.applyReleaseCarrierToAsset(ctx, &asset, version)
	if asset.Status == "" || asset.Status == AgentAssetDeleted {
		asset.Status = AgentAssetActive
		asset.DeletedAt = nil
	}
	asset.UpdatedAt = s.now()
	if err := s.saveAgentAsset(ctx, asset); err != nil {
		return AgentAsset{}, err
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.asset.activate_version", string(agentID), "allowed", string(version))
	return asset, nil
}

func (s *Service) applyReleaseCarrierToAsset(ctx context.Context, asset *AgentAsset, version contracts.AgentVersion) {
	if asset == nil {
		return
	}
	for _, release := range s.releases {
		if release.TenantID == asset.TenantID && release.AgentID == asset.AgentID && release.Version == version {
			applyReleaseCarrierSnapshot(asset, release)
			return
		}
	}
	if s.store == nil {
		ensureAgentAssetCarrierDefaults(asset)
		return
	}
	releases, err := s.store.ListReleases(ctx)
	if err != nil {
		ensureAgentAssetCarrierDefaults(asset)
		return
	}
	for _, release := range releases {
		if release.TenantID == asset.TenantID && release.AgentID == asset.AgentID && release.Version == version {
			applyReleaseCarrierSnapshot(asset, release)
			return
		}
	}
	ensureAgentAssetCarrierDefaults(asset)
}

func applyReleaseCarrierSnapshot(asset *AgentAsset, release contracts.AgentPackageVersion) {
	asset.CarrierKind = contracts.NormalizeCarrierKind(release.SourceKind, release.CarrierKind)
	asset.RuntimeContract = contracts.NormalizeRuntimeContract(asset.CarrierKind, release.RuntimeContract)
	asset.SourceKind = contracts.NormalizeSourceKind(release.SourceKind)
	asset.SourceProviderID = release.SourceProviderID
	asset.ManifestHash = release.ManifestHash
	asset.ConformanceStatus = release.ConformanceStatus
	if asset.ConformanceStatus == "" {
		asset.ConformanceStatus = contracts.RuntimeConformanceUnknown
	}
}

func ensureAgentAssetCarrierDefaults(asset *AgentAsset) {
	if asset.SourceKind == "" {
		asset.SourceKind = contracts.AgentSourceKindPackage
	}
	asset.CarrierKind = contracts.NormalizeCarrierKind(asset.SourceKind, asset.CarrierKind)
	asset.RuntimeContract = contracts.NormalizeRuntimeContract(asset.CarrierKind, asset.RuntimeContract)
	if asset.ConformanceStatus == "" {
		asset.ConformanceStatus = contracts.RuntimeConformanceUnknown
	}
}

func (s *Service) saveAgentAsset(ctx context.Context, asset AgentAsset) error {
	s.mu.Lock()
	s.assets[agentAssetKey(asset.TenantID, asset.AgentID)] = asset
	s.mu.Unlock()
	if s.store != nil {
		return s.store.SaveAgentAsset(ctx, asset)
	}
	return nil
}

func validAgentAssetStatus(status string) bool {
	switch status {
	case AgentAssetActive, AgentAssetDisabled, AgentAssetDeleted:
		return true
	default:
		return false
	}
}

func agentAssetKey(tenantID contracts.TenantID, agentID contracts.AgentID) string {
	return string(tenantID) + "\x00" + string(agentID)
}

func (s *Service) CreateDraft(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, source AgentPackageSource, actorID string) (Draft, error) {
	now := s.now()
	draft := Draft{
		DraftID:   idgen.New("draft"),
		TenantID:  tenantID,
		AgentID:   agentID,
		Version:   version,
		Source:    source,
		Status:    contracts.ReleaseDraft,
		CreatedBy: actorID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.drafts[draft.DraftID] = draft
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveDraft(ctx, draft); err != nil {
			return Draft{}, err
		}
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.package.draft_create", draft.DraftID, "allowed", "")
	return draft, nil
}

func (s *Service) PatchSourceForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, source AgentPackageSource, actorID string) (Draft, error) {
	if err := ValidateSourceMetadata(source.Metadata); err != nil {
		return Draft{}, err
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source = source
	})
}

func (s *Service) PatchStrategiesForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, strategies contracts.AgentStrategies, actorID string) (Draft, error) {
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source.Strategies = strategies
	})
}

func (s *Service) PatchCollaboratorsForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, collaborators []contracts.AgentCollaboratorRef, actorID string) (Draft, error) {
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source.Collaborators = collaborators
	})
}

func (s *Service) PatchExportsForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, exports contracts.AgentExports, actorID string) (Draft, error) {
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source.Exports = exports
	})
}

func (s *Service) UpsertCollaboratorForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, collaborator contracts.AgentCollaboratorRef, actorID string) (Draft, error) {
	if collaborator.AgentID == "" {
		return Draft{}, fmt.Errorf("agent_id is required")
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source.Collaborators = upsertCollaborator(draft.Source.Collaborators, collaborator)
	})
}

func (s *Service) RemoveCollaboratorForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, agentID contracts.AgentID, actorID string) (Draft, error) {
	if agentID == "" {
		return Draft{}, fmt.Errorf("agent_id is required")
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		out := make([]contracts.AgentCollaboratorRef, 0, len(draft.Source.Collaborators))
		for _, current := range draft.Source.Collaborators {
			if current.AgentID != agentID {
				out = append(out, current)
			}
		}
		draft.Source.Collaborators = out
	})
}

func (s *Service) UpsertExportedToolForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, tool contracts.AgentExportedTool, actorID string) (Draft, error) {
	if strings.TrimSpace(tool.ToolID) == "" {
		return Draft{}, fmt.Errorf("tool_id is required")
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Source.Exports.Tools = upsertExportedTool(draft.Source.Exports.Tools, tool)
	})
}

func (s *Service) RemoveExportedToolForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, toolID string, actorID string) (Draft, error) {
	if strings.TrimSpace(toolID) == "" {
		return Draft{}, fmt.Errorf("tool_id is required")
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		out := make([]contracts.AgentExportedTool, 0, len(draft.Source.Exports.Tools))
		for _, current := range draft.Source.Exports.Tools {
			if current.ToolID != toolID {
				out = append(out, current)
			}
		}
		draft.Source.Exports.Tools = out
	})
}

func (s *Service) UpsertSkillForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, skill SkillDraftInput, actorID string) (Draft, error) {
	if skill.SkillID == "" {
		return Draft{}, fmt.Errorf("skill_id is required")
	}
	if skill.RiskLevel == "" {
		skill.RiskLevel = contracts.RiskLow
	}
	if err := skill.RiskLevel.Validate(); err != nil {
		return Draft{}, err
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		definitions := append([]contracts.SkillDefinition(nil), draft.Source.SkillDefinitions...)
		replacement := skillDefinitionFromInput(skill)
		replaced := false
		for i, current := range definitions {
			if current.Card.SkillID == skill.SkillID && current.Card.Version == skill.Version {
				definitions[i] = replacement
				replaced = true
				break
			}
		}
		if !replaced {
			definitions = append(definitions, replacement)
		}
		draft.Source.SkillDefinitions = definitions
		draft.Source.Skills = upsertSkillRef(draft.Source.Skills, contracts.SkillDefinitionRef{SkillID: skill.SkillID, Version: skill.Version})
	})
}

func (s *Service) RemoveSkillForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, skillID string, version string, actorID string) (Draft, error) {
	if skillID == "" {
		return Draft{}, fmt.Errorf("skill_id is required")
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		next := make([]contracts.SkillDefinition, 0, len(draft.Source.SkillDefinitions))
		for _, current := range draft.Source.SkillDefinitions {
			if current.Card.SkillID == skillID && (version == "" || current.Card.Version == version) {
				continue
			}
			next = append(next, current)
		}
		draft.Source.SkillDefinitions = next
		draft.Source.Skills = removeSkillRef(draft.Source.Skills, skillID, version)
	})
}

func (s *Service) CreateProposalForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, proposalType string, title string, reason string, patch map[string]any, actorID string) (Proposal, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return Proposal{}, err
	}
	if !ok {
		return Proposal{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.TenantID != tenantID {
		return Proposal{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	if proposalType == "" {
		proposalType = "package_change"
	}
	now := s.now()
	proposal := Proposal{
		ProposalID: contracts.ProposalID(idgen.New("proposal")),
		TenantID:   tenantID,
		DraftID:    draft.DraftID,
		AgentID:    draft.AgentID,
		Version:    draft.Version,
		Type:       proposalType,
		Title:      title,
		Reason:     reason,
		Patch:      clonePatch(patch),
		Status:     contracts.ProposalDraft,
		CreatedBy:  actorID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.mu.Lock()
	s.proposals[proposal.ProposalID] = proposal
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveProposal(ctx, proposal); err != nil {
			return Proposal{}, err
		}
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.package.proposal.create", string(proposal.ProposalID), "allowed", reason)
	return proposal, nil
}

func (s *Service) SubmitProposalForTenant(ctx context.Context, tenantID contracts.TenantID, proposalID contracts.ProposalID, actorID string) (Proposal, error) {
	return s.updateProposalForTenant(ctx, tenantID, proposalID, actorID, "agent.package.proposal.submit", func(proposal *Proposal) error {
		if proposal.Status != contracts.ProposalDraft {
			return fmt.Errorf("proposal %s is not draft", proposalID)
		}
		proposal.Status = contracts.ProposalPendingReview
		return nil
	})
}

func (s *Service) ApproveProposalForTenant(ctx context.Context, tenantID contracts.TenantID, proposalID contracts.ProposalID, actorID string) (Proposal, error) {
	return s.updateProposalForTenant(ctx, tenantID, proposalID, actorID, "agent.package.proposal.approve", func(proposal *Proposal) error {
		if proposal.Status != contracts.ProposalPendingReview {
			return fmt.Errorf("proposal %s is not pending review", proposalID)
		}
		proposal.Status = contracts.ProposalApproved
		proposal.ReviewedBy = actorID
		return nil
	})
}

func (s *Service) RejectProposalForTenant(ctx context.Context, tenantID contracts.TenantID, proposalID contracts.ProposalID, actorID string, reason string) (Proposal, error) {
	return s.updateProposalForTenant(ctx, tenantID, proposalID, actorID, "agent.package.proposal.reject", func(proposal *Proposal) error {
		if proposal.Status != contracts.ProposalPendingReview {
			return fmt.Errorf("proposal %s is not pending review", proposalID)
		}
		proposal.Status = contracts.ProposalRejected
		proposal.ReviewedBy = actorID
		if reason != "" {
			proposal.Reason = reason
		}
		return nil
	})
}

func (s *Service) PublishProposalForTenant(ctx context.Context, tenantID contracts.TenantID, proposalID contracts.ProposalID, actorID string) (contracts.AgentPackageVersion, error) {
	proposal, ok, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if !ok {
		return contracts.AgentPackageVersion{}, fmt.Errorf("proposal %s not found", proposalID)
	}
	if proposal.TenantID != tenantID {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "proposal tenant does not match caller tenant", nil)
	}
	if proposal.Status != contracts.ProposalApproved {
		return contracts.AgentPackageVersion{}, fmt.Errorf("proposal %s is not approved", proposalID)
	}
	release, err := s.PublishDraftForTenant(ctx, tenantID, proposal.DraftID, actorID)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	_, err = s.updateProposalForTenant(ctx, tenantID, proposalID, actorID, "agent.package.proposal.publish", func(proposal *Proposal) error {
		proposal.Status = contracts.ProposalPublished
		return nil
	})
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	return release, nil
}

func (s *Service) ValidateDraft(ctx context.Context, draftID string) (Draft, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return Draft{}, err
	}
	if !ok {
		return Draft{}, fmt.Errorf("draft %s not found", draftID)
	}
	if _, err := Compile(draft.AgentID, draft.Version, draft.Source); err != nil {
		return Draft{}, err
	}
	return s.patch(ctx, draftID, "system", func(draft *Draft) {
		draft.Status = contracts.ReleaseValidated
	})
}

func (s *Service) ValidateDraftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (Draft, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return Draft{}, err
	}
	if !ok {
		return Draft{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.TenantID != tenantID {
		return Draft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	if _, err := Compile(draft.AgentID, draft.Version, draft.Source); err != nil {
		return Draft{}, err
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Status = contracts.ReleaseValidated
	})
}

func (s *Service) MarkReviewed(ctx context.Context, draftID string, actorID string) (Draft, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return Draft{}, err
	}
	if !ok {
		return Draft{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.Status != contracts.ReleaseValidated && draft.Status != contracts.ReleaseEvaluated {
		return Draft{}, fmt.Errorf("draft %s is not ready for review", draftID)
	}
	return s.patch(ctx, draftID, actorID, func(draft *Draft) {
		draft.Status = contracts.ReleaseReviewed
	})
}

func (s *Service) MarkReviewedForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (Draft, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return Draft{}, err
	}
	if !ok {
		return Draft{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.TenantID != tenantID {
		return Draft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	if draft.Status != contracts.ReleaseValidated && draft.Status != contracts.ReleaseEvaluated {
		return Draft{}, fmt.Errorf("draft %s is not ready for review", draftID)
	}
	return s.patchForTenant(ctx, tenantID, draftID, actorID, func(draft *Draft) {
		draft.Status = contracts.ReleaseReviewed
	})
}

func (s *Service) PublishDraft(ctx context.Context, draftID string, actorID string) (contracts.AgentPackageVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.drafts[draftID]
	var err error
	if !ok && s.store != nil {
		s.mu.Unlock()
		draft, ok, err = s.store.GetDraft(ctx, draftID)
		s.mu.Lock()
		if err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	if !ok {
		return contracts.AgentPackageVersion{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.Status != contracts.ReleaseValidated && draft.Status != contracts.ReleaseEvaluated && draft.Status != contracts.ReleaseReviewed {
		return contracts.AgentPackageVersion{}, fmt.Errorf("draft %s is not ready to publish", draftID)
	}
	exists, err := s.releaseVersionExistsLocked(ctx, draft.TenantID, draft.AgentID, draft.Version)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if exists {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodePackageVersionConflict, "agent package version already exists", map[string]any{
			"tenant_id": draft.TenantID,
			"agent_id":  draft.AgentID,
			"version":   draft.Version,
		})
	}
	sourceHash, err := hash.StableJSON(draft.Source)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	compiled, err := Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	compiled.TenantID = draft.TenantID
	packageVersionID := contracts.PackageVersionID(idgen.New("pkgver"))
	compiled.PackageVersionID = packageVersionID
	strategyHash, err := hash.StableJSON(compiled.Strategies)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	compiledHash, err := hash.StableJSON(compiled)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	now := s.now()
	release := contracts.AgentPackageVersion{
		PackageVersionID:  packageVersionID,
		TenantID:          draft.TenantID,
		AgentID:           draft.AgentID,
		Version:           draft.Version,
		Status:            contracts.ReleasePublished,
		SourceHash:        sourceHash,
		CompiledHash:      compiledHash,
		StrategyHash:      strategyHash,
		SourceKind:        compiled.SourceKind,
		SourceProviderID:  compiled.SourceProviderID,
		ManifestVersion:   compiled.ManifestVersion,
		ManifestHash:      compiled.ManifestHash,
		CarrierKind:       compiled.CarrierKind,
		RuntimeContract:   compiled.RuntimeContract,
		ConformanceStatus: compiled.ConformanceStatus,
		CreatedBy:         draft.CreatedBy,
		CreatedAt:         draft.CreatedAt,
		PublishedAt:       &now,
	}
	s.releases[release.PackageVersionID] = release
	draft.Status = contracts.ReleasePublished
	draft.UpdatedAt = now
	s.drafts[draftID] = draft
	if s.store != nil {
		if err := s.store.SaveRelease(ctx, release, draft.Source, compiled); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
		if err := s.store.SaveDraft(ctx, draft); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	s.auditEvent(ctx, draft.TenantID, actorID, contracts.AuditAgentPackagePublish, string(release.PackageVersionID), "allowed", "")
	return release, nil
}

func (s *Service) PublishDraftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (contracts.AgentPackageVersion, error) {
	draft, ok, err := s.getDraft(ctx, draftID)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if !ok {
		return contracts.AgentPackageVersion{}, fmt.Errorf("draft %s not found", draftID)
	}
	if draft.TenantID != tenantID {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	return s.PublishDraft(ctx, draftID, actorID)
}

func (s *Service) MarkCanary(ctx context.Context, packageVersionID contracts.PackageVersionID, actorID string) (contracts.AgentPackageVersion, error) {
	return s.MarkCanaryWithRule(ctx, packageVersionID, actorID, 0, nil)
}

func (s *Service) MarkCanaryWithRule(ctx context.Context, packageVersionID contracts.PackageVersionID, actorID string, percent int, scope []string) (contracts.AgentPackageVersion, error) {
	release, err := s.markRelease(ctx, packageVersionID, contracts.ReleaseCanary, actorID, "agent.package.canary")
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if percent > 0 {
		release.CanaryPercent = percent
	}
	release.CanaryScope = cleanScope(scope)
	s.mu.Lock()
	s.releases[packageVersionID] = release
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.UpdateReleaseCanary(ctx, packageVersionID, contracts.ReleaseCanary, release.CanaryPercent, release.CanaryScope); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	return release, nil
}

func (s *Service) MarkStable(ctx context.Context, packageVersionID contracts.PackageVersionID, actorID string) (contracts.AgentPackageVersion, error) {
	s.mu.RLock()
	passed := s.evalPass[packageVersionID]
	s.mu.RUnlock()
	if !passed && s.store != nil {
		if release, ok, err := s.store.GetRelease(ctx, packageVersionID); err != nil {
			return contracts.AgentPackageVersion{}, err
		} else if ok && release.Status == contracts.ReleaseEvaluated {
			passed = true
		}
	}
	if !passed {
		return contracts.AgentPackageVersion{}, fmt.Errorf("package version %s cannot become stable before passing eval", packageVersionID)
	}
	return s.markRelease(ctx, packageVersionID, contracts.ReleaseStable, actorID, "agent.package.stable")
}

func (s *Service) MarkEvalResult(ctx context.Context, packageVersionID contracts.PackageVersionID, passed bool, actorID string, reason string) (contracts.AgentPackageVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, ok := s.releases[packageVersionID]
	var err error
	if !ok && s.store != nil {
		s.mu.Unlock()
		release, ok, err = s.store.GetRelease(ctx, packageVersionID)
		s.mu.Lock()
		if err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	if !ok {
		return contracts.AgentPackageVersion{}, fmt.Errorf("package version %s not found", packageVersionID)
	}
	statusChanged := false
	if passed && release.Status != contracts.ReleaseStable {
		if err := validateReleaseTransition(release.Status, contracts.ReleaseEvaluated); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
		release.Status = contracts.ReleaseEvaluated
		s.releases[packageVersionID] = release
		statusChanged = true
	}
	s.evalPass[packageVersionID] = passed
	if s.store != nil {
		if err := s.store.MarkEvalResult(ctx, packageVersionID, passed); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
		if statusChanged {
			if err := s.store.UpdateReleaseStatus(ctx, packageVersionID, release.Status); err != nil {
				return contracts.AgentPackageVersion{}, err
			}
		}
	}
	decision := "denied"
	if passed {
		decision = "allowed"
	}
	s.auditEvent(ctx, release.TenantID, actorID, "agent.package.eval", string(packageVersionID), decision, reason)
	return release, nil
}

func (s *Service) Rollback(ctx context.Context, packageVersionID contracts.PackageVersionID, actorID string, reason string) (contracts.AgentPackageVersion, error) {
	release, err := s.markRelease(ctx, packageVersionID, contracts.ReleaseRolledBack, actorID, contracts.AuditAgentPackageRollback)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	s.auditEvent(ctx, release.TenantID, actorID, contracts.AuditAgentPackageRollback, string(packageVersionID), "allowed", reason)
	return release, nil
}

func (s *Service) GetRelease(packageVersionID contracts.PackageVersionID) (contracts.AgentPackageVersion, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	release, ok := s.releases[packageVersionID]
	if !ok && s.store != nil {
		release, ok, _ = s.store.GetRelease(context.Background(), packageVersionID)
	}
	return release, ok
}

func (s *Service) GetDraft(ctx context.Context, draftID string) (Draft, bool, error) {
	return s.getDraft(ctx, draftID)
}

func (s *Service) ListDrafts(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]Draft, error) {
	if s.store != nil {
		return s.store.ListDrafts(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Draft, 0)
	for _, draft := range s.drafts {
		if draft.TenantID != tenantID {
			continue
		}
		if agentID != "" && draft.AgentID != agentID {
			continue
		}
		out = append(out, draft)
	}
	return out, nil
}

func (s *Service) GetProposal(ctx context.Context, proposalID contracts.ProposalID) (Proposal, bool, error) {
	return s.getProposal(ctx, proposalID)
}

func (s *Service) ListReleases() []contracts.AgentPackageVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store != nil {
		if releases, err := s.store.ListReleases(context.Background()); err == nil {
			return releases
		}
	}
	out := make([]contracts.AgentPackageVersion, 0, len(s.releases))
	for _, release := range s.releases {
		out = append(out, release)
	}
	return out
}

func (s *Service) RecordCanaryHit(ctx context.Context, hit contracts.CanaryHit) error {
	if hit.HitID == "" {
		hit.HitID = contracts.CanaryHitID(idgen.New("canaryhit"))
	}
	if hit.CreatedAt.IsZero() {
		hit.CreatedAt = s.now()
	}
	if s.store != nil {
		return s.store.RecordCanaryHit(ctx, hit)
	}
	return nil
}

func (s *Service) ListCanaryHits(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]contracts.CanaryHit, error) {
	if s.store != nil {
		return s.store.ListCanaryHits(ctx, tenantID, agentID)
	}
	return nil, nil
}

func (s *Service) GetPromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (PromptProfileProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok {
		return PromptProfileProjection{}, false, nil
	}
	return store.GetPromptProfileProjection(ctx, tenantID, agentID, version, strings.TrimSpace(draftID))
}

func (s *Service) GetActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (PromptProfileProjection, bool, error) {
	if store, ok := s.store.(PromptProfileStore); ok {
		return store.GetActivePromptProfileProjection(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.promptProfiles[promptProfileKey(tenantID, agentID)]
	return profile, ok, nil
}

func (s *Service) UpsertPromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, identityPrompt string, systemPrompt string, developerPrompt string, agentsMD string, actorID string) (PromptProfileProjection, error) {
	if tenantID == "" || agentID == "" {
		return PromptProfileProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt profile requires tenant_id and agent_id", nil)
	}
	if version == "" {
		return PromptProfileProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt profile requires agent version", map[string]any{"agent_id": agentID})
	}
	if strings.TrimSpace(identityPrompt) == "" && strings.TrimSpace(agentsMD) == "" {
		return PromptProfileProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt profile requires identity_prompt or agents_md", map[string]any{"agent_id": agentID})
	}
	now := s.now()
	profile := PromptProfileProjection{
		TenantID:        tenantID,
		AgentID:         agentID,
		Version:         version,
		SourceKind:      PromptProfileSourceKind,
		SourceID:        string(agentID),
		Status:          contracts.ReleaseStable,
		IdentityPrompt:  identityPrompt,
		SystemPrompt:    systemPrompt,
		DeveloperPrompt: developerPrompt,
		AgentsMD:        agentsMD,
		UpdatedAt:       now,
	}
	if store, ok := s.store.(PromptProfileStore); ok {
		if err := store.UpsertPromptProfileProjection(ctx, profile); err != nil {
			return PromptProfileProjection{}, err
		}
	} else {
		s.mu.Lock()
		s.promptProfiles[promptProfileKey(tenantID, agentID)] = profile
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.prompt_profile.upsert", string(agentID), "allowed", "")
	return profile, nil
}

func (s *Service) DeleteActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, actorID string) error {
	if tenantID == "" || agentID == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "prompt profile delete requires tenant_id and agent_id", nil)
	}
	if store, ok := s.store.(PromptProfileStore); ok {
		if err := store.DeleteActivePromptProfileProjection(ctx, tenantID, agentID); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		delete(s.promptProfiles, promptProfileKey(tenantID, agentID))
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.prompt_profile.delete", string(agentID), "allowed", "")
	return nil
}

func (s *Service) GetToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (ToolBindingProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok {
		return ToolBindingProjection{}, false, nil
	}
	return store.GetToolBindingProjection(ctx, tenantID, agentID, version, strings.TrimSpace(draftID))
}

func (s *Service) GetActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (ToolBindingProjection, bool, error) {
	if store, ok := s.store.(ToolBindingStore); ok {
		return store.GetActiveToolBindingProjection(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.toolBindings[promptProfileKey(tenantID, agentID)]
	return binding, ok, nil
}

func (s *Service) UpsertToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, bindings contracts.AgentToolsConfig, actorID string) (ToolBindingProjection, error) {
	if tenantID == "" || agentID == "" {
		return ToolBindingProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool binding requires tenant_id and agent_id", nil)
	}
	if version == "" {
		return ToolBindingProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool binding requires agent version", map[string]any{"agent_id": agentID})
	}
	if err := validateToolBindings(bindings); err != nil {
		return ToolBindingProjection{}, err
	}
	now := s.now()
	binding := ToolBindingProjection{
		TenantID:   tenantID,
		AgentID:    agentID,
		Version:    version,
		SourceKind: ToolBindingSourceKind,
		SourceID:   string(agentID),
		Status:     contracts.ReleaseStable,
		Bindings:   bindings,
		UpdatedAt:  now,
	}
	if store, ok := s.store.(ToolBindingStore); ok {
		if err := store.UpsertToolBindingProjection(ctx, binding); err != nil {
			return ToolBindingProjection{}, err
		}
	} else {
		s.mu.Lock()
		s.toolBindings[promptProfileKey(tenantID, agentID)] = binding
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.tool_binding.upsert", string(agentID), "allowed", "")
	return binding, nil
}

func (s *Service) DeleteActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, actorID string) error {
	if tenantID == "" || agentID == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool binding delete requires tenant_id and agent_id", nil)
	}
	if store, ok := s.store.(ToolBindingStore); ok {
		if err := store.DeleteActiveToolBindingProjection(ctx, tenantID, agentID); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		delete(s.toolBindings, promptProfileKey(tenantID, agentID))
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.tool_binding.delete", string(agentID), "allowed", "")
	return nil
}

func (s *Service) ListSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]SkillDefinitionProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok || (version == "" && strings.TrimSpace(draftID) == "") {
		return nil, false, nil
	}
	skills, err := store.ListSkillDefinitionProjections(ctx, tenantID, agentID, version, strings.TrimSpace(draftID))
	return skills, true, err
}

func (s *Service) GetSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, skillID string) (SkillDefinitionProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok {
		return SkillDefinitionProjection{}, false, nil
	}
	return store.GetSkillDefinitionProjection(ctx, tenantID, agentID, version, strings.TrimSpace(draftID), strings.TrimSpace(skillID))
}

func (s *Service) ListActiveSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]SkillDefinitionProjection, error) {
	if store, ok := s.store.(SkillDefinitionStore); ok {
		return store.ListActiveSkillDefinitionProjections(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SkillDefinitionProjection, 0)
	prefix := promptProfileKey(tenantID, agentID) + "\x00"
	for key, skill := range s.skills {
		if strings.HasPrefix(key, prefix) && skill.Status == contracts.ReleaseStable {
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SkillID == out[j].SkillID {
			return out[i].SkillVersion < out[j].SkillVersion
		}
		return out[i].SkillID < out[j].SkillID
	})
	return out, nil
}

func (s *Service) GetActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) (SkillDefinitionProjection, bool, error) {
	if store, ok := s.store.(SkillDefinitionStore); ok {
		return store.GetActiveSkillDefinitionProjection(ctx, tenantID, agentID, strings.TrimSpace(skillID))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := skillDefinitionKey(tenantID, agentID, strings.TrimSpace(skillID)) + "\x00"
	var newest SkillDefinitionProjection
	found := false
	for key, skill := range s.skills {
		if key != strings.TrimSuffix(prefix, "\x00") && !strings.HasPrefix(key, prefix) {
			continue
		}
		if skill.Status != contracts.ReleaseStable {
			continue
		}
		if !found || skill.UpdatedAt.After(newest.UpdatedAt) {
			newest = skill
			found = true
		}
	}
	return newest, found, nil
}

func (s *Service) UpsertSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, definition contracts.SkillDefinition, actorID string) (SkillDefinitionProjection, error) {
	if tenantID == "" || agentID == "" {
		return SkillDefinitionProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill definition requires tenant_id and agent_id", nil)
	}
	if version == "" {
		return SkillDefinitionProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill definition requires agent version", map[string]any{"agent_id": agentID})
	}
	if strings.TrimSpace(definition.Card.SkillID) == "" {
		return SkillDefinitionProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill definition requires skill_id", map[string]any{"agent_id": agentID})
	}
	if definition.Card.Version == "" {
		definition.Card.Version = "v1"
	}
	if definition.Instruction.SkillID == "" {
		definition.Instruction.SkillID = definition.Card.SkillID
	}
	if definition.Card.RiskLevel == "" {
		definition.Card.RiskLevel = contracts.RiskLow
	}
	if err := definition.Card.RiskLevel.Validate(); err != nil {
		return SkillDefinitionProjection{}, err
	}
	now := s.now()
	skill := SkillDefinitionProjection{
		TenantID:     tenantID,
		AgentID:      agentID,
		Version:      version,
		SourceKind:   SkillDefinitionSourceKind,
		SourceID:     string(agentID),
		Status:       contracts.ReleaseStable,
		SkillID:      definition.Card.SkillID,
		SkillVersion: definition.Card.Version,
		Definition:   definition,
		UpdatedAt:    now,
	}
	if store, ok := s.store.(SkillDefinitionStore); ok {
		if err := store.UpsertSkillDefinitionProjection(ctx, skill); err != nil {
			return SkillDefinitionProjection{}, err
		}
	} else {
		s.mu.Lock()
		for key, current := range s.skills {
			if current.SkillID == skill.SkillID && current.TenantID == tenantID && current.AgentID == agentID && current.Status == contracts.ReleaseStable {
				current.Status = contracts.ReleasePublished
				s.skills[key] = current
			}
		}
		s.skills[skillDefinitionKey(tenantID, agentID, skill.SkillID, skill.SkillVersion)] = skill
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.skill_definition.upsert", string(agentID)+"/"+skill.SkillID, "allowed", "")
	return skill, nil
}

func (s *Service) DeleteActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, actorID string) error {
	if tenantID == "" || agentID == "" || strings.TrimSpace(skillID) == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill delete requires tenant_id, agent_id and skill_id", nil)
	}
	if store, ok := s.store.(SkillDefinitionStore); ok {
		if err := store.DeleteActiveSkillDefinitionProjection(ctx, tenantID, agentID, strings.TrimSpace(skillID)); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		prefix := skillDefinitionKey(tenantID, agentID, strings.TrimSpace(skillID)) + "\x00"
		for key, skill := range s.skills {
			if key == strings.TrimSuffix(prefix, "\x00") || strings.HasPrefix(key, prefix) {
				skill.Status = contracts.ReleaseDeprecated
				skill.Definition.Card.Status = "deleted"
				skill.UpdatedAt = s.now()
				s.skills[key] = skill
			}
		}
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.skill_definition.delete", string(agentID)+"/"+strings.TrimSpace(skillID), "allowed", "")
	return nil
}

func (s *Service) ListSkillDefinitionProjectionVersions(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) ([]SkillDefinitionProjection, error) {
	skillID = strings.TrimSpace(skillID)
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	if store, ok := s.store.(SkillDefinitionVersionStore); ok {
		return store.ListSkillDefinitionProjectionVersions(ctx, tenantID, agentID, skillID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SkillDefinitionProjection, 0)
	prefix := skillDefinitionKey(tenantID, agentID, skillID) + "\x00"
	for key, skill := range s.skills {
		if skillID == "" {
			if skill.TenantID == tenantID && skill.AgentID == agentID {
				out = append(out, skill)
			}
			continue
		}
		if key == strings.TrimSuffix(prefix, "\x00") || strings.HasPrefix(key, prefix) {
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == contracts.ReleaseStable
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Service) ActivateSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, skillVersion string, actorID string) (SkillDefinitionProjection, error) {
	skillID = strings.TrimSpace(skillID)
	skillVersion = strings.TrimSpace(skillVersion)
	if tenantID == "" || agentID == "" || skillID == "" || skillVersion == "" {
		return SkillDefinitionProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill activation requires tenant_id, agent_id, skill_id and skill_version", nil)
	}
	if store, ok := s.store.(SkillDefinitionVersionStore); ok {
		if err := store.ActivateSkillDefinitionProjection(ctx, tenantID, agentID, skillID, skillVersion); err != nil {
			return SkillDefinitionProjection{}, err
		}
		skill, found, err := s.GetActiveSkillDefinitionProjection(ctx, tenantID, agentID, skillID)
		if err != nil || !found {
			return SkillDefinitionProjection{}, err
		}
		s.auditEvent(ctx, tenantID, actorID, "agent.skill_definition.activate", string(agentID)+"/"+skill.SkillID+"/"+skill.SkillVersion, "allowed", "")
		return skill, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var activated SkillDefinitionProjection
	found := false
	prefix := skillDefinitionKey(tenantID, agentID, skillID) + "\x00"
	for key, skill := range s.skills {
		if key != strings.TrimSuffix(prefix, "\x00") && !strings.HasPrefix(key, prefix) {
			continue
		}
		if skill.SkillID != skillID {
			continue
		}
		if skill.SkillVersion == skillVersion {
			skill.Status = contracts.ReleaseStable
			skill.UpdatedAt = s.now()
			activated = skill
			found = true
		} else if skill.Status == contracts.ReleaseStable {
			skill.Status = contracts.ReleasePublished
		}
		s.skills[key] = skill
	}
	if !found {
		return SkillDefinitionProjection{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "skill version not found", map[string]any{"skill_id": skillID, "skill_version": skillVersion})
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.skill_definition.activate", string(agentID)+"/"+activated.SkillID+"/"+activated.SkillVersion, "allowed", "")
	return activated, nil
}

func (s *Service) ListCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]CollaboratorProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok || (version == "" && strings.TrimSpace(draftID) == "") {
		return nil, false, nil
	}
	collaborators, err := store.ListCollaboratorProjections(ctx, tenantID, agentID, version, strings.TrimSpace(draftID))
	return collaborators, true, err
}

func (s *Service) GetCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, collaboratorAgentID contracts.AgentID) (CollaboratorProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok {
		return CollaboratorProjection{}, false, nil
	}
	return store.GetCollaboratorProjection(ctx, tenantID, agentID, version, strings.TrimSpace(draftID), collaboratorAgentID)
}

func (s *Service) ListActiveCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]CollaboratorProjection, error) {
	if store, ok := s.store.(CollaboratorStore); ok {
		return store.ListActiveCollaboratorProjections(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CollaboratorProjection, 0)
	prefix := promptProfileKey(tenantID, agentID) + "\x00"
	for key, collaborator := range s.collaborators {
		if strings.HasPrefix(key, prefix) {
			out = append(out, collaborator)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CollaboratorAgentID == out[j].CollaboratorAgentID {
			return out[i].CollaboratorVersion < out[j].CollaboratorVersion
		}
		return out[i].CollaboratorAgentID < out[j].CollaboratorAgentID
	})
	return out, nil
}

func (s *Service) GetActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) (CollaboratorProjection, bool, error) {
	if store, ok := s.store.(CollaboratorStore); ok {
		return store.GetActiveCollaboratorProjection(ctx, tenantID, agentID, collaboratorAgentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	collaborator, ok := s.collaborators[collaboratorKey(tenantID, agentID, collaboratorAgentID)]
	return collaborator, ok, nil
}

func (s *Service) UpsertCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, collaborator contracts.AgentCollaboratorRef, actorID string) (CollaboratorProjection, error) {
	if tenantID == "" || agentID == "" {
		return CollaboratorProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator requires tenant_id and agent_id", nil)
	}
	if version == "" {
		return CollaboratorProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator requires agent version", map[string]any{"agent_id": agentID})
	}
	if err := validateCollaborators([]contracts.AgentCollaboratorRef{collaborator}); err != nil {
		return CollaboratorProjection{}, err
	}
	now := s.now()
	projection := CollaboratorProjection{
		TenantID:            tenantID,
		AgentID:             agentID,
		Version:             version,
		SourceKind:          CollaboratorSourceKind,
		SourceID:            string(agentID),
		Status:              contracts.ReleaseStable,
		CollaboratorAgentID: collaborator.AgentID,
		CollaboratorVersion: collaborator.Version,
		Collaborator:        collaborator,
		UpdatedAt:           now,
	}
	if store, ok := s.store.(CollaboratorStore); ok {
		if err := store.UpsertCollaboratorProjection(ctx, projection); err != nil {
			return CollaboratorProjection{}, err
		}
	} else {
		s.mu.Lock()
		s.collaborators[collaboratorKey(tenantID, agentID, collaborator.AgentID)] = projection
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.collaborator.upsert", string(agentID)+"/"+string(collaborator.AgentID), "allowed", "")
	return projection, nil
}

func (s *Service) DeleteActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID, actorID string) error {
	if tenantID == "" || agentID == "" || collaboratorAgentID == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator delete requires tenant_id, agent_id and collaborator_agent_id", nil)
	}
	if store, ok := s.store.(CollaboratorStore); ok {
		if err := store.DeleteActiveCollaboratorProjection(ctx, tenantID, agentID, collaboratorAgentID); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		delete(s.collaborators, collaboratorKey(tenantID, agentID, collaboratorAgentID))
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.collaborator.delete", string(agentID)+"/"+string(collaboratorAgentID), "allowed", "")
	return nil
}

func (s *Service) ListExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]ExportedToolProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok || (version == "" && strings.TrimSpace(draftID) == "") {
		return nil, false, nil
	}
	tools, err := store.ListExportedToolProjections(ctx, tenantID, agentID, version, strings.TrimSpace(draftID))
	return tools, true, err
}

func (s *Service) GetExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, toolID string) (ExportedToolProjection, bool, error) {
	store, ok := s.store.(ProjectionStore)
	if !ok {
		return ExportedToolProjection{}, false, nil
	}
	return store.GetExportedToolProjection(ctx, tenantID, agentID, version, strings.TrimSpace(draftID), strings.TrimSpace(toolID))
}

func (s *Service) ListActiveExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]ExportedToolProjection, error) {
	if store, ok := s.store.(ExportedToolStore); ok {
		return store.ListActiveExportedToolProjections(ctx, tenantID, agentID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ExportedToolProjection, 0)
	prefix := promptProfileKey(tenantID, agentID) + "\x00"
	for key, tool := range s.exportedTools {
		if strings.HasPrefix(key, prefix) {
			out = append(out, tool)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ToolID == out[j].ToolID {
			return out[i].ToolVersion < out[j].ToolVersion
		}
		return out[i].ToolID < out[j].ToolID
	})
	return out, nil
}

func (s *Service) GetActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) (ExportedToolProjection, bool, error) {
	if store, ok := s.store.(ExportedToolStore); ok {
		return store.GetActiveExportedToolProjection(ctx, tenantID, agentID, strings.TrimSpace(toolID))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tool, ok := s.exportedTools[exportedToolKey(tenantID, agentID, strings.TrimSpace(toolID))]
	return tool, ok, nil
}

func (s *Service) UpsertExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, tool contracts.AgentExportedTool, actorID string) (ExportedToolProjection, error) {
	if tenantID == "" || agentID == "" {
		return ExportedToolProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool requires tenant_id and agent_id", nil)
	}
	if version == "" {
		return ExportedToolProjection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool requires agent version", map[string]any{"agent_id": agentID})
	}
	exports := contracts.AgentExports{Tools: []contracts.AgentExportedTool{tool}}
	if err := validateExports(&exports); err != nil {
		return ExportedToolProjection{}, err
	}
	tool = exports.Tools[0]
	now := s.now()
	projection := ExportedToolProjection{
		TenantID:    tenantID,
		AgentID:     agentID,
		Version:     version,
		SourceKind:  ExportedToolSourceKind,
		SourceID:    string(agentID),
		Status:      contracts.ReleaseStable,
		ToolID:      tool.ToolID,
		ToolVersion: tool.Version,
		Tool:        tool,
		UpdatedAt:   now,
	}
	if store, ok := s.store.(ExportedToolStore); ok {
		if err := store.UpsertExportedToolProjection(ctx, projection); err != nil {
			return ExportedToolProjection{}, err
		}
	} else {
		s.mu.Lock()
		s.exportedTools[exportedToolKey(tenantID, agentID, tool.ToolID)] = projection
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.exported_tool.upsert", string(agentID)+"/"+tool.ToolID, "allowed", "")
	return projection, nil
}

func (s *Service) DeleteActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string, actorID string) error {
	if tenantID == "" || agentID == "" || strings.TrimSpace(toolID) == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "exported tool delete requires tenant_id, agent_id and tool_id", nil)
	}
	toolID = strings.TrimSpace(toolID)
	if store, ok := s.store.(ExportedToolStore); ok {
		if err := store.DeleteActiveExportedToolProjection(ctx, tenantID, agentID, toolID); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		delete(s.exportedTools, exportedToolKey(tenantID, agentID, toolID))
		s.mu.Unlock()
	}
	s.auditEvent(ctx, tenantID, actorID, "agent.exported_tool.delete", string(agentID)+"/"+toolID, "allowed", "")
	return nil
}

func promptProfileKey(tenantID contracts.TenantID, agentID contracts.AgentID) string {
	return string(tenantID) + "\x00" + string(agentID)
}

func skillDefinitionKey(tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, skillVersion ...string) string {
	key := promptProfileKey(tenantID, agentID) + "\x00" + skillID
	if len(skillVersion) > 0 && strings.TrimSpace(skillVersion[0]) != "" {
		key += "\x00" + strings.TrimSpace(skillVersion[0])
	}
	return key
}

func collaboratorKey(tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) string {
	return promptProfileKey(tenantID, agentID) + "\x00" + string(collaboratorAgentID)
}

func exportedToolKey(tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) string {
	return promptProfileKey(tenantID, agentID) + "\x00" + strings.TrimSpace(toolID)
}

func (s *Service) patch(ctx context.Context, draftID string, actorID string, fn func(*Draft)) (Draft, error) {
	return s.patchForTenant(ctx, "", draftID, actorID, fn)
}

func (s *Service) patchForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string, fn func(*Draft)) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.drafts[draftID]
	var err error
	if !ok && s.store != nil {
		s.mu.Unlock()
		draft, ok, err = s.store.GetDraft(ctx, draftID)
		s.mu.Lock()
		if err != nil {
			return Draft{}, err
		}
	}
	if !ok {
		return Draft{}, fmt.Errorf("draft %s not found", draftID)
	}
	if tenantID != "" && draft.TenantID != tenantID {
		return Draft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	fn(&draft)
	draft.UpdatedAt = s.now()
	s.drafts[draftID] = draft
	if s.store != nil {
		if err := s.store.SaveDraft(ctx, draft); err != nil {
			return Draft{}, err
		}
	}
	s.auditEvent(ctx, draft.TenantID, actorID, "agent.package.draft_update", draftID, "allowed", "")
	return draft, nil
}

func upsertCollaborator(collaborators []contracts.AgentCollaboratorRef, collaborator contracts.AgentCollaboratorRef) []contracts.AgentCollaboratorRef {
	out := make([]contracts.AgentCollaboratorRef, 0, len(collaborators)+1)
	replaced := false
	for _, current := range collaborators {
		if current.AgentID == collaborator.AgentID {
			out = append(out, collaborator)
			replaced = true
			continue
		}
		out = append(out, current)
	}
	if !replaced {
		out = append(out, collaborator)
	}
	return out
}

func upsertExportedTool(tools []contracts.AgentExportedTool, tool contracts.AgentExportedTool) []contracts.AgentExportedTool {
	out := make([]contracts.AgentExportedTool, 0, len(tools)+1)
	replaced := false
	for _, current := range tools {
		if current.ToolID == tool.ToolID {
			out = append(out, tool)
			replaced = true
			continue
		}
		out = append(out, current)
	}
	if !replaced {
		out = append(out, tool)
	}
	return out
}

func upsertSkillRef(refs []contracts.SkillDefinitionRef, ref contracts.SkillDefinitionRef) []contracts.SkillDefinitionRef {
	out := make([]contracts.SkillDefinitionRef, 0, len(refs)+1)
	replaced := false
	for _, current := range refs {
		if current.SkillID == ref.SkillID {
			out = append(out, ref)
			replaced = true
			continue
		}
		out = append(out, current)
	}
	if !replaced {
		out = append(out, ref)
	}
	return out
}

func removeSkillRef(refs []contracts.SkillDefinitionRef, skillID string, version string) []contracts.SkillDefinitionRef {
	out := make([]contracts.SkillDefinitionRef, 0, len(refs))
	for _, current := range refs {
		if current.SkillID == skillID && (version == "" || current.Version == version) {
			continue
		}
		out = append(out, current)
	}
	return out
}

func skillDefinitionFromInput(skill SkillDraftInput) contracts.SkillDefinition {
	name := skill.Name
	if skill.Name != "" {
		name = skill.Name
	}
	if name == "" {
		name = skill.SkillID
	}
	description := skill.Description
	if description == "" {
		description = name
	}
	riskLevel := skill.RiskLevel
	if riskLevel == "" {
		riskLevel = contracts.RiskLow
	}
	resources := append([]contracts.SkillResourceRef(nil), skill.Resources...)
	return contracts.SkillDefinition{
		Card: contracts.SkillCard{
			SkillID:      skill.SkillID,
			Version:      skill.Version,
			Name:         name,
			Description:  description,
			Tags:         append([]string(nil), skill.Tags...),
			WhenToUse:    append([]string(nil), skill.WhenToUse...),
			RiskLevel:    riskLevel,
			ResourceRefs: skillResourceIDs(resources),
		},
		Instruction: contracts.SkillInstruction{
			SkillID:            skill.SkillID,
			Content:            skill.Instruction,
			OutputRequirements: append([]string(nil), skill.OutputRequirements...),
			Constraints:        append([]string(nil), skill.Constraints...),
		},
		Resources:               resources,
		RecommendedTools:        append([]string(nil), skill.RecommendedTools...),
		AllowedTools:            append([]string(nil), skill.AllowedTools...),
		RecommendedMemoryReads:  append([]string(nil), skill.RecommendedMemoryReads...),
		RecommendedMemoryWrites: append([]string(nil), skill.RecommendedMemoryWrites...),
		RecommendedHandoffs:     append([]string(nil), skill.RecommendedHandoffs...),
		CompletionCriteria:      append([]string(nil), skill.CompletionCriteria...),
		OutputSchema:            cloneAnyMap(skill.OutputSchema),
	}
}

func cloneAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cleanScope(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func clonePatch(patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	out := make(map[string]any, len(patch))
	for key, value := range patch {
		out[key] = value
	}
	return out
}

func (s *Service) getProposal(ctx context.Context, proposalID contracts.ProposalID) (Proposal, bool, error) {
	s.mu.RLock()
	proposal, ok := s.proposals[proposalID]
	s.mu.RUnlock()
	if ok || s.store == nil {
		return proposal, ok, nil
	}
	return s.store.GetProposal(ctx, proposalID)
}

func (s *Service) updateProposalForTenant(ctx context.Context, tenantID contracts.TenantID, proposalID contracts.ProposalID, actorID string, action string, update func(*Proposal) error) (Proposal, error) {
	proposal, ok, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return Proposal{}, err
	}
	if !ok {
		return Proposal{}, fmt.Errorf("proposal %s not found", proposalID)
	}
	if proposal.TenantID != tenantID {
		return Proposal{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "proposal tenant does not match caller tenant", nil)
	}
	if err := update(&proposal); err != nil {
		return Proposal{}, err
	}
	proposal.UpdatedAt = s.now()
	s.mu.Lock()
	s.proposals[proposal.ProposalID] = proposal
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveProposal(ctx, proposal); err != nil {
			return Proposal{}, err
		}
	}
	s.auditEvent(ctx, tenantID, actorID, action, string(proposal.ProposalID), "allowed", proposal.Reason)
	return proposal, nil
}

func (s *Service) markRelease(ctx context.Context, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus, actorID string, action string) (contracts.AgentPackageVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, ok := s.releases[packageVersionID]
	var err error
	if !ok && s.store != nil {
		s.mu.Unlock()
		release, ok, err = s.store.GetRelease(ctx, packageVersionID)
		s.mu.Lock()
		if err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	if !ok {
		return contracts.AgentPackageVersion{}, fmt.Errorf("package version %s not found", packageVersionID)
	}
	if err := validateReleaseTransition(release.Status, status); err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	release.Status = status
	s.releases[packageVersionID] = release
	if s.store != nil {
		if err := s.store.UpdateReleaseStatus(ctx, packageVersionID, status); err != nil {
			return contracts.AgentPackageVersion{}, err
		}
	}
	s.auditEvent(ctx, release.TenantID, actorID, action, string(packageVersionID), "allowed", "")
	return release, nil
}

func validateReleaseTransition(current contracts.ReleaseStatus, next contracts.ReleaseStatus) error {
	switch next {
	case contracts.ReleaseCanary:
		if current == contracts.ReleasePublished || current == contracts.ReleaseEvaluated || current == contracts.ReleaseReviewed {
			return nil
		}
	case contracts.ReleaseEvaluated:
		if current == contracts.ReleasePublished || current == contracts.ReleaseCanary || current == contracts.ReleaseEvaluated || current == contracts.ReleaseReviewed {
			return nil
		}
	case contracts.ReleaseStable:
		if current == contracts.ReleaseEvaluated || current == contracts.ReleaseCanary {
			return nil
		}
	case contracts.ReleaseRolledBack:
		if current != contracts.ReleaseRolledBack && current != contracts.ReleaseDraft {
			return nil
		}
	default:
		return nil
	}
	return fmt.Errorf("invalid release transition from %s to %s", current, next)
}

func (s *Service) releaseVersionExistsLocked(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (bool, error) {
	for _, release := range s.releases {
		if release.TenantID == tenantID && release.AgentID == agentID && release.Version == version {
			return true, nil
		}
	}
	if s.store == nil {
		return false, nil
	}
	s.mu.Unlock()
	releases, err := s.store.ListReleases(ctx)
	s.mu.Lock()
	if err != nil {
		return false, err
	}
	for _, release := range releases {
		if release.TenantID == tenantID && release.AgentID == agentID && release.Version == version {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) getDraft(ctx context.Context, draftID string) (Draft, bool, error) {
	s.mu.RLock()
	draft, ok := s.drafts[draftID]
	s.mu.RUnlock()
	if ok || s.store == nil {
		return draft, ok, nil
	}
	return s.store.GetDraft(ctx, draftID)
}

func (s *Service) auditEvent(ctx context.Context, tenantID contracts.TenantID, actorID string, action string, resourceID string, decision string, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    "optimizer",
		Action:       action,
		ResourceType: "agent_package",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		CreatedAt:    s.now(),
	})
}
