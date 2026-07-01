package audit

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/pkg/idgen"
)

type Logger interface {
	Log(ctx context.Context, event contracts.AuditEvent) error
	Search(ctx context.Context, filter Filter) ([]contracts.AuditEvent, error)
	Count(ctx context.Context, filter Filter) (int, error)
}

type Filter struct {
	TenantID     contracts.TenantID
	Action       string
	ResourceID   string
	ResourceType string
	TraceID      contracts.TraceID
	RunID        contracts.AgentRunID
	TaskID       contracts.TaskID
	ActorID      string
	Decision     string
	Query        string
	From         time.Time
	To           time.Time
	Limit        int
	Offset       int
	Desc         bool
}

type InMemoryLogger struct {
	mu     sync.RWMutex
	events []contracts.AuditEvent
}

func NewInMemoryLogger() *InMemoryLogger {
	return &InMemoryLogger{}
}

func (l *InMemoryLogger) Log(_ context.Context, event contracts.AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if event.AuditID == "" {
		event.AuditID = idgen.New("audit")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	l.events = append(l.events, event)
	return nil
}

func (l *InMemoryLogger) Search(_ context.Context, filter Filter) ([]contracts.AuditEvent, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]contracts.AuditEvent, 0)
	for _, event := range l.events {
		if auditEventMatchesFilter(event, filter) {
			out = append(out, event)
		}
	}
	if filter.Desc {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].CreatedAt.Equal(out[j].CreatedAt) {
				return out[i].AuditID > out[j].AuditID
			}
			return out[i].CreatedAt.After(out[j].CreatedAt)
		})
	}
	return pageAuditEvents(out, filter.Limit, filter.Offset), nil
}

func (l *InMemoryLogger) Count(_ context.Context, filter Filter) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	total := 0
	for _, event := range l.events {
		if auditEventMatchesFilter(event, filter) {
			total++
		}
	}
	return total, nil
}

func auditEventMatchesFilter(event contracts.AuditEvent, filter Filter) bool {
	if filter.TenantID != "" && event.TenantID != filter.TenantID {
		return false
	}
	if filter.Action != "" && event.Action != filter.Action {
		return false
	}
	if filter.ResourceID != "" && event.ResourceID != filter.ResourceID {
		return false
	}
	if filter.ResourceType != "" && event.ResourceType != filter.ResourceType {
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
	if filter.ActorID != "" && event.ActorID != filter.ActorID {
		return false
	}
	if filter.Decision != "" && event.Decision != filter.Decision {
		return false
	}
	if !filter.From.IsZero() && event.CreatedAt.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && event.CreatedAt.After(filter.To) {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query == "" {
		return true
	}
	values := []string{
		event.AuditID,
		string(event.TenantID),
		event.ActorID,
		event.ActorType,
		event.Action,
		event.ResourceType,
		event.ResourceID,
		event.Decision,
		event.Reason,
		string(event.TraceID),
		string(event.TaskID),
		string(event.RunID),
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func pageAuditEvents(events []contracts.AuditEvent, limit int, offset int) []contracts.AuditEvent {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(events)
	}
	if offset >= len(events) {
		return []contracts.AuditEvent{}
	}
	end := offset + limit
	if end > len(events) {
		end = len(events)
	}
	return append([]contracts.AuditEvent(nil), events[offset:end]...)
}
