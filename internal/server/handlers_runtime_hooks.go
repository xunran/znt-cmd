package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/governance/approval"
	runtimehook "znt/internal/runtime/hook"
)

func handleAgentRuntimeHooks(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, parts []string) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	if len(parts) == 1 && parts[0] == "preview" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook preview method", nil), http.StatusMethodNotAllowed)
			return
		}
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook preview json")
		if !ok {
			return
		}
		payload["agent_id"] = string(agentID)
		if payloadString(payload, "agent_version") == "" && r.URL.Query().Get("agent_version") != "" {
			payload["agent_version"] = r.URL.Query().Get("agent_version")
		}
		envelope := contracts.AgentEnvelope{TraceID: contracts.TraceID(payloadString(payload, "trace_id")), Target: contracts.AgentTarget{AgentID: agentID, Version: contracts.AgentVersion(payloadString(payload, "agent_version"))}, Payload: payload}
		result, err := runtimeHookPreview(r, appCore, envelope, caller)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, result, http.StatusOK)
		return
	}
	if len(parts) > 0 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown agent runtime-hooks resource path", map[string]any{"path": strings.Join(parts, "/")}), http.StatusNotFound)
		return
	}
	version := contracts.AgentVersion(r.URL.Query().Get("agent_version"))
	switch r.Method {
	case http.MethodGet:
		bindings, err := appCore.RuntimeHooks.ListBindings(r.Context(), caller.TenantID, agentID, version)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"bindings": bindings}, http.StatusOK)
	case http.MethodPut, http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook binding json")
		if !ok {
			return
		}
		payload["agent_id"] = string(agentID)
		if payloadString(payload, "agent_version") == "" && version != "" {
			payload["agent_version"] = string(version)
		}
		binding, err := parseRuntimeHookBindingPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		binding.TenantID = caller.TenantID
		binding, err = appCore.RuntimeHooks.PrepareBinding(r.Context(), binding)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		approvalResponse, pending, err := runtimeHookBindingApprovalGate(r, appCore, caller, contracts.TraceID(payloadString(payload, "trace_id")), binding, contracts.ApprovalID(payloadString(payload, "approval_id")))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if pending {
			writeJSON(w, approvalResponse, http.StatusOK)
			return
		}
		if err := appCore.RuntimeHooks.UpsertBinding(r.Context(), binding); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"binding": binding}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hooks method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRuntimeHookProviders(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		providers, err := appCore.RuntimeHooks.ListProviders(r.Context(), caller.TenantID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"providers": providers}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook provider json")
		if !ok {
			return
		}
		provider, err := parseRuntimeHookProviderPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		provider.TenantID = caller.TenantID
		if err := appCore.RuntimeHooks.UpsertProvider(r.Context(), provider); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook providers method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRuntimeHookProviderResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, providerID string) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	providerID, suffix, _ := strings.Cut(providerID, "/")
	if providerID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown runtime hook provider resource path", map[string]any{"path": providerID}), http.StatusNotFound)
		return
	}
	if suffix == "health" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook provider health method", nil), http.StatusMethodNotAllowed)
			return
		}
		traceID := contracts.TraceID(r.URL.Query().Get("trace_id"))
		provider, err := appCore.RuntimeHooks.CheckProviderHealthForTrace(r.Context(), caller.TenantID, providerID, traceID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
		return
	}
	if suffix == "catalog" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook provider catalog method", nil), http.StatusMethodNotAllowed)
			return
		}
		provider, catalog, err := appCore.RuntimeHooks.ReadProviderCatalog(r.Context(), caller.TenantID, providerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider, "catalog": catalog}, http.StatusOK)
		return
	}
	if suffix == "catalog/sync" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook provider catalog sync method", nil), http.StatusMethodNotAllowed)
			return
		}
		provider, hooks, err := appCore.RuntimeHooks.SyncProviderCatalog(r.Context(), caller.TenantID, providerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider, "hooks": hooks}, http.StatusOK)
		return
	}
	if suffix != "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown runtime hook provider resource path", map[string]any{"path": providerID + "/" + suffix}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		provider, ok, err := runtimeHookProviderByID(r.Context(), appCore, caller.TenantID, providerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "runtime hook provider not found", map[string]any{"provider_id": providerID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook provider json")
		if !ok {
			return
		}
		provider, err := parseRuntimeHookProviderPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		provider.TenantID = caller.TenantID
		provider.ProviderID = providerID
		if err := appCore.RuntimeHooks.UpsertProvider(r.Context(), provider); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
	case http.MethodDelete:
		provider, ok, err := runtimeHookProviderByID(r.Context(), appCore, caller.TenantID, providerID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "runtime hook provider not found", map[string]any{"provider_id": providerID}), http.StatusNotFound)
			return
		}
		provider.Status = runtimehook.StatusDisabled
		if err := appCore.RuntimeHooks.UpsertProvider(r.Context(), provider); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"provider": provider}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook provider method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRuntimeHookManifests(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		hooks, err := appCore.RuntimeHooks.ListManifests(r.Context(), caller.TenantID, strings.TrimSpace(query.Get("provider_id")), runtimehook.HookPoint(strings.TrimSpace(query.Get("phase"))), strings.TrimSpace(query.Get("status")))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"hooks": hooks}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook manifest json")
		if !ok {
			return
		}
		manifest, err := parseRuntimeHookManifestPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		manifest.TenantID = caller.TenantID
		if err := appCore.RuntimeHooks.UpsertManifest(r.Context(), manifest); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"hook": manifest}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook manifests method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRuntimeHookManifestResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, hookID string) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	path := strings.Trim(hookID, "/")
	if path == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown runtime hook manifest resource path", map[string]any{"path": hookID}), http.StatusNotFound)
		return
	}
	parts := strings.Split(path, "/")
	hookID = parts[0]
	if len(parts) == 2 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook manifest versions method", nil), http.StatusMethodNotAllowed)
			return
		}
		versions, err := appCore.RuntimeHooks.ListManifestVersions(r.Context(), caller.TenantID, hookID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"versions": versions}, http.StatusOK)
		return
	}
	if len(parts) == 3 && parts[1] == "versions" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook manifest version method", nil), http.StatusMethodNotAllowed)
			return
		}
		version, ok, err := appCore.RuntimeHooks.GetManifestVersion(r.Context(), caller.TenantID, hookID, parts[2])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "runtime hook manifest version not found", map[string]any{"hook_id": hookID, "version": parts[2]}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"version": version}, http.StatusOK)
		return
	}
	if len(parts) == 4 && parts[1] == "versions" && parts[3] == "activate" {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook manifest version activate method", nil), http.StatusMethodNotAllowed)
			return
		}
		version, err := appCore.RuntimeHooks.ActivateManifestVersion(r.Context(), caller.TenantID, hookID, parts[2])
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"hook": version.Manifest, "version": version}, http.StatusOK)
		return
	}
	if len(parts) != 1 {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown runtime hook manifest resource path", map[string]any{"path": path}), http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		manifest, ok, err := appCore.RuntimeHooks.GetManifest(r.Context(), caller.TenantID, hookID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "runtime hook manifest not found", map[string]any{"hook_id": hookID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"hook": manifest}, http.StatusOK)
	case http.MethodPut, http.MethodPatch:
		payload, ok := decodeMapPayload(w, r, "invalid runtime hook manifest json")
		if !ok {
			return
		}
		manifest, err := parseRuntimeHookManifestPayload(payload)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		manifest.TenantID = caller.TenantID
		manifest.HookID = hookID
		if err := appCore.RuntimeHooks.UpsertManifest(r.Context(), manifest); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"hook": manifest}, http.StatusOK)
	case http.MethodDelete:
		manifest, ok, err := appCore.RuntimeHooks.GetManifest(r.Context(), caller.TenantID, hookID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "runtime hook manifest not found", map[string]any{"hook_id": hookID}), http.StatusNotFound)
			return
		}
		manifest.Status = runtimehook.StatusDisabled
		if err := appCore.RuntimeHooks.UpsertManifest(r.Context(), manifest); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"hook": manifest}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook manifest method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRuntimeHookEvents(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook events method", nil), http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	events, err := appCore.RuntimeHooks.ListEvents(r.Context(), caller.TenantID, contracts.TraceID(strings.TrimSpace(query.Get("trace_id"))))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	status := strings.TrimSpace(query.Get("status"))
	hookID := strings.TrimSpace(query.Get("hook_id"))
	providerID := strings.TrimSpace(query.Get("provider_id"))
	phase := strings.TrimSpace(query.Get("phase"))
	filtered := make([]runtimehook.HookEvent, 0, len(events))
	for _, event := range events {
		if status != "" && event.Status != status {
			continue
		}
		if hookID != "" && event.HookID != hookID {
			continue
		}
		if providerID != "" && event.ProviderID != providerID {
			continue
		}
		if phase != "" && string(event.Phase) != phase {
			continue
		}
		filtered = append(filtered, event)
	}
	writeJSON(w, map[string]any{"events": filtered}, http.StatusOK)
}

