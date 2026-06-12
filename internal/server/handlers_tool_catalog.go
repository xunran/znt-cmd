package server

import (
	"net/http"
	"strconv"
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
		filter := parseToolProviderListQuery(r)
		writeJSON(w, map[string]any{"providers": appCore.ToolCatalog.ListProvidersFiltered(caller.TenantID, filter)}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid tool provider json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "provider"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedToolProviderPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		provider := parseToolProviderPayload(payload)
		if isManagedAdapterProviderType(provider.ProviderType) {
			writeRuntimeError(w, managedAdapterProviderPublicWriteError(provider.ProviderType))
			return
		}
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
	if suffix == "operations" || strings.HasPrefix(suffix, "operations/") {
		handleToolProviderOperations(w, r, appCore, caller, providerID, strings.Trim(strings.TrimPrefix(suffix, "operations"), "/"))
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
	case http.MethodPut:
		payload, ok := decodeMapPayload(w, r, "invalid tool provider json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "provider"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedToolProviderPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolProviderPayloadID(source, providerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		provider := parseToolProviderPayload(payload)
		if isManagedAdapterProviderType(provider.ProviderType) {
			writeRuntimeError(w, managedAdapterProviderPublicWriteError(provider.ProviderType))
			return
		}
		provider.TenantID = caller.TenantID
		provider.ProviderID = providerID
		updated, err := appCore.ToolCatalog.UpsertProvider(r.Context(), provider, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": updated}, http.StatusOK)
	case http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool provider json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "provider"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedToolProviderPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolProviderPayloadID(source, providerID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		existing, ok := appCore.ToolCatalog.GetProvider(caller.TenantID, providerID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID}), http.StatusNotFound)
			return
		}
		if isManagedAdapterProviderType(existing.ProviderType) {
			writeRuntimeError(w, managedAdapterProviderPublicWriteError(existing.ProviderType))
			return
		}
		if _, ok := source["provider_type"]; ok {
			payloadProviderType := payloadString(source, "provider_type")
			if isManagedAdapterProviderType(payloadProviderType) {
				writeRuntimeError(w, managedAdapterProviderPublicWriteError(payloadProviderType))
				return
			}
		}
		provider := mergeToolProviderPatch(existing, source)
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
		if isManagedAdapterProviderType(provider.ProviderType) {
			writeRuntimeError(w, managedAdapterProviderPublicWriteError(provider.ProviderType))
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

func handleToolProviderOperations(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, providerID string, path string) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	operationID, suffix, _ := strings.Cut(path, "/")
	operationID = strings.TrimSpace(operationID)
	if operationID == "from-resource" && suffix == "" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported adapter operation resource generation method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid adapter operation resource generation json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "operation"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		request := parseAdapterOperationFromResourcePayload(payload)
		operation, err := appCore.ToolCatalog.UpsertAdapterOperationFromResource(r.Context(), caller.TenantID, providerID, request, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"operation": operation}, http.StatusCreated)
		return
	}
	if operationID == "" {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{"operations": appCore.ToolCatalog.ListAdapterOperations(caller.TenantID, providerID)}, http.StatusOK)
		case http.MethodPost:
			payload, ok := decodeMapPayload(w, r, "invalid adapter operation json")
			if !ok {
				return
			}
			if err := rejectResourceEnvelopePayload(payload, "operation"); err != nil {
				writeRuntimeError(w, err)
				return
			}
			source := payload
			if err := validateAdapterOperationCreatePayloadID(source, providerID); err != nil {
				writeRuntimeError(w, err)
				return
			}
			operation := parseAdapterOperationPayload(payload)
			operation.TenantID = caller.TenantID
			operation.ProviderID = providerID
			created, err := appCore.ToolCatalog.UpsertAdapterOperation(r.Context(), operation, caller.CallerID)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"operation": created}, http.StatusCreated)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported adapter operations method", nil), http.StatusMethodNotAllowed)
		}
		return
	}
	if suffix == "publish" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported adapter operation publish method", nil), http.StatusMethodNotAllowed)
			return
		}
		manifest, err := appCore.ToolCatalog.PublishAdapterOperation(r.Context(), caller.TenantID, providerID, operationID, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"tool": manifest}, http.StatusOK)
		return
	}
	if suffix == "test" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported adapter operation test method", nil), http.StatusMethodNotAllowed)
			return
		}
		operation, ok := appCore.ToolCatalog.GetAdapterOperation(caller.TenantID, providerID, operationID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": providerID, "operation_id": operationID}), http.StatusNotFound)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid adapter operation test json")
		if !ok {
			return
		}
		args := parseMetadata(payload["arguments"])
		if len(args) == 0 {
			args = parseMetadata(payload["payload"])
		}
		output, err := appCore.ToolCatalog.TestAdapterOperation(r.Context(), caller.TenantID, operation, args, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"operation": operation, "output": output}, http.StatusOK)
		return
	}
	if suffix != "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown adapter operation resource path", map[string]any{"path": path}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		operation, ok := appCore.ToolCatalog.GetAdapterOperation(caller.TenantID, providerID, operationID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": providerID, "operation_id": operationID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"operation": operation}, http.StatusOK)
	case http.MethodPut:
		payload, ok := decodeMapPayload(w, r, "invalid adapter operation json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "operation"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateAdapterOperationPathPayloadID(source, providerID, operationID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		operation := parseAdapterOperationPayload(payload)
		operation.TenantID = caller.TenantID
		operation.ProviderID = providerID
		operation.OperationID = operationID
		updated, err := appCore.ToolCatalog.UpsertAdapterOperation(r.Context(), operation, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"operation": updated}, http.StatusOK)
	case http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid adapter operation json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "operation"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateAdapterOperationPathPayloadID(source, providerID, operationID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		existing, ok := appCore.ToolCatalog.GetAdapterOperation(caller.TenantID, providerID, operationID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": providerID, "operation_id": operationID}), http.StatusNotFound)
			return
		}
		operation := mergeAdapterOperationPatch(existing, source)
		updated, err := appCore.ToolCatalog.UpsertAdapterOperation(r.Context(), operation, caller.CallerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"operation": updated}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported adapter operation method", nil), http.StatusMethodNotAllowed)
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
		if err := rejectResourceEnvelopePayload(payload, "group"); err != nil {
			writeRuntimeError(w, err)
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
	case http.MethodPut:
		payload, ok := decodeMapPayload(w, r, "invalid tool group json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "group"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolGroupPayloadID(source, groupID); err != nil {
			writeRuntimeError(w, err)
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
	case http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool group json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "group"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolGroupPayloadID(source, groupID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		existing, ok := appCore.ToolCatalog.GetGroup(caller.TenantID, groupID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool group not found", map[string]any{"group_id": groupID}), http.StatusNotFound)
			return
		}
		group := mergeToolGroupPatch(existing, source)
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
		filter := parseToolManifestListQuery(r)
		writeJSON(w, map[string]any{"tools": appCore.ToolCatalog.ListManifestsFiltered(caller.TenantID, filter)}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid tool manifest json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "manifest", "tool"); err != nil {
			writeRuntimeError(w, err)
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
	case http.MethodPut:
		payload, ok := decodeMapPayload(w, r, "invalid tool manifest json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "manifest", "tool"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolManifestPayloadID(source, toolID); err != nil {
			writeRuntimeError(w, err)
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
	case http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid tool manifest json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "manifest", "tool"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateToolManifestPayloadID(source, toolID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		existing, ok := appCore.ToolCatalog.GetManifest(caller.TenantID, toolID)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool manifest not found", map[string]any{"tool_id": toolID}), http.StatusNotFound)
			return
		}
		manifest, err := mergeToolManifestPatch(existing, source)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
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
	return toolcatalog.ToolProvider{
		ProviderID:          payloadString(payload, "provider_id"),
		ProviderType:        payloadString(payload, "provider_type"),
		Name:                payloadString(payload, "name"),
		Description:         payloadString(payload, "description"),
		ServiceConnectionID: payloadString(payload, "service_connection_id"),
		Status:              payloadString(payload, "status"),
		Version:             payloadString(payload, "version"),
	}
}

func parseToolProviderListQuery(r *http.Request) toolcatalog.ProviderListFilter {
	query := r.URL.Query()
	return toolcatalog.ProviderListFilter{
		Query:          strings.TrimSpace(query.Get("q")),
		ProviderType:   strings.TrimSpace(query.Get("provider_type")),
		Status:         strings.TrimSpace(query.Get("status")),
		HealthStatus:   strings.TrimSpace(query.Get("health_status")),
		IncludeManaged: queryBool(r, "include_managed"),
		PageSize:       queryPageSize(r, 200),
		Cursor:         strings.TrimSpace(query.Get("cursor")),
	}
}

func parseToolManifestListQuery(r *http.Request) toolcatalog.ManifestListFilter {
	query := r.URL.Query()
	return toolcatalog.ManifestListFilter{
		Query:        strings.TrimSpace(query.Get("q")),
		ProviderID:   strings.TrimSpace(query.Get("provider_id")),
		ExecutorType: strings.TrimSpace(query.Get("executor_type")),
		Status:       strings.TrimSpace(query.Get("status")),
		RiskLevel:    contracts.RiskLevel(strings.TrimSpace(query.Get("risk_level"))),
		Visibility:   contracts.ToolVisibility(strings.TrimSpace(query.Get("visibility"))),
		PageSize:     queryPageSize(r, 200),
		Cursor:       strings.TrimSpace(query.Get("cursor")),
	}
}

func parseToolManifestListPayload(payload map[string]any) toolcatalog.ManifestListFilter {
	source := payload
	if raw, ok := payload["filter"].(map[string]any); ok {
		source = raw
	}
	return toolcatalog.ManifestListFilter{
		Query:        payloadString(source, "q"),
		ProviderID:   payloadString(source, "provider_id"),
		ExecutorType: payloadString(source, "executor_type"),
		Status:       payloadString(source, "status"),
		RiskLevel:    contracts.RiskLevel(payloadString(source, "risk_level")),
		Visibility:   contracts.ToolVisibility(payloadString(source, "visibility")),
		PageSize:     payloadInt(source, "page_size"),
		Cursor:       payloadString(source, "cursor"),
	}
}

func queryPageSize(r *http.Request, max int) int {
	pageSize, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page_size")))
	if err != nil || pageSize <= 0 {
		return 0
	}
	if max > 0 && pageSize > max {
		return max
	}
	return pageSize
}

func queryBool(r *http.Request, key string) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(key)))
	return value == "true" || value == "1" || value == "yes"
}

func validateToolProviderPayloadID(source map[string]any, providerID string) error {
	if value := strings.TrimSpace(payloadString(source, "provider_id")); value != "" && value != providerID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id in body must match path", map[string]any{"provider_id": providerID, "body_provider_id": value})
	}
	return nil
}

func mergeToolProviderPatch(existing toolcatalog.ToolProvider, source map[string]any) toolcatalog.ToolProvider {
	provider := existing
	if _, ok := source["provider_type"]; ok {
		provider.ProviderType = payloadString(source, "provider_type")
	}
	if _, ok := source["name"]; ok {
		provider.Name = payloadString(source, "name")
	}
	if _, ok := source["description"]; ok {
		provider.Description = payloadString(source, "description")
	}
	if _, ok := source["service_connection_id"]; ok {
		provider.ServiceConnectionID = payloadString(source, "service_connection_id")
	}
	if _, ok := source["status"]; ok {
		provider.Status = payloadString(source, "status")
	}
	if _, ok := source["version"]; ok {
		provider.Version = payloadString(source, "version")
	}
	return provider
}

func rejectUnsupportedToolProviderPayloadFields(payload map[string]any) error {
	for _, field := range []string{"endpoint", "endpoint_ref", "auth_ref", "secret_ref", "token_ref"} {
		if _, ok := payload[field]; ok {
			return unsupportedToolProviderPayloadFieldError(field)
		}
	}
	for _, field := range []string{"health_status", "last_health_check_at", "last_health_error"} {
		if _, ok := payload[field]; ok {
			return unsupportedToolProviderHealthPayloadFieldError(field)
		}
	}
	return nil
}

func unsupportedToolProviderPayloadFieldError(field string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool provider field is no longer supported; use service_connection_id", map[string]any{"field": field})
}

func unsupportedToolProviderHealthPayloadFieldError(field string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool provider health fields are managed by health checks", map[string]any{"field": field})
}

func isManagedAdapterProviderType(providerType string) bool {
	switch strings.TrimSpace(providerType) {
	case toolcatalog.ProviderTypeHTTPAPIAdapter, toolcatalog.ProviderTypeDatabaseAdapter:
		return true
	default:
		return false
	}
}

func managedAdapterProviderPublicWriteError(providerType string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter providers are internal; create adapter operations instead", map[string]any{"provider_type": strings.TrimSpace(providerType)})
}

func parseAdapterOperationPayload(payload map[string]any) toolcatalog.AdapterOperation {
	readOnly := false
	if _, ok := payload["read_only"]; ok {
		readOnly = payloadBool(payload, "read_only")
	}
	return toolcatalog.AdapterOperation{
		ProviderID:          payloadString(payload, "provider_id"),
		OperationID:         payloadString(payload, "operation_id"),
		ToolID:              payloadString(payload, "tool_id"),
		GroupID:             payloadString(payload, "group_id"),
		Name:                payloadString(payload, "name"),
		Description:         payloadString(payload, "description"),
		WhenToUse:           stringSlice(payload["when_to_use"]),
		ServiceConnectionID: payloadString(payload, "service_connection_id"),
		Method:              payloadString(payload, "method"),
		Path:                payloadString(payload, "path"),
		Headers:             parseStringMap(payload["headers"]),
		InputSchema:         parseMetadata(payload["input_schema"]),
		OutputSchema:        parseMetadata(payload["output_schema"]),
		RequestMapping:      parseMetadata(payload["request_mapping"]),
		ResponseMapping:     parseMetadata(payload["response_mapping"]),
		ResourceID:          payloadString(payload, "resource_id"),
		QueryTemplate:       payloadString(payload, "query_template"),
		ParameterSchema:     parseMetadata(payload["parameter_schema"]),
		MaxRows:             payloadInt(payload, "max_rows"),
		RedactColumns:       stringSlice(payload["redact_columns"]),
		ReadOnly:            readOnly,
		RiskLevel:           contracts.RiskLevel(payloadString(payload, "risk_level")),
		Visibility:          contracts.ToolVisibility(payloadString(payload, "visibility")),
		Status:              payloadString(payload, "status"),
		Version:             payloadString(payload, "version"),
	}
}

func parseAdapterOperationFromResourcePayload(payload map[string]any) toolcatalog.AdapterOperationFromResourceRequest {
	return toolcatalog.AdapterOperationFromResourceRequest{
		ServiceConnectionID: payloadString(payload, "service_connection_id"),
		ResourceID:          payloadString(payload, "resource_id"),
		OperationID:         payloadString(payload, "operation_id"),
		ToolID:              payloadString(payload, "tool_id"),
		GroupID:             payloadString(payload, "group_id"),
		Name:                payloadString(payload, "name"),
		Description:         payloadString(payload, "description"),
		WhenToUse:           stringSlice(payload["when_to_use"]),
		InputSchema:         parseMetadata(payload["input_schema"]),
		OutputSchema:        parseMetadata(payload["output_schema"]),
		QueryTemplate:       payloadString(payload, "query_template"),
		ParameterSchema:     parseMetadata(payload["parameter_schema"]),
		MaxRows:             payloadInt(payload, "max_rows"),
		RedactColumns:       stringSlice(payload["redact_columns"]),
		RiskLevel:           contracts.RiskLevel(payloadString(payload, "risk_level")),
		Visibility:          contracts.ToolVisibility(payloadString(payload, "visibility")),
		Status:              payloadString(payload, "status"),
		Version:             payloadString(payload, "version"),
	}
}

func validateAdapterOperationCreatePayloadID(source map[string]any, providerID string) error {
	if value := strings.TrimSpace(payloadString(source, "provider_id")); value != "" && value != providerID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id in body must match path", map[string]any{"provider_id": providerID, "body_provider_id": value})
	}
	if value := strings.TrimSpace(payloadString(source, "operation_id")); value == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation_id is required", nil)
	}
	return nil
}

