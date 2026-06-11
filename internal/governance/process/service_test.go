package process

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
)

func TestMultiReviewMajorityPassesGateAndCompletesRun(t *testing.T) {
	ctx := context.Background()
	traceRecorder := trace.NewInMemoryRecorder()
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(NewInMemoryStore(), auditLogger, traceRecorder)
	service.Now = fixedNow()

	template, err := service.UpsertTemplate(ctx, UpsertTemplateInput{
		TenantID: "tenant_1",
		Name:     "governed process",
		ActorID:  "owner",
		Gates: []contracts.GovernanceGateDefinition{{
			GateID:            "quality_gate",
			ReviewMode:        contracts.GovernanceReviewMulti,
			ConsensusPolicy:   contracts.GovernanceConsensusMajority,
			EscalationPolicy:  contracts.GovernanceEscalationOrchestrator,
			RequiredReviewers: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.StartRun(ctx, StartRunInput{
		TenantID:    "tenant_1",
		TemplateID:  template.TemplateID,
		SubjectType: "artifact",
		SubjectID:   "artifact_1",
		TraceID:     "trace_1",
		ActorID:     "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProcessRun.Status != contracts.GovernanceRunActive || len(snapshot.Gates) != 1 {
		t.Fatalf("unexpected start snapshot: %#v", snapshot)
	}

	gateID := snapshot.Gates[0].GateRunID
	snapshot, err = service.OpenGate(ctx, OpenGateInput{
		TenantID:  "tenant_1",
		GateRunID: gateID,
		EvidenceRefs: []contracts.GovernanceEvidenceRef{{
			Type:    "trace",
			TraceID: "trace_1",
			Summary: "candidate output",
		}},
		ActorID: "producer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGateOpen {
		t.Fatalf("expected open gate, got %#v", snapshot.Gates[0])
	}

	for _, reviewer := range []string{"reviewer_1", "reviewer_2"} {
		snapshot, err = service.SubmitReview(ctx, ReviewInput{
			TenantID:     "tenant_1",
			GateRunID:    gateID,
			ReviewerID:   reviewer,
			ReviewerType: "agent",
			Decision:     contracts.GovernanceReviewApprove,
			Reason:       "looks good",
			Independent:  true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGateOpen {
		t.Fatalf("gate should wait for required reviewer count, got %#v", snapshot.Gates[0])
	}

	snapshot, err = service.SubmitReview(ctx, ReviewInput{
		TenantID:     "tenant_1",
		GateRunID:    gateID,
		ReviewerID:   "reviewer_3",
		ReviewerType: "agent",
		Decision:     contracts.GovernanceReviewReject,
		Reason:       "minor concern",
		Independent:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGatePassed {
		t.Fatalf("expected majority pass, got %#v", snapshot.Gates[0])
	}
	if snapshot.ProcessRun.Status != contracts.GovernanceRunCompleted {
		t.Fatalf("expected completed run, got %#v", snapshot.ProcessRun)
	}

	events, err := traceRecorder.ListByTrace(ctx, "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected governance trace events")
	}
	audits, err := auditLogger.Search(ctx, audit.Filter{TenantID: "tenant_1", ResourceID: string(gateID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 {
		t.Fatal("expected governance audit events")
	}
}

func TestSplitReviewsEscalateAndArbitrate(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewInMemoryStore(), audit.NewInMemoryLogger(), trace.NewInMemoryRecorder())
	service.Now = fixedNow()
	snapshot, err := service.StartRun(ctx, StartRunInput{
		TenantID:    "tenant_1",
		SubjectType: "task",
		SubjectID:   "task_1",
		Gates: []contracts.GovernanceGateDefinition{{
			GateID:            "committee",
			ReviewMode:        contracts.GovernanceReviewMulti,
			ConsensusPolicy:   contracts.GovernanceConsensusAll,
			EscalationPolicy:  contracts.GovernanceEscalationOrchestrator,
			RequiredReviewers: 2,
		}},
		ActorID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	gateID := snapshot.Gates[0].GateRunID
	if _, err := service.OpenGate(ctx, OpenGateInput{TenantID: "tenant_1", GateRunID: gateID, ActorID: "producer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitReview(ctx, ReviewInput{TenantID: "tenant_1", GateRunID: gateID, ReviewerID: "a", Decision: contracts.GovernanceReviewApprove}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.SubmitReview(ctx, ReviewInput{TenantID: "tenant_1", GateRunID: gateID, ReviewerID: "b", Decision: contracts.GovernanceReviewReject})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGateEscalationPending {
		t.Fatalf("expected escalation pending, got %#v", snapshot.Gates[0])
	}

	snapshot, err = service.Escalate(ctx, EscalateInput{TenantID: "tenant_1", GateRunID: gateID, Issue: "reviewers split", ActorID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Conflicts) != 1 || snapshot.Conflicts[0].Status != "open" {
		t.Fatalf("expected open conflict, got %#v", snapshot.Conflicts)
	}

	snapshot, err = service.Arbitrate(ctx, ArbitrateInput{
		TenantID:     "tenant_1",
		ConflictID:   snapshot.Conflicts[0].ConflictID,
		Decision:     contracts.GovernanceReviewApprove,
		Reason:       "evidence supports approval",
		ArbitratorID: "orchestrator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGateArbitrated || snapshot.ProcessRun.Status != contracts.GovernanceRunCompleted {
		t.Fatalf("unexpected arbitrated snapshot: %#v", snapshot)
	}
}

func TestReviewModeNonePassesWithoutReview(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewInMemoryStore(), nil, nil)
	service.Now = fixedNow()
	snapshot, err := service.StartRun(ctx, StartRunInput{
		TenantID:    "tenant_1",
		SubjectType: "artifact",
		SubjectID:   "artifact_1",
		Gates: []contracts.GovernanceGateDefinition{{
			GateID:     "optional",
			ReviewMode: contracts.GovernanceReviewNone,
		}},
		ActorID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.OpenGate(ctx, OpenGateInput{TenantID: "tenant_1", GateRunID: snapshot.Gates[0].GateRunID, ActorID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Gates[0].Status != contracts.GovernanceGatePassed || snapshot.ProcessRun.Status != contracts.GovernanceRunCompleted {
		t.Fatalf("expected pass-through governance, got %#v", snapshot)
	}
}

func fixedNow() func() time.Time {
	return func() time.Time {
		return time.Unix(100, 0).UTC()
	}
}
