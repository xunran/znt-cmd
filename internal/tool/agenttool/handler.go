package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/agentdelegation"
	"znt/internal/contracts"
	"znt/internal/governance/trace"
	"znt/internal/tool/catalog"
	"znt/pkg/idgen"
)

type RunResult struct {
	RunID        contracts.AgentRunID
	TaskID       contracts.TaskID
	Status       contracts.RunStatus
	Reply        *contracts.DecisionReply
	Ask          *contracts.ClarificationRequest
	ArtifactRefs []contracts.ArtifactRef
	Error        *contracts.RuntimeError
}

type Handler struct {
	Agents        loader.Loader
	AgentRunnable func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error
	StartAgentRun func(ctx context.Context, envelope contracts.AgentEnvelope) (RunResult, error)
	Trace         trace.Recorder
	Delegations   agentdelegation.Repository
	Now           func() time.Time
}

func (h Handler) ExecuteAgentTool(ctx context.Context, call contracts.ToolCall, manifest catalog.ToolManifest) (map[string]any, []contracts.ArtifactRef, error) {
	providerAgentID := contracts.AgentID(manifest.Executor.ProviderID)
	if providerAgentID == "" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "agent_tool provider_id is required", nil)
	}
	if h.Agents == nil {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "agent loader is not configured", nil)
	}
	if h.AgentRunnable != nil {
		if err := h.AgentRunnable(ctx, call.TenantID, providerAgentID); err != nil {
			return nil, nil, err
		}
	}
	provider, err := h.Agents.Load(ctx, call.TenantID, providerAgentID, "")
	if err != nil {
		return nil, nil, err
	}
	var exported *contracts.AgentExportedTool
	for i := range provider.Exports.Tools {
		tool := &provider.Exports.Tools[i]
		if tool.ToolID == manifest.ToolID || tool.Operation == manifest.Executor.Operation {
			exported = tool
			break
		}
	}
	if exported == nil || exported.Status == "disabled" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "agent exported tool is not enabled", map[string]any{"tool_id": manifest.ToolID, "provider_agent_id": providerAgentID})
	}
	if strings.TrimSpace(exported.Status) != "" && exported.Status != "enabled" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent exported tool is not enabled", map[string]any{"tool_id": manifest.ToolID, "status": exported.Status})
	}
	operation := strings.TrimSpace(manifest.Executor.Operation)
	if operation == "" {
		operation = strings.TrimSpace(exported.Operation)
	}
	if operation == "" {
		operation = exported.ToolID
	}
	startedAt := now(h.Now)
	delegationID := idgen.New("delegation")
	delegation := agentdelegation.Delegation{
		DelegationID:    delegationID,
		TenantID:        call.TenantID,
		TraceID:         contracts.TraceID(traceID(call)),
		ParentRunID:     call.RunID,
		ParentTaskID:    call.TaskID,
		ToolCallID:      call.ToolCallID,
		ToolID:          manifest.ToolID,
		Operation:       operation,
		ProviderAgentID: providerAgentID,
		Status:          agentdelegation.StatusInvoked,
		StartedAt:       &startedAt,
		CreatedAt:       startedAt,
		UpdatedAt:       startedAt,
	}
	_ = h.recordDelegation(ctx, delegation)
	_ = h.record(ctx, call, "agent_tool.invoked", map[string]any{
		"delegation_id":       delegationID,
		"trace_id":            traceID(call),
		"parent_run_id":       call.RunID,
		"parent_task_id":      call.TaskID,
		"source_tool_call_id": call.ToolCallID,
		"provider_agent_id":   providerAgentID,
		"operation":           operation,
		"tool_id":             manifest.ToolID,
	})
	if h.StartAgentRun == nil {
		err := contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "agent_tool runner is not configured", map[string]any{"tool_id": call.ToolID, "provider_agent_id": providerAgentID})
		completedAt := now(h.Now)
		delegation.Status = agentdelegation.StatusFailed
		delegation.ErrorSummary = sanitizeToolAgentErrorSummary(err)
		delegation.CompletedAt = &completedAt
		delegation.UpdatedAt = completedAt
		_ = h.recordDelegation(ctx, delegation)
		_ = h.record(ctx, call, "agent_tool.failed", map[string]any{
			"delegation_id":       delegationID,
			"trace_id":            traceID(call),
			"parent_run_id":       call.RunID,
			"parent_task_id":      call.TaskID,
			"source_tool_call_id": call.ToolCallID,
			"provider_agent_id":   providerAgentID,
			"operation":           operation,
			"tool_id":             manifest.ToolID,
			"error":               err.Error(),
		})
		return nil, nil, err
	}
	providerInput := agentToolInput(*exported, operation, call.Arguments)
	runtimeContext := contracts.RuntimeContext{
		TenantID:  call.TenantID,
		RequestID: call.IdempotencyKey,
	}
	if parentConversation := parentRuntimeConversation(call.RuntimeContext); parentConversation != nil {
		runtimeContext.Conversation = conversationForProviderAgentRun(parentConversation, providerInput, call.ToolID, providerAgentID)
		if runtimeContext.Conversation.ExternalRefs == nil {
			runtimeContext.Conversation.ExternalRefs = map[string]string{}
		}
		runtimeContext.Conversation.ExternalRefs["chat_allowed_tool_ids"] = "parent_context.read"
	}
	result, err := h.StartAgentRun(ctx, contracts.AgentEnvelope{
		EnvelopeID: idgen.New("envelope"),
		TraceID:    contracts.TraceID(traceID(call)),
		Target: contracts.AgentTarget{
			AgentID: providerAgentID,
		},
		Caller: contracts.AgentCaller{
			CallerID:   call.ToolID,
			CallerType: "agent_tool",
			TenantID:   call.TenantID,
		},
		Command: "agent.run",
		Payload: map[string]any{
			"input":               providerInput,
			"tool_id":             manifest.ToolID,
			"operation":           operation,
			"arguments":           call.Arguments,
			"source_tool_call_id": string(call.ToolCallID),
			"source_run_id":       string(call.RunID),
			"source_task_id":      string(call.TaskID),
		},
		Context:   runtimeContext,
		CreatedAt: now(h.Now),
	})
	if err != nil {
		completedAt := now(h.Now)
		delegation.Status = agentdelegation.StatusFailed
		delegation.ErrorSummary = sanitizeToolAgentErrorSummary(err)
		delegation.CompletedAt = &completedAt
		delegation.UpdatedAt = completedAt
		_ = h.recordDelegation(ctx, delegation)
		_ = h.record(ctx, call, "agent_tool.failed", map[string]any{
			"delegation_id":       delegationID,
			"trace_id":            traceID(call),
			"parent_run_id":       call.RunID,
			"parent_task_id":      call.TaskID,
			"source_tool_call_id": call.ToolCallID,
			"provider_agent_id":   providerAgentID,
			"operation":           operation,
			"tool_id":             manifest.ToolID,
			"error":               err.Error(),
		})
		return nil, result.ArtifactRefs, err
	}
	if result.Error != nil {
		completedAt := now(h.Now)
		delegation.Status = agentdelegation.StatusFailed
		delegation.ChildRunID = result.RunID
		delegation.ChildTaskID = result.TaskID
		delegation.ResultStatus = string(result.Status)
		delegation.ErrorSummary = sanitizeToolAgentErrorSummary(result.Error)
		delegation.CompletedAt = &completedAt
		delegation.UpdatedAt = completedAt
		_ = h.recordDelegation(ctx, delegation)
		_ = h.record(ctx, call, "agent_tool.failed", map[string]any{
			"delegation_id":       delegationID,
			"trace_id":            traceID(call),
			"parent_run_id":       call.RunID,
			"parent_task_id":      call.TaskID,
			"source_tool_call_id": call.ToolCallID,
			"provider_agent_id":   providerAgentID,
			"operation":           operation,
			"tool_id":             manifest.ToolID,
			"run_id":              result.RunID,
			"task_id":             result.TaskID,
			"error":               result.Error.ToTracePayload(),
		})
		return nil, result.ArtifactRefs, result.Error
	}
	toolAgentResult := buildToolAgentResult(manifest.ToolID, providerAgentID, operation, result)
	output := map[string]any{
		"provider_agent_id": providerAgentID,
		"operation":         operation,
		"run_id":            result.RunID,
		"task_id":           result.TaskID,
		"status":            result.Status,
		"tool_agent_result": toolAgentResult,
	}
	if text := stringValue(toolAgentResult, "result_summary"); text != "" {
		output["reply_text"] = text
	}
	if result.Ask != nil {
		output["ask"] = result.Ask
	}
	completedAt := now(h.Now)
	delegation.Status = agentdelegation.StatusCompleted
	delegation.ChildRunID = result.RunID
	delegation.ChildTaskID = result.TaskID
	delegation.ResultStatus = stringValue(toolAgentResult, "status")
	delegation.ResultSummary = stringValue(toolAgentResult, "result_summary")
	delegation.CompletedAt = &completedAt
	delegation.UpdatedAt = completedAt
	_ = h.recordDelegation(ctx, delegation)
	_ = h.record(ctx, call, "agent_tool.completed", map[string]any{
		"delegation_id":       delegationID,
		"trace_id":            traceID(call),
		"parent_run_id":       call.RunID,
		"parent_task_id":      call.TaskID,
		"source_tool_call_id": call.ToolCallID,
		"provider_agent_id":   providerAgentID,
		"operation":           operation,
		"tool_id":             manifest.ToolID,
		"run_id":              result.RunID,
		"task_id":             result.TaskID,
		"status":              result.Status,
	})
	return output, result.ArtifactRefs, nil
}

