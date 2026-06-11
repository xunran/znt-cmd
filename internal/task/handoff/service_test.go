package handoff

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/trace"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
)

func TestCreateHandoffCreatesChildTaskAndParentEvent(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	taskRuntime := taskruntime.NewService(taskRepo, events)
	parent := taskrepo.NewTask("parent_1", "tenant_1", "agent_a", "v1", "policy_default", "parent", "delegate", time.Unix(1, 0).UTC())
	if _, err := taskRuntime.CreateTask(context.Background(), parent, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	service := NewService(taskRuntime, taskRepo, events)
	traceRecorder := trace.NewInMemoryRecorder()
	service.Trace = traceRecorder
	result, err := service.Create(context.Background(), CreateInput{
		TenantID:     "tenant_1",
		TraceID:      "trace_handoff_1",
		ParentTaskID: parent.TaskID,
		SourceRunID:  "run_parent_1",
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		Reason:       "needs specialist",
		Policy: contracts.HandoffPolicy{
			DefaultMode:       contracts.HandoffHybrid,
			AllowArtifactRead: true,
		},
		ActorID: "agent_a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handoff.Status != contracts.HandoffRunning || result.ChildTask == nil || result.Package.Hash == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.ChildTask.AgentVersion != "v1" || result.ChildTask.PolicySetID != "policy_default" {
		t.Fatalf("unexpected child task version/policy: %#v", result.ChildTask)
	}
	parentEvents, err := events.ListByTask(context.Background(), parent.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range parentEvents {
		if event.Type == "handoff.created" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected handoff.created parent event, got %#v", parentEvents)
	}
	traces, err := traceRecorder.ListByTrace(context.Background(), "trace_handoff_1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTrace(traces, contracts.TraceHandoffPolicyChecked) ||
		!hasTrace(traces, contracts.TraceHandoffPackaged) ||
		!hasTrace(traces, contracts.TraceHandoffCreated) {
		t.Fatalf("expected handoff trace matrix, got %#v", traces)
	}
}

func TestCreateHandoffUsesTargetVersionPolicyAndContextRefs(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	taskRuntime := taskruntime.NewService(taskRepo, events)
	parent := taskrepo.NewTask("parent_1", "tenant_1", "agent_a", "v1", "policy_default", "parent", "delegate", time.Unix(1, 0).UTC())
	if _, err := taskRuntime.CreateTask(context.Background(), parent, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	service := NewService(taskRuntime, taskRepo, events)
	result, err := service.Create(context.Background(), CreateInput{
		TenantID:       "tenant_1",
		ParentTaskID:   parent.TaskID,
		SourceRunID:    "run_parent_1",
		FromAgentID:    "agent_a",
		ToAgentID:      "agent_b",
		ToAgentVersion: "v2",
		ToPolicySetID:  "policy_target",
		Objective:      "review",
		MemoryRefs:     []contracts.MemoryID{"memory_1"},
		ExpectedOutput: contracts.ExpectedOutput{Format: "json", Requirements: []string{"include summary"}},
		Policy: contracts.HandoffPolicy{
			DefaultMode:     contracts.HandoffHybrid,
			AllowMemoryRead: true,
		},
		ActorID: "agent_a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChildTask == nil || result.ChildTask.AgentVersion != "v2" || result.ChildTask.PolicySetID != "policy_target" {
		t.Fatalf("unexpected child task: %#v", result.ChildTask)
	}
	if result.Package.ExpectedOutput.Format != "json" || len(result.Package.MemoryRefs) != 1 {
		t.Fatalf("expected package refs/output to be propagated, got %#v", result.Package)
	}
}

func TestCreateHandoffDeniesFullContext(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	taskRuntime := taskruntime.NewService(taskRepo, events)
	parent := taskrepo.NewTask("parent_1", "tenant_1", "agent_a", "v1", "policy_default", "parent", "delegate", time.Unix(1, 0).UTC())
	if _, err := taskRuntime.CreateTask(context.Background(), parent, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	service := NewService(taskRuntime, taskRepo, events)
	_, err := service.Create(context.Background(), CreateInput{
		TenantID:     "tenant_1",
		ParentTaskID: parent.TaskID,
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		Mode:         contracts.HandoffFullContext,
		Policy:       contracts.HandoffPolicy{AllowFullContext: false},
		ActorID:      "agent_a",
	})
	if err == nil {
		t.Fatal("expected full_context denial")
	}
}

func TestCreateHandoffChecksParentTenant(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	taskRuntime := taskruntime.NewService(taskRepo, events)
	parent := taskrepo.NewTask("parent_1", "tenant_1", "agent_a", "v1", "policy_default", "parent", "delegate", time.Unix(1, 0).UTC())
	if _, err := taskRuntime.CreateTask(context.Background(), parent, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	service := NewService(taskRuntime, taskRepo, events)
	_, err := service.Create(context.Background(), CreateInput{
		TenantID:     "tenant_2",
		ParentTaskID: parent.TaskID,
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		ActorID:      "agent_a",
	})
	if err == nil {
		t.Fatal("expected cross-tenant handoff to fail")
	}
}

func hasTrace(events []contracts.TraceEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
