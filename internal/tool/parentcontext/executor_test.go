package parentcontext

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/governance/audit"
)

func TestExecutorReadsRecentMessagesFromParentContext(t *testing.T) {
	store := conversationstore.NewInMemoryStore()
	if err := store.UpsertThread(context.Background(), conversationstore.Thread{
		TenantID:       "tenant_1",
		ConversationID: "chat_1",
		ThreadID:       "chat_1",
		Kind:           "group",
		Provider:       "znt-cmd",
		CreatedAt:      time.Unix(1, 0).UTC(),
		UpdatedAt:      time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(context.Background(), conversationstore.MessageRecord{
		TenantID:       "tenant_1",
		ConversationID: "chat_1",
		ThreadID:       "chat_1",
		Message: contracts.ConversationMessage{
			MessageID:   "msg_1",
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "need parent context token=secret",
			ThreadID:    "chat_1",
			CreatedAt:   time.Unix(2, 0).UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	auditLogger := audit.NewInMemoryLogger()
	output, _, err := (Executor{
		Conversations: store,
		Audit:         auditLogger,
		Now:           func() time.Time { return time.Unix(3, 0).UTC() },
	}).Execute(context.Background(), contracts.ToolCall{
		TenantID:   "tenant_1",
		ToolCallID: "toolcall_1",
		ToolID:     ToolID,
		TraceID:    "trace_1",
		RunID:      "run_1",
		TaskID:     "task_1",
		Arguments: map[string]any{
			"limit": 1,
		},
		RuntimeContext: map[string]any{
			"parent_context": map[string]any{
				"conversation": map[string]any{
					"conversation_id": "chat_1",
					"thread_id":       "chat_1",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := output["messages"].([]map[string]any)
	if !ok || len(messages) != 1 || messages[0]["text"] != "[redacted parent context]" || messages[0]["trust_level"] != "untrusted_user_text" {
		t.Fatalf("expected parent messages, got %#v", output)
	}
	if output["source_run_id"] != contracts.AgentRunID("run_1") || output["tool_call_id"] != contracts.ToolCallID("toolcall_1") || output["trust_level"] != "untrusted_user_text" {
		t.Fatalf("expected parent context provenance, got %#v", output)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: "parent_context.read", RunID: "run_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].TraceID != "trace_1" || audits[0].ResourceID != "chat_1" {
		t.Fatalf("expected parent context audit, got %#v", audits)
	}
}
