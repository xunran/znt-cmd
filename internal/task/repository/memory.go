package repository

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type TaskRepository interface {
	Create(ctx context.Context, task contracts.Task) error
	Get(ctx context.Context, taskID contracts.TaskID) (contracts.Task, error)
	UpdateWithVersion(ctx context.Context, task contracts.Task, expectedVersion int64) error
	ListByTenantStatus(ctx context.Context, tenantID contracts.TenantID, status contracts.TaskStatus) ([]contracts.Task, error)
}

type EventRepository interface {
	Append(ctx context.Context, event contracts.TaskEvent) error
	ListByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TaskEvent, error)
}

type AtomicRepository interface {
	CreateTaskAndAppendEvent(ctx context.Context, task contracts.Task, event contracts.TaskEvent) error
	UpdateWithVersionAndAppendEvent(ctx context.Context, task contracts.Task, expectedVersion int64, event contracts.TaskEvent) error
}

type InMemoryStore struct {
	mu     sync.RWMutex
	tasks  map[contracts.TaskID]contracts.Task
	events map[contracts.TaskID][]contracts.TaskEvent
	ids    map[contracts.TaskEventID]struct{}
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		tasks:  map[contracts.TaskID]contracts.Task{},
		events: map[contracts.TaskID][]contracts.TaskEvent{},
		ids:    map[contracts.TaskEventID]struct{}{},
	}
}

func (r *InMemoryStore) Create(_ context.Context, task contracts.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[task.TaskID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.tasks[task.TaskID] = task
	return nil
}

func (r *InMemoryStore) CreateTaskAndAppendEvent(_ context.Context, task contracts.Task, event contracts.TaskEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[task.TaskID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	if _, ok := r.ids[event.EventID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.tasks[task.TaskID] = task
	r.appendEventLocked(event)
	return nil
}

func (r *InMemoryStore) Get(_ context.Context, taskID contracts.TaskID) (contracts.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return contracts.Task{}, storagerepo.ErrNotFound
	}
	return task, nil
}

func (r *InMemoryStore) UpdateWithVersion(_ context.Context, task contracts.Task, expectedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updateTaskLocked(task, expectedVersion)
}

func (r *InMemoryStore) UpdateWithVersionAndAppendEvent(_ context.Context, task contracts.Task, expectedVersion int64, event contracts.TaskEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ids[event.EventID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	if err := r.updateTaskLocked(task, expectedVersion); err != nil {
		return err
	}
	r.appendEventLocked(event)
	return nil
}

func (r *InMemoryStore) ListByTenantStatus(_ context.Context, tenantID contracts.TenantID, status contracts.TaskStatus) ([]contracts.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.Task, 0)
	for _, task := range r.tasks {
		if task.TenantID == tenantID && task.Status == status {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *InMemoryStore) Append(_ context.Context, event contracts.TaskEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ids[event.EventID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.appendEventLocked(event)
	return nil
}

func (r *InMemoryStore) ListByTask(_ context.Context, taskID contracts.TaskID) ([]contracts.TaskEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := r.events[taskID]
	out := make([]contracts.TaskEvent, len(events))
	copy(out, events)
	return out, nil
}

func (r *InMemoryStore) updateTaskLocked(task contracts.Task, expectedVersion int64) error {
	existing, ok := r.tasks[task.TaskID]
	if !ok {
		return storagerepo.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return storagerepo.ErrConflict
	}
	task.Version = expectedVersion + 1
	task.UpdatedAt = task.UpdatedAt.UTC()
	r.tasks[task.TaskID] = task
	return nil
}

func (r *InMemoryStore) appendEventLocked(event contracts.TaskEvent) {
	r.ids[event.EventID] = struct{}{}
	r.events[event.TaskID] = append(r.events[event.TaskID], event)
	sort.SliceStable(r.events[event.TaskID], func(i, j int) bool {
		return eventLess(r.events[event.TaskID][i], r.events[event.TaskID][j])
	})
}

type InMemoryTaskRepository struct {
	mu    sync.RWMutex
	tasks map[contracts.TaskID]contracts.Task
}

func NewInMemoryTaskRepository() *InMemoryTaskRepository {
	return &InMemoryTaskRepository{tasks: map[contracts.TaskID]contracts.Task{}}
}

func (r *InMemoryTaskRepository) Create(_ context.Context, task contracts.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[task.TaskID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.tasks[task.TaskID] = task
	return nil
}

func (r *InMemoryTaskRepository) Get(_ context.Context, taskID contracts.TaskID) (contracts.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return contracts.Task{}, storagerepo.ErrNotFound
	}
	return task, nil
}

func (r *InMemoryTaskRepository) UpdateWithVersion(_ context.Context, task contracts.Task, expectedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.tasks[task.TaskID]
	if !ok {
		return storagerepo.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return storagerepo.ErrConflict
	}
	task.Version = expectedVersion + 1
	task.UpdatedAt = task.UpdatedAt.UTC()
	r.tasks[task.TaskID] = task
	return nil
}

func (r *InMemoryTaskRepository) ListByTenantStatus(_ context.Context, tenantID contracts.TenantID, status contracts.TaskStatus) ([]contracts.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.Task, 0)
	for _, task := range r.tasks {
		if task.TenantID == tenantID && task.Status == status {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

type InMemoryEventRepository struct {
	mu     sync.RWMutex
	events map[contracts.TaskID][]contracts.TaskEvent
	ids    map[contracts.TaskEventID]struct{}
}

func NewInMemoryEventRepository() *InMemoryEventRepository {
	return &InMemoryEventRepository{
		events: map[contracts.TaskID][]contracts.TaskEvent{},
		ids:    map[contracts.TaskEventID]struct{}{},
	}
}

func (r *InMemoryEventRepository) Append(_ context.Context, event contracts.TaskEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ids[event.EventID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.ids[event.EventID] = struct{}{}
	r.events[event.TaskID] = append(r.events[event.TaskID], event)
	sort.SliceStable(r.events[event.TaskID], func(i, j int) bool {
		return eventLess(r.events[event.TaskID][i], r.events[event.TaskID][j])
	})
	return nil
}

func (r *InMemoryEventRepository) ListByTask(_ context.Context, taskID contracts.TaskID) ([]contracts.TaskEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := r.events[taskID]
	out := make([]contracts.TaskEvent, len(events))
	copy(out, events)
	return out, nil
}

func eventLess(a, b contracts.TaskEvent) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.EventID < b.EventID
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

func IsNotFound(err error) bool {
	return errors.Is(err, storagerepo.ErrNotFound)
}

func NewTask(taskID contracts.TaskID, tenantID contracts.TenantID, agentID contracts.AgentID, agentVersion contracts.AgentVersion, policySetID contracts.PolicySetID, title string, objective string, now time.Time) contracts.Task {
	return contracts.Task{
		TaskID:        taskID,
		TenantID:      tenantID,
		Title:         title,
		Objective:     objective,
		Status:        contracts.TaskCreated,
		AgentID:       agentID,
		AgentVersion:  agentVersion,
		PolicySetID:   policySetID,
		SchemaVersion: "v1.0-alpha",
		Version:       0,
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
	}
}
