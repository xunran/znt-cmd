package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"znt/internal/asset/artifact"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	runrepo "znt/internal/runtime/run"
	taskruntime "znt/internal/task/runtime"
	toolrepo "znt/internal/tool/repository"
)

const maxToolResultSummaryLength = 1500

const (
	SourceConversationRecent    = "conversation_recent"
	SourceConversationRetrieval = "conversation_retrieval"
	SourceTaskHistory           = "task_history"
	SourceMemorySummary         = "memory_summary"
	SourceArtifactRefs          = "artifact_refs"
	SourceToolResults           = "tool_results"
	SourceRuntimeHookContext    = "runtime_hook_context"
	SourceAgentPluginContext    = "agent_plugin_context"
)

type Collector struct {
	Runs     runrepo.Repository
	Tasks    *taskruntime.Service
	Memory   artifact.MemoryStore
	ToolRepo toolrepo.Repository
}

type Input struct {
	TenantID        contracts.TenantID
	TaskID          contracts.TaskID
	RunID           contracts.AgentRunID
	AgentID         contracts.AgentID
	UserID          contracts.UserID
	ContextStrategy contracts.ContextStrategy
	MemoryStrategy  contracts.MemoryUseStrategy
}

type Result struct {
	TaskEvents   []contracts.TaskEvent
	TaskHistory  []contracts.RetrievedContext
	Memory       []contracts.MemorySummary
	ArtifactRefs []contracts.ArtifactRef
	ToolResults  []contracts.ToolResultSummary
}

func (c Collector) Collect(ctx context.Context, input Input) (Result, error) {
	events, err := c.TaskEventsForContext(ctx, input.TaskID, input.ContextStrategy)
	if err != nil {
		return Result{}, err
	}
	result := Result{TaskEvents: events}
	if SourceEnabled(input.ContextStrategy, SourceToolResults) {
		result.ToolResults = c.ToolSummaries(ctx, input.RunID, contracts.IntValue(input.ContextStrategy.ToolResultMaxItems))
	}
	if SourceEnabled(input.ContextStrategy, SourceTaskHistory) {
		result.TaskHistory = c.TaskHistory(ctx, input.TenantID, input.TaskID, input.RunID, events, contracts.IntValue(input.ContextStrategy.TaskHistoryMaxItems))
	}
	if SourceEnabled(input.ContextStrategy, SourceMemorySummary) && MemoryStrategyEnabled(input.MemoryStrategy.ReadEnabled) {
		result.Memory = c.MemorySummaries(ctx, input.TenantID, input.AgentID, input.UserID, MemoryReadLimit(input.ContextStrategy, input.MemoryStrategy), input.MemoryStrategy)
	}
	if SourceEnabled(input.ContextStrategy, SourceArtifactRefs) {
		result.ArtifactRefs = c.ArtifactRefs(ctx, input.RunID, contracts.IntValue(input.ContextStrategy.ArtifactRefMaxItems))
	}
	return result, nil
}

func (c Collector) TaskEventsForContext(ctx context.Context, taskID contracts.TaskID, strategy contracts.ContextStrategy) ([]contracts.TaskEvent, error) {
	if c.Tasks == nil || taskID == "" {
		return nil, nil
	}
	limit, enabled := TaskEventsReadLimit(strategy)
	if !enabled {
		return nil, nil
	}
	if limit > 0 {
		return c.Tasks.EventsLimit(ctx, taskID, limit)
	}
	return c.Tasks.Events(ctx, taskID)
}

func TaskEventsReadLimit(strategy contracts.ContextStrategy) (int, bool) {
	enabled := false
	unlimited := false
	limit := 0
	consider := func(source string, configured *int) {
		if !SourceEnabled(strategy, source) {
			return
		}
		enabled = true
		if configured == nil || *configured <= 0 {
			unlimited = true
			return
		}
		if *configured > limit {
			limit = *configured
		}
	}
	consider(SourceTaskHistory, strategy.TaskHistoryMaxItems)
	consider(SourceConversationRetrieval, strategy.RetrievalMaxResults)
	if !enabled {
		return 0, false
	}
	if unlimited {
		return 0, true
	}
	return limit, true
}

