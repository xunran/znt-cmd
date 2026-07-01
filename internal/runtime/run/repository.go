package run

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type ListFilter struct {
	TenantID contracts.TenantID
	AgentID  contracts.AgentID
	Status   contracts.RunStatus
	TraceID  contracts.TraceID
	TaskID   contracts.TaskID
	Query    string
	From     time.Time
	To       time.Time
	Limit    int
	Offset   int
}

type Repository interface {
	Create(ctx context.Context, run contracts.AgentRun) error
	Get(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	List(ctx context.Context, filter ListFilter) ([]contracts.AgentRun, error)
	Count(ctx context.Context, filter ListFilter) (int, error)
	MarkRunning(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	MarkCompleted(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	MarkFailed(ctx context.Context, runID contracts.AgentRunID, runtimeErr *contracts.RuntimeError) (contracts.AgentRun, error)
	MarkWaitingInput(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	MarkWaitingApproval(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	MarkCancelled(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
	UpdateVersionSnapshot(ctx context.Context, runID contracts.AgentRunID, snapshot contracts.VersionSnapshot) (contracts.AgentRun, error)
	IncrementStep(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, string, error)
	IncrementToolCall(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error)
}

type InMemoryRepository struct {
	mu   sync.RWMutex
	runs map[contracts.AgentRunID]contracts.AgentRun
	now  func() time.Time
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		runs: map[contracts.AgentRunID]contracts.AgentRun{},
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (r *InMemoryRepository) Create(_ context.Context, run contracts.AgentRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runs[run.RunID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	contracts.NormalizeRunCarrierSnapshot(&run)
	r.runs[run.RunID] = run
	return nil
}

func (r *InMemoryRepository) Get(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[runID]
	if !ok {
		return contracts.AgentRun{}, storagerepo.ErrNotFound
	}
	return run, nil
}

func (r *InMemoryRepository) List(_ context.Context, filter ListFilter) ([]contracts.AgentRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.AgentRun, 0, len(r.runs))
	for _, run := range r.runs {
		if runMatchesListFilter(run, filter) {
			out = append(out, run)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].RunID > out[j].RunID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return pageRuns(out, filter.Limit, filter.Offset), nil
}

func (r *InMemoryRepository) Count(_ context.Context, filter ListFilter) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, run := range r.runs {
		if runMatchesListFilter(run, filter) {
			total++
		}
	}
	return total, nil
}

func runMatchesListFilter(run contracts.AgentRun, filter ListFilter) bool {
	if filter.TenantID != "" && run.TenantID != filter.TenantID {
		return false
	}
	if filter.AgentID != "" && run.AgentID != filter.AgentID {
		return false
	}
	if filter.Status != "" && run.Status != filter.Status {
		return false
	}
	if filter.TraceID != "" && run.TraceID != filter.TraceID {
		return false
	}
	if filter.TaskID != "" && run.TaskID != filter.TaskID {
		return false
	}
	if !filter.From.IsZero() && run.StartedAt.Before(filter.From) {
		return false
	}
	if !filter.To.IsZero() && run.StartedAt.After(filter.To) {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query == "" {
		return true
	}
	values := []string{
		string(run.RunID),
		string(run.TraceID),
		string(run.TaskID),
		string(run.AgentID),
		string(run.Status),
		run.Input,
		string(run.AgentVersion),
		string(run.PolicySetID),
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func (r *InMemoryRepository) MarkRunning(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunRunning
	})
}

func (r *InMemoryRepository) MarkCompleted(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	now := r.now()
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunCompleted
		run.CompletedAt = &now
	})
}

func (r *InMemoryRepository) MarkFailed(_ context.Context, runID contracts.AgentRunID, runtimeErr *contracts.RuntimeError) (contracts.AgentRun, error) {
	now := r.now()
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunFailed
		run.CompletedAt = &now
		if runtimeErr != nil {
			run.ErrorCode = runtimeErr.Code
			run.ErrorMessage = runtimeErr.Message
		}
	})
}

func (r *InMemoryRepository) MarkWaitingInput(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunWaitingInput
	})
}

func (r *InMemoryRepository) MarkWaitingApproval(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunWaitingApproval
	})
}

func (r *InMemoryRepository) MarkCancelled(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	now := r.now()
	return r.update(runID, func(run *contracts.AgentRun) {
		run.Status = contracts.RunCancelled
		run.CompletedAt = &now
	})
}

func (r *InMemoryRepository) UpdateVersionSnapshot(_ context.Context, runID contracts.AgentRunID, snapshot contracts.VersionSnapshot) (contracts.AgentRun, error) {
	return r.update(runID, func(run *contracts.AgentRun) {
		run.VersionSnapshot = snapshot
	})
}

func (r *InMemoryRepository) IncrementStep(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, string, error) {
	var stepID string
	run, err := r.update(runID, func(run *contracts.AgentRun) {
		run.StepCount++
		stepID = "step_" + string(run.RunID) + "_" + itoa(run.StepCount)
	})
	return run, stepID, err
}

func (r *InMemoryRepository) IncrementToolCall(_ context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.update(runID, func(run *contracts.AgentRun) {
		run.ToolCallCount++
	})
}

func (r *InMemoryRepository) update(runID contracts.AgentRunID, fn func(*contracts.AgentRun)) (contracts.AgentRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok {
		return contracts.AgentRun{}, storagerepo.ErrNotFound
	}
	fn(&run)
	contracts.NormalizeRunCarrierSnapshot(&run)
	r.runs[runID] = run
	return run, nil
}

func pageRuns(runs []contracts.AgentRun, limit int, offset int) []contracts.AgentRun {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(runs)
	}
	if offset >= len(runs) {
		return []contracts.AgentRun{}
	}
	end := offset + limit
	if end > len(runs) {
		end = len(runs)
	}
	return append([]contracts.AgentRun(nil), runs[offset:end]...)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
