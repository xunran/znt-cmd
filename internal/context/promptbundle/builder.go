package promptbundle

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type Builder struct {
	now func() time.Time
}

func NewBuilder() Builder {
	return Builder{now: func() time.Time { return time.Now().UTC() }}
}

func (b Builder) Build(_ context.Context, agent contracts.AgentDefinition, view contracts.WorkView) (contracts.PromptBundle, error) {
	contextText := renderContext(view)
	bundle := contracts.PromptBundle{
		BundleID: idgen.New("promptbundle"),
		RunID:    view.RunID,
		System:   sourceBlock("system instructions", agent.SystemPrompt),
		Developer: strings.Join([]string{
			sourceBlock("developer instructions", agent.DeveloperPrompt),
			sourceBlock("agent package instructions", agent.IdentityPrompt),
		}, "\n"),
		Task:      sourceBlock("task objective", view.TaskSummary.Objective),
		Context:   contextText,
		ToolCards: view.CandidateTools,
		OutputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"type",
			},
			"properties": map[string]any{
				"type": map[string]any{"enum": []string{"reply", "ask_clarification", "tool_call", "unsupported", "error", "no_op"}},
			},
		},
		Constraints: view.Constraints,
		CreatedAt:   b.now(),
	}
	seenSkillInstructions := map[string]struct{}{}
	for _, instruction := range view.CandidateSkillInstructions {
		if strings.TrimSpace(instruction.Content) == "" {
			continue
		}
		seenSkillInstructions[instruction.SkillID] = struct{}{}
		bundle.SkillInstructions = append(bundle.SkillInstructions, renderSkillInstruction(instruction))
	}
	for _, skill := range view.CandidateSkills {
		if _, ok := seenSkillInstructions[skill.SkillID]; ok {
			continue
		}
		bundle.SkillInstructions = append(bundle.SkillInstructions, sourceBlock("skill instruction "+skill.SkillID, fmt.Sprintf("%s: %s", skill.Name, strings.Join(skill.WhenToUse, "; "))))
	}
	if len(bundle.SkillInstructions) > 0 {
		bundle.Developer = strings.Join(append([]string{bundle.Developer}, bundle.SkillInstructions...), "\n")
	}
	stable, err := hash.StableJSON(map[string]any{
		"system":      bundle.System,
		"developer":   bundle.Developer,
		"task":        bundle.Task,
		"context":     bundle.Context,
		"constraints": bundle.Constraints,
		"skills":      bundle.SkillInstructions,
		"tools":       bundle.ToolCards,
	})
	if err != nil {
		return contracts.PromptBundle{}, err
	}
	bundle.Hash = stable
	return bundle, nil
}

func renderSkillInstruction(instruction contracts.SkillInstruction) string {
	parts := []string{instruction.Content}
	if len(instruction.OutputRequirements) > 0 {
		parts = append(parts, "output requirements: "+strings.Join(instruction.OutputRequirements, "; "))
	}
	if len(instruction.Constraints) > 0 {
		parts = append(parts, "constraints: "+strings.Join(instruction.Constraints, "; "))
	}
	return sourceBlock("skill instruction "+instruction.SkillID, strings.Join(parts, "\n"))
}

