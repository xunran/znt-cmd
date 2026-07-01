package array

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	storagerepo "znt/internal/storage/repository"
)

type memoryOutboxStore struct {
	mu       sync.Mutex
	bindings map[string]contracts.ExternalTaskBinding
	items    map[string]contracts.ExternalDeliveryOutboxItem
	byIDem   map[string]string
}

func newMemoryOutboxStore() *memoryOutboxStore {
	return &memoryOutboxStore{bindings: map[string]contracts.ExternalTaskBinding{}, items: map[string]contracts.ExternalDeliveryOutboxItem{}, byIDem: map[string]string{}}
}

func (s *memoryOutboxStore) SaveBinding(_ context.Context, binding contracts.ExternalTaskBinding) (contracts.ExternalTaskBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[binding.Provider+"\x00"+string(binding.ExternalTaskID)] = binding
	return binding, nil
}

func (s *memoryOutboxStore) GetBinding(_ context.Context, provider string, externalTaskID contracts.ExternalTaskID) (contracts.ExternalTaskBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[provider+"\x00"+string(externalTaskID)]
	return binding, ok, nil
}

func (s *memoryOutboxStore) GetBindingByCoreTask(_ context.Context, tenantID contracts.TenantID, coreTaskID contracts.TaskID) (contracts.ExternalTaskBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, binding := range s.bindings {
		if binding.TenantID == tenantID && binding.CoreTaskID == coreTaskID {
			return binding, true, nil
		}
	}
	return contracts.ExternalTaskBinding{}, false, nil
}

func (s *memoryOutboxStore) UpdateBindingStatus(_ context.Context, provider string, externalTaskID contracts.ExternalTaskID, status string, lastError string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := provider + "\x00" + string(externalTaskID)
	binding, ok := s.bindings[key]
	if !ok {
		return storagerepo.ErrNotFound
	}
	binding.Status = status
	binding.LastError = lastError
	binding.UpdatedAt = updatedAt
	s.bindings[key] = binding
	return nil
}

func (s *memoryOutboxStore) EnqueueDelivery(_ context.Context, item contracts.ExternalDeliveryOutboxItem) (contracts.ExternalDeliveryOutboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(item.TenantID) + "\x00" + item.IdempotencyKey
	if existingID, ok := s.byIDem[key]; ok {
		existing := s.items[existingID]
		existing.Payload = item.Payload
		existing.Status = item.Status
		existing.UpdatedAt = item.UpdatedAt
		s.items[existingID] = existing
		return existing, nil
	}
	s.byIDem[key] = item.OutboxID
	s.items[item.OutboxID] = item
	return item, nil
}

func (s *memoryOutboxStore) MarkDeliveryAttempt(_ context.Context, outboxID string, status string, lastError string, nextAttemptAt time.Time, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[outboxID]
	if !ok {
		return storagerepo.ErrNotFound
	}
	item.Status = status
	item.LastError = lastError
	item.NextAttemptAt = nextAttemptAt
	item.UpdatedAt = updatedAt
	if status == "delivered" || status == "failed" {
		item.AttemptCount++
	}
	s.items[outboxID] = item
	return nil
}

func (s *memoryOutboxStore) GetDelivery(_ context.Context, tenantID contracts.TenantID, outboxID string) (contracts.ExternalDeliveryOutboxItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[outboxID]
	if !ok || item.TenantID != tenantID {
		return contracts.ExternalDeliveryOutboxItem{}, false, nil
	}
	return item, true, nil
}

func (s *memoryOutboxStore) ListDeliveriesDue(_ context.Context, opts contracts.ExternalDeliveryReplayOptions, now time.Time) ([]contracts.ExternalDeliveryOutboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	statusSet := map[string]bool{}
	for _, status := range opts.Statuses {
		statusSet[status] = true
	}
	out := make([]contracts.ExternalDeliveryOutboxItem, 0)
	for _, item := range s.items {
		if opts.TenantID != "" && item.TenantID != opts.TenantID {
			continue
		}
		if !statusSet[item.Status] {
			continue
		}
		if !item.NextAttemptAt.IsZero() && item.NextAttemptAt.After(now) {
			continue
		}
		out = append(out, item)
		if len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

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

func TestBridgeQueuesDeliveryOutboxAndMarksDelivered(t *testing.T) {
	store := newMemoryOutboxStore()
	bridge := NewBridgeWithStore(store)
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.SendMessage(context.Background(), contracts.SendExternalMessageRequest{Ref: ref, Message: "hello", IdempotencyKey: "idem_1"}); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 {
		t.Fatalf("expected one outbox item, got %#v", store.items)
	}
	for _, item := range store.items {
		if item.Status != "delivered" || item.AttemptCount != 1 || item.IdempotencyKey != "idem_1" || item.CoreTaskID != "task_1" {
			t.Fatalf("unexpected outbox item %#v", item)
		}
	}
}

func TestBridgeQueuesDeliveryOutboxAndMarksFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "remote down", http.StatusBadGateway)
	}))
	defer server.Close()

	store := newMemoryOutboxStore()
	bridge := NewBridgeWithStoreAndAdapter(store, NewHTTPAdapter(server.URL, ""))
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.SendMessage(context.Background(), contracts.SendExternalMessageRequest{Ref: ref, Message: "hello", IdempotencyKey: "idem_1"}); err == nil {
		t.Fatal("expected send failure")
	}
	for _, item := range store.items {
		if item.Status != "failed" || item.AttemptCount != 1 || item.LastError == "" || item.NextAttemptAt.IsZero() {
			t.Fatalf("unexpected failed outbox item %#v", item)
		}
	}
}

