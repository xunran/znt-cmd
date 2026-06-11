package eval

import (
	"context"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/pkg/idgen"
)

type Gates struct {
	RequireCriticalPass bool    `json:"require_critical_pass"`
	RequireSafetyPass   bool    `json:"require_safety_pass"`
	MinPassRate         float64 `json:"min_pass_rate,omitempty"`
	MaxToolMisuseRate   float64 `json:"max_tool_misuse_rate,omitempty"`
}

type Suite struct {
	SuiteID   contracts.EvalSuiteID `json:"suite_id"`
	TenantID  contracts.TenantID    `json:"tenant_id"`
	Name      string                `json:"name"`
	Cases     []Case                `json:"cases"`
	Gates     Gates                 `json:"gates"`
	CreatedBy string                `json:"created_by"`
	CreatedAt time.Time             `json:"created_at"`
}

type SuiteResult struct {
	EvalRunID      contracts.EvalRunID   `json:"eval_run_id"`
	SuiteID        contracts.EvalSuiteID `json:"suite_id"`
	TenantID       contracts.TenantID    `json:"tenant_id"`
	Passed         bool                  `json:"passed"`
	PassRate       float64               `json:"pass_rate"`
	ToolMisuseRate float64               `json:"tool_misuse_rate"`
	Failures       []string              `json:"failures,omitempty"`
	Results        []Result              `json:"results"`
	CreatedAt      time.Time             `json:"created_at"`
}

type Store struct {
	mu      sync.RWMutex
	suites  map[contracts.EvalSuiteID]Suite
	results map[contracts.EvalRunID]SuiteResult
	repo    Repository
	now     func() time.Time
}

type Repository interface {
	SaveSuite(ctx context.Context, suite Suite) error
	GetSuite(ctx context.Context, suiteID contracts.EvalSuiteID) (Suite, bool, error)
	SaveResult(ctx context.Context, result SuiteResult) error
	GetResult(ctx context.Context, evalRunID contracts.EvalRunID) (SuiteResult, bool, error)
}

func NewStore() *Store {
	return NewStoreWithRepository(nil)
}

func NewStoreWithRepository(repo Repository) *Store {
	return &Store{
		suites:  map[contracts.EvalSuiteID]Suite{},
		results: map[contracts.EvalRunID]SuiteResult{},
		repo:    repo,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (s *Store) CreateSuite(ctx context.Context, tenantID contracts.TenantID, name string, gates Gates, actorID string) (Suite, error) {
	if gates.MinPassRate == 0 {
		gates.MinPassRate = 1
	}
	suite := Suite{
		SuiteID:   contracts.EvalSuiteID(idgen.New("evalsuite")),
		TenantID:  tenantID,
		Name:      name,
		Gates:     gates,
		CreatedBy: actorID,
		CreatedAt: s.now(),
	}
	s.mu.Lock()
	s.suites[suite.SuiteID] = suite
	s.mu.Unlock()
	if s.repo != nil {
		if err := s.repo.SaveSuite(ctx, suite); err != nil {
			return Suite{}, err
		}
	}
	return suite, nil
}

func (s *Store) AddCase(ctx context.Context, suiteID contracts.EvalSuiteID, tc Case) (Suite, bool, error) {
	s.mu.Lock()
	suite, ok := s.suites[suiteID]
	s.mu.Unlock()
	if !ok && s.repo != nil {
		var err error
		suite, ok, err = s.repo.GetSuite(ctx, suiteID)
		if err != nil {
			return Suite{}, false, err
		}
	}
	if !ok {
		return Suite{}, false, nil
	}
	tc.SuiteID = suiteID
	suite.Cases = append(suite.Cases, tc)
	s.mu.Lock()
	s.suites[suiteID] = suite
	s.mu.Unlock()
	if s.repo != nil {
		if err := s.repo.SaveSuite(ctx, suite); err != nil {
			return Suite{}, false, err
		}
	}
	return suite, true, nil
}

func (s *Store) GetSuite(ctx context.Context, suiteID contracts.EvalSuiteID) (Suite, bool, error) {
	s.mu.RLock()
	suite, ok := s.suites[suiteID]
	s.mu.RUnlock()
	if ok || s.repo == nil {
		return suite, ok, nil
	}
	suite, ok, err := s.repo.GetSuite(ctx, suiteID)
	if err != nil || !ok {
		return Suite{}, ok, err
	}
	s.mu.Lock()
	s.suites[suiteID] = suite
	s.mu.Unlock()
	return suite, true, nil
}

func (s *Store) SaveResult(ctx context.Context, result SuiteResult) (SuiteResult, error) {
	if result.EvalRunID == "" {
		result.EvalRunID = contracts.EvalRunID(idgen.New("eval"))
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = s.now()
	}
	s.mu.Lock()
	s.results[result.EvalRunID] = result
	s.mu.Unlock()
	if s.repo != nil {
		if err := s.repo.SaveResult(ctx, result); err != nil {
			return SuiteResult{}, err
		}
	}
	return result, nil
}

func (s *Store) GetResult(ctx context.Context, evalRunID contracts.EvalRunID) (SuiteResult, bool, error) {
	s.mu.RLock()
	result, ok := s.results[evalRunID]
	s.mu.RUnlock()
	if ok || s.repo == nil {
		return result, ok, nil
	}
	result, ok, err := s.repo.GetResult(ctx, evalRunID)
	if err != nil || !ok {
		return SuiteResult{}, ok, err
	}
	s.mu.Lock()
	s.results[evalRunID] = result
	s.mu.Unlock()
	return result, true, nil
}

func BuildSuiteResult(suite Suite, results []Result, now time.Time) SuiteResult {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	failures := make([]string, 0)
	passedCount := 0
	toolCalls := 0
	toolMisuse := 0
	for _, result := range results {
		if result.Passed {
			passedCount++
		}
		if suite.Gates.RequireCriticalPass && result.Critical && !result.Passed {
			failures = append(failures, result.CaseName+": critical eval failed")
		}
		if suite.Gates.RequireSafetyPass && result.Safety && !result.Passed {
			failures = append(failures, result.CaseName+": safety eval failed")
		}
		for _, failure := range result.Failures {
			failures = append(failures, result.CaseName+": "+failure)
		}
		toolCalls += result.ToolCallsTotal
		toolMisuse += result.ToolMisuse
	}
	passRate := 1.0
	if len(results) > 0 {
		passRate = float64(passedCount) / float64(len(results))
	}
	toolMisuseRate := 0.0
	if toolCalls > 0 {
		toolMisuseRate = float64(toolMisuse) / float64(toolCalls)
	}
	if passRate < suite.Gates.MinPassRate {
		failures = append(failures, "pass rate below gate")
	}
	if suite.Gates.MaxToolMisuseRate > 0 && toolMisuseRate > suite.Gates.MaxToolMisuseRate {
		failures = append(failures, "tool misuse rate above gate")
	}
	return SuiteResult{
		EvalRunID:      contracts.EvalRunID(idgen.New("eval")),
		SuiteID:        suite.SuiteID,
		TenantID:       suite.TenantID,
		Passed:         len(failures) == 0,
		PassRate:       passRate,
		ToolMisuseRate: toolMisuseRate,
		Failures:       failures,
		Results:        results,
		CreatedAt:      now,
	}
}
