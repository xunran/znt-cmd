package plan

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestCreateCompletePlan(t *testing.T) {
	service := NewService(NewInMemoryRepository())
	service.now = func() time.Time { return time.Unix(1, 0).UTC() }
	task := contracts.Task{TaskID: "task_1", TenantID: "tenant_1", Objective: "build report"}

	created, steps, event, err := service.CreatePlan(context.Background(), task, "", []StepInput{
		{Title: "Collect facts"},
		{Title: "Write report"},
	}, "agent_1", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != contracts.PlanRunning || len(steps) != 2 || event.Type != "plan.created" {
		t.Fatalf("unexpected plan create result: %#v %#v %#v", created, steps, event)
	}
	current, ok, err := service.CurrentStep(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || current.StepID != steps[0].StepID {
		t.Fatalf("unexpected current step: %#v", current)
	}
	if _, _, err := service.StartStep(context.Background(), task.TaskID, steps[0].StepID, "agent_1", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteStep(context.Background(), task.TaskID, steps[0].StepID, nil, "toolres_1", "agent_1", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartStep(context.Background(), task.TaskID, steps[1].StepID, "agent_1", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteStep(context.Background(), task.TaskID, steps[1].StepID, []contracts.ArtifactRef{{ArtifactID: "artifact_1"}}, "toolres_2", "agent_1", "agent"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActivePlan != nil {
		t.Fatalf("completed plan should not be active: %#v", snapshot.ActivePlan)
	}
	if len(snapshot.Plans) != 1 || snapshot.Plans[0].Status != contracts.PlanCompleted {
		t.Fatalf("expected completed plan history, got %#v", snapshot.Plans)
	}
	if len(snapshot.Events) != 6 {
		t.Fatalf("expected create + two start + two completion + plan completed events, got %#v", snapshot.Events)
	}
	if snapshot.Events[len(snapshot.Events)-1].Type != "plan.completed" {
		t.Fatalf("expected plan.completed event, got %#v", snapshot.Events)
	}
}

func TestStartStepRequiresPreviousStepsDone(t *testing.T) {
	service := NewService(NewInMemoryRepository())
	task := contracts.Task{TaskID: "task_1", TenantID: "tenant_1", Objective: "build report"}
	_, steps, _, err := service.CreatePlan(context.Background(), task, "", []StepInput{
		{Title: "Collect facts"},
		{Title: "Write report"},
	}, "agent_1", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartStep(context.Background(), task.TaskID, steps[1].StepID, "agent_1", "agent"); err == nil {
		t.Fatal("expected second step start to require first step completion")
	}
}

func TestReplanPreservesHistory(t *testing.T) {
	service := NewService(NewInMemoryRepository())
	task := contracts.Task{TaskID: "task_1", TenantID: "tenant_1", Objective: "build report"}
	first, _, _, err := service.CreatePlan(context.Background(), task, "", []StepInput{{Title: "Old step"}}, "agent_1", "agent")
	if err != nil {
		t.Fatal(err)
	}
	second, steps, event, err := service.Replan(context.Background(), task, "new objective", []StepInput{{Title: "New step"}}, "agent_1", "agent", "new evidence")
	if err != nil {
		t.Fatal(err)
	}
	if second.PlanID == first.PlanID || event.Type != "plan.replanned" || len(steps) != 1 {
		t.Fatalf("unexpected replan result: %#v %#v %#v", second, steps, event)
	}
	snapshot, err := service.Snapshot(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Plans) != 2 || snapshot.Plans[0].Status != contracts.PlanSuperseded || snapshot.Plans[1].Status != contracts.PlanRunning {
		t.Fatalf("expected superseded + running history, got %#v", snapshot.Plans)
	}
	if len(snapshot.Events) < 3 {
		t.Fatalf("expected preserved plan events, got %#v", snapshot.Events)
	}
}
