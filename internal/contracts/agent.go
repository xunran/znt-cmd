package contracts

import "time"

type AgentPackageVersion struct {
	PackageVersionID PackageVersionID `json:"package_version_id"`
	TenantID         TenantID         `json:"tenant_id,omitempty"`
	AgentID          AgentID          `json:"agent_id"`
	Version          AgentVersion     `json:"version"`

	Status ReleaseStatus `json:"status"`

	SourceHash   string `json:"source_hash"`
	CompiledHash string `json:"compiled_hash"`
	StrategyHash string `json:"strategy_hash,omitempty"`

	SourceKind        AgentSourceKind          `json:"source_kind,omitempty"`
	SourceProviderID  string                   `json:"source_provider_id,omitempty"`
	ManifestVersion   string                   `json:"manifest_version,omitempty"`
	ManifestHash      string                   `json:"manifest_hash,omitempty"`
	CarrierKind       AgentCarrierKind         `json:"carrier_kind,omitempty"`
	RuntimeContract   RuntimeContractKind      `json:"runtime_contract,omitempty"`
	ConformanceStatus RuntimeConformanceStatus `json:"conformance_status,omitempty"`

	CanaryPercent int      `json:"canary_percent,omitempty"`
	CanaryScope   []string `json:"canary_scope,omitempty"`

	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

type CanaryHit struct {
	HitID            CanaryHitID      `json:"hit_id"`
	TenantID         TenantID         `json:"tenant_id"`
	AgentID          AgentID          `json:"agent_id"`
	RequestedVersion AgentVersion     `json:"requested_version,omitempty"`
	ResolvedVersion  AgentVersion     `json:"resolved_version"`
	PackageVersionID PackageVersionID `json:"package_version_id,omitempty"`
	RunID            AgentRunID       `json:"run_id,omitempty"`
	TraceID          TraceID          `json:"trace_id,omitempty"`
	CallerID         string           `json:"caller_id,omitempty"`
	CanaryPercent    int              `json:"canary_percent,omitempty"`
	Reason           string           `json:"reason,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
}

type AgentDefinition struct {
	TenantID TenantID `json:"tenant_id,omitempty"`

	AgentID AgentID      `json:"agent_id"`
	Version AgentVersion `json:"version"`

	PackageVersionID  PackageVersionID         `json:"package_version_id,omitempty"`
	SourceKind        AgentSourceKind          `json:"source_kind,omitempty"`
	SourceProviderID  string                   `json:"source_provider_id,omitempty"`
	ManifestVersion   string                   `json:"manifest_version,omitempty"`
	ManifestHash      string                   `json:"manifest_hash,omitempty"`
	CarrierKind       AgentCarrierKind         `json:"carrier_kind,omitempty"`
	RuntimeContract   RuntimeContractKind      `json:"runtime_contract,omitempty"`
	ConformanceStatus RuntimeConformanceStatus `json:"conformance_status,omitempty"`

	Name        string `json:"name"`
	Description string `json:"description"`

	IdentityPrompt  string `json:"identity_prompt"`
	SystemPrompt    string `json:"system_prompt"`
	DeveloperPrompt string `json:"developer_prompt,omitempty"`

	Skills []SkillDefinitionRef `json:"skills"`
	Tools  AgentToolsConfig     `json:"tools"`

	Collaborators []AgentCollaboratorRef `json:"collaborators,omitempty"`
	Exports       AgentExports           `json:"exports,omitempty"`
	RuntimeHooks  AgentRuntimeHooks      `json:"runtime_hooks,omitempty"`

	SkillDefinitions []SkillDefinition `json:"skill_definitions,omitempty"`

	PolicyRefs AgentPolicyRefs `json:"policy_refs"`
	Strategies AgentStrategies `json:"strategies,omitempty"`

	Runtime RuntimeLimits `json:"runtime"`

	ContractVersion string    `json:"contract_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type AgentDefinitionSummary struct {
	AgentID     AgentID      `json:"agent_id"`
	Version     AgentVersion `json:"version"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
}

type AgentToolsConfig struct {
	AllowedToolIDs      []string `json:"allowed_tool_ids"`
	AllowedToolGroupIDs []string `json:"allowed_tool_group_ids,omitempty"`
	ExposedToolIDs      []string `json:"exposed_tool_ids,omitempty"`
	DeniedToolIDs       []string `json:"denied_tool_ids,omitempty"`
	DeniedToolGroupIDs  []string `json:"denied_tool_group_ids,omitempty"`
}

type AgentCollaboratorRef struct {
	AgentID             AgentID       `json:"agent_id"`
	Version             AgentVersion  `json:"version,omitempty"`
	Alias               string        `json:"alias,omitempty"`
	Name                string        `json:"name,omitempty"`
	Description         string        `json:"description,omitempty"`
	WhenToUse           []string      `json:"when_to_use,omitempty"`
	Capabilities        []string      `json:"capabilities,omitempty"`
	AllowedHandoffModes []HandoffMode `json:"allowed_handoff_modes,omitempty"`
	DefaultHandoffMode  HandoffMode   `json:"default_handoff_mode,omitempty"`
	MaxContextTokens    int           `json:"max_context_tokens,omitempty"`
	RequiresApproval    bool          `json:"requires_approval,omitempty"`
	Status              string        `json:"status,omitempty"`
}

type AgentExports struct {
	Tools []AgentExportedTool `json:"tools,omitempty"`
}

type AgentRuntimeHooks struct {
	Mode  string                    `json:"mode,omitempty"`
	Hooks []AgentRuntimeHookBinding `json:"hooks,omitempty"`
}

type RuntimeHookApprovalPolicy struct {
	RequireApproval bool     `json:"require_approval,omitempty"`
	ProviderTypes   []string `json:"provider_types,omitempty"`
	Phases          []string `json:"phases,omitempty"`
	FailurePolicies []string `json:"failure_policies,omitempty"`
}

type AgentRuntimeHookBinding struct {
	HookID           string                    `json:"hook_id"`
	ProviderType     string                    `json:"provider_type,omitempty"`
	ProviderID       string                    `json:"provider_id,omitempty"`
	Phase            string                    `json:"phase"`
	Version          string                    `json:"version,omitempty"`
	Enabled          bool                      `json:"enabled"`
	TimeoutMS        int                       `json:"timeout_ms,omitempty"`
	FailurePolicy    string                    `json:"failure_policy,omitempty"`
	RequiresApproval bool                      `json:"requires_approval,omitempty"`
	ApprovalPolicy   RuntimeHookApprovalPolicy `json:"approval_policy,omitempty"`
	Config           map[string]any            `json:"config,omitempty"`
}

type AgentExportedTool struct {
	ToolID       string         `json:"tool_id"`
	GroupID      string         `json:"group_id,omitempty"`
	Operation    string         `json:"operation,omitempty"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	WhenToUse    []string       `json:"when_to_use,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	RiskLevel    RiskLevel      `json:"risk_level"`
	Visibility   ToolVisibility `json:"visibility"`
	Status       string         `json:"status,omitempty"`
	Version      string         `json:"version,omitempty"`
}

type AgentPolicyRefs struct {
	PolicySetID PolicySetID `json:"policy_set_id,omitempty"`
}

type RuntimeLimits struct {
	MaxSteps                   int           `json:"max_steps"`
	MaxToolCalls               int           `json:"max_tool_calls"`
	MaxDuration                time.Duration `json:"max_duration"`
	MaxPromptTokens            int           `json:"max_prompt_tokens"`
	MaxHandoffDepth            int           `json:"max_handoff_depth,omitempty"`
	MaxChildTasks              int           `json:"max_child_tasks,omitempty"`
	MaxRepairAttempts          int           `json:"max_repair_attempts,omitempty"`
	MaxModelRetries            int           `json:"max_model_retries,omitempty"`
	MaxConsecutiveToolFailures int           `json:"max_consecutive_tool_failures,omitempty"`
}

type SkillDefinitionRef struct {
	SkillID string `json:"skill_id"`
	Version string `json:"version"`
}

type SkillCard struct {
	SkillID string `json:"skill_id"`
	Version string `json:"version"`

	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Status      string   `json:"status,omitempty"`

	WhenToUse    []string `json:"when_to_use"`
	WhenNotToUse []string `json:"when_not_to_use,omitempty"`

	RiskLevel RiskLevel `json:"risk_level"`

	ResourceRefs []string `json:"resource_refs,omitempty"`
}

type SkillInstruction struct {
	SkillID string `json:"skill_id"`
	Content string `json:"content"`

	OutputRequirements []string `json:"output_requirements,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
}

type SkillResourceRef struct {
	ResourceID string `json:"resource_id"`
	Type       string `json:"type"`
	URI        string `json:"uri"`
	LoadPolicy string `json:"load_policy"`
}

type SkillDefinition struct {
	Card                    SkillCard          `json:"card"`
	Instruction             SkillInstruction   `json:"instruction"`
	Resources               []SkillResourceRef `json:"resources,omitempty"`
	RecommendedTools        []string           `json:"recommended_tools,omitempty"`
	AllowedTools            []string           `json:"allowed_tools,omitempty"`
	RecommendedMemoryReads  []string           `json:"recommended_memory_reads,omitempty"`
	RecommendedMemoryWrites []string           `json:"recommended_memory_writes,omitempty"`
	RecommendedHandoffs     []string           `json:"recommended_handoffs,omitempty"`
	CompletionCriteria      []string           `json:"completion_criteria,omitempty"`
	OutputSchema            map[string]any     `json:"output_schema,omitempty"`
}
