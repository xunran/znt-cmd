package audit

import (
	"context"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/pkg/idgen"
)

type Logger interface {
	Log(ctx context.Context, event contracts.AuditEvent) error
	Search(ctx context.Context, filter Filter) ([]contracts.AuditEvent, error)
}

type Filter struct {
	TenantID     contracts.TenantID
	Action       string
	ResourceID   string
	ResourceType string
	RunID        contracts.AgentRunID
	TaskID       contracts.TaskID
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
		if filter.TenantID != "" && event.TenantID != filter.TenantID {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		if filter.ResourceID != "" && event.ResourceID != filter.ResourceID {
			continue
		}
		if filter.ResourceType != "" && event.ResourceType != filter.ResourceType {
			continue
		}
		if filter.RunID != "" && event.RunID != filter.RunID {
			continue
		}
		if filter.TaskID != "" && event.TaskID != filter.TaskID {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}