func handleRuntimeHookGovernance(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.RuntimeHooks == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook governance method", nil), http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	from, queryErr := parseRuntimeHookGovernanceTime(query.Get("from"), "from")
	if queryErr != nil {
		writeError(w, queryErr, http.StatusBadRequest)
		return
	}
	to, queryErr := parseRuntimeHookGovernanceTime(query.Get("to"), "to")
	if queryErr != nil {
		writeError(w, queryErr, http.StatusBadRequest)
		return
	}
	if from != nil && to != nil && from.After(*to) {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime hook governance from must be before to", map[string]any{"from": from.Format(time.RFC3339Nano), "to": to.Format(time.RFC3339Nano)}), http.StatusBadRequest)
		return
	}
	interval, intervalID, queryErr := parseRuntimeHookGovernanceInterval(query.Get("interval"))
	if queryErr != nil {
		writeError(w, queryErr, http.StatusBadRequest)
		return
	}
	summary, err := appCore.RuntimeHooks.GovernanceSummary(r.Context(), caller.TenantID, runtimehook.HookEventFilter{
		TraceID:    contracts.TraceID(strings.TrimSpace(query.Get("trace_id"))),
		ProviderID: strings.TrimSpace(query.Get("provider_id")),
		HookID:     strings.TrimSpace(query.Get("hook_id")),
		Phase:      runtimehook.HookPoint(strings.TrimSpace(query.Get("phase"))),
		Status:     strings.TrimSpace(query.Get("status")),
		From:       from,
		To:         to,
		Interval:   interval,
		IntervalID: intervalID,
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"summary": summary}, http.StatusOK)
}

