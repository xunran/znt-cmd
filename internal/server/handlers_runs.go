package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	auditlog "znt/internal/governance/audit"
	"znt/internal/governance/replay"
	runtimehook "znt/internal/runtime/hook"
	runrepo "znt/internal/runtime/run"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
)

type runTimelineEvent struct {
	EventID    string         `json:"event_id"`
	Title      string         `json:"title"`
	Status     string         `json:"status,omitempty"`
	Category   string         `json:"category"`
	OccurredAt time.Time      `json:"occurred_at"`
	DurationMS int            `json:"duration_ms,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

type runDiagnosticsResponse struct {
	Run            map[string]any            `json:"run"`
	Summary        runDiagnosticsSummary     `json:"summary"`
	Routing        []runRouteDiagnostic      `json:"routing"`
	Strategy       runStrategyDiagnostic     `json:"strategy"`
	Context        runContextDiagnostic      `json:"context"`
	Prompt         runPromptDiagnostic       `json:"prompt"`
	Model          runModelDiagnostic        `json:"model"`
	Decisions      []runDecisionDiagnostic   `json:"decisions"`
	Tools          []runToolDiagnostic       `json:"tools"`
	ToolAgentCalls []toolAgentCallDiagnostic `json:"tool_agent_calls,omitempty"`
	RuntimeHooks   []runtimehook.HookEvent   `json:"runtime_hooks"`
	AuditEvents    []contracts.AuditEvent    `json:"audit_events"`
	Replay         replay.Report             `json:"replay"`
	UsageEvidence  usageEvidenceResponse     `json:"usage_evidence"`
	Timeline       []runTimelineEvent        `json:"timeline"`
	Artifacts      []contracts.ArtifactID    `json:"artifacts"`
	TraceEndpoints map[string]string         `json:"trace_endpoints"`
	Meta           map[string]any            `json:"meta"`
}

type traceDiagnosticsResponse struct {
	TraceID        contracts.TraceID         `json:"trace_id"`
	TenantID       contracts.TenantID        `json:"tenant_id,omitempty"`
	Runs           []contracts.AgentRun      `json:"runs"`
	Summary        runDiagnosticsSummary     `json:"summary"`
	Routing        []runRouteDiagnostic      `json:"routing"`
	Strategy       runStrategyDiagnostic     `json:"strategy"`
	Context        runContextDiagnostic      `json:"context"`
	Prompt         runPromptDiagnostic       `json:"prompt"`
	Model          runModelDiagnostic        `json:"model"`
	Decisions      []runDecisionDiagnostic   `json:"decisions"`
	ToolAgentCalls []toolAgentCallDiagnostic `json:"tool_agent_calls,omitempty"`
	Replay         replay.Report             `json:"replay"`
	UsageEvidence  usageEvidenceResponse     `json:"usage_evidence"`
	Timeline       []runTimelineEvent        `json:"timeline"`
	Meta           map[string]any            `json:"meta"`
}

type runDiagnosticsSummary struct {
	Status                 contracts.RunStatus    `json:"status,omitempty"`
	ErrorCode              contracts.ErrorCode    `json:"error_code,omitempty"`
	ErrorMessage           string                 `json:"error_message,omitempty"`
	StrategyLimitReason    string                 `json:"strategy_limit_reason,omitempty"`
	DurationMS             int64                  `json:"duration_ms,omitempty"`
	TraceEventsTotal       int                    `json:"trace_events_total"`
	TaskEventsTotal        int                    `json:"task_events_total"`
	ToolCallsTotal         int                    `json:"tool_calls_total"`
	ToolFailuresTotal      int                    `json:"tool_failures_total"`
	RuntimeHookEventsTotal int                    `json:"runtime_hook_events_total"`
	AuditEventsTotal       int                    `json:"audit_events_total"`
	ModelCallsTotal        int                    `json:"model_calls_total"`
	ModelFailuresTotal     int                    `json:"model_failures_total"`
	PromptTokensTotal      int                    `json:"prompt_tokens_total"`
	CompletionTokensTotal  int                    `json:"completion_tokens_total"`
	PromptBundleHashes     []string               `json:"prompt_bundle_hashes"`
	PolicyVersions         []string               `json:"policy_versions"`
	Problems               []string               `json:"problems,omitempty"`
	FirstAt                *time.Time             `json:"first_at,omitempty"`
	LastAt                 *time.Time             `json:"last_at,omitempty"`
	RunIDs                 []contracts.AgentRunID `json:"run_ids,omitempty"`
}

type runRouteDiagnostic struct {
	AgentID           contracts.AgentID             `json:"agent_id,omitempty"`
	RequestedVersion  contracts.AgentVersion        `json:"requested_version,omitempty"`
	ResolvedVersion   contracts.AgentVersion        `json:"resolved_version,omitempty"`
	ReleaseStatus     contracts.ReleaseStatus       `json:"release_status,omitempty"`
	PackageVersionID  contracts.PackageVersionID    `json:"package_version_id,omitempty"`
	CarrierKind       contracts.AgentCarrierKind    `json:"carrier_kind,omitempty"`
	RuntimeContract   contracts.RuntimeContractKind `json:"runtime_contract,omitempty"`
	SourceKind        contracts.AgentSourceKind     `json:"source_kind,omitempty"`
	SourceProviderID  string                        `json:"source_provider_id,omitempty"`
	ManifestVersion   string                        `json:"manifest_version,omitempty"`
	ManifestHash      string                        `json:"manifest_hash,omitempty"`
	StrategyHash      string                        `json:"strategy_hash,omitempty"`
	RouteReason       string                        `json:"route_reason,omitempty"`
	Canary            bool                          `json:"canary"`
	CanaryPercent     int                           `json:"canary_percent,omitempty"`
	AssignmentKeyHash string                        `json:"assignment_key_hash,omitempty"`
	CreatedAt         time.Time                     `json:"created_at"`
}

type runPromptDiagnostic struct {
	PromptBundleHash   string                    `json:"prompt_bundle_hash,omitempty"`
	PromptBundleHashes []string                  `json:"prompt_bundle_hashes,omitempty"`
	PolicySetID        contracts.PolicySetID     `json:"policy_set_id,omitempty"`
	PolicyVersionID    contracts.PolicyVersionID `json:"policy_version_id,omitempty"`
	PolicyVersion      string                    `json:"policy_version,omitempty"`
	PromptRecorded     bool                      `json:"prompt_recorded"`
	PreviewCommand     map[string]any            `json:"preview_command,omitempty"`
	RedactionPolicy    string                    `json:"redaction_policy"`
	AssemblySteps      []promptAssemblyStepView  `json:"assembly_steps,omitempty"`
	PromptSnapshots    []promptSnapshotView      `json:"prompt_snapshots,omitempty"`
}

type promptAssemblyStepView struct {
	StepID         string `json:"step_id"`
	Title          string `json:"title"`
	SourceType     string `json:"source_type"`
	SourceLabel    string `json:"source_label"`
	EditTarget     string `json:"edit_target,omitempty"`
	MessageRole    string `json:"message_role"`
	PromptSection  string `json:"prompt_section"`
	Reason         string `json:"reason,omitempty"`
	Content        string `json:"content,omitempty"`
	ContentPreview string `json:"content_preview,omitempty"`
	TokensEstimate int    `json:"tokens_estimate,omitempty"`
	Included       bool   `json:"included"`
}

type promptMessageView struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type promptSnapshotView struct {
	SnapshotID       string                   `json:"snapshot_id"`
	PromptBundleHash string                   `json:"prompt_bundle_hash,omitempty"`
	ModelProvider    string                   `json:"model_provider,omitempty"`
	ModelName        string                   `json:"model_name,omitempty"`
	RepairAttempt    int                      `json:"repair_attempt,omitempty"`
	Messages         []promptMessageView      `json:"messages"`
	AssemblySteps    []promptAssemblyStepView `json:"assembly_steps,omitempty"`
	TokensEstimate   int                      `json:"tokens_estimate,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
}

