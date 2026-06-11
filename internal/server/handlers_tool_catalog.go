package server

import (
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	toolcatalog "znt/internal/tool/catalog"
)

func handleToolProviders(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"providers": appCore.ToolCatalog.ListProviders(caller.TenantID)}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid tool provider json")
		if !ok {
			return
		}
		provider := parseToolProviderPayload(payload)
		provider.TenantID = caller.TenantID
		created, err := appCore.ToolCatalog.UpsertProvider(r.Context(), provider, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": created}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool providers method", nil), http.StatusMethodNotAllowed)
	}
}

func handleToolProviderResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	providerID, suffix, _ := strings.Cut(path, "/")
	if providerID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "provider_id is required", nil), http.StatusBadRequest)
		return
	}
	if suffix == "sync" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported provider sync method", nil), http.StatusMethodNotAllowed)
			return
		}
		tools, err := appCore.ToolCatalog.SyncProviderCatalog(r.Context(), caller.TenantID, providerID, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tools": tools}, http.StatusOK)
		return
	}
	if suffix == "health" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported provider health method", nil), http.StatusMethodNotAllowed)
			return
		}
		traceID := contracts.TraceID(r.URL.Query().Get("trace_id"))
		provider, err := appCore.ToolCatalog.CheckProviderHealthForTrace(r.Context(), caller.TenantID, providerID, caller.CallerID, traceID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
		return
	}
	if suffix != "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown tool provider resource path", map[string]any{"path": path}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		provider, ok := appCore.ToolCatalog.GetProvider(caller.TenantID, providerID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool provider json")
		if !ok {
			return
		}
		provider := parseToolProviderPayload(payload)
		provider.TenantID = caller.TenantID
		provider.ProviderID = providerID
		updated, err := appCore.ToolCatalog.UpsertProvider(r.Context(), provider, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": updated}, http.StatusOK)
	case http.MethodDelete:
		provider, ok := appCore.ToolCatalog.GetProvider(caller.TenantID, providerID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID}), http.StatusNotFound)
			return
		}
		provider.Status = toolcatalog.StatusDisabled
		updated, err := appCore.ToolCatalog.UpsertProvider(r.Context(), provider, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool provider method", nil), http.StatusMethodNotAllowed)
	}
}

func handleToolGroups(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"groups": appCore.ToolCatalog.ListGroups(caller.TenantID)}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid tool group json")
		if !ok {
			return
		}
		group := parseToolGroupPayload(payload)
		group.TenantID = caller.TenantID
		created, err := appCore.ToolCatalog.UpsertGroup(r.Context(), group, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"group": created}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool groups method", nil), http.StatusMethodNotAllowed)
	}
}

func handleToolGroupResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, groupID string) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	if groupID == "" || strings.Contains(groupID, "/") {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown tool group resource path", map[string]any{"path": groupID}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		group, ok := appCore.ToolCatalog.GetGroup(caller.TenantID, groupID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool group not found", map[string]any{"group_id": groupID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"group": group}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool group json")
		if !ok {
			return
		}
		group := parseToolGroupPayload(payload)
		group.TenantID = caller.TenantID
		group.GroupID = groupID
		updated, err := appCore.ToolCatalog.UpsertGroup(r.Context(), group, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"group": updated}, http.StatusOK)
	case http.MethodDelete:
		group, ok := appCore.ToolCatalog.GetGroup(caller.TenantID, groupID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool group not found", map[string]any{"group_id": groupID}), http.StatusNotFound)
			return
		}
		group.Status = toolcatalog.StatusDisabled
		updated, err := appCore.ToolCatalog.UpsertGroup(r.Context(), group, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"group": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool group method", nil), http.StatusMethodNotAllowed)
	}
}

func handleToolManifests(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"tools": appCore.ToolCatalog.ListManifests(caller.TenantID)}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid tool manifest json")
		if !ok {
			return
		}
		manifest, err := parseToolManifestPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		manifest.TenantID = caller.TenantID
		created, err := appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tool": created}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool manifests method", nil), http.StatusMethodNotAllowed)
	}
}

func handleToolManifestResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, toolID string) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	if toolID == "" || strings.Contains(toolID, "/") {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown tool manifest resource path", map[string]any{"path": toolID}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		manifest, ok := appCore.ToolCatalog.GetManifest(caller.TenantID, toolID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool manifest not found", map[string]any{"tool_id": toolID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"tool": manifest}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool manifest json")
		if !ok {
			return
		}
		manifest, err := parseToolManifestPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		manifest.TenantID = caller.TenantID
		manifest.ToolID = toolID
		updated, err := appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tool": updated}, http.StatusOK)
	case http.MethodDelete:
		manifest, ok := appCore.ToolCatalog.GetManifest(caller.TenantID, toolID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool manifest not found", map[string]any{"tool_id": toolID}), http.StatusNotFound)
			return
		}
		manifest.Status = toolcatalog.StatusDisabled
		updated, err := appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tool": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool manifest method", nil), http.StatusMethodNotAllowed)
	}
}

func parseToolProviderPayload(payload map[string]any) toolcatalog.ToolProvider {
	source := payload
	if raw, ok := payload["provider"].(map[string]any); ok {
		source = raw
	}
	return toolcatalog.ToolProvider{
		ProviderID:      payloadString(source, "provider_id"),
		ProviderType:    payloadString(source, "provider_type"),
		Name:            payloadString(source, "name"),
		Description:     payloadString(source, "description"),
		Endpoint:        payloadString(source, "endpoint"),
		Status:          payloadString(source, "status"),
		HealthStatus:    payloadString(source, "health_status"),
		LastHealthError: payloadString(source, "last_health_error"),
		AuthRef:         payloadString(source, "auth_ref"),
		TimeoutMS:       intValue(source["timeout_ms"], 0),
		RetryMax:        intValue(source["retry_max"], 0),
		Version:         payloadString(source, "version"),
	}
}

