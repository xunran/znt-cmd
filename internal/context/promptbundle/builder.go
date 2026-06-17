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
	contextText, contextSteps := renderContext(view)
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
	bundle.AssemblySteps = append(bundle.AssemblySteps,
		promptStep("system-instructions", "加入智能体系统提示词", "agent_system_prompt", "智能体配置", "智能体详情 > 基础提示词", "system", "system", "当前运行使用该智能体", agent.SystemPrompt),
		promptStep("developer-instructions", "加入开发者提示词", "agent_developer_prompt", "智能体配置", "智能体详情 > 开发者提示词", "system", "developer", "当前智能体配置了开发者指令", agent.DeveloperPrompt),
		promptStep("agent-identity", "加入智能体身份说明", "agent_identity_prompt", "智能体配置", "智能体详情 > 身份说明", "system", "developer", "帮助模型理解当前智能体角色", agent.IdentityPrompt),
		promptStep("task-objective", "加入任务目标", "task_objective", "运行任务", "重新发起运行或修改任务输入", "user", "task", "本次运行的目标", view.TaskSummary.Objective),
	)
	bundle.AssemblySteps = append(bundle.AssemblySteps, contextSteps...)
	if view.ContextAssemblyReport != nil {
		report := *view.ContextAssemblyReport
		bundle.ContextAssemblyReport = &report
	}
	seenSkillInstructions := map[string]struct{}{}
	for _, instruction := range view.CandidateSkillInstructions {
		if strings.TrimSpace(instruction.Content) == "" {
			continue
		}
		seenSkillInstructions[instruction.SkillID] = struct{}{}
		rendered := renderSkillInstruction(instruction)
		bundle.SkillInstructions = append(bundle.SkillInstructions, rendered)
		bundle.AssemblySteps = append(bundle.AssemblySteps, promptStep(
			"skill-instruction-"+instruction.SkillID,
			"加入技能执行指令",
			"skill_instruction",
			"技能配置",
			"技能管理 > "+instruction.SkillID,
			"system",
			"developer",
			"候选技能命中本次任务",
			rendered,
		))
	}
	for _, skill := range view.CandidateSkills {
		if _, ok := seenSkillInstructions[skill.SkillID]; ok {
			continue
		}
		rendered := sourceBlock("skill instruction "+skill.SkillID, fmt.Sprintf("%s: %s", skill.Name, strings.Join(skill.WhenToUse, "; ")))
		bundle.SkillInstructions = append(bundle.SkillInstructions, rendered)
		bundle.AssemblySteps = append(bundle.AssemblySteps, promptStep(
			"skill-card-"+skill.SkillID,
			"加入技能使用说明",
			"skill_card",
			"技能配置",
			"技能管理 > "+skill.SkillID,
			"system",
			"developer",
			"候选技能进入本次上下文",
			rendered,
		))
	}
	if len(bundle.SkillInstructions) > 0 {
		bundle.Developer = strings.Join(append([]string{bundle.Developer}, bundle.SkillInstructions...), "\n")
	}
	if err := RefreshHash(&bundle); err != nil {
		return contracts.PromptBundle{}, err
	}
	return bundle, nil
}

func RefreshHash(bundle *contracts.PromptBundle) error {
	stable, err := hash.StableJSON(map[string]any{
		"system":         bundle.System,
		"developer":      bundle.Developer,
		"task":           bundle.Task,
		"context":        bundle.Context,
		"context_report": bundle.ContextAssemblyReport,
		"constraints":    bundle.Constraints,
		"output_schema":  bundle.OutputSchema,
		"skills":         bundle.SkillInstructions,
		"tools":          bundle.ToolCards,
	})
	if err != nil {
		return err
	}
	bundle.Hash = stable
	return nil
}

func promptStep(stepID string, title string, sourceType string, sourceLabel string, editTarget string, role string, section string, reason string, content string) contracts.PromptAssemblyStep {
	content = strings.TrimSpace(content)
	return contracts.PromptAssemblyStep{
		StepID:         stepID,
		Title:          title,
		SourceType:     sourceType,
		SourceLabel:    sourceLabel,
		EditTarget:     editTarget,
		MessageRole:    role,
		PromptSection:  section,
		Reason:         reason,
		ContentPreview: previewText(content, 320),
		TokensEstimate: estimateTokens(content),
		Included:       content != "",
	}
}

func previewText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	words := len(strings.Fields(value))
	runes := len([]rune(value))
	charEstimate := (runes + 3) / 4
	if words > charEstimate {
		return words
	}
	return charEstimate
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

