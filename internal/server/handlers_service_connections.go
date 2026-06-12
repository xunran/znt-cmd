package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	tracequery "znt/internal/governance/trace"
	serviceconnection "znt/internal/serviceconnection"
	toolcatalog "znt/internal/tool/catalog"
)

type serviceConnectionUsageView struct {
	TenantID     contracts.TenantID               `json:"tenant_id"`
	ConnectionID string                           `json:"connection_id"`
	TraceID      contracts.TraceID                `json:"trace_id,omitempty"`
	From         *time.Time                       `json:"from,omitempty"`
	To           *time.Time                       `json:"to,omitempty"`
	Summary      serviceConnectionUsageSummary    `json:"summary"`
	Providers    []serviceConnectionUsageProvider `json:"providers"`
	Tools        []serviceConnectionUsageTool     `json:"tools"`
	RecentEvents []contracts.TraceEvent           `json:"recent_events,omitempty"`
}

type serviceConnectionUsageSummary struct {
	ProvidersTotal      int        `json:"providers_total"`
	OperationsTotal     int        `json:"operations_total"`
	ToolsTotal          int        `json:"tools_total"`
	TraceEventsTotal    int        `json:"trace_events_total"`
	InvocationsTotal    int        `json:"invocations_total"`
	CompletionsTotal    int        `json:"completions_total"`
	FailuresTotal       int        `json:"failures_total"`
	HealthChecksTotal   int        `json:"health_checks_total"`
	HealthFailuresTotal int        `json:"health_failures_total"`
	LatencyMS           int        `json:"latency_ms"`
	AverageLatencyMS    int        `json:"average_latency_ms"`
	LastEventAt         *time.Time `json:"last_event_at,omitempty"`
	ErrorCodes          []string   `json:"error_codes"`
	HealthStatuses      []string   `json:"health_statuses"`
}

type serviceConnectionUsageProvider struct {
	ProviderID          string                         `json:"provider_id"`
	ProviderType        string                         `json:"provider_type"`
	Name                string                         `json:"name"`
	Status              string                         `json:"status"`
	HealthStatus        string                         `json:"health_status"`
	ServiceConnectionID string                         `json:"service_connection_id,omitempty"`
	TraceEvidence       serviceConnectionTraceEvidence `json:"trace_evidence"`
}

type serviceConnectionUsageTool struct {
	ToolID        string                         `json:"tool_id"`
	Name          string                         `json:"name"`
	ProviderID    string                         `json:"provider_id,omitempty"`
	ExecutorType  string                         `json:"executor_type"`
	Operation     string                         `json:"operation,omitempty"`
	Status        string                         `json:"status"`
	TraceEvidence serviceConnectionTraceEvidence `json:"trace_evidence"`
}

type serviceConnectionTraceEvidence struct {
	EventsTotal         int        `json:"events_total"`
	InvocationsTotal    int        `json:"invocations_total"`
	CompletionsTotal    int        `json:"completions_total"`
	FailuresTotal       int        `json:"failures_total"`
	HealthChecksTotal   int        `json:"health_checks_total"`
	HealthFailuresTotal int        `json:"health_failures_total"`
	LatencyMS           int        `json:"latency_ms"`
	AverageLatencyMS    int        `json:"average_latency_ms"`
	LastEventAt         *time.Time `json:"last_event_at,omitempty"`
	ErrorCodes          []string   `json:"error_codes"`
	HealthStatuses      []string   `json:"health_statuses"`
	latencySamples      int
}

func handleServiceConnections(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.ServiceConnections == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		filter := serviceconnection.ListFilter{
			ConnectionType: strings.TrimSpace(r.URL.Query().Get("connection_type")),
			Status:         strings.TrimSpace(r.URL.Query().Get("status")),
			HealthStatus:   strings.TrimSpace(r.URL.Query().Get("health_status")),
			Environment:    strings.TrimSpace(r.URL.Query().Get("environment")),
			Query:          strings.TrimSpace(r.URL.Query().Get("q")),
			PageSize:       queryPageSize(r, 200),
			Cursor:         strings.TrimSpace(r.URL.Query().Get("cursor")),
		}
		connections, err := appCore.ServiceConnections.List(r.Context(), caller.TenantID, filter)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connections": connections}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid service connection json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "connection"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedServiceConnectionPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		connection := parseServiceConnectionPayload(payload)
		connection.TenantID = caller.TenantID
		created, err := appCore.ServiceConnections.Upsert(r.Context(), connection)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": created}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connections method", nil), http.StatusMethodNotAllowed)
	}
}