func validateAdapterOperationPathPayloadID(source map[string]any, providerID string, operationID string) error {
	if value := strings.TrimSpace(payloadString(source, "provider_id")); value != "" && value != providerID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id in body must match path", map[string]any{"provider_id": providerID, "body_provider_id": value})
	}
	if value := strings.TrimSpace(payloadString(source, "operation_id")); value != "" && value != operationID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation_id in body must match path", map[string]any{"operation_id": operationID, "body_operation_id": value})
	}
	return nil
}

func mergeAdapterOperationPatch(existing toolcatalog.AdapterOperation, source map[string]any) toolcatalog.AdapterOperation {
	operation := existing
	if _, ok := source["tool_id"]; ok {
		operation.ToolID = payloadString(source, "tool_id")
	}
	if _, ok := source["group_id"]; ok {
		operation.GroupID = payloadString(source, "group_id")
	}
	if _, ok := source["name"]; ok {
		operation.Name = payloadString(source, "name")
	}
	if _, ok := source["description"]; ok {
		operation.Description = payloadString(source, "description")
	}
	if _, ok := source["when_to_use"]; ok {
		operation.WhenToUse = stringSlice(source["when_to_use"])
	}
	if _, ok := source["service_connection_id"]; ok {
		operation.ServiceConnectionID = payloadString(source, "service_connection_id")
	}
	if _, ok := source["method"]; ok {
		operation.Method = payloadString(source, "method")
	}
	if _, ok := source["path"]; ok {
		operation.Path = payloadString(source, "path")
	}
	if _, ok := source["headers"]; ok {
		operation.Headers = parseStringMap(source["headers"])
	}
	if _, ok := source["input_schema"]; ok {
		operation.InputSchema = parseMetadata(source["input_schema"])
	}
	if _, ok := source["output_schema"]; ok {
		operation.OutputSchema = parseMetadata(source["output_schema"])
	}
	if _, ok := source["request_mapping"]; ok {
		operation.RequestMapping = parseMetadata(source["request_mapping"])
	}
	if _, ok := source["response_mapping"]; ok {
		operation.ResponseMapping = parseMetadata(source["response_mapping"])
	}
	if _, ok := source["resource_id"]; ok {
		operation.ResourceID = payloadString(source, "resource_id")
	}
	if _, ok := source["query_template"]; ok {
		operation.QueryTemplate = payloadString(source, "query_template")
	}
	if _, ok := source["parameter_schema"]; ok {
		operation.ParameterSchema = parseMetadata(source["parameter_schema"])
	}
	if _, ok := source["max_rows"]; ok {
		operation.MaxRows = payloadInt(source, "max_rows")
	}
	if _, ok := source["redact_columns"]; ok {
		operation.RedactColumns = stringSlice(source["redact_columns"])
	}
	if _, ok := source["read_only"]; ok {
		operation.ReadOnly = payloadBool(source, "read_only")
	}
	if _, ok := source["risk_level"]; ok {
		operation.RiskLevel = contracts.RiskLevel(payloadString(source, "risk_level"))
	}
	if _, ok := source["visibility"]; ok {
		operation.Visibility = contracts.ToolVisibility(payloadString(source, "visibility"))
	}
	if _, ok := source["status"]; ok {
		operation.Status = payloadString(source, "status")
	}
	if _, ok := source["version"]; ok {
		operation.Version = payloadString(source, "version")
	}
	return operation
}

