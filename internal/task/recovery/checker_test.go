package recovery

import (
	"testing"

	"znt/internal/contracts"
)

func TestCheckConsistentTask(t *testing.T) {
	task := contracts.Task{TaskID: "task_1", Status: contracts.TaskCompleted}
	events := []contracts.TaskEvent{
		{Type: "task.created"},
		{Type: "task.accepted"},
		{Type: "task.plan_started"},
		{Type: "task.run_started"},
		{Type: "task.completed"},
	}
	result := Check(task, events)
	if !result.Consistent {
		t.Fatalf("expected consistent result, got %#v", result)
	}
}

func TestCheckDetectsMismatch(t *testing.T) {
	task := contracts.Task{TaskID: "task_1", Status: contracts.TaskRunning}
	events := []contracts.TaskEvent{{Type: "task.created"}, {Type: "task.completed"}}
	result := Check(task, events)
	if result.Consistent {
		t.Fatalf("expected mismatch, got %#v", result)
	}
}