func handleServiceConnectionTemplates(w http.ResponseWriter, r *http.Request, appCore *core.Core, _ auth.CallerIdentity) {
	if appCore.ServiceConnections == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection templates method", nil), http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{"templates": appCore.ServiceConnections.Templates()}, http.StatusOK)
}

func handleServiceConnectionResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.ServiceConnections == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil))
		return
	}
	connectionID, suffix, _ := strings.Cut(path, "/")
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "connection_id is required", nil), http.StatusBadRequest)
		return
	}
	switch suffix {
	case "":
		handleServiceConnectionByID(w, r, appCore, caller, connectionID)
	case "test":
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection test method", nil), http.StatusMethodNotAllowed)
			return
		}
		connection, resources, err := appCore.ServiceConnections.Test(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": connection, "resources": resources}, http.StatusOK)
	case "enable":
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection enable method", nil), http.StatusMethodNotAllowed)
			return
		}
		connection, err := appCore.ServiceConnections.Enable(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": connection}, http.StatusOK)
	case "disable":
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection disable method", nil), http.StatusMethodNotAllowed)
			return
		}
		connection, err := appCore.ServiceConnections.Disable(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": connection}, http.StatusOK)
	case "resources":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection resources method", nil), http.StatusMethodNotAllowed)
			return
		}
		resources, err := appCore.ServiceConnections.ListResources(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"resources": resources}, http.StatusOK)
	case "health-events":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection health events method", nil), http.StatusMethodNotAllowed)
			return
		}
		connection, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
			return
		}
		events, err := appCore.ServiceConnections.ListHealthEvents(r.Context(), caller.TenantID, connectionID, serviceConnectionQueryLimit(r, 50, 200))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": connection, "health_events": events}, http.StatusOK)
	case "impact":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection impact method", nil), http.StatusMethodNotAllowed)
			return
		}
		connection, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
			return
		}
		if appCore.ToolCatalog == nil {
			writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
			return
		}
		impact := appCore.ToolCatalog.ServiceConnectionImpact(caller.TenantID, connectionID)
		writeJSON(w, map[string]any{"connection": connection, "impact": impact}, http.StatusOK)
	case "usage":
		handleServiceConnectionUsage(w, r, appCore, caller, connectionID)
	case "secret-rotations":
		handleServiceConnectionSecretRotations(w, r, appCore, caller, connectionID)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown service connection resource path", map[string]any{"path": path}), http.StatusNotFound)
	}
}

func handleServiceConnectionByID(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, connectionID string) {
	switch r.Method {
	case http.MethodGet:
		connection, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"connection": connection}, http.StatusOK)
	case http.MethodPut:
		payload, ok := decodeMapPayload(w, r, "invalid service connection json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "connection"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedServiceConnectionPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateServiceConnectionPayloadID(source, connectionID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		connection := parseServiceConnectionPayload(payload)
		connection.TenantID = caller.TenantID
		connection.ConnectionID = connectionID
		updated, err := appCore.ServiceConnections.Upsert(r.Context(), connection)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": updated}, http.StatusOK)
	case http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid service connection json")
		if !ok {
			return
		}
		if err := rejectResourceEnvelopePayload(payload, "connection"); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := rejectUnsupportedServiceConnectionPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		source := payload
		if err := validateServiceConnectionPayloadID(source, connectionID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := validateServiceConnectionPatchAuthFields(source); err != nil {
			writeRuntimeError(w, err)
			return
		}
		existing, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
			return
		}
		connection := mergeServiceConnectionPatch(existing, source)
		updated, err := appCore.ServiceConnections.Upsert(r.Context(), connection)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": updated}, http.StatusOK)
	case http.MethodDelete:
		impact := appCore.ToolCatalog.ServiceConnectionImpact(caller.TenantID, connectionID)
		if serviceConnectionImpactHasDependencies(impact) {
			writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection has dependent tool providers or tools; remove dependencies before deleting", map[string]any{
				"connection_id": connectionID,
				"impact":        impact.Summary,
			}))
			return
		}
		if err := appCore.ServiceConnections.Delete(r.Context(), caller.TenantID, connectionID); err != nil {
			writeRuntimeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection method", nil), http.StatusMethodNotAllowed)
	}
}