func parseToolGroupPayload(payload map[string]any) toolcatalog.ToolGroup {
	source := payload
	if raw, ok := payload["group"].(map[string]any); ok {
		source = raw
	}
	return toolcatalog.ToolGroup{
		GroupID:     payloadString(source, "group_id"),
		Name:        payloadString(source, "name"),
		Description: payloadString(source, "description"),
		Status:      payloadString(source, "status"),
		Version:     payloadString(source, "version"),
	}
}

func toolProviderUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	provider := toolcatalog.ToolProvider{
		TenantID:        caller.TenantID,
		ProviderID:      payloadString(envelope.Payload, "provider_id"),
		ProviderType:    payloadString(envelope.Payload, "provider_type"),
		Name:            payloadString(envelope.Payload, "name"),
		Description:     payloadString(envelope.Payload, "description"),
		Endpoint:        payloadString(envelope.Payload, "endpoint"),
		Status:          payloadString(envelope.Payload, "status"),
		HealthStatus:    payloadString(envelope.Payload, "health_status"),
		LastHealthError: payloadString(envelope.Payload, "last_health_error"),
		AuthRef:         payloadString(envelope.Payload, "auth_ref"),
		TimeoutMS:       intValue(envelope.Payload["timeout_ms"], 0),
		RetryMax:        intValue(envelope.Payload["retry_max"], 0),
		Version:         payloadString(envelope.Payload, "version"),
	}
	return appCore.ToolCatalog.UpsertProvider(r.Context(), provider, caller.CallerID)
}

func toolProviderSync(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	providerID := strings.TrimSpace(payloadString(envelope.Payload, "provider_id"))
	if providerID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool.provider.sync requires provider_id", nil)
	}
	tools, err := appCore.ToolCatalog.SyncProviderCatalog(r.Context(), caller.TenantID, providerID, caller.CallerID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tools": tools}, nil
}

func toolGroupUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	group := toolcatalog.ToolGroup{
		TenantID:    caller.TenantID,
		GroupID:     payloadString(envelope.Payload, "group_id"),
		Name:        payloadString(envelope.Payload, "name"),
		Description: payloadString(envelope.Payload, "description"),
		Status:      payloadString(envelope.Payload, "status"),
		Version:     payloadString(envelope.Payload, "version"),
	}
	if raw, ok := envelope.Payload["group"].(map[string]any); ok {
		group.GroupID = payloadString(raw, "group_id")
		group.Name = payloadString(raw, "name")
		group.Description = payloadString(raw, "description")
		group.Status = payloadString(raw, "status")
		group.Version = payloadString(raw, "version")
	}
	group.TenantID = caller.TenantID
	return appCore.ToolCatalog.UpsertGroup(r.Context(), group, caller.CallerID)
}

func toolGroupList(_ *http.Request, appCore *core.Core, _ contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	return map[string]any{"groups": appCore.ToolCatalog.ListGroups(caller.TenantID)}, nil
}

func toolManifestUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	manifest, err := parseToolManifestPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	manifest.TenantID = caller.TenantID
	return appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
}

func toolManifestList(_ *http.Request, appCore *core.Core, _ contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	return map[string]any{"tools": appCore.ToolCatalog.ListManifests(caller.TenantID)}, nil
}

func parseToolManifestPayload(payload map[string]any) (toolcatalog.ToolManifest, error) {
	source := payload
	if raw, ok := payload["manifest"].(map[string]any); ok {
		source = raw
	}
	manifest := toolcatalog.ToolManifest{
		ToolID:       payloadString(source, "tool_id"),
		GroupID:      payloadString(source, "group_id"),
		Name:         payloadString(source, "name"),
		Description:  payloadString(source, "description"),
		WhenToUse:    stringSlice(source["when_to_use"]),
		InputSchema:  parseMetadata(source["input_schema"]),
		OutputSchema: parseMetadata(source["output_schema"]),
		RiskLevel:    contracts.RiskLevel(payloadString(source, "risk_level")),
		Visibility:   contracts.ToolVisibility(payloadString(source, "visibility")),
		Executor:     parseToolExecutorSpec(source["executor"]),
		Status:       payloadString(source, "status"),
		Version:      payloadString(source, "version"),
	}
	if executionProfile := payloadString(source, "execution_profile"); executionProfile != "" {
		manifest.ExecutionProfile = executionProfile
	}
	if manifest.Executor.Type == "" {
		manifest.Executor.Type = payloadString(source, "executor_type")
		manifest.Executor.ProviderID = payloadString(source, "provider_id")
		manifest.Executor.Operation = payloadString(source, "operation")
		manifest.Executor.URL = payloadString(source, "url")
		manifest.Executor.Method = payloadString(source, "method")
		manifest.Executor.Headers = parseStringMap(source["headers"])
	}
	return manifest, nil
}

func parseToolExecutorSpec(value any) toolcatalog.ExecutorSpec {
	raw, ok := value.(map[string]any)
	if !ok {
		return toolcatalog.ExecutorSpec{}
	}
	return toolcatalog.ExecutorSpec{
		Type:       payloadString(raw, "type"),
		ProviderID: payloadString(raw, "provider_id"),
		Operation:  payloadString(raw, "operation"),
		URL:        payloadString(raw, "url"),
		Method:     payloadString(raw, "method"),
		Headers:    parseStringMap(raw["headers"]),
	}
}
