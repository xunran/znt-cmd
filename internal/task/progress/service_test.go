package progress

import (
	"context"
	"strings"
	"testing"
	"time"

	"znt/internal/contracts"
	taskrepo "znt/internal/task/repository"
)

func TestProgressQueryUsesRecentGroupBinding(t *testing.T) {
	ctx := context.Background()
	tasks := taskrepo.NewInMemoryStore()
	now := time.Now().UTC()
	task := taskrepo.NewTask("task-1", "tenant", "analysis-agent", "v1", "policy_default", "分析", "整理上线风险", now)
	task.Status = contracts.TaskRunning
	if err := tasks.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryStore()
	if _, err := store.SaveBinding(ctx, contracts.GroupTaskBinding{
		TenantID:  "tenant",
		GroupID:   "group-a",
		TaskID:    "task-1",
		AgentID:   "analysis-agent",
		Objective: "整理上线风险",
		CreatedBy: "alice",
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, tasks, nil, nil, nil, nil)
	summary, err := svc.Query(ctx, QueryInput{TenantID: "tenant", GroupID: "group-a", Query: "刚才那个任务"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TaskStatus != contracts.TaskRunning || !strings.Contains(summary.Summary, "analysis-agent") {
		t.Fatalf("unexpected summary %#v", summary)
	}
}