func renderContext(view contracts.WorkView) string {
	parts := []string{
		sourceBlock("user input", view.UserInput),
		sourceBlock("task summary", fmt.Sprintf("task_id=%s status=%s title=%s", view.TaskSummary.TaskID, view.TaskSummary.Status, view.TaskSummary.Title)),
	}
	if view.ConversationContext != nil {
		parts = append(parts, renderConversationContext(*view.ConversationContext)...)
	}
	if view.PlanSummary != nil {
		parts = append(parts, sourceBlock("task plan", fmt.Sprintf("plan_id=%s status=%s objective=%s", view.PlanSummary.PlanID, view.PlanSummary.Status, view.PlanSummary.Objective)))
	}
	if view.CurrentPlanStep != nil {
		parts = append(parts, sourceBlock("current plan step", fmt.Sprintf("step_id=%s index=%d status=%s title=%s", view.CurrentPlanStep.StepID, view.CurrentPlanStep.Index, view.CurrentPlanStep.Status, view.CurrentPlanStep.Title)))
	}
	if len(view.TaskHistory) > 0 {
		lines := make([]string, 0, len(view.TaskHistory))
		for _, item := range view.TaskHistory {
			lines = append(lines, renderRetrievedContext(item))
		}
		parts = append(parts, sourceBlock("task history", strings.Join(lines, "\n\n")))
	}
	if view.HandoffContext != nil {
		parts = append(parts, sourceBlock("handoff context", fmt.Sprintf("package_id=%s from=%s mode=%s summary=%s", view.HandoffContext.PackageID, view.HandoffContext.FromAgent, view.HandoffContext.Mode, view.HandoffContext.Summary)))
	}
	for _, memory := range view.MemorySummaries {
		parts = append(parts, sourceBlock("memory summary", fmt.Sprintf("%s %s", memory.MemoryID, memory.Summary)))
	}
	for _, artifact := range view.ArtifactRefs {
		parts = append(parts, sourceBlock("artifact summary", fmt.Sprintf("%s %s %s", artifact.ArtifactID, artifact.Type, artifact.Summary)))
	}
	for _, mark := range view.RiskMarks {
		parts = append(parts, sourceBlock("risk mark", fmt.Sprintf("%s: %s", mark.Level, mark.Reason)))
	}
	for _, result := range view.ToolResultSummaries {
		parts = append(parts, sourceBlock("tool result", fmt.Sprintf("%s %s %s", result.ToolCallID, result.Status, result.Summary)))
	}
	for _, capability := range view.CandidateCapabilities {
		parts = append(parts, sourceBlock("retrieved capability", fmt.Sprintf("%s/%s: %s", capability.Type, capability.Name, capability.Description)))
	}
	for _, skill := range view.CandidateSkills {
		parts = append(parts, sourceBlock("retrieved skill card", fmt.Sprintf("%s: %s", skill.Name, skill.Description)))
	}
	for _, collaborator := range view.CandidateCollaborators {
		parts = append(parts, sourceBlock("retrieved collaborator card", fmt.Sprintf("%s agent_id=%s version=%s alias=%s capabilities=%s when_to_use=%s", collaborator.Name, collaborator.AgentID, collaborator.Version, collaborator.Alias, strings.Join(collaborator.Capabilities, "; "), strings.Join(collaborator.WhenToUse, "; "))))
	}
	for _, tool := range view.CandidateTools {
		parts = append(parts, sourceBlock("retrieved tool card", fmt.Sprintf("%s: %s", tool.Name, tool.Description)))
	}
	return strings.Join(parts, "\n")
}

