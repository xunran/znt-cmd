package intake

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
)

func TestEvaluateMatchesHighestPriorityPolicyAndTraces(t *testing.T) {
	traceRecorder := trace.NewInMemoryRecorder()
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(nil, auditLogger, traceRecorder)
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }

	if _, err := service.Upsert(context.Background(), Policy{
		TenantID:      "tenant_1",
		PolicyID:      "low",
		Name:          "Low",
		Priority:      1,
		AgentID:       "agent_1",
		Channel:       "web",
		MatchType:     MatchContains,
		Pattern:       "deploy",
		ReplyText:     "收到，我先看一下。",
		ReplyKind:     ReplyAcknowledgment,
		ContinueToRun: true,
	}, "optimizer_1", "optimizer"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := service.Upsert(context.Background(), Policy{
		TenantID:      "tenant_1",
		PolicyID:      "high",
		Name:          "High",
		Priority:      10,
		AgentID:       "agent_1",
		Channel:       "web",
		MatchType:     MatchRegex,
		Pattern:       "deploy|release",
		ReplyText:     "收到，我会先确认发布上下文。",
		ReplyKind:     ReplyStatusUpdate,
		ContinueToRun: true,
	}, "optimizer_1", "optimizer"); err != nil {
		t.Fatal(err)
	}

	result, err := service.Evaluate(context.Background(), EvaluateRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		AgentID:  "agent_1",
		Channel:  "web",
		Input:    "please deploy this",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched || result.PolicyID != "high" || result.ReplyText != "收到，我会先确认发布上下文。" || result.Dispatch != DispatchExternalChannel {
		t.Fatalf("unexpected evaluate result: %#v", result)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != contracts.TraceIntakePreReplyEvaluated || events[0].Payload["policy_id"] != "high" {
		t.Fatalf("expected intake trace, got %#v", events)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: contracts.AuditIntakePolicyUpserted})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 {
		t.Fatalf("expected two policy upsert audits, got %#v", audits)
	}
}

func TestEvaluateNoMatchContinuesWithoutReply(t *testing.T) {
	service := NewService(nil, nil, nil)
	if _, err := service.Upsert(context.Background(), Policy{
		TenantID:      "tenant_1",
		PolicyID:      "billing",
		MatchType:     MatchPrefix,
		Pattern:       "billing",
		ReplyText:     "收到，我先看账单上下文。",
		ContinueToRun: true,
	}, "", ""); err != nil {
		t.Fatal(err)
	}

	result, err := service.Evaluate(context.Background(), EvaluateRequest{
		TenantID: "tenant_1",
		Input:    "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched || !result.ContinueToRun || result.ReplyText != "" {
		t.Fatalf("expected no-match continue result, got %#v", result)
	}
}

func TestDeleteWritesAudit(t *testing.T) {
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(nil, auditLogger, nil)
	if _, err := service.Upsert(context.Background(), Policy{
		TenantID:      "tenant_1",
		PolicyID:      "policy_1",
		MatchType:     MatchAlways,
		ReplyText:     "收到。",
		ContinueToRun: true,
	}, "optimizer_1", "optimizer"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.Delete(context.Background(), "tenant_1", "policy_1", "optimizer_1", "optimizer"); err != nil || !ok {
		t.Fatalf("expected delete ok, ok=%v err=%v", ok, err)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{TenantID: "tenant_1", Action: contracts.AuditIntakePolicyDeleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != "policy_1" {
		t.Fatalf("expected delete audit, got %#v", audits)
	}
}

func TestInvalidRegexRejected(t *testing.T) {
	service := NewService(nil, nil, nil)
	_, err := service.Upsert(context.Background(), Policy{
		TenantID:  "tenant_1",
		PolicyID:  "bad",
		MatchType: MatchRegex,
		Pattern:   "[",
		ReplyText: "收到。",
	}, "", "")
	if err == nil {
		t.Fatal("expected regex validation error")
	}
}
