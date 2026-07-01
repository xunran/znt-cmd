package trace

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
)

type ListFilter struct {
	TenantID contracts.TenantID
	TraceID  contracts.TraceID
	RunID    contracts.AgentRunID
	TaskID   contracts.TaskID
	Type     string
	Query    string
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

type SummaryFilter struct {
	TenantID contracts.TenantID
	TraceID  contracts.TraceID
	RunID    contracts.AgentRunID
	TaskID   contracts.TaskID
	Type     string
	Status   contracts.RunStatus
	Query    string
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

type Summary struct {
	TraceID         contracts.TraceID    `json:"trace_id"`
	TenantID        contracts.TenantID   `json:"tenant_id,omitempty"`
	PrimaryRunID    contracts.AgentRunID `json:"run_id,omitempty"`
	TaskID          contracts.TaskID     `json:"task_id,omitempty"`
	AgentID         contracts.AgentID    `json:"agent_id,omitempty"`
	Status          contracts.RunStatus  `json:"status,omitempty"`
	EventCount      int                  `json:"event_count"`
	FirstAt         time.Time            `json:"first_at"`
	LastAt          time.Time            `json:"last_at"`
	LatestEventType string               `json:"latest_event_type,omitempty"`
}

type Recorder interface {
	Record(ctx context.Context, event contracts.TraceEvent) error
	ListByTrace(ctx context.Context, traceID contracts.TraceID) ([]contracts.TraceEvent, error)
	ListByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.TraceEvent, error)
	ListByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TraceEvent, error)
	List(ctx context.Context, filter ListFilter) ([]contracts.TraceEvent, error)
	ListSummaries(ctx context.Context, filter SummaryFilter) ([]Summary, error)
	CountSummaries(ctx context.Context, filter SummaryFilter) (int, error)
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
		return traceEventMatchesListFilter(event, filter)
	}, filter.Limit, filter.Offset), nil
}

func (r *InMemoryRecorder) ListSummaries(_ context.Context, filter SummaryFilter) ([]Summary, error) {
	return pageTraceSummaries(r.summaryList(filter), filter.Limit, filter.Offset), nil
}

func (r *InMemoryRecorder) CountSummaries(_ context.Context, filter SummaryFilter) (int, error) {
	return len(r.summaryList(filter)), nil
}

func (r *InMemoryRecorder) summaryList(filter SummaryFilter) []Summary {
	r.mu.RLock()
	defer r.mu.RUnlock()
	grouped := map[contracts.TraceID]*Summary{}
	for _, event := range r.events {
		if !traceEventMatchesSummaryFilter(event, filter) {
			continue
		}
		summary := grouped[event.TraceID]
		if summary == nil {
			summary = &Summary{
				TraceID:    event.TraceID,
				TenantID:   event.TenantID,
				FirstAt:    event.CreatedAt.UTC(),
				LastAt:     event.CreatedAt.UTC(),
				Status:     contracts.RunCompleted,
				EventCount: 0,
			}
			grouped[event.TraceID] = summary
		}
		summary.EventCount++
		if summary.PrimaryRunID == "" && event.RunID != "" {
			summary.PrimaryRunID = event.RunID
		}
		if summary.TaskID == "" && event.TaskID != "" {
			summary.TaskID = event.TaskID
		}
		if event.CreatedAt.Before(summary.FirstAt) {
			summary.FirstAt = event.CreatedAt.UTC()
		}
		if event.CreatedAt.After(summary.LastAt) || event.CreatedAt.Equal(summary.LastAt) {
			summary.LastAt = event.CreatedAt.UTC()
			summary.LatestEventType = event.Type
		}
		summary.Status = combineTraceSummaryStatus(summary.Status, event.Type)
	}
	out := make([]Summary, 0, len(grouped))
	for _, summary := range grouped {
		if filter.Status != "" && summary.Status != filter.Status {
			continue
		}
		out = append(out, *summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastAt.Equal(out[j].LastAt) {
			return out[i].TraceID > out[j].TraceID
		}
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out
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

func traceEventMatchesListFilter(event contracts.TraceEvent, filter ListFilter) bool {
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
	if filter.From != nil && event.CreatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && event.CreatedAt.After(*filter.To) {
		return false
	}
	return traceEventMatchesQuery(event, filter.Query)
}

func traceEventMatchesSummaryFilter(event contracts.TraceEvent, filter SummaryFilter) bool {
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
	if filter.From != nil && event.CreatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && event.CreatedAt.After(*filter.To) {
		return false
	}
	return traceEventMatchesQuery(event, filter.Query)
}

func traceEventMatchesQuery(event contracts.TraceEvent, rawQuery string) bool {
	query := strings.ToLower(strings.TrimSpace(rawQuery))
	if query == "" {
		return true
	}
	values := []string{
		string(event.TraceID),
		string(event.SpanID),
		string(event.RunID),
		string(event.TaskID),
		event.Type,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func combineTraceSummaryStatus(current contracts.RunStatus, eventType string) contracts.RunStatus {
	lowerType := strings.ToLower(eventType)
	if strings.Contains(lowerType, "failed") || strings.Contains(lowerType, "denied") {
		return contracts.RunFailed
	}
	if current == contracts.RunFailed {
		return current
	}
	if strings.Contains(lowerType, "pending_approval") || eventType == contracts.TraceApprovalRequested {
		return contracts.RunWaitingApproval
	}
	if current == contracts.RunWaitingApproval {
		return current
	}
	if eventType == contracts.TraceRunCreated {
		return contracts.RunRunning
	}
	return current
}

func pageTraceSummaries(summaries []Summary, limit int, offset int) []Summary {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(summaries)
	}
	if offset >= len(summaries) {
		return []Summary{}
	}
	end := offset + limit
	if end > len(summaries) {
		end = len(summaries)
	}
	return append([]Summary(nil), summaries[offset:end]...)
}
