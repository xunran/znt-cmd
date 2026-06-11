package handoffpkg

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestBuilderDefaultsHybridAndHashes(t *testing.T) {
	pkg, err := NewBuilder().Build(context.Background(), Input{
		ParentTaskID: "task_1",
		SourceRunID:  "run_1",
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		Reason:       "needs expert",
		Summary:      "summary",
		Policy:       contracts.HandoffPolicy{DefaultMode: contracts.HandoffHybrid},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Mode != contracts.HandoffHybrid || pkg.Hash == "" {
		t.Fatalf("unexpected package: %#v", pkg)
	}
}

func TestBuilderEnforcesMaxContextTokens(t *testing.T) {
	pkg, err := NewBuilder().Build(context.Background(), Input{
		ParentTaskID: "task_1",
		SourceRunID:  "run_1",
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		Reason:       "needs expert",
		Summary:      "one two three four five",
		ArtifactRefs: []contracts.ArtifactRef{{
			ArtifactID: "artifact_1",
			Type:       "text",
			URI:        "memory://artifact_1",
			Summary:    "artifact summary",
		}},
		Policy: contracts.HandoffPolicy{
			DefaultMode:      contracts.HandoffFullContext,
			AllowFullContext: true,
			MaxContextTokens: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Summary != "one two three" {
		t.Fatalf("expected summary to be truncated, got %q", pkg.Summary)
	}
	if len(pkg.ArtifactRefs) != 0 {
		t.Fatalf("expected artifacts beyond budget to be dropped, got %#v", pkg.ArtifactRefs)
	}
	if !hasConstraint(pkg.Constraints, "handoff context compressed by max_context_tokens policy") {
		t.Fatalf("expected compression constraint, got %#v", pkg.Constraints)
	}
}

func TestBuilderFullContextRespectsScopeFlags(t *testing.T) {
	pkg, err := NewBuilder().Build(context.Background(), Input{
		ParentTaskID: "task_1",
		SourceRunID:  "run_1",
		FromAgentID:  "agent_a",
		ToAgentID:    "agent_b",
		Objective:    "review",
		Summary:      "summary",
		ArtifactRefs: []contracts.ArtifactRef{{ArtifactID: "artifact_1", Summary: "artifact"}},
		Mode:         contracts.HandoffFullContext,
		Policy: contracts.HandoffPolicy{
			AllowFullContext:   true,
			AllowArtifactRead:  false,
			AllowTaskEventRead: true,
			AllowMemoryRead:    false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.ArtifactRefs) != 0 {
		t.Fatalf("expected artifact refs to be hidden, got %#v", pkg.ArtifactRefs)
	}
	if !hasConstraintValue(pkg.AllowedContextScopes, "task_events") || !hasConstraintValue(pkg.DeniedContextScopes, "artifact_refs") || !hasConstraintValue(pkg.DeniedContextScopes, "memory_refs") {
		t.Fatalf("unexpected scopes allowed=%#v denied=%#v", pkg.AllowedContextScopes, pkg.DeniedContextScopes)
	}
}

func hasConstraint(values []string, target string) bool {
	return hasConstraintValue(values, target)
}

func hasConstraintValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