func serviceConnectionImpactHasDependencies(impact toolcatalog.ServiceConnectionImpact) bool {
	return len(impact.Providers) > 0 || len(impact.Operations) > 0 || len(impact.Tools) > 0
}

func parseServiceConnectionPayload(payload map[string]any) serviceconnection.ServiceConnection {
	return serviceconnection.ServiceConnection{
		ConnectionID:       payloadString(payload, "connection_id"),
		Name:               payloadString(payload, "name"),
		ConnectionType:     payloadString(payload, "connection_type"),
		Environment:        payloadString(payload, "environment"),
		Status:             payloadString(payload, "status"),
		Description:        payloadString(payload, "description"),
		BaseURL:            payloadString(payload, "base_url"),
		AuthType:           payloadString(payload, "auth_type"),
		AuthRef:            payloadString(payload, "auth_ref"),
		NetworkScope:       payloadString(payload, "network_scope"),
		TimeoutMS:          intValue(payload["timeout_ms"], 0),
		RetryMax:           intValue(payload["retry_max"], 0),
		HealthCheckEnabled: boolValue(payload["health_check_enabled"], true),
		Metadata:           parseMetadata(payload["metadata"]),
		Version:            payloadString(payload, "version"),
	}
}

func validateServiceConnectionPayloadID(source map[string]any, connectionID string) error {
	if value := strings.TrimSpace(payloadString(source, "connection_id")); value != "" && value != connectionID {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id in body must match path", map[string]any{"connection_id": connectionID, "body_connection_id": value})
	}
	return nil
}

func validateServiceConnectionPatchAuthFields(source map[string]any) error {
	_, hasAuthType := source["auth_type"]
	_, hasAuthRef := source["auth_ref"]
	if hasAuthType != hasAuthRef {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_type and auth_ref must be patched together", nil)
	}
	return nil
}

func mergeServiceConnectionPatch(existing serviceconnection.ServiceConnection, source map[string]any) serviceconnection.ServiceConnection {
	connection := existing
	if _, ok := source["name"]; ok {
		connection.Name = payloadString(source, "name")
	}
	if _, ok := source["connection_type"]; ok {
		connection.ConnectionType = payloadString(source, "connection_type")
	}
	if _, ok := source["environment"]; ok {
		connection.Environment = payloadString(source, "environment")
	}
	if _, ok := source["status"]; ok {
		connection.Status = payloadString(source, "status")
	}
	if _, ok := source["description"]; ok {
		connection.Description = payloadString(source, "description")
	}
	if _, ok := source["base_url"]; ok {
		connection.BaseURL = payloadString(source, "base_url")
	}
	if _, ok := source["auth_type"]; ok {
		connection.AuthType = payloadString(source, "auth_type")
	}
	if _, ok := source["auth_ref"]; ok {
		connection.AuthRef = payloadString(source, "auth_ref")
	}
	if _, ok := source["network_scope"]; ok {
		connection.NetworkScope = payloadString(source, "network_scope")
	}
	if _, ok := source["timeout_ms"]; ok {
		connection.TimeoutMS = intValue(source["timeout_ms"], 0)
	}
	if _, ok := source["retry_max"]; ok {
		connection.RetryMax = intValue(source["retry_max"], 0)
	}
	if _, ok := source["health_check_enabled"]; ok {
		connection.HealthCheckEnabled = boolValue(source["health_check_enabled"], true)
	}
	if _, ok := source["metadata"]; ok {
		connection.Metadata = parseMetadata(source["metadata"])
	}
	if _, ok := source["version"]; ok {
		connection.Version = payloadString(source, "version")
	}
	return connection
}

func rejectUnsupportedServiceConnectionPayloadFields(payload map[string]any) error {
	for _, field := range []string{"secret_ref", "token_ref"} {
		if _, ok := payload[field]; ok {
			return unsupportedServiceConnectionPayloadFieldError(field)
		}
	}
	for _, field := range []string{"health_status", "last_health_at", "last_health_error"} {
		if _, ok := payload[field]; ok {
			return unsupportedServiceConnectionHealthPayloadFieldError(field)
		}
	}
	return nil
}

func unsupportedServiceConnectionPayloadFieldError(field string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service connection field is no longer supported; use auth_ref", map[string]any{"field": field})
}

func unsupportedServiceConnectionHealthPayloadFieldError(field string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service connection health fields are managed by connection tests", map[string]any{"field": field})
}