type runModelDiagnostic struct {
	Provider              string                   `json:"provider,omitempty"`
	Name                  string                   `json:"name,omitempty"`
	PromptTokensTotal     int                      `json:"prompt_tokens_total"`
	CompletionTokensTotal int                      `json:"completion_tokens_total"`
	Calls                 []runModelCallDiagnostic `json:"calls"`
}

type runStrategyDiagnostic struct {
	SourceKind           contracts.AgentSourceKind     `json:"source_kind,omitempty"`
	SourceProviderID     string                        `json:"source_provider_id,omitempty"`
	ServiceConnectionID  string                        `json:"service_connection_id,omitempty"`
	CarrierKind          contracts.AgentCarrierKind    `json:"carrier_kind,omitempty"`
	RuntimeContract      contracts.RuntimeContractKind `json:"runtime_contract,omitempty"`
	ManifestVersion      string                        `json:"manifest_version,omitempty"`
	ManifestHash         string                        `json:"manifest_hash,omitempty"`
	StrategyHash         string                        `json:"strategy_hash,omitempty"`
	ContextMode          string                        `json:"context_mode,omitempty"`
	ContextSources       []string                      `json:"context_sources,omitempty"`
	Model                any                           `json:"model,omitempty"`
	Runtime              any                           `json:"runtime,omitempty"`
	Tools                any                           `json:"tools,omitempty"`
	Skills               any                           `json:"skills,omitempty"`
	Collaboration        any                           `json:"collaboration,omitempty"`
	Memory               any                           `json:"memory,omitempty"`
	Knowledge            any                           `json:"knowledge,omitempty"`
	Repair               any                           `json:"repair,omitempty"`
	Output               any                           `json:"output,omitempty"`
	GuardrailAdjustments []any                         `json:"guardrail_adjustments,omitempty"`
	AgentPackage         contracts.PackageVersionID    `json:"agent_package,omitempty"`
	ResolvedAt           *time.Time                    `json:"resolved_at,omitempty"`
}

type runContextDiagnostic struct {
	StrategyHash       string                               `json:"strategy_hash,omitempty"`
	Mode               string                               `json:"mode,omitempty"`
	TokenBudget        int                                  `json:"token_budget,omitempty"`
	EstimatedTokensIn  int                                  `json:"estimated_tokens_in,omitempty"`
	EstimatedTokensOut int                                  `json:"estimated_tokens_out,omitempty"`
	Sources            []contracts.ContextSourceReport      `json:"sources,omitempty"`
	ExternalSources    []contracts.ContextSourceReport      `json:"external_sources,omitempty"`
	Compression        *contracts.ContextCompressionReport  `json:"compression,omitempty"`
	CompressionEvents  []contracts.ContextCompressionReport `json:"compression_events,omitempty"`
	PromptBundleHash   string                               `json:"prompt_bundle_hash,omitempty"`
}

type runModelCallDiagnostic struct {
	EventType        string    `json:"event_type"`
	ModelProvider    string    `json:"model_provider,omitempty"`
	ModelName        string    `json:"model_name,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	RepairAttempt    int       `json:"repair_attempt,omitempty"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type runDecisionDiagnostic struct {
	DecisionID    string         `json:"decision_id,omitempty"`
	Type          string         `json:"type,omitempty"`
	EventType     string         `json:"event_type"`
	RepairAttempt int            `json:"repair_attempt,omitempty"`
	Warnings      any            `json:"warnings,omitempty"`
	Error         string         `json:"error,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Payload       map[string]any `json:"payload,omitempty"`
}

type runToolDiagnostic struct {
	Call      contracts.ToolCall    `json:"call"`
	Result    *contracts.ToolResult `json:"result,omitempty"`
	HasResult bool                  `json:"has_result"`
}

type toolAgentCallDiagnostic struct {
	ProviderAgentID string               `json:"provider_agent_id,omitempty"`
	ToolID          string               `json:"tool_id,omitempty"`
	Operation       string               `json:"operation,omitempty"`
	Status          string               `json:"status,omitempty"`
	RunID           contracts.AgentRunID `json:"run_id,omitempty"`
	TaskID          contracts.TaskID     `json:"task_id,omitempty"`
	ErrorSummary    string               `json:"error_summary,omitempty"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	CompletedAt     *time.Time           `json:"completed_at,omitempty"`
}

func handleRuns(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	switch r.Method {
	case http.MethodGet:
		handleRunList(w, r, appCore, caller)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runs method", nil), http.StatusMethodNotAllowed)
	}
}

func handleRunResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	runIDRaw, suffix, _ := strings.Cut(strings.Trim(path, "/"), "/")
	if runIDRaw == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "run_id is required", nil), http.StatusBadRequest)
		return
	}
	run, ok := runForCaller(w, r, appCore, caller, contracts.AgentRunID(runIDRaw))
	if !ok {
		return
	}
	switch suffix {
	case "":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported run method", nil), http.StatusMethodNotAllowed)
			return
		}
		detail, err := buildRunDetail(r, appCore, caller, run)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, detail, http.StatusOK)
	case "timeline":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported run timeline method", nil), http.StatusMethodNotAllowed)
			return
		}
		timeline, err := buildRunTimeline(r, appCore, caller, run)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"run_id": run.RunID, "timeline": timeline, "meta": runResponseMeta(caller, run.TraceID)}, http.StatusOK)
	case "diagnostics":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported run diagnostics method", nil), http.StatusMethodNotAllowed)
			return
		}
		diagnostics, err := buildRunDiagnostics(r, appCore, caller, run)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, diagnostics, http.StatusOK)
	case "final-response":
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported run final-response method", nil), http.StatusMethodNotAllowed)
			return
		}
		response, artifacts, err := runFinalResponse(r, appCore, caller, run)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"run_id": run.RunID, "response": response, "artifacts": artifacts, "meta": runResponseMeta(caller, run.TraceID)}, http.StatusOK)
	case "replay":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported run replay method", nil), http.StatusMethodNotAllowed)
			return
		}
		events, err := traceEventsForRun(r, appCore, caller, run)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, replay.Build(events), http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown run resource path", map[string]any{"path": path}), http.StatusNotFound)
	}
}

func handleRunList(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	query := r.URL.Query()
	limit := queryInt(query.Get("limit"), 50, 200)
	offset := queryInt(query.Get("offset"), 0, 0)
	if offset == 0 {
		offset = queryInt(query.Get("cursor"), 0, 0)
	}
	from, err := queryTime(query.Get("from"))
	if err != nil {
		writeInvalidRunTimeFilter(w, "from")
		return
	}
	if from.IsZero() {
		from, err = queryTime(query.Get("started_from"))
		if err != nil {
			writeInvalidRunTimeFilter(w, "started_from")
			return
		}
	}
	to, err := queryTime(query.Get("to"))
	if err != nil {
		writeInvalidRunTimeFilter(w, "to")
		return
	}
	if to.IsZero() {
		to, err = queryTime(query.Get("started_to"))
		if err != nil {
			writeInvalidRunTimeFilter(w, "started_to")
			return
		}
	}
	filter := runrepo.ListFilter{
		TenantID: caller.TenantID,
		AgentID:  contracts.AgentID(strings.TrimSpace(query.Get("agent_id"))),
		Status:   contracts.RunStatus(strings.TrimSpace(query.Get("status"))),
		TraceID:  contracts.TraceID(strings.TrimSpace(query.Get("trace_id"))),
		TaskID:   contracts.TaskID(strings.TrimSpace(query.Get("task_id"))),
		From:     from,
		To:       to,
		Limit:    limit,
		Offset:   offset,
	}
	if filter.Status != "" {
		if err := filter.Status.Validate(); err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, err.Error(), nil), http.StatusBadRequest)
			return
		}
	}
	runs, err := appCore.Runs.List(r.Context(), filter)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	meta := runResponseMeta(caller, filter.TraceID)
	meta["limit"] = limit
	meta["offset"] = offset
	meta["count"] = len(runs)
	if !filter.From.IsZero() {
		meta["from"] = filter.From
	}
	if !filter.To.IsZero() {
		meta["to"] = filter.To
	}
	if len(runs) == limit {
		meta["next_cursor"] = strconv.Itoa(offset + limit)
	}
	writeJSON(w, map[string]any{"runs": runs, "meta": meta}, http.StatusOK)
}

