package array

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	storagerepo "znt/internal/storage/repository"
)

func TestBridgeStoresMessagesAndArtifacts(t *testing.T) {
	bridge := NewBridge()
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if err := bridge.SendMessage(context.Background(), contracts.SendExternalMessageRequest{Ref: ref, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.AttachArtifact(context.Background(), contracts.AttachArtifactRequest{Ref: ref, ArtifactRef: contracts.ArtifactRef{ArtifactID: "artifact_1"}}); err != nil {
		t.Fatal(err)
	}
	if len(bridge.Messages) != 1 || len(bridge.Artifacts) != 1 {
		t.Fatalf("unexpected bridge state: %#v", bridge)
	}
}

func TestBridgeCheckAccessUsesTenantBinding(t *testing.T) {
	bridge := NewBridge()
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := bridge.CheckAccess(context.Background(), contracts.CollaborationAccessRequest{
		Ref:      ref,
		TenantID: "tenant_2",
		CallerID: "user_2",
		Action:   "read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatalf("expected cross-tenant access to be denied: %#v", decision)
	}
}

func TestBridgeWritebackFailureRequiresBinding(t *testing.T) {
	bridge := NewBridge()
	err := bridge.MarkWritebackFailed(context.Background(), "array", "missing", "failed")
	if !errors.Is(err, storagerepo.ErrNotFound) {
		t.Fatalf("expected not found for missing binding, got %v", err)
	}
}

func TestBridgeHTTPAdapterReadsTaskAndChecksAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_1" {
			t.Fatalf("missing bearer token: %s", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/tasks/ext_1":
			if r.URL.Query().Get("provider") != "array" {
				t.Fatalf("provider query was not forwarded: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(contracts.ExternalTaskSummary{
				Ref:    contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"},
				Title:  "remote task",
				Status: "open",
			})
		case "/access-check":
			_ = json.NewEncoder(w).Encode(contracts.AccessDecision{Allowed: true, Reason: "remote ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bridge := NewBridgeWithAdapter(NewHTTPAdapter(server.URL, "token_1"))
	summary, err := bridge.GetTask(context.Background(), contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Title != "remote task" || summary.Status != "open" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	decision, err := bridge.CheckAccess(context.Background(), contracts.CollaborationAccessRequest{
		Ref:      contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"},
		TenantID: "tenant_1",
		Action:   "read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.Reason != "remote ok" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestBridgeHTTPWritebackFailureMarksBinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "remote down", http.StatusBadGateway)
	}))
	defer server.Close()

	bridge := NewBridgeWithAdapter(NewHTTPAdapter(server.URL, ""))
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	err := bridge.SendMessage(context.Background(), contracts.SendExternalMessageRequest{Ref: ref, Message: "hello"})
	if err == nil {
		t.Fatal("expected send failure")
	}
	binding, ok, err := bridge.GetBinding(context.Background(), ref.Provider, ref.ExternalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || binding.Status != "writeback_failed" || binding.LastError == "" {
		t.Fatalf("writeback failure was not recorded: %#v", binding)
	}
}

func TestSyncerRecordsWritebackFailureGovernance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "remote down", http.StatusBadGateway)
	}))
	defer server.Close()

	bridge := NewBridgeWithAdapter(NewHTTPAdapter(server.URL, ""))
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	traceRecorder := trace.NewInMemoryRecorder()
	auditLogger := audit.NewInMemoryLogger()
	syncer := NewGovernedSyncer(bridge, traceRecorder, auditLogger)
	syncer.ReplyWithContext(context.Background(), &contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}, SyncContext{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		RunID:     "run_1",
		TaskID:    "task_1",
		ActorID:   "agent_1",
		ActorType: "agent",
	}, "hello")

	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != contracts.TraceExternalWritebackFailed || events[0].RunID != "run_1" || events[0].TaskID != "task_1" {
		t.Fatalf("unexpected writeback trace events: %#v", events)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{
		TenantID: "tenant_1",
		Action:   contracts.AuditExternalWritebackFailed,
		TaskID:   "task_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].RunID != "run_1" || audits[0].Reason == "" {
		t.Fatalf("unexpected writeback audit events: %#v", audits)
	}
}
