package collector

import (
	"strings"

	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
)

func ApplyContextSourcePolicy(view *contracts.WorkView, strategy contracts.ContextStrategy, memory contracts.MemoryUseStrategy) []contracts.ContextSourceReport {
	reports := make([]contracts.ContextSourceReport, 0, 8)
	if view.ConversationContext != nil {
		candidates := len(view.ConversationContext.RecentMessages)
		if !SourceEnabled(strategy, SourceConversationRecent) {
			view.ConversationContext.RecentMessages = nil
			reports = append(reports, NewContextSourceReport(SourceConversationRecent, candidates, 0, contracts.IntValue(strategy.RecentMessageLimit), "disabled_by_context_strategy"))
		} else {
			selected, dropped, reason := applyConversationMessageBudget(&view.ConversationContext.RecentMessages, SourceBudget(strategy, SourceConversationRecent))
			reports = append(reports, NewContextSourceReport(SourceConversationRecent, candidates, selected, SourceReportLimit(strategy, SourceConversationRecent, contracts.IntValue(strategy.RecentMessageLimit)), reason))
			if dropped > 0 && reason == "" {
				reports[len(reports)-1].Reason = "limited_by_context_strategy"
			}
		}
		candidates = len(view.ConversationContext.Retrieved)
		if !SourceEnabled(strategy, SourceConversationRetrieval) {
			view.ConversationContext.Retrieved = nil
			reports = append(reports, NewContextSourceReport(SourceConversationRetrieval, candidates, 0, contracts.IntValue(strategy.RetrievalMaxResults), "disabled_by_context_strategy"))
		} else {
			selected, dropped, reason := applyRetrievedContextBudget(&view.ConversationContext.Retrieved, SourceBudget(strategy, SourceConversationRetrieval))
			reports = append(reports, NewContextSourceReport(SourceConversationRetrieval, candidates, selected, SourceReportLimit(strategy, SourceConversationRetrieval, contracts.IntValue(strategy.RetrievalMaxResults)), reason))
			if dropped > 0 && reason == "" {
				reports[len(reports)-1].Reason = "limited_by_context_strategy"
			}
		}
	}
	candidates := len(view.TaskHistory)
	if !SourceEnabled(strategy, SourceTaskHistory) {
		view.TaskHistory = nil
		reports = append(reports, NewContextSourceReport(SourceTaskHistory, candidates, 0, contracts.IntValue(strategy.TaskHistoryMaxItems), "disabled_by_context_strategy"))
	} else {
		selected, dropped, reason := applyRetrievedContextBudget(&view.TaskHistory, SourceBudget(strategy, SourceTaskHistory))
		reports = append(reports, NewContextSourceReport(SourceTaskHistory, candidates, selected, SourceReportLimit(strategy, SourceTaskHistory, contracts.IntValue(strategy.TaskHistoryMaxItems)), reason))
		if dropped > 0 && reason == "" {
			reports[len(reports)-1].Reason = "limited_by_context_strategy"
		}
	}
	candidates = len(view.MemorySummaries)
	if !MemoryStrategyEnabled(memory.ReadEnabled) {
		view.MemorySummaries = nil
		reports = append(reports, NewContextSourceReport(SourceMemorySummary, candidates, 0, MemoryReadLimit(strategy, memory), "disabled_by_memory_strategy"))
	} else if !SourceEnabled(strategy, SourceMemorySummary) {
		view.MemorySummaries = nil
		reports = append(reports, NewContextSourceReport(SourceMemorySummary, candidates, 0, MemoryReadLimit(strategy, memory), "disabled_by_context_strategy"))
	} else {
		selected, dropped, reason := applyMemorySummaryBudget(&view.MemorySummaries, SourceBudget(strategy, SourceMemorySummary))
		reports = append(reports, NewContextSourceReport(SourceMemorySummary, candidates, selected, SourceReportLimit(strategy, SourceMemorySummary, MemoryReadLimit(strategy, memory)), reason))
		if dropped > 0 && reason == "" {
			reports[len(reports)-1].Reason = "limited_by_context_strategy"
		}
	}
	candidates = len(view.ToolResultSummaries)
	if !SourceEnabled(strategy, SourceToolResults) {
		view.ToolResultSummaries = nil
		reports = append(reports, NewContextSourceReport(SourceToolResults, candidates, 0, contracts.IntValue(strategy.ToolResultMaxItems), "disabled_by_context_strategy"))
	} else {
		selected, dropped, reason := applyToolResultBudget(&view.ToolResultSummaries, SourceBudget(strategy, SourceToolResults))
		reports = append(reports, NewContextSourceReport(SourceToolResults, candidates, selected, SourceReportLimit(strategy, SourceToolResults, contracts.IntValue(strategy.ToolResultMaxItems)), reason))
		if dropped > 0 && reason == "" {
			reports[len(reports)-1].Reason = "limited_by_context_strategy"
		}
	}
	if len(view.ArtifactRefs) > 0 {
		artifactReports, selected := ApplyArtifactRefSourcePolicy(view.ArtifactRefs, strategy)
		reports = append(reports, artifactReports...)
		view.ArtifactRefs = selected
	}
	return NonEmptyContextSourceReports(reports)
}