func renderContext(view contracts.WorkView) (string, []contracts.PromptAssemblyStep) {
	parts := []string{}
	steps := []contracts.PromptAssemblyStep{}
	addContext := func(stepID string, title string, sourceType string, sourceLabel string, editTarget string, reason string, content string) {
		parts = append(parts, content)
		steps = append(steps, promptStep(stepID, title, sourceType, sourceLabel, editTarget, "user", "context", reason, content))
	}
	addContext(
		"user-input",
		"加入用户输入",
		"user_input",
		"本次运行输入",
		"重新发起运行",
		"模型需要理解用户当前问题",
		sourceBlock("user input", renderDynamicContext("user_input", "user_input:"+string(view.RunID), "untrusted_user_text", view.UserInput)),
	)
	addContext(
		"task-summary",
		"加入任务摘要",
		"task_summary",
		"任务运行上下文",
		"任务管理 / 运行上下文",
		"帮助模型了解任务状态",
		sourceBlock("task summary", fmt.Sprintf("task_id=%s status=%s title=%s", view.TaskSummary.TaskID, view.TaskSummary.Status, view.TaskSummary.Title)),
	)
	if view.ConversationContext != nil {
		conversationParts := renderConversationContext(*view.ConversationContext)
		if len(conversationParts) > 0 {
			content := strings.Join(conversationParts, "\n")
			addContext("conversation-context", "加入会话上下文", "conversation_context", "会话历史", "上下文策略 > 会话历史", "本次运行来自会话或需要参考最近消息", content)
		}
	}
	if view.PlanSummary != nil {
		addContext("task-plan", "加入任务计划", "task_plan", "任务计划", "任务计划配置", "当前任务已有计划信息", sourceBlock("task plan", fmt.Sprintf("plan_id=%s status=%s objective=%s", view.PlanSummary.PlanID, view.PlanSummary.Status, view.PlanSummary.Objective)))
	}
	if view.CurrentPlanStep != nil {
		addContext("current-plan-step", "加入当前计划步骤", "current_plan_step", "任务计划", "任务计划配置", "模型需要知道当前执行到哪一步", sourceBlock("current plan step", fmt.Sprintf("step_id=%s index=%d status=%s title=%s", view.CurrentPlanStep.StepID, view.CurrentPlanStep.Index, view.CurrentPlanStep.Status, view.CurrentPlanStep.Title)))
	}
	if len(view.TaskHistory) > 0 {
		lines := make([]string, 0, len(view.TaskHistory))
		for _, item := range view.TaskHistory {
			lines = append(lines, renderRetrievedContext(item))
		}
		addContext("task-history", "加入任务历史", "task_history", "历史任务记录", "上下文策略 > 任务历史", "上下文策略选择了历史任务", sourceBlock("task history", strings.Join(lines, "\n\n")))
	}
	if view.HandoffContext != nil {
		addContext("handoff-context", "加入协作交接上下文", "handoff_context", "协作智能体", "协作 / 交接配置", "本次任务来自其他智能体交接", sourceBlock("handoff context", fmt.Sprintf("package_id=%s from=%s mode=%s summary=%s", view.HandoffContext.PackageID, view.HandoffContext.FromAgent, view.HandoffContext.Mode, view.HandoffContext.Summary)))
	}
	for _, memory := range view.MemorySummaries {
		addContext("memory-"+string(memory.MemoryID), "加入记忆摘要", "memory_summary", "记忆", "上下文策略 > 记忆", "记忆策略选择了相关记忆", sourceBlock("memory summary", renderMemorySummary(memory)))
	}
	for _, artifact := range view.ArtifactRefs {
		addContext("artifact-"+string(artifact.ArtifactID), "加入产物摘要", "artifact_summary", "运行产物", "产物 / 文件上下文", "本次上下文包含相关产物", sourceBlock("artifact summary", renderArtifactRef(artifact)))
	}
	for _, mark := range view.RiskMarks {
		addContext("risk-mark-"+string(mark.Level), "加入风险标记", "risk_mark", "风控策略", "风控 / 审批策略", "风险策略命中了本次运行", sourceBlock("risk mark", fmt.Sprintf("%s: %s", mark.Level, mark.Reason)))
	}
	for _, result := range view.ToolResultSummaries {
		addContext("tool-result-"+string(result.ToolCallID), "加入工具结果", "tool_result", "工具调用结果", "工具调用记录", "模型需要基于工具返回继续决策或回复", sourceBlock("tool result", renderToolResultSummary(result)))
	}
	for _, capability := range view.CandidateCapabilities {
		addContext("capability-"+capability.ID, "加入能力卡片", "capability_card", "能力目录", "能力配置", "能力检索命中本次任务", sourceBlock("retrieved capability", fmt.Sprintf("%s/%s: %s", capability.Type, capability.Name, capability.Description)))
	}
	for _, skill := range view.CandidateSkills {
		addContext("retrieved-skill-"+skill.SkillID, "加入技能卡片", "skill_card", "技能配置", "技能管理 > "+skill.SkillID, "技能检索命中本次任务", sourceBlock("retrieved skill card", fmt.Sprintf("%s: %s", skill.Name, skill.Description)))
	}
	for _, collaborator := range view.CandidateCollaborators {
		addContext("collaborator-"+string(collaborator.AgentID), "加入协作智能体卡片", "collaborator_card", "协作智能体配置", "协作智能体 > "+string(collaborator.AgentID), "候选协作智能体可处理部分任务", sourceBlock("retrieved collaborator card", fmt.Sprintf("%s agent_id=%s version=%s alias=%s capabilities=%s when_to_use=%s", collaborator.Name, collaborator.AgentID, collaborator.Version, collaborator.Alias, strings.Join(collaborator.Capabilities, "; "), strings.Join(collaborator.WhenToUse, "; "))))
	}
	for _, tool := range view.CandidateTools {
		addContext("tool-card-"+tool.ToolID, "加入工具说明", "tool_card", "工具目录", "工具接入 > "+tool.ToolID, "工具检索命中本次任务，模型可选择调用", sourceBlock("retrieved tool card", fmt.Sprintf("%s: %s", tool.Name, tool.Description)))
	}
	return strings.Join(parts, "\n"), steps
}