func buildRunDetail(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) (map[string]any, error) {
	traceEvents, err := traceEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	toolCalls, toolResults, err := toolsForRun(r, appCore, run)
	if err != nil {
		return nil, err
	}
	auditEvents, err := auditEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	hookEvents, err := hookEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"run":                  run,
		"trace_summary":        traceSummary(traceEvents),
		"tool_summary":         toolSummary(toolCalls, toolResults),
		"runtime_hook_summary": runtimeHookSummary(hookEvents),
		"audit_summary":        auditSummary(auditEvents),
		"meta":                 runResponseMeta(caller, run.TraceID),
	}, nil
}

func traceSummary(events []contracts.TraceEvent) map[string]any {
	types := map[string]int{}
	var firstAt *time.Time
	var lastAt *time.Time
	for _, event := range events {
		types[event.Type]++
		at := event.CreatedAt
		if firstAt == nil || at.Before(*firstAt) {
			firstAt = &at
		}
		if lastAt == nil || at.After(*lastAt) {
			lastAt = &at
		}
	}
	return map[string]any{
		"events_total": len(events),
		"types":        types,
		"first_at":     firstAt,
		"last_at":      lastAt,
	}
}

func toolSummary(calls []contracts.ToolCall, results []contracts.ToolResult) map[string]any {
	toolIDs := make([]string, 0, len(calls))
	failures := 0
	statuses := map[string]int{}
	for _, call := range calls {
		toolIDs = append(toolIDs, call.ToolID)
	}
	for _, result := range results {
		statuses[string(result.Status)]++
		if result.Status == contracts.ToolResultFailed || result.Status == contracts.ToolResultDenied {
			failures++
		}
	}
	return map[string]any{
		"calls_total":    len(calls),
		"results_total":  len(results),
		"failures_total": failures,
		"tool_ids":       uniqueStrings(toolIDs),
		"statuses":       statuses,
	}
}

func runtimeHookSummary(events []runtimehook.HookEvent) map[string]any {
	statuses := map[string]int{}
	phases := map[string]int{}
	hookIDs := make([]string, 0, len(events))
	for _, event := range events {
		statuses[event.Status]++
		phases[string(event.Phase)]++
		hookIDs = append(hookIDs, event.HookID)
	}
	return map[string]any{
		"events_total": len(events),
		"statuses":     statuses,
		"phases":       phases,
		"hook_ids":     uniqueStrings(hookIDs),
	}
}

func auditSummary(events []contracts.AuditEvent) map[string]any {
	actions := map[string]int{}
	decisions := map[string]int{}
	for _, event := range events {
		actions[event.Action]++
		decisions[event.Decision]++
	}
	return map[string]any{
		"events_total": len(events),
		"actions":      actions,
		"decisions":    decisions,
	}
}

func runForCaller(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, runID contracts.AgentRunID) (contracts.AgentRun, bool) {
	run, err := appCore.Runs.Get(r.Context(), runID)
	if err != nil {
		if errors.Is(err, storagerepo.ErrNotFound) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "run not found", map[string]any{"run_id": runID}), http.StatusNotFound)
			return contracts.AgentRun{}, false
		}
		writeRuntimeError(w, err)
		return contracts.AgentRun{}, false
	}
	if !sameTenant(run.TenantID, caller.TenantID) {
		writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "run tenant does not match caller tenant", nil), http.StatusForbidden)
		return contracts.AgentRun{}, false
	}
	return run, true
}

func buildRunDiagnostics(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) (runDiagnosticsResponse, error) {
	traceEvents, err := traceEventsForRun(r, appCore, caller, run)
	if err != nil {
		return runDiagnosticsResponse{}, err
	}
	taskEvents, err := taskEventsForRun(r, appCore, caller, run)
	if err != nil {
		return runDiagnosticsResponse{}, err
	}
	toolCalls, toolResults, err := toolsForRun(r, appCore, run)
	if err != nil {
		return runDiagnosticsResponse{}, err
	}
	auditEvents, err := auditEventsForRun(r, appCore, caller, run)
	if err != nil {
		return runDiagnosticsResponse{}, err
	}
	hookEvents, err := hookEventsForRun(r, appCore, caller, run)
	if err != nil {
		return runDiagnosticsResponse{}, err
	}
	timeline := buildTimeline(run, traceEvents, taskEvents, toolCalls, toolResults, auditEvents, hookEvents)
	report := replay.Build(traceEvents)
	usage := buildUsageEvidence(run.TraceID, caller.TenantID, traceEvents)
	diagnostics := runDiagnosticsResponse{
		Run:            runDiagnosticRecord(run, traceEvents),
		Summary:        diagnosticsSummary(run, traceEvents, taskEvents, toolCalls, toolResults, auditEvents, hookEvents, report),
		Routing:        routeDiagnostics(traceEvents),
		Strategy:       strategyDiagnostics(run, traceEvents, appCore),
		Context:        contextDiagnostics(traceEvents),
		Prompt:         promptDiagnostics(run, traceEvents),
		Model:          modelDiagnostics(run, traceEvents),
		Decisions:      decisionDiagnostics(traceEvents),
		Tools:          toolDiagnostics(toolCalls, toolResults),
		ToolAgentCalls: toolAgentCallDiagnostics(traceEvents),
		RuntimeHooks:   hookEvents,
		AuditEvents:    auditEvents,
		Replay:         report,
		UsageEvidence:  usage,
		Timeline:       timeline,
		Artifacts:      usage.ArtifactRefs,
		TraceEndpoints: map[string]string{"trace": "/v1/traces/" + string(run.TraceID), "replay": "/v1/traces/" + string(run.TraceID) + "/replay"},
		Meta:           runResponseMeta(caller, run.TraceID),
	}
	return diagnostics, nil
}

func runDiagnosticRecord(run contracts.AgentRun, events []contracts.TraceEvent) map[string]any {
	out := map[string]any{}
	data, err := json.Marshal(run)
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	if finalResponse, source := finalResponseFromEvents(events); finalResponse != "" {
		out["final_response"] = finalResponse
		out["output"] = finalResponse
		out["output_preview"] = finalResponse
		out["final_response_source"] = source
	}
	if run.ErrorMessage != "" {
		out["failure_reason"] = run.ErrorMessage
	}
	return out
}

func finalResponseFromEvents(events []contracts.TraceEvent) (string, string) {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type != contracts.TraceResponseSent {
			continue
		}
		if text := responseTextFromPayload(event.Payload); text != "" {
			return text, string(event.Type)
		}
	}
	return "", ""
}

