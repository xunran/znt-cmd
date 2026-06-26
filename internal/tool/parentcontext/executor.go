package parentcontext

import (
	"context"
	"strings"
	"time"

	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/governance/audit"
	"znt/internal/tool/registry"
	"znt/pkg/idgen"
)

const ToolID = "parent_context.read"

type Executor struct {
	Conversations conversationstore.Store
	Audit         audit.Logger
	Now           func() time.Time
}

func Register(reg registry.Registry, conversations conversationstore.Store) error {
	return RegisterWithAudit(reg, conversations, nil)
}

func RegisterWithAudit(reg registry.Registry, conversations conversationstore.Store, auditLogger audit.Logger) error {
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
		Executor:  Executor{Conversations: conversations, Audit: auditLogger},
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
	e.auditRead(ctx, call, conversationID, threadID, len(messages))
	return map[string]any{
		"conversation_id": conversationID,
		"thread_id":       threadID,
		"limit":           limit,
		"source_run_id":   call.RunID,
		"tool_call_id":    call.ToolCallID,
		"trust_level":     "untrusted_user_text",
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
			"text":         sanitizeParentContextText(message.Text),
			"thread_id":    message.ThreadID,
			"created_at":   message.CreatedAt,
			"trust_level":  trustLevelForMessage(message),
		})
	}
	return out
}

func sanitizeParentContextText(value string) string {
	text, _ := sanitizeText(value, 1200)
	return text
}

func sanitizeText(value string, limit int) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	internalMarkers := []string{"authorization:", "bearer ", "api_key", "api-key", "password=", "secret=", "token=", "stack trace", "panic:", "trace_id", "run_id", "tool_call_id"}
	for _, marker := range internalMarkers {
		if strings.Contains(lower, marker) {
			return "[redacted parent context]", true
		}
	}
	runes := []rune(text)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "...(truncated)", true
	}
	return text, false
}

func trustLevelForMessage(message contracts.ConversationMessage) string {
	switch strings.ToLower(strings.TrimSpace(message.SpeakerType)) {
	case "agent", "assistant", "tool", "agent_tool":
		return "tool_result"
	case "system":
		return "system_record"
	default:
		return "untrusted_user_text"
	}
}

func (e Executor) auditRead(ctx context.Context, call contracts.ToolCall, conversationID string, threadID string, messageCount int) {
	if e.Audit == nil {
		return
	}
	_ = e.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     call.TenantID,
		ActorID:      string(call.ToolID),
		ActorType:    "tool",
		Action:       "parent_context.read",
		ResourceType: "conversation",
		ResourceID:   conversationID,
		Decision:     "allowed",
		Reason:       "tool agent read parent conversation context",
		TraceID:      call.TraceID,
		TaskID:       call.TaskID,
		RunID:        call.RunID,
		CreatedAt:    e.now(),
	})
	_ = messageCount
	_ = threadID
}

func (e Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
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
