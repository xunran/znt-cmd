package contracts

import (
	"encoding/json"
	"fmt"
	"time"
)

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

	TaskID TaskID `json:"task_id,omitempty"`

	Permissions []Permission `json:"permissions,omitempty"`

	Conversation *RuntimeConversation `json:"conversation,omitempty"`
	ExternalTask *ExternalTaskRef     `json:"external_task,omitempty"`

	RequestID string `json:"request_id,omitempty"`
	Locale    string `json:"locale,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

func (c *RuntimeContext) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, ok := raw["session_id"]; ok {
		return fmt.Errorf("session_id is removed; use context.conversation.conversation_id")
	}
	if _, ok := raw["collaboration"]; ok {
		return fmt.Errorf("collaboration is removed; use context.conversation and context.external_task")
	}
	type runtimeContext RuntimeContext
	var decoded runtimeContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = RuntimeContext(decoded)
	return nil
}

type RuntimeConversation struct {
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind,omitempty"` // direct, group, thread

	ConversationID string `json:"conversation_id"`
	ThreadID       string `json:"thread_id,omitempty"`

	ExternalRefs map[string]string `json:"external_refs,omitempty"`

	CurrentMessage *RuntimeMessage           `json:"current_message,omitempty"`
	RecentMessages []ConversationMessage     `json:"recent_messages,omitempty"`
	Participants   []ConversationParticipant `json:"participants,omitempty"`
}

type RuntimeMessage struct {
	MessageID         string         `json:"message_id,omitempty"`
	ExternalMessageID string         `json:"external_message_id,omitempty"`
	SpeakerID         string         `json:"speaker_id,omitempty"`
	SpeakerType       string         `json:"speaker_type,omitempty"`
	SpeakerName       string         `json:"speaker_name,omitempty"`
	ReplyToMessageID  string         `json:"reply_to_message_id,omitempty"`
	ThreadID          string         `json:"thread_id,omitempty"`
	Mentions          []string       `json:"mentions,omitempty"`
	Text              string         `json:"text,omitempty"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}
