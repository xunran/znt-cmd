package agenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"znt/internal/agentdef/loader"
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
	_ = h.record(ctx, call, "agent_tool.invoked", map[string]any{
		"trace_id":          traceID(call),
		"provider_agent_id": providerAgentID,
		"operation":         operation,
		"tool_id":           manifest.ToolID,
	})
	if h.StartAgentRun == nil {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "agent_tool runner is not configured", map[string]any{"tool_id": call.ToolID, "provider_agent_id": providerAgentID})
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
			"input":               agentToolInput(*exported, operation, call.Arguments),
			"tool_id":             manifest.ToolID,
			"operation":           operation,
			"arguments":           call.Arguments,
			"source_tool_call_id": string(call.ToolCallID),
			"source_run_id":       string(call.RunID),
			"source_task_id":      string(call.TaskID),
		},
		Context: contracts.RuntimeContext{
			TenantID:  call.TenantID,
			RequestID: call.IdempotencyKey,
		},
		CreatedAt: now(h.Now),
	})
	if err != nil {
		_ = h.record(ctx, call, "agent_tool.failed", map[string]any{
			"trace_id":          traceID(call),
			"provider_agent_id": providerAgentID,
			"operation":         operation,
			"tool_id":           manifest.ToolID,
			"error":             err.Error(),
		})
		return nil, result.ArtifactRefs, err
	}
	if result.Error != nil {
		_ = h.record(ctx, call, "agent_tool.failed", map[string]any{
			"trace_id":          traceID(call),
			"provider_agent_id": providerAgentID,
			"operation":         operation,
			"tool_id":           manifest.ToolID,
			"error":             result.Error.ToTracePayload(),
		})
		return nil, result.ArtifactRefs, result.Error
	}
	output := map[string]any{
		"provider_agent_id": providerAgentID,
		"operation":         operation,
		"run_id":            result.RunID,
		"task_id":           result.TaskID,
		"status":            result.Status,
	}
	if result.Reply != nil {
		output["reply"] = result.Reply
		output["reply_text"] = result.Reply.Text
	}
	if result.Ask != nil {
		output["ask"] = result.Ask
	}
	_ = h.record(ctx, call, "agent_tool.completed", map[string]any{
		"trace_id":          traceID(call),
		"provider_agent_id": providerAgentID,
		"operation":         operation,
		"tool_id":           manifest.ToolID,
		"run_id":            result.RunID,
		"task_id":           result.TaskID,
		"status":            result.Status,
	})
	return output, result.ArtifactRefs, nil
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