func ContextAssemblyReport(strategyHash string, strategy contracts.ContextStrategy, sourceReports []contracts.ContextSourceReport) *contracts.ContextAssemblyReport {
	return &contracts.ContextAssemblyReport{
		StrategyHash: strategyHash,
		Mode:         strategy.Mode,
		TokenBudget:  contracts.IntValue(strategy.ContextTokenBudget),
		Sources:      sourceReports,
	}
}

func ExternalContextSourceReports(sources []contracts.ContextSourceReport) []contracts.ContextSourceReport {
	out := make([]contracts.ContextSourceReport, 0)
	for _, source := range sources {
		if IsExternalContextSource(source.SourceType) {
			out = append(out, source)
		}
	}
	return out
}

func IsExternalContextSource(sourceType string) bool {
	return sourceType == SourceRuntimeHookContext || sourceType == SourceAgentPluginContext
}

func NewContextSourceReport(source string, candidates int, selected int, limit int, reason string) contracts.ContextSourceReport {
	if selected > candidates {
		selected = candidates
	}
	return contracts.ContextSourceReport{
		SourceType:     source,
		TrustLevel:     ContextSourceTrustLevel(source),
		CandidateCount: candidates,
		SelectedCount:  selected,
		DroppedCount:   candidates - selected,
		Limit:          limit,
		Reason:         reason,
	}
}

func ContextSourceTrustLevel(source string) string {
	switch source {
	case SourceConversationRecent, SourceConversationRetrieval, SourceTaskHistory:
		return "untrusted_user_text"
	case SourceMemorySummary:
		return "system_record"
	case SourceArtifactRefs, SourceToolResults:
		return "tool_result"
	default:
		return ""
	}
}

func NonEmptyContextSourceReports(reports []contracts.ContextSourceReport) []contracts.ContextSourceReport {
	out := make([]contracts.ContextSourceReport, 0, len(reports))
	for _, report := range reports {
		if report.CandidateCount == 0 && report.SelectedCount == 0 && report.DroppedCount == 0 {
			continue
		}
		out = append(out, report)
	}
	return out
}

func SourceBudget(strategy contracts.ContextStrategy, source string) (int, bool) {
	if strategy.SourceBudgets == nil {
		return 0, false
	}
	budget, ok := strategy.SourceBudgets[source]
	return budget, ok
}

func SourceReportLimit(strategy contracts.ContextStrategy, source string, fallback int) int {
	if budget, ok := SourceBudget(strategy, source); ok && budget > 0 {
		return budget
	}
	return fallback
}

