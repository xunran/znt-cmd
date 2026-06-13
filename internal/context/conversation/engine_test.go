package conversation

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestBasicRetrieverRespectsSourceSpeakerAndThreadFilters(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	conversation := contracts.ConversationContext{
		Kind: KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "find alpha",
			ThreadID:    "thread_a",
			CreatedAt:   now,
		},
		RecentMessages: []contracts.ConversationMessage{{
			MessageID:   "msg_keep",
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "alpha legacy context",
			ThreadID:    "thread_a",
			CreatedAt:   now.Add(-time.Minute),
		}, {
			MessageID:   "msg_wrong_speaker",
			SpeakerID:   "user_2",
			SpeakerType: "user",
			Text:        "alpha legacy context",
			ThreadID:    "thread_a",
			CreatedAt:   now.Add(-2 * time.Minute),
		}, {
			MessageID:   "msg_wrong_thread",
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "alpha legacy context",
			ThreadID:    "thread_b",
			CreatedAt:   now.Add(-3 * time.Minute),
		}},
	}
	items, err := (BasicRetriever{}).Retrieve(context.Background(), []contracts.ContextRetrievalQuery{{
		Query:      "alpha",
		Sources:    []string{"conversation_history"},
		SpeakerIDs: []string{"user_1"},
		ThreadID:   "thread_a",
		MaxResults: 10,
	}}, RetrievalInput{
		Conversation: conversation,
		Memory: []contracts.MemorySummary{{
			MemoryID: "mem_filtered",
			Summary:  "alpha memory should not be returned because source filter excludes memory",
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SourceID != "msg_keep" {
		t.Fatalf("expected only filtered conversation result, got %#v", items)
	}
}

func TestBuildRetrievalQueryDoesNotOverConstrainSpeaker(t *testing.T) {
	query := BuildRetrievalQuery(contracts.ConversationContext{
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID: "user_1",
			Text:      "previous issue",
			ThreadID:  "thread_a",
		},
	}, "previous issue")
	if len(query.SpeakerIDs) != 0 {
		t.Fatalf("default retrieval query should not be speaker-only, got %#v", query.SpeakerIDs)
	}
	if query.ThreadID != "thread_a" {
		t.Fatalf("expected thread hint to be retained, got %#v", query)
	}
	if query.MaxResults != 0 {
		t.Fatalf("default retrieval query should not carry a hard-coded max result limit, got %#v", query)
	}
}
