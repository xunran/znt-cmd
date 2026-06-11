package metrics

import (
	"encoding/json"
	"fmt"
	"time"

	"znt/internal/contracts"
)

type Snapshot struct {
	ModelCalls                                   int `json:"model_calls_total"`
	ModelFailures                                int `json:"model_failures_total"`
	ToolInvocations                              int `json:"tool_invocations_total"`
	ToolFailures                                 int `json:"tool_failures_total"`
	ToolApprovalWaits                            int `json:"tool_approval_waits_total"`
	ApprovalRequests                             int `json:"approval_requests_total"`
	ApprovalResolutions                          int `json:"approval_resolutions_total"`
	Handoffs                                     int `json:"handoffs_total"`
	HandoffFailures                              int `json:"handoff_failures_total"`
	HandoffCompleted                             int `json:"handoffs_completed_total"`
	RuntimeHookInvocations                       int `json:"runtime_hook_invocations_total"`
	RuntimeHookApplied                           int `json:"runtime_hook_applied_total"`
	RuntimeHookDenied                            int `json:"runtime_hook_denied_total"`
	RuntimeHookFailures                          int `json:"runtime_hook_failures_total"`
	RuntimeHookTimeouts                          int `json:"runtime_hook_timeouts_total"`
	RuntimeHookProviderHealthChecks              int `json:"runtime_hook_provider_health_checks_total"`
	RuntimeHookProviderHealthFailures            int `json:"runtime_hook_provider_health_failures_total"`
	ToolProviderInvocations                      int `json:"tool_provider_invocations_total"`
	ToolProviderFailures                         int `json:"tool_provider_failures_total"`
	ToolProviderHealthChecks                     int `json:"tool_provider_health_checks_total"`
	ToolProviderHealthFailures                   int `json:"tool_provider_health_failures_total"`
	ConversationContextsBuilt                    int `json:"conversation_contexts_built_total"`
	ConversationAddresseeJudgements              int `json:"conversation_addressee_judgements_total"`
	ConversationSufficiencyJudgements            int `json:"conversation_sufficiency_judgements_total"`
	ConversationRetrievalRequests                int `json:"conversation_retrieval_requests_total"`
	ConversationRetrievalCompletions             int `json:"conversation_retrieval_completions_total"`
	ConversationRetrievalFailures                int `json:"conversation_retrieval_failures_total"`
	ConversationRetrievedContextMerges           int `json:"conversation_retrieved_context_merges_total"`
	ExternalWritebacks                           int `json:"external_writebacks_total"`
	ExternalWritebackFailures                    int `json:"external_writeback_failures_total"`
	PromptBundles                                int `json:"prompt_bundles_total"`
	InputsReceived                               int `json:"inputs_received_total"`
	AgentsLoaded                                 int `json:"agents_loaded_total"`
	TasksLoaded                                  int `json:"tasks_loaded_total"`
	TasksCreated                                 int `json:"tasks_created_total"`
	CapabilitiesRetrieved                        int `json:"capabilities_retrieved_total"`
	ResponseEvents                               int `json:"response_events_total"`
	DurationMilliseconds                         int `json:"duration_ms,omitempty"`
	RuntimeHookLatencyMilliseconds               int `json:"runtime_hook_latency_ms,omitempty"`
	RuntimeHookProviderHealthLatencyMilliseconds int `json:"runtime_hook_provider_health_latency_ms,omitempty"`
	ToolProviderLatencyMilliseconds              int `json:"tool_provider_latency_ms,omitempty"`
	ApprovalWaitMilliseconds                     int `json:"approval_wait_ms,omitempty"`
	HandoffLatencyMilliseconds                   int `json:"handoff_latency_ms,omitempty"`
	ModelFailureRatePercent                      int `json:"model_failure_rate_percent,omitempty"`
	ToolFailureRatePercent                       int `json:"tool_failure_rate_percent,omitempty"`
	HandoffFailureRatePercent                    int `json:"handoff_failure_rate_percent,omitempty"`
	ExternalWritebackFailureRatePercent          int `json:"external_writeback_failure_rate_percent,omitempty"`
	RuntimeHookProviderHealthFailureRatePercent  int `json:"runtime_hook_provider_health_failure_rate_percent,omitempty"`
	ToolProviderFailureRatePercent               int `json:"tool_provider_failure_rate_percent,omitempty"`
	ToolProviderHealthFailureRatePercent         int `json:"tool_provider_health_failure_rate_percent,omitempty"`
}

