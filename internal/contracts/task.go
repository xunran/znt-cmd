package contracts

import "time"

type Task struct {
	TaskID TaskID `json:"task_id"`

	TenantID TenantID `json:"tenant_id"`

	ParentTaskID *TaskID `json:"parent_task_id,omitempty"`
	RootTaskID   *TaskID `json:"root_task_id,omitempty"`

	Title       string `json:"title"`
	Objective   string `json:"objective"`
	Description string `json:"description,omitempty"`

	Status TaskStatus `json:"status"`

	OwnerAgentID    AgentID `json:"owner_agent_id,omitempty"`
	AssignedAgentID AgentID `json:"assigned_agent_id,omitempty"`

	SourceHandoffID *HandoffID `json:"source_handoff_id,omitempty"`

	AgentID      AgentID      `json:"agent_id"`
	AgentVersion AgentVersion `json:"agent_version"`
	PolicySetID  PolicySetID  `json:"policy_set_id"`

	SchemaVersion string `json:"schema_version,omitempty"`
	Version       int64  `json:"version"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type TaskEvent struct {
	EventID  TaskEventID `json:"event_id"`
	TaskID   TaskID      `json:"task_id"`
	TenantID TenantID    `json:"tenant_id,omitempty"`

	Type string `json:"type"`

	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`

	Payload map[string]any `json:"payload"`

	RunID  AgentRunID `json:"run_id,omitempty"`
	StepID string     `json:"step_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type TaskSummary struct {
	TaskID    TaskID     `json:"task_id,omitempty"`
	Status    TaskStatus `json:"status,omitempty"`
	Title     string     `json:"title,omitempty"`
	Objective string     `json:"objective,omitempty"`
}

type TaskPlan struct {
	PlanID string `json:"plan_id"`
	TaskID TaskID `json:"task_id"`

	Objective string     `json:"objective"`
	Status    PlanStatus `json:"status"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PlanStep struct {
	StepID string `json:"step_id"`
	PlanID string `json:"plan_id"`
	TaskID TaskID `json:"task_id"`

	Index       int    `json:"index"`
	Title       string `json:"title"`
	Description string `json:"description"`

	ExpectedToolHints []string `json:"expected_tool_hints,omitempty"`

	Status PlanStepStatus `json:"status"`

	ResultRefs []ArtifactRef `json:"result_refs,omitempty"`

	FailureReason string `json:"failure_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PlanEvent struct {
	EventID string `json:"event_id"`
	PlanID  string `json:"plan_id"`
	TaskID  TaskID `json:"task_id"`

	Type string `json:"type"`

	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`

	Payload map[string]any `json:"payload,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type PlanSummary struct {
	PlanID    string     `json:"plan_id"`
	Objective string     `json:"objective"`
	Status    PlanStatus `json:"status"`
}

type PlanStepSummary struct {
	StepID string         `json:"step_id"`
	Index  int            `json:"index"`
	Title  string         `json:"title"`
	Status PlanStepStatus `json:"status"`
}
