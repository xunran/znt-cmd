package contracts

import (
	"fmt"
	"time"
)

type PolicySet struct {
	PolicySetID PolicySetID `json:"policy_set_id"`
	TenantID    TenantID    `json:"tenant_id"`

	Version string `json:"version"`

	RuntimePolicy           RuntimePolicy           `json:"runtime_policy"`
	ToolPolicy              ToolPolicy              `json:"tool_policy"`
	ToolRepairPolicy        ToolRepairPolicy        `json:"tool_repair_policy"`
	ApprovalPolicy          ApprovalPolicy          `json:"approval_policy"`
	PromptPolicy            PromptPolicy            `json:"prompt_policy"`
	ContextGovernancePolicy ContextGovernancePolicy `json:"context_governance_policy,omitempty"`
	RecoveryPolicy          TaskRecoveryPolicy      `json:"recovery_policy"`
	TaskUpgradePolicy       TaskUpgradePolicy       `json:"task_upgrade_policy"`
	HandoffPolicy           HandoffPolicy           `json:"handoff_policy"`
	ReleasePolicy           ReleasePolicy           `json:"release_policy"`
	MemoryPolicy            MemoryPolicy            `json:"memory_policy"`
	ArtifactPolicy          ArtifactPolicy          `json:"artifact_policy"`

	CreatedAt time.Time `json:"created_at"`
}

type PolicyDraft struct {
	DraftID     string        `json:"draft_id"`
	TenantID    TenantID      `json:"tenant_id"`
	PolicySetID PolicySetID   `json:"policy_set_id"`
	Version     string        `json:"version"`
	Policy      PolicySet     `json:"policy"`
	Status      ReleaseStatus `json:"status"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type PolicyVersion struct {
	PolicyVersionID PolicyVersionID `json:"policy_version_id"`
	TenantID        TenantID        `json:"tenant_id"`
	PolicySetID     PolicySetID     `json:"policy_set_id"`
	Version         string          `json:"version"`
	Status          ReleaseStatus   `json:"status"`
	PolicyHash      string          `json:"policy_hash"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
}

type RuntimePolicy struct {
	MaxSteps                   int `json:"max_steps,omitempty"`
	MaxToolCalls               int `json:"max_tool_calls,omitempty"`
	MaxModelRetries            int `json:"max_model_retries,omitempty"`
	MaxRepairAttempts          int `json:"max_repair_attempts,omitempty"`
	MaxConsecutiveToolFailures int `json:"max_consecutive_tool_failures,omitempty"`
}

type ToolPolicy struct {
	AllowedToolIDs             []string  `json:"allowed_tool_ids,omitempty"`
	AllowedToolGroupIDs        []string  `json:"allowed_tool_group_ids,omitempty"`
	DeniedToolIDs              []string  `json:"denied_tool_ids,omitempty"`
	DeniedToolGroupIDs         []string  `json:"denied_tool_group_ids,omitempty"`
	RequireApprovalAtRiskLevel RiskLevel `json:"require_approval_at_risk_level,omitempty"`
}

type ToolRepairPolicy struct {
	Enabled                  bool      `json:"enabled"`
	MaxRepairAttempts        int       `json:"max_repair_attempts,omitempty"`
	RepairableErrorCodes     []string  `json:"repairable_error_codes,omitempty"`
	StopOnDenied             bool      `json:"stop_on_denied"`
	StopAtOrAboveRiskLevel   RiskLevel `json:"stop_at_or_above_risk_level,omitempty"`
	RequestModelRepairOnFail bool      `json:"request_model_repair_on_fail"`
}

type ApprovalPolicy struct {
	RequireApprovalForHighRisk      bool `json:"require_approval_for_high_risk"`
	RequireApprovalForExternalWrite bool `json:"require_approval_for_external_write"`
}

type PromptPolicy struct {
	MaxPromptTokens int      `json:"max_prompt_tokens,omitempty"`
	BlockedPhrases  []string `json:"blocked_phrases,omitempty"`
}

type TaskRecoveryPolicy struct {
	AllowResumeFromEvents bool `json:"allow_resume_from_events"`
}

type TaskUpgradePolicy struct {
	Enabled           bool         `json:"enabled"`
	TargetVersion     AgentVersion `json:"target_version,omitempty"`
	MinTaskAgeSeconds int          `json:"min_task_age_seconds,omitempty"`
}

type HandoffPolicy struct {
	DefaultMode      HandoffMode `json:"default_mode"`
	AllowFullContext bool        `json:"allow_full_context"`
	MaxContextTokens int         `json:"max_context_tokens"`

	RequireApprovalForCrossAgent         bool `json:"require_approval_for_cross_agent"`
	RequireApprovalForSensitiveArtifacts bool `json:"require_approval_for_sensitive_artifacts"`

	AllowParentTaskQuery bool `json:"allow_parent_task_query"`
	AllowArtifactRead    bool `json:"allow_artifact_read"`
	AllowMemoryRead      bool `json:"allow_memory_read"`
	AllowTaskEventRead   bool `json:"allow_task_event_read"`
}

type ReleasePolicy struct {
	RequireRollbackReason           bool            `json:"require_rollback_reason"`
	DefaultCanaryPercent            int             `json:"default_canary_percent,omitempty"`
	MaxCanaryPercent                int             `json:"max_canary_percent,omitempty"`
	MaxCanaryPercentWithoutApproval int             `json:"max_canary_percent_without_approval,omitempty"`
	RequireApprovalForStable        bool            `json:"require_approval_for_stable"`
	RequireCanaryBeforeStable       bool            `json:"require_canary_before_stable"`
	AllowedWindowsUTC               []ReleaseWindow `json:"allowed_windows_utc,omitempty"`
}

type ReleaseWindow struct {
	Days         []string `json:"days,omitempty"`
	StartHourUTC int      `json:"start_hour_utc"`
	EndHourUTC   int      `json:"end_hour_utc"`
}

type MemoryPolicy struct {
	AllowWrite bool     `json:"allow_write"`
	AllowRead  bool     `json:"allow_read"`
	Scopes     []string `json:"scopes,omitempty"`
}

type ArtifactPolicy struct {
	AllowRead   bool `json:"allow_read"`
	AllowDelete bool `json:"allow_delete"`
}

type PolicyDecisionType string

const (
	PolicyDecisionAllowed          PolicyDecisionType = "allowed"
	PolicyDecisionDenied           PolicyDecisionType = "denied"
	PolicyDecisionApprovalRequired PolicyDecisionType = "approval_required"
)

type PolicyDecision struct {
	Decision         PolicyDecisionType `json:"decision"`
	Reason           string             `json:"reason,omitempty"`
	RiskLevel        RiskLevel          `json:"risk_level"`
	AppliedPolicyIDs []string           `json:"applied_policy_ids,omitempty"`
}

func (v PolicyDecisionType) Validate() error {
	switch v {
	case PolicyDecisionAllowed, PolicyDecisionDenied, PolicyDecisionApprovalRequired:
		return nil
	default:
		return fmt.Errorf("unknown policy decision %q", v)
	}
}
