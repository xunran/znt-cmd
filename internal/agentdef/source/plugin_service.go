package source

import (
	"context"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

type PluginServiceAdapter struct{}

func (PluginServiceAdapter) Kind() contracts.AgentSourceKind {
	return contracts.AgentSourceKindPlugin
}

func (PluginServiceAdapter) Normalize(_ context.Context, req NormalizeRequest) (NormalizedSource, error) {
	source := req.Source
	if source.SourceKind == "" && req.Plugin.ProviderID != "" {
		source = agentpackage.PackageSourceFromPlugin(req.Plugin)
	}
	source.SourceKind = contracts.AgentSourceKindPlugin
	return NormalizedSource{AgentID: req.AgentID, Version: req.Version, Source: source}, nil
}

func (PluginServiceAdapter) Validate(_ context.Context, normalized NormalizedSource) error {
	if err := agentpackage.ValidatePluginSourceMetadata(normalized.Source.Metadata); err != nil {
		return err
	}
	_, err := agentpackage.Compile(normalized.AgentID, normalized.Version, normalized.Source)
	return err
}

func (PluginServiceAdapter) Compile(_ context.Context, normalized NormalizedSource) (CompiledCarrier, error) {
	if err := agentpackage.ValidatePluginSourceMetadata(normalized.Source.Metadata); err != nil {
		return CompiledCarrier{}, err
	}
	definition, err := agentpackage.Compile(normalized.AgentID, normalized.Version, normalized.Source)
	if err != nil {
		return CompiledCarrier{}, err
	}
	definition.CarrierKind = contracts.AgentCarrierKindAgentPluginSource
	definition.RuntimeContract = contracts.RuntimeContractManaged
	return compiledCarrier(definition), nil
}