func handleServiceConnectionUsage(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, connectionID string) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection usage method", nil), http.StatusMethodNotAllowed)
		return
	}
	connection, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if !ok {
		writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
		return
	}
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	if appCore.Trace == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace recorder is unavailable", nil))
		return
	}
	traceID := contracts.TraceID(strings.TrimSpace(r.URL.Query().Get("trace_id")))
	from, queryErr := parseServiceConnectionUsageTime(r.URL.Query().Get("from"), "from")
	if queryErr != nil {
		writeError(w, queryErr, http.StatusBadRequest)
		return
	}
	to, queryErr := parseServiceConnectionUsageTime(r.URL.Query().Get("to"), "to")
	if queryErr != nil {
		writeError(w, queryErr, http.StatusBadRequest)
		return
	}
	if from != nil && to != nil && from.After(*to) {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "service connection usage from must be before to", map[string]any{"from": from.Format(time.RFC3339Nano), "to": to.Format(time.RFC3339Nano)}), http.StatusBadRequest)
		return
	}
	var events []contracts.TraceEvent
	if traceID != "" {
		events, err = appCore.Trace.ListByTrace(r.Context(), traceID)
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		var allowed bool
		events, allowed = traceEventsForTenant(events, caller.TenantID)
		if !allowed {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace tenant does not match caller tenant", nil), http.StatusForbidden)
			return
		}
		if from != nil || to != nil {
			events, err = appCore.Trace.List(r.Context(), tracequery.ListFilter{
				TenantID: caller.TenantID,
				TraceID:  traceID,
				From:     from,
				To:       to,
			})
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
		}
	} else {
		events, err = appCore.Trace.List(r.Context(), tracequery.ListFilter{
			TenantID: caller.TenantID,
			From:     from,
			To:       to,
			Limit:    serviceConnectionQueryLimit(r, 500, 2000),
		})
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
	}
	impact := appCore.ToolCatalog.ServiceConnectionImpact(caller.TenantID, connectionID)
	usage := buildServiceConnectionUsage(caller.TenantID, connection, impact, events, traceID, from, to)
	writeJSON(w, map[string]any{"connection": connection, "usage": usage}, http.StatusOK)
}

func handleServiceConnectionSecretRotations(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, connectionID string) {
	switch r.Method {
	case http.MethodGet:
		connection, ok, err := appCore.ServiceConnections.Get(r.Context(), caller.TenantID, connectionID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID}), http.StatusNotFound)
			return
		}
		rotations, err := appCore.ServiceConnections.ListSecretRotations(r.Context(), caller.TenantID, connectionID, serviceConnectionQueryLimit(r, 50, 200))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"connection": connection, "secret_rotations": rotations}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid service connection secret rotation json")
		if !ok {
			return
		}
		if err := rejectUnsupportedServiceConnectionPayloadFields(payload); err != nil {
			writeRuntimeError(w, err)
			return
		}
		request := serviceconnection.ServiceConnectionSecretRotationRequest{
			AuthRef:   payloadString(payload, "auth_ref"),
			AuthType:  payloadString(payload, "auth_type"),
			Reason:    payloadString(payload, "reason"),
			RotatedBy: caller.CallerID,
		}
		connection, rotation, err := appCore.ServiceConnections.RotateSecret(r.Context(), caller.TenantID, connectionID, request)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if appCore.Audit != nil {
			_ = appCore.Audit.Log(r.Context(), contracts.AuditEvent{
				TenantID:     caller.TenantID,
				ActorID:      caller.CallerID,
				ActorType:    caller.CallerType,
				Action:       contracts.AuditServiceConnectionSecretRotated,
				ResourceType: "service_connection",
				ResourceID:   connectionID,
				Decision:     "allowed",
				Reason:       "auth_ref rotated",
				CreatedAt:    time.Now().UTC(),
			})
		}
		writeJSON(w, map[string]any{"connection": connection, "rotation": rotation}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported service connection secret rotations method", nil), http.StatusMethodNotAllowed)
	}
}

