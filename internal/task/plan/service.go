package plan

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
)

type StepInput struct {
	Title             string   `json:"title"`
	Description       string   `json:"description,omitempty"`
	ExpectedToolHints []string `json:"expected_tool_hints,omitempty"`
}

type Snapshot struct {
	ActivePlan  *contracts.TaskPlan   `json:"active_plan,omitempty"`
	CurrentStep *contracts.PlanStep   `json:"current_step,omitempty"`
	Plans       []contracts.TaskPlan  `json:"plans"`
	Steps       []contracts.PlanStep  `json:"steps"`
	Events      []contracts.PlanEvent `json:"events"`
}

type Reader interface {
	ActivePlan(ctx context.Context, taskID contracts.TaskID) (contracts.TaskPlan, bool, error)
	CurrentStep(ctx context.Context, taskID contracts.TaskID) (contracts.PlanStep, bool, error)
}

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreatePlan(ctx context.Context, task contracts.Task, objective string, steps []StepInput, actorID string, actorType string) (contracts.TaskPlan, []contracts.PlanStep, contracts.PlanEvent, error) {
	return s.createPlan(ctx, task, objective, steps, actorID, actorType, "plan.created", nil)
}

func (s *Service) createPlan(ctx context.Context, task contracts.Task, objective string, steps []StepInput, actorID string, actorType string, eventType string, payload map[string]any) (contracts.TaskPlan, []contracts.PlanStep, contracts.PlanEvent, error) {
	if objective == "" {
		objective = task.Objective
	}
	if len(steps) == 0 {
		return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, fmt.Errorf("create plan requires at least one step")
	}
	now := s.now()
	plan := contracts.TaskPlan{
		PlanID:    idgen.New("plan"),
		TaskID:    task.TaskID,
		Objective: objective,
		Status:    contracts.PlanRunning,
		CreatedBy: actorID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	planSteps := make([]contracts.PlanStep, 0, len(steps))
	for i, input := range steps {
		title := input.Title
		if title == "" {
			title = fmt.Sprintf("Step %d", i+1)
		}
		planSteps = append(planSteps, contracts.PlanStep{
			StepID:            idgen.New("planstep"),
			PlanID:            plan.PlanID,
			TaskID:            task.TaskID,
			Index:             i + 1,
			Title:             title,
			Description:       input.Description,
			ExpectedToolHints: input.ExpectedToolHints,
			Status:            contracts.PlanStepPending,
			CreatedAt:         now,
			UpdatedAt:         now,
		})
	}
	if err := s.repo.CreatePlan(ctx, plan, planSteps); err != nil {
		return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["step_count"] = len(planSteps)
	event := s.event(plan, eventType, actorID, actorType, payload)
	if err := s.repo.AppendEvent(ctx, event); err != nil {
		return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
	}
	return plan, planSteps, event, nil
}

func (s *Service) Replan(ctx context.Context, task contracts.Task, objective string, steps []StepInput, actorID string, actorType string, reason string) (contracts.TaskPlan, []contracts.PlanStep, contracts.PlanEvent, error) {
	active, ok, err := s.repo.ActivePlan(ctx, task.TaskID)
	if err != nil {
		return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
	}
	if ok {
		active.Status = contracts.PlanSuperseded
		active.UpdatedAt = s.now()
		if err := s.repo.UpdatePlan(ctx, active); err != nil {
			return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
		}
		if err := s.repo.AppendEvent(ctx, s.event(active, "plan.superseded", actorID, actorType, map[string]any{"reason": reason})); err != nil {
			return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
		}
	}
	payload := map[string]any{"reason": reason}
	if ok {
		payload["superseded_plan_id"] = active.PlanID
	}
	plan, planSteps, event, err := s.createPlan(ctx, task, objective, steps, actorID, actorType, "plan.replanned", payload)
	if err != nil {
		return contracts.TaskPlan{}, nil, contracts.PlanEvent{}, err
	}
	return plan, planSteps, event, nil
}

func (s *Service) ActivePlan(ctx context.Context, taskID contracts.TaskID) (contracts.TaskPlan, bool, error) {
	return s.repo.ActivePlan(ctx, taskID)
}

func (s *Service) CurrentStep(ctx context.Context, taskID contracts.TaskID) (contracts.PlanStep, bool, error) {
	plan, ok, err := s.repo.ActivePlan(ctx, taskID)
	if err != nil || !ok {
		return contracts.PlanStep{}, false, err
	}
	steps, err := s.repo.ListStepsByPlan(ctx, plan.PlanID)
	if err != nil {
		return contracts.PlanStep{}, false, err
	}
	for _, step := range steps {
		if step.Status == contracts.PlanStepRunning {
			return step, true, nil
		}
	}
	for _, step := range steps {
		if step.Status == contracts.PlanStepPending {
			return step, true, nil
		}
	}
	return contracts.PlanStep{}, false, nil
}

func (s *Service) StartStep(ctx context.Context, taskID contracts.TaskID, stepID string, actorID string, actorType string) (contracts.PlanStep, contracts.PlanEvent, error) {
	step, err := s.repo.GetStep(ctx, stepID)
	if err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	if step.TaskID != taskID {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("plan step %s does not belong to task %s", stepID, taskID)
	}
	if step.Status != contracts.PlanStepPending {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("cannot start plan step %s from status %s", stepID, step.Status)
	}
	if err := s.ensurePreviousStepsDone(ctx, step); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	step.Status = contracts.PlanStepRunning
	step.UpdatedAt = s.now()
	if err := s.repo.UpdateStep(ctx, step); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	event := s.stepEvent(step, "plan_step.running", actorID, actorType, nil)
	if err := s.repo.AppendEvent(ctx, event); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	return step, event, nil
}

func (s *Service) CompleteStep(ctx context.Context, taskID contracts.TaskID, stepID string, resultRefs []contracts.ArtifactRef, toolResultID contracts.ToolResultID, actorID string, actorType string) (contracts.PlanStep, contracts.PlanEvent, error) {
	step, err := s.repo.GetStep(ctx, stepID)
	if err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	if step.TaskID != taskID {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("plan step %s does not belong to task %s", stepID, taskID)
	}
	if step.Status != contracts.PlanStepRunning {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("cannot complete plan step %s from status %s", stepID, step.Status)
	}
	step.Status = contracts.PlanStepCompleted
	step.ResultRefs = resultRefs
	step.UpdatedAt = s.now()
	if err := s.repo.UpdateStep(ctx, step); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	event := s.stepEvent(step, "plan_step.completed", actorID, actorType, map[string]any{"tool_result_id": toolResultID, "result_refs": resultRefs})
	if err := s.repo.AppendEvent(ctx, event); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	if err := s.completePlanIfDone(ctx, step.PlanID, actorID, actorType); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	return step, event, nil
}

func (s *Service) FailStep(ctx context.Context, taskID contracts.TaskID, stepID string, reason string, actorID string, actorType string) (contracts.PlanStep, contracts.PlanEvent, error) {
	step, err := s.repo.GetStep(ctx, stepID)
	if err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	if step.TaskID != taskID {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("plan step %s does not belong to task %s", stepID, taskID)
	}
	if step.Status != contracts.PlanStepRunning {
		return contracts.PlanStep{}, contracts.PlanEvent{}, fmt.Errorf("cannot fail plan step %s from status %s", stepID, step.Status)
	}
	step.Status = contracts.PlanStepFailed
	step.FailureReason = reason
	step.UpdatedAt = s.now()
	if err := s.repo.UpdateStep(ctx, step); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	event := s.stepEvent(step, "plan_step.failed", actorID, actorType, map[string]any{"reason": reason})
	if err := s.repo.AppendEvent(ctx, event); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	plan, err := s.repo.GetPlan(ctx, step.PlanID)
	if err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	plan.Status = contracts.PlanFailed
	plan.UpdatedAt = s.now()
	if err := s.repo.UpdatePlan(ctx, plan); err != nil {
		return contracts.PlanStep{}, contracts.PlanEvent{}, err
	}
	return step, event, nil
}

func (s *Service) Snapshot(ctx context.Context, taskID contracts.TaskID) (Snapshot, error) {
	plans, err := s.repo.ListPlansByTask(ctx, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	events, err := s.repo.ListEventsByTask(ctx, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Plans: plans, Events: events}
	if active, ok, err := s.repo.ActivePlan(ctx, taskID); err != nil {
		return Snapshot{}, err
	} else if ok {
		snapshot.ActivePlan = &active
		steps, err := s.repo.ListStepsByPlan(ctx, active.PlanID)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Steps = steps
		if step, ok, err := s.CurrentStep(ctx, taskID); err != nil {
			return Snapshot{}, err
		} else if ok {
			snapshot.CurrentStep = &step
		}
	}
	return snapshot, nil
}

func (s *Service) ensurePreviousStepsDone(ctx context.Context, step contracts.PlanStep) error {
	steps, err := s.repo.ListStepsByPlan(ctx, step.PlanID)
	if err != nil {
		return err
	}
	for _, previous := range steps {
		if previous.Index >= step.Index {
			continue
		}
		if previous.Status != contracts.PlanStepCompleted && previous.Status != contracts.PlanStepSkipped {
			return fmt.Errorf("cannot start plan step %s before step %s is finished", step.StepID, previous.StepID)
		}
	}
	return nil
}

func (s *Service) completePlanIfDone(ctx context.Context, planID string, actorID string, actorType string) error {
	steps, err := s.repo.ListStepsByPlan(ctx, planID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.Status != contracts.PlanStepCompleted && step.Status != contracts.PlanStepSkipped {
			return nil
		}
	}
	plan, err := s.repo.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	plan.Status = contracts.PlanCompleted
	plan.UpdatedAt = s.now()
	if err := s.repo.UpdatePlan(ctx, plan); err != nil {
		return err
	}
	return s.repo.AppendEvent(ctx, s.event(plan, "plan.completed", actorID, actorType, map[string]any{"step_count": len(steps)}))
}

func (s *Service) event(plan contracts.TaskPlan, eventType string, actorID string, actorType string, payload map[string]any) contracts.PlanEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	return contracts.PlanEvent{
		EventID:   idgen.New("planevt"),
		PlanID:    plan.PlanID,
		TaskID:    plan.TaskID,
		Type:      eventType,
		ActorID:   actorID,
		ActorType: actorType,
		Payload:   payload,
		CreatedAt: s.now(),
	}
}

func (s *Service) stepEvent(step contracts.PlanStep, eventType string, actorID string, actorType string, payload map[string]any) contracts.PlanEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["step_id"] = step.StepID
	payload["step_index"] = step.Index
	return contracts.PlanEvent{
		EventID:   idgen.New("planevt"),
		PlanID:    step.PlanID,
		TaskID:    step.TaskID,
		Type:      eventType,
		ActorID:   actorID,
		ActorType: actorType,
		Payload:   payload,
		CreatedAt: s.now(),
	}
}

type Repository interface {
	CreatePlan(ctx context.Context, plan contracts.TaskPlan, steps []contracts.PlanStep) error
	UpdatePlan(ctx context.Context, plan contracts.TaskPlan) error
	GetPlan(ctx context.Context, planID string) (contracts.TaskPlan, error)
	ActivePlan(ctx context.Context, taskID contracts.TaskID) (contracts.TaskPlan, bool, error)
	ListPlansByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TaskPlan, error)
	GetStep(ctx context.Context, stepID string) (contracts.PlanStep, error)
	UpdateStep(ctx context.Context, step contracts.PlanStep) error
	ListStepsByPlan(ctx context.Context, planID string) ([]contracts.PlanStep, error)
	AppendEvent(ctx context.Context, event contracts.PlanEvent) error
	ListEventsByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.PlanEvent, error)
}

