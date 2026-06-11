package artifact

import (
	"context"
	"fmt"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
)

type MemoryStore interface {
	WriteMemory(ctx context.Context, event contracts.MemoryEvent, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error)
	WriteMemoryWithPolicy(ctx context.Context, event contracts.MemoryEvent, policy contracts.MemoryPolicy, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error)
	GetMemory(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryEvent, error)
	ListMemory(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID) ([]contracts.MemorySummary, error)
}

type InMemoryMemoryStore struct {
	mu     sync.RWMutex
	events map[contracts.MemoryID]contracts.MemoryEvent
	audit  audit.Logger
	now    func() time.Time
}

func NewInMemoryMemoryStore(auditLogger audit.Logger) *InMemoryMemoryStore {
	return &InMemoryMemoryStore{
		events: map[contracts.MemoryID]contracts.MemoryEvent{},
		audit:  auditLogger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryMemoryStore) WriteMemory(ctx context.Context, event contracts.MemoryEvent, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error) {
	return s.WriteMemoryWithPolicy(ctx, event, contracts.MemoryPolicy{AllowWrite: true, AllowRead: true}, actorID, actorType, traceID)
}

func (s *InMemoryMemoryStore) WriteMemoryWithPolicy(ctx context.Context, event contracts.MemoryEvent, policy contracts.MemoryPolicy, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error) {
	if event.TenantID == "" {
		return contracts.MemoryEvent{}, fmt.Errorf("memory tenant_id is required")
	}
	if !policy.AllowWrite {
		return contracts.MemoryEvent{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "memory write is denied by policy", nil)
	}
	if len(policy.Scopes) > 0 && !scopeAllowed(policy.Scopes, event.Scope) {
		return contracts.MemoryEvent{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "memory scope is denied by policy", map[string]any{"scope": event.Scope})
	}
	if event.MemoryID == "" {
		event.MemoryID = contracts.MemoryID(idgen.New("memory"))
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now()
	}
	s.mu.Lock()
	if _, ok := s.events[event.MemoryID]; ok {
		s.mu.Unlock()
		return contracts.MemoryEvent{}, storagerepo.ErrDuplicateRequest
	}
	s.events[event.MemoryID] = event
	s.mu.Unlock()
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			TenantID:     event.TenantID,
			ActorID:      actorID,
			ActorType:    actorType,
			Action:       contracts.AuditMemoryWrite,
			ResourceType: "memory",
			ResourceID:   string(event.MemoryID),
			Decision:     "allowed",
			TraceID:      traceID,
			CreatedAt:    s.now(),
		})
	}
	return event, nil
}

func (s *InMemoryMemoryStore) GetMemory(_ context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	event, ok := s.events[memoryID]
	if !ok || event.TenantID != tenantID {
		return contracts.MemoryEvent{}, storagerepo.ErrNotFound
	}
	return event, nil
}

func scopeAllowed(allowed []string, scope string) bool {
	for _, current := range allowed {
		if current == scope {
			return true
		}
	}
	return false
}

func (s *InMemoryMemoryStore) ListMemory(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID) ([]contracts.MemorySummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.MemorySummary, 0)
	for _, event := range s.events {
		if event.TenantID != tenantID {
			continue
		}
		if agentID != "" && event.AgentID != agentID {
			continue
		}
		if userID != "" && event.UserID != userID {
			continue
		}
		out = append(out, contracts.MemorySummary{MemoryID: event.MemoryID, Summary: event.Summary})
	}
	return out, nil
}