func parseRuntimeHookGovernanceTime(raw string, name string) (*time.Time, *contracts.RuntimeError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid runtime hook governance time", map[string]any{"param": name, "value": raw, "format": time.RFC3339Nano})
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func parseRuntimeHookGovernanceInterval(raw string) (time.Duration, string, *contracts.RuntimeError) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, "", nil
	}
	switch raw {
	case "minute":
		return time.Minute, "1m", nil
	case "hour":
		return time.Hour, "1h", nil
	case "day":
		return 24 * time.Hour, "24h", nil
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 0, "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid runtime hook governance interval", map[string]any{"interval": raw, "allowed": []string{"minute", "hour", "day", "Go duration such as 15m or 24h"}})
	}
	return interval, raw, nil
}

func handleRuntimeHookApprovals(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.Approvals == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook approvals method", nil), http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	status := contracts.ApprovalStatus(strings.TrimSpace(query.Get("status")))
	if status != "" && !validApprovalStatus(status) {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported approval status", map[string]any{"status": status}), http.StatusBadRequest)
		return
	}
	approvals := appCore.Approvals.List(approval.ListFilter{
		TenantID:     caller.TenantID,
		ResourceType: "runtime_hook_binding",
		ResourceID:   strings.TrimSpace(query.Get("resource_id")),
		Action:       "runtime_hook.binding.upsert",
		Status:       status,
		TraceID:      contracts.TraceID(strings.TrimSpace(query.Get("trace_id"))),
	})
	agentID := strings.TrimSpace(query.Get("agent_id"))
	hookID := strings.TrimSpace(query.Get("hook_id"))
	phase := strings.TrimSpace(query.Get("phase"))
	if agentID != "" || hookID != "" || phase != "" {
		filtered := make([]contracts.ApprovalRequest, 0, len(approvals))
		for _, request := range approvals {
			if runtimeHookApprovalResourceMatches(request.ResourceID, agentID, hookID, phase) {
				filtered = append(filtered, request)
			}
		}
		approvals = filtered
	}
	sort.SliceStable(approvals, func(i, j int) bool {
		if approvals[i].CreatedAt.Equal(approvals[j].CreatedAt) {
			return approvals[i].ApprovalID > approvals[j].ApprovalID
		}
		return approvals[i].CreatedAt.After(approvals[j].CreatedAt)
	})
	writeJSON(w, map[string]any{"approvals": approvals}, http.StatusOK)
}

