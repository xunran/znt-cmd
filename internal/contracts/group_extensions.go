package contracts

import "time"

const (
	MemberTypeHuman  = "human"
	MemberTypeAgent  = "agent"
	MemberTypeBot    = "bot"
	MemberTypeSystem = "system"

	MemberStatusActive = "active"
	MemberStatusMuted  = "muted"
	MemberStatusLeft   = "left"
)

type GroupMemberProfile struct {
	TenantID       TenantID       `json:"tenant_id"`
	GroupID        GroupID        `json:"group_id"`
	MemberID       GroupMemberID  `json:"member_id"`
	ExternalUserID string         `json:"external_user_id,omitempty"`
	DisplayName    string         `json:"display_name,omitempty"`
	Aliases        []string       `json:"aliases,omitempty"`
	MemberType     string         `json:"member_type"`
	Roles          []string       `json:"roles,omitempty"`
	PermissionRefs []string       `json:"permission_refs,omitempty"`
	Status         string         `json:"status,omitempty"`
	LastSeenAt     time.Time      `json:"last_seen_at,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

const (
	PermissionSubjectUser  = "user"
	PermissionSubjectRole  = "role"
	PermissionSubjectAgent = "agent"

	PermissionDecisionAllowed          = "allowed"
	PermissionDecisionDenied           = "denied"
	PermissionDecisionApprovalRequired = "approval_required"

	PermissionActionSkillProposeUpdate  = "agent.skill.propose_update"
	PermissionActionSkillPublish        = "agent.skill.publish"
	PermissionActionAgentPackageCreate  = "agent.package.create"
	PermissionActionAgentPackagePublish = "agent.package.publish"
	PermissionActionAgentDelegate       = "agent.delegate"
	PermissionActionKnowledgeCreate     = "knowledge.create"
	PermissionActionKnowledgeSearch     = "knowledge.search"
	PermissionActionKnowledgeIngest     = "knowledge.ingest"
	PermissionActionMemoryRead          = "memory.read"
	PermissionActionMemoryWrite         = "memory.write"
	PermissionActionMemoryShare         = "memory.share"
	PermissionActionCrossGroupSearch    = "cross_group.search"
	PermissionActionCrossGroupPolicy    = "cross_group.policy"
)

type GroupPermissionPolicy struct {
	TenantID         TenantID  `json:"tenant_id"`
	GroupID          GroupID   `json:"group_id"`
	SubjectID        string    `json:"subject_id"`
	SubjectType      string    `json:"subject_type"`
	Actions          []string  `json:"actions"`
	ResourceScopes   []string  `json:"resource_scopes,omitempty"`
	RequiresApproval bool      `json:"requires_approval,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type PermissionCheckInput struct {
	TenantID      TenantID            `json:"tenant_id"`
	GroupID       GroupID             `json:"group_id"`
	ActorID       string              `json:"actor_id"`
	ActorType     string              `json:"actor_type,omitempty"`
	Member        *GroupMemberProfile `json:"member,omitempty"`
	Roles         []string            `json:"roles,omitempty"`
	Action        string              `json:"action"`
	ResourceType  string              `json:"resource_type,omitempty"`
	ResourceID    string              `json:"resource_id,omitempty"`
	ResourceScope string              `json:"resource_scope,omitempty"`
	TraceID       TraceID             `json:"trace_id,omitempty"`
	TaskID        TaskID              `json:"task_id,omitempty"`
	RunID         AgentRunID          `json:"run_id,omitempty"`
	Metadata      map[string]any      `json:"metadata,omitempty"`
}

