package admission

import (
	"fmt"
	"sync"

	"znt/internal/contracts"
)

type Config struct {
	MaxRunningRuns       int
	TenantMaxRunningRuns int
	AgentMaxRunningRuns  int
}

type Limiter struct {
	mu            sync.Mutex
	cfg           Config
	globalRunning int
	tenantRunning map[contracts.TenantID]int
	agentRunning  map[agentKey]int
	rejectedTotal int64
}

type Stats struct {
	MaxRunningRuns       int   `json:"run_max_concurrent"`
	TenantMaxRunningRuns int   `json:"tenant_run_max_concurrent"`
	AgentMaxRunningRuns  int   `json:"agent_run_max_concurrent"`
	RunningRuns          int   `json:"agent_runs_running"`
	RejectedRunsTotal    int64 `json:"agent_runs_rejected_total"`
}

type agentKey struct {
	TenantID contracts.TenantID
	AgentID  contracts.AgentID
}

func New(cfg Config) *Limiter {
	return &Limiter{
		cfg:           cfg,
		tenantRunning: map[contracts.TenantID]int{},
		agentRunning:  map[agentKey]int{},
	}
}

func (l *Limiter) AcquireRun(tenantID contracts.TenantID, agentID contracts.AgentID) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cfg.MaxRunningRuns > 0 && l.globalRunning >= l.cfg.MaxRunningRuns {
		l.rejectedTotal++
		return nil, rejected("global", l.cfg.MaxRunningRuns, l.globalRunning)
	}
	if l.cfg.TenantMaxRunningRuns > 0 && l.tenantRunning[tenantID] >= l.cfg.TenantMaxRunningRuns {
		l.rejectedTotal++
		return nil, rejected("tenant", l.cfg.TenantMaxRunningRuns, l.tenantRunning[tenantID])
	}
	key := agentKey{TenantID: tenantID, AgentID: agentID}
	if l.cfg.AgentMaxRunningRuns > 0 && l.agentRunning[key] >= l.cfg.AgentMaxRunningRuns {
		l.rejectedTotal++
		return nil, rejected("agent", l.cfg.AgentMaxRunningRuns, l.agentRunning[key])
	}
	l.globalRunning++
	l.tenantRunning[tenantID]++
	l.agentRunning[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.releaseRun(tenantID, agentID)
		})
	}, nil
}

func (l *Limiter) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		MaxRunningRuns:       l.cfg.MaxRunningRuns,
		TenantMaxRunningRuns: l.cfg.TenantMaxRunningRuns,
		AgentMaxRunningRuns:  l.cfg.AgentMaxRunningRuns,
		RunningRuns:          l.globalRunning,
		RejectedRunsTotal:    l.rejectedTotal,
	}
}

func (l *Limiter) releaseRun(tenantID contracts.TenantID, agentID contracts.AgentID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.globalRunning > 0 {
		l.globalRunning--
	}
	if l.tenantRunning[tenantID] > 0 {
		l.tenantRunning[tenantID]--
	}
	if l.tenantRunning[tenantID] == 0 {
		delete(l.tenantRunning, tenantID)
	}
	key := agentKey{TenantID: tenantID, AgentID: agentID}
	if l.agentRunning[key] > 0 {
		l.agentRunning[key]--
	}
	if l.agentRunning[key] == 0 {
		delete(l.agentRunning, key)
	}
}

func rejected(scope string, limit int, running int) error {
	return contracts.NewRuntimeError(contracts.CodeAdmissionRejected, fmt.Sprintf("%s run concurrency limit reached", scope), map[string]any{
		"scope":   scope,
		"limit":   limit,
		"running": running,
	})
}
