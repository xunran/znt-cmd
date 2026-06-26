package agentdelegation

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

const (
	StatusInvoked   = "invoked"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type Delegation struct {
	DelegationID    string
	TenantID        contracts.TenantID
	TraceID         contracts.TraceID
	ParentRunID     contracts.AgentRunID
	ParentTaskID    contracts.TaskID
	ToolCallID      contracts.ToolCallID
	ToolID          string
	Operation       string
	ProviderAgentID contracts.AgentID
	ChildRunID      contracts.AgentRunID
	ChildTaskID     contracts.TaskID
	Status          string
	ResultStatus    string
	ResultSummary   string
	ErrorSummary    string
	Metadata        map[string]any
	StartedAt       *time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Repository interface {
	Upsert(ctx context.Context, delegation Delegation) error
	ListByParentRun(ctx context.Context, tenantID contracts.TenantID, parentRunID contracts.AgentRunID) ([]Delegation, error)
	ListByTrace(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]Delegation, error)
}

type InMemoryRepository struct {
	mu    sync.RWMutex
	items map[string]Delegation
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{items: map[string]Delegation{}}
}

func (r *InMemoryRepository) Upsert(_ context.Context, delegation Delegation) error {
	if delegation.TenantID == "" || strings.TrimSpace(delegation.DelegationID) == "" || strings.TrimSpace(string(delegation.ToolCallID)) == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent delegation requires tenant_id, delegation_id and tool_call_id", nil)
	}
	now := time.Now().UTC()
	if delegation.UpdatedAt.IsZero() {
		delegation.UpdatedAt = now
	}
	key := delegationKey(delegation.TenantID, delegation.ToolCallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.items[key]; ok {
		if delegation.CreatedAt.IsZero() {
			delegation.CreatedAt = existing.CreatedAt
		}
		if delegation.DelegationID == "" {
			delegation.DelegationID = existing.DelegationID
		}
	} else if delegation.CreatedAt.IsZero() {
		delegation.CreatedAt = now
	}
	r.items[key] = cloneDelegation(delegation)
	return nil
}

func (r *InMemoryRepository) ListByParentRun(_ context.Context, tenantID contracts.TenantID, parentRunID contracts.AgentRunID) ([]Delegation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Delegation, 0)
	for _, item := range r.items {
		if item.TenantID == tenantID && item.ParentRunID == parentRunID {
			out = append(out, cloneDelegation(item))
		}
	}
	sortDelegations(out)
	return out, nil
}

func (r *InMemoryRepository) ListByTrace(_ context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]Delegation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Delegation, 0)
	for _, item := range r.items {
		if item.TenantID == tenantID && item.TraceID == traceID {
			out = append(out, cloneDelegation(item))
		}
	}
	sortDelegations(out)
	return out, nil
}

func delegationKey(tenantID contracts.TenantID, toolCallID contracts.ToolCallID) string {
	return strings.Join([]string{string(tenantID), string(toolCallID)}, "\x00")
}

func sortDelegations(items []Delegation) {
	sort.SliceStable(items, func(i, j int) bool {
		left := delegationSortTime(items[i])
		right := delegationSortTime(items[j])
		if left.Equal(right) {
			return items[i].DelegationID < items[j].DelegationID
		}
		return left.Before(right)
	})
}

func delegationSortTime(item Delegation) time.Time {
	if item.StartedAt != nil && !item.StartedAt.IsZero() {
		return *item.StartedAt
	}
	if !item.CreatedAt.IsZero() {
		return item.CreatedAt
	}
	return item.UpdatedAt
}

func cloneDelegation(item Delegation) Delegation {
	if item.Metadata != nil {
		metadata := make(map[string]any, len(item.Metadata))
		for key, value := range item.Metadata {
			metadata[key] = value
		}
		item.Metadata = metadata
	}
	if item.StartedAt != nil {
		startedAt := item.StartedAt.UTC()
		item.StartedAt = &startedAt
	}
	if item.CompletedAt != nil {
		completedAt := item.CompletedAt.UTC()
		item.CompletedAt = &completedAt
	}
	return item
}

func IsNotFound(err error) bool {
	return err == storagerepo.ErrNotFound
}
