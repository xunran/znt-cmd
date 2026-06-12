package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	toolcatalog "znt/internal/tool/catalog"
)

type toolProviderGovernanceQuery struct {
	TraceID      contracts.TraceID
	ProviderID   string
	ToolID       string
	Status       string
	HealthStatus string
	ExecutorType string
}

type toolProviderGovernanceView struct {
	TenantID       contracts.TenantID               `json:"tenant_id"`
	TraceID        contracts.TraceID                `json:"trace_id,omitempty"`
	Summary        toolProviderGovernanceSummary    `json:"summary"`
	ProviderMatrix []toolProviderGovernanceProvider `json:"provider_matrix"`
	ToolMatrix     []toolProviderGovernanceTool     `json:"tool_matrix"`
}

type toolProviderGovernanceSummary struct {
	ProvidersTotal                   int `json:"providers_total"`
	EnabledProvidersTotal            int `json:"enabled_providers_total"`
	DisabledProvidersTotal           int `json:"disabled_providers_total"`
	HealthyProvidersTotal            int `json:"healthy_providers_total"`
	UnhealthyProvidersTotal          int `json:"unhealthy_providers_total"`
	UnknownHealthProvidersTotal      int `json:"unknown_health_providers_total"`
	BlockedProvidersTotal            int `json:"blocked_providers_total"`
	ToolsTotal                       int `json:"tools_total"`
	EnabledToolsTotal                int `json:"enabled_tools_total"`
	DisabledToolsTotal               int `json:"disabled_tools_total"`
	DraftToolsTotal                  int `json:"draft_tools_total"`
	BlockedToolsTotal                int `json:"blocked_tools_total"`
	StaticToolHostToolsTotal         int `json:"static_tool_host_tools_total"`
	AgentPluginServiceToolsTotal     int `json:"agent_plugin_service_tools_total"`
	MCPToolsTotal                    int `json:"mcp_tools_total"`
	HTTPAPIAdapterToolsTotal         int `json:"http_api_adapter_tools_total"`
	DatabaseAdapterToolsTotal        int `json:"database_adapter_tools_total"`
	AgentToolToolsTotal              int `json:"agent_tool_tools_total"`
	MissingProviderToolsTotal        int `json:"missing_provider_tools_total"`
	HighRiskToolsTotal               int `json:"high_risk_tools_total"`
	CriticalRiskToolsTotal           int `json:"critical_risk_tools_total"`
	TraceEventsTotal                 int `json:"trace_events_total"`
	TraceProviderInvocationsTotal    int `json:"trace_provider_invocations_total"`
	TraceProviderCompletionsTotal    int `json:"trace_provider_completions_total"`
	TraceProviderFailuresTotal       int `json:"trace_provider_failures_total"`
	TraceProviderHealthChecksTotal   int `json:"trace_provider_health_checks_total"`
	TraceProviderHealthFailuresTotal int `json:"trace_provider_health_failures_total"`
	TraceProviderLatencyMS           int `json:"trace_provider_latency_ms"`
}

type toolProviderGovernanceProvider struct {
	TenantID               contracts.TenantID                   `json:"tenant_id,omitempty"`
	ProviderID             string                               `json:"provider_id"`
	ProviderType           string                               `json:"provider_type"`
	Name                   string                               `json:"name"`
	Status                 string                               `json:"status"`
	HealthStatus           string                               `json:"health_status"`
	LastHealthCheckAt      *time.Time                           `json:"last_health_check_at,omitempty"`
	LastHealthError        string                               `json:"last_health_error,omitempty"`
	ServiceConnectionID    string                               `json:"service_connection_id"`
	Version                string                               `json:"version"`
	Runnable               bool                                 `json:"runnable"`
	BlockedReasons         []string                             `json:"blocked_reasons"`
	ToolsTotal             int                                  `json:"tools_total"`
	EnabledToolsTotal      int                                  `json:"enabled_tools_total"`
	DisabledToolsTotal     int                                  `json:"disabled_tools_total"`
	DraftToolsTotal        int                                  `json:"draft_tools_total"`
	BlockedToolsTotal      int                                  `json:"blocked_tools_total"`
	HighRiskToolsTotal     int                                  `json:"high_risk_tools_total"`
	CriticalRiskToolsTotal int                                  `json:"critical_risk_tools_total"`
	Groups                 []string                             `json:"groups"`
	TraceEvidence          *toolProviderGovernanceTraceEvidence `json:"trace_evidence,omitempty"`
}

