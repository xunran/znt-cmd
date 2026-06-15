package contracts

import "time"

type TraceEvent struct {
	TraceID  TraceID  `json:"trace_id"`
	TenantID TenantID `json:"tenant_id,omitempty"`
	SpanID   SpanID   `json:"span_id"`

	RunID  AgentRunID `json:"run_id,omitempty"`
	TaskID TaskID     `json:"task_id,omitempty"`

	Type string `json:"type"`

	Payload map[string]any `json:"payload"`

	CreatedAt time.Time `json:"created_at"`
}

type AuditEvent struct {
	AuditID string `json:"audit_id"`

	TenantID TenantID `json:"tenant_id"`

	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`

	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`

	Decision string `json:"decision"`

	Reason string `json:"reason,omitempty"`

	TraceID TraceID    `json:"trace_id,omitempty"`
	TaskID  TaskID     `json:"task_id,omitempty"`
	RunID   AgentRunID `json:"run_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

type GovernanceReviewMode string

const (
	GovernanceReviewNone   GovernanceReviewMode = "none"
	GovernanceReviewSingle GovernanceReviewMode = "single"
	GovernanceReviewMulti  GovernanceReviewMode = "multi"
)

type GovernanceConsensusPolicy string

const (
	GovernanceConsensusAny      GovernanceConsensusPolicy = "any"
	GovernanceConsensusMajority GovernanceConsensusPolicy = "majority"
	GovernanceConsensusAll      GovernanceConsensusPolicy = "all"
)

type GovernanceEscalationPolicy string

const (
	GovernanceEscalationNone         GovernanceEscalationPolicy = "none"
	GovernanceEscalationOrchestrator GovernanceEscalationPolicy = "orchestrator"
)

type GovernanceRunStatus string

const (
	GovernanceRunActive    GovernanceRunStatus = "active"
	GovernanceRunCompleted GovernanceRunStatus = "completed"
	GovernanceRunBlocked   GovernanceRunStatus = "blocked"
	GovernanceRunCancelled GovernanceRunStatus = "cancelled"
)

type GovernanceGateStatus string

const (
	GovernanceGatePending           GovernanceGateStatus = "pending"
	GovernanceGateOpen              GovernanceGateStatus = "open"
	GovernanceGatePassed            GovernanceGateStatus = "passed"
	GovernanceGateRejected          GovernanceGateStatus = "rejected"
	GovernanceGateEscalationPending GovernanceGateStatus = "escalation_pending"
	GovernanceGateArbitrated        GovernanceGateStatus = "arbitrated"
	GovernanceGateSkipped           GovernanceGateStatus = "skipped"
)

type GovernanceReviewDecision string

const (
	GovernanceReviewApprove GovernanceReviewDecision = "approve"
	GovernanceReviewReject  GovernanceReviewDecision = "reject"
	GovernanceReviewRevise  GovernanceReviewDecision = "revise"
	GovernanceReviewAbstain GovernanceReviewDecision = "abstain"
)

type GovernanceArtifactRef struct {
	ArtifactID ArtifactID `json:"artifact_id,omitempty"`
	Type       string     `json:"type,omitempty"`
	URI        string     `json:"uri,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	Hash       string     `json:"hash,omitempty"`
}

type GovernanceEvidenceRef struct {
	EvidenceID  string         `json:"evidence_id,omitempty"`
	Type        string         `json:"type"`
	TraceID     TraceID        `json:"trace_id,omitempty"`
	AuditID     string         `json:"audit_id,omitempty"`
	ArtifactID  ArtifactID     `json:"artifact_id,omitempty"`
	TaskID      TaskID         `json:"task_id,omitempty"`
	RunID       AgentRunID     `json:"run_id,omitempty"`
	URI         string         `json:"uri,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	SubmittedBy string         `json:"submitted_by,omitempty"`
	SubmittedAt *time.Time     `json:"submitted_at,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type GovernanceActorRef struct {
	ActorID   string `json:"actor_id"`
	ActorType string `json:"actor_type"`
	Role      string `json:"role,omitempty"`
}

