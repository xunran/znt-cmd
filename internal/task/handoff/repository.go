package handoff

import (
	"context"
	"sync"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type InMemoryRepository struct {
	mu       sync.RWMutex
	handoffs map[contracts.HandoffID]contracts.AgentHandoff
	packages map[contracts.ContextPackageID]contracts.HandoffContextPackage
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		handoffs: map[contracts.HandoffID]contracts.AgentHandoff{},
		packages: map[contracts.ContextPackageID]contracts.HandoffContextPackage{},
	}
}

func (r *InMemoryRepository) Save(_ context.Context, handoff contracts.AgentHandoff, pkg contracts.HandoffContextPackage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handoffs[handoff.HandoffID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	r.handoffs[handoff.HandoffID] = handoff
	if pkg.PackageID != "" {
		r.packages[pkg.PackageID] = pkg
	}
	return nil
}

func (r *InMemoryRepository) Get(_ context.Context, handoffID contracts.HandoffID) (contracts.AgentHandoff, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handoff, ok := r.handoffs[handoffID]
	return handoff, ok, nil
}

func (r *InMemoryRepository) Update(_ context.Context, handoff contracts.AgentHandoff) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handoffs[handoff.HandoffID]; !ok {
		return storagerepo.ErrNotFound
	}
	r.handoffs[handoff.HandoffID] = handoff
	return nil
}