type toolProviderGovernanceTool struct {
	TenantID       contracts.TenantID                   `json:"tenant_id,omitempty"`
	ToolID         string                               `json:"tool_id"`
	Name           string                               `json:"name"`
	GroupID        string                               `json:"group_id,omitempty"`
	ProviderID     string                               `json:"provider_id,omitempty"`
	ExecutorType   string                               `json:"executor_type"`
	Operation      string                               `json:"operation,omitempty"`
	Status         string                               `json:"status"`
	Version        string                               `json:"version"`
	RiskLevel      contracts.RiskLevel                  `json:"risk_level"`
	Visibility     contracts.ToolVisibility             `json:"visibility"`
	Runnable       bool                                 `json:"runnable"`
	BlockedReasons []string                             `json:"blocked_reasons"`
	TraceEvidence  *toolProviderGovernanceTraceEvidence `json:"trace_evidence,omitempty"`
}

type toolProviderGovernanceTraceEvidence struct {
	EventsTotal         int        `json:"events_total"`
	InvocationsTotal    int        `json:"invocations_total"`
	CompletionsTotal    int        `json:"completions_total"`
	FailuresTotal       int        `json:"failures_total"`
	HealthChecksTotal   int        `json:"health_checks_total"`
	HealthFailuresTotal int        `json:"health_failures_total"`
	LatencyMS           int        `json:"latency_ms"`
	LastEventAt         *time.Time `json:"last_event_at,omitempty"`
	ErrorCodes          []string   `json:"error_codes"`
	HealthStatuses      []string   `json:"health_statuses"`
}

func handleToolProviderGovernance(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.ToolCatalog == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool catalog service is unavailable", nil))
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tool provider governance method", nil), http.StatusMethodNotAllowed)
		return
	}
	query := toolProviderGovernanceQuery{
		TraceID:      contracts.TraceID(strings.TrimSpace(r.URL.Query().Get("trace_id"))),
		ProviderID:   strings.TrimSpace(r.URL.Query().Get("provider_id")),
		ToolID:       strings.TrimSpace(r.URL.Query().Get("tool_id")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
		HealthStatus: strings.TrimSpace(r.URL.Query().Get("health_status")),
		ExecutorType: strings.TrimSpace(r.URL.Query().Get("executor_type")),
	}
	var events []contracts.TraceEvent
	if query.TraceID != "" {
		if appCore.Trace == nil {
			writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace recorder is unavailable", nil))
			return
		}
		var err error
		events, err = appCore.Trace.ListByTrace(r.Context(), query.TraceID)
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
	}
	view := buildToolProviderGovernanceView(
		caller.TenantID,
		query,
		appCore.ToolCatalog.ListProviders(caller.TenantID),
		appCore.ToolCatalog.ListGroups(caller.TenantID),
		appCore.ToolCatalog.ListManifests(caller.TenantID),
		events,
	)
	writeJSON(w, map[string]any{"governance": view}, http.StatusOK)
}