func ApplyArtifactRefSourcePolicy(refs []contracts.ArtifactRef, strategy contracts.ContextStrategy) ([]contracts.ContextSourceReport, []contracts.ArtifactRef) {
	type sourceState struct {
		report contracts.ContextSourceReport
	}
	order := make([]string, 0, 3)
	states := map[string]*sourceState{}
	usedBySource := map[string]int{}
	stateFor := func(ref contracts.ArtifactRef) *sourceState {
		report := artifactRefSourceReport(ref, strategy)
		key := strings.Join([]string{report.SourceType, report.SourceRef, report.ProviderID, report.HookID, report.ToolCallID, report.TrustLevel}, "\x00")
		state, ok := states[key]
		if ok {
			return state
		}
		state = &sourceState{report: report}
		states[key] = state
		order = append(order, key)
		return state
	}
	selectedRefs := make([]contracts.ArtifactRef, 0, len(refs))
	for _, ref := range refs {
		source := artifactRefContextSource(ref)
		state := stateFor(ref)
		state.report.CandidateCount++
		if !SourceEnabled(strategy, source) {
			state.report.Reason = "disabled_by_context_strategy"
			continue
		}
		budget, hasBudget := SourceBudget(strategy, source)
		if hasBudget && budget > 0 {
			cost := ApproximateTokens(strings.Join([]string{string(ref.ArtifactID), ref.Type, ref.URI, ref.Summary}, " "))
			if usedBySource[source]+cost > budget {
				state.report.Reason = "source_budget_exceeded"
				continue
			}
			usedBySource[source] += cost
		}
		state.report.SelectedCount++
		selectedRefs = append(selectedRefs, ref)
	}
	reports := make([]contracts.ContextSourceReport, 0, len(order))
	for _, key := range order {
		state := states[key]
		state.report.DroppedCount = state.report.CandidateCount - state.report.SelectedCount
		reports = append(reports, state.report)
	}
	return reports, selectedRefs
}

func ApproximateTokens(value string) int {
	return len(strings.Fields(value))
}

func MergeContextSourceReport(report *contracts.ContextAssemblyReport, source string, candidates int, selected int, limit int, reason string) {
	if report == nil || candidates == 0 {
		return
	}
	MergeContextSourceReportRow(report, contracts.ContextSourceReport{
		SourceType:     source,
		CandidateCount: candidates,
		SelectedCount:  selected,
		DroppedCount:   candidates - selected,
		Limit:          limit,
		Reason:         reason,
	})
}

func MergeContextSourceReportRow(report *contracts.ContextAssemblyReport, row contracts.ContextSourceReport) {
	if report == nil || row.CandidateCount == 0 {
		return
	}
	if row.SelectedCount > row.CandidateCount {
		row.SelectedCount = row.CandidateCount
	}
	row.DroppedCount = row.CandidateCount - row.SelectedCount
	for i := range report.Sources {
		if ContextSourceReportSameRef(report.Sources[i], row) {
			report.Sources[i].CandidateCount += row.CandidateCount
			report.Sources[i].SelectedCount += row.SelectedCount
			report.Sources[i].DroppedCount += row.DroppedCount
			if report.Sources[i].Limit == 0 {
				report.Sources[i].Limit = row.Limit
			}
			if report.Sources[i].Reason == "" {
				report.Sources[i].Reason = row.Reason
			}
			return
		}
	}
	report.Sources = append(report.Sources, row)
}

func ContextSourceReportSameRef(left contracts.ContextSourceReport, right contracts.ContextSourceReport) bool {
	return left.SourceType == right.SourceType &&
		left.SourceRef == right.SourceRef &&
		left.ProviderID == right.ProviderID &&
		left.HookID == right.HookID &&
		left.ToolCallID == right.ToolCallID &&
		left.TrustLevel == right.TrustLevel
}

