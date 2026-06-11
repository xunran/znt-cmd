package contracts

import "fmt"

type DecisionType string

const (
	DecisionTypeReply            DecisionType = "reply"
	DecisionTypeNoOp             DecisionType = "no_op"
	DecisionTypeAskClarification DecisionType = "ask_clarification"
	DecisionTypeToolCall         DecisionType = "tool_call"
	DecisionTypeUnsupported      DecisionType = "unsupported"
	DecisionTypeError            DecisionType = "error"
)

type ReplyKind string

const (
	ReplyAnswer               ReplyKind = "answer"
	ReplyRefusal              ReplyKind = "refusal"
	ReplyPolicyNotice         ReplyKind = "policy_notice"
	ReplyClarificationMessage ReplyKind = "clarification_message"
	ReplyStatusUpdate         ReplyKind = "status_update"
)

type TaskStatus string

const (
	TaskCreated         TaskStatus = "created"
	TaskAccepted        TaskStatus = "accepted"
	TaskPlanning        TaskStatus = "planning"
	TaskRunning         TaskStatus = "running"
	TaskWaitingInput    TaskStatus = "waiting_input"
	TaskWaitingTool     TaskStatus = "waiting_tool"
	TaskWaitingApproval TaskStatus = "waiting_approval"
	TaskBlocked         TaskStatus = "blocked"
	TaskPaused          TaskStatus = "paused"
	TaskCompleted       TaskStatus = "completed"
	TaskFailed          TaskStatus = "failed"
	TaskCancelled       TaskStatus = "cancelled"
	TaskRejected        TaskStatus = "rejected"
)

type RunStatus string

const (
	RunCreated         RunStatus = "created"
	RunRunning         RunStatus = "running"
	RunWaitingInput    RunStatus = "waiting_input"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunCompleted       RunStatus = "completed"
	RunFailed          RunStatus = "failed"
	RunCancelled       RunStatus = "cancelled"
)

type ToolResultStatus string

const (
	ToolResultSucceeded       ToolResultStatus = "succeeded"
	ToolResultFailed          ToolResultStatus = "failed"
	ToolResultDenied          ToolResultStatus = "denied"
	ToolResultPendingApproval ToolResultStatus = "pending_approval"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type HandoffMode string

const (
	HandoffFullContext   HandoffMode = "full_context"
	HandoffSummaryOnly   HandoffMode = "summary_only"
	HandoffReferenceOnly HandoffMode = "reference_only"
	HandoffHybrid        HandoffMode = "hybrid"
)

type ReleaseStatus string

const (
	ReleaseDraft      ReleaseStatus = "draft"
	ReleaseValidated  ReleaseStatus = "validated"
	ReleaseEvaluated  ReleaseStatus = "evaluated"
	ReleaseReviewed   ReleaseStatus = "reviewed"
	ReleasePublished  ReleaseStatus = "published"
	ReleaseCanary     ReleaseStatus = "canary"
	ReleaseStable     ReleaseStatus = "stable"
	ReleaseDeprecated ReleaseStatus = "deprecated"
	ReleaseRolledBack ReleaseStatus = "rolled_back"
)

type ProposalStatus string

const (
	ProposalDraft         ProposalStatus = "draft"
	ProposalPendingReview ProposalStatus = "pending_review"
	ProposalApproved      ProposalStatus = "approved"
	ProposalRejected      ProposalStatus = "rejected"
	ProposalPublished     ProposalStatus = "published"
	ProposalSuperseded    ProposalStatus = "superseded"
)

type ToolVisibility string

const (
	ToolPrivate   ToolVisibility = "private"
	ToolProtected ToolVisibility = "protected"
	ToolExposed   ToolVisibility = "exposed"
)

type HandoffStatus string

const (
	HandoffCreated   HandoffStatus = "created"
	HandoffAccepted  HandoffStatus = "accepted"
	HandoffRejected  HandoffStatus = "rejected"
	HandoffRunning   HandoffStatus = "running"
	HandoffCompleted HandoffStatus = "completed"
	HandoffFailed    HandoffStatus = "failed"
	HandoffCancelled HandoffStatus = "cancelled"
)

type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanRunning    PlanStatus = "running"
	PlanCompleted  PlanStatus = "completed"
	PlanFailed     PlanStatus = "failed"
	PlanSuperseded PlanStatus = "superseded"
)

