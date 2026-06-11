package state

import (
	"fmt"

	"znt/internal/contracts"
)

type Transition struct {
	From      contracts.TaskStatus
	Command   contracts.TaskCommand
	To        contracts.TaskStatus
	Audit     bool
	EventType string
}

var transitions = []Transition{
	{From: contracts.TaskCreated, Command: contracts.CmdAccept, To: contracts.TaskAccepted, EventType: "task.accepted"},
	{From: contracts.TaskAccepted, Command: contracts.CmdPlanStarted, To: contracts.TaskPlanning, EventType: "task.plan_started"},
	{From: contracts.TaskPlanning, Command: contracts.CmdRunStarted, To: contracts.TaskRunning, EventType: "task.run_started"},
	{From: contracts.TaskRunning, Command: contracts.CmdAskClarification, To: contracts.TaskWaitingInput, EventType: "task.waiting_input"},
	{From: contracts.TaskWaitingInput, Command: contracts.CmdProvideInput, To: contracts.TaskRunning, EventType: "task.input_provided"},
	{From: contracts.TaskRunning, Command: contracts.CmdApprovalRequired, To: contracts.TaskWaitingApproval, Audit: true, EventType: "task.approval_required"},
	{From: contracts.TaskWaitingApproval, Command: contracts.CmdApproveAction, To: contracts.TaskRunning, Audit: true, EventType: "task.approved"},
	{From: contracts.TaskWaitingApproval, Command: contracts.CmdRejectAction, To: contracts.TaskBlocked, Audit: true, EventType: "task.approval_rejected"},
	{From: contracts.TaskRunning, Command: contracts.CmdToolWaiting, To: contracts.TaskWaitingTool, EventType: "task.tool_waiting"},
	{From: contracts.TaskWaitingTool, Command: contracts.CmdToolCompleted, To: contracts.TaskRunning, EventType: "task.tool_completed"},
	{From: contracts.TaskRunning, Command: contracts.CmdPause, To: contracts.TaskPaused, Audit: true, EventType: "task.paused"},
	{From: contracts.TaskPaused, Command: contracts.CmdResume, To: contracts.TaskRunning, Audit: true, EventType: "task.resumed"},
	{From: contracts.TaskRunning, Command: contracts.CmdComplete, To: contracts.TaskCompleted, EventType: "task.completed"},
	{From: contracts.TaskRunning, Command: contracts.CmdFail, To: contracts.TaskFailed, EventType: "task.failed"},
}

func Apply(current contracts.TaskStatus, command contracts.TaskCommand) (Transition, error) {
	if command == contracts.CmdCancel {
		if IsTerminal(current) {
			return Transition{}, fmt.Errorf("cannot cancel terminal task status %q", current)
		}
		return Transition{From: current, Command: command, To: contracts.TaskCancelled, Audit: true, EventType: "task.cancelled"}, nil
	}
	for _, transition := range transitions {
		if transition.From == current && transition.Command == command {
			return transition, nil
		}
	}
	return Transition{}, fmt.Errorf("invalid task transition from %q with command %q", current, command)
}

func IsTerminal(status contracts.TaskStatus) bool {
	switch status {
	case contracts.TaskCompleted, contracts.TaskFailed, contracts.TaskCancelled, contracts.TaskRejected:
		return true
	default:
		return false
	}
}