type PermissionDecision struct {
	Decision         string    `json:"decision"`
	Reason           string    `json:"reason,omitempty"`
	ReasonCode       string    `json:"reason_code,omitempty"`
	RequiresApproval bool      `json:"requires_approval,omitempty"`
	AppliedPolicyIDs []string  `json:"applied_policy_ids,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

type SkillUpdateRequest struct {
	RequestID      SkillUpdateRequestID `json:"request_id"`
	TenantID       TenantID             `json:"tenant_id"`
	AgentID        AgentID              `json:"agent_id"`
	GroupID        GroupID              `json:"group_id"`
	RequestedBy    string               `json:"requested_by"`
	Objective      string               `json:"objective"`
	TargetSkillID  string               `json:"target_skill_id,omitempty"`
	ProposedPatch  map[string]any       `json:"proposed_patch,omitempty"`
	Status         string               `json:"status"`
	ApprovalTaskID TaskID               `json:"approval_task_id,omitempty"`
	Reason         string               `json:"reason,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

const (
	SkillUpdateDraft           = "draft"
	SkillUpdateWaitingApproval = "waiting_approval"
	SkillUpdateApproved        = "approved"
	SkillUpdatePublished       = "published"
	SkillUpdateRejected        = "rejected"
)

type ConversationTopicThread struct {
	TenantID        TenantID             `json:"tenant_id"`
	GroupID         GroupID              `json:"group_id"`
	ThreadID        ConversationThreadID `json:"thread_id"`
	TopicID         TopicID              `json:"topic_id"`
	Summary         string               `json:"summary"`
	Participants    []string             `json:"participants,omitempty"`
	RelatedTaskIDs  []TaskID             `json:"related_task_ids,omitempty"`
	RelatedAgentIDs []AgentID            `json:"related_agent_ids,omitempty"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

const (
	MemoryScopeGlobal = "global"
	MemoryScopeTenant = "tenant"
	MemoryScopeGroup  = "group"
	MemoryScopeUser   = "user"
	MemoryScopeTask   = "task"
	MemoryScopeShared = "shared"

	VisibilityPrivate      = "private"
	VisibilityGroup        = "group"
	VisibilitySharedGroups = "shared_groups"
	VisibilityShared       = "shared"
	VisibilityTenant       = "tenant"
)

const (
	KnowledgeIndexBM25      = "bm25"
	KnowledgeIndexEmbedding = "embedding"
	KnowledgeIndexHybrid    = "hybrid"

	KnowledgeSearchBM25      = "bm25"
	KnowledgeSearchEmbedding = "embedding"
	KnowledgeSearchHybrid    = "hybrid"

	KnowledgeBaseStatusReady    = "ready"
	KnowledgeBaseStatusIndexing = "indexing"
	KnowledgeBaseStatusDisabled = "disabled"

	KnowledgeDocumentIndexPending = "pending"
	KnowledgeDocumentIndexReady   = "ready"
	KnowledgeDocumentIndexFailed  = "failed"

	KnowledgeIngestionQueued    = "queued"
	KnowledgeIngestionIndexing  = "indexing"
	KnowledgeIngestionCompleted = "completed"
	KnowledgeIngestionFailed    = "failed"

	CrossGroupShareEnabled  = "enabled"
	CrossGroupShareDisabled = "disabled"

	RedactionPolicySummaryOnly = "summary_only"
	RedactionPolicyMaskEmails  = "mask_emails"
	RedactionPolicyMaskNumbers = "mask_numbers"
	RedactionPolicyStrict      = "strict"
)

type MemoryScope struct {
	TenantID           TenantID  `json:"tenant_id"`
	MemoryID           MemoryID  `json:"memory_id"`
	ScopeType          string    `json:"scope_type"`
	ScopeID            string    `json:"scope_id"`
	Visibility         string    `json:"visibility"`
	OwnerGroupID       GroupID   `json:"owner_group_id,omitempty"`
	SharedWithGroupIDs []GroupID `json:"shared_with_group_ids,omitempty"`
	ReadRoles          []string  `json:"read_roles,omitempty"`
	WriteRoles         []string  `json:"write_roles,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type KnowledgeBase struct {
	KnowledgeBaseID KnowledgeBaseID `json:"knowledge_base_id"`
	TenantID        TenantID        `json:"tenant_id"`
	Name            string          `json:"name"`
	OwnerGroupID    GroupID         `json:"owner_group_id,omitempty"`
	Visibility      string          `json:"visibility"`
	SourceType      string          `json:"source_type"`
	IndexType       string          `json:"index_type"`
	SearchMode      string          `json:"search_mode,omitempty"`
	Status          string          `json:"status"`
	CreatedBy       string          `json:"created_by,omitempty"`
	DocumentCount   int             `json:"document_count,omitempty"`
	LastIndexedAt   *time.Time      `json:"last_indexed_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at,omitempty"`
}

type KnowledgeDocument struct {
	DocumentID      KnowledgeDocumentID `json:"document_id"`
	KnowledgeBaseID KnowledgeBaseID     `json:"knowledge_base_id"`
	TenantID        TenantID            `json:"tenant_id"`
	SourceGroupID   GroupID             `json:"source_group_id,omitempty"`
	Title           string              `json:"title"`
	Content         string              `json:"content"`
	SourceURI       string              `json:"source_uri,omitempty"`
	Visibility      string              `json:"visibility,omitempty"`
	IndexStatus     string              `json:"index_status,omitempty"`
	IndexedAt       *time.Time          `json:"indexed_at,omitempty"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
	CreatedAt       time.Time           `json:"created_at,omitempty"`
}

type KnowledgeSearchResult struct {
	DocumentID      KnowledgeDocumentID `json:"document_id"`
	KnowledgeBaseID KnowledgeBaseID     `json:"knowledge_base_id"`
	SourceGroupID   GroupID             `json:"source_group_id,omitempty"`
	Title           string              `json:"title"`
	Snippet         string              `json:"snippet"`
	Score           float64             `json:"score"`
	SourceURI       string              `json:"source_uri,omitempty"`
	Visibility      string              `json:"visibility,omitempty"`
	SearchMode      string              `json:"search_mode,omitempty"`
	Redacted        bool                `json:"redacted,omitempty"`
	RedactionPolicy string              `json:"redaction_policy,omitempty"`
}

type KnowledgeIngestionJob struct {
	JobID           KnowledgeIngestionJobID `json:"job_id"`
	TenantID        TenantID                `json:"tenant_id"`
	KnowledgeBaseID KnowledgeBaseID         `json:"knowledge_base_id"`
	DocumentID      KnowledgeDocumentID     `json:"document_id,omitempty"`
	SourceGroupID   GroupID                 `json:"source_group_id,omitempty"`
	Status          string                  `json:"status"`
	IndexType       string                  `json:"index_type,omitempty"`
	SearchMode      string                  `json:"search_mode,omitempty"`
	Error           string                  `json:"error,omitempty"`
	CreatedBy       string                  `json:"created_by,omitempty"`
	CreatedAt       time.Time               `json:"created_at,omitempty"`
	UpdatedAt       time.Time               `json:"updated_at,omitempty"`
	CompletedAt     *time.Time              `json:"completed_at,omitempty"`
}

type CrossGroupSharePolicy struct {
	PolicyID         CrossGroupSharePolicyID `json:"policy_id"`
	TenantID         TenantID                `json:"tenant_id"`
	SourceGroupID    GroupID                 `json:"source_group_id"`
	TargetGroupID    GroupID                 `json:"target_group_id"`
	KnowledgeBaseIDs []KnowledgeBaseID       `json:"knowledge_base_ids,omitempty"`
	RedactionPolicy  string                  `json:"redaction_policy"`
	RequiresApproval bool                    `json:"requires_approval,omitempty"`
	Status           string                  `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	CreatedBy        string                  `json:"created_by,omitempty"`
	ApprovedBy       string                  `json:"approved_by,omitempty"`
	ApprovalID       ApprovalID              `json:"approval_id,omitempty"`
	CreatedAt        time.Time               `json:"created_at,omitempty"`
	UpdatedAt        time.Time               `json:"updated_at,omitempty"`
}

type GroupTaskBinding struct {
	BindingID GroupTaskBindingID `json:"binding_id"`
	TenantID  TenantID           `json:"tenant_id"`
	GroupID   GroupID            `json:"group_id"`
	MessageID string             `json:"message_id"`
	TaskID    TaskID             `json:"task_id"`
	RunID     AgentRunID         `json:"run_id,omitempty"`
	HandoffID HandoffID          `json:"handoff_id,omitempty"`
	AgentID   AgentID            `json:"agent_id"`
	Objective string             `json:"objective"`
	CreatedBy string             `json:"created_by"`
	CreatedAt time.Time          `json:"created_at"`
}

type TaskProgressSummary struct {
	TenantID      TenantID      `json:"tenant_id"`
	GroupID       GroupID       `json:"group_id"`
	TaskID        TaskID        `json:"task_id,omitempty"`
	RunID         AgentRunID    `json:"run_id,omitempty"`
	HandoffID     HandoffID     `json:"handoff_id,omitempty"`
	AgentID       AgentID       `json:"agent_id,omitempty"`
	Objective     string        `json:"objective,omitempty"`
	TaskStatus    TaskStatus    `json:"task_status,omitempty"`
	RunStatus     RunStatus     `json:"run_status,omitempty"`
	HandoffStatus HandoffStatus `json:"handoff_status,omitempty"`
	Summary       string        `json:"summary"`
	UpdatedAt     time.Time     `json:"updated_at,omitempty"`
}

type AgentCapability struct {
	CapabilityID AgentCapabilityID `json:"capability_id"`
	TenantID     TenantID          `json:"tenant_id"`
	AgentID      AgentID           `json:"agent_id"`
	Version      AgentVersion      `json:"version,omitempty"`
	Name         string            `json:"name,omitempty"`
	Description  string            `json:"description,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	WhenToUse    []string          `json:"when_to_use,omitempty"`
	RiskLevel    RiskLevel         `json:"risk_level,omitempty"`
	CreatedAt    time.Time         `json:"created_at,omitempty"`
}

type AgentCapabilityMatch struct {
	Capability AgentCapability `json:"capability"`
	Score      float64         `json:"score"`
	Reason     string          `json:"reason,omitempty"`
}

type AgentDraftRequest struct {
	RequestID   AgentDraftRequestID `json:"request_id"`
	TenantID    TenantID            `json:"tenant_id"`
	GroupID     GroupID             `json:"group_id,omitempty"`
	RequestedBy string              `json:"requested_by"`
	AgentID     AgentID             `json:"agent_id"`
	Name        string              `json:"name"`
	Objective   string              `json:"objective"`
	Status      string              `json:"status"`
	DraftID     string              `json:"draft_id,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

type TonePolicy struct {
	TenantID     TenantID   `json:"tenant_id,omitempty"`
	GroupID      GroupID    `json:"group_id,omitempty"`
	DefaultStyle string     `json:"default_style"`
	GroupStyle   string     `json:"group_style,omitempty"`
	Rules        []ToneRule `json:"rules,omitempty"`
}

type ToneRule struct {
	When  string `json:"when"`
	Style string `json:"style"`
}

type ToneDecision struct {
	Style       string   `json:"style"`
	ShouldReply bool     `json:"should_reply"`
	Reasons     []string `json:"reasons,omitempty"`
}
