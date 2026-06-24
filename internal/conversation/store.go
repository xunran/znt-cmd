package conversation

import (
	"context"
	"time"

	"znt/internal/contracts"
)

type Thread struct {
	TenantID       contracts.TenantID `json:"tenant_id"`
	ConversationID string             `json:"conversation_id"`
	ThreadID       string             `json:"thread_id"`
	Kind           string             `json:"kind,omitempty"`
	Provider       string             `json:"provider,omitempty"`
	ExternalRefs   map[string]string  `json:"external_refs,omitempty"`
	LastMessageAt  time.Time          `json:"last_message_at,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type MessageRecord struct {
	TenantID       contracts.TenantID `json:"tenant_id"`
	ConversationID string             `json:"conversation_id"`
	ThreadID       string             `json:"thread_id,omitempty"`
	Message        contracts.ConversationMessage
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type Store interface {
	UpsertThread(ctx context.Context, thread Thread) error
	GetThread(ctx context.Context, tenantID contracts.TenantID, conversationID string, threadID string) (Thread, error)
	ListThreads(ctx context.Context, tenantID contracts.TenantID, kind string, limit int, offset int) ([]Thread, error)
	AppendMessage(ctx context.Context, message MessageRecord) error
	RecentMessages(ctx context.Context, tenantID contracts.TenantID, conversationID string, threadID string, limit int) ([]contracts.ConversationMessage, error)
	GetMessage(ctx context.Context, tenantID contracts.TenantID, conversationID string, messageID string) (contracts.ConversationMessage, error)
}