func buildToolProviderGovernanceView(tenantID contracts.TenantID, query toolProviderGovernanceQuery, providers []toolcatalog.ToolProvider, groups []toolcatalog.ToolGroup, manifests []toolcatalog.ToolManifest, events []contracts.TraceEvent) toolProviderGovernanceView {
	providerByKey := map[string]toolcatalog.ToolProvider{}
	for _, provider := range providers {
		providerByKey[toolProviderGovernanceKey(provider.TenantID, provider.ProviderID)] = provider
	}
	groupByKey := map[string]toolcatalog.ToolGroup{}
	for _, group := range groups {
		groupByKey[toolProviderGovernanceKey(group.TenantID, group.GroupID)] = group
	}
	traceSummary, providerEvidence, toolEvidence := collectToolProviderGovernanceTraceEvidence(events, query)

	providerRows := make([]toolProviderGovernanceProvider, 0, len(providers))
	providerIndex := map[string]int{}
	for _, provider := range providers {
		if !toolProviderGovernanceProviderMatches(provider, query) {
			continue
		}
		runnable, blockedReasons := toolProviderGovernanceProviderState(provider)
		row := toolProviderGovernanceProvider{
			TenantID:            provider.TenantID,
			ProviderID:          provider.ProviderID,
			ProviderType:        provider.ProviderType,
			Name:                provider.Name,
			Status:              provider.Status,
			HealthStatus:        provider.HealthStatus,
			LastHealthCheckAt:   provider.LastHealthCheckAt,
			LastHealthError:     provider.LastHealthError,
			ServiceConnectionID: provider.ServiceConnectionID,
			Version:             provider.Version,
			Runnable:            runnable,
			BlockedReasons:      blockedReasons,
			Groups:              []string{},
		}
		if evidence, ok := providerEvidence[provider.ProviderID]; ok {
			row.TraceEvidence = cloneToolProviderGovernanceTraceEvidence(evidence)
		}
		providerIndex[toolProviderGovernanceKey(provider.TenantID, provider.ProviderID)] = len(providerRows)
		providerRows = append(providerRows, row)
	}

	toolRows := make([]toolProviderGovernanceTool, 0, len(manifests))
	for _, manifest := range manifests {
		providerID := toolProviderGovernanceManifestProviderID(manifest)
		if !toolProviderGovernanceToolMatches(manifest, providerID, query) {
			continue
		}
		group, groupFound := toolProviderGovernanceGroup(groupByKey, manifest.TenantID, manifest.GroupID)
		provider, providerFound := lookupToolProviderGovernanceProvider(providerByKey, manifest.TenantID, providerID)
		runnable, blockedReasons := toolProviderGovernanceToolState(manifest, group, groupFound, provider, providerFound)
		row := toolProviderGovernanceTool{
			TenantID:       manifest.TenantID,
			ToolID:         manifest.ToolID,
			Name:           manifest.Name,
			GroupID:        manifest.GroupID,
			ProviderID:     providerID,
			ExecutorType:   manifest.Executor.Type,
			Operation:      manifest.Executor.Operation,
			Status:         manifest.Status,
			Version:        manifest.Version,
			RiskLevel:      manifest.RiskLevel,
			Visibility:     manifest.Visibility,
			Runnable:       runnable,
			BlockedReasons: blockedReasons,
		}
		if evidence, ok := toolEvidence[manifest.ToolID]; ok {
			row.TraceEvidence = cloneToolProviderGovernanceTraceEvidence(evidence)
		}
		toolRows = append(toolRows, row)
		if toolProviderGovernanceExecutorUsesToolProvider(manifest.Executor.Type) && providerFound {
			if idx, ok := providerIndex[toolProviderGovernanceKey(provider.TenantID, provider.ProviderID)]; ok {
				updateToolProviderGovernanceProviderCounts(&providerRows[idx], manifest, runnable)
			}
		}
	}
	if query.ToolID != "" {
		providerRows = toolProviderGovernanceProvidersWithSelectedTools(providerRows)
	}
	sortToolProviderGovernanceProviders(providerRows)
	sortToolProviderGovernanceTools(toolRows)
	summary := summarizeToolProviderGovernance(providerRows, toolRows, traceSummary)
	return toolProviderGovernanceView{
		TenantID:       tenantID,
		TraceID:        query.TraceID,
		Summary:        summary,
		ProviderMatrix: providerRows,
		ToolMatrix:     toolRows,
	}
}

func toolProviderGovernanceProviderMatches(provider toolcatalog.ToolProvider, query toolProviderGovernanceQuery) bool {
	if query.ProviderID != "" && provider.ProviderID != query.ProviderID {
		return false
	}
	if query.Status != "" && provider.Status != query.Status {
		return false
	}
	if query.HealthStatus != "" && provider.HealthStatus != query.HealthStatus {
		return false
	}
	return true
}

func toolProviderGovernanceToolMatches(manifest toolcatalog.ToolManifest, providerID string, query toolProviderGovernanceQuery) bool {
	if query.ProviderID != "" && providerID != query.ProviderID {
		return false
	}
	if query.ToolID != "" && manifest.ToolID != query.ToolID {
		return false
	}
	if query.Status != "" && manifest.Status != query.Status {
		return false
	}
	if query.ExecutorType != "" && manifest.Executor.Type != query.ExecutorType {
		return false
	}
	return true
}

