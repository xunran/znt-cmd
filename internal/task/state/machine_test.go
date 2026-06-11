package state

import (
	"testing"

	"znt/internal/contracts"
)

func TestApplyValidTransitions(t *testing.T) {
	transition, err := Apply(contracts.TaskCreated, contracts.CmdAccept)
	if err != nil {
		t.Fatal(err)
	}
	if transition.To != contracts.TaskAccepted {
		t.Fatalf("expected accepted, got %s", transition.To)
	}
	transition, err = Apply(contracts.TaskRunning, contracts.CmdApprovalRequired)
	if err != nil {
		t.Fatal(err)
	}
	if !transition.Audit || transition.To != contracts.TaskWaitingApproval {
		t.Fatalf("expected audited waiting approval transition, got %#v", transition)
	}
}

func TestApplyRejectsInvalidTransitions(t *testing.T) {
	if _, err := Apply(contracts.TaskCompleted, contracts.CmdResume); err == nil {
		t.Fatal("expected terminal task transition to fail")
	}
	if _, err := Apply(contracts.TaskCreated, contracts.CmdComplete); err == nil {
		t.Fatal("expected invalid transition to fail")
	}
}