func renderDynamicContext(sourceType string, sourceRef string, trustLevel string, content string) string {
	header := contextMetadataLine(sourceType, sourceRef, trustLevel)
	if strings.TrimSpace(content) == "" {
		return header
	}
	return header + "\n" + content
}

func renderMemorySummary(memory contracts.MemorySummary) string {
	body := strings.TrimSpace(fmt.Sprintf("%s %s", memory.MemoryID, memory.Summary))
	return renderDynamicContext("memory_summary", "memory:"+string(memory.MemoryID), "system_record", body)
}

func renderToolResultSummary(result contracts.ToolResultSummary) string {
	body := strings.TrimSpace(fmt.Sprintf("%s %s %s", result.ToolCallID, result.Status, result.Summary))
	return renderDynamicContext("tool_result", "tool_result:"+string(result.ToolCallID), "tool_result", body)
}

func renderArtifactRef(artifact contracts.ArtifactRef) string {
	sourceType := artifactMetadataString(artifact, "source_type")
	if sourceType == "" {
		sourceType = "artifact_refs"
	}
	sourceRef := artifactMetadataString(artifact, "source_ref")
	if sourceRef == "" && artifact.ArtifactID != "" {
		sourceRef = "artifact:" + string(artifact.ArtifactID)
	}
	trustLevel := artifactMetadataString(artifact, "trust_level")
	if trustLevel == "" {
		trustLevel = "tool_result"
	}
	parts := []string{
		contextMetadataLine(sourceType, sourceRef, trustLevel),
		fmt.Sprintf("%s %s", artifact.ArtifactID, artifact.Type),
	}
	if sourceType := artifactMetadataString(artifact, "source_type"); sourceType != "" {
		parts = append(parts, "source_type="+sourceType)
	}
	if sourceRef := artifactMetadataString(artifact, "source_ref"); sourceRef != "" {
		parts = append(parts, "source_ref="+sourceRef)
	}
	if providerID := artifactMetadataString(artifact, "provider_id"); providerID != "" {
		parts = append(parts, "provider_id="+providerID)
	}
	if hookID := artifactMetadataString(artifact, "hook_id"); hookID != "" {
		parts = append(parts, "hook_id="+hookID)
	}
	if toolCallID := artifactMetadataString(artifact, "tool_call_id"); toolCallID != "" {
		parts = append(parts, "tool_call_id="+toolCallID)
	}
	if trustLevel := artifactMetadataString(artifact, "trust_level"); trustLevel != "" {
		parts = append(parts, "trust_level="+trustLevel)
	}
	if artifact.Summary != "" {
		parts = append(parts, artifact.Summary)
	}
	return strings.Join(parts, " ")
}

func artifactMetadataString(artifact contracts.ArtifactRef, key string) string {
	if artifact.Metadata == nil {
		return ""
	}
	if value, ok := artifact.Metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
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
	sourceRef := firstNonEmptyString(message.MessageID, message.ExternalMessageID)
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
	body := strings.TrimSpace(prefix) + ": " + message.Text
	return renderDynamicContext("conversation_recent", "message:"+sourceRef, "untrusted_user_text", body)
}

func renderRetrievedContext(item contracts.RetrievedContext) string {
	createdAt := ""
	if !item.CreatedAt.IsZero() {
		createdAt = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	sourceRef := ""
	if strings.TrimSpace(item.SourceID) != "" {
		sourceRef = strings.TrimSpace(item.SourceType) + ":" + strings.TrimSpace(item.SourceID)
	}
	lines := []string{
		fmt.Sprintf("[%s relevance=%.2f recency=%.2f created_at=%s visibility=%s]", contextMetadataLine(item.SourceType, sourceRef, item.TrustLevel), item.Relevance, item.RecencyScore, createdAt, item.Visibility),
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

func contextMetadataLine(sourceType string, sourceRef string, trustLevel string) string {
	sourceType = strings.TrimSpace(sourceType)
	if sourceType == "" {
		sourceType = "context"
	}
	trustLevel = strings.TrimSpace(trustLevel)
	if trustLevel == "" {
		trustLevel = "untrusted_user_text"
	}
	parts := []string{
		"source_type=" + sourceType,
		"trust_level=" + trustLevel,
		"input_boundary=untrusted",
	}
	if sourceRef = safeSourceRef(sourceRef); sourceRef != "" {
		parts = append(parts, "source_ref="+sourceRef)
	}
	return strings.Join(parts, " ")
}

func safeSourceRef(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasSuffix(value, ":") {
		return ""
	}
	return strings.Join(strings.Fields(value), "_")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