func parseToolGroupPayload(payload map[string]any) toolcatalog.ToolGroup {
	return toolcatalog.ToolGroup{
		GroupID:     payloadString(payload, "group_id"),
		Name:        payloadString(payload, "name"),
		Description: payloadString(payload, "description"),
		Status:      payloadString(payload, "status"),
		Version:     payloadString(payload, "version"),
	}
}

func validateToolGroupPayloadID(source map[string]any, groupID string) error {
	if value := strings.TrimSpace(payloadString(source, "group_id")); value != "" && value != groupID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "group_id in body must match path", map[string]any{"group_id": groupID, "body_group_id": value})
	}
	return nil
}

func mergeToolGroupPatch(existing toolcatalog.ToolGroup, source map[string]any) toolcatalog.ToolGroup {
	group := existing
	if _, ok := source["name"]; ok {
		group.Name = payloadString(source, "name")
	}
	if _, ok := source["description"]; ok {
		group.Description = payloadString(source, "description")
	}
	if _, ok := source["status"]; ok {
		group.Status = payloadString(source, "status")
	}
	if _, ok := source["version"]; ok {
		group.Version = payloadString(source, "version")
	}
	return group
}

func toolProviderUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	if err := rejectResourceEnvelopePayload(envelope.Payload, "provider"); err != nil {
		return nil, err
	}
	if err := rejectUnsupportedToolProviderPayloadFields(envelope.Payload); err != nil {
		return nil, err
	}
	provider := toolcatalog.ToolProvider{
		TenantID:            caller.TenantID,
		ProviderID:          payloadString(envelope.Payload, "provider_id"),
		ProviderType:        payloadString(envelope.Payload, "provider_type"),
		Name:                payloadString(envelope.Payload, "name"),
		Description:         payloadString(envelope.Payload, "description"),
		ServiceConnectionID: payloadString(envelope.Payload, "service_connection_id"),
		Status:              payloadString(envelope.Payload, "status"),
		Version:             payloadString(envelope.Payload, "version"),
	}
	if isManagedAdapterProviderType(provider.ProviderType) {
		return nil, managedAdapterProviderPublicWriteError(provider.ProviderType)
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
	if err := rejectResourceEnvelopePayload(envelope.Payload, "group"); err != nil {
		return nil, err
	}
	group := toolcatalog.ToolGroup{
		TenantID:    caller.TenantID,
		GroupID:     payloadString(envelope.Payload, "group_id"),
		Name:        payloadString(envelope.Payload, "name"),
		Description: payloadString(envelope.Payload, "description"),
		Status:      payloadString(envelope.Payload, "status"),
		Version:     payloadString(envelope.Payload, "version"),
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
	if err := rejectResourceEnvelopePayload(envelope.Payload, "manifest", "tool"); err != nil {
		return nil, err
	}
	manifest, err := parseToolManifestPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	manifest.TenantID = caller.TenantID
	return appCore.ToolCatalog.UpsertManifest(r.Context(), manifest, caller.CallerID)
}

func toolManifestList(_ *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ToolCatalog == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil)
	}
	filter := parseToolManifestListPayload(envelope.Payload)
	return map[string]any{"tools": appCore.ToolCatalog.ListManifestsFiltered(caller.TenantID, filter)}, nil
}

