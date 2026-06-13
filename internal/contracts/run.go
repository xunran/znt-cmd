package contracts

import "time"

type AgentRun struct {
	RunID   AgentRunID `json:"run_id"`
	TraceID TraceID    `json:"trace_id"`

	TenantID TenantID `json:"tenant_id"`

	AgentID      AgentID      `json:"agent_id"`
	AgentVersion AgentVersion `json:"agent_version"`

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
	StepID    string     `json:"step_id"`
	RunID     AgentRunID `json:"run_id"`
	TaskID    TaskID     `json:"task_id,omitempty"`
	Index     int        `json:"index"`
	StartedAt time.Time  `json:"started_at"`
}

type VersionSnapshot struct {
	ContractVersion      string            `json:"contract_version,omitempty"`
	AgentDefinition      AgentVersion      `json:"agent_definition"`
	AgentPackage         PackageVersionID  `json:"agent_package,omitempty"`
	SourceKind           AgentSourceKind   `json:"source_kind,omitempty"`
	SourceProviderID     string            `json:"source_provider_id,omitempty"`
	ManifestVersion      string            `json:"manifest_version,omitempty"`
	ManifestHash         string            `json:"manifest_hash,omitempty"`
	StrategyHash         string            `json:"strategy_hash,omitempty"`
	PolicySet            PolicySetID       `json:"policy_set"`
	PolicyVersionID      PolicyVersionID   `json:"policy_version_id,omitempty"`
	PolicySetVersion     string            `json:"policy_set_version,omitempty"`
	ToolDefinitions      map[string]string `json:"tool_definitions,omitempty"`
	SkillDefinitions     map[string]string `json:"skill_definitions,omitempty"`
	ModelProvider        string            `json:"model_provider,omitempty"`
	ModelName            string            `json:"model_name,omitempty"`
	PromptBundleHash     string            `json:"prompt_bundle_hash,omitempty"`
	AdditionalAttributes map[string]string `json:"additional_attributes,omitempty"`
}
