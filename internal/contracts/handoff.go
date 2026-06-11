package contracts

import "time"

type ExpectedOutput struct {
	Format       string   `json:"format,omitempty"`
	Requirements []string `json:"requirements,omitempty"`
}

type AgentHandoff struct {
	HandoffID HandoffID `json:"handoff_id"`
	TenantID  TenantID  `json:"tenant_id,omitempty"`

	ParentTaskID TaskID  `json:"parent_task_id"`
	ChildTaskID  *TaskID `json:"child_task_id,omitempty"`

	FromAgentID AgentID `json:"from_agent_id"`
	ToAgentID   AgentID `json:"to_agent_id"`

	Objective string `json:"objective"`
	Reason    string `json:"reason"`

	ContextPackageRef ContextPackageID `json:"context_package_ref"`

	ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`

	ExpectedOutput ExpectedOutput `json:"expected_output"`

	Status HandoffStatus `json:"status"`

	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type HandoffContextPackage struct {
	PackageID ContextPackageID `json:"package_id"`
	TenantID  TenantID         `json:"tenant_id,omitempty"`

	ParentTaskID TaskID     `json:"parent_task_id"`
	SourceRunID  AgentRunID `json:"source_run_id"`

	FromAgentID AgentID `json:"from_agent_id"`
	ToAgentID   AgentID `json:"to_agent_id"`

	Objective string `json:"objective"`
	Reason    string `json:"reason"`

	Summary       string   `json:"summary"`
	KeyFacts      []string `json:"key_facts"`
	Constraints   []string `json:"constraints"`
	OpenQuestions []string `json:"open_questions,omitempty"`

	ArtifactRefs   []ArtifactRef  `json:"artifact_refs,omitempty"`
	ToolResultRefs []ToolResultID `json:"tool_result_refs,omitempty"`
	MemoryRefs     []MemoryID     `json:"memory_refs,omitempty"`
	TaskEventRefs  []TaskEventID  `json:"task_event_refs,omitempty"`

	AllowedContextScopes []string `json:"allowed_context_scopes"`
	DeniedContextScopes  []string `json:"denied_context_scopes,omitempty"`

	ExpectedOutput ExpectedOutput `json:"expected_output"`

	Mode HandoffMode `json:"mode"`

	Hash string `json:"hash"`

	CreatedAt time.Time `json:"created_at"`
}

type HandoffContextSummary struct {
	PackageID ContextPackageID `json:"package_id"`
	FromAgent AgentID          `json:"from_agent_id"`
	Objective string           `json:"objective"`
	Summary   string           `json:"summary"`
	Mode      HandoffMode      `json:"mode"`
}