func parseToolManifestPayload(payload map[string]any) (toolcatalog.ToolManifest, error) {
	if err := rejectFlatToolManifestExecutorFields(payload); err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	manifest := toolcatalog.ToolManifest{
		ToolID:       payloadString(payload, "tool_id"),
		GroupID:      payloadString(payload, "group_id"),
		Name:         payloadString(payload, "name"),
		Description:  payloadString(payload, "description"),
		WhenToUse:    stringSlice(payload["when_to_use"]),
		InputSchema:  parseMetadata(payload["input_schema"]),
		OutputSchema: parseMetadata(payload["output_schema"]),
		RiskLevel:    contracts.RiskLevel(payloadString(payload, "risk_level")),
		Visibility:   contracts.ToolVisibility(payloadString(payload, "visibility")),
		Status:       payloadString(payload, "status"),
		Version:      payloadString(payload, "version"),
	}
	executor, err := parseToolExecutorSpec(payload["executor"])
	if err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	manifest.Executor = executor
	if executionProfile := payloadString(payload, "execution_profile"); executionProfile != "" {
		manifest.ExecutionProfile = executionProfile
	}
	return manifest, nil
}

func validateToolManifestPayloadID(source map[string]any, toolID string) error {
	if value := strings.TrimSpace(payloadString(source, "tool_id")); value != "" && value != toolID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool_id in body must match path", map[string]any{"tool_id": toolID, "body_tool_id": value})
	}
	return nil
}

