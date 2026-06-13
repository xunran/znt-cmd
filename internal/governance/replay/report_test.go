package replay

import (
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestBuildReplayReport(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	report := Build([]contracts.TraceEvent{
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceInputReceived, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceAgentLoaded, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceTaskCreated, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceCapabilityRetrieved, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceRunCreated, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceRunCreated, Payload: map[string]any{"policy_set_version": "v1"}, CreatedAt: now},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TracePromptBundleBuilt, Payload: map[string]any{
			"hash": "hash_1",
			"context_assembly_report": contracts.ContextAssemblyReport{
				StrategyHash: "strategy_hash_1",
				Mode:         "balanced",
			},
		}, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceContextCompressionCompleted, Payload: map[string]any{"applied": true}, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceDecisionCreated, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceDecisionValidated, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceModelCalled, Payload: map[string]any{"prompt_bundle_hash": "hash_1"}, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceModelCompleted, CreatedAt: now.Add(2 * time.Second)},
		{TraceID: "trace_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceResponseSent, CreatedAt: now.Add(3 * time.Second)},
	})
	if report.Status != "ok" || report.EventCount != 13 || report.EventTypes[contracts.TraceModelCalled] != 1 || len(report.PromptBundleHashes) != 1 || len(report.PolicyVersions) != 1 {
		t.Fatalf("unexpected replay report: %#v", report)
	}
	if len(report.ContextStrategyHashes) != 1 || report.ContextStrategyHashes[0] != "strategy_hash_1" || report.ContextCompressionCount != 1 || report.ContextCompressionApplied != 1 {
		t.Fatalf("unexpected replay report: %#v", report)
	}
}

func TestBuildReplayReportCollectsStrategyResolvedHash(t *testing.T) {
	report := Build([]contracts.TraceEvent{{
		TraceID: "trace_1",
		TenantID: "tenant_1",
		Type:    contracts.TraceStrategyResolved,
		Payload: map[string]any{"strategy_hash": "strategy_hash_from_resolver"},
	}})
	if len(report.ContextStrategyHashes) != 1 || report.ContextStrategyHashes[0] != "strategy_hash_from_resolver" {
		t.Fatalf("expected strategy hash from strategy.resolved trace, got %#v", report)
	}
}

func TestBuildReplayReportCollectsContextCollectionHash(t *testing.T) {
	report := Build([]contracts.TraceEvent{{
		TraceID: "trace_1",
		TenantID: "tenant_1",
		Type:    contracts.TraceContextCollectionCompleted,
		Payload: map[string]any{
			"context_assembly_report": contracts.ContextAssemblyReport{
				StrategyHash: "strategy_hash_from_context_collection",
				Mode:         "balanced",
			},
		},
	}})
	if len(report.ContextStrategyHashes) != 1 || report.ContextStrategyHashes[0] != "strategy_hash_from_context_collection" {
		t.Fatalf("expected strategy hash from context.collection.completed trace, got %#v", report)
	}
}

func TestBuildReplayReportFlagsMissingCompletion(t *testing.T) {
	report := Build([]contracts.TraceEvent{
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceRunCreated},
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceModelCalled},
	})
	if report.Status != "incomplete" || len(report.Problems) == 0 {
		t.Fatalf("expected incomplete replay report, got %#v", report)
	}
}

func TestBuildReplayReportFlagsSensitivePayload(t *testing.T) {
	report := Build([]contracts.TraceEvent{
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceRunCreated, Payload: map[string]any{"policy_set_version": "v1"}},
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceToolInvoked, Payload: map[string]any{
			"headers": map[string]any{"authorization": "Bearer secret-token-value"},
			"config":  map[string]any{"api_key": "sk-test-secret-token-value"},
		}},
	})
	if len(report.RedactionViolations) != 2 {
		t.Fatalf("expected sensitive payload violations, got %#v", report)
	}
}

func TestBuildReplayReportAllowsCredentialReferences(t *testing.T) {
	report := Build([]contracts.TraceEvent{
		{TraceID: "trace_1", TenantID: "tenant_1", Type: contracts.TraceCredentialUsed, Payload: map[string]any{
			"credential_ref": "cred/github",
			"secret_ref":     "secret://tenant_1/github",
			"ciphertext_ref": "cipher://tenant_1/blob",
		}},
	})
	if len(report.RedactionViolations) != 0 {
		t.Fatalf("credential references should not be redaction violations: %#v", report.RedactionViolations)
	}
}
