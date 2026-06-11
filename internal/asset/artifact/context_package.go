package artifact

import (
	"context"
	"sync"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type ContextPackageStore interface {
	SaveContextPackage(ctx context.Context, pkg contracts.HandoffContextPackage) error
	GetContextPackage(ctx context.Context, tenantID contracts.TenantID, packageID contracts.ContextPackageID) (contracts.HandoffContextPackage, error)
}

type InMemoryContextPackageStore struct {
	mu       sync.RWMutex
	packages map[contracts.ContextPackageID]contracts.HandoffContextPackage
}

func NewInMemoryContextPackageStore() *InMemoryContextPackageStore {
	return &InMemoryContextPackageStore{packages: map[contracts.ContextPackageID]contracts.HandoffContextPackage{}}
}

func (s *InMemoryContextPackageStore) SaveContextPackage(_ context.Context, pkg contracts.HandoffContextPackage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.packages[pkg.PackageID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	s.packages[pkg.PackageID] = pkg
	return nil
}

func (s *InMemoryContextPackageStore) GetContextPackage(_ context.Context, tenantID contracts.TenantID, packageID contracts.ContextPackageID) (contracts.HandoffContextPackage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pkg, ok := s.packages[packageID]
	if !ok || pkg.TenantID != tenantID {
		return contracts.HandoffContextPackage{}, storagerepo.ErrNotFound
	}
	return pkg, nil
}
