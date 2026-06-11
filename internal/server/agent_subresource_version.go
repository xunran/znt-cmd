package server

import (
	"context"

	"znt/internal/app/core"
	"znt/internal/contracts"
)

func standaloneAgentSubresourceVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, payload map[string]any) (contracts.AgentVersion, bool, error) {
	version := contracts.AgentVersion(payloadString(payload, "agent_version"))
	if version == "" {
		version = contracts.AgentVersion(payloadString(payload, "version"))
	}
	if version != "" {
		return version, true, nil
	}
	if asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID); err != nil {
		return "", false, err
	} else if ok {
		if asset.ActiveVersion != "" {
			return asset.ActiveVersion, false, nil
		}
		if asset.DefaultVersion != "" {
			return asset.DefaultVersion, false, nil
		}
	}
	if appCore.AgentRegistry != nil {
		if version := appCore.AgentRegistry.DefaultVersionForTenant(tenantID, agentID); version != "" {
			return version, false, nil
		}
	}
	if appCore.Agents != nil {
		definition, err := appCore.Agents.Load(ctx, tenantID, agentID, "")
		if err == nil && definition.Version != "" {
			return definition.Version, false, nil
		}
	}
	return "", false, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "prompt profile requires an agent version", map[string]any{"agent_id": agentID})
}