func FilterRuntimeHookContextBlocks(blocks []runtimehook.ContextBlock, strategy contracts.ContextStrategy) ([]runtimehook.ContextBlock, []contracts.ContextSourceReport) {
	type sourceState struct {
		report contracts.ContextSourceReport
	}
	out := make([]runtimehook.ContextBlock, 0, len(blocks))
	order := make([]string, 0, len(blocks))
	states := map[string]*sourceState{}
	usedBySource := map[string]int{}
	stateFor := func(block runtimehook.ContextBlock) *sourceState {
		report := ContextBlockSourceReport(block, strategy)
		key := strings.Join([]string{report.SourceType, report.SourceRef, report.ProviderID, report.HookID, report.ToolCallID, report.TrustLevel}, "\x00")
		if state, ok := states[key]; ok {
			return state
		}
		state := &sourceState{report: report}
		states[key] = state
		order = append(order, key)
		return state
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		source := ContextBlockSourceType(block)
		state := stateFor(block)
		state.report.CandidateCount++
		if !SourceEnabled(strategy, source) {
			state.report.Reason = "disabled_by_context_strategy"
			continue
		}
		budget, hasBudget := SourceBudget(strategy, source)
		if hasBudget && budget > 0 {
			cost := ApproximateTokens(strings.Join([]string{block.ID, block.Title, block.Content}, " "))
			if usedBySource[source]+cost > budget {
				state.report.Reason = "source_budget_exceeded"
				continue
			}
			usedBySource[source] += cost
		}
		state.report.SelectedCount++
		out = append(out, block)
	}
	reports := make([]contracts.ContextSourceReport, 0, len(order))
	for _, key := range order {
		state := states[key]
		state.report.DroppedCount = state.report.CandidateCount - state.report.SelectedCount
		reports = append(reports, state.report)
	}
	return out, reports
}

func ContextBlockSourceReport(block runtimehook.ContextBlock, strategy contracts.ContextStrategy) contracts.ContextSourceReport {
	source := ContextBlockSourceType(block)
	return contracts.ContextSourceReport{
		SourceType: source,
		SourceRef:  ContextBlockMetadataString(block, "source_ref"),
		ProviderID: ContextBlockMetadataString(block, "provider_id"),
		HookID:     ContextBlockMetadataString(block, "hook_id"),
		ToolCallID: ContextBlockMetadataString(block, "tool_call_id"),
		TrustLevel: ContextBlockTrustLevel(block),
		Limit:      SourceReportLimit(strategy, source, 0),
	}
}

func ContextBlockSourceType(block runtimehook.ContextBlock) string {
	sourceType := ContextBlockMetadataString(block, "source_type")
	switch sourceType {
	case SourceAgentPluginContext:
		return SourceAgentPluginContext
	default:
		return SourceRuntimeHookContext
	}
}

func ContextBlockTrustLevel(block runtimehook.ContextBlock) string {
	trustLevel := ContextBlockMetadataString(block, "trust_level")
	if trustLevel == "" {
		return "untrusted_external_context"
	}
	return trustLevel
}

func ContextBlockSourceRef(block runtimehook.ContextBlock) string {
	if sourceRef := ContextBlockMetadataString(block, "source_ref"); sourceRef != "" {
		return sourceRef
	}
	if strings.TrimSpace(block.ID) != "" {
		return strings.TrimSpace(block.ID)
	}
	return ContextBlockSourceType(block)
}