func responseTextFromPayload(payload map[string]any) string {
	for _, key := range []string{"final_response", "reply_text", "output_preview", "output", "text", "content"} {
		if text := stringFromMap(payload, key); strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	if reply, ok := payload["reply"].(map[string]any); ok {
		if text := stringFromMap(reply, "text"); strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func buildTraceDiagnostics(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, traceID contracts.TraceID, events []contracts.TraceEvent) (traceDiagnosticsResponse, error) {
	if events == nil {
		var err error
		events, err = appCore.Trace.ListByTrace(r.Context(), traceID)
		if err != nil {
			return traceDiagnosticsResponse{}, err
		}
		filtered, ok := traceEventsForTenant(events, caller.TenantID)
		if !ok {
			return traceDiagnosticsResponse{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace tenant does not match caller tenant", nil)
		}
		events = filtered
	}
	runs, err := appCore.Runs.List(r.Context(), runrepo.ListFilter{TenantID: caller.TenantID, TraceID: traceID})
	if err != nil {
		return traceDiagnosticsResponse{}, err
	}
	var taskEvents []contracts.TaskEvent
	var toolCalls []contracts.ToolCall
	var toolResults []contracts.ToolResult
	var auditEvents []contracts.AuditEvent
	var hookEvents []runtimehook.HookEvent
	for _, run := range runs {
		runTaskEvents, err := taskEventsForRun(r, appCore, caller, run)
		if err != nil {
			return traceDiagnosticsResponse{}, err
		}
		taskEvents = append(taskEvents, runTaskEvents...)
		runToolCalls, runToolResults, err := toolsForRun(r, appCore, run)
		if err != nil {
			return traceDiagnosticsResponse{}, err
		}
		toolCalls = append(toolCalls, runToolCalls...)
		toolResults = append(toolResults, runToolResults...)
		runAuditEvents, err := auditEventsForRun(r, appCore, caller, run)
		if err != nil {
			return traceDiagnosticsResponse{}, err
		}
		auditEvents = append(auditEvents, runAuditEvents...)
		runHookEvents, err := hookEventsForRun(r, appCore, caller, run)
		if err != nil {
			return traceDiagnosticsResponse{}, err
		}
		hookEvents = append(hookEvents, runHookEvents...)
	}
	var baseRun contracts.AgentRun
	if len(runs) > 0 {
		baseRun = runs[0]
	}
	report := replay.Build(events)
	return traceDiagnosticsResponse{
		TraceID:        traceID,
		TenantID:       caller.TenantID,
		Runs:           runs,
		Summary:        diagnosticsSummary(baseRun, events, taskEvents, toolCalls, toolResults, auditEvents, hookEvents, report),
		Routing:        routeDiagnostics(events),
		Strategy:       strategyDiagnostics(baseRun, events, appCore),
		Context:        contextDiagnostics(events),
		Prompt:         promptDiagnostics(baseRun, events),
		Model:          modelDiagnostics(baseRun, events),
		Decisions:      decisionDiagnostics(events),
		ToolAgentCalls: toolAgentCallDiagnostics(events),
		Replay:         report,
		UsageEvidence:  buildUsageEvidence(traceID, caller.TenantID, events),
		Timeline:       buildTimeline(baseRun, events, taskEvents, toolCalls, toolResults, auditEvents, hookEvents),
		Meta:           runResponseMeta(caller, traceID),
	}, nil
}

func buildRunTimeline(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) ([]runTimelineEvent, error) {
	traceEvents, err := traceEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	taskEvents, err := taskEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	toolCalls, toolResults, err := toolsForRun(r, appCore, run)
	if err != nil {
		return nil, err
	}
	auditEvents, err := auditEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	hookEvents, err := hookEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, err
	}
	return buildTimeline(run, traceEvents, taskEvents, toolCalls, toolResults, auditEvents, hookEvents), nil
}

func traceEventsForRun(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) ([]contracts.TraceEvent, error) {
	if appCore.Trace == nil || run.TraceID == "" {
		return []contracts.TraceEvent{}, nil
	}
	events, err := appCore.Trace.ListByTrace(r.Context(), run.TraceID)
	if err != nil {
		return nil, err
	}
	events, ok := traceEventsForTenant(events, caller.TenantID)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace tenant does not match caller tenant", nil)
	}
	out := make([]contracts.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.RunID == "" || event.RunID == run.RunID {
			out = append(out, event)
		}
	}
	return out, nil
}

func taskEventsForRun(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) ([]contracts.TaskEvent, error) {
	if appCore.TaskEvents == nil || run.TaskID == "" {
		return []contracts.TaskEvent{}, nil
	}
	taskEvents, err := appCore.TaskEvents.ListByTask(r.Context(), run.TaskID)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.TaskEvent, 0, len(taskEvents))
	for _, event := range taskEvents {
		if event.TenantID != "" && event.TenantID != caller.TenantID {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task event tenant does not match caller tenant", nil)
		}
		if event.RunID == "" || event.RunID == run.RunID {
			out = append(out, event)
		}
	}
	return out, nil
}

func toolsForRun(r *http.Request, appCore *core.Core, run contracts.AgentRun) ([]contracts.ToolCall, []contracts.ToolResult, error) {
	if appCore.ToolRepo == nil {
		return []contracts.ToolCall{}, []contracts.ToolResult{}, nil
	}
	calls, err := appCore.ToolRepo.ListCallsByRun(r.Context(), run.RunID)
	if err != nil {
		return nil, nil, err
	}
	results, err := appCore.ToolRepo.ListResultsByRun(r.Context(), run.RunID)
	if err != nil {
		return nil, nil, err
	}
	return calls, results, nil
}

func auditEventsForRun(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) ([]contracts.AuditEvent, error) {
	if appCore.Audit == nil {
		return []contracts.AuditEvent{}, nil
	}
	merged := make([]contracts.AuditEvent, 0)
	seen := make(map[string]struct{})
	addEvents := func(events []contracts.AuditEvent) {
		for _, event := range events {
			key := auditEventKey(event)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, event)
		}
	}

	runEvents, err := appCore.Audit.Search(r.Context(), auditlog.Filter{TenantID: caller.TenantID, RunID: run.RunID})
	if err != nil {
		return nil, err
	}
	addEvents(runEvents)
	if run.TaskID != "" {
		taskEvents, err := appCore.Audit.Search(r.Context(), auditlog.Filter{TenantID: caller.TenantID, TaskID: run.TaskID})
		if err != nil {
			return nil, err
		}
		addEvents(taskEvents)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].AuditID < merged[j].AuditID
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged, nil
}

