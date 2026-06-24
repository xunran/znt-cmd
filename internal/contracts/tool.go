package contracts

import "time"

type ToolDefinition struct {
	ToolID      string `json:"tool_id"`
	GroupID     string `json:"group_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`

	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`

	RiskLevel RiskLevel `json:"risk_level"`

	Visibility ToolVisibility `json:"visibility"`

	ExecutionProfile string `json:"execution_profile"`

	Version string `json:"version"`
}

type ToolCard struct {
	ToolID      string         `json:"tool_id"`
	GroupID     string         `json:"group_id,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	WhenToUse   []string       `json:"when_to_use,omitempty"`
	RiskLevel   RiskLevel      `json:"risk_level"`
	Visibility  ToolVisibility `json:"visibility"`
	Version     string         `json:"version"`
}

type ToolCall struct {
	ToolCallID ToolCallID `json:"tool_call_id"`
	TenantID   TenantID   `json:"tenant_id,omitempty"`
	ToolID     string     `json:"tool_id"`
	Name       string     `json:"name"`

	ToolVersion      string `json:"tool_version,omitempty"`
	ExecutionProfile string `json:"execution_profile,omitempty"`

	Arguments map[string]any `json:"arguments"`

	TraceID    TraceID    `json:"trace_id,omitempty"`
	RunID      AgentRunID `json:"run_id"`
	TaskID     TaskID     `json:"task_id,omitempty"`
	PlanStepID string     `json:"plan_step_id,omitempty"`

	IdempotencyKey string `json:"idempotency_key"`

	CreatedAt time.Time `json:"created_at"`

	RuntimeContext map[string]any `json:"-"`
}

type ToolResult struct {
	ToolResultID ToolResultID `json:"tool_result_id"`
	ToolCallID   ToolCallID   `json:"tool_call_id"`

	Status ToolResultStatus `json:"status"`

	Output map[string]any      `json:"output,omitempty"`
	Error  *ToolExecutionError `json:"error,omitempty"`

	ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`

	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type ToolExecutionError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ToolResultSummary struct {
	ToolCallID ToolCallID       `json:"tool_call_id"`
	Status     ToolResultStatus `json:"status"`
	Summary    string           `json:"summary"`
}
