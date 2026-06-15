package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	agentplugin "znt/internal/agentdef/plugin"
	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	policyengine "znt/internal/policy/engine"
	runtimehook "znt/internal/runtime/hook"
	serviceconnection "znt/internal/serviceconnection"
	toolcatalog "znt/internal/tool/catalog"
	"znt/pkg/idgen"
)

func packagePublish(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	payload := envelope.Payload
	if draftID, _ := payload["draft_id"].(string); draftID != "" {
		if err := validateDraftPluginSourceForTenant(r.Context(), appCore, caller.TenantID, draftID); err != nil {
			return nil, err
		}
		release, err := appCore.Packages.PublishDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
		if err != nil {
			return nil, err
		}
		return releaseAndRegisterDraft(r, appCore, release, draftID, caller)
	}
	agentID, _ := payload["agent_id"].(string)
	version, _ := payload["version"].(string)
	prompt, _ := payload["prompt"].(string)
	agentsMD, _ := payload["agents_md"].(string)
	if agentID == "" {
		agentID = string(envelope.Target.AgentID)
	}
	if agentID == "" || version == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.publish requires agent_id and version", nil)
	}
	source, err := agentPackageSourceFromPayload(payload)
	if err != nil {
		return nil, err
	}
	source.AgentsMD = agentsMD
	source.Prompt = prompt
	if err := validateAgentPluginSourceProvider(r.Context(), appCore, caller.TenantID, source); err != nil {
		return nil, err
	}
	draft, err := appCore.Packages.CreateDraft(r.Context(), caller.TenantID, contracts.AgentID(agentID), contracts.AgentVersion(version), source, caller.CallerID)
	if err != nil {
		return nil, err
	}
	if _, err := appCore.Packages.ValidateDraft(r.Context(), draft.DraftID); err != nil {
		return nil, err
	}
	release, err := appCore.Packages.PublishDraft(r.Context(), draft.DraftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return releaseAndRegisterDraft(r, appCore, release, draft.DraftID, caller)
}

func releaseAndRegisterDraft(r *http.Request, appCore *core.Core, release contracts.AgentPackageVersion, draftID string, caller auth.CallerIdentity) (contracts.AgentPackageVersion, error) {
	draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return contracts.AgentPackageVersion{}, err
	}
	if ok {
		compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
		if err != nil {
			return contracts.AgentPackageVersion{}, err
		}
		compiled.TenantID = caller.TenantID
		compiled.PackageVersionID = release.PackageVersionID
		appCore.AgentRegistry.PutVersion(compiled)
		if appCore.ToolCatalog != nil {
			if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
				return contracts.AgentPackageVersion{}, err
			}
		}
	}
	return release, nil
}

func packageDraftCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	agentID, _ := envelope.Payload["agent_id"].(string)
	version, _ := envelope.Payload["version"].(string)
	if agentID == "" {
		agentID = string(envelope.Target.AgentID)
	}
	if agentID == "" || version == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.create requires agent_id and version", nil)
	}
	source, err := agentPackageSourceFromPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	if err := validateAgentPluginSourceProvider(r.Context(), appCore, caller.TenantID, source); err != nil {
		return nil, err
	}
	return appCore.Packages.CreateDraft(r.Context(), caller.TenantID, contracts.AgentID(agentID), contracts.AgentVersion(version), source, caller.CallerID)
}

func agentPackageSourceFromPayload(payload map[string]any) (agentpackage.AgentPackageSource, error) {
	strategies, err := parseAgentStrategiesPayload(payload["strategies"])
	if err != nil {
		return agentpackage.AgentPackageSource{}, err
	}
	skills, err := parseSkillRefsPayload(payload["skills"])
	if err != nil {
		return agentpackage.AgentPackageSource{}, err
	}
	skillDefinitions, err := parseSkillDefinitionsPayload(payload["skill_definitions"])
	if err != nil {
		return agentpackage.AgentPackageSource{}, err
	}
	metadata := parseMetadata(payload["metadata"])
	if err := agentpackage.ValidateSourceMetadata(metadata); err != nil {
		return agentpackage.AgentPackageSource{}, err
	}
	return agentpackage.AgentPackageSource{
		SourceKind:       contracts.AgentSourceKind(payloadString(payload, "source_kind")),
		ProviderID:       payloadString(payload, "provider_id"),
		ManifestVersion:  payloadString(payload, "manifest_version"),
		AgentsMD:         payloadString(payload, "agents_md"),
		Prompt:           payloadString(payload, "prompt"),
		Strategies:       strategies,
		ToolBindings:     parseToolsPayload(payload["tool_bindings"]),
		Skills:           skills,
		SkillDefinitions: skillDefinitions,
		Collaborators:    parseCollaboratorsPayload(payload["collaborators"]),
		Exports:          parseAgentExportsPayload(payload["exports"]),
		RuntimeHooks:     parseRuntimeHooksPayload(payload["runtime_hooks"]),
		Metadata:         metadata,
	}, nil
}