func (h Handler) recordDelegation(ctx context.Context, delegation agentdelegation.Delegation) error {
	if h.Delegations == nil {
		return nil
	}
	return h.Delegations.Upsert(ctx, delegation)
}

func buildToolAgentResult(toolID string, providerAgentID contracts.AgentID, operation string, result RunResult) map[string]any {
	status := "ok"
	missingContext := false
	riskFlags := []string{"requires_main_agent_review"}
	summary := ""
	businessResult := map[string]any{}
	review := map[string]any{
		"permission_review": map[string]any{
			"decision": "requires_main_agent_review",
			"reason":   "tool agent output is never directly visible to the user",
		},
		"business_sufficiency": map[string]any{
			"status":        "sufficient",
			"missing_facts": []string{},
		},
	}
	if result.Reply != nil {
		summary = result.Reply.Text
	}
	if result.Ask != nil {
		status = "needs_clarification"
		missingContext = true
		summary = result.Ask.Question
		businessResult["ask"] = result.Ask
		review["business_sufficiency"] = map[string]any{
			"status":        "needs_clarification",
			"missing_facts": []string{"tool_agent_requested_user_clarification"},
		}
	}
	if result.Status != "" && result.Status != contracts.RunCompleted && status == "ok" {
		status = string(result.Status)
	}
	summary, sanitized := sanitizeToolAgentResultText(summary)
	if sanitized {
		riskFlags = append(riskFlags, "tool_agent_output_sanitized")
		review["permission_review"] = map[string]any{
			"decision": "sanitized",
			"reason":   "internal or non-user-ready content was removed",
		}
	}
	if summary != "" {
		businessResult["summary"] = summary
	} else if !missingContext {
		review["business_sufficiency"] = map[string]any{
			"status":        "insufficient",
			"missing_facts": []string{"tool_agent_result_summary"},
		}
		riskFlags = append(riskFlags, "business_sufficiency_insufficient")
	}
	return map[string]any{
		"schema_version":    "tool_agent_result.v1",
		"status":            status,
		"provider_agent_id": providerAgentID,
		"tool_id":           toolID,
		"operation":         operation,
		"run_id":            result.RunID,
		"task_id":           result.TaskID,
		"result_summary":    summary,
		"business_result":   businessResult,
		"risk_flags":        riskFlags,
		"missing_context":   missingContext,
		"safe_for_user":     false,
		"review":            review,
	}
}