func validApprovalStatus(status contracts.ApprovalStatus) bool {
	switch status {
	case contracts.ApprovalPending, contracts.ApprovalApproved, contracts.ApprovalRejected, contracts.ApprovalUsed:
		return true
	default:
		return false
	}
}

func runtimeHookApprovalResourceMatches(resourceID string, agentID string, hookID string, phase string) bool {
	parts := strings.Split(resourceID, ":")
	if agentID != "" {
		if len(parts) < 1 || parts[0] != agentID {
			return false
		}
	}
	if hookID != "" {
		if len(parts) < 2 || parts[1] != hookID {
			return false
		}
	}
	if phase != "" {
		if len(parts) < 3 || parts[2] != phase {
			return false
		}
	}
	return true
}

func runtimeHookProviderByID(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, providerID string) (runtimehook.Provider, bool, error) {
	providers, err := appCore.RuntimeHooks.ListProviders(ctx, tenantID)
	if err != nil {
		return runtimehook.Provider{}, false, err
	}
	for _, provider := range providers {
		if provider.ProviderID == providerID {
			return provider, true, nil
		}
	}
	return runtimehook.Provider{}, false, nil
}

func runtimeHookProviderUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	provider, err := parseRuntimeHookProviderPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	provider.TenantID = caller.TenantID
	if err := appCore.RuntimeHooks.UpsertProvider(r.Context(), provider); err != nil {
		return nil, err
	}
	return provider, nil
}

func runtimeHookProviderList(r *http.Request, appCore *core.Core, _ contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	providers, err := appCore.RuntimeHooks.ListProviders(r.Context(), caller.TenantID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"providers": providers}, nil
}

func runtimeHookBindingUpsert(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	binding, err := parseRuntimeHookBindingPayload(envelope.Payload)
	if err != nil {
		return nil, err
	}
	binding.TenantID = caller.TenantID
	binding, err = appCore.RuntimeHooks.PrepareBinding(r.Context(), binding)
	if err != nil {
		return nil, err
	}
	approvalResponse, pending, err := runtimeHookBindingApprovalGate(r, appCore, caller, envelope.TraceID, binding, contracts.ApprovalID(payloadString(envelope.Payload, "approval_id")))
	if err != nil || pending {
		return approvalResponse, err
	}
	if err := appCore.RuntimeHooks.UpsertBinding(r.Context(), binding); err != nil {
		return nil, err
	}
	return binding, nil
}