func agentPluginSync(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	providerID := payloadString(envelope.Payload, "provider_id")
	var manifest agentplugin.AgentPluginManifest
	if envelope.Payload["manifest"] != nil {
		parsed, err := parseAgentPluginManifestPayload(envelope.Payload["manifest"])
		if err != nil {
			return nil, err
		}
		manifest = parsed
	} else {
		if providerID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.plugin.sync requires provider_id when manifest is omitted", nil)
		}
		_, connection, err := agentPluginProviderConnection(r.Context(), appCore, caller.TenantID, providerID)
		if err != nil {
			return nil, err
		}
		manifest, err = agentplugin.FetchManifest(r.Context(), nil, connection)
		if err != nil {
			return agentPluginSyncToolCatalogFallback(r, appCore, envelope, caller, providerID, connection.ConnectionID, err)
		}
	}
	if providerID != "" && manifest.ProviderID != "" && manifest.ProviderID != providerID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "agent plugin manifest provider_id does not match requested provider", map[string]any{"provider_id": providerID, "manifest_provider_id": manifest.ProviderID})
	}
	if providerID == "" {
		providerID = manifest.ProviderID
	}
	overrides, err := agentPluginSourceOverridesFromPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	source, err := agentplugin.BuildSource(agentplugin.SourceInput{
		ProviderID: providerID,
		Manifest:   manifest,
		Overrides:  overrides,
	})
	if err != nil {
		return nil, err
	}
	packageSource := agentpackage.PackageSourceFromPlugin(source)
	if err := validateAgentPluginSourceProvider(r.Context(), appCore, caller.TenantID, packageSource); err != nil {
		return nil, err
	}
	_, connection, err := agentPluginProviderConnection(r.Context(), appCore, caller.TenantID, packageSource.ProviderID)
	if err != nil {
		return nil, err
	}
	agentID := contracts.AgentID(payloadString(envelope.Payload, "agent_id"))
	if agentID == "" {
		agentID = manifest.Agent.AgentID
	}
	version := contracts.AgentVersion(payloadString(envelope.Payload, "version"))
	if version == "" {
		version = manifest.Agent.Version
	}
	if agentID == "" || version == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.plugin.sync requires agent_id and version in payload or manifest", nil)
	}
	draft, err := appCore.Packages.CreateDraft(r.Context(), caller.TenantID, agentID, version, packageSource, caller.CallerID)
	if err != nil {
		return nil, err
	}
	toolManifests := agentplugin.ToolManifests(source.ProviderID, manifest)
	for i := range toolManifests {
		toolManifests[i].TenantID = caller.TenantID
		if _, err := appCore.ToolCatalog.UpsertManifest(r.Context(), toolManifests[i], caller.CallerID); err != nil {
			return nil, err
		}
	}
	hookManifests, err := syncAgentPluginHookManifests(r.Context(), appCore, caller.TenantID, agentID, version, source.ProviderID, connection, manifest)
	if err != nil {
		return nil, err
	}
	manifestHash := agentplugin.ManifestHash(manifest)
	recordAgentPluginSyncedTrace(r, appCore, envelope, caller, agentID, version, source.ProviderID, connection.ConnectionID, manifestHash, len(toolManifests), len(hookManifests), "")
	return map[string]any{
		"draft":          draft,
		"source_kind":    contracts.AgentSourceKindPlugin,
		"provider_id":    source.ProviderID,
		"manifest_hash":  manifestHash,
		"manifest":       agentplugin.NormalizeManifest(manifest),
		"tool_manifests": toolManifests,
		"hook_manifests": hookManifests,
	}, nil
}