func toolProviderGovernanceProviderState(provider toolcatalog.ToolProvider) (bool, []string) {
	reasons := make([]string, 0)
	if provider.Status != toolcatalog.StatusEnabled {
		reasons = append(reasons, "provider_status:"+provider.Status)
	}
	if provider.HealthStatus == toolcatalog.HealthUnhealthy {
		reasons = append(reasons, "provider_unhealthy")
	}
	return len(reasons) == 0, reasons
}

func toolProviderGovernanceToolState(manifest toolcatalog.ToolManifest, group toolcatalog.ToolGroup, groupFound bool, provider toolcatalog.ToolProvider, providerFound bool) (bool, []string) {
	reasons := make([]string, 0)
	if manifest.Status != toolcatalog.StatusEnabled {
		reasons = append(reasons, "tool_status:"+manifest.Status)
	}
	if strings.TrimSpace(manifest.GroupID) != "" && groupFound && group.Status != toolcatalog.StatusEnabled {
		reasons = append(reasons, "group_status:"+group.Status)
	}
	if toolProviderGovernanceExecutorUsesToolProvider(manifest.Executor.Type) {
		if !providerFound {
			reasons = append(reasons, "provider_missing")
		} else {
			if provider.Status != toolcatalog.StatusEnabled {
				reasons = append(reasons, "provider_status:"+provider.Status)
			}
			if provider.HealthStatus == toolcatalog.HealthUnhealthy {
				reasons = append(reasons, "provider_unhealthy")
			}
		}
	}
	return len(reasons) == 0, reasons
}

func updateToolProviderGovernanceProviderCounts(row *toolProviderGovernanceProvider, manifest toolcatalog.ToolManifest, runnable bool) {
	row.ToolsTotal++
	switch manifest.Status {
	case toolcatalog.StatusEnabled:
		row.EnabledToolsTotal++
	case toolcatalog.StatusDisabled:
		row.DisabledToolsTotal++
	case toolcatalog.StatusDraft:
		row.DraftToolsTotal++
	}
	if !runnable {
		row.BlockedToolsTotal++
	}
	if manifest.RiskLevel == contracts.RiskHigh {
		row.HighRiskToolsTotal++
	}
	if manifest.RiskLevel == contracts.RiskCritical {
		row.CriticalRiskToolsTotal++
	}
	if manifest.GroupID != "" {
		row.Groups = appendUniqueString(row.Groups, manifest.GroupID)
	}
}

