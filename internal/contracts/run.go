package contracts

import "time"

type AgentRun struct {
	RunID   AgentRunID `json:"run_id"`
	TraceID TraceID    `json:"trace_id"`

	TenantID TenantID `json:"tenant_id"`

	AgentID          AgentID             `json:"agent_id"`
	AgentVersion     AgentVersion        `json:"agent_version"`
	CarrierKind      AgentCarrierKind    `json:"carrier_kind,omitempty"`
	RuntimeContract  RuntimeContractKind `json:"runtime_contract,omitempty"`
	SourceKind       AgentSourceKind     `json:"source_kind,omitempty"`
	SourceProviderID string              `json:"source_provider_id,omitempty"`
	CarrierVersion   AgentVersion        `json:"carrier_version,omitempty"`
	ManifestHash     string              `json:"manifest_hash,omitempty"`

	TaskID TaskID `json:"task_id,omitempty"`

	Input          string `json:"input,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`

	Status RunStatus `json:"status"`

	StepCount     int `json:"step_count"`
	ToolCallCount int `json:"tool_call_count"`

	PolicySetID PolicySetID `json:"policy_set_id"`

	VersionSnapshot VersionSnapshot `json:"version_snapshot"`

	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	ErrorCode    ErrorCode `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type RunStep struct {
	StepID          string              `json:"step_id"`
	RunID           AgentRunID          `json:"run_id"`
	TaskID          TaskID              `json:"task_id,omitempty"`
	Index           int                 `json:"index"`
	CarrierKind     AgentCarrierKind    `json:"carrier_kind,omitempty"`
	RuntimeContract RuntimeContractKind `json:"runtime_contract,omitempty"`
	StartedAt       time.Time           `json:"started_at"`
}

type VersionSnapshot struct {
	ContractVersion      string              `json:"contract_version,omitempty"`
	AgentDefinition      AgentVersion        `json:"agent_definition"`
	AgentPackage         PackageVersionID    `json:"agent_package,omitempty"`
	CarrierKind          AgentCarrierKind    `json:"carrier_kind,omitempty"`
	RuntimeContract      RuntimeContractKind `json:"runtime_contract,omitempty"`
	CarrierVersion       AgentVersion        `json:"carrier_version,omitempty"`
	SourceKind           AgentSourceKind     `json:"source_kind,omitempty"`
	SourceProviderID     string              `json:"source_provider_id,omitempty"`
	ManifestVersion      string              `json:"manifest_version,omitempty"`
	ManifestHash         string              `json:"manifest_hash,omitempty"`
	StrategyHash         string              `json:"strategy_hash,omitempty"`
	PolicySet            PolicySetID         `json:"policy_set"`
	PolicyVersionID      PolicyVersionID     `json:"policy_version_id,omitempty"`
	PolicySetVersion     string              `json:"policy_set_version,omitempty"`
	ToolDefinitions      map[string]string   `json:"tool_definitions,omitempty"`
	SkillDefinitions     map[string]string   `json:"skill_definitions,omitempty"`
	ModelProvider        string              `json:"model_provider,omitempty"`
	ModelName            string              `json:"model_name,omitempty"`
	PromptBundleHash     string              `json:"prompt_bundle_hash,omitempty"`
	AdditionalAttributes map[string]string   `json:"additional_attributes,omitempty"`
}

func NormalizeRunCarrierSnapshot(run *AgentRun) {
	if run == nil {
		return
	}
	run.SourceKind = NormalizeSourceKind(run.SourceKind)
	run.CarrierKind = NormalizeCarrierKind(run.SourceKind, run.CarrierKind)
	run.RuntimeContract = NormalizeRuntimeContract(run.CarrierKind, run.RuntimeContract)
	if run.CarrierVersion == "" {
		run.CarrierVersion = run.AgentVersion
	}
	if run.VersionSnapshot.SourceKind == "" {
		run.VersionSnapshot.SourceKind = run.SourceKind
	}
	if run.VersionSnapshot.CarrierKind == "" {
		run.VersionSnapshot.CarrierKind = run.CarrierKind
	}
	if run.VersionSnapshot.RuntimeContract == "" {
		run.VersionSnapshot.RuntimeContract = run.RuntimeContract
	}
	if run.VersionSnapshot.CarrierVersion == "" {
		run.VersionSnapshot.CarrierVersion = run.CarrierVersion
	}
	if run.VersionSnapshot.SourceProviderID == "" {
		run.VersionSnapshot.SourceProviderID = run.SourceProviderID
	}
	if run.VersionSnapshot.ManifestHash == "" {
		run.VersionSnapshot.ManifestHash = run.ManifestHash
	}
	run.SourceKind = NormalizeSourceKind(run.VersionSnapshot.SourceKind)
	run.CarrierKind = NormalizeCarrierKind(run.SourceKind, run.VersionSnapshot.CarrierKind)
	run.RuntimeContract = NormalizeRuntimeContract(run.CarrierKind, run.VersionSnapshot.RuntimeContract)
	run.CarrierVersion = run.VersionSnapshot.CarrierVersion
	if run.SourceProviderID == "" {
		run.SourceProviderID = run.VersionSnapshot.SourceProviderID
	}
	if run.ManifestHash == "" {
		run.ManifestHash = run.VersionSnapshot.ManifestHash
	}
}