func agentPluginSyncToolCatalogFallback(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, providerID string, connectionID string, manifestErr error) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, manifestErr
	}
	toolManifests, err := appCore.ToolCatalog.SyncProviderCatalog(r.Context(), caller.TenantID, providerID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	recordAgentPluginSyncedTrace(r, appCore, envelope, caller, "", "", providerID, connectionID, "", len(toolManifests), 0, "tool_catalog_fallback")
	return map[string]any{
		"source_kind":    contracts.AgentSourceKindPlugin,
		"provider_id":    providerID,
		"fallback":       "tool_catalog",
		"draft_created":  false,
		"manifest_error": manifestErr.Error(),
		"tool_manifests": toolManifests,
		"hook_manifests": []runtimehook.HookManifest{},
	}, nil
}

func recordAgentPluginSyncedTrace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, agentID contracts.AgentID, version contracts.AgentVersion, providerID string, connectionID string, manifestHash string, toolCount int, hookCount int, fallback string) {
	if appCore.Trace == nil || envelope.TraceID == "" {
		return
	}
	payload := map[string]any{
		"source_kind":           contracts.AgentSourceKindPlugin,
		"agent_id":              agentID,
		"agent_version":         version,
		"provider_id":           providerID,
		"service_connection_id": connectionID,
		"manifest_hash":         manifestHash,
		"tool_count":            toolCount,
		"hook_count":            hookCount,
	}
	if fallback != "" {
		payload["fallback"] = fallback
		payload["draft_created"] = false
	}
	_ = appCore.Trace.Record(r.Context(), contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  caller.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		Type:      contracts.TraceAgentPluginSynced,
		CreatedAt: time.Now().UTC(),
		Payload:   payload,
	})
}

func parseAgentPluginManifestPayload(value any) (agentplugin.AgentPluginManifest, error) {
	if value == nil {
		return agentplugin.AgentPluginManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.plugin.sync requires manifest", nil)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return agentplugin.AgentPluginManifest{}, err
	}
	manifest, err := agentplugin.DecodeManifest(data)
	if err != nil {
		return agentplugin.AgentPluginManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid agent plugin manifest", map[string]any{"error": err.Error()})
	}
	return manifest, nil
}

func agentPluginSourceOverridesFromPayload(payload map[string]any) (agentpackage.AgentPluginSource, error) {
	strategies, err := parseAgentStrategiesPayload(payload["strategies"])
	if err != nil {
		return agentpackage.AgentPluginSource{}, err
	}
	skills, err := parseSkillRefsPayload(payload["skills"])
	if err != nil {
		return agentpackage.AgentPluginSource{}, err
	}
	skillDefinitions, err := parseSkillDefinitionsPayload(payload["skill_definitions"])
	if err != nil {
		return agentpackage.AgentPluginSource{}, err
	}
	metadata := parseMetadata(payload["metadata"])
	if err := agentpackage.ValidateSourceMetadata(metadata); err != nil {
		return agentpackage.AgentPluginSource{}, err
	}
	return agentpackage.AgentPluginSource{
		ProviderID:       payloadString(payload, "provider_id"),
		ManifestVersion:  payloadString(payload, "manifest_version"),
		AgentsMD:         payloadString(payload, "agents_md"),
		Prompt:           payloadString(payload, "prompt"),
		Strategies:       strategies,
		ToolBindings:     parseToolsPayload(payload["tool_bindings"]),
		Skills:           skills,
		SkillDefinitions: skillDefinitions,
		Collaborators:    parseCollaboratorsPayload(payload["collaborators"]),
		Exports:          parseAgentExportsPayload(payload["exports"]),
		RuntimeHooks:     parseRuntimeHooksPayload(payload["runtime_hooks"]),
		Metadata:         metadata,
	}, nil
}