func TestBridgeReplaysFailedDelivery(t *testing.T) {
	store := newMemoryOutboxStore()
	bridge := NewBridgeWithStore(store)
	ref := contracts.ExternalTaskRef{Provider: "array", ExternalTaskID: "ext_1"}
	if _, err := bridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	item, err := store.EnqueueDelivery(context.Background(), contracts.ExternalDeliveryOutboxItem{
		OutboxID:       "outbox_1",
		TenantID:       "tenant_1",
		Provider:       "array",
		ExternalTaskID: "ext_1",
		CoreTaskID:     "task_1",
		EventType:      "external_delivery",
		Channel:        "message",
		Payload:        map[string]any{"message": "hello replay"},
		IdempotencyKey: "idem_replay",
		Status:         "failed",
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := bridge.ReplayDelivery(context.Background(), "tenant_1", item.OutboxID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != "delivered" || len(bridge.Messages) != 1 || bridge.Messages[0].Message != "hello replay" {
		t.Fatalf("expected replayed delivery, item=%#v messages=%#v", replayed, bridge.Messages)
	}
}

func TestBridgeReplayDueDeliveriesMarksDeadLetterAtMaxAttempts(t *testing.T) {
	store := newMemoryOutboxStore()
	bridge := NewBridgeWithStore(store)
	now := time.Now().Add(-time.Minute)
	item, err := store.EnqueueDelivery(context.Background(), contracts.ExternalDeliveryOutboxItem{
		OutboxID:       "outbox_maxed",
		TenantID:       "tenant_1",
		Provider:       "array",
		ExternalTaskID: "ext_1",
		CoreTaskID:     "task_1",
		EventType:      "external_delivery",
		Channel:        "message",
		Payload:        map[string]any{"message": "hello"},
		IdempotencyKey: "idem_maxed",
		Status:         "failed",
		AttemptCount:   3,
		LastError:      "remote still down",
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := bridge.ReplayDueDeliveriesWithOptions(context.Background(), contracts.ExternalDeliveryReplayOptions{
		TenantID:    "tenant_1",
		Statuses:    []string{"failed"},
		Limit:       10,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].OutboxID != item.OutboxID || items[0].Status != "dead_letter" || items[0].AttemptCount != 3 {
		t.Fatalf("expected dead letter without extra attempt, got %#v", items)
	}
	if len(bridge.Messages) != 0 {
		t.Fatalf("dead letter should not be replayed, messages=%#v", bridge.Messages)
	}
}

func TestDeliveryRetryWorkerReplaysDueDeliveries(t *testing.T) {
	store := newMemoryOutboxStore()
	bridge := NewBridgeWithStore(store)
	now := time.Now().Add(-time.Minute)
	item, err := store.EnqueueDelivery(context.Background(), contracts.ExternalDeliveryOutboxItem{
		OutboxID:       "outbox_due",
		TenantID:       "tenant_1",
		Provider:       "array",
		ExternalTaskID: "ext_1",
		CoreTaskID:     "task_1",
		EventType:      "external_delivery",
		Channel:        "message",
		Payload:        map[string]any{"message": "hello worker"},
		IdempotencyKey: "idem_worker",
		Status:         "failed",
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := DeliveryRetryWorker{
		Bridge:      bridge,
		BatchSize:   10,
		MaxAttempts: 5,
	}
	items, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].OutboxID != item.OutboxID || items[0].Status != "delivered" {
		t.Fatalf("expected worker to deliver due item, got %#v", items)
	}
	if len(bridge.Messages) != 1 || bridge.Messages[0].Message != "hello worker" {
		t.Fatalf("expected replayed message, got %#v", bridge.Messages)
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