func runtimeHookBindingApprovalGate(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, traceID contracts.TraceID, binding runtimehook.Binding, approvalID contracts.ApprovalID) (map[string]any, bool, error) {
	required, reason, err := runtimeHookBindingRequiresApproval(r.Context(), appCore, caller.TenantID, binding)
	if err != nil || !required {
		return nil, false, err
	}
	resourceID := runtimeHookBindingApprovalResourceID(binding)
	if approvalID != "" {
		if appCore.Approvals == nil {
			return nil, false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
		}
		if _, err := appCore.Approvals.Consume(r.Context(), caller.TenantID, approvalID, "runtime_hook_binding", resourceID, "runtime_hook.binding.upsert", caller.CallerID); err != nil {
			return nil, false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, err.Error(), map[string]any{"approval_id": approvalID})
		}
		return nil, false, nil
	}
	if appCore.Approvals == nil {
		return nil, false, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil)
	}
	approvalReq, err := appCore.Approvals.Request(r.Context(), approval.RequestInput{
		TenantID:     caller.TenantID,
		ResourceType: "runtime_hook_binding",
		ResourceID:   resourceID,
		Action:       "runtime_hook.binding.upsert",
		RiskLevel:    contracts.RiskHigh,
		Reason:       reason,
		RequestedBy:  caller.CallerID,
		TraceID:      traceID,
	})
	if err != nil {
		return nil, false, err
	}
	return map[string]any{
		"status":   "approval_required",
		"approval": approvalReq,
		"binding":  binding,
	}, true, nil
}

func runtimeHookBindingRequiresApproval(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, binding runtimehook.Binding) (bool, string, error) {
	if binding.RequiresApproval {
		return true, "runtime hook binding requires approval", nil
	}
	if runtimehook.ApprovalPolicyMatches(binding.ApprovalPolicy, binding) {
		return true, "runtime hook binding approval policy requires approval", nil
	}
	manifest, ok, err := appCore.RuntimeHooks.GetManifest(ctx, tenantID, binding.HookID)
	if err != nil || !ok {
		return false, "", err
	}
	if manifest.RequiresApproval {
		return true, fmt.Sprintf("runtime hook manifest %s requires approval before binding", binding.HookID), nil
	}
	if runtimehook.ApprovalPolicyMatches(manifest.ApprovalPolicy, binding) {
		return true, fmt.Sprintf("runtime hook manifest %s approval policy requires approval before binding", binding.HookID), nil
	}
	return false, "", nil
}

func runtimeHookBindingApprovalResourceID(binding runtimehook.Binding) string {
	parts := []string{string(binding.AgentID), binding.HookID, string(binding.Phase)}
	if binding.AgentVersion != "" {
		parts = append(parts, "agent_version="+string(binding.AgentVersion))
	}
	if binding.Version != "" {
		parts = append(parts, "hook_version="+binding.Version)
	}
	return strings.Join(parts, ":")
}

func runtimeHookBindingList(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	agentID := contracts.AgentID(payloadString(envelope.Payload, "agent_id"))
	if agentID == "" {
		agentID = envelope.Target.AgentID
	}
	if agentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime_hook.binding.list requires agent_id", nil)
	}
	version := contracts.AgentVersion(payloadString(envelope.Payload, "agent_version"))
	if version == "" {
		version = envelope.Target.Version
	}
	bindings, err := appCore.RuntimeHooks.ListBindings(r.Context(), caller.TenantID, agentID, version)
	if err != nil {
		return nil, err
	}
	return map[string]any{"bindings": bindings}, nil
}

