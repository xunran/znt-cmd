package trace

import (
	"context"
	"sort"
	"sync"

	"znt/internal/contracts"
)

type ListFilter struct {
	TenantID contracts.TenantID
	TraceID  contracts.TraceID
	RunID    contracts.AgentRunID
	TaskID   contracts.TaskID
	Type     string
	Limit    int
	Offset   int
}

type Recorder interface {
	Record(ctx context.Context, event contracts.TraceEvent) error
	ListByTrace(ctx context.Context, traceID contracts.TraceID) ([]contracts.TraceEvent, error)
	ListByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.TraceEvent, error)
	ListByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TraceEvent, error)
	List(ctx context.Context, filter ListFilter) ([]contracts.TraceEvent, error)
}

type InMemoryRecorder struct {
	mu     sync.RWMutex
	events []contracts.TraceEvent
}

func NewInMemoryRecorder() *InMemoryRecorder {
	return &InMemoryRecorder{}
}

func (r *InMemoryRecorder) Record(_ context.Context, event contracts.TraceEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *InMemoryRecorder) ListByTrace(_ context.Context, traceID contracts.TraceID) ([]contracts.TraceEvent, error) {
	return r.filter(func(event contracts.TraceEvent) bool {
		return event.TraceID == traceID
	}), nil
}

func (r *InMemoryRecorder) ListByRun(_ context.Context, runID contracts.AgentRunID) ([]contracts.TraceEvent, error) {
	return r.filter(func(event contracts.TraceEvent) bool {
		return event.RunID == runID
	}), nil
}

func (r *InMemoryRecorder) ListByTask(_ context.Context, taskID contracts.TaskID) ([]contracts.TraceEvent, error) {
	return r.filter(func(event contracts.TraceEvent) bool {
		return event.TaskID == taskID
	}), nil
}

func (r *InMemoryRecorder) List(_ context.Context, filter ListFilter) ([]contracts.TraceEvent, error) {
	return r.filterPage(func(event contracts.TraceEvent) bool {
		if filter.TenantID != "" && event.TenantID != filter.TenantID {
			return false
		}
		if filter.TraceID != "" && event.TraceID != filter.TraceID {
			return false
		}
		if filter.RunID != "" && event.RunID != filter.RunID {
			return false
		}
		if filter.TaskID != "" && event.TaskID != filter.TaskID {
			return false
		}
		if filter.Type != "" && event.Type != filter.Type {
			return false
		}
		return true
	}, filter.Limit, filter.Offset), nil
}

func (r *InMemoryRecorder) filter(match func(contracts.TraceEvent) bool) []contracts.TraceEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.TraceEvent, 0)
	for _, event := range r.events {
		if match(event) {
			out = append(out, event)
		}
	}
	return out
}

func (r *InMemoryRecorder) filterPage(match func(contracts.TraceEvent) bool, limit int, offset int) []contracts.TraceEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.TraceEvent, 0)
	for _, event := range r.events {
		if match(event) {
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].SpanID < out[j].SpanID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(out)
	}
	if offset >= len(out) {
		return []contracts.TraceEvent{}
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return append([]contracts.TraceEvent(nil), out[offset:end]...)
}