func parseAgentStrategiesPayload(value any) (contracts.AgentStrategies, error) {
	if value == nil {
		return contracts.AgentStrategies{}, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return contracts.AgentStrategies{}, err
	}
	var strategies contracts.AgentStrategies
	if err := decodeAgentStrategiesStrict(data, &strategies); err != nil {
		return contracts.AgentStrategies{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid strategies payload", map[string]any{"error": err.Error()})
	}
	return strategies, nil
}

func mergeAgentStrategiesPayload(base contracts.AgentStrategies, value any) (contracts.AgentStrategies, error) {
	if value == nil {
		return contracts.AgentStrategies{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_strategies requires strategies", nil)
	}
	baseData, err := json.Marshal(base)
	if err != nil {
		return contracts.AgentStrategies{}, err
	}
	var merged map[string]any
	if err := json.Unmarshal(baseData, &merged); err != nil {
		return contracts.AgentStrategies{}, err
	}
	patchData, err := json.Marshal(value)
	if err != nil {
		return contracts.AgentStrategies{}, err
	}
	var patch map[string]any
	if err := json.Unmarshal(patchData, &patch); err != nil {
		return contracts.AgentStrategies{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid strategies payload", map[string]any{"error": err.Error()})
	}
	if len(patch) == 0 {
		return contracts.AgentStrategies{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_strategies requires at least one strategy family", nil)
	}
	mergeJSONObjects(merged, patch)
	data, err := json.Marshal(merged)
	if err != nil {
		return contracts.AgentStrategies{}, err
	}
	var strategies contracts.AgentStrategies
	if err := decodeAgentStrategiesStrict(data, &strategies); err != nil {
		return contracts.AgentStrategies{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid strategies payload", map[string]any{"error": err.Error()})
	}
	return strategies, nil
}

func decodeAgentStrategiesStrict(data []byte, target *contracts.AgentStrategies) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("strategies payload contains multiple JSON values")
		}
		return err
	}
	return nil
}

func mergeJSONObjects(base map[string]any, patch map[string]any) {
	for key, value := range patch {
		patchObject, patchOK := value.(map[string]any)
		baseObject, baseOK := base[key].(map[string]any)
		if patchOK && baseOK {
			mergeJSONObjects(baseObject, patchObject)
			base[key] = baseObject
			continue
		}
		base[key] = value
	}
}

func validateDraftPluginSourceForTenant(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, draftID string) error {
	draft, ok, err := appCore.Packages.GetDraft(ctx, draftID)
	if err != nil {
		return err
	}
	if !ok {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if draft.TenantID != tenantID {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	return validateAgentPluginSourceProvider(ctx, appCore, tenantID, draft.Source)
}

func validateAgentPluginSourceProvider(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, source agentpackage.AgentPackageSource) error {
	if source.SourceKind != contracts.AgentSourceKindPlugin {
		return nil
	}
	if err := agentpackage.ValidateSourceMetadata(source.Metadata); err != nil {
		return err
	}
	if err := agentpackage.ValidatePluginSourceMetadata(source.Metadata); err != nil {
		return err
	}
	_, _, err := agentPluginProviderConnection(ctx, appCore, tenantID, source.ProviderID)
	return err
}

func validatePackageReleasePluginSourceForTenant(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, release contracts.AgentPackageVersion) error {
	if release.TenantID != tenantID {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent package release tenant does not match caller tenant", nil)
	}
	if release.SourceKind != contracts.AgentSourceKindPlugin {
		return nil
	}
	_, _, err := agentPluginProviderConnection(ctx, appCore, tenantID, release.SourceProviderID)
	return err
}

func agentPluginProviderConnection(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, providerID string) (toolcatalog.ToolProvider, serviceconnection.ServiceConnection, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "plugin_service source requires provider_id", nil)
	}
	if appCore.ToolCatalog == nil {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	provider, ok := appCore.ToolCatalog.GetProvider(tenantID, providerID)
	if !ok {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "agent plugin provider not found", map[string]any{"provider_id": providerID})
	}
	if provider.ProviderType != toolcatalog.ProviderTypeAgentPlugin {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "plugin source provider must be agent_plugin_service", map[string]any{"provider_id": provider.ProviderID, "provider_type": provider.ProviderType})
	}
	if provider.Status != toolcatalog.StatusEnabled {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent plugin provider is not enabled", map[string]any{"provider_id": provider.ProviderID, "status": provider.Status})
	}
	if provider.HealthStatus == toolcatalog.HealthUnhealthy {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent plugin provider is unhealthy", map[string]any{"provider_id": provider.ProviderID, "health_status": provider.HealthStatus})
	}
	if provider.ServiceConnectionID == "" {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "agent plugin provider requires service_connection_id", map[string]any{"provider_id": provider.ProviderID})
	}
	if appCore.ServiceConnections == nil {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil)
	}
	connection, ok, err := appCore.ServiceConnections.Get(ctx, tenantID, provider.ServiceConnectionID)
	if err != nil {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, err
	}
	if !ok {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "agent plugin service connection not found", map[string]any{"provider_id": provider.ProviderID, "connection_id": provider.ServiceConnectionID})
	}
	if connection.Status != serviceconnection.StatusEnabled {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent plugin service connection is not enabled", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID, "status": connection.Status})
	}
	if connection.HealthStatus == serviceconnection.HealthUnhealthy {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "agent plugin service connection is unhealthy", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID, "health_status": connection.HealthStatus})
	}
	if connection.BaseURL == "" {
		return toolcatalog.ToolProvider{}, serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "agent plugin service connection base_url is required", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID})
	}
	return provider, connection, nil
}

