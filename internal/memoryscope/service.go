package memoryscope

import (
	"context"
	"fmt"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type Service interface {
	SaveScope(ctx context.Context, scope contracts.MemoryScope) (contracts.MemoryScope, error)
	GetScope(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryScope, bool, error)
	CanRead(ctx context.Context, input AccessInput) (bool, string, error)
	CanWrite(ctx context.Context, input AccessInput) (bool, string, error)
	GrantShare(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID, targetGroupID contracts.GroupID, actorID string) (contracts.MemoryScope, error)
}

type Store interface {
	SaveScope(ctx context.Context, scope contracts.MemoryScope) error
	GetScope(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryScope, bool, error)
}

type AccessInput struct {
	TenantID contracts.TenantID
	GroupID  contracts.GroupID
	MemoryID contracts.MemoryID
	Roles    []string
	TraceID  contracts.TraceID
	TaskID   contracts.TaskID
	RunID    contracts.AgentRunID
}

type InMemoryService struct {
	mu    sync.RWMutex
	items map[string]contracts.MemoryScope
	store Store
	audit audit.Logger
	trace trace.Recorder
	now   func() time.Time
}

func NewInMemoryService(auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return NewInMemoryServiceWithStore(nil, auditLogger, traceRecorder)
}

func NewInMemoryServiceWithStore(store Store, auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return &InMemoryService{
		items: map[string]contracts.MemoryScope{},
		store: store,
		audit: auditLogger,
		trace: traceRecorder,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryService) SaveScope(ctx context.Context, scope contracts.MemoryScope) (contracts.MemoryScope, error) {
	if scope.TenantID == "" || scope.MemoryID == "" {
		return contracts.MemoryScope{}, fmt.Errorf("tenant_id and memory_id are required")
	}
	if scope.Visibility == "" {
		scope.Visibility = contracts.VisibilityPrivate
	}
	if scope.ScopeType == "" {
		scope.ScopeType = contracts.MemoryScopeGroup
	}
	now := s.now()
	if scope.CreatedAt.IsZero() {
		scope.CreatedAt = now
	}
	scope.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key(scope.TenantID, scope.MemoryID)] = cloneScope(scope)
	if s.store != nil {
		if err := s.store.SaveScope(ctx, scope); err != nil {
			return contracts.MemoryScope{}, err
		}
	}
	return cloneScope(scope), nil
}

func (s *InMemoryService) GetScope(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryScope, bool, error) {
	if s.store != nil {
		scope, ok, err := s.store.GetScope(ctx, tenantID, memoryID)
		if err != nil || ok {
			return scope, ok, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	scope, ok := s.items[key(tenantID, memoryID)]
	return cloneScope(scope), ok, nil
}

func (s *InMemoryService) CanRead(ctx context.Context, input AccessInput) (bool, string, error) {
	return s.check(ctx, input, true)
}

func (s *InMemoryService) CanWrite(ctx context.Context, input AccessInput) (bool, string, error) {
	return s.check(ctx, input, false)
}

func (s *InMemoryService) GrantShare(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID, targetGroupID contracts.GroupID, actorID string) (contracts.MemoryScope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, ok := s.items[key(tenantID, memoryID)]
	if !ok && s.store != nil {
		stored, storedOK, err := s.store.GetScope(ctx, tenantID, memoryID)
		if err != nil {
			return contracts.MemoryScope{}, err
		}
		if storedOK {
			scope, ok = stored, true
		}
	}
	if !ok {
		return contracts.MemoryScope{}, fmt.Errorf("memory scope %s not found", memoryID)
	}
	if !containsGroup(scope.SharedWithGroupIDs, targetGroupID) {
		scope.SharedWithGroupIDs = append(scope.SharedWithGroupIDs, targetGroupID)
	}
	scope.Visibility = contracts.VisibilitySharedGroups
	scope.ScopeType = contracts.MemoryScopeShared
	scope.UpdatedAt = s.now()
	s.items[key(tenantID, memoryID)] = cloneScope(scope)
	if s.store != nil {
		if err := s.store.SaveScope(ctx, scope); err != nil {
			return contracts.MemoryScope{}, err
		}
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     tenantID,
			ActorID:      actorID,
			ActorType:    "member",
			Action:       contracts.AuditMemoryShared,
			ResourceType: "memory",
			ResourceID:   string(memoryID),
			Decision:     "allowed",
			Reason:       "shared_with_group=" + string(targetGroupID),
			CreatedAt:    s.now(),
		})
	}
	return cloneScope(scope), nil
}

func (s *InMemoryService) check(ctx context.Context, input AccessInput, read bool) (bool, string, error) {
	scope, ok, err := s.GetScope(ctx, input.TenantID, input.MemoryID)
	if err != nil || !ok {
		return false, "memory scope not found", err
	}
	allowed, reason := allows(scope, input.GroupID, input.Roles, read)
	if s.trace != nil && input.TraceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TraceMemoryScopeChecked,
			Payload: map[string]any{
				"memory_id":  input.MemoryID,
				"group_id":   input.GroupID,
				"operation":  map[bool]string{true: "read", false: "write"}[read],
				"allowed":    allowed,
				"visibility": scope.Visibility,
				"reason":     reason,
			},
			CreatedAt: s.now(),
		})
	}
	return allowed, reason, nil
}

func allows(scope contracts.MemoryScope, groupID contracts.GroupID, roles []string, read bool) (bool, string) {
	roleGate := scope.ReadRoles
	if !read {
		roleGate = scope.WriteRoles
	}
	if len(roleGate) > 0 && !intersects(roleGate, roles) {
		return false, "role not allowed"
	}
	switch scope.Visibility {
	case contracts.VisibilityTenant:
		return true, "tenant visibility"
	case contracts.VisibilityGroup, contracts.VisibilityPrivate:
		if scope.OwnerGroupID == groupID || scope.ScopeID == string(groupID) {
			return true, "same group"
		}
	case contracts.VisibilitySharedGroups, contracts.VisibilityShared:
		if scope.OwnerGroupID == groupID || scope.ScopeID == string(groupID) || containsGroup(scope.SharedWithGroupIDs, groupID) {
			return true, "shared group"
		}
	}
	return false, "scope denied"
}

func key(tenantID contracts.TenantID, memoryID contracts.MemoryID) string {
	return string(tenantID) + "\x00" + string(memoryID)
}

func intersects(a []string, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

func containsGroup(values []contracts.GroupID, target contracts.GroupID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneScope(scope contracts.MemoryScope) contracts.MemoryScope {
	scope.SharedWithGroupIDs = append([]contracts.GroupID(nil), scope.SharedWithGroupIDs...)
	scope.ReadRoles = append([]string(nil), scope.ReadRoles...)
	scope.WriteRoles = append([]string(nil), scope.WriteRoles...)
	return scope
}
