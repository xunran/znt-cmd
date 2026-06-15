package source

import (
	"context"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

type PackageAdapter struct{}

func (PackageAdapter) Kind() contracts.AgentSourceKind {
	return contracts.AgentSourceKindPackage
}

func (PackageAdapter) Normalize(_ context.Context, req NormalizeRequest) (NormalizedSource, error) {
	source := req.Source
	source.SourceKind = contracts.AgentSourceKindPackage
	return NormalizedSource{AgentID: req.AgentID, Version: req.Version, Source: source}, nil
}

func (PackageAdapter) Validate(_ context.Context, normalized NormalizedSource) error {
	_, err := agentpackage.Compile(normalized.AgentID, normalized.Version, normalized.Source)
	return err
}

func (PackageAdapter) Compile(_ context.Context, normalized NormalizedSource) (CompiledCarrier, error) {
	definition, err := agentpackage.Compile(normalized.AgentID, normalized.Version, normalized.Source)
	if err != nil {
		return CompiledCarrier{}, err
	}
	return compiledCarrier(definition), nil
}
