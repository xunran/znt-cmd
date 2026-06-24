package parentcontext

import (
	"context"
	"strings"

	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/tool/registry"
)

const ToolID = "parent_context.read"

type Executor struct {
	Conversations conversationstore.Store
}

func Register(reg registry.Registry, conversations conversationstore.Store) error {
	return registry.RegisterInternal(reg, registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      ToolID,
			GroupID:     "context",
			Name:        ToolID,
			Description: "Reads recent messages from the parent group conversation for a tool agent run.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"conversation_id": map[string]any{"type": "string"},
					"thread_id":       map[string]any{"type": "string"},
					"limit":           map[string]any{"type": "number"},
				},
			},
			OutputSchema:     map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor:  Executor{Conversations: conversations},
		WhenToUse: []string{"tool agent needs parent conversation context", "read recent group chat messages"},
	})
}

func (e Executor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.Conversations == nil {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "conversation store is not configured", nil)
	}
	conversationID := stringArg(call, "conversation_id")
	threadID := stringArg(call, "thread_id")
	if conversationID == "" || threadID == "" {
		if parent := parentConversation(call); parent != nil {
			if conversationID == "" {
				conversationID = strings.TrimSpace(parent.ConversationID)
			}
			if threadID == "" {
				threadID = strings.TrimSpace(parent.ThreadID)
			}
		}
	}
	if conversationID == "" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "conversation_id is required", nil)
	}
	if threadID == "" {
		threadID = conversationID
	}
	limit := intArg(call, "limit")
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	messages, err := e.Conversations.RecentMessages(ctx, call.TenantID, conversationID, threadID, limit)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"conversation_id": conversationID,
		"thread_id":       threadID,
		"limit":           limit,
		"messages":        messageViews(messages),
	}, nil, nil
}

func parentConversation(call contracts.ToolCall) *contracts.RuntimeConversation {
	parent, ok := call.RuntimeContext["parent_context"].(map[string]any)
	if !ok {
		parent, ok = call.Arguments["_parent_context"].(map[string]any)
	}
	if !ok {
		return nil
	}
	rawConversation, ok := parent["conversation"].(map[string]any)
	if !ok {
		return nil
	}
	conversationID := strings.TrimSpace(stringFromMap(rawConversation, "conversation_id"))
	if conversationID == "" {
		return nil
	}
	threadID := strings.TrimSpace(stringFromMap(rawConversation, "thread_id"))
	if threadID == "" {
		threadID = conversationID
	}
	return &contracts.RuntimeConversation{
		ConversationID: conversationID,
		ThreadID:       threadID,
		Provider:       stringFromMap(rawConversation, "provider"),
		Kind:           stringFromMap(rawConversation, "kind"),
	}
}

func messageViews(messages []contracts.ConversationMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		out = append(out, map[string]any{
			"message_id":   message.MessageID,
			"speaker_id":   message.SpeakerID,
			"speaker_type": message.SpeakerType,
			"speaker_name": message.SpeakerName,
			"text":         message.Text,
			"thread_id":    message.ThreadID,
			"created_at":   message.CreatedAt,
		})
	}
	return out
}

func stringArg(call contracts.ToolCall, key string) string {
	if call.Arguments == nil {
		return ""
	}
	value, _ := call.Arguments[key].(string)
	return strings.TrimSpace(value)
}

func intArg(call contracts.ToolCall, key string) int {
	if call.Arguments == nil {
		return 0
	}
	switch value := call.Arguments[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}
