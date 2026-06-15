package source

import (
	"context"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

type Adapter interface {
	Kind() contracts.AgentSourceKind
	Normalize(ctx context.Context, req NormalizeRequest) (NormalizedSource, error)
	Validate(ctx context.Context, source NormalizedSource) error
	Compile(ctx context.Context, source NormalizedSource) (CompiledCarrier, error)
}

type NormalizeRequest struct {
	AgentID contracts.AgentID
	Version contracts.AgentVersion
	Source  agentpackage.AgentPackageSource
	Plugin  agentpackage.AgentPluginSource
}

type NormalizedSource struct {
	AgentID contracts.AgentID
	Version contracts.AgentVersion
	Source  agentpackage.AgentPackageSource
}

type CompiledCarrier struct {
	Agent             contracts.AgentDefinition
	CarrierKind       contracts.AgentCarrierKind
	RuntimeContract   contracts.RuntimeContractKind
	ConformanceStatus contracts.RuntimeConformanceStatus
	ManifestHash      string
}

func compiledCarrier(definition contracts.AgentDefinition) CompiledCarrier {
	carrierKind := contracts.NormalizeCarrierKind(definition.SourceKind, definition.CarrierKind)
	runtimeContract := contracts.NormalizeRuntimeContract(carrierKind, definition.RuntimeContract)
	return CompiledCarrier{
		Agent:             definition,
		CarrierKind:       carrierKind,
		RuntimeContract:   runtimeContract,
		ConformanceStatus: definition.ConformanceStatus,
		ManifestHash:      definition.ManifestHash,
	}
}