func auditEventKey(event contracts.AuditEvent) string {
	if event.AuditID != "" {
		return string(event.AuditID)
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", event.CreatedAt.Format(time.RFC3339Nano), event.Action, event.ResourceType, event.ResourceID, event.RunID, event.TaskID)
}

func hookEventsForRun(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) ([]runtimehook.HookEvent, error) {
	if appCore.RuntimeHooks == nil {
		return []runtimehook.HookEvent{}, nil
	}
	events, err := appCore.RuntimeHooks.ListEvents(r.Context(), caller.TenantID, run.TraceID)
	if err != nil {
		return nil, err
	}
	out := make([]runtimehook.HookEvent, 0, len(events))
	for _, event := range events {
		if event.RunID == "" || event.RunID == run.RunID {
			out = append(out, event)
		}
	}
	return out, nil
}

func buildTimeline(run contracts.AgentRun, traceEvents []contracts.TraceEvent, taskEvents []contracts.TaskEvent, toolCalls []contracts.ToolCall, toolResults []contracts.ToolResult, auditEvents []contracts.AuditEvent, hookEvents []runtimehook.HookEvent) []runTimelineEvent {
	out := make([]runTimelineEvent, 0, 2+len(traceEvents)+len(taskEvents)+len(toolCalls)+len(toolResults)+len(auditEvents)+len(hookEvents))
	if run.RunID != "" {
		out = append(out, runTimelineEvent{
			EventID:    "run:" + string(run.RunID) + ":created",
			Title:      "run.created",
			Status:     string(run.Status),
			Category:   "run",
			OccurredAt: run.StartedAt,
			Payload: safePayload(map[string]any{
				"run_id":        run.RunID,
				"trace_id":      run.TraceID,
				"task_id":       run.TaskID,
				"agent_id":      run.AgentID,
				"agent_version": run.AgentVersion,
				"policy_set_id": run.PolicySetID,
			}),
		})
		if run.CompletedAt != nil {
			out = append(out, runTimelineEvent{
				EventID:    "run:" + string(run.RunID) + ":completed",
				Title:      "run." + string(run.Status),
				Status:     string(run.Status),
				Category:   "run",
				OccurredAt: *run.CompletedAt,
				DurationMS: int(run.CompletedAt.Sub(run.StartedAt).Milliseconds()),
				Payload: safePayload(map[string]any{
					"error_code":    run.ErrorCode,
					"error_message": run.ErrorMessage,
				}),
			})
		}
	}
	for _, event := range traceEvents {
		out = append(out, runTimelineEvent{
			EventID:    fmt.Sprintf("trace:%s:%s:%d", event.SpanID, event.Type, event.CreatedAt.UnixNano()),
			Title:      event.Type,
			Status:     timelineStatus(event.Payload),
			Category:   "trace",
			OccurredAt: event.CreatedAt,
			Payload:    safePayload(event.Payload),
		})
	}
	for _, event := range taskEvents {
		out = append(out, runTimelineEvent{
			EventID:    string(event.EventID),
			Title:      event.Type,
			Status:     stringFromMap(event.Payload, "to_status"),
			Category:   "task",
			OccurredAt: event.CreatedAt,
			Actor:      strings.Trim(event.ActorType+":"+event.ActorID, ":"),
			Payload:    safePayload(event.Payload),
		})
	}
	for _, call := range toolCalls {
		out = append(out, runTimelineEvent{
			EventID:    "tool_call:" + string(call.ToolCallID),
			Title:      "tool.call " + call.ToolID,
			Category:   "tool",
			OccurredAt: call.CreatedAt,
			Payload: safePayload(map[string]any{
				"tool_call_id":      call.ToolCallID,
				"tool_id":           call.ToolID,
				"name":              call.Name,
				"tool_version":      call.ToolVersion,
				"execution_profile": call.ExecutionProfile,
				"arguments":         call.Arguments,
				"plan_step_id":      call.PlanStepID,
			}),
		})
	}
	for _, result := range toolResults {
		out = append(out, runTimelineEvent{
			EventID:    "tool_result:" + string(result.ToolResultID),
			Title:      "tool.result " + string(result.Status),
			Status:     string(result.Status),
			Category:   "tool",
			OccurredAt: result.CompletedAt,
			DurationMS: int(result.CompletedAt.Sub(result.StartedAt).Milliseconds()),
			Payload: safePayload(map[string]any{
				"tool_result_id": result.ToolResultID,
				"tool_call_id":   result.ToolCallID,
				"status":         result.Status,
				"output":         result.Output,
				"error":          result.Error,
				"artifact_refs":  result.ArtifactRefs,
			}),
		})
	}
	for _, event := range auditEvents {
		out = append(out, runTimelineEvent{
			EventID:    "audit:" + event.AuditID,
			Title:      event.Action,
			Status:     event.Decision,
			Category:   "audit",
			OccurredAt: event.CreatedAt,
			Actor:      strings.Trim(event.ActorType+":"+event.ActorID, ":"),
			Payload: safePayload(map[string]any{
				"resource_type": event.ResourceType,
				"resource_id":   event.ResourceID,
				"reason":        event.Reason,
			}),
		})
	}
	for _, event := range hookEvents {
		out = append(out, runTimelineEvent{
			EventID:    "runtime_hook:" + event.EventID,
			Title:      "runtime_hook." + string(event.Phase),
			Status:     event.Status,
			Category:   "runtime_hook",
			OccurredAt: event.CreatedAt,
			DurationMS: event.LatencyMS,
			Payload: safePayload(map[string]any{
				"hook_id":       event.HookID,
				"provider_id":   event.ProviderID,
				"provider_type": event.ProviderType,
				"phase":         event.Phase,
				"reason":        event.Reason,
				"patch":         event.Patch,
			}),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OccurredAt.Equal(out[j].OccurredAt) {
			if out[i].Category == out[j].Category {
				return out[i].EventID < out[j].EventID
			}
			return out[i].Category < out[j].Category
		}
		return out[i].OccurredAt.Before(out[j].OccurredAt)
	})
	return out
}

func diagnosticsSummary(run contracts.AgentRun, traceEvents []contracts.TraceEvent, taskEvents []contracts.TaskEvent, toolCalls []contracts.ToolCall, toolResults []contracts.ToolResult, auditEvents []contracts.AuditEvent, hookEvents []runtimehook.HookEvent, report replay.Report) runDiagnosticsSummary {
	usage := buildUsageEvidence(run.TraceID, run.TenantID, traceEvents)
	summary := runDiagnosticsSummary{
		Status:                 run.Status,
		ErrorCode:              run.ErrorCode,
		ErrorMessage:           run.ErrorMessage,
		StrategyLimitReason:    strategyLimitReason(run),
		TraceEventsTotal:       len(traceEvents),
		TaskEventsTotal:        len(taskEvents),
		ToolCallsTotal:         len(toolCalls),
		RuntimeHookEventsTotal: len(hookEvents),
		AuditEventsTotal:       len(auditEvents),
		ModelCallsTotal:        usage.ModelCalls,
		ModelFailuresTotal:     usage.ModelFailures,
		PromptTokensTotal:      usage.PromptTokens,
		CompletionTokensTotal:  usage.CompletionTokens,
		PromptBundleHashes:     report.PromptBundleHashes,
		PolicyVersions:         report.PolicyVersions,
		Problems:               report.Problems,
		FirstAt:                report.FirstAt,
		LastAt:                 report.LastAt,
		RunIDs:                 report.RunIDs,
	}
	if run.RunID != "" {
		summary.RunIDs = appendUniqueRunID(summary.RunIDs, run.RunID)
	}
	if run.CompletedAt != nil {
		summary.DurationMS = run.CompletedAt.Sub(run.StartedAt).Milliseconds()
	}
	for _, result := range toolResults {
		if result.Status == contracts.ToolResultFailed || result.Status == contracts.ToolResultDenied {
			summary.ToolFailuresTotal++
		}
	}
	return summary
}

func strategyLimitReason(run contracts.AgentRun) string {
	if run.Status != contracts.RunFailed {
		return ""
	}
	message := strings.ToLower(strings.TrimSpace(run.ErrorMessage))
	switch {
	case strings.Contains(message, "max steps exceeded"):
		return "runtime.max_steps"
	case strings.Contains(message, "max duration exceeded"):
		return "runtime.max_duration_seconds"
	case strings.Contains(message, "max prompt tokens exceeded"):
		return "prompt.max_prompt_tokens"
	case strings.Contains(message, "max tool calls exceeded"):
		return "tools.max_tool_calls"
	case strings.Contains(message, "max consecutive tool failures"):
		return "runtime.max_consecutive_tool_failures"
	default:
		return ""
	}
}

func routeDiagnostics(events []contracts.TraceEvent) []runRouteDiagnostic {
	out := []runRouteDiagnostic{}
	for _, event := range events {
		switch event.Type {
		case contracts.TraceAgentRouteResolved:
			out = append(out, runRouteDiagnostic{
				AgentID:           contracts.AgentID(stringFromMap(event.Payload, "agent_id")),
				RequestedVersion:  contracts.AgentVersion(stringFromMap(event.Payload, "requested_version")),
				ResolvedVersion:   contracts.AgentVersion(stringFromMap(event.Payload, "resolved_version")),
				ReleaseStatus:     contracts.ReleaseStatus(stringFromMap(event.Payload, "release_status")),
				PackageVersionID:  contracts.PackageVersionID(stringFromMap(event.Payload, "package_version_id")),
				CarrierKind:       contracts.AgentCarrierKind(stringFromMap(event.Payload, "carrier_kind")),
				RuntimeContract:   contracts.RuntimeContractKind(stringFromMap(event.Payload, "runtime_contract")),
				SourceKind:        contracts.AgentSourceKind(stringFromMap(event.Payload, "source_kind")),
				SourceProviderID:  stringFromMap(event.Payload, "source_provider_id"),
				ManifestVersion:   stringFromMap(event.Payload, "manifest_version"),
				ManifestHash:      stringFromMap(event.Payload, "manifest_hash"),
				StrategyHash:      stringFromMap(event.Payload, "strategy_hash"),
				RouteReason:       stringFromMap(event.Payload, "route_reason"),
				Canary:            boolFromMap(event.Payload, "canary"),
				CanaryPercent:     intFromMap(event.Payload, "canary_percent"),
				AssignmentKeyHash: stringFromMap(event.Payload, "assignment_key_hash"),
				CreatedAt:         event.CreatedAt,
			})
		case contracts.TraceCanaryRouted:
			out = append(out, runRouteDiagnostic{
				AgentID:          contracts.AgentID(stringFromMap(event.Payload, "agent_id")),
				ResolvedVersion:  contracts.AgentVersion(firstNonEmpty(stringFromMap(event.Payload, "resolved_version"), stringFromMap(event.Payload, "agent_version"))),
				ReleaseStatus:    contracts.ReleaseCanary,
				PackageVersionID: contracts.PackageVersionID(stringFromMap(event.Payload, "package_version_id")),
				CarrierKind:      contracts.AgentCarrierKind(stringFromMap(event.Payload, "carrier_kind")),
				RuntimeContract:  contracts.RuntimeContractKind(stringFromMap(event.Payload, "runtime_contract")),
				SourceKind:       contracts.AgentSourceKind(stringFromMap(event.Payload, "source_kind")),
				SourceProviderID: stringFromMap(event.Payload, "source_provider_id"),
				ManifestVersion:  stringFromMap(event.Payload, "manifest_version"),
				ManifestHash:     stringFromMap(event.Payload, "manifest_hash"),
				StrategyHash:     stringFromMap(event.Payload, "strategy_hash"),
				RouteReason:      "default_version_canary_route",
				Canary:           true,
				CanaryPercent:    intFromMap(event.Payload, "canary_percent"),
				CreatedAt:        event.CreatedAt,
			})
		}
	}
	return out
}

func strategyDiagnostics(run contracts.AgentRun, events []contracts.TraceEvent, appCore *core.Core) runStrategyDiagnostic {
	out := runStrategyDiagnostic{
		CarrierKind:      run.VersionSnapshot.CarrierKind,
		RuntimeContract:  run.VersionSnapshot.RuntimeContract,
		SourceKind:       run.VersionSnapshot.SourceKind,
		SourceProviderID: run.VersionSnapshot.SourceProviderID,
		ManifestVersion:  run.VersionSnapshot.ManifestVersion,
		ManifestHash:     run.VersionSnapshot.ManifestHash,
		StrategyHash:     run.VersionSnapshot.StrategyHash,
		AgentPackage:     run.VersionSnapshot.AgentPackage,
	}
	for _, event := range events {
		if event.Type == contracts.TraceStrategyGuardrailApplied {
			if len(out.GuardrailAdjustments) == 0 {
				out.GuardrailAdjustments = safeSliceValue(event.Payload["adjustments"])
			}
			continue
		}
		if event.Type != contracts.TraceStrategyResolved {
			continue
		}
		if out.StrategyHash == "" {
			out.StrategyHash = stringFromMap(event.Payload, "strategy_hash")
		}
		if out.CarrierKind == "" {
			out.CarrierKind = contracts.AgentCarrierKind(stringFromMap(event.Payload, "carrier_kind"))
		}
		if out.RuntimeContract == "" {
			out.RuntimeContract = contracts.RuntimeContractKind(stringFromMap(event.Payload, "runtime_contract"))
		}
		if out.SourceKind == "" {
			out.SourceKind = contracts.AgentSourceKind(stringFromMap(event.Payload, "source_kind"))
		}
		if out.SourceProviderID == "" {
			out.SourceProviderID = stringFromMap(event.Payload, "source_provider_id")
		}
		if out.ManifestVersion == "" {
			out.ManifestVersion = stringFromMap(event.Payload, "manifest_version")
		}
		if out.ManifestHash == "" {
			out.ManifestHash = stringFromMap(event.Payload, "manifest_hash")
		}
		if out.AgentPackage == "" {
			out.AgentPackage = contracts.PackageVersionID(stringFromMap(event.Payload, "agent_package"))
		}
		if out.ContextMode == "" {
			out.ContextMode = stringFromMap(event.Payload, "context_mode")
		}
		if len(out.ContextSources) == 0 {
			out.ContextSources = stringSliceFromMap(event.Payload, "context_sources")
		}
		if out.Model == nil {
			out.Model = safeValue(event.Payload["model"])
		}
		if out.Runtime == nil {
			out.Runtime = safeValue(event.Payload["runtime"])
		}
		if out.Tools == nil {
			out.Tools = safeValue(event.Payload["tools"])
		}
		if out.Skills == nil {
			out.Skills = safeValue(event.Payload["skills"])
		}
		if out.Collaboration == nil {
			out.Collaboration = safeValue(event.Payload["collaboration"])
		}
		if out.Memory == nil {
			out.Memory = safeValue(event.Payload["memory"])
		}
		if out.Knowledge == nil {
			out.Knowledge = safeValue(event.Payload["knowledge"])
		}
		if out.Repair == nil {
			out.Repair = safeValue(event.Payload["repair"])
		}
		if out.Output == nil {
			out.Output = safeValue(event.Payload["output"])
		}
		if len(out.GuardrailAdjustments) == 0 {
			out.GuardrailAdjustments = safeSliceValue(event.Payload["guardrail_adjustments"])
		}
		resolvedAt := event.CreatedAt
		out.ResolvedAt = &resolvedAt
	}
	if appCore != nil && appCore.ToolCatalog != nil && run.TenantID != "" && out.SourceProviderID != "" {
		if provider, ok := appCore.ToolCatalog.GetProvider(run.TenantID, out.SourceProviderID); ok {
			out.ServiceConnectionID = provider.ServiceConnectionID
		}
	}
	return out
}

func contextDiagnostics(events []contracts.TraceEvent) runContextDiagnostic {
	out := runContextDiagnostic{}
	for _, event := range events {
		switch event.Type {
		case contracts.TraceContextCollectionCompleted:
			if report, ok := contextAssemblyReportFromAny(event.Payload["context_assembly_report"]); ok {
				out.StrategyHash = report.StrategyHash
				out.Mode = report.Mode
				out.TokenBudget = report.TokenBudget
				out.EstimatedTokensIn = report.EstimatedTokensIn
				out.EstimatedTokensOut = report.EstimatedTokensOut
				out.Sources = append([]contracts.ContextSourceReport(nil), report.Sources...)
				out.ExternalSources = externalContextSources(report.Sources)
				if report.Compression != nil {
					compression := *report.Compression
					out.Compression = &compression
				}
			}
		case contracts.TracePromptBundleBuilt:
			if hash := stringFromMap(event.Payload, "hash"); hash != "" {
				out.PromptBundleHash = hash
			}
			if report, ok := contextAssemblyReportFromAny(event.Payload["context_assembly_report"]); ok {
				out.StrategyHash = report.StrategyHash
				out.Mode = report.Mode
				out.TokenBudget = report.TokenBudget
				out.EstimatedTokensIn = report.EstimatedTokensIn
				out.EstimatedTokensOut = report.EstimatedTokensOut
				out.Sources = append([]contracts.ContextSourceReport(nil), report.Sources...)
				out.ExternalSources = externalContextSources(report.Sources)
				if report.Compression != nil {
					compression := *report.Compression
					out.Compression = &compression
				}
			}
			if report, ok := contextCompressionReportFromAny(event.Payload["compression_report"]); ok {
				out.Compression = &report
			}
		case contracts.TraceContextCompressionCompleted:
			if report, ok := contextCompressionReportFromAny(event.Payload); ok {
				out.CompressionEvents = append(out.CompressionEvents, report)
				out.Compression = &report
			}
		}
	}
	return out
}

func contextAssemblyReportFromAny(value any) (contracts.ContextAssemblyReport, bool) {
	if value == nil {
		return contracts.ContextAssemblyReport{}, false
	}
	if report, ok := value.(contracts.ContextAssemblyReport); ok {
		return report, true
	}
	if report, ok := value.(*contracts.ContextAssemblyReport); ok && report != nil {
		return *report, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return contracts.ContextAssemblyReport{}, false
	}
	var report contracts.ContextAssemblyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return contracts.ContextAssemblyReport{}, false
	}
	if report.StrategyHash == "" && report.Mode == "" && len(report.Sources) == 0 && report.TokenBudget == 0 && report.Compression == nil {
		return contracts.ContextAssemblyReport{}, false
	}
	return report, true
}

func contextCompressionReportFromAny(value any) (contracts.ContextCompressionReport, bool) {
	if value == nil {
		return contracts.ContextCompressionReport{}, false
	}
	if report, ok := value.(contracts.ContextCompressionReport); ok {
		return report, true
	}
	if report, ok := value.(*contracts.ContextCompressionReport); ok && report != nil {
		return *report, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return contracts.ContextCompressionReport{}, false
	}
	var report contracts.ContextCompressionReport
	if err := json.Unmarshal(data, &report); err != nil {
		return contracts.ContextCompressionReport{}, false
	}
	if report.Mode == "" && report.InputTokens == 0 && report.OutputTokens == 0 && report.SummaryHash == "" && report.FailureReason == "" && len(report.SourceRefs) == 0 && !report.Applied {
		return contracts.ContextCompressionReport{}, false
	}
	return report, true
}

func externalContextSources(sources []contracts.ContextSourceReport) []contracts.ContextSourceReport {
	out := make([]contracts.ContextSourceReport, 0)
	for _, source := range sources {
		switch source.SourceType {
		case "runtime_hook_context", "agent_plugin_context":
			out = append(out, source)
		}
	}
	return out
}

func promptDiagnostics(run contracts.AgentRun, events []contracts.TraceEvent) runPromptDiagnostic {
	hashes := uniqueStrings(promptHashes(events))
	if run.VersionSnapshot.PromptBundleHash != "" {
		hashes = uniqueStrings(append([]string{run.VersionSnapshot.PromptBundleHash}, hashes...))
	}
	snapshots := promptSnapshots(events)
	steps := promptAssemblySteps(events, snapshots)
	preview := map[string]any{}
	if run.AgentID != "" {
		preview = map[string]any{
			"command": "prompt.preview",
			"target":  map[string]any{"agent_id": run.AgentID, "version": run.AgentVersion},
			"payload": map[string]any{"input": run.Input},
			"context": map[string]any{"tenant_id": run.TenantID},
		}
	}
	return runPromptDiagnostic{
		PromptBundleHash:   firstString(hashes),
		PromptBundleHashes: hashes,
		PolicySetID:        run.VersionSnapshot.PolicySet,
		PolicyVersionID:    run.VersionSnapshot.PolicyVersionID,
		PolicyVersion:      run.VersionSnapshot.PolicySetVersion,
		PromptRecorded:     len(snapshots) > 0,
		PreviewCommand:     preview,
		RedactionPolicy:    promptRedactionPolicy(len(snapshots) > 0),
		AssemblySteps:      steps,
		PromptSnapshots:    snapshots,
	}
}

func promptRedactionPolicy(recorded bool) string {
	if recorded {
		return "prompt snapshots are recorded for local diagnostics; sensitive secrets should still be redacted by upstream policies"
	}
	return "raw PromptBundle is not stored in run logs; use prompt.preview with optimizer/admin roles to reconstruct a sanitized preview"
}

func promptSnapshots(events []contracts.TraceEvent) []promptSnapshotView {
	out := []promptSnapshotView{}
	for _, event := range events {
		if event.Type != contracts.TraceModelCalled {
			continue
		}
		var snapshot struct {
			PromptBundleHash string                   `json:"prompt_bundle_hash"`
			ModelProvider    string                   `json:"model_provider"`
			ModelName        string                   `json:"model_name"`
			RepairAttempt    int                      `json:"repair_attempt"`
			Messages         []promptMessageView      `json:"messages"`
			AssemblySteps    []promptAssemblyStepView `json:"assembly_steps"`
			TokensEstimate   int                      `json:"tokens_estimate"`
		}
		if !decodeJSONish(event.Payload["prompt_snapshot"], &snapshot) {
			continue
		}
		if len(snapshot.Messages) == 0 {
			continue
		}
		out = append(out, promptSnapshotView{
			SnapshotID:       fmt.Sprintf("%s-%d", event.Type, len(out)+1),
			PromptBundleHash: snapshot.PromptBundleHash,
			ModelProvider:    snapshot.ModelProvider,
			ModelName:        snapshot.ModelName,
			RepairAttempt:    snapshot.RepairAttempt,
			Messages:         snapshot.Messages,
			AssemblySteps:    normalizePromptAssemblySteps(snapshot.AssemblySteps),
			TokensEstimate:   snapshot.TokensEstimate,
			CreatedAt:        event.CreatedAt,
		})
	}
	return out
}

func promptAssemblySteps(events []contracts.TraceEvent, snapshots []promptSnapshotView) []promptAssemblyStepView {
	for i := len(snapshots) - 1; i >= 0; i-- {
		if len(snapshots[i].AssemblySteps) > 0 {
			return snapshots[i].AssemblySteps
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != contracts.TracePromptBundleBuilt {
			continue
		}
		steps := promptAssemblyStepsFromAny(events[i].Payload["assembly_steps"])
		if len(steps) > 0 {
			return steps
		}
	}
	return nil
}

func promptAssemblyStepsFromAny(value any) []promptAssemblyStepView {
	var out []promptAssemblyStepView
	if !decodeJSONish(value, &out) {
		return nil
	}
	return normalizePromptAssemblySteps(out)
}

func normalizePromptAssemblySteps(out []promptAssemblyStepView) []promptAssemblyStepView {
	for i := range out {
		if strings.TrimSpace(out[i].Content) == "" && strings.TrimSpace(out[i].ContentPreview) != "" {
			out[i].Content = out[i].ContentPreview
		}
	}
	return out
}

func promptMessagesFromAny(value any) []promptMessageView {
	var out []promptMessageView
	if !decodeJSONish(value, &out) {
		return nil
	}
	return out
}

func decodeJSONish(value any, target any) bool {
	if value == nil {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, target) == nil
}

func modelDiagnostics(run contracts.AgentRun, events []contracts.TraceEvent) runModelDiagnostic {
	out := runModelDiagnostic{Provider: run.VersionSnapshot.ModelProvider, Name: run.VersionSnapshot.ModelName}
	for _, event := range events {
		if event.Type != contracts.TraceModelCalled && event.Type != contracts.TraceModelCompleted {
			continue
		}
		row := runModelCallDiagnostic{
			EventType:        event.Type,
			ModelProvider:    stringFromMap(event.Payload, "model_provider"),
			ModelName:        stringFromMap(event.Payload, "model_name"),
			PromptTokens:     intFromMap(event.Payload, "prompt_tokens"),
			CompletionTokens: intFromMap(event.Payload, "completion_tokens"),
			RepairAttempt:    intFromMap(event.Payload, "repair_attempt"),
			Error:            stringFromMap(event.Payload, "error"),
			CreatedAt:        event.CreatedAt,
		}
		if out.Provider == "" {
			out.Provider = row.ModelProvider
		}
		if out.Name == "" {
			out.Name = row.ModelName
		}
		out.PromptTokensTotal += row.PromptTokens
		out.CompletionTokensTotal += row.CompletionTokens
		out.Calls = append(out.Calls, row)
	}
	return out
}

func decisionDiagnostics(events []contracts.TraceEvent) []runDecisionDiagnostic {
	out := []runDecisionDiagnostic{}
	for _, event := range events {
		switch event.Type {
		case contracts.TraceDecisionCreated, contracts.TraceDecisionValidated, contracts.TraceDecisionCompleted, contracts.TraceDecisionRepairRequested:
			out = append(out, runDecisionDiagnostic{
				DecisionID:    stringFromMap(event.Payload, "decision_id"),
				Type:          stringFromMap(event.Payload, "type"),
				EventType:     event.Type,
				RepairAttempt: intFromMap(event.Payload, "repair_attempt"),
				Warnings:      event.Payload["warnings"],
				Error:         stringFromMap(event.Payload, "error"),
				CreatedAt:     event.CreatedAt,
				Payload:       safePayload(event.Payload),
			})
		}
	}
	return out
}

func toolDiagnostics(calls []contracts.ToolCall, results []contracts.ToolResult) []runToolDiagnostic {
	byCall := map[contracts.ToolCallID]contracts.ToolResult{}
	for _, result := range results {
		byCall[result.ToolCallID] = result
	}
	out := make([]runToolDiagnostic, 0, len(calls))
	for _, call := range calls {
		call.Arguments = safePayload(call.Arguments)
		row := runToolDiagnostic{Call: call}
		if result, ok := byCall[call.ToolCallID]; ok {
			resultCopy := result
			resultCopy.Output = safePayload(resultCopy.Output)
			if resultCopy.Error != nil {
				resultCopy.Error = &contracts.ToolExecutionError{
					Code:    resultCopy.Error.Code,
					Message: resultCopy.Error.Message,
					Details: safePayload(resultCopy.Error.Details),
				}
			}
			row.Result = &resultCopy
			row.HasResult = true
		}
		out = append(out, row)
	}
	return out
}

func toolAgentCallDiagnostics(events []contracts.TraceEvent) []toolAgentCallDiagnostic {
	byKey := map[string]*toolAgentCallDiagnostic{}
	order := make([]string, 0)
	keyFor := func(event contracts.TraceEvent) string {
		key := strings.Join([]string{
			stringFromMap(event.Payload, "provider_agent_id"),
			stringFromMap(event.Payload, "tool_id"),
			stringFromMap(event.Payload, "operation"),
		}, "\x00")
		if strings.Trim(key, "\x00") == "" {
			key = fmt.Sprintf("%s:%d", event.SpanID, event.CreatedAt.UnixNano())
		}
		return key
	}
	for _, event := range events {
		if event.Type != "agent_tool.invoked" && event.Type != "agent_tool.completed" && event.Type != "agent_tool.failed" {
			continue
		}
		key := keyFor(event)
		row, ok := byKey[key]
		if !ok {
			row = &toolAgentCallDiagnostic{
				ProviderAgentID: stringFromMap(event.Payload, "provider_agent_id"),
				ToolID:          stringFromMap(event.Payload, "tool_id"),
				Operation:       stringFromMap(event.Payload, "operation"),
			}
			byKey[key] = row
			order = append(order, key)
		}
		switch event.Type {
		case "agent_tool.invoked":
			row.Status = "invoked"
			if row.StartedAt == nil {
				startedAt := event.CreatedAt
				row.StartedAt = &startedAt
			}
		case "agent_tool.completed":
			row.Status = firstNonEmpty(stringFromMap(event.Payload, "status"), "completed")
			row.RunID = contracts.AgentRunID(stringFromMap(event.Payload, "run_id"))
			row.TaskID = contracts.TaskID(stringFromMap(event.Payload, "task_id"))
			completedAt := event.CreatedAt
			row.CompletedAt = &completedAt
		case "agent_tool.failed":
			row.Status = "failed"
			row.ErrorSummary = summarizeDiagnosticError(event.Payload["error"])
			completedAt := event.CreatedAt
			row.CompletedAt = &completedAt
		}
	}
	out := make([]toolAgentCallDiagnostic, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}

func summarizeDiagnosticError(value any) string {
	switch current := value.(type) {
	case string:
		return truncateDiagnosticText(sanitizeDiagnosticErrorSummary(current), 180)
	case map[string]any:
		return truncateDiagnosticText(sanitizeDiagnosticErrorSummary(firstNonEmpty(stringFromMap(current, "message"), stringFromMap(current, "code"))), 180)
	default:
		return ""
	}
}

func sanitizeDiagnosticErrorSummary(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"capability_not_available",
		"no operation required",
		"no-op",
		"no_op",
		"tool result schema validation failed",
		"agent exported tool is not enabled",
		"trace_id",
		"run_id",
		"task_id",
		"tool_call_id",
		"stack trace",
		"panic:",
		"runtime error",
		"worker_id",
	} {
		if lower == marker || strings.Contains(lower, marker) {
			return "internal tool error hidden"
		}
	}
	return text
}

func truncateDiagnosticText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func runFinalResponse(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, run contracts.AgentRun) (map[string]any, []contracts.ArtifactID, error) {
	traceEvents, err := traceEventsForRun(r, appCore, caller, run)
	if err != nil {
		return nil, nil, err
	}
	response := map[string]any{"available": false, "reason": "final response body is not stored in run logs"}
	for i := len(traceEvents) - 1; i >= 0; i-- {
		event := traceEvents[i]
		if event.Type == contracts.TraceResponseSent {
			response = safePayload(event.Payload)
			response["available"] = true
			response["trace_event_type"] = event.Type
			response["created_at"] = event.CreatedAt
			break
		}
	}
	usage := buildUsageEvidence(run.TraceID, caller.TenantID, traceEvents)
	return response, usage.ArtifactRefs, nil
}

func runResponseMeta(caller auth.CallerIdentity, traceID contracts.TraceID) map[string]any {
	return map[string]any{
		"request_id": idgen.New("req"),
		"trace_id":   traceID,
		"tenant_id":  caller.TenantID,
	}
}

func queryInt(raw string, fallback int, max int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

func queryTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time filter %q", raw)
}

func writeInvalidRunTimeFilter(w http.ResponseWriter, parameter string) {
	writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid run time filter", map[string]any{
		"parameter":        parameter,
		"accepted_formats": []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"},
	}), http.StatusBadRequest)
}

