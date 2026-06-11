package run

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestRunRepositoryLifecycle(t *testing.T) {
	repo := NewInMemoryRepository()
	run := contracts.AgentRun{
		RunID:        "run_1",
		TraceID:      "trace_1",
		TenantID:     "tenant_1",
		AgentID:      "agent_1",
		AgentVersion: "v1",
		Status:       contracts.RunCreated,
		PolicySetID:  "policy_1",
		StartedAt:    time.Unix(1, 0).UTC(),
	}
	if err := repo.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	running, err := repo.MarkRunning(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != contracts.RunRunning {
		t.Fatalf("expected running, got %s", running.Status)
	}
	stepped, stepID, err := repo.IncrementStep(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stepped.StepCount != 1 || stepID == "" {
		t.Fatalf("expected step increment, got %#v %q", stepped, stepID)
	}
	withTool, err := repo.IncrementToolCall(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if withTool.ToolCallCount != 1 {
		t.Fatalf("expected tool call count increment, got %#v", withTool)
	}
	snapshot := contracts.VersionSnapshot{
		AgentDefinition:  "v1",
		PolicySet:        "policy_1",
		PolicySetVersion: "v2",
		PromptBundleHash: "prompt_hash_1",
	}
	updated, err := repo.UpdateVersionSnapshot(context.Background(), run.RunID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VersionSnapshot.PromptBundleHash != "prompt_hash_1" || updated.VersionSnapshot.PolicySetVersion != "v2" {
		t.Fatalf("expected snapshot update, got %#v", updated.VersionSnapshot)
	}
	completed, err := repo.MarkCompleted(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != contracts.RunCompleted || completed.CompletedAt == nil {
		t.Fatalf("expected completed, got %#v", completed)
	}
}

func TestRunRepositoryListFilterAndCancel(t *testing.T) {
	repo := NewInMemoryRepository()
	base := time.Unix(10, 0).UTC()
	runs := []contracts.AgentRun{
		{RunID: "run_old", TraceID: "trace_a", TenantID: "tenant_1", AgentID: "agent_1", AgentVersion: "v1", TaskID: "task_1", Status: contracts.RunCompleted, PolicySetID: "policy_1", StartedAt: base},
		{RunID: "run_new", TraceID: "trace_b", TenantID: "tenant_1", AgentID: "agent_1", AgentVersion: "v1", TaskID: "task_2", Status: contracts.RunRunning, PolicySetID: "policy_1", StartedAt: base.Add(time.Second)},
		{RunID: "run_other", TraceID: "trace_c", TenantID: "tenant_2", AgentID: "agent_1", AgentVersion: "v1", TaskID: "task_3", Status: contracts.RunRunning, PolicySetID: "policy_1", StartedAt: base.Add(2 * time.Second)},
	}
	for _, run := range runs {
		if err := repo.Create(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := repo.List(context.Background(), ListFilter{TenantID: "tenant_1", AgentID: "agent_1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].RunID != "run_new" {
		t.Fatalf("expected newest tenant run first, got %#v", listed)
	}
	filtered, err := repo.List(context.Background(), ListFilter{TenantID: "tenant_1", Status: contracts.RunCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].RunID != "run_old" {
		t.Fatalf("expected completed run filter, got %#v", filtered)
	}
	cancelled, err := repo.MarkCancelled(context.Background(), "run_new")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != contracts.RunCancelled || cancelled.CompletedAt == nil {
		t.Fatalf("expected cancelled run, got %#v", cancelled)
	}
}