func FromTrace(events []contracts.TraceEvent) Snapshot {
	var snapshot Snapshot
	hookStarts := map[string]time.Time{}
	var approvalRequestedAt *time.Time
	var handoffCreatedAt *time.Time
	if len(events) > 1 {
		first := events[0].CreatedAt
		last := events[0].CreatedAt
		for _, event := range events {
			if event.CreatedAt.Before(first) {
				first = event.CreatedAt
			}
			if event.CreatedAt.After(last) {
				last = event.CreatedAt
			}
		}
		snapshot.DurationMilliseconds = int(last.Sub(first).Milliseconds())
	}
	for _, event := range events {
		switch event.Type {
		case contracts.TraceInputReceived:
			snapshot.InputsReceived++
		case contracts.TraceAgentLoaded:
			snapshot.AgentsLoaded++
		case contracts.TraceTaskCreated:
			snapshot.TasksCreated++
		case contracts.TraceTaskLoaded:
			snapshot.TasksLoaded++
		case contracts.TraceCapabilityRetrieved:
			snapshot.CapabilitiesRetrieved++
		case contracts.TraceModelCalled:
			snapshot.ModelCalls++
		case contracts.TraceModelCompleted:
			if _, failed := event.Payload["error"]; failed {
				snapshot.ModelFailures++
			}
		case contracts.TraceToolInvoked:
			snapshot.ToolInvocations++
		case contracts.TraceToolFailed, contracts.TraceToolDenied:
			snapshot.ToolFailures++
		case contracts.TraceToolPendingApproval:
			snapshot.ToolApprovalWaits++
		case contracts.TraceHandoffCreated:
			snapshot.Handoffs++
			if handoffCreatedAt == nil && !event.CreatedAt.IsZero() {
				createdAt := event.CreatedAt
				handoffCreatedAt = &createdAt
			}
		case contracts.TraceHandoffCompleted:
			snapshot.HandoffCompleted++
			if handoffCreatedAt != nil && !event.CreatedAt.IsZero() {
				snapshot.HandoffLatencyMilliseconds += int(event.CreatedAt.Sub(*handoffCreatedAt).Milliseconds())
				handoffCreatedAt = nil
			}
		case "handoff.failed":
			snapshot.HandoffFailures++
		case contracts.TraceApprovalRequested:
			snapshot.ApprovalRequests++
			if approvalRequestedAt == nil && !event.CreatedAt.IsZero() {
				requestedAt := event.CreatedAt
				approvalRequestedAt = &requestedAt
			}
		case contracts.TraceApprovalResolved:
			snapshot.ApprovalResolutions++
			if approvalRequestedAt != nil && !event.CreatedAt.IsZero() {
				snapshot.ApprovalWaitMilliseconds += int(event.CreatedAt.Sub(*approvalRequestedAt).Milliseconds())
				approvalRequestedAt = nil
			}
		case contracts.TraceRuntimeHookInvoked:
			snapshot.RuntimeHookInvocations++
			if !event.CreatedAt.IsZero() {
				hookStarts[hookKey(event)] = event.CreatedAt
			}
		case contracts.TraceRuntimeHookApplied:
			snapshot.RuntimeHookApplied++
			snapshot.RuntimeHookLatencyMilliseconds += elapsedFromStart(hookStarts, event)
		case contracts.TraceRuntimeHookDenied:
			snapshot.RuntimeHookDenied++
			snapshot.RuntimeHookLatencyMilliseconds += elapsedFromStart(hookStarts, event)
		case contracts.TraceRuntimeHookFailed:
			snapshot.RuntimeHookFailures++
			snapshot.RuntimeHookLatencyMilliseconds += elapsedFromStart(hookStarts, event)
		case contracts.TraceRuntimeHookTimeout:
			snapshot.RuntimeHookTimeouts++
			snapshot.RuntimeHookLatencyMilliseconds += elapsedFromStart(hookStarts, event)
		case contracts.TraceRuntimeHookProviderHealthChecked:
			snapshot.RuntimeHookProviderHealthChecks++
			snapshot.RuntimeHookProviderHealthLatencyMilliseconds += intPayload(event.Payload, "latency_ms")
			if healthStatus, _ := event.Payload["health_status"].(string); healthStatus != "" && healthStatus != "healthy" {
				snapshot.RuntimeHookProviderHealthFailures++
			}
		case contracts.TraceToolProviderInvoked:
			snapshot.ToolProviderInvocations++
		case contracts.TraceToolProviderCompleted:
			snapshot.ToolProviderLatencyMilliseconds += intPayload(event.Payload, "latency_ms")
		case contracts.TraceToolProviderFailed:
			snapshot.ToolProviderFailures++
			snapshot.ToolProviderLatencyMilliseconds += intPayload(event.Payload, "latency_ms")
		case contracts.TraceToolProviderHealthChecked:
			snapshot.ToolProviderHealthChecks++
			snapshot.ToolProviderLatencyMilliseconds += intPayload(event.Payload, "latency_ms")
			if healthStatus, _ := event.Payload["health_status"].(string); healthStatus != "" && healthStatus != "healthy" {
				snapshot.ToolProviderHealthFailures++
			}
		case contracts.TraceConversationContextBuilt:
			snapshot.ConversationContextsBuilt++
		case contracts.TraceConversationAddresseeJudged:
			snapshot.ConversationAddresseeJudgements++
		case contracts.TraceConversationSufficiencyJudged:
			snapshot.ConversationSufficiencyJudgements++
		case contracts.TraceConversationContextRetrievalRequested:
			snapshot.ConversationRetrievalRequests++
		case contracts.TraceConversationContextRetrievalCompleted:
			snapshot.ConversationRetrievalCompletions++
		case contracts.TraceConversationContextRetrievalFailed:
			snapshot.ConversationRetrievalFailures++
		case contracts.TraceConversationRetrievedContextMerged:
			snapshot.ConversationRetrievedContextMerges++
		case contracts.TraceExternalWritebackOK:
			snapshot.ExternalWritebacks++
		case contracts.TraceExternalWritebackFailed:
			snapshot.ExternalWritebacks++
			snapshot.ExternalWritebackFailures++
		case contracts.TracePromptBundleBuilt:
			snapshot.PromptBundles++
		case contracts.TraceResponseSent:
			snapshot.ResponseEvents++
		}
	}
	if snapshot.ModelCalls > 0 {
		snapshot.ModelFailureRatePercent = snapshot.ModelFailures * 100 / snapshot.ModelCalls
	}
	if snapshot.ToolInvocations > 0 {
		snapshot.ToolFailureRatePercent = snapshot.ToolFailures * 100 / snapshot.ToolInvocations
	}
	if snapshot.Handoffs > 0 {
		snapshot.HandoffFailureRatePercent = snapshot.HandoffFailures * 100 / snapshot.Handoffs
	}
	if snapshot.ExternalWritebacks > 0 {
		snapshot.ExternalWritebackFailureRatePercent = snapshot.ExternalWritebackFailures * 100 / snapshot.ExternalWritebacks
	}
	if snapshot.RuntimeHookProviderHealthChecks > 0 {
		snapshot.RuntimeHookProviderHealthFailureRatePercent = snapshot.RuntimeHookProviderHealthFailures * 100 / snapshot.RuntimeHookProviderHealthChecks
	}
	if snapshot.ToolProviderInvocations > 0 {
		snapshot.ToolProviderFailureRatePercent = snapshot.ToolProviderFailures * 100 / snapshot.ToolProviderInvocations
	}
	if snapshot.ToolProviderHealthChecks > 0 {
		snapshot.ToolProviderHealthFailureRatePercent = snapshot.ToolProviderHealthFailures * 100 / snapshot.ToolProviderHealthChecks
	}
	return snapshot
}

func hookKey(event contracts.TraceEvent) string {
	hookID, _ := event.Payload["hook_id"].(string)
	phase, _ := event.Payload["phase"].(string)
	if hookID == "" && phase == "" {
		return "__default__"
	}
	return fmt.Sprintf("%s\x00%s", hookID, phase)
}

func elapsedFromStart(starts map[string]time.Time, event contracts.TraceEvent) int {
	if event.CreatedAt.IsZero() {
		return 0
	}
	key := hookKey(event)
	start, ok := starts[key]
	if !ok || start.IsZero() {
		return 0
	}
	delete(starts, key)
	return int(event.CreatedAt.Sub(start).Milliseconds())
}

func intPayload(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}