func summarizeToolProviderGovernance(providers []toolProviderGovernanceProvider, tools []toolProviderGovernanceTool, traceSummary toolProviderGovernanceTraceEvidence) toolProviderGovernanceSummary {
	summary := toolProviderGovernanceSummary{
		TraceEventsTotal:                 traceSummary.EventsTotal,
		TraceProviderInvocationsTotal:    traceSummary.InvocationsTotal,
		TraceProviderCompletionsTotal:    traceSummary.CompletionsTotal,
		TraceProviderFailuresTotal:       traceSummary.FailuresTotal,
		TraceProviderHealthChecksTotal:   traceSummary.HealthChecksTotal,
		TraceProviderHealthFailuresTotal: traceSummary.HealthFailuresTotal,
		TraceProviderLatencyMS:           traceSummary.LatencyMS,
	}
	for _, provider := range providers {
		summary.ProvidersTotal++
		if provider.Status == toolcatalog.StatusEnabled {
			summary.EnabledProvidersTotal++
		}
		if provider.Status == toolcatalog.StatusDisabled {
			summary.DisabledProvidersTotal++
		}
		switch provider.HealthStatus {
		case toolcatalog.HealthHealthy:
			summary.HealthyProvidersTotal++
		case toolcatalog.HealthUnhealthy:
			summary.UnhealthyProvidersTotal++
		case toolcatalog.HealthUnknown:
			summary.UnknownHealthProvidersTotal++
		}
		if !provider.Runnable {
			summary.BlockedProvidersTotal++
		}
	}
	for _, tool := range tools {
		summary.ToolsTotal++
		switch tool.Status {
		case toolcatalog.StatusEnabled:
			summary.EnabledToolsTotal++
		case toolcatalog.StatusDisabled:
			summary.DisabledToolsTotal++
		case toolcatalog.StatusDraft:
			summary.DraftToolsTotal++
		}
		if !tool.Runnable {
			summary.BlockedToolsTotal++
		}
		switch tool.ExecutorType {
		case toolcatalog.ExecutorTypeStaticToolHost:
			summary.StaticToolHostToolsTotal++
			if contains(tool.BlockedReasons, "provider_missing") {
				summary.MissingProviderToolsTotal++
			}
		case toolcatalog.ExecutorTypeAgentPlugin:
			summary.AgentPluginServiceToolsTotal++
			if contains(tool.BlockedReasons, "provider_missing") {
				summary.MissingProviderToolsTotal++
			}
		case toolcatalog.ExecutorTypeMCP:
			summary.MCPToolsTotal++
			if contains(tool.BlockedReasons, "provider_missing") {
				summary.MissingProviderToolsTotal++
			}
		case toolcatalog.ExecutorTypeHTTPAPIAdapter:
			summary.HTTPAPIAdapterToolsTotal++
			if contains(tool.BlockedReasons, "provider_missing") {
				summary.MissingProviderToolsTotal++
			}
		case toolcatalog.ExecutorTypeDatabaseAdapter:
			summary.DatabaseAdapterToolsTotal++
			if contains(tool.BlockedReasons, "provider_missing") {
				summary.MissingProviderToolsTotal++
			}
		case toolcatalog.ExecutorTypeAgentTool:
			summary.AgentToolToolsTotal++
		}
		if tool.RiskLevel == contracts.RiskHigh {
			summary.HighRiskToolsTotal++
		}
		if tool.RiskLevel == contracts.RiskCritical {
			summary.CriticalRiskToolsTotal++
		}
	}
	return summary
}

func collectToolProviderGovernanceTraceEvidence(events []contracts.TraceEvent, query toolProviderGovernanceQuery) (toolProviderGovernanceTraceEvidence, map[string]toolProviderGovernanceTraceEvidence, map[string]toolProviderGovernanceTraceEvidence) {
	summary := toolProviderGovernanceTraceEvidence{ErrorCodes: []string{}, HealthStatuses: []string{}}
	providers := map[string]toolProviderGovernanceTraceEvidence{}
	tools := map[string]toolProviderGovernanceTraceEvidence{}
	for _, event := range events {
		if !isToolProviderTraceEvent(event.Type) {
			continue
		}
		providerID := payloadString(event.Payload, "provider_id")
		toolID := payloadString(event.Payload, "tool_id")
		if query.ProviderID != "" && providerID != query.ProviderID {
			continue
		}
		if query.ToolID != "" && toolID != query.ToolID {
			continue
		}
		addToolProviderGovernanceTraceEvent(&summary, event)
		if providerID != "" {
			evidence := providers[providerID]
			addToolProviderGovernanceTraceEvent(&evidence, event)
			providers[providerID] = evidence
		}
		if toolID != "" {
			evidence := tools[toolID]
			addToolProviderGovernanceTraceEvent(&evidence, event)
			tools[toolID] = evidence
		}
	}
	return summary, providers, tools
}

func isToolProviderTraceEvent(eventType string) bool {
	switch eventType {
	case contracts.TraceToolProviderInvoked, contracts.TraceToolProviderCompleted, contracts.TraceToolProviderFailed, contracts.TraceToolProviderHealthChecked:
		return true
	default:
		return false
	}
}

func addToolProviderGovernanceTraceEvent(evidence *toolProviderGovernanceTraceEvidence, event contracts.TraceEvent) {
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
	case contracts.TraceToolProviderFailed:
		evidence.FailuresTotal++
		evidence.LatencyMS += payloadInt(event.Payload, "latency_ms")
		if errorCode := payloadString(event.Payload, "error_code"); errorCode != "" {
			evidence.ErrorCodes = appendUniqueString(evidence.ErrorCodes, errorCode)
		}
	case contracts.TraceToolProviderHealthChecked:
		evidence.HealthChecksTotal++
		evidence.LatencyMS += payloadInt(event.Payload, "latency_ms")
		if healthStatus := payloadString(event.Payload, "health_status"); healthStatus != "" {
			evidence.HealthStatuses = appendUniqueString(evidence.HealthStatuses, healthStatus)
			if healthStatus != toolcatalog.HealthHealthy {
				evidence.HealthFailuresTotal++
			}
		}
	}
}