func ContextBlockMetadataString(block runtimehook.ContextBlock, key string) string {
	if block.Metadata == nil {
		return ""
	}
	if value, ok := block.Metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func ContextBlockArtifactMetadata(block runtimehook.ContextBlock) map[string]any {
	metadata := make(map[string]any, len(block.Metadata)+6)
	for key, value := range block.Metadata {
		metadata[key] = value
	}
	metadata["source_type"] = ContextBlockSourceType(block)
	metadata["source_ref"] = ContextBlockSourceRef(block)
	metadata["trust_level"] = ContextBlockTrustLevel(block)
	if providerID := ContextBlockMetadataString(block, "provider_id"); providerID != "" {
		metadata["provider_id"] = providerID
	}
	if hookID := ContextBlockMetadataString(block, "hook_id"); hookID != "" {
		metadata["hook_id"] = hookID
	}
	if toolCallID := ContextBlockMetadataString(block, "tool_call_id"); toolCallID != "" {
		metadata["tool_call_id"] = toolCallID
	}
	return metadata
}

func artifactRefSourceReport(ref contracts.ArtifactRef, strategy contracts.ContextStrategy) contracts.ContextSourceReport {
	source := artifactRefContextSource(ref)
	limit := 0
	if source == SourceArtifactRefs {
		limit = contracts.IntValue(strategy.ArtifactRefMaxItems)
	}
	report := contracts.ContextSourceReport{
		SourceType: source,
		Limit:      SourceReportLimit(strategy, source, limit),
	}
	if source == SourceRuntimeHookContext || source == SourceAgentPluginContext {
		report.SourceRef = artifactRefMetadataString(ref, "source_ref")
		if report.SourceRef == "" {
			report.SourceRef = string(ref.ArtifactID)
		}
		report.ProviderID = artifactRefMetadataString(ref, "provider_id")
		report.HookID = artifactRefMetadataString(ref, "hook_id")
		report.ToolCallID = artifactRefMetadataString(ref, "tool_call_id")
		report.TrustLevel = artifactRefMetadataString(ref, "trust_level")
		if report.TrustLevel == "" {
			report.TrustLevel = "untrusted_external_context"
		}
	}
	return report
}

func artifactRefMetadataString(ref contracts.ArtifactRef, key string) string {
	if ref.Metadata == nil {
		return ""
	}
	if value, ok := ref.Metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func artifactRefContextSource(ref contracts.ArtifactRef) string {
	switch ref.Type {
	case SourceRuntimeHookContext:
		return SourceRuntimeHookContext
	case SourceAgentPluginContext:
		return SourceAgentPluginContext
	default:
		return SourceArtifactRefs
	}
}

func applyConversationMessageBudget(items *[]contracts.ConversationMessage, budget int, hasBudget bool) (int, int, string) {
	if !hasBudget || budget <= 0 {
		return len(*items), 0, ""
	}
	selected := make([]contracts.ConversationMessage, 0, len(*items))
	used := 0
	for _, item := range *items {
		cost := ApproximateTokens(item.Text)
		if used+cost > budget {
			break
		}
		selected = append(selected, item)
		used += cost
	}
	dropped := len(*items) - len(selected)
	*items = selected
	if dropped > 0 {
		return len(selected), dropped, "source_budget_exceeded"
	}
	return len(selected), 0, ""
}

func applyRetrievedContextBudget(items *[]contracts.RetrievedContext, budget int, hasBudget bool) (int, int, string) {
	if !hasBudget || budget <= 0 {
		return len(*items), 0, ""
	}
	selected := make([]contracts.RetrievedContext, 0, len(*items))
	used := 0
	for _, item := range *items {
		cost := ApproximateTokens(strings.Join([]string{item.Summary, item.Snippet}, " "))
		if used+cost > budget {
			break
		}
		selected = append(selected, item)
		used += cost
	}
	dropped := len(*items) - len(selected)
	*items = selected
	if dropped > 0 {
		return len(selected), dropped, "source_budget_exceeded"
	}
	return len(selected), 0, ""
}

func applyMemorySummaryBudget(items *[]contracts.MemorySummary, budget int, hasBudget bool) (int, int, string) {
	if !hasBudget || budget <= 0 {
		return len(*items), 0, ""
	}
	selected := make([]contracts.MemorySummary, 0, len(*items))
	used := 0
	for _, item := range *items {
		cost := ApproximateTokens(item.Summary)
		if used+cost > budget {
			break
		}
		selected = append(selected, item)
		used += cost
	}
	dropped := len(*items) - len(selected)
	*items = selected
	if dropped > 0 {
		return len(selected), dropped, "source_budget_exceeded"
	}
	return len(selected), 0, ""
}

func applyToolResultBudget(items *[]contracts.ToolResultSummary, budget int, hasBudget bool) (int, int, string) {
	if !hasBudget || budget <= 0 {
		return len(*items), 0, ""
	}
	selected := make([]contracts.ToolResultSummary, 0, len(*items))
	used := 0
	for _, item := range *items {
		cost := ApproximateTokens(item.Summary)
		if used+cost > budget {
			break
		}
		selected = append(selected, item)
		used += cost
	}
	dropped := len(*items) - len(selected)
	*items = selected
	if dropped > 0 {
		return len(selected), dropped, "source_budget_exceeded"
	}
	return len(selected), 0, ""
}