func syncAgentPluginHookManifests(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, providerID string, connection serviceconnection.ServiceConnection, manifest agentplugin.AgentPluginManifest) ([]runtimehook.HookManifest, error) {
	manifest = agentplugin.NormalizeManifest(manifest)
	if len(manifest.Hooks) == 0 {
		return nil, nil
	}
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	provider := runtimehook.Provider{
		TenantID:            tenantID,
		ProviderID:          providerID,
		Name:                providerID,
		ProviderType:        runtimehook.ProviderTypeStaticHookHost,
		ServiceConnectionID: connection.ConnectionID,
		Status:              runtimehook.StatusEnabled,
		HealthStatus:        runtimehook.HealthUnknown,
		Version:             manifest.ManifestVersion,
	}
	if provider.Version == "" {
		provider.Version = "v1"
	}
	if err := appCore.RuntimeHooks.UpsertProvider(ctx, provider); err != nil {
		return nil, err
	}
	synced := make([]runtimehook.HookManifest, 0, len(manifest.Hooks))
	for _, hook := range manifest.Hooks {
		hook.HookID = strings.TrimSpace(hook.HookID)
		if hook.HookID == "" {
			continue
		}
		hook.TenantID = tenantID
		hook.ProviderID = providerID
		if hook.Version == "" {
			hook.Version = "v1"
		}
		if hook.Status == "" {
			hook.Status = runtimehook.StatusEnabled
		}
		if hook.TimeoutMS == 0 {
			hook.TimeoutMS = 300
		}
		if hook.FailurePolicy == "" {
			hook.FailurePolicy = "ignore"
		}
		if err := appCore.RuntimeHooks.UpsertManifest(ctx, hook); err != nil {
			return nil, err
		}
		synced = append(synced, hook)
		if hook.Status != runtimehook.StatusEnabled {
			continue
		}
		binding := runtimehook.Binding{
			TenantID:         tenantID,
			AgentID:          agentID,
			AgentVersion:     version,
			HookID:           hook.HookID,
			ProviderType:     runtimehook.ProviderTypeStaticHookHost,
			ProviderID:       providerID,
			Phase:            hook.Phase,
			Version:          hook.Version,
			Enabled:          hook.Status == runtimehook.StatusEnabled,
			TimeoutMS:        hook.TimeoutMS,
			FailurePolicy:    hook.FailurePolicy,
			RequiresApproval: hook.RequiresApproval,
			ApprovalPolicy:   hook.ApprovalPolicy,
		}
		if err := appCore.RuntimeHooks.UpsertBinding(ctx, binding); err != nil {
			return nil, err
		}
	}
	return synced, nil
}

func packageCollaboratorUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator command requires draft_id", nil)
	}
	collaborator := parseCollaboratorPayload(envelope.Payload)
	if collaborator.AgentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator command requires agent_id", nil)
	}
	if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
		return nil, err
	}
	return appCore.Packages.UpsertCollaboratorForTenant(r.Context(), caller.TenantID, draftID, collaborator, caller.CallerID)
}