func sanitizeToolAgentResultText(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	suppressed := []string{
		"capability_not_available",
		"no operation required",
		"no-op",
		"no_op",
		"tool result schema validation failed",
		"agent exported tool is not enabled",
	}
	for _, item := range suppressed {
		if lower == item || strings.Contains(lower, item) {
			return "tool agent returned no user-ready content", true
		}
	}
	internalMarkers := []string{
		"trace_id",
		"run_id",
		"task_id",
		"stack trace",
		"panic:",
		"runtime error",
		"worker_id",
		"tool_call_id",
	}
	for _, item := range internalMarkers {
		if strings.Contains(lower, item) {
			return "tool agent returned content that requires review", true
		}
	}
	const limit = 1200
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "...(truncated)", true
	}
	return text, false
}

func sanitizeToolAgentErrorSummary(value any) string {
	switch current := value.(type) {
	case nil:
		return ""
	case *contracts.RuntimeError:
		if current == nil {
			return ""
		}
		return sanitizeToolAgentErrorText(current.Message)
	case contracts.RuntimeError:
		return sanitizeToolAgentErrorText(current.Message)
	case error:
		return sanitizeToolAgentErrorText(current.Error())
	case string:
		return sanitizeToolAgentErrorText(current)
	default:
		return "tool agent failed"
	}
}

