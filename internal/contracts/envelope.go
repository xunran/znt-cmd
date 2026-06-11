package contracts

import "time"

type Permission string

type AgentEnvelope struct {
	EnvelopeID string         `json:"envelope_id"`
	TraceID    TraceID        `json:"trace_id"`
	Target     AgentTarget    `json:"target"`
	Caller     AgentCaller    `json:"caller"`
	Command    string         `json:"command"`
	Payload    map[string]any `json:"payload"`
	Context    RuntimeContext `json:"context"`
	CreatedAt  time.Time      `json:"created_at"`
}

type AgentTarget struct {
	AgentID AgentID      `json:"agent_id"`
	Version AgentVersion `json:"version,omitempty"`
}

type AgentCaller struct {
	CallerID   string   `json:"caller_id"`
	CallerType string   `json:"caller_type"`
	TenantID   TenantID `json:"tenant_id"`
}

type RuntimeContext struct {
	TenantID TenantID `json:"tenant_id"`
	UserID   UserID   `json:"user_id,omitempty"`

	SessionID string `json:"session_id,omitempty"`
	TaskID    TaskID `json:"task_id,omitempty"`

	Permissions []Permission `json:"permissions,omitempty"`

	Collaboration *CollaborationContext `json:"collaboration,omitempty"`

	RequestID string `json:"request_id,omitempty"`
	Locale    string `json:"locale,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

type CollaborationContext struct {
	Provider string `json:"provider"`

	ExternalWorkspaceID string `json:"external_workspace_id,omitempty"`
	ExternalGroupID     string `json:"external_group_id,omitempty"`
	ExternalChannelID   string `json:"external_channel_id,omitempty"`
	ExternalThreadID    string `json:"external_thread_id,omitempty"`
	ExternalTaskID      string `json:"external_task_id,omitempty"`
	ExternalMessageID   string `json:"external_message_id,omitempty"`

	CallerID   string `json:"caller_id"`
	CallerType string `json:"caller_type"`

	ReplyTarget *ReplyTarget `json:"reply_target,omitempty"`

	ConversationKind   string                    `json:"conversation_kind,omitempty"` // direct, group, thread
	CurrentSpeakerName string                    `json:"current_speaker_name,omitempty"`
	MentionedAgentIDs  []string                  `json:"mentioned_agent_ids,omitempty"`
	ReplyToMessageID   string                    `json:"reply_to_message_id,omitempty"`
	ThreadID           string                    `json:"thread_id,omitempty"`
	RecentMessages     []ConversationMessage     `json:"recent_messages,omitempty"`
	Participants       []ConversationParticipant `json:"participants,omitempty"`
}

type ReplyTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