func promptHashes(events []contracts.TraceEvent) []string {
	out := []string{}
	for _, event := range events {
		if value, ok := event.Payload["prompt_bundle_hash"].(string); ok && value != "" {
			out = append(out, value)
		}
		if event.Type == contracts.TracePromptBundleBuilt {
			if value, ok := event.Payload["hash"].(string); ok && value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func appendUniqueRunID(values []contracts.AgentRunID, value contracts.AgentRunID) []contracts.AgentRunID {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func timelineStatus(payload map[string]any) string {
	for _, key := range []string{"status", "run_status", "to_status", "decision"} {
		if value := stringFromMap(payload, key); value != "" {
			return value
		}
	}
	if stringFromMap(payload, "error") != "" {
		return "error"
	}
	return ""
}

func stringFromMap(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	switch value := payload[key].(type) {
	case string:
		return value
	case contracts.AgentID:
		return string(value)
	case contracts.AgentVersion:
		return string(value)
	case contracts.PackageVersionID:
		return string(value)
	case contracts.ReleaseStatus:
		return string(value)
	case contracts.PolicySetID:
		return string(value)
	case contracts.PolicyVersionID:
		return string(value)
	case contracts.TraceID:
		return string(value)
	case contracts.AgentRunID:
		return string(value)
	case contracts.TaskID:
		return string(value)
	case contracts.ErrorCode:
		return string(value)
	case contracts.RunStatus:
		return string(value)
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func stringSliceFromMap(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	switch values := payload[key].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if item, ok := value.(string); ok && item != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func intFromMap(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	return intValue(payload[key], 0)
}

func boolFromMap(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	return boolFromAny(payload[key])
}

func safePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		if sensitiveLogKey(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = safeValue(value)
	}
	return out
}

func safeSliceValue(value any) []any {
	if value == nil {
		return nil
	}
	if values, ok := value.([]any); ok {
		out := make([]any, 0, len(values))
		for _, item := range values {
			out = append(out, safeValue(item))
		}
		return out
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var values []any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil
	}
	for i := range values {
		values[i] = safeValue(values[i])
	}
	return values
}

func safeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return safePayload(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, safeValue(item))
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if sensitiveLogKey(key) || looksSensitiveLogValue(item) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = item
		}
		return out
	case *contracts.ToolExecutionError:
		if typed == nil {
			return nil
		}
		return contracts.ToolExecutionError{
			Code:    typed.Code,
			Message: typed.Message,
			Details: safePayload(typed.Details),
		}
	case contracts.ToolExecutionError:
		typed.Details = safePayload(typed.Details)
		return typed
	case string:
		if looksSensitiveLogValue(typed) {
			return "[redacted]"
		}
		return typed
	default:
		return value
	}
}

func sensitiveLogKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "apikey", "authorization", "password", "passwd", "secret", "clientsecret", "accesstoken", "refreshtoken", "sessiontoken", "privatekey":
		return true
	default:
		return false
	}
}

func looksSensitiveLogValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if len(trimmed) >= 12 && (strings.HasPrefix(trimmed, "sk-") || strings.HasPrefix(trimmed, "sk_")) {
		return true
	}
	if strings.HasPrefix(lower, "bearer ") && len(trimmed) > len("bearer ")+8 {
		return true
	}
	if strings.Contains(lower, "-----begin private key-----") {
		return true
	}
	return false
}