func sanitizeToolAgentErrorText(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	summary, sanitized := sanitizeToolAgentResultText(text)
	if sanitized {
		return summary
	}
	return summary
}

func agentToolInput(tool contracts.AgentExportedTool, operation string, arguments map[string]any) string {
	for _, key := range []string{"input", "task", "objective"} {
		if value, _ := arguments[key].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		encoded = []byte("{}")
	}
	if operation == "" {
		operation = tool.ToolID
	}
	return fmt.Sprintf("Execute exported tool %s (%s) with arguments: %s", tool.ToolID, operation, string(encoded))
}

func parentRuntimeConversation(runtimeContext map[string]any) *contracts.RuntimeConversation {
	parent, ok := runtimeContext["parent_context"].(map[string]any)
	if !ok {
		return nil
	}
	rawConversation, ok := parent["conversation"].(map[string]any)
	if !ok {
		return nil
	}
	conversationID := strings.TrimSpace(stringFromMap(rawConversation, "conversation_id"))
	if conversationID == "" {
		return nil
	}
	conversation := &contracts.RuntimeConversation{
		Provider:       stringFromMap(rawConversation, "provider"),
		Kind:           stringFromMap(rawConversation, "kind"),
		ConversationID: conversationID,
		ThreadID:       stringFromMap(rawConversation, "thread_id"),
		ExternalRefs:   stringMapFromAny(rawConversation["external_refs"]),
		CurrentMessage: runtimeMessageFromAny(rawConversation["current_message"]),
	}
	if conversation.ThreadID == "" {
		conversation.ThreadID = conversation.ConversationID
	}
	return conversation
}

func conversationForProviderAgentRun(conversation *contracts.RuntimeConversation, input string, callerToolID string, providerAgentID contracts.AgentID) *contracts.RuntimeConversation {
	if conversation == nil {
		return nil
	}
	parentMessage := conversation.CurrentMessage
	if parentRecent, ok := conversationMessageFromRuntime(parentMessage); ok {
		conversation.RecentMessages = append(conversation.RecentMessages, parentRecent)
	}
	metadata := map[string]any{"source": "agent_tool_request"}
	if parentMessage != nil {
		if parentMessage.MessageID != "" {
			metadata["parent_message_id"] = parentMessage.MessageID
		}
		if parentMessage.SpeakerID != "" {
			metadata["parent_speaker_id"] = parentMessage.SpeakerID
		}
	}
	conversation.CurrentMessage = &contracts.RuntimeMessage{
		MessageID:   idgen.New("msg"),
		SpeakerID:   string(callerToolID),
		SpeakerType: "agent_tool",
		ThreadID:    conversation.ThreadID,
		Text:        strings.TrimSpace(input),
		Mentions:    []string{string(providerAgentID)},
		Metadata:    metadata,
	}
	return conversation
}

