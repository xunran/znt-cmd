package repository

import (
	"context"
	"strings"
	"sync"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type Repository interface {
	SaveCall(ctx context.Context, call contracts.ToolCall) (contracts.ToolCall, bool, error)
	GetCall(ctx context.Context, callID contracts.ToolCallID) (contracts.ToolCall, bool, error)
	SaveResult(ctx context.Context, result contracts.ToolResult) error
	GetResultByCall(ctx context.Context, callID contracts.ToolCallID) (contracts.ToolResult, bool, error)
	GetResultByIdempotencyKey(ctx context.Context, tenantID contracts.TenantID, key string) (contracts.ToolResult, bool, error)
	ListCallsByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.ToolCall, error)
	ListResultsByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.ToolResult, error)
}

type InMemoryRepository struct {
	mu             sync.RWMutex
	calls          map[contracts.ToolCallID]contracts.ToolCall
	resultsByCall  map[contracts.ToolCallID]contracts.ToolResult
	idempotencyMap map[string]contracts.ToolCallID
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		calls:          map[contracts.ToolCallID]contracts.ToolCall{},
		resultsByCall:  map[contracts.ToolCallID]contracts.ToolResult{},
		idempotencyMap: map[string]contracts.ToolCallID{},
	}
}

func (r *InMemoryRepository) SaveCall(_ context.Context, call contracts.ToolCall) (contracts.ToolCall, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if call.IdempotencyKey != "" {
		key := idempotencyKey(call.TenantID, call.IdempotencyKey)
		if existingID, ok := r.idempotencyMap[key]; ok {
			return r.calls[existingID], true, nil
		}
	}
	if _, ok := r.calls[call.ToolCallID]; ok {
		return contracts.ToolCall{}, false, storagerepo.ErrDuplicateRequest
	}
	r.calls[call.ToolCallID] = call
	if call.IdempotencyKey != "" {
		r.idempotencyMap[idempotencyKey(call.TenantID, call.IdempotencyKey)] = call.ToolCallID
	}
	return call, false, nil
}

func (r *InMemoryRepository) GetCall(_ context.Context, callID contracts.ToolCallID) (contracts.ToolCall, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	call, ok := r.calls[callID]
	return call, ok, nil
}

func (r *InMemoryRepository) SaveResult(_ context.Context, result contracts.ToolResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resultsByCall[result.ToolCallID] = result
	return nil
}

func (r *InMemoryRepository) GetResultByCall(_ context.Context, callID contracts.ToolCallID) (contracts.ToolResult, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result, ok := r.resultsByCall[callID]
	return result, ok, nil
}

func (r *InMemoryRepository) GetResultByIdempotencyKey(_ context.Context, tenantID contracts.TenantID, key string) (contracts.ToolResult, bool, error) {
	if key == "" {
		return contracts.ToolResult{}, false, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	callID, ok := r.idempotencyMap[idempotencyKey(tenantID, key)]
	if !ok {
		return contracts.ToolResult{}, false, nil
	}
	result, ok := r.resultsByCall[callID]
	return result, ok, nil
}

func (r *InMemoryRepository) ListCallsByRun(_ context.Context, runID contracts.AgentRunID) ([]contracts.ToolCall, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolCall, 0)
	for _, call := range r.calls {
		if call.RunID == runID {
			out = append(out, call)
		}
	}
	return out, nil
}

func (r *InMemoryRepository) ListResultsByRun(_ context.Context, runID contracts.AgentRunID) ([]contracts.ToolResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolResult, 0)
	for callID, result := range r.resultsByCall {
		if r.calls[callID].RunID == runID {
			out = append(out, result)
		}
	}
	return out, nil
}

func idempotencyKey(tenantID contracts.TenantID, key string) string {
	return strings.Join([]string{string(tenantID), key}, "\x00")
}
