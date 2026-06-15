package driver

import (
	"errors"
	"testing"

	"znt/internal/contracts"
	"znt/internal/runtime/kernel"
)

func TestRegistryFailsClosedForUnknownCarrier(t *testing.T) {
	registry := MustRegistry(NewNative(kernel.Coordinator{}))
	_, err := registry.Get(contracts.AgentCarrierKindExternalRuntime, contracts.RuntimeContractConnected)
	if err == nil {
		t.Fatal("expected missing external runtime driver to fail")
	}
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeAgentRuntimeDriverUnavailable {
		t.Fatalf("expected unavailable runtime error, got %#v", err)
	}
}

func TestRegistryResolvesNativeManagedDriver(t *testing.T) {
	registry := MustRegistry(NewNative(kernel.Coordinator{}))
	item, err := registry.Get(contracts.AgentCarrierKindNativeAgent, contracts.RuntimeContractManaged)
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind() != contracts.AgentCarrierKindNativeAgent || item.Contract() != contracts.RuntimeContractManaged {
		t.Fatalf("unexpected driver %#v", item)
	}
}
