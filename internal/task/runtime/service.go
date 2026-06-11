package runtime

import (
	"context"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	taskrepo "znt/internal/task/repository"
	"znt/internal/task/state"
	"znt/pkg/idgen"
)

type Service struct {
	tasks  taskrepo.TaskRepository
	events taskrepo.EventRepository
	Audit  audit.Logger
	now    func() time.Time
}

type CommandInput struct {
	TaskID    contracts.TaskID
	Command   contracts.TaskCommand
	ActorID   string
	ActorType string
	Payload   map[string]any
	RunID     contracts.AgentRunID
	StepID    string
}

func NewService(tasks taskrepo.TaskRepository, events taskrepo.EventRepository) *Service {
	return &Service{
		tasks:  tasks,
		events: events,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateTask(ctx context.Context, task contracts.Task, actorID string, actorType string) (contracts.Task, error) {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = s.now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.Status == "" {
		task.Status = contracts.TaskCreated
	}
	event := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    task.TaskID,
		TenantID:  task.TenantID,
		Type:      "task.created",
		ActorID:   actorID,
		ActorType: actorType,
		Payload:   map[string]any{"status": task.Status},
		CreatedAt: s.now(),
	}
	if atomic, ok := s.tasks.(taskrepo.AtomicRepository); ok {
		if err := atomic.CreateTaskAndAppendEvent(ctx, task, event); err != nil {
			return contracts.Task{}, err
		}
		return task, nil
	}
	if err := s.tasks.Create(ctx, task); err != nil {
		return contracts.Task{}, err
	}
	if err := s.events.Append(ctx, event); err != nil {
		return contracts.Task{}, err
	}
	return task, nil
}

func (s *Service) ApplyCommand(ctx context.Context, input CommandInput) (contracts.Task, contracts.TaskEvent, state.Transition, error) {
	task, err := s.tasks.Get(ctx, input.TaskID)
	if err != nil {
		return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
	}
	transition, err := state.Apply(task.Status, input.Command)
	if err != nil {
		return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
	}
	updated := task
	updated.Status = transition.To
	now := s.now()
	updated.UpdatedAt = now
	if state.IsTerminal(updated.Status) {
		updated.CompletedAt = &now
	}
	event := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    task.TaskID,
		TenantID:  task.TenantID,
		Type:      transition.EventType,
		ActorID:   input.ActorID,
		ActorType: input.ActorType,
		Payload: map[string]any{
			"from_status": task.Status,
			"to_status":   transition.To,
			"command":     input.Command,
		},
		RunID:     input.RunID,
		StepID:    input.StepID,
		CreatedAt: now,
	}
	for key, value := range input.Payload {
		event.Payload[key] = value
	}
	if atomic, ok := s.tasks.(taskrepo.AtomicRepository); ok {
		if err := atomic.UpdateWithVersionAndAppendEvent(ctx, updated, task.Version, event); err != nil {
			return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
		}
	} else {
		if err := s.tasks.UpdateWithVersion(ctx, updated, task.Version); err != nil {
			return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
		}
		if err := s.events.Append(ctx, event); err != nil {
			return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
		}
	}
	updated, err = s.tasks.Get(ctx, task.TaskID)
	if err != nil {
		return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
	}
	if transition.Audit {
		if err := s.recordTransitionAudit(ctx, updated, event, input, transition); err != nil {
			return contracts.Task{}, contracts.TaskEvent{}, state.Transition{}, err
		}
	}
	return updated, event, transition, nil
}

func (s *Service) Events(ctx context.Context, taskID contracts.TaskID) ([]contracts.TaskEvent, error) {
	return s.events.ListByTask(ctx, taskID)
}

func (s *Service) AppendEvent(ctx context.Context, event contracts.TaskEvent) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now()
	}
	return s.events.Append(ctx, event)
}

func (s *Service) recordTransitionAudit(ctx context.Context, task contracts.Task, event contracts.TaskEvent, input CommandInput, transition state.Transition) error {
	if s.Audit == nil {
		return nil
	}
	return s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     task.TenantID,
		ActorID:      input.ActorID,
		ActorType:    input.ActorType,
		Action:       transition.EventType,
		ResourceType: "task",
		ResourceID:   string(task.TaskID),
		Decision:     "allowed",
		Reason:       string(transition.Command),
		TaskID:       task.TaskID,
		RunID:        input.RunID,
		CreatedAt:    event.CreatedAt,
	})
}