func packageCollaboratorReplace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator.replace requires draft_id", nil)
	}
	collaborators := parseCollaboratorsPayload(envelope.Payload["collaborators"])
	for _, collaborator := range collaborators {
		if collaborator.AgentID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaborator agent_id is required", nil)
		}
		if _, err := appCore.AgentRegistry.Load(r.Context(), caller.TenantID, collaborator.AgentID, collaborator.Version); err != nil {
			return nil, err
		}
	}
	return appCore.Packages.PatchCollaboratorsForTenant(r.Context(), caller.TenantID, draftID, collaborators, caller.CallerID)
}

func packageCollaboratorRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	agentID := contracts.AgentID(payloadString(envelope.Payload, "agent_id"))
	if draftID == "" || agentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.collaborator.remove requires draft_id and agent_id", nil)
	}
	return appCore.Packages.RemoveCollaboratorForTenant(r.Context(), caller.TenantID, draftID, agentID, caller.CallerID)
}

func packageExportedToolUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool command requires draft_id", nil)
	}
	tool := parseExportedToolPayload(envelope.Payload)
	if tool.ToolID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool command requires tool_id", nil)
	}
	draft, err := appCore.Packages.UpsertExportedToolForTenant(r.Context(), caller.TenantID, draftID, tool, caller.CallerID)
	if err != nil {
		return nil, err
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return nil, err
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
			return nil, err
		}
	}
	return draft, nil
}

func packageExportedToolReplace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool.replace requires draft_id", nil)
	}
	existing, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if existing.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	exports := parseAgentExportsPayload(envelope.Payload["exports"])
	draft, err := appCore.Packages.PatchExportsForTenant(r.Context(), caller.TenantID, draftID, exports, caller.CallerID)
	if err != nil {
		return nil, err
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		return nil, err
	}
	compiled.TenantID = caller.TenantID
	if appCore.ToolCatalog != nil {
		if err := disableRemovedAgentExportedTools(r.Context(), appCore, existing, compiled, caller.CallerID); err != nil {
			return nil, err
		}
		if err := syncAgentExportedTools(r.Context(), appCore, compiled, caller.CallerID); err != nil {
			return nil, err
		}
	}
	return draft, nil
}

func packageExportedToolRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	toolID := payloadString(envelope.Payload, "tool_id")
	if draftID == "" || toolID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.exported_tool.remove requires draft_id and tool_id", nil)
	}
	draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if draft.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	updated, err := appCore.Packages.RemoveExportedToolForTenant(r.Context(), caller.TenantID, draftID, toolID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	if appCore.ToolCatalog != nil {
		manifest := toolcatalog.ToolManifest{
			TenantID:    caller.TenantID,
			ToolID:      toolID,
			Name:        toolID,
			Description: "disabled exported tool",
			InputSchema: map[string]any{"type": "object"},
			Executor:    toolcatalog.ExecutorSpec{Type: toolcatalog.ExecutorTypeAgentTool, ProviderID: string(draft.AgentID), Operation: toolID},
			Status:      toolcatalog.StatusDisabled,
		}
		if existing, ok := appCore.ToolCatalog.GetManifest(caller.TenantID, toolID); ok {
			manifest = existing
			manifest.Status = toolcatalog.StatusDisabled
		}
		_, _ = appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
	}
	return updated, nil
}

func packageDraftPatchStrategies(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if err := requirePayloadKeys(envelope.Command, envelope.Payload, "draft_id", "strategies"); err != nil {
		return nil, err
	}
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.patch_strategies requires draft_id", nil)
	}
	draft, ok, err := appCore.Packages.GetDraft(r.Context(), draftID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "draft not found", map[string]any{"draft_id": draftID})
	}
	if draft.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "draft tenant does not match caller tenant", nil)
	}
	strategies, err := mergeAgentStrategiesPayload(draft.Source.Strategies, envelope.Payload["strategies"])
	if err != nil {
		return nil, err
	}
	return appCore.Packages.PatchStrategiesForTenant(r.Context(), caller.TenantID, draftID, strategies, caller.CallerID)
}

func requirePayloadKeys(command string, payload map[string]any, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range payload {
		if _, ok := allowedSet[key]; !ok {
			return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown payload field", map[string]any{"command": command, "field": key})
		}
	}
	return nil
}

func packageSkillUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.skill command requires draft_id", nil)
	}
	skill, err := skillDraftInput(envelope.Payload)
	if err != nil {
		return nil, err
	}
	draft, err := appCore.Packages.UpsertSkillForTenant(r.Context(), caller.TenantID, draftID, skill, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageSkillRemove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	skillID := payloadString(envelope.Payload, "skill_id")
	if draftID == "" || skillID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.skill.remove requires draft_id and skill_id", nil)
	}
	draft, err := appCore.Packages.RemoveSkillForTenant(r.Context(), caller.TenantID, draftID, skillID, payloadString(envelope.Payload, "version"), caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageProposalCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.create requires draft_id", nil)
	}
	if err := validateDraftPluginSourceForTenant(r.Context(), appCore, caller.TenantID, draftID); err != nil {
		return nil, err
	}
	return appCore.Packages.CreateProposalForTenant(
		r.Context(),
		caller.TenantID,
		draftID,
		payloadString(envelope.Payload, "proposal_type"),
		payloadString(envelope.Payload, "title"),
		payloadString(envelope.Payload, "reason"),
		parseMetadata(envelope.Payload["patch"]),
		caller.CallerID,
	)
}

func packageProposalSubmit(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.submit requires proposal_id", nil)
	}
	return appCore.Packages.SubmitProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
}

func packageProposalApprove(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.approve requires proposal_id", nil)
	}
	return appCore.Packages.ApproveProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
}

func packageProposalReject(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.reject requires proposal_id", nil)
	}
	return appCore.Packages.RejectProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID, payloadString(envelope.Payload, "reason"))
}

func packageProposalPublish(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	proposalID := contracts.ProposalID(payloadString(envelope.Payload, "proposal_id"))
	if proposalID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.proposal.publish requires proposal_id", nil)
	}
	proposal, ok, err := appCore.Packages.GetProposal(r.Context(), proposalID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "proposal not found", nil)
	}
	if err := validateDraftPluginSourceForTenant(r.Context(), appCore, caller.TenantID, proposal.DraftID); err != nil {
		return nil, err
	}
	release, err := appCore.Packages.PublishProposalForTenant(r.Context(), caller.TenantID, proposalID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return releaseAndRegisterDraft(r, appCore, release, proposal.DraftID, caller)
}

