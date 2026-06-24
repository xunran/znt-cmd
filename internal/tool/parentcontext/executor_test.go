package parentcontext

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
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
			Text:        "需要上下文",
			ThreadID:    "chat_1",
			CreatedAt:   time.Unix(2, 0).UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	output, _, err := (Executor{Conversations: store}).Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
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
	if !ok || len(messages) != 1 || messages[0]["text"] != "需要上下文" {
		t.Fatalf("expected parent messages, got %#v", output)
	}
}