func cloneToolProviderGovernanceTraceEvidence(evidence toolProviderGovernanceTraceEvidence) *toolProviderGovernanceTraceEvidence {
	evidence.ErrorCodes = append([]string{}, evidence.ErrorCodes...)
	evidence.HealthStatuses = append([]string{}, evidence.HealthStatuses...)
	return &evidence
}

func toolProviderGovernanceManifestProviderID(manifest toolcatalog.ToolManifest) string {
	switch manifest.Executor.Type {
	case toolcatalog.ExecutorTypeStaticToolHost, toolcatalog.ExecutorTypeAgentPlugin, toolcatalog.ExecutorTypeMCP, toolcatalog.ExecutorTypeHTTPAPIAdapter, toolcatalog.ExecutorTypeDatabaseAdapter, toolcatalog.ExecutorTypeAgentTool:
		return manifest.Executor.ProviderID
	default:
		return ""
	}
}

func toolProviderGovernanceExecutorUsesToolProvider(executorType string) bool {
	return executorType == toolcatalog.ExecutorTypeStaticToolHost || executorType == toolcatalog.ExecutorTypeAgentPlugin || executorType == toolcatalog.ExecutorTypeMCP || executorType == toolcatalog.ExecutorTypeHTTPAPIAdapter || executorType == toolcatalog.ExecutorTypeDatabaseAdapter
}

func lookupToolProviderGovernanceProvider(providerByKey map[string]toolcatalog.ToolProvider, tenantID contracts.TenantID, providerID string) (toolcatalog.ToolProvider, bool) {
	if providerID == "" {
		return toolcatalog.ToolProvider{}, false
	}
	if provider, ok := providerByKey[toolProviderGovernanceKey(tenantID, providerID)]; ok {
		return provider, true
	}
	if tenantID != "" {
		if provider, ok := providerByKey[toolProviderGovernanceKey("", providerID)]; ok {
			return provider, true
		}
	}
	return toolcatalog.ToolProvider{}, false
}

func toolProviderGovernanceGroup(groupByKey map[string]toolcatalog.ToolGroup, tenantID contracts.TenantID, groupID string) (toolcatalog.ToolGroup, bool) {
	if groupID == "" {
		return toolcatalog.ToolGroup{}, false
	}
	if group, ok := groupByKey[toolProviderGovernanceKey(tenantID, groupID)]; ok {
		return group, true
	}
	if tenantID != "" {
		if group, ok := groupByKey[toolProviderGovernanceKey("", groupID)]; ok {
			return group, true
		}
	}
	return toolcatalog.ToolGroup{}, false
}

func toolProviderGovernanceKey(tenantID contracts.TenantID, id string) string {
	return string(tenantID) + "\x00" + id
}

func toolProviderGovernanceProvidersWithSelectedTools(rows []toolProviderGovernanceProvider) []toolProviderGovernanceProvider {
	out := make([]toolProviderGovernanceProvider, 0, len(rows))
	for _, row := range rows {
		if row.ToolsTotal > 0 {
			out = append(out, row)
		}
	}
	return out
}

func sortToolProviderGovernanceProviders(rows []toolProviderGovernanceProvider) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ProviderID == rows[j].ProviderID {
			return rows[i].TenantID < rows[j].TenantID
		}
		return rows[i].ProviderID < rows[j].ProviderID
	})
	for i := range rows {
		sort.Strings(rows[i].Groups)
	}
}

func sortToolProviderGovernanceTools(rows []toolProviderGovernanceTool) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ToolID == rows[j].ToolID {
			return rows[i].TenantID < rows[j].TenantID
		}
		return rows[i].ToolID < rows[j].ToolID
	})
}

func appendUniqueString(values []string, value string) []string {
	if value == "" || contains(values, value) {
		return values
	}
	return append(values, value)
}
