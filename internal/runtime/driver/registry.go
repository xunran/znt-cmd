package driver

import (
	"context"
	"sync"

	"znt/internal/contracts"
)

type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

func NewRegistry(drivers ...Driver) (*Registry, error) {
	registry := &Registry{drivers: map[string]Driver{}}
	for _, item := range drivers {
		if err := registry.Register(item); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func MustRegistry(drivers ...Driver) *Registry {
	registry, err := NewRegistry(drivers...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(item Driver) error {
	if item == nil {
		return contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "runtime driver is nil", nil)
	}
	kind := item.Kind()
	contract := item.Contract()
	if kind == "" || contract == "" {
		return contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "runtime driver requires kind and contract", map[string]any{
			"carrier_kind":     kind,
			"runtime_contract": contract,
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drivers[key(kind, contract)] = item
	return nil
}

func (r *Registry) Get(kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) (Driver, error) {
	if r == nil {
		return nil, unavailable(kind, contract)
	}
	kind = contracts.NormalizeCarrierKind("", kind)
	contract = contracts.NormalizeRuntimeContract(kind, contract)
	r.mu.RLock()
	item := r.drivers[key(kind, contract)]
	r.mu.RUnlock()
	if item == nil {
		return nil, unavailable(kind, contract)
	}
	return item, nil
}

func (r *Registry) DefaultNative() (Driver, error) {
	return r.Get(contracts.AgentCarrierKindNativeAgent, contracts.RuntimeContractManaged)
}

func unavailable(kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) error {
	return contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "agent runtime driver is unavailable", map[string]any{
		"carrier_kind":     kind,
		"runtime_contract": contract,
	})
}

func key(kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) string {
	return string(kind) + "\x00" + string(contract)
}

func IsUnavailable(err error) bool {
	runtimeErr, ok := err.(*contracts.RuntimeError)
	return ok && runtimeErr.Code == contracts.CodeAgentRuntimeDriverUnavailable
}

func ValidateRegistered(ctx context.Context, registry *Registry, kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) error {
	_, err := registry.Get(kind, contract)
	return err
}
