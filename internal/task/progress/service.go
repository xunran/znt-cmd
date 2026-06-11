package progress

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	runrepo "znt/internal/runtime/run"
	taskhandoff "znt/internal/task/handoff"
	taskrepo "znt/internal/task/repository"
	"znt/pkg/idgen"
)

type Store interface {
	SaveBinding(ctx context.Context, binding contracts.GroupTaskBinding) (contracts.GroupTaskBinding, error)
	FindRecentByGroup(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, limit int) ([]contracts.GroupTaskBinding, error)
	FindByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (contracts.GroupTaskBinding, bool, error)
}

type InMemoryStore struct {
	mu       sync.RWMutex
	bindings map[contracts.GroupTaskBindingID]contracts.GroupTaskBinding
	now      func() time.Time
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		bindings: map[contracts.GroupTaskBindingID]contracts.GroupTaskBinding{},
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryStore) SaveBinding(_ context.Context, binding contracts.GroupTaskBinding) (contracts.GroupTaskBinding, error) {
	if binding.TenantID == "" || binding.GroupID == "" || binding.TaskID == "" {
		return contracts.GroupTaskBinding{}, fmt.Errorf("tenant_id, group_id and task_id are required")
	}
	if binding.BindingID == "" {
		binding.BindingID = contracts.GroupTaskBindingID(idgen.New("gtbind"))
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[binding.BindingID] = binding
	return binding, nil
}

func (s *InMemoryStore) FindRecentByGroup(_ context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, limit int) ([]contracts.GroupTaskBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GroupTaskBinding, 0)
	for _, binding := range s.bindings {
		if binding.TenantID == tenantID && binding.GroupID == groupID {
			out = append(out, binding)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit <= 0 {
		limit = 10
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) FindByTask(_ context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (contracts.GroupTaskBinding, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, binding := range s.bindings {
		if binding.TenantID == tenantID && binding.TaskID == taskID {
			return binding, true, nil
		}
	}
	return contracts.GroupTaskBinding{}, false, nil
}

type Service struct {
	Store    Store
	Tasks    taskrepo.TaskRepository
	Runs     runrepo.Repository
	Handoffs *taskhandoff.Service
	Audit    audit.Logger
	Trace    trace.Recorder
	Now      func() time.Time
}

type QueryInput struct {
	TenantID contracts.TenantID
	GroupID  contracts.GroupID
	TaskID   contracts.TaskID
	Query    string
	ActorID  string
	TraceID  contracts.TraceID
	RunID    contracts.AgentRunID
}

func NewService(store Store, tasks taskrepo.TaskRepository, runs runrepo.Repository, handoffs *taskhandoff.Service, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	if store == nil {
		store = NewInMemoryStore()
	}
	return &Service{
		Store:    store,
		Tasks:    tasks,
		Runs:     runs,
		Handoffs: handoffs,
		Audit:    auditLogger,
		Trace:    traceRecorder,
		Now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) SaveBinding(ctx context.Context, binding contracts.GroupTaskBinding) (contracts.GroupTaskBinding, error) {
	return s.Store.SaveBinding(ctx, binding)
}

func (s *Service) Query(ctx context.Context, input QueryInput) (contracts.TaskProgressSummary, error) {
	if input.TenantID == "" || input.GroupID == "" {
		return contracts.TaskProgressSummary{}, fmt.Errorf("tenant_id and group_id are required")
	}
	var binding contracts.GroupTaskBinding
	var ok bool
	var err error
	if input.TaskID != "" {
		binding, ok, err = s.Store.FindByTask(ctx, input.TenantID, input.TaskID)
	} else {
		var bindings []contracts.GroupTaskBinding
		bindings, err = s.Store.FindRecentByGroup(ctx, input.TenantID, input.GroupID, 10)
		if err == nil {
			binding, ok = chooseBinding(bindings, input.Query)
		}
	}
	if err != nil {
		return contracts.TaskProgressSummary{}, err
	}
	if !ok {
		summary := contracts.TaskProgressSummary{
			TenantID:  input.TenantID,
			GroupID:   input.GroupID,
			Summary:   "没有找到这个群里可关联的专业智能体任务进度。",
			UpdatedAt: s.Now(),
		}
		s.record(ctx, input, summary)
		return summary, nil
	}
	summary := contracts.TaskProgressSummary{
		TenantID:  binding.TenantID,
		GroupID:   binding.GroupID,
		TaskID:    binding.TaskID,
		RunID:     binding.RunID,
		HandoffID: binding.HandoffID,
		AgentID:   binding.AgentID,
		Objective: binding.Objective,
		UpdatedAt: s.Now(),
	}
	if s.Tasks != nil {
		task, err := s.Tasks.Get(ctx, binding.TaskID)
		if err == nil {
			summary.TaskStatus = task.Status
			summary.UpdatedAt = task.UpdatedAt
		}
	}
	if s.Runs != nil && binding.RunID != "" {
		run, err := s.Runs.Get(ctx, binding.RunID)
		if err == nil {
			summary.RunStatus = run.Status
			if run.CompletedAt != nil {
				summary.UpdatedAt = *run.CompletedAt
			}
		}
	}
	if s.Handoffs != nil && binding.HandoffID != "" {
		handoff, ok := s.Handoffs.Get(binding.HandoffID)
		if ok {
			summary.HandoffStatus = handoff.Status
			if handoff.CompletedAt != nil {
				summary.UpdatedAt = *handoff.CompletedAt
			}
		}
	}
	summary.Summary = renderSummary(summary)
	s.record(ctx, input, summary)
	return summary, nil
}

func chooseBinding(bindings []contracts.GroupTaskBinding, query string) (contracts.GroupTaskBinding, bool) {
	if len(bindings) == 0 {
		return contracts.GroupTaskBinding{}, false
	}
	query = strings.ToLower(query)
	if query != "" {
		for _, binding := range bindings {
			if strings.Contains(strings.ToLower(binding.Objective), query) || strings.Contains(query, strings.ToLower(binding.Objective)) {
				return binding, true
			}
		}
	}
	return bindings[0], true
}

func renderSummary(summary contracts.TaskProgressSummary) string {
	if summary.TaskID == "" {
		return "没有找到这个群里可关联的专业智能体任务进度。"
	}
	status := string(summary.TaskStatus)
	if status == "" {
		status = "unknown"
	}
	parts := []string{fmt.Sprintf("任务 %s 当前状态是 %s", summary.TaskID, status)}
	if summary.AgentID != "" {
		parts = append(parts, "负责智能体 "+string(summary.AgentID))
	}
	if summary.RunStatus != "" {
		parts = append(parts, "运行状态 "+string(summary.RunStatus))
	}
	if summary.HandoffStatus != "" {
		parts = append(parts, "交接状态 "+string(summary.HandoffStatus))
	}
	if summary.Objective != "" {
		parts = append(parts, "目标："+summary.Objective)
	}
	return strings.Join(parts, "；") + "。"
}

func (s *Service) record(ctx context.Context, input QueryInput, summary contracts.TaskProgressSummary) {
	if s.Trace != nil && input.TraceID != "" {
		_ = s.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   summary.TaskID,
			Type:     contracts.TraceAgentProgressQueried,
			Payload: map[string]any{
				"group_id":       input.GroupID,
				"task_id":        summary.TaskID,
				"agent_id":       summary.AgentID,
				"task_status":    summary.TaskStatus,
				"run_status":     summary.RunStatus,
				"handoff_status": summary.HandoffStatus,
			},
			CreatedAt: s.Now(),
		})
	}
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.ActorID,
			ActorType:    "member",
			Action:       contracts.AuditAgentProgressQueried,
			ResourceType: "task",
			ResourceID:   string(summary.TaskID),
			Decision:     "allowed",
			TraceID:      input.TraceID,
			TaskID:       summary.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.Now(),
		})
	}
}