func packageDraftValidate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.draft.validate requires draft_id", nil)
	}
	if err := validateDraftPluginSourceForTenant(r.Context(), appCore, caller.TenantID, draftID); err != nil {
		return nil, err
	}
	draft, err := appCore.Packages.ValidateDraftForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageReview(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	draftID := payloadString(envelope.Payload, "draft_id")
	if draftID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.review requires draft_id", nil)
	}
	if err := validateDraftPluginSourceForTenant(r.Context(), appCore, caller.TenantID, draftID); err != nil {
		return nil, err
	}
	draft, err := appCore.Packages.MarkReviewedForTenant(r.Context(), caller.TenantID, draftID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func packageReleaseAction(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, action string) (any, error) {
	packageVersionID, _ := envelope.Payload["package_version_id"].(string)
	if packageVersionID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "package release command requires package_version_id", nil)
	}
	current, err := ensurePackageReleaseTenant(appCore, contracts.PackageVersionID(packageVersionID), caller.TenantID)
	if err != nil {
		return nil, err
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, "policy_default")
	resourceAction := "agent.package." + action
	approved, err := validateReleaseApproval(appCore, envelope, caller, "agent_package_version", packageVersionID, resourceAction)
	if err != nil {
		return nil, err
	}
	releaseReq := policyengine.ReleaseRequest{
		Action:        action,
		CurrentStatus: current.Status,
		CanaryPercent: payloadInt(envelope.Payload, "canary_percent"),
		Approved:      approved,
		Reason:        payloadString(envelope.Payload, "reason"),
		Now:           time.Now().UTC(),
	}
	if _, err := policyengine.EvaluateReleaseAction(policySet.ReleasePolicy, releaseReq); err != nil {
		return requestReleaseApprovalOnRequired(r, appCore, envelope, caller, err, "agent_package_version", packageVersionID, resourceAction, releaseReq)
	}
	if action == "canary" || action == "stable" {
		if err := validatePackageReleasePluginSourceForTenant(r.Context(), appCore, caller.TenantID, current); err != nil {
			return nil, err
		}
	}
	if approved {
		if err := consumeReleaseApproval(r, appCore, envelope, caller, "agent_package_version", packageVersionID, resourceAction); err != nil {
			return nil, err
		}
	}
	switch action {
	case "canary":
		return appCore.Packages.MarkCanaryWithRule(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID, payloadInt(envelope.Payload, "canary_percent"), stringSlice(envelope.Payload["canary_scope"]))
	case "stable":
		release, err := appCore.Packages.MarkStable(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID)
		if err != nil {
			return nil, err
		}
		if _, err := appCore.Packages.EnsureAgentAssetVersionForTenant(r.Context(), caller.TenantID, release.AgentID, release.Version, caller.CallerID); err != nil {
			return nil, err
		}
		if err := appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, release.AgentID, release.Version); err != nil {
			return nil, err
		}
		return release, nil
	case "rollback":
		reason, _ := envelope.Payload["reason"].(string)
		wasActive, err := isActiveAgentVersion(r.Context(), appCore, caller.TenantID, current.AgentID, current.Version)
		if err != nil {
			return nil, err
		}
		fallback := contracts.AgentVersion("")
		if wasActive {
			var ok bool
			fallback, ok = fallbackStableVersion(appCore.Packages.ListReleases(), current.TenantID, current.AgentID, current.Version)
			if !ok {
				return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "rollback active agent package requires another stable version", map[string]any{
					"agent_id":              current.AgentID,
					"rolled_back_version":   current.Version,
					"package_version_id":    current.PackageVersionID,
					"required_release_type": contracts.ReleaseStable,
				})
			}
			if err := ensureAgentVersionCanBeDefault(r.Context(), appCore, caller.TenantID, current.AgentID, fallback); err != nil {
				return nil, err
			}
			if _, err := appCore.Packages.EnsureAgentAssetVersionForTenant(r.Context(), caller.TenantID, current.AgentID, fallback, caller.CallerID); err != nil {
				return nil, err
			}
			if appCore.AgentRegistry != nil {
				if err := appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, current.AgentID, fallback); err != nil {
					_, _ = appCore.Packages.EnsureAgentAssetVersionForTenant(r.Context(), caller.TenantID, current.AgentID, current.Version, caller.CallerID)
					return nil, err
				}
			}
		}
		release, err := appCore.Packages.Rollback(r.Context(), contracts.PackageVersionID(packageVersionID), caller.CallerID, reason)
		if err != nil {
			if wasActive {
				_, _ = appCore.Packages.EnsureAgentAssetVersionForTenant(r.Context(), caller.TenantID, current.AgentID, current.Version, caller.CallerID)
				if appCore.AgentRegistry != nil {
					_ = appCore.AgentRegistry.SetDefaultForTenant(caller.TenantID, current.AgentID, current.Version)
				}
			}
			return nil, err
		}
		return release, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown release action", nil)
	}
}

func isActiveAgentVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (bool, error) {
	if appCore.Packages != nil {
		asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID)
		if err != nil {
			return false, err
		}
		if ok && (asset.ActiveVersion == version || asset.DefaultVersion == version) {
			return true, nil
		}
	}
	if appCore.AgentRegistry != nil && appCore.AgentRegistry.DefaultVersionForTenant(tenantID, agentID) == version {
		return true, nil
	}
	return false, nil
}

func ensureAgentVersionCanBeDefault(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) error {
	if appCore.AgentRegistry == nil {
		return nil
	}
	definition, err := appCore.AgentRegistry.Load(ctx, tenantID, agentID, version)
	if err != nil {
		return err
	}
	if definition.SourceKind == contracts.AgentSourceKindPlugin {
		_, _, err = agentPluginProviderConnection(ctx, appCore, tenantID, definition.SourceProviderID)
	}
	return err
}

func fallbackStableVersion(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID, rolledBack contracts.AgentVersion) (contracts.AgentVersion, bool) {
	fallback := contracts.AgentVersion("")
	var fallbackAt time.Time
	for _, release := range releases {
		if release.TenantID != tenantID || release.AgentID != agentID || release.Version == rolledBack || release.Status != contracts.ReleaseStable {
			continue
		}
		at := release.CreatedAt
		if release.PublishedAt != nil {
			at = *release.PublishedAt
		}
		if fallbackAt.IsZero() || at.After(fallbackAt) {
			fallback = release.Version
			fallbackAt = at
		}
	}
	return fallback, fallback != ""
}