type GovernanceGateDefinition struct {
	GateID            string                     `json:"gate_id"`
	Name              string                     `json:"name,omitempty"`
	SubjectType       string                     `json:"subject_type,omitempty"`
	ReviewMode        GovernanceReviewMode       `json:"review_mode"`
	ConsensusPolicy   GovernanceConsensusPolicy  `json:"consensus_policy,omitempty"`
	EscalationPolicy  GovernanceEscalationPolicy `json:"escalation_policy,omitempty"`
	RequiredReviewers int                        `json:"required_reviewers,omitempty"`
	MinApprovals      int                        `json:"min_approvals,omitempty"`
	ReviewerRoles     []string                   `json:"reviewer_roles,omitempty"`
	EvidenceTypes     []string                   `json:"evidence_types,omitempty"`
	Metadata          map[string]any             `json:"metadata,omitempty"`
}

type GovernanceProcessTemplate struct {
	TemplateID GovernanceProcessTemplateID `json:"template_id"`
	TenantID   TenantID                    `json:"tenant_id"`
	Name       string                      `json:"name"`
	Version    string                      `json:"version"`
	Status     string                      `json:"status,omitempty"`
	Gates      []GovernanceGateDefinition  `json:"gates"`
	Metadata   map[string]any              `json:"metadata,omitempty"`
	CreatedBy  string                      `json:"created_by,omitempty"`
	CreatedAt  time.Time                   `json:"created_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
}

type GovernanceProcessRun struct {
	RunID       GovernanceProcessRunID      `json:"run_id"`
	TenantID    TenantID                    `json:"tenant_id"`
	TemplateID  GovernanceProcessTemplateID `json:"template_id,omitempty"`
	Status      GovernanceRunStatus         `json:"status"`
	SubjectType string                      `json:"subject_type"`
	SubjectID   string                      `json:"subject_id"`
	TaskID      TaskID                      `json:"task_id,omitempty"`
	AgentRunID  AgentRunID                  `json:"agent_run_id,omitempty"`
	TraceID     TraceID                     `json:"trace_id,omitempty"`
	PolicySetID PolicySetID                 `json:"policy_set_id,omitempty"`
	Actors      []GovernanceActorRef        `json:"actors,omitempty"`
	Metadata    map[string]any              `json:"metadata,omitempty"`
	CreatedBy   string                      `json:"created_by,omitempty"`
	CreatedAt   time.Time                   `json:"created_at"`
	UpdatedAt   time.Time                   `json:"updated_at"`
	CompletedAt *time.Time                  `json:"completed_at,omitempty"`
}

type GovernanceGateRun struct {
	GateRunID          GovernanceGateRunID        `json:"gate_run_id"`
	ProcessRunID       GovernanceProcessRunID     `json:"process_run_id"`
	TenantID           TenantID                   `json:"tenant_id"`
	GateID             string                     `json:"gate_id"`
	Name               string                     `json:"name,omitempty"`
	Status             GovernanceGateStatus       `json:"status"`
	SubjectType        string                     `json:"subject_type,omitempty"`
	SubjectID          string                     `json:"subject_id,omitempty"`
	ReviewMode         GovernanceReviewMode       `json:"review_mode"`
	ConsensusPolicy    GovernanceConsensusPolicy  `json:"consensus_policy,omitempty"`
	EscalationPolicy   GovernanceEscalationPolicy `json:"escalation_policy,omitempty"`
	RequiredReviewers  int                        `json:"required_reviewers,omitempty"`
	MinApprovals       int                        `json:"min_approvals,omitempty"`
	ReviewerRoles      []string                   `json:"reviewer_roles,omitempty"`
	EvidenceTypes      []string                   `json:"evidence_types,omitempty"`
	ArtifactRefs       []GovernanceArtifactRef    `json:"artifact_refs,omitempty"`
	EvidenceRefs       []GovernanceEvidenceRef    `json:"evidence_refs,omitempty"`
	OpenedAt           *time.Time                 `json:"opened_at,omitempty"`
	ResolvedAt         *time.Time                 `json:"resolved_at,omitempty"`
	ResolvedBy         string                     `json:"resolved_by,omitempty"`
	ResolutionDecision GovernanceReviewDecision   `json:"resolution_decision,omitempty"`
	ResolutionReason   string                     `json:"resolution_reason,omitempty"`
	Metadata           map[string]any             `json:"metadata,omitempty"`
	CreatedAt          time.Time                  `json:"created_at"`
	UpdatedAt          time.Time                  `json:"updated_at"`
}

type GovernanceReview struct {
	ReviewID     GovernanceReviewID       `json:"review_id"`
	GateRunID    GovernanceGateRunID      `json:"gate_run_id"`
	ProcessRunID GovernanceProcessRunID   `json:"process_run_id"`
	TenantID     TenantID                 `json:"tenant_id"`
	ReviewerID   string                   `json:"reviewer_id"`
	ReviewerType string                   `json:"reviewer_type"`
	Decision     GovernanceReviewDecision `json:"decision"`
	Reason       string                   `json:"reason,omitempty"`
	EvidenceRefs []GovernanceEvidenceRef  `json:"evidence_refs,omitempty"`
	Independent  bool                     `json:"independent"`
	CreatedAt    time.Time                `json:"created_at"`
}

type GovernanceConflict struct {
	ConflictID          GovernanceConflictID     `json:"conflict_id"`
	GateRunID           GovernanceGateRunID      `json:"gate_run_id"`
	ProcessRunID        GovernanceProcessRunID   `json:"process_run_id"`
	TenantID            TenantID                 `json:"tenant_id"`
	Status              string                   `json:"status"`
	Issue               string                   `json:"issue"`
	Arguments           []GovernanceEvidenceRef  `json:"arguments,omitempty"`
	EscalatedTo         string                   `json:"escalated_to,omitempty"`
	ArbitrationDecision GovernanceReviewDecision `json:"arbitration_decision,omitempty"`
	ResolutionReason    string                   `json:"resolution_reason,omitempty"`
	CreatedBy           string                   `json:"created_by,omitempty"`
	CreatedAt           time.Time                `json:"created_at"`
	ResolvedAt          *time.Time               `json:"resolved_at,omitempty"`
}

type GovernanceProcessSnapshot struct {
	ProcessRun GovernanceProcessRun `json:"process_run"`
	Gates      []GovernanceGateRun  `json:"gates"`
	Reviews    []GovernanceReview   `json:"reviews"`
	Conflicts  []GovernanceConflict `json:"conflicts"`
}

const (
	TraceInputReceived                         = "input.received"
	TraceAgentLoaded                           = "agent.loaded"
	TraceRunCreated                            = "run.created"
	TraceTaskCreated                           = "task.created"
	TraceTaskLoaded                            = "task.loaded"
	TraceConversationInputRecorded             = "conversation.input.recorded"
	TraceWorkViewBuilt                         = "workview.built"
	TraceCapabilityRetrieved                   = "capability.retrieved"
	TraceConversationContextBuilt              = "conversation.context.built"
	TraceConversationAddresseeJudged           = "conversation.addressee.judged"
	TraceConversationSufficiencyJudged         = "conversation.sufficiency.judged"
	TraceConversationContextRetrievalRequested = "conversation.context_retrieval.requested"
	TraceConversationContextRetrievalCompleted = "conversation.context_retrieval.completed"
	TraceConversationContextRetrievalFailed    = "conversation.context_retrieval.failed"
	TraceConversationRetrievedContextMerged    = "conversation.retrieved_context.merged"
	TraceConversationRouteGuardApplied         = "conversation.route_guard.applied"
	TracePromptBundleBuilt                     = "promptbundle.built"
	TraceModelCalled                           = "model.called"
	TraceModelDelta                            = "model.delta"
	TraceModelCompleted                        = "model.completed"
	TraceDecisionCreated                       = "decision.created"
	TraceDecisionValidated                     = "decision.validated"
	TraceDecisionCompleted                     = "decision.completed"
	TraceDecisionRepairRequested               = "decision.repair_requested"
	TraceToolPolicyChecked                     = "tool.policy_checked"
	TraceToolInvoked                           = "tool.invoked"
	TraceToolDenied                            = "tool.denied"
	TraceToolPendingApproval                   = "tool.pending_approval"
	TraceToolCompleted                         = "tool.completed"
	TraceToolFailed                            = "tool.failed"
	TraceTaskStatusChanged                     = "task.status_changed"
	TraceHandoffPolicyChecked                  = "handoff.policy_checked"
	TraceHandoffCreated                        = "handoff.created"
	TraceHandoffPackaged                       = "handoff.context_packaged"
	TraceHandoffCompleted                      = "handoff.completed"
	TraceResponseSent                          = "response.sent"
	TraceApprovalRequested                     = "approval.requested"
	TraceApprovalResolved                      = "approval.resolved"
	TraceAgentRouteResolved                    = "agent.route.resolved"
	TraceAgentVersionRestored                  = "agent.version.restored"
	TraceCanaryRouted                          = "canary.routed"
	TraceEvalRunStarted                        = "eval.run.started"
	TraceEvalCaseCompleted                     = "eval.case.completed"
	TraceEvalCaseFailed                        = "eval.case.failed"
	TraceEvalSummaryCreated                    = "eval.summary.created"
	TraceExternalWritebackOK                   = "external.writeback_succeeded"
	TraceExternalWritebackFailed               = "external.writeback_failed"
	TraceCredentialUsed                        = "credential.used"
	TraceToolProviderHealthChecked             = "tool_provider.health_checked"
	TraceToolProviderInvoked                   = "tool_provider.invoked"
	TraceToolProviderCompleted                 = "tool_provider.completed"
	TraceToolProviderFailed                    = "tool_provider.failed"
	TraceRuntimeHookInvoked                    = "runtime_hook.invoked"
	TraceRuntimeHookApplied                    = "runtime_hook.applied"
	TraceRuntimeHookDenied                     = "runtime_hook.denied"
	TraceRuntimeHookFailed                     = "runtime_hook.failed"
	TraceRuntimeHookTimeout                    = "runtime_hook.timeout"
	TraceRuntimeHookProviderHealthChecked      = "runtime_hook.provider_health_checked"
	TraceAgentPluginSynced                     = "agent.plugin.synced"
	TraceIdentityMemberResolved                = "identity.member_resolved"
	TracePermissionChecked                     = "permission.checked"
	TraceSkillUpdateRequested                  = "skill.update_requested"
	TraceSkillUpdateApproved                   = "skill.update_approved"
	TraceSkillUpdatePublished                  = "skill.update_published"
	TraceConversationTopicMatched              = "conversation.topic_matched"
	TraceStrategyResolved                      = "strategy.resolved"
	TraceStrategyGuardrailApplied              = "strategy.guardrail_applied"
	TraceModelStrategySelected                 = "model.strategy.selected"
	TraceToolStrategyApplied                   = "tool.strategy.applied"
	TraceCollaborationStrategyApplied          = "collaboration.strategy.applied"
	TraceMemoryStrategyApplied                 = "memory.strategy.applied"
	TraceRuntimeStrategyApplied                = "runtime.strategy.applied"
	TraceRepairStrategyApplied                 = "repair.strategy.applied"
	TraceContextCollectionCompleted            = "context.collection.completed"
	TraceContextCompressionRequested           = "context.compression.requested"
	TraceContextCompressionCompleted           = "context.compression.completed"
	TraceContextCompressionFailed              = "context.compression.failed"
	TraceContextCompressionFallbackApplied     = "context.compression.fallback_applied"
	TraceContextExternalSourceSelected         = "context.external_source.selected"
	TraceContextExternalSourceDropped          = "context.external_source.dropped"
	TraceMemoryScopeChecked                    = "memory.scope_checked"
	TraceMemorySharedRetrieved                 = "memory.shared_retrieved"
	TraceAgentCapabilityMatched                = "agent.capability_matched"
	TraceAgentFactoryDraftCreated              = "agent.factory.draft_created"
	TraceAgentProgressQueried                  = "agent.progress.queried"
	TraceKnowledgeCreated                      = "knowledge.created"
	TraceKnowledgeSearchRequested              = "knowledge.search_requested"
	TraceKnowledgeSearchCompleted              = "knowledge.search_completed"
	TraceCrossGroupSearchRequested             = "cross_group.search_requested"
	TraceCrossGroupSearchDenied                = "cross_group.search_denied"
	TraceCrossGroupSearchCompleted             = "cross_group.search_completed"
	TraceTonePolicyApplied                     = "tone.policy_applied"
	TraceGovernanceProcessStarted              = "governance.process.started"
	TraceGovernanceGateOpened                  = "governance.gate.opened"
	TraceGovernanceReviewSubmitted             = "governance.review.submitted"
	TraceGovernanceGateResolved                = "governance.gate.resolved"
	TraceGovernanceConflictEscalated           = "governance.conflict.escalated"
	TraceGovernanceConflictArbitrated          = "governance.conflict.arbitrated"
	TraceIntakePreReplyEvaluated               = "intake.pre_reply_evaluated"
)

const (
	AuditToolPolicyDenied               = "tool.policy_denied"
	AuditToolApprovalRequired           = "tool.approval_required"
	AuditToolHighRiskInvoked            = "tool.high_risk_invoked"
	AuditToolProviderUpserted           = "tool.provider.upsert"
	AuditToolProviderSynced             = "tool.provider.sync"
	AuditToolProviderHealthChecked      = "tool.provider.health_check"
	AuditToolGroupUpserted              = "tool.group.upsert"
	AuditToolManifestUpserted           = "tool.manifest.upsert"
	AuditToolAdapterOperationUpserted   = "tool.adapter_operation.upsert"
	AuditToolAdapterOperationPublished  = "tool.adapter_operation.publish"
	AuditToolAdapterOperationTested     = "tool.adapter_operation.test"
	AuditAgentPackagePublish            = "agent.package.publish"
	AuditAgentPackageRollback           = "agent.package.rollback"
	AuditAgentVersionRestored           = "agent.version.restored"
	AuditPolicyUpdate                   = "policy.update"
	AuditMemoryWrite                    = "memory.write"
	AuditArtifactDelete                 = "artifact.delete"
	AuditHandoffCreated                 = "handoff.created"
	AuditHandoffPolicyChecked           = "handoff.policy_checked"
	AuditHandoffContextRead             = "handoff.context_read"
	AuditExternalToolCall               = "external_tool_call"
	AuditExternalWritebackFailed        = "external.writeback_failed"
	AuditCredentialUsed                 = "credential.used"
	AuditRuntimeHookInvoked             = "runtime_hook.invoked"
	AuditRuntimeHookApplied             = "runtime_hook.applied"
	AuditRuntimeHookDenied              = "runtime_hook.denied"
	AuditRuntimeHookFailed              = "runtime_hook.failed"
	AuditServiceConnectionSecretRotated = "service_connection.secret_rotated"
	AuditApprovalRequested              = "approval.requested"
	AuditApprovalResolved               = "approval.resolved"
	AuditPermissionChecked              = "permission.checked"
	AuditPermissionDenied               = "permission.denied"
	AuditSkillUpdateRequested           = "skill.update_requested"
	AuditKnowledgeCreated               = "knowledge.created"
	AuditKnowledgeSearch                = "knowledge.search"
	AuditCrossGroupSearch               = "cross_group.search"
	AuditMemoryShared                   = "memory.shared"
	AuditAgentDraftCreated              = "agent.factory.draft_created"
	AuditAgentProgressQueried           = "agent.progress.queried"
	AuditGovernanceTemplateUpserted     = "governance.template.upserted"
	AuditGovernanceProcessStarted       = "governance.process.started"
	AuditGovernanceGateOpened           = "governance.gate.opened"
	AuditGovernanceReviewSubmitted      = "governance.review.submitted"
	AuditGovernanceGateResolved         = "governance.gate.resolved"
	AuditGovernanceConflictEscalated    = "governance.conflict.escalated"
	AuditGovernanceConflictArbitrated   = "governance.conflict.arbitrated"
	AuditIntakePolicyUpserted           = "intake.policy.upserted"
	AuditIntakePolicyDeleted            = "intake.policy.deleted"
)