func conversationMessageFromRuntime(message *contracts.RuntimeMessage) (contracts.ConversationMessage, bool) {
	if message == nil {
		return contracts.ConversationMessage{}, false
	}
	if strings.TrimSpace(message.MessageID) == "" && strings.TrimSpace(message.Text) == "" {
		return contracts.ConversationMessage{}, false
	}
	return contracts.ConversationMessage{
		MessageID:         message.MessageID,
		ExternalMessageID: message.ExternalMessageID,
		SpeakerID:         message.SpeakerID,
		SpeakerType:       message.SpeakerType,
		SpeakerName:       message.SpeakerName,
		Text:              message.Text,
		CreatedAt:         message.CreatedAt,
		ReplyToMessageID:  message.ReplyToMessageID,
		ThreadID:          message.ThreadID,
		Mentions:          append([]string(nil), message.Mentions...),
		Metadata:          cloneMap(message.Metadata),
	}, true
}

func runtimeMessageFromAny(value any) *contracts.RuntimeMessage {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	messageID := stringFromMap(raw, "message_id")
	text := stringFromMap(raw, "text")
	if strings.TrimSpace(messageID) == "" && strings.TrimSpace(text) == "" {
		return nil
	}
	return &contracts.RuntimeMessage{
		MessageID:         messageID,
		ExternalMessageID: stringFromMap(raw, "external_id"),
		SpeakerID:         stringFromMap(raw, "speaker_id"),
		SpeakerType:       stringFromMap(raw, "speaker_type"),
		SpeakerName:       stringFromMap(raw, "speaker_name"),
		ReplyToMessageID:  stringFromMap(raw, "reply_to_id"),
		ThreadID:          stringFromMap(raw, "thread_id"),
		Text:              text,
		Mentions:          stringSliceFromAny(raw["mentions"]),
		Metadata:          anyMapFromAny(raw["metadata"]),
	}
}

func (h Handler) record(ctx context.Context, call contracts.ToolCall, eventType string, payload map[string]any) error {
	if h.Trace == nil {
		return nil
	}
	traceID := contracts.TraceID(stringValue(payload, "trace_id"))
	if traceID == "" {
		return nil
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	return h.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  call.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     call.RunID,
		TaskID:    call.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: now,
	})
}

func now(clock func() time.Time) time.Time {
	if clock != nil {
		return clock().UTC()
	}
	return time.Now().UTC()
}

func stringArg(call contracts.ToolCall, key string) string {
	if call.Arguments == nil {
		return ""
	}
	value, _ := call.Arguments[key].(string)
	return value
}

func traceID(call contracts.ToolCall) string {
	if call.TraceID != "" {
		return string(call.TraceID)
	}
	return stringArg(call, "trace_id")
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func stringMapFromAny(value any) map[string]string {
	raw, ok := value.(map[string]string)
	if ok {
		out := make(map[string]string, len(raw))
		for key, current := range raw {
			out[key] = current
		}
		return out
	}
	rawAny, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for key, current := range rawAny {
		if text, ok := current.(string); ok {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringSliceFromAny(value any) []string {
	raw, ok := value.([]string)
	if ok {
		return append([]string(nil), raw...)
	}
	rawAny, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawAny))
	for _, current := range rawAny {
		if text, ok := current.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func anyMapFromAny(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(raw))
	for key, current := range raw {
		out[key] = current
	}
	return out
}

func cloneMap(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, len(raw))
	for key, current := range raw {
		out[key] = current
	}
	return out
}
