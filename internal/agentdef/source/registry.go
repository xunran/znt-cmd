package source

import (
	"sync"

	"znt/internal/contracts"
)

type Registry struct {
	mu       sync.RWMutex
	adapters map[contracts.AgentSourceKind]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{adapters: map[contracts.AgentSourceKind]Adapter{}}
	for _, adapter := range adapters {
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(PackageAdapter{}, PluginServiceAdapter{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(adapter Adapter) error {
	if adapter == nil || adapter.Kind() == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "source adapter requires source kind", nil)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Kind()] = adapter
	return nil
}

func (r *Registry) Get(kind contracts.AgentSourceKind) (Adapter, error) {
	if r == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "source adapter registry is unavailable", nil)
	}
	kind = contracts.NormalizeSourceKind(kind)
	r.mu.RLock()
	adapter := r.adapters[kind]
	r.mu.RUnlock()
	if adapter == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "source adapter is unavailable", map[string]any{"source_kind": kind})
	}
	return adapter, nil
}