type PlanStepStatus string

const (
	PlanStepPending   PlanStepStatus = "pending"
	PlanStepRunning   PlanStepStatus = "running"
	PlanStepCompleted PlanStepStatus = "completed"
	PlanStepFailed    PlanStepStatus = "failed"
	PlanStepSkipped   PlanStepStatus = "skipped"
)

type TaskCommand string

const (
	CmdAccept              TaskCommand = "accept"
	CmdPlanStarted         TaskCommand = "plan_started"
	CmdRunStarted          TaskCommand = "run_started"
	CmdAskClarification    TaskCommand = "ask_clarification"
	CmdApprovalRequired    TaskCommand = "approval_required"
	CmdToolWaiting         TaskCommand = "tool_waiting"
	CmdToolCompleted       TaskCommand = "tool_completed"
	CmdComplete            TaskCommand = "complete"
	CmdFail                TaskCommand = "fail"
	CmdProvideInput        TaskCommand = "provide_input"
	CmdApproveAction       TaskCommand = "approve_action"
	CmdRejectAction        TaskCommand = "reject_action"
	CmdCancel              TaskCommand = "cancel"
	CmdPause               TaskCommand = "pause"
	CmdResume              TaskCommand = "resume"
	CmdCreatePlan          TaskCommand = "create_plan"
	CmdUpdatePlan          TaskCommand = "update_plan"
	CmdReplan              TaskCommand = "replan"
	CmdCompleteStep        TaskCommand = "complete_step"
	CmdFailStep            TaskCommand = "fail_step"
	CmdUpgradeAgentVersion TaskCommand = "upgrade_agent_version"
	CmdCreateHandoff       TaskCommand = "create_handoff"
	CmdAcceptHandoff       TaskCommand = "accept_handoff"
	CmdRejectHandoff       TaskCommand = "reject_handoff"
	CmdCompleteHandoff     TaskCommand = "complete_handoff"
	CmdFailHandoff         TaskCommand = "fail_handoff"
)

func (v DecisionType) Validate() error {
	switch v {
	case DecisionTypeReply, DecisionTypeNoOp, DecisionTypeAskClarification, DecisionTypeToolCall, DecisionTypeUnsupported, DecisionTypeError:
		return nil
	default:
		return fmt.Errorf("unknown decision type %q", v)
	}
}

func (v TaskStatus) Validate() error {
	switch v {
	case TaskCreated, TaskAccepted, TaskPlanning, TaskRunning, TaskWaitingInput, TaskWaitingTool, TaskWaitingApproval, TaskBlocked, TaskPaused, TaskCompleted, TaskFailed, TaskCancelled, TaskRejected:
		return nil
	default:
		return fmt.Errorf("unknown task status %q", v)
	}
}

func (v RunStatus) Validate() error {
	switch v {
	case RunCreated, RunRunning, RunWaitingInput, RunWaitingApproval, RunCompleted, RunFailed, RunCancelled:
		return nil
	default:
		return fmt.Errorf("unknown run status %q", v)
	}
}

func (v RiskLevel) Validate() error {
	switch v {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return nil
	default:
		return fmt.Errorf("unknown risk level %q", v)
	}
}

func (v ToolResultStatus) Validate() error {
	switch v {
	case ToolResultSucceeded, ToolResultFailed, ToolResultDenied, ToolResultPendingApproval:
		return nil
	default:
		return fmt.Errorf("unknown tool result status %q", v)
	}
}

func (v HandoffMode) Validate() error {
	switch v {
	case HandoffFullContext, HandoffSummaryOnly, HandoffReferenceOnly, HandoffHybrid:
		return nil
	default:
		return fmt.Errorf("unknown handoff mode %q", v)
	}
}

func (v ReleaseStatus) Validate() error {
	switch v {
	case ReleaseDraft, ReleaseValidated, ReleaseEvaluated, ReleaseReviewed, ReleasePublished, ReleaseCanary, ReleaseStable, ReleaseDeprecated, ReleaseRolledBack:
		return nil
	default:
		return fmt.Errorf("unknown release status %q", v)
	}
}