func runtimeHookPreview(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.RuntimeHooks == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service is unavailable", nil)
	}
	agentID := envelope.Target.AgentID
	if agentID == "" {
		agentID = contracts.AgentID(payloadString(envelope.Payload, "agent_id"))
	}
	version := envelope.Target.Version
	if version == "" {
		version = contracts.AgentVersion(payloadString(envelope.Payload, "agent_version"))
	}
	if agentID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime_hook.preview requires agent_id", nil)
	}
	agent, err := appCore.Agents.Load(r.Context(), caller.TenantID, agentID, version)
	if err != nil {
		return nil, err
	}
	if agent.TenantID == "" {
		agent.TenantID = caller.TenantID
	}
	phase := runtimehook.HookPoint(payloadString(envelope.Payload, "phase"))
	if phase == "" {
		phase = runtimehook.BeforeModelCall
	}
	objective := payloadString(envelope.Payload, "input")
	policy := appCore.LoadPolicySet(r.Context(), caller.TenantID, agent.PolicyRefs.PolicySetID)
	payload := map[string]any{
		"objective": objective,
		"payload":   envelope.Payload,
	}
	if appCore.Coordinator.Tools != nil {
		if candidates, err := appCore.Coordinator.Tools.Candidates(r.Context(), agent, policy, objective); err == nil {
			payload["candidates"] = candidates
		}
	}
	patch, err := appCore.RuntimeHooks.Preview(r.Context(), runtimehook.InvokeRequest{
		TenantID: caller.TenantID,
		TraceID:  envelope.TraceID,
		RunID:    contracts.AgentRunID(payloadString(envelope.Payload, "run_id")),
		TaskID:   contracts.TaskID(payloadString(envelope.Payload, "task_id")),
		Agent:    agent,
		Policy:   policy,
		Phase:    phase,
		Payload:  payload,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"phase": phase, "patch": patch}, nil
}

func parseRuntimeHooksPayload(value any) contracts.AgentRuntimeHooks {
	raw, ok := value.(map[string]any)
	if !ok {
		return contracts.AgentRuntimeHooks{}
	}
	bindingsRaw, _ := raw["hooks"].([]any)
	bindings := make([]contracts.AgentRuntimeHookBinding, 0, len(bindingsRaw))
	for _, item := range bindingsRaw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		bindings = append(bindings, contracts.AgentRuntimeHookBinding{
			HookID:           payloadString(row, "hook_id"),
			ProviderType:     payloadString(row, "provider_type"),
			ProviderID:       payloadString(row, "provider_id"),
			Phase:            payloadString(row, "phase"),
			Version:          payloadString(row, "version"),
			Enabled:          boolValue(row["enabled"], true),
			TimeoutMS:        intValue(row["timeout_ms"], 0),
			FailurePolicy:    payloadString(row, "failure_policy"),
			RequiresApproval: boolValue(row["requires_approval"], false),
			ApprovalPolicy:   parseContractRuntimeHookApprovalPolicy(row["approval_policy"]),
			Config:           parseMetadata(row["config"]),
		})
	}
	return contracts.AgentRuntimeHooks{
		Mode:  payloadString(raw, "mode"),
		Hooks: bindings,
	}
}

func parseContractRuntimeHookApprovalPolicy(value any) contracts.RuntimeHookApprovalPolicy {
	raw, ok := value.(map[string]any)
	if !ok {
		return contracts.RuntimeHookApprovalPolicy{}
	}
	return contracts.RuntimeHookApprovalPolicy{
		RequireApproval: boolValue(raw["require_approval"], false),
		ProviderTypes:   stringSlice(raw["provider_types"]),
		Phases:          stringSlice(raw["phases"]),
		FailurePolicies: stringSlice(raw["failure_policies"]),
	}
}

func parseRuntimeHookApprovalPolicy(value any) runtimehook.ApprovalPolicy {
	raw := parseContractRuntimeHookApprovalPolicy(value)
	policy := runtimehook.ApprovalPolicy{
		RequireApproval: raw.RequireApproval,
		FailurePolicies: append([]string(nil), raw.FailurePolicies...),
	}
	for _, providerType := range raw.ProviderTypes {
		policy.ProviderTypes = append(policy.ProviderTypes, runtimehook.ProviderType(providerType))
	}
	for _, phase := range raw.Phases {
		policy.Phases = append(policy.Phases, runtimehook.HookPoint(phase))
	}
	return policy
}

