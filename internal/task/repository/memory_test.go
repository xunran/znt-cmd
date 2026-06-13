package repository

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestEventRepositoryListByTaskLimit(t *testing.T) {
	repo := NewInMemoryEventRepository()
	base := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	for i, eventID := range []contracts.TaskEventID{"event_1", "event_2", "event_3"} {
		if err := repo.Append(context.Background(), contracts.TaskEvent{
			EventID:   eventID,
			TaskID:    "task_1",
			TenantID:  "tenant_1",
			Type:      "conversation.input",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Append(context.Background(), contracts.TaskEvent{
		EventID:   "event_other",
		TaskID:    "task_other",
		TenantID:  "tenant_1",
		Type:      "conversation.input",
		CreatedAt: base.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	limited, err := repo.ListByTaskLimit(context.Background(), "task_1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].EventID != "event_2" || limited[1].EventID != "event_3" {
		t.Fatalf("expected latest two task events in ascending order, got %#v", limited)
	}
	all, err := repo.ListByTaskLimit(context.Background(), "task_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected unlimited task events for limit 0, got %#v", all)
	}
}