func SourceEnabled(strategy contracts.ContextStrategy, source string) bool {
	if len(strategy.EnabledSources) == 0 {
		return true
	}
	for _, item := range strategy.EnabledSources {
		if strings.TrimSpace(item) == source {
			return true
		}
	}
	return false
}

func MemoryStrategyEnabled(value *bool) bool {
	return value == nil || *value
}

func MemoryReadLimit(contextStrategy contracts.ContextStrategy, memoryStrategy contracts.MemoryUseStrategy) int {
	contextLimit := contracts.IntValue(contextStrategy.MemoryMaxItems)
	memoryLimit := contracts.IntValue(memoryStrategy.MaxMemoryItems)
	if contextLimit <= 0 {
		return memoryLimit
	}
	if memoryLimit <= 0 || contextLimit < memoryLimit {
		return contextLimit
	}
	return memoryLimit
}

func (c Collector) ToolSummaries(ctx context.Context, runID contracts.AgentRunID, limit int) []contracts.ToolResultSummary {
	if c.ToolRepo == nil {
		return nil
	}
	results, err := c.ToolRepo.ListResultsByRunLimit(ctx, runID, limit)
	if err != nil {
		return nil
	}
	out := make([]contracts.ToolResultSummary, 0, len(results))
	for _, result := range results {
		out = append(out, contracts.ToolResultSummary{
			ToolCallID: result.ToolCallID,
			Status:     result.Status,
			Summary:    summarizeToolResult(result),
		})
	}
	return out
}