func parseRuntimeHookProviderPayload(payload map[string]any) (runtimehook.Provider, error) {
	source := payload
	if raw, ok := payload["provider"].(map[string]any); ok {
		source = raw
	}
	provider := runtimehook.Provider{
		ProviderID:      payloadString(source, "provider_id"),
		Name:            payloadString(source, "name"),
		Description:     payloadString(source, "description"),
		ProviderType:    runtimehook.ProviderType(payloadString(source, "provider_type")),
		Endpoint:        payloadString(source, "endpoint"),
		Status:          payloadString(source, "status"),
		HealthStatus:    payloadString(source, "health_status"),
		LastHealthError: payloadString(source, "last_health_error"),
		Version:         payloadString(source, "version"),
		Config:          parseMetadata(source["config"]),
	}
	if provider.ProviderID == "" {
		return runtimehook.Provider{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime_hook.provider.upsert requires provider_id", nil)
	}
	if provider.Name == "" {
		provider.Name = provider.ProviderID
	}
	if provider.ProviderType == "" {
		provider.ProviderType = runtimehook.ProviderTypeGo
	}
	return provider, nil
}

func parseRuntimeHookManifestPayload(payload map[string]any) (runtimehook.HookManifest, error) {
	source := payload
	if raw, ok := payload["hook"].(map[string]any); ok {
		source = raw
	}
	if raw, ok := payload["manifest"].(map[string]any); ok {
		source = raw
	}
	manifest := runtimehook.HookManifest{
		HookID:           payloadString(source, "hook_id"),
		ProviderID:       payloadString(source, "provider_id"),
		Name:             payloadString(source, "name"),
		Description:      payloadString(source, "description"),
		Phase:            runtimehook.HookPoint(payloadString(source, "phase")),
		Status:           payloadString(source, "status"),
		Version:          payloadString(source, "version"),
		TimeoutMS:        intValue(source["timeout_ms"], 0),
		FailurePolicy:    payloadString(source, "failure_policy"),
		RequiresApproval: boolValue(source["requires_approval"], false),
		ApprovalPolicy:   parseRuntimeHookApprovalPolicy(source["approval_policy"]),
		ConfigSchema:     parseMetadata(source["config_schema"]),
		RequestSchema:    parseMetadata(source["request_schema"]),
		PatchSchema:      parseMetadata(source["patch_schema"]),
	}
	if manifest.HookID == "" || manifest.Phase == "" {
		return runtimehook.HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime hook manifest requires hook_id and phase", nil)
	}
	if manifest.Name == "" {
		manifest.Name = manifest.HookID
	}
	return manifest, nil
}

func parseRuntimeHookBindingPayload(payload map[string]any) (runtimehook.Binding, error) {
	source := payload
	if raw, ok := payload["binding"].(map[string]any); ok {
		source = raw
	}
	binding := runtimehook.Binding{
		AgentID:          contracts.AgentID(payloadString(source, "agent_id")),
		AgentVersion:     contracts.AgentVersion(payloadString(source, "agent_version")),
		HookID:           payloadString(source, "hook_id"),
		ProviderType:     runtimehook.ProviderType(payloadString(source, "provider_type")),
		ProviderID:       payloadString(source, "provider_id"),
		Phase:            runtimehook.HookPoint(payloadString(source, "phase")),
		Version:          payloadString(source, "version"),
		Enabled:          boolValue(source["enabled"], true),
		TimeoutMS:        intValue(source["timeout_ms"], 0),
		FailurePolicy:    payloadString(source, "failure_policy"),
		RequiresApproval: boolValue(source["requires_approval"], false),
		ApprovalPolicy:   parseRuntimeHookApprovalPolicy(source["approval_policy"]),
		Config:           parseMetadata(source["config"]),
	}
	if binding.AgentID == "" || binding.HookID == "" || binding.Phase == "" {
		return runtimehook.Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime_hook.binding.upsert requires agent_id, hook_id and phase", nil)
	}
	if binding.ProviderType == "" {
		binding.ProviderType = runtimehook.ProviderTypeGo
	}
	return binding, nil
}