func buildServiceConnectionUsage(tenantID contracts.TenantID, connection serviceconnection.ServiceConnection, impact toolcatalog.ServiceConnectionImpact, events []contracts.TraceEvent, traceID contracts.TraceID, from *time.Time, to *time.Time) serviceConnectionUsageView {
	directProviderIDs := map[string]struct{}{}
	operationKeys := map[string]struct{}{}
	toolIDs := map[string]struct{}{}
	for _, provider := range impact.Providers {
		if provider.ServiceConnectionID == connection.ConnectionID {
			directProviderIDs[provider.ProviderID] = struct{}{}
		}
	}
	for _, operation := range impact.Operations {
		operationKeys[serviceConnectionOperationKey(operation.ProviderID, operation.OperationID)] = struct{}{}
	}
	for _, tool := range impact.Tools {
		toolIDs[tool.ToolID] = struct{}{}
	}

	summary := serviceConnectionTraceEvidence{ErrorCodes: []string{}, HealthStatuses: []string{}}
	providerEvidence := map[string]serviceConnectionTraceEvidence{}
	toolEvidence := map[string]serviceConnectionTraceEvidence{}
	recentEvents := make([]contracts.TraceEvent, 0)
	for _, event := range events {
		if event.TenantID != tenantID || !isServiceConnectionTraceEvent(event.Type) {
			continue
		}
		providerID := payloadString(event.Payload, "provider_id")
		toolID := payloadString(event.Payload, "tool_id")
		if !serviceConnectionEventMatches(connection.ConnectionID, event, directProviderIDs, operationKeys, toolIDs) {
			continue
		}
		addServiceConnectionTraceEvent(&summary, event)
		if providerID != "" {
			evidence := providerEvidence[providerID]
			addServiceConnectionTraceEvent(&evidence, event)
			providerEvidence[providerID] = evidence
		}
		if toolID != "" {
			evidence := toolEvidence[toolID]
			addServiceConnectionTraceEvent(&evidence, event)
			toolEvidence[toolID] = evidence
		}
		recentEvents = append(recentEvents, event)
	}
	finalizeServiceConnectionTraceEvidence(&summary)
	sort.SliceStable(recentEvents, func(i, j int) bool {
		if recentEvents[i].CreatedAt.Equal(recentEvents[j].CreatedAt) {
			return recentEvents[i].SpanID < recentEvents[j].SpanID
		}
		return recentEvents[i].CreatedAt.After(recentEvents[j].CreatedAt)
	})
	if len(recentEvents) > 20 {
		recentEvents = recentEvents[:20]
	}

	providers := make([]serviceConnectionUsageProvider, 0, len(impact.Providers))
	for _, provider := range impact.Providers {
		evidence := providerEvidence[provider.ProviderID]
		finalizeServiceConnectionTraceEvidence(&evidence)
		providers = append(providers, serviceConnectionUsageProvider{
			ProviderID:          provider.ProviderID,
			ProviderType:        provider.ProviderType,
			Name:                provider.Name,
			Status:              provider.Status,
			HealthStatus:        provider.HealthStatus,
			ServiceConnectionID: provider.ServiceConnectionID,
			TraceEvidence:       evidence,
		})
	}
	tools := make([]serviceConnectionUsageTool, 0, len(impact.Tools))
	for _, tool := range impact.Tools {
		evidence := toolEvidence[tool.ToolID]
		finalizeServiceConnectionTraceEvidence(&evidence)
		tools = append(tools, serviceConnectionUsageTool{
			ToolID:        tool.ToolID,
			Name:          tool.Name,
			ProviderID:    tool.Executor.ProviderID,
			ExecutorType:  tool.Executor.Type,
			Operation:     tool.Executor.Operation,
			Status:        tool.Status,
			TraceEvidence: evidence,
		})
	}
	return serviceConnectionUsageView{
		TenantID:     tenantID,
		ConnectionID: connection.ConnectionID,
		TraceID:      traceID,
		From:         from,
		To:           to,
		Summary: serviceConnectionUsageSummary{
			ProvidersTotal:      len(impact.Providers),
			OperationsTotal:     len(impact.Operations),
			ToolsTotal:          len(impact.Tools),
			TraceEventsTotal:    summary.EventsTotal,
			InvocationsTotal:    summary.InvocationsTotal,
			CompletionsTotal:    summary.CompletionsTotal,
			FailuresTotal:       summary.FailuresTotal,
			HealthChecksTotal:   summary.HealthChecksTotal,
			HealthFailuresTotal: summary.HealthFailuresTotal,
			LatencyMS:           summary.LatencyMS,
			AverageLatencyMS:    summary.AverageLatencyMS,
			LastEventAt:         summary.LastEventAt,
			ErrorCodes:          append([]string{}, summary.ErrorCodes...),
			HealthStatuses:      append([]string{}, summary.HealthStatuses...),
		},
		Providers:    providers,
		Tools:        tools,
		RecentEvents: recentEvents,
	}
}

