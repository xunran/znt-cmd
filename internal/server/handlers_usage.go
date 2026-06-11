package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

type usageEvidenceResponse struct {
	TraceID                contracts.TraceID      `json:"trace_id"`
	TenantID               contracts.TenantID     `json:"tenant_id,omitempty"`
	Source                 string                 `json:"source"`
	Ledger                 string                 `json:"ledger"`
	EventCount             int                    `json:"event_count"`
	RunIDs                 []contracts.AgentRunID `json:"run_ids"`
	TaskIDs                []contracts.TaskID     `json:"task_ids"`
	ModelCalls             int                    `json:"model_calls_total"`
	ModelFailures          int                    `json:"model_failures_total"`
	PromptTokens           int                    `json:"prompt_tokens_total"`
	CompletionTokens       int                    `json:"completion_tokens_total"`
	ToolInvocations        int                    `json:"tool_invocations_total"`
	ToolCompletions        int                    `json:"tool_completions_total"`
	ToolFailures           int                    `json:"tool_failures_total"`
	ToolApprovalWaits      int                    `json:"tool_approval_waits_total"`
	ToolProviderCalls      int                    `json:"tool_provider_calls_total"`
	ToolProviderFailures   int                    `json:"tool_provider_failures_total"`
	ApprovalRequests       int                    `json:"approval_requests_total"`
	ApprovalResolutions    int                    `json:"approval_resolutions_total"`
	RuntimeHookInvocations int                    `json:"runtime_hook_invocations_total"`
	ArtifactRefs           []contracts.ArtifactID `json:"artifact_refs"`
	ToolIDs                []string               `json:"tool_ids"`
	ModelNames             []string               `json:"model_names"`
	FirstAt                *time.Time             `json:"first_at,omitempty"`
	LastAt                 *time.Time             `json:"last_at,omitempty"`
}

func handleUsageEvidence(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported usage evidence method", nil), http.StatusMethodNotAllowed)
		return
	}
	traceID := contracts.TraceID(strings.TrimSpace(r.URL.Query().Get("trace_id")))
	if traceID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "usage evidence requires trace_id", nil), http.StatusBadRequest)
		return
	}
	if appCore.Trace == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace recorder is unavailable", nil))
		return
	}
	events, err := appCore.Trace.ListByTrace(r.Context(), traceID)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	events, allowed := traceEventsForTenant(events, caller.TenantID)
	if !allowed {
		writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace tenant does not match caller tenant", nil), http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"usage_evidence": buildUsageEvidence(traceID, caller.TenantID, events)}, http.StatusOK)
}