func rejectFlatToolManifestExecutorFields(source map[string]any) error {
	for _, field := range []string{"executor_type", "provider_id", "operation"} {
		if _, ok := source[field]; ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool manifest executor must be submitted through executor object", map[string]any{"field": field})
		}
	}
	return nil
}

func mergeToolManifestPatch(existing toolcatalog.ToolManifest, source map[string]any) (toolcatalog.ToolManifest, error) {
	if err := rejectFlatToolManifestExecutorFields(source); err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	manifest := existing
	if _, ok := source["group_id"]; ok {
		manifest.GroupID = payloadString(source, "group_id")
	}
	if _, ok := source["name"]; ok {
		manifest.Name = payloadString(source, "name")
	}
	if _, ok := source["description"]; ok {
		manifest.Description = payloadString(source, "description")
	}
	if _, ok := source["when_to_use"]; ok {
		manifest.WhenToUse = stringSlice(source["when_to_use"])
	}
	if _, ok := source["input_schema"]; ok {
		manifest.InputSchema = parseMetadata(source["input_schema"])
	}
	if _, ok := source["output_schema"]; ok {
		manifest.OutputSchema = parseMetadata(source["output_schema"])
	}
	if _, ok := source["risk_level"]; ok {
		manifest.RiskLevel = contracts.RiskLevel(payloadString(source, "risk_level"))
	}
	if _, ok := source["visibility"]; ok {
		manifest.Visibility = contracts.ToolVisibility(payloadString(source, "visibility"))
	}
	if _, ok := source["execution_profile"]; ok {
		manifest.ExecutionProfile = payloadString(source, "execution_profile")
	}
	if _, ok := source["executor"]; ok {
		executor, err := parseToolExecutorSpec(source["executor"])
		if err != nil {
			return toolcatalog.ToolManifest{}, err
		}
		manifest.Executor = executor
	}
	if _, ok := source["status"]; ok {
		manifest.Status = payloadString(source, "status")
	}
	if _, ok := source["version"]; ok {
		manifest.Version = payloadString(source, "version")
	}
	return manifest, nil
}

func parseToolExecutorSpec(value any) (toolcatalog.ExecutorSpec, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return toolcatalog.ExecutorSpec{}, nil
	}
	for field := range raw {
		if field != "type" && field != "provider_id" && field != "operation" {
			return toolcatalog.ExecutorSpec{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool executor field is not supported; use provider_id and operation", map[string]any{"field": field})
		}
	}
	return toolcatalog.ExecutorSpec{
		Type:       payloadString(raw, "type"),
		ProviderID: payloadString(raw, "provider_id"),
		Operation:  payloadString(raw, "operation"),
	}, nil
}