func (c Collector) TaskHistory(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, currentRunID contracts.AgentRunID, events []contracts.TaskEvent, limit int) []contracts.RetrievedContext {
	if taskID == "" {
		return nil
	}
	out := make([]contracts.RetrievedContext, 0)
	for _, event := range events {
		if event.Type != "conversation.input" && event.Type != "run.resumed_input" {
			continue
		}
		if eventRunID(event) == string(currentRunID) {
			continue
		}
		input, _ := event.Payload["input"].(string)
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		out = append(out, contracts.RetrievedContext{
			SourceType: "task_event",
			SourceID:   string(event.EventID),
			SpeakerID:  event.ActorID,
			CreatedAt:  event.CreatedAt,
			Summary:    "previous task input",
			Snippet:    input,
			TrustLevel: "untrusted_user_text",
			Visibility: "task",
		})
	}
	if c.Runs != nil {
		if runs, err := c.Runs.List(ctx, runrepo.ListFilter{TenantID: tenantID, TaskID: taskID, Limit: limit}); err == nil {
			for _, run := range runs {
				if run.RunID == currentRunID {
					continue
				}
				summary := fmt.Sprintf("run_id=%s status=%s", run.RunID, run.Status)
				trustLevel := "system_record"
				if strings.TrimSpace(run.Input) != "" {
					summary += " input=" + run.Input
					trustLevel = "untrusted_user_text"
				}
				out = append(out, contracts.RetrievedContext{
					SourceType: "previous_run",
					SourceID:   string(run.RunID),
					CreatedAt:  run.StartedAt,
					Summary:    summary,
					TrustLevel: trustLevel,
					Visibility: "task",
				})
			}
		}
	}
	if c.ToolRepo != nil {
		if results, err := c.ToolRepo.ListResultsByTaskLimit(ctx, tenantID, taskID, limit); err == nil {
			for _, result := range results {
				call, ok, err := c.ToolRepo.GetCall(ctx, result.ToolCallID)
				if err != nil || !ok {
					continue
				}
				if call.RunID == currentRunID {
					continue
				}
				toolName := contextconversation.FirstNonEmpty(call.ToolID, call.Name)
				summary := fmt.Sprintf("tool=%s status=%s %s", toolName, result.Status, summarizeToolResult(result))
				out = append(out, contracts.RetrievedContext{
					SourceType: "tool_result",
					SourceID:   string(result.ToolResultID),
					CreatedAt:  result.CompletedAt,
					Summary:    strings.TrimSpace(summary),
					TrustLevel: "tool_result",
					Visibility: "task",
				})
				for _, ref := range result.ArtifactRefs {
					if ref.ArtifactID == "" {
						continue
					}
					out = append(out, contracts.RetrievedContext{
						SourceType: "artifact",
						SourceID:   string(ref.ArtifactID),
						CreatedAt:  result.CompletedAt,
						Summary:    fmt.Sprintf("%s %s", ref.Type, ref.Summary),
						TrustLevel: "tool_result",
						Visibility: "task",
					})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.IsZero() || out[j].CreatedAt.IsZero() {
			return i < j
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (c Collector) ArtifactRefs(ctx context.Context, runID contracts.AgentRunID, limit int) []contracts.ArtifactRef {
	if c.ToolRepo == nil {
		return nil
	}
	refs, err := c.ToolRepo.ListArtifactRefsByRunLimit(ctx, runID, limit)
	if err != nil {
		return nil
	}
	return refs
}

func (c Collector) MemorySummaries(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID, limit int, strategy contracts.MemoryUseStrategy) []contracts.MemorySummary {
	if c.Memory == nil || tenantID == "" {
		return nil
	}
	memories, err := c.Memory.ListMemoryLimit(ctx, tenantID, agentID, userID, strategy.ReadScopes, limit)
	if err != nil {
		return nil
	}
	return memories
}

func summarizeToolResult(result contracts.ToolResult) string {
	if result.Error != nil {
		return result.Error.Message
	}
	if len(result.Output) > 0 {
		if summary := summarizeToolOutput(result.Output); summary != "" {
			return summary
		}
		return "tool output available"
	}
	if len(result.ArtifactRefs) > 0 {
		return "artifact refs available"
	}
	return ""
}

func summarizeToolOutput(output map[string]any) string {
	parts := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(key string, value any) {
		text := strings.TrimSpace(toolOutputText(value))
		if text == "" {
			return
		}
		line := key + "=" + text
		if _, ok := seen[line]; ok {
			return
		}
		seen[line] = struct{}{}
		parts = append(parts, line)
	}

	for _, key := range []string{"final", "should_continue", "next_action"} {
		if value, ok := output[key]; ok {
			add(key, value)
		}
	}
	for _, key := range []string{"reply", "response", "answer", "text", "content", "summary", "message"} {
		if value, ok := output[key]; ok {
			add(key, value)
		}
	}
	if decision, ok := output["final_decision"]; ok {
		if text := nestedToolOutputText(decision, "reply", "text"); text != "" {
			add("final_decision.reply.text", text)
		}
		add("final_decision", decision)
	}
	if decision, ok := output["decision_json"]; ok {
		add("decision_json", decision)
	}
	if instruction, ok := output["decision_instruction"]; ok {
		add("decision_instruction", instruction)
	}
	if len(parts) > 0 {
		return truncateToolResultSummary(strings.Join(parts, "; "))
	}
	if raw, err := json.Marshal(output); err == nil {
		return truncateToolResultSummary(string(raw))
	}
	return ""
}

func nestedToolOutputText(value any, path ...string) string {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return toolOutputText(current)
}

func toolOutputText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(typed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return strings.TrimSpace(string(raw))
	}
}

func truncateToolResultSummary(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxToolResultSummaryLength {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxToolResultSummaryLength]) + "...(truncated)"
}

func eventRunID(event contracts.TaskEvent) string {
	if event.Payload == nil {
		return ""
	}
	switch value := event.Payload["run_id"].(type) {
	case contracts.AgentRunID:
		return string(value)
	case string:
		return value
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}