type InMemoryRepository struct {
	mu           sync.RWMutex
	plans        map[string]contracts.TaskPlan
	plansByTask  map[contracts.TaskID][]string
	steps        map[string]contracts.PlanStep
	stepsByPlan  map[string][]string
	events       map[string]contracts.PlanEvent
	eventsByTask map[contracts.TaskID][]string
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		plans:        map[string]contracts.TaskPlan{},
		plansByTask:  map[contracts.TaskID][]string{},
		steps:        map[string]contracts.PlanStep{},
		stepsByPlan:  map[string][]string{},
		events:       map[string]contracts.PlanEvent{},
		eventsByTask: map[contracts.TaskID][]string{},
	}
}

func (r *InMemoryRepository) CreatePlan(_ context.Context, plan contracts.TaskPlan, steps []contracts.PlanStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plans[plan.PlanID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.plans[plan.PlanID] = plan
	r.plansByTask[plan.TaskID] = append(r.plansByTask[plan.TaskID], plan.PlanID)
	for _, step := range steps {
		if _, ok := r.steps[step.StepID]; ok {
			return storagerepo.ErrDuplicateRequest
		}
		r.steps[step.StepID] = step
		r.stepsByPlan[plan.PlanID] = append(r.stepsByPlan[plan.PlanID], step.StepID)
	}
	return nil
}

func (r *InMemoryRepository) UpdatePlan(_ context.Context, plan contracts.TaskPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plans[plan.PlanID]; !ok {
		return storagerepo.ErrNotFound
	}
	r.plans[plan.PlanID] = plan
	return nil
}

func (r *InMemoryRepository) GetPlan(_ context.Context, planID string) (contracts.TaskPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plan, ok := r.plans[planID]
	if !ok {
		return contracts.TaskPlan{}, storagerepo.ErrNotFound
	}
	return plan, nil
}

func (r *InMemoryRepository) ActivePlan(_ context.Context, taskID contracts.TaskID) (contracts.TaskPlan, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := len(r.plansByTask[taskID]) - 1; i >= 0; i-- {
		plan := r.plans[r.plansByTask[taskID][i]]
		if plan.Status == contracts.PlanRunning || plan.Status == contracts.PlanPending {
			return plan, true, nil
		}
	}
	return contracts.TaskPlan{}, false, nil
}

func (r *InMemoryRepository) ListPlansByTask(_ context.Context, taskID contracts.TaskID) ([]contracts.TaskPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.TaskPlan, 0, len(r.plansByTask[taskID]))
	for _, id := range r.plansByTask[taskID] {
		out = append(out, r.plans[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *InMemoryRepository) GetStep(_ context.Context, stepID string) (contracts.PlanStep, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	step, ok := r.steps[stepID]
	if !ok {
		return contracts.PlanStep{}, storagerepo.ErrNotFound
	}
	return step, nil
}

func (r *InMemoryRepository) UpdateStep(_ context.Context, step contracts.PlanStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.steps[step.StepID]; !ok {
		return storagerepo.ErrNotFound
	}
	r.steps[step.StepID] = step
	return nil
}

func (r *InMemoryRepository) ListStepsByPlan(_ context.Context, planID string) ([]contracts.PlanStep, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.PlanStep, 0, len(r.stepsByPlan[planID]))
	for _, id := range r.stepsByPlan[planID] {
		out = append(out, r.steps[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Index < out[j].Index
	})
	return out, nil
}

func (r *InMemoryRepository) AppendEvent(_ context.Context, event contracts.PlanEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.events[event.EventID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.events[event.EventID] = event
	r.eventsByTask[event.TaskID] = append(r.eventsByTask[event.TaskID], event.EventID)
	return nil
}

func (r *InMemoryRepository) ListEventsByTask(_ context.Context, taskID contracts.TaskID) ([]contracts.PlanEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.PlanEvent, 0, len(r.eventsByTask[taskID]))
	for _, id := range r.eventsByTask[taskID] {
		out = append(out, r.events[id])
	}
	return out, nil
}
