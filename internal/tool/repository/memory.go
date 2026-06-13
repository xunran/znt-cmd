package repository

import (
	"context"
	"sort"
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
	ListResultsByRunLimit(ctx context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ToolResult, error)
	ListArtifactRefsByRunLimit(ctx context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ArtifactRef, error)
	ListCallsByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolCall, error)
	ListResultsByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolResult, error)
	ListResultsByTaskLimit(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, limit int) ([]contracts.ToolResult, error)
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
	return r.listResultsByRun(runID, 0), nil
}

func (r *InMemoryRepository) ListResultsByRunLimit(_ context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ToolResult, error) {
	return r.listResultsByRun(runID, limit), nil
}

func (r *InMemoryRepository) listResultsByRun(runID contracts.AgentRunID, limit int) []contracts.ToolResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolResult, 0)
	for callID, result := range r.resultsByCall {
		if r.calls[callID].RunID == runID {
			out = append(out, result)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CompletedAt.Equal(out[j].CompletedAt) {
			return out[i].ToolResultID < out[j].ToolResultID
		}
		return out[i].CompletedAt.Before(out[j].CompletedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (r *InMemoryRepository) ListArtifactRefsByRunLimit(_ context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ArtifactRef, error) {
	results := r.listResultsByRun(runID, 0)
	seen := map[contracts.ArtifactID]struct{}{}
	out := make([]contracts.ArtifactRef, 0)
	for i := len(results) - 1; i >= 0; i-- {
		for j := len(results[i].ArtifactRefs) - 1; j >= 0; j-- {
			ref := results[i].ArtifactRefs[j]
			if ref.ArtifactID == "" {
				continue
			}
			if _, ok := seen[ref.ArtifactID]; ok {
				continue
			}
			seen[ref.ArtifactID] = struct{}{}
			out = append(out, ref)
			if limit > 0 && len(out) >= limit {
				return reverseArtifactRefs(out), nil
			}
		}
	}
	return reverseArtifactRefs(out), nil
}

func (r *InMemoryRepository) ListCallsByTask(_ context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolCall, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolCall, 0)
	for _, call := range r.calls {
		if tenantID != "" && call.TenantID != tenantID {
			continue
		}
		if call.TaskID == taskID {
			out = append(out, call)
		}
	}
	return out, nil
}

func (r *InMemoryRepository) ListResultsByTask(_ context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolResult, error) {
	return r.listResultsByTask(tenantID, taskID, 0), nil
}

func (r *InMemoryRepository) ListResultsByTaskLimit(_ context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, limit int) ([]contracts.ToolResult, error) {
	return r.listResultsByTask(tenantID, taskID, limit), nil
}

func (r *InMemoryRepository) listResultsByTask(tenantID contracts.TenantID, taskID contracts.TaskID, limit int) []contracts.ToolResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolResult, 0)
	for callID, result := range r.resultsByCall {
		call := r.calls[callID]
		if tenantID != "" && call.TenantID != tenantID {
			continue
		}
		if call.TaskID == taskID {
			out = append(out, result)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CompletedAt.Equal(out[j].CompletedAt) {
			return out[i].ToolResultID < out[j].ToolResultID
		}
		return out[i].CompletedAt.Before(out[j].CompletedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func idempotencyKey(tenantID contracts.TenantID, key string) string {
	return strings.Join([]string{string(tenantID), key}, "\x00")
}

func reverseArtifactRefs(refs []contracts.ArtifactRef) []contracts.ArtifactRef {
	for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
		refs[i], refs[j] = refs[j], refs[i]
	}
	return refs
}
