package runtime

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	taskrepo "znt/internal/task/repository"
)

func TestServiceApplyCommandWritesEvent(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	service := NewService(tasks, events)
	service.now = func() time.Time { return time.Unix(10, 0).UTC() }

	task := taskrepo.NewTask("task_1", "tenant_1", "agent_1", "v1", "policy_1", "title", "objective", service.now())
	if _, err := service.CreateTask(context.Background(), task, "system", "system"); err != nil {
		t.Fatal(err)
	}
	updated, event, transition, err := service.ApplyCommand(context.Background(), CommandInput{
		TaskID:    task.TaskID,
		Command:   contracts.CmdAccept,
		ActorID:   "agent_1",
		ActorType: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != contracts.TaskAccepted || updated.Version != 1 {
		t.Fatalf("unexpected updated task: %#v", updated)
	}
	if event.Type != "task.accepted" || transition.To != contracts.TaskAccepted {
		t.Fatalf("unexpected event/transition: %#v %#v", event, transition)
	}
	allEvents, err := service.Events(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(allEvents) != 2 {
		t.Fatalf("expected create + transition events, got %d", len(allEvents))
	}
}

func TestRepositoryOptimisticConflict(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	task := taskrepo.NewTask("task_1", "tenant_1", "agent_1", "v1", "policy_1", "title", "objective", time.Unix(1, 0))
	if err := tasks.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	task.Status = contracts.TaskAccepted
	if err := tasks.UpdateWithVersion(context.Background(), task, 2); err == nil {
		t.Fatal("expected optimistic lock conflict")
	}
}

func TestServiceApplyCommandWritesAuditForAuditedTransition(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(tasks, events)
	service.Audit = auditLogger
	service.now = func() time.Time { return time.Unix(20, 0).UTC() }

	task := taskrepo.NewTask("task_1", "tenant_1", "agent_1", "v1", "policy_1", "title", "objective", service.now())
	task.Status = contracts.TaskRunning
	if err := tasks.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.ApplyCommand(context.Background(), CommandInput{
		TaskID:    task.TaskID,
		Command:   contracts.CmdApprovalRequired,
		ActorID:   "agent_1",
		ActorType: "agent",
		RunID:     "run_1",
	}); err != nil {
		t.Fatal(err)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: "task.approval_required"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].TaskID != task.TaskID || audits[0].RunID != "run_1" {
		t.Fatalf("expected transition audit, got %#v", audits)
	}
}
