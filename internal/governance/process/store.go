package process

import (
	"context"
	"fmt"
	"sync"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type Store interface {
	UpsertTemplate(ctx context.Context, template contracts.GovernanceProcessTemplate) error
	GetTemplate(ctx context.Context, tenantID contracts.TenantID, templateID contracts.GovernanceProcessTemplateID) (contracts.GovernanceProcessTemplate, bool, error)
	CreateRun(ctx context.Context, run contracts.GovernanceProcessRun, gates []contracts.GovernanceGateRun) error
	GetRun(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) (contracts.GovernanceProcessRun, bool, error)
	UpdateRun(ctx context.Context, run contracts.GovernanceProcessRun) error
	GetGate(ctx context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) (contracts.GovernanceGateRun, bool, error)
	GetGateByDefinition(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID, gateID string) (contracts.GovernanceGateRun, bool, error)
	ListGates(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceGateRun, error)
	UpdateGate(ctx context.Context, gate contracts.GovernanceGateRun) error
	SaveReview(ctx context.Context, review contracts.GovernanceReview) error
	ListReviews(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceReview, error)
	ListReviewsByGate(ctx context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) ([]contracts.GovernanceReview, error)
	SaveConflict(ctx context.Context, conflict contracts.GovernanceConflict) error
	UpdateConflict(ctx context.Context, conflict contracts.GovernanceConflict) error
	GetConflict(ctx context.Context, tenantID contracts.TenantID, conflictID contracts.GovernanceConflictID) (contracts.GovernanceConflict, bool, error)
	ListConflicts(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceConflict, error)
}

type InMemoryStore struct {
	mu        sync.RWMutex
	templates map[string]contracts.GovernanceProcessTemplate
	runs      map[contracts.GovernanceProcessRunID]contracts.GovernanceProcessRun
	gates     map[contracts.GovernanceGateRunID]contracts.GovernanceGateRun
	reviews   map[contracts.GovernanceReviewID]contracts.GovernanceReview
	conflicts map[contracts.GovernanceConflictID]contracts.GovernanceConflict
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		templates: map[string]contracts.GovernanceProcessTemplate{},
		runs:      map[contracts.GovernanceProcessRunID]contracts.GovernanceProcessRun{},
		gates:     map[contracts.GovernanceGateRunID]contracts.GovernanceGateRun{},
		reviews:   map[contracts.GovernanceReviewID]contracts.GovernanceReview{},
		conflicts: map[contracts.GovernanceConflictID]contracts.GovernanceConflict{},
	}
}

func (s *InMemoryStore) UpsertTemplate(_ context.Context, template contracts.GovernanceProcessTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates[templateKey(template.TenantID, template.TemplateID)] = template
	return nil
}

func (s *InMemoryStore) GetTemplate(_ context.Context, tenantID contracts.TenantID, templateID contracts.GovernanceProcessTemplateID) (contracts.GovernanceProcessTemplate, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	template, ok := s.templates[templateKey(tenantID, templateID)]
	return template, ok, nil
}

func (s *InMemoryStore) CreateRun(_ context.Context, run contracts.GovernanceProcessRun, gates []contracts.GovernanceGateRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.RunID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	s.runs[run.RunID] = run
	for _, gate := range gates {
		s.gates[gate.GateRunID] = gate
	}
	return nil
}

func (s *InMemoryStore) GetRun(_ context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) (contracts.GovernanceProcessRun, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return contracts.GovernanceProcessRun{}, false, nil
	}
	return run, true, nil
}

func (s *InMemoryStore) UpdateRun(_ context.Context, run contracts.GovernanceProcessRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.RunID]; !ok {
		return storagerepo.ErrNotFound
	}
	s.runs[run.RunID] = run
	return nil
}

func (s *InMemoryStore) GetGate(_ context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) (contracts.GovernanceGateRun, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gate, ok := s.gates[gateRunID]
	if !ok || gate.TenantID != tenantID {
		return contracts.GovernanceGateRun{}, false, nil
	}
	return gate, true, nil
}

func (s *InMemoryStore) GetGateByDefinition(_ context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID, gateID string) (contracts.GovernanceGateRun, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, gate := range s.gates {
		if gate.TenantID == tenantID && gate.ProcessRunID == runID && gate.GateID == gateID {
			return gate, true, nil
		}
	}
	return contracts.GovernanceGateRun{}, false, nil
}

func (s *InMemoryStore) ListGates(_ context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceGateRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GovernanceGateRun, 0)
	for _, gate := range s.gates {
		if gate.TenantID == tenantID && gate.ProcessRunID == runID {
			out = append(out, gate)
		}
	}
	return out, nil
}

func (s *InMemoryStore) UpdateGate(_ context.Context, gate contracts.GovernanceGateRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.gates[gate.GateRunID]; !ok {
		return storagerepo.ErrNotFound
	}
	s.gates[gate.GateRunID] = gate
	return nil
}

func (s *InMemoryStore) SaveReview(_ context.Context, review contracts.GovernanceReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviews[review.ReviewID] = review
	return nil
}

func (s *InMemoryStore) ListReviews(_ context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceReview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GovernanceReview, 0)
	for _, review := range s.reviews {
		if review.TenantID == tenantID && review.ProcessRunID == runID {
			out = append(out, review)
		}
	}
	return out, nil
}

func (s *InMemoryStore) ListReviewsByGate(_ context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) ([]contracts.GovernanceReview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GovernanceReview, 0)
	for _, review := range s.reviews {
		if review.TenantID == tenantID && review.GateRunID == gateRunID {
			out = append(out, review)
		}
	}
	return out, nil
}

func (s *InMemoryStore) SaveConflict(_ context.Context, conflict contracts.GovernanceConflict) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conflicts[conflict.ConflictID] = conflict
	return nil
}

func (s *InMemoryStore) UpdateConflict(_ context.Context, conflict contracts.GovernanceConflict) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conflicts[conflict.ConflictID]; !ok {
		return storagerepo.ErrNotFound
	}
	s.conflicts[conflict.ConflictID] = conflict
	return nil
}

func (s *InMemoryStore) GetConflict(_ context.Context, tenantID contracts.TenantID, conflictID contracts.GovernanceConflictID) (contracts.GovernanceConflict, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conflict, ok := s.conflicts[conflictID]
	if !ok || conflict.TenantID != tenantID {
		return contracts.GovernanceConflict{}, false, nil
	}
	return conflict, true, nil
}

func (s *InMemoryStore) ListConflicts(_ context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceConflict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GovernanceConflict, 0)
	for _, conflict := range s.conflicts {
		if conflict.TenantID == tenantID && conflict.ProcessRunID == runID {
			out = append(out, conflict)
		}
	}
	return out, nil
}

func templateKey(tenantID contracts.TenantID, templateID contracts.GovernanceProcessTemplateID) string {
	return fmt.Sprintf("%s/%s", tenantID, templateID)
}
