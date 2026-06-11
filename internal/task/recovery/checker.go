package recovery

import (
	"fmt"

	"znt/internal/contracts"
)

type CheckResult struct {
	Consistent bool                 `json:"consistent"`
	Expected   contracts.TaskStatus `json:"expected"`
	Actual     contracts.TaskStatus `json:"actual"`
	Problems   []string             `json:"problems,omitempty"`
}

func Check(task contracts.Task, events []contracts.TaskEvent) CheckResult {
	expected := contracts.TaskCreated
	for _, event := range events {
		if next, ok := statusFromEvent(event.Type); ok {
			expected = next
		}
	}
	result := CheckResult{Consistent: expected == task.Status, Expected: expected, Actual: task.Status}
	if !result.Consistent {
		result.Problems = append(result.Problems, fmt.Sprintf("task status %s does not match replayed status %s", task.Status, expected))
	}
	if task.Status.Validate() != nil {
		result.Consistent = false
		result.Problems = append(result.Problems, "task has invalid status")
	}
	return result
}

func statusFromEvent(eventType string) (contracts.TaskStatus, bool) {
	switch eventType {
	case "task.created":
		return contracts.TaskCreated, true
	case "task.accepted":
		return contracts.TaskAccepted, true
	case "task.plan_started":
		return contracts.TaskPlanning, true
	case "task.run_started":
		return contracts.TaskRunning, true
	case "task.waiting_input":
		return contracts.TaskWaitingInput, true
	case "task.input_provided":
		return contracts.TaskRunning, true
	case "task.approval_required":
		return contracts.TaskWaitingApproval, true
	case "task.approved":
		return contracts.TaskRunning, true
	case "task.approval_rejected":
		return contracts.TaskBlocked, true
	case "task.tool_waiting":
		return contracts.TaskWaitingTool, true
	case "task.tool_completed":
		return contracts.TaskRunning, true
	case "task.paused":
		return contracts.TaskPaused, true
	case "task.resumed":
		return contracts.TaskRunning, true
	case "task.completed":
		return contracts.TaskCompleted, true
	case "task.failed":
		return contracts.TaskFailed, true
	case "task.cancelled":
		return contracts.TaskCancelled, true
	default:
		return "", false
	}
}