func serviceConnectionEventMatches(connectionID string, event contracts.TraceEvent, directProviderIDs map[string]struct{}, operationKeys map[string]struct{}, toolIDs map[string]struct{}) bool {
	if payloadString(event.Payload, "connection_id") == connectionID {
		return true
	}
	providerID := payloadString(event.Payload, "provider_id")
	toolID := payloadString(event.Payload, "tool_id")
	operation := payloadString(event.Payload, "operation")
	if toolID != "" {
		if _, ok := toolIDs[toolID]; ok {
			return true
		}
	}
	if providerID != "" && operation != "" {
		if _, ok := operationKeys[serviceConnectionOperationKey(providerID, operation)]; ok {
			return true
		}
	}
	if providerID != "" {
		if _, ok := directProviderIDs[providerID]; ok {
			return toolID == "" || containsKey(toolIDs, toolID)
		}
	}
	return false
}

func isServiceConnectionTraceEvent(eventType string) bool {
	switch eventType {
	case contracts.TraceToolProviderInvoked, contracts.TraceToolProviderCompleted, contracts.TraceToolProviderFailed, contracts.TraceToolProviderHealthChecked:
		return true
	default:
		return false
	}
}

func addServiceConnectionTraceEvent(evidence *serviceConnectionTraceEvidence, event contracts.TraceEvent) {
	if evidence.ErrorCodes == nil {
		evidence.ErrorCodes = []string{}
	}
	if evidence.HealthStatuses == nil {
		evidence.HealthStatuses = []string{}
	}
	evidence.EventsTotal++
	if !event.CreatedAt.IsZero() && (evidence.LastEventAt == nil || event.CreatedAt.After(*evidence.LastEventAt)) {
		createdAt := event.CreatedAt
		evidence.LastEventAt = &createdAt
	}
	switch event.Type {
	case contracts.TraceToolProviderInvoked:
		evidence.InvocationsTotal++
	case contracts.TraceToolProviderCompleted:
		evidence.CompletionsTotal++
		evidence.LatencyMS += payloadInt(event.Payload, "latency_ms")
		evidence.latencySamples++
	case contracts.TraceToolProviderFailed:
		evidence.FailuresTotal++
		evidence.LatencyMS += payloadInt(event.Payload, "latency_ms")
		evidence.latencySamples++
		if errorCode := payloadString(event.Payload, "error_code"); errorCode != "" {
			evidence.ErrorCodes = appendUniqueString(evidence.ErrorCodes, errorCode)
		}
	case contracts.TraceToolProviderHealthChecked:
		evidence.HealthChecksTotal++
		evidence.LatencyMS += payloadInt(event.Payload, "latency_ms")
		evidence.latencySamples++
		if healthStatus := payloadString(event.Payload, "health_status"); healthStatus != "" {
			evidence.HealthStatuses = appendUniqueString(evidence.HealthStatuses, healthStatus)
			if healthStatus != toolcatalog.HealthHealthy {
				evidence.HealthFailuresTotal++
			}
		}
	}
}

func finalizeServiceConnectionTraceEvidence(evidence *serviceConnectionTraceEvidence) {
	if evidence.ErrorCodes == nil {
		evidence.ErrorCodes = []string{}
	}
	if evidence.HealthStatuses == nil {
		evidence.HealthStatuses = []string{}
	}
	if evidence.latencySamples > 0 {
		evidence.AverageLatencyMS = evidence.LatencyMS / evidence.latencySamples
	}
}

func serviceConnectionOperationKey(providerID string, operationID string) string {
	return providerID + "\x00" + operationID
}

func containsKey(values map[string]struct{}, target string) bool {
	_, ok := values[target]
	return ok
}

func serviceConnectionQueryLimit(r *http.Request, fallback int, max int) int {
	limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if err != nil || limit <= 0 {
		return fallback
	}
	if max > 0 && limit > max {
		return max
	}
	return limit
}

func parseServiceConnectionUsageTime(raw string, name string) (*time.Time, *contracts.RuntimeError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid service connection usage time", map[string]any{"param": name, "value": raw, "format": time.RFC3339Nano})
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