func buildUsageEvidence(traceID contracts.TraceID, tenantID contracts.TenantID, events []contracts.TraceEvent) usageEvidenceResponse {
	evidence := usageEvidenceResponse{
		TraceID:      traceID,
		TenantID:     tenantID,
		Source:       "trace",
		Ledger:       "external_metering",
		EventCount:   len(events),
		RunIDs:       []contracts.AgentRunID{},
		TaskIDs:      []contracts.TaskID{},
		ArtifactRefs: []contracts.ArtifactID{},
		ToolIDs:      []string{},
		ModelNames:   []string{},
	}
	runIDs := map[contracts.AgentRunID]struct{}{}
	taskIDs := map[contracts.TaskID]struct{}{}
	artifactRefs := map[contracts.ArtifactID]struct{}{}
	toolIDs := map[string]struct{}{}
	modelNames := map[string]struct{}{}
	for i, event := range events {
		if i == 0 || event.CreatedAt.Before(*evidence.FirstAt) {
			at := event.CreatedAt
			evidence.FirstAt = &at
		}
		if i == 0 || event.CreatedAt.After(*evidence.LastAt) {
			at := event.CreatedAt
			evidence.LastAt = &at
		}
		if event.RunID != "" {
			runIDs[event.RunID] = struct{}{}
		}
		if event.TaskID != "" {
			taskIDs[event.TaskID] = struct{}{}
		}
		if toolID, _ := event.Payload["tool_id"].(string); toolID != "" {
			toolIDs[toolID] = struct{}{}
		}
		for _, artifactID := range artifactIDsFromPayload(event.Payload) {
			artifactRefs[artifactID] = struct{}{}
		}
		switch event.Type {
		case contracts.TraceModelCalled:
			evidence.ModelCalls++
		case contracts.TraceModelCompleted:
			if _, failed := event.Payload["error"]; failed {
				evidence.ModelFailures++
			}
			evidence.PromptTokens += payloadInt(event.Payload, "prompt_tokens")
			evidence.CompletionTokens += payloadInt(event.Payload, "completion_tokens")
			modelProvider, _ := event.Payload["model_provider"].(string)
			modelName, _ := event.Payload["model_name"].(string)
			modelKey := strings.Trim(strings.TrimSpace(modelProvider)+"/"+strings.TrimSpace(modelName), "/")
			if modelKey != "" {
				modelNames[modelKey] = struct{}{}
			}
		case contracts.TraceToolInvoked:
			evidence.ToolInvocations++
		case contracts.TraceToolCompleted:
			evidence.ToolCompletions++
		case contracts.TraceToolFailed, contracts.TraceToolDenied:
			evidence.ToolFailures++
		case contracts.TraceToolPendingApproval:
			evidence.ToolApprovalWaits++
		case contracts.TraceToolProviderInvoked:
			evidence.ToolProviderCalls++
		case contracts.TraceToolProviderFailed:
			evidence.ToolProviderFailures++
		case contracts.TraceApprovalRequested:
			evidence.ApprovalRequests++
		case contracts.TraceApprovalResolved:
			evidence.ApprovalResolutions++
		case contracts.TraceRuntimeHookInvoked:
			evidence.RuntimeHookInvocations++
		}
	}
	evidence.RunIDs = sortedUsageRunIDs(runIDs)
	evidence.TaskIDs = sortedUsageTaskIDs(taskIDs)
	evidence.ArtifactRefs = sortedUsageArtifactIDs(artifactRefs)
	evidence.ToolIDs = sortedUsageStrings(toolIDs)
	evidence.ModelNames = sortedUsageStrings(modelNames)
	return evidence
}

func artifactIDsFromPayload(payload map[string]any) []contracts.ArtifactID {
	out := []contracts.ArtifactID{}
	if payload == nil {
		return out
	}
	for _, key := range []string{"artifact_id", "artifact_ref"} {
		if value, _ := payload[key].(string); value != "" {
			out = append(out, contracts.ArtifactID(value))
		}
	}
	for _, key := range []string{"artifact_refs", "artifacts"} {
		out = append(out, artifactIDsFromAny(payload[key])...)
	}
	return out
}

func artifactIDsFromAny(value any) []contracts.ArtifactID {
	switch typed := value.(type) {
	case []any:
		out := make([]contracts.ArtifactID, 0, len(typed))
		for _, item := range typed {
			out = append(out, artifactIDsFromAny(item)...)
		}
		return out
	case []contracts.ArtifactRef:
		out := make([]contracts.ArtifactID, 0, len(typed))
		for _, item := range typed {
			if item.ArtifactID != "" {
				out = append(out, item.ArtifactID)
			}
		}
		return out
	case map[string]any:
		if value, _ := typed["artifact_id"].(string); value != "" {
			return []contracts.ArtifactID{contracts.ArtifactID(value)}
		}
	case string:
		if typed != "" {
			return []contracts.ArtifactID{contracts.ArtifactID(typed)}
		}
	}
	return nil
}

func sortedUsageRunIDs(values map[contracts.AgentRunID]struct{}) []contracts.AgentRunID {
	out := make([]contracts.AgentRunID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedUsageTaskIDs(values map[contracts.TaskID]struct{}) []contracts.TaskID {
	out := make([]contracts.TaskID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedUsageArtifactIDs(values map[contracts.ArtifactID]struct{}) []contracts.ArtifactID {
	out := make([]contracts.ArtifactID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedUsageStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
