package handoff

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestEvaluateDeniesFullContextWhenDisabled(t *testing.T) {
	decision, err := New().Evaluate(context.Background(), contracts.HandoffPolicy{AllowFullContext: false}, contracts.HandoffFullContext, nil)
	if err == nil {
		t.Fatal("expected denied error")
	}
	if decision.Decision != "denied" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluateArtifactApproval(t *testing.T) {
	decision, err := New().Evaluate(context.Background(), contracts.HandoffPolicy{
		AllowArtifactRead:                    true,
		RequireApprovalForSensitiveArtifacts: true,
	}, contracts.HandoffHybrid, []contracts.ArtifactRef{{ArtifactID: "artifact_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "approval_required" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluateRequestDeniesMissingTargetAgent(t *testing.T) {
	decision, err := New().EvaluateRequest(context.Background(), EvaluateRequest{
		Policy:            contracts.HandoffPolicy{DefaultMode: contracts.HandoffHybrid},
		FromAgentID:       "agent_a",
		ToAgentID:         "agent_b",
		ToAgentExists:     false,
		CapabilityMatched: true,
	})
	if err == nil {
		t.Fatal("expected missing target agent to be denied")
	}
	if decision.Decision != contracts.PolicyDecisionDenied {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluateRequestDeniesMemoryRefsWhenDisabled(t *testing.T) {
	decision, err := New().EvaluateRequest(context.Background(), EvaluateRequest{
		Policy:            contracts.HandoffPolicy{DefaultMode: contracts.HandoffHybrid, AllowMemoryRead: false},
		FromAgentID:       "agent_a",
		ToAgentID:         "agent_b",
		ToAgentExists:     true,
		CapabilityMatched: true,
		MemoryRefs:        []contracts.MemoryID{"memory_1"},
	})
	if err == nil {
		t.Fatal("expected memory refs to be denied")
	}
	if decision.Decision != contracts.PolicyDecisionDenied {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEvaluateRequestRequiresApprovalForCrossAgent(t *testing.T) {
	decision, err := New().EvaluateRequest(context.Background(), EvaluateRequest{
		Policy: contracts.HandoffPolicy{
			DefaultMode:                  contracts.HandoffHybrid,
			RequireApprovalForCrossAgent: true,
		},
		FromAgentID:       "agent_a",
		ToAgentID:         "agent_b",
		ToAgentExists:     true,
		CapabilityMatched: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != contracts.PolicyDecisionApprovalRequired {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}
