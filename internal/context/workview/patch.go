package workview

import (
	"strings"

	contextcollector "znt/internal/context/collector"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
	"znt/pkg/idgen"
)

func ApplyRuntimeHookPatch(view *contracts.WorkView, patch runtimehook.Patch) {
	for _, ref := range patch.DropContextRefs {
		switch ref {
		case "conversation":
			view.ConversationContext = nil
		case "memory":
			view.MemorySummaries = nil
		case "artifacts":
			view.ArtifactRefs = nil
		case "tool_results":
			view.ToolResultSummaries = nil
		case "capabilities":
			view.CandidateCapabilities = nil
		case "skills":
			view.CandidateSkills = nil
			view.CandidateSkillInstructions = nil
		case "tools":
			view.CandidateTools = nil
		case "collaborators":
			view.CandidateCollaborators = nil
		}
	}
	for _, block := range patch.AddContextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		id := contracts.ArtifactID(block.ID)
		if id == "" {
			id = contracts.ArtifactID(idgen.New("hookctx"))
		}
		summary := block.Content
		if block.Title != "" {
			summary = block.Title + ": " + block.Content
		}
		view.ArtifactRefs = append(view.ArtifactRefs, contracts.ArtifactRef{
			ArtifactID: id,
			Type:       contextcollector.ContextBlockSourceType(block),
			Summary:    summary,
			Metadata:   contextcollector.ContextBlockArtifactMetadata(block),
		})
	}
	for _, hint := range patch.PlannerHints {
		if strings.TrimSpace(hint.Content) != "" {
			view.Constraints = append(view.Constraints, "planner hint: "+hint.Content)
		}
	}
}
