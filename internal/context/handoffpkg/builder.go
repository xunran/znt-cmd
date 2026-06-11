package handoffpkg

import (
	"context"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type Builder struct {
	Now func() time.Time
}

type Input struct {
	TenantID       contracts.TenantID
	ParentTaskID   contracts.TaskID
	SourceRunID    contracts.AgentRunID
	FromAgentID    contracts.AgentID
	ToAgentID      contracts.AgentID
	Objective      string
	Reason         string
	Summary        string
	ArtifactRefs   []contracts.ArtifactRef
	MemoryRefs     []contracts.MemoryID
	ExpectedOutput contracts.ExpectedOutput
	Mode           contracts.HandoffMode
	Policy         contracts.HandoffPolicy
}

func NewBuilder() Builder {
	return Builder{Now: func() time.Time { return time.Now().UTC() }}
}

func (b Builder) Build(_ context.Context, input Input) (contracts.HandoffContextPackage, error) {
	mode := input.Mode
	if mode == "" {
		mode = input.Policy.DefaultMode
	}
	if mode == "" {
		mode = contracts.HandoffHybrid
	}
	allowed, denied, artifacts, memoryRefs, summary := packageScope(input, mode)
	compressed := false
	summary, artifacts, compressed = enforceTokenBudget(summary, artifacts, input.Policy.MaxContextTokens)
	expectedOutput := input.ExpectedOutput
	if expectedOutput.Format == "" {
		expectedOutput.Format = "text"
	}
	content := map[string]any{
		"parent_task_id":  input.ParentTaskID,
		"source_run_id":   input.SourceRunID,
		"from_agent_id":   input.FromAgentID,
		"to_agent_id":     input.ToAgentID,
		"objective":       input.Objective,
		"reason":          input.Reason,
		"summary":         summary,
		"artifact_refs":   artifacts,
		"memory_refs":     memoryRefs,
		"expected_output": expectedOutput,
		"mode":            mode,
		"allowed_scopes":  allowed,
		"denied_scopes":   denied,
	}
	stableHash, err := hash.StableJSON(content)
	if err != nil {
		return contracts.HandoffContextPackage{}, err
	}
	return contracts.HandoffContextPackage{
		PackageID:            contracts.ContextPackageID(idgen.New("ctxpkg")),
		TenantID:             input.TenantID,
		ParentTaskID:         input.ParentTaskID,
		SourceRunID:          input.SourceRunID,
		FromAgentID:          input.FromAgentID,
		ToAgentID:            input.ToAgentID,
		Objective:            input.Objective,
		Reason:               input.Reason,
		Summary:              summary,
		KeyFacts:             []string{},
		Constraints:          handoffConstraints(input, mode, compressed),
		ArtifactRefs:         artifacts,
		MemoryRefs:           memoryRefs,
		AllowedContextScopes: allowed,
		DeniedContextScopes:  denied,
		ExpectedOutput:       expectedOutput,
		Mode:                 mode,
		Hash:                 stableHash,
		CreatedAt:            b.Now(),
	}, nil
}

func packageScope(input Input, mode contracts.HandoffMode) ([]string, []string, []contracts.ArtifactRef, []contracts.MemoryID, string) {
	summary := input.Summary
	if summary == "" {
		summary = input.Objective
	}
	switch mode {
	case contracts.HandoffFullContext:
		allowed := []string{"summary"}
		denied := make([]string, 0)
		artifacts := []contracts.ArtifactRef(nil)
		memoryRefs := []contracts.MemoryID(nil)
		if input.Policy.AllowArtifactRead {
			allowed = append(allowed, "artifact_refs")
			artifacts = input.ArtifactRefs
		} else {
			denied = append(denied, "artifact_refs")
		}
		if input.Policy.AllowTaskEventRead {
			allowed = append(allowed, "task_events")
		} else {
			denied = append(denied, "task_events")
		}
		if input.Policy.AllowMemoryRead {
			allowed = append(allowed, "memory_refs")
			memoryRefs = input.MemoryRefs
		} else {
			denied = append(denied, "memory_refs")
		}
		return allowed, denied, artifacts, memoryRefs, summary
	case contracts.HandoffSummaryOnly:
		return []string{"summary"}, []string{"artifact_refs", "task_events", "memory_refs"}, nil, nil, summary
	case contracts.HandoffReferenceOnly:
		allowed := make([]string, 0, 2)
		denied := []string{"summary", "task_events"}
		artifacts := []contracts.ArtifactRef(nil)
		memoryRefs := []contracts.MemoryID(nil)
		if input.Policy.AllowArtifactRead {
			allowed = append(allowed, "artifact_refs")
			artifacts = input.ArtifactRefs
		} else {
			denied = append(denied, "artifact_refs")
		}
		if input.Policy.AllowMemoryRead {
			allowed = append(allowed, "memory_refs")
			memoryRefs = input.MemoryRefs
		} else {
			denied = append(denied, "memory_refs")
		}
		return allowed, denied, artifacts, memoryRefs, ""
	default:
		allowed := []string{"summary"}
		denied := make([]string, 0)
		if input.Policy.AllowArtifactRead {
			allowed = append(allowed, "artifact_refs")
		} else {
			denied = append(denied, "artifact_refs")
		}
		artifacts := []contracts.ArtifactRef(nil)
		if input.Policy.AllowArtifactRead {
			artifacts = input.ArtifactRefs
		}
		memoryRefs := []contracts.MemoryID(nil)
		if input.Policy.AllowMemoryRead {
			allowed = append(allowed, "memory_refs")
			memoryRefs = input.MemoryRefs
		} else {
			denied = append(denied, "memory_refs")
		}
		if !input.Policy.AllowTaskEventRead {
			denied = append(denied, "task_events")
		}
		return allowed, denied, artifacts, memoryRefs, summary
	}
}

func handoffConstraints(input Input, mode contracts.HandoffMode, compressed bool) []string {
	constraints := []string{"downstream agent must rebuild WorkView"}
	if input.Policy.MaxContextTokens > 0 {
		constraints = append(constraints, "handoff context max tokens enforced by policy")
	}
	if compressed {
		constraints = append(constraints, "handoff context compressed by max_context_tokens policy")
	}
	if mode == contracts.HandoffReferenceOnly {
		constraints = append(constraints, "reference_only handoff must dereference approved artifacts before use")
	}
	return constraints
}

func enforceTokenBudget(summary string, artifacts []contracts.ArtifactRef, maxTokens int) (string, []contracts.ArtifactRef, bool) {
	if maxTokens <= 0 || handoffTokenCount(summary, artifacts) <= maxTokens {
		return summary, artifacts, false
	}
	compressed := true
	summary = truncateWords(summary, maxTokens)
	remaining := maxTokens - len(strings.Fields(summary))
	if remaining <= 0 {
		return summary, nil, compressed
	}
	kept := make([]contracts.ArtifactRef, 0, len(artifacts))
	for _, artifact := range artifacts {
		cost := artifactTokenCount(artifact)
		if cost <= remaining {
			kept = append(kept, artifact)
			remaining -= cost
		}
	}
	return summary, kept, compressed
}

func handoffTokenCount(summary string, artifacts []contracts.ArtifactRef) int {
	total := len(strings.Fields(summary))
	for _, artifact := range artifacts {
		total += artifactTokenCount(artifact)
	}
	return total
}

func artifactTokenCount(artifact contracts.ArtifactRef) int {
	return len(strings.Fields(strings.Join([]string{
		string(artifact.ArtifactID),
		artifact.Type,
		artifact.URI,
		artifact.Summary,
		artifact.Hash,
	}, " ")))
}

func truncateWords(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	words := strings.Fields(value)
	if len(words) <= limit {
		return value
	}
	return strings.Join(words[:limit], " ")
}