func renderConversationContext(conversation contracts.ConversationContext) []string {
	parts := make([]string, 0, 4)
	current := conversation.CurrentMessage
	lines := []string{
		fmt.Sprintf("kind=%s", conversation.Kind),
		fmt.Sprintf("current_speaker_id=%s", current.SpeakerID),
		fmt.Sprintf("current_speaker_type=%s", current.SpeakerType),
		fmt.Sprintf("current_speaker_name=%s", current.SpeakerName),
		fmt.Sprintf("message_id=%s", current.MessageID),
		fmt.Sprintf("external_message_id=%s", current.ExternalMessageID),
		fmt.Sprintf("reply_to_message_id=%s", current.ReplyToMessageID),
		fmt.Sprintf("thread_id=%s", current.ThreadID),
	}
	if addressing := conversation.Addressing; addressing != nil {
		lines = append(lines,
			fmt.Sprintf("addressed_to_agent=%t", addressing.AddressedToAgent),
			fmt.Sprintf("addressing_confidence=%.2f", addressing.Confidence),
			fmt.Sprintf("addressing_reason=%s", addressing.Reason),
			fmt.Sprintf("addressing_source=%s", addressing.DecisionSource),
			fmt.Sprintf("suggested_action=%s", addressing.SuggestedAction),
		)
		if len(addressing.Signals) > 0 {
			lines = append(lines, "addressing_signals="+strings.Join(addressing.Signals, ", "))
		}
		if len(addressing.AddresseeIDs) > 0 {
			lines = append(lines, "addressee_ids="+strings.Join(addressing.AddresseeIDs, ", "))
		}
	}
	parts = append(parts, sourceBlock("conversation context", strings.Join(lines, "\n")))
	if len(conversation.Participants) > 0 {
		lines := make([]string, 0, len(conversation.Participants))
		for _, participant := range conversation.Participants {
			lines = append(lines, fmt.Sprintf("%s type=%s name=%s role=%s", participant.ID, participant.Type, participant.Name, participant.Role))
		}
		parts = append(parts, sourceBlock("conversation participants", strings.Join(lines, "\n")))
	}
	if len(conversation.RecentMessages) > 0 {
		lines := make([]string, 0, len(conversation.RecentMessages))
		for _, message := range conversation.RecentMessages {
			lines = append(lines, renderConversationMessage(message))
		}
		parts = append(parts, sourceBlock("recent messages", strings.Join(lines, "\n")))
	}
	if sufficiency := conversation.Sufficiency; sufficiency != nil {
		lines := []string{
			fmt.Sprintf("phase=%s", sufficiency.Phase),
			fmt.Sprintf("sufficient=%t", sufficiency.Sufficient),
			fmt.Sprintf("confidence=%.2f", sufficiency.Confidence),
			fmt.Sprintf("reason=%s", sufficiency.Reason),
			fmt.Sprintf("retrieval_needed=%t", sufficiency.RetrievalNeeded),
			fmt.Sprintf("suggested_action=%s", sufficiency.SuggestedAction),
		}
		if len(sufficiency.MissingFacts) > 0 {
			lines = append(lines, "missing_facts:")
			for _, fact := range sufficiency.MissingFacts {
				lines = append(lines, "- "+fact)
			}
		}
		if len(sufficiency.Queries) > 0 {
			lines = append(lines, "retrieval_queries:")
			for _, query := range sufficiency.Queries {
				lines = append(lines, fmt.Sprintf("- query=%s sources=%s thread_id=%s time_hint=%s", query.Query, strings.Join(query.Sources, ","), query.ThreadID, query.TimeHint))
			}
		}
		parts = append(parts, sourceBlock("context sufficiency", strings.Join(lines, "\n")))
	}
	if len(conversation.Retrieved) > 0 {
		lines := make([]string, 0, len(conversation.Retrieved))
		for _, item := range conversation.Retrieved {
			lines = append(lines, renderRetrievedContext(item))
		}
		parts = append(parts, sourceBlock("retrieved context", strings.Join(lines, "\n\n")))
	}
	return parts
}

func renderConversationMessage(message contracts.ConversationMessage) string {
	createdAt := ""
	if !message.CreatedAt.IsZero() {
		createdAt = message.CreatedAt.UTC().Format(time.RFC3339)
	}
	prefix := fmt.Sprintf("[%s] %s %s", createdAt, message.SpeakerID, message.SpeakerName)
	meta := []string{}
	if message.MessageID != "" {
		meta = append(meta, "message_id="+message.MessageID)
	}
	if message.ReplyToMessageID != "" {
		meta = append(meta, "reply_to="+message.ReplyToMessageID)
	}
	if message.ThreadID != "" {
		meta = append(meta, "thread_id="+message.ThreadID)
	}
	if len(meta) > 0 {
		prefix += " (" + strings.Join(meta, " ") + ")"
	}
	return strings.TrimSpace(prefix) + ": " + message.Text
}

func renderRetrievedContext(item contracts.RetrievedContext) string {
	createdAt := ""
	if !item.CreatedAt.IsZero() {
		createdAt = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	lines := []string{
		fmt.Sprintf("[%s %s relevance=%.2f recency=%.2f created_at=%s trust=%s visibility=%s input_boundary=untrusted]", item.SourceType, item.SourceID, item.Relevance, item.RecencyScore, createdAt, item.TrustLevel, item.Visibility),
	}
	if item.SpeakerID != "" || item.SpeakerName != "" {
		lines = append(lines, fmt.Sprintf("speaker=%s %s", item.SpeakerID, item.SpeakerName))
	}
	if item.Summary != "" {
		lines = append(lines, "summary="+item.Summary)
	}
	if item.Snippet != "" {
		lines = append(lines, "snippet="+item.Snippet)
	}
	return strings.Join(lines, "\n")
}

func sourceBlock(source string, content string) string {
	source = sanitizeSourceLabel(source)
	if strings.TrimSpace(content) == "" {
		return fmt.Sprintf("<%s>\n</%s>", source, source)
	}
	return fmt.Sprintf("<%s>\n%s\n</%s>", source, escapeSourceContent(content), source)
}

func sanitizeSourceLabel(source string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(source) {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == ' ' || r == '.' || r == '_' || r == '-':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "source"
	}
	return out.String()
}

func escapeSourceContent(content string) string {
	return html.EscapeString(content)
}
