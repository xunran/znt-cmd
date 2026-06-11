package metrics

import (
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestFromTraceCountsGovernanceSignals(t *testing.T) {
	snapshot := FromTrace([]contracts.TraceEvent{
		{Type: contracts.TraceModelCalled},
		{Type: contracts.TraceModelCompleted, Payload: map[string]any{"error": "timeout"}},
		{Type: contracts.TraceToolInvoked},
		{Type: contracts.TraceToolFailed},
		{Type: contracts.TraceHandoffCreated},
		{Type: contracts.TracePromptBundleBuilt},
		{Type: contracts.TraceConversationContextBuilt},
		{Type: contracts.TraceConversationAddresseeJudged},
		{Type: contracts.TraceConversationSufficiencyJudged},
		{Type: contracts.TraceConversationContextRetrievalRequested},
		{Type: contracts.TraceConversationContextRetrievalCompleted},
		{Type: contracts.TraceConversationRetrievedContextMerged},
		{Type: contracts.TraceExternalWritebackOK},
		{Type: contracts.TraceExternalWritebackFailed},
	})
	if snapshot.ModelCalls != 1 || snapshot.ModelFailures != 1 || snapshot.ToolInvocations != 1 ||
		snapshot.ToolFailures != 1 || snapshot.Handoffs != 1 || snapshot.PromptBundles != 1 ||
		snapshot.ConversationContextsBuilt != 1 || snapshot.ConversationAddresseeJudgements != 1 ||
		snapshot.ConversationSufficiencyJudgements != 1 || snapshot.ConversationRetrievalRequests != 1 ||
		snapshot.ConversationRetrievalCompletions != 1 || snapshot.ConversationRetrievedContextMerges != 1 ||
		snapshot.ExternalWritebacks != 2 || snapshot.ExternalWritebackFailures != 1 ||
		snapshot.ExternalWritebackFailureRatePercent != 50 {
		t.Fatalf("unexpected metrics snapshot: %#v", snapshot)
	}
}

func TestFromTraceReportsGovernanceLatencies(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	snapshot := FromTrace([]contracts.TraceEvent{
		{Type: contracts.TraceHandoffCreated, CreatedAt: base},
		{Type: contracts.TraceRuntimeHookInvoked, Payload: map[string]any{"hook_id": "rank", "phase": "before_model_call"}, CreatedAt: base.Add(10 * time.Millisecond)},
		{Type: contracts.TraceRuntimeHookApplied, Payload: map[string]any{"hook_id": "rank", "phase": "before_model_call"}, CreatedAt: base.Add(35 * time.Millisecond)},
		{Type: contracts.TraceApprovalRequested, CreatedAt: base.Add(50 * time.Millisecond)},
		{Type: contracts.TraceApprovalResolved, CreatedAt: base.Add(90 * time.Millisecond)},
		{Type: contracts.TraceHandoffCompleted, CreatedAt: base.Add(125 * time.Millisecond)},
	})
	if snapshot.RuntimeHookInvocations != 1 || snapshot.RuntimeHookApplied != 1 || snapshot.RuntimeHookLatencyMilliseconds != 25 {
		t.Fatalf("unexpected hook metrics: %#v", snapshot)
	}
	if snapshot.ApprovalRequests != 1 || snapshot.ApprovalResolutions != 1 || snapshot.ApprovalWaitMilliseconds != 40 {
		t.Fatalf("unexpected approval metrics: %#v", snapshot)
	}
	if snapshot.HandoffLatencyMilliseconds != 125 {
		t.Fatalf("unexpected handoff latency: %#v", snapshot)
	}
}

func TestFromTraceCountsToolProviderSignals(t *testing.T) {
	snapshot := FromTrace([]contracts.TraceEvent{
		{Type: contracts.TraceToolProviderInvoked},
		{Type: contracts.TraceToolProviderCompleted, Payload: map[string]any{"latency_ms": 15}},
		{Type: contracts.TraceToolProviderInvoked},
		{Type: contracts.TraceToolProviderFailed, Payload: map[string]any{"latency_ms": float64(25)}},
		{Type: contracts.TraceToolProviderHealthChecked, Payload: map[string]any{"health_status": "healthy", "latency_ms": 5}},
		{Type: contracts.TraceToolProviderHealthChecked, Payload: map[string]any{"health_status": "unhealthy", "latency_ms": 10}},
	})
	if snapshot.ToolProviderInvocations != 2 || snapshot.ToolProviderFailures != 1 ||
		snapshot.ToolProviderHealthChecks != 2 || snapshot.ToolProviderHealthFailures != 1 ||
		snapshot.ToolProviderLatencyMilliseconds != 55 {
		t.Fatalf("unexpected provider metrics: %#v", snapshot)
	}
	if snapshot.ToolProviderFailureRatePercent != 50 || snapshot.ToolProviderHealthFailureRatePercent != 50 {
		t.Fatalf("unexpected provider failure rates: %#v", snapshot)
	}
}

func TestFromTraceCountsRuntimeHookProviderHealth(t *testing.T) {
	snapshot := FromTrace([]contracts.TraceEvent{
		{Type: contracts.TraceRuntimeHookProviderHealthChecked, Payload: map[string]any{"health_status": "healthy", "latency_ms": 7}},
		{Type: contracts.TraceRuntimeHookProviderHealthChecked, Payload: map[string]any{"health_status": "unhealthy", "latency_ms": float64(13)}},
	})
	if snapshot.RuntimeHookProviderHealthChecks != 2 || snapshot.RuntimeHookProviderHealthFailures != 1 ||
		snapshot.RuntimeHookProviderHealthLatencyMilliseconds != 20 ||
		snapshot.RuntimeHookProviderHealthFailureRatePercent != 50 {
		t.Fatalf("unexpected runtime hook provider health metrics: %#v", snapshot)
	}
}
