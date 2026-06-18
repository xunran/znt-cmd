package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/config"
	"znt/internal/app/core"
	"znt/internal/contracts"
	agentdiscovery "znt/internal/discovery/agent"
	auditlog "znt/internal/governance/audit"
	"znt/internal/governance/replay"
	releasereport "znt/internal/release"
	taskrecovery "znt/internal/task/recovery"
	"znt/pkg/idgen"
)

func NewHandler(cfg config.Config, logger *slog.Logger) (http.Handler, error) {
	appCore, err := core.New(cfg)
	if err != nil {
		return nil, err
	}
	return NewHandlerWithCore(appCore, logger), nil
}

func NewHandlerWithCore(appCore *core.Core, logger *slog.Logger) http.Handler {
	cfg := appCore.Config
	authenticator := auth.New(cfg.ServiceToken)
	metrics := &metricsState{}
	mux := http.NewServeMux()
	registerHealthRoutes(mux, appCore)
	registerReadinessRoutes(mux, appCore, authenticator)
	registerMetricsRoutes(mux, appCore, authenticator, metrics)
	mux.HandleFunc("/v1/commands", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
			return
		}
		var envelope contracts.AgentEnvelope
		if !decodeJSONPayload(w, r, &envelope, "invalid command json") {
			return
		}
		if envelope.TraceID == "" {
			envelope.TraceID = contracts.TraceID(idgen.New("trace"))
		}
		if envelope.EnvelopeID == "" {
			envelope.EnvelopeID = idgen.New("env")
		}
		if envelope.Context.TenantID == "" {
			envelope.Context.TenantID = caller.TenantID
		}
		envelope.Caller = contracts.AgentCaller{CallerID: caller.CallerID, CallerType: caller.CallerType, TenantID: caller.TenantID}
		envelope.CreatedAt = time.Now().UTC()
		result, err := dispatchCommand(r, appCore, metrics, envelope, caller)
		if err != nil {
			status := http.StatusBadRequest
			var runtimeErr *contracts.RuntimeError
			if errors.As(err, &runtimeErr) {
				writeError(w, runtimeErr, statusForRuntimeError(runtimeErr, status))
				return
			}
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), status)
			return
		}
		writeJSON(w, result, http.StatusOK)
	}))
	mux.HandleFunc("/v1/approvals", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleApprovals(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/approvals/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleApprovalResource(w, r, appCore, caller, contracts.ApprovalID(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/approvals/"), "/")))
	}))
	mux.HandleFunc("/v1/traces/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/traces/")
		traceIDRaw, suffix, _ := strings.Cut(path, "/")
		traceID := contracts.TraceID(traceIDRaw)
		events, err := appCore.Trace.ListByTrace(r.Context(), traceID)
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		events, allowed := traceEventsForTenant(events, caller.TenantID)
		if !allowed {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "trace tenant does not match caller tenant", nil), http.StatusForbidden)
			return
		}
		if suffix == "replay" {
			writeJSON(w, replay.Build(events), http.StatusOK)
			return
		}
		if suffix == "diagnostics" {
			diagnostics, err := buildTraceDiagnostics(r, appCore, caller, traceID, events)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, diagnostics, http.StatusOK)
			return
		}
		writeJSON(w, map[string]any{"trace_id": traceID, "events": events}, http.StatusOK)
	}))
	mux.HandleFunc("/v1/runs", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuns(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/runs/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRunResource(w, r, appCore, caller, strings.TrimPrefix(r.URL.Path, "/v1/runs/"))
	}))
	mux.HandleFunc("/v1/tasks/start", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleTaskStartResource(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/tasks/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
		taskID, suffix, _ := strings.Cut(path, "/")
		if suffix == "timeline" {
			task, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeTaskCancelled, err.Error(), nil), http.StatusNotFound)
				return
			}
			if !sameTenant(task.TenantID, caller.TenantID) {
				writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil), http.StatusForbidden)
				return
			}
			events, err := appCore.TaskEvents.ListByTask(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"task_id": taskID, "events": events}, http.StatusOK)
			return
		}
		if suffix == "recovery" {
			task, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeTaskCancelled, err.Error(), nil), http.StatusNotFound)
				return
			}
			if !sameTenant(task.TenantID, caller.TenantID) {
				writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil), http.StatusForbidden)
				return
			}
			events, err := appCore.TaskEvents.ListByTask(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			writeJSON(w, taskrecovery.Check(task, events), http.StatusOK)
			return
		}
		if suffix == "plan" {
			task, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeTaskCancelled, err.Error(), nil), http.StatusNotFound)
				return
			}
			if !sameTenant(task.TenantID, caller.TenantID) {
				writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil), http.StatusForbidden)
				return
			}
			snapshot, err := appCore.Plans.Snapshot(r.Context(), contracts.TaskID(taskID))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			writeJSON(w, snapshot, http.StatusOK)
			return
		}
		task, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(taskID))
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeTaskCancelled, err.Error(), nil), http.StatusNotFound)
			return
		}
		if !sameTenant(task.TenantID, caller.TenantID) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil), http.StatusForbidden)
			return
		}
		writeJSON(w, task, http.StatusOK)
	}))
	mux.HandleFunc("/v1/audit", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		filter := auditlog.Filter{
			TenantID:   caller.TenantID,
			Action:     r.URL.Query().Get("action"),
			ResourceID: r.URL.Query().Get("resource_id"),
		}
		events, err := appCore.Audit.Search(r.Context(), filter)
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"events": events}, http.StatusOK)
	}))
	mux.HandleFunc("/v1/intake/policies", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleIntakePolicies(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/intake/policies/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleIntakePolicyResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/intake/policies/"), "/"))
	}))
	mux.HandleFunc("/v1/intake/evaluate", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleIntakeEvaluate(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/governance/templates", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleGovernanceTemplates(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/governance/runs", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleGovernanceRuns(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/governance/runs/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleGovernanceRunResource(w, r, appCore, caller, strings.TrimPrefix(r.URL.Path, "/v1/governance/runs/"))
	}))
	mux.HandleFunc("/v1/governance/gates/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleGovernanceGateResource(w, r, appCore, caller, strings.TrimPrefix(r.URL.Path, "/v1/governance/gates/"))
	}))
	mux.HandleFunc("/v1/governance/conflicts/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleGovernanceConflictResource(w, r, appCore, caller, strings.TrimPrefix(r.URL.Path, "/v1/governance/conflicts/"))
	}))
	mux.HandleFunc("/v1/tools/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/tools/")
		toolCallID, suffix, _ := strings.Cut(path, "/")
		if suffix != "trace" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown tool query", nil), http.StatusNotFound)
			return
		}
		call, ok, err := appCore.ToolRepo.GetCall(r.Context(), contracts.ToolCallID(toolCallID))
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool call not found", nil), http.StatusNotFound)
			return
		}
		if !sameTenant(call.TenantID, caller.TenantID) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool call tenant does not match caller tenant", nil), http.StatusForbidden)
			return
		}
		result, hasResult, err := appCore.ToolRepo.GetResultByCall(r.Context(), contracts.ToolCallID(toolCallID))
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"tool_call": call, "tool_result": result, "has_result": hasResult}, http.StatusOK)
	}))
	mux.HandleFunc("/v1/tool-providers", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolProviders(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/tool-provider-governance", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolProviderGovernance(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/tool-providers/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolProviderResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tool-providers/"), "/"))
	}))
	mux.HandleFunc("/v1/service-connections", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleServiceConnections(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/service-connections/templates", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleServiceConnectionTemplates(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/service-connections/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleServiceConnectionResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/service-connections/"), "/"))
	}))
	mux.HandleFunc("/v1/tool-groups", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolGroups(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/tool-groups/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolGroupResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tool-groups/"), "/"))
	}))
	mux.HandleFunc("/v1/tool-manifests", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolManifests(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/tool-manifests/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleToolManifestResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/tool-manifests/"), "/"))
	}))
	mux.HandleFunc("/v1/runtime-hook-providers", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookProviders(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/runtime-hook-providers/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookProviderResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/runtime-hook-providers/"), "/"))
	}))
	mux.HandleFunc("/v1/runtime-hook-manifests", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookManifests(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/runtime-hook-manifests/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookManifestResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/runtime-hook-manifests/"), "/"))
	}))
	mux.HandleFunc("/v1/runtime-hook-events", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookEvents(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/runtime-hook-governance", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookGovernance(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/runtime-hook-approvals", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleRuntimeHookApprovals(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/knowledge-bases", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleKnowledgeBases(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/knowledge-bases/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleKnowledgeBaseResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/knowledge-bases/"), "/"))
	}))
	mux.HandleFunc("/v1/knowledge-search", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleKnowledgeSearch(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/cross-group-share-policies", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleCrossGroupSharePolicies(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/cross-group-share-policies/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleCrossGroupSharePolicyResource(w, r, appCore, caller, strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/cross-group-share-policies/"), "/"))
	}))
	mux.HandleFunc("/v1/cross-groups/search", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleCrossGroupSearch(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/agents", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		switch r.Method {
		case http.MethodPost:
			handleAgentCreate(w, r, appCore, caller)
		case http.MethodGet:
			handleAgentList(w, r, appCore, caller)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agents method", nil), http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/v1/skills", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleSkills(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/skills/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleSkillResource(w, r, appCore, caller, strings.TrimPrefix(r.URL.Path, "/v1/skills/"))
	}))
	mux.HandleFunc("/v1/agents/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/agents/"), "/")
		if path == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent_id is required", nil), http.StatusBadRequest)
			return
		}
		parts := strings.Split(path, "/")
		agentID := contracts.AgentID(parts[0])
		if len(parts) > 1 {
			handleAgentSubresource(w, r, appCore, caller, agentID, parts[1:])
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleAgentGet(w, r, appCore, caller, agentID)
		case http.MethodPatch:
			handleAgentPatch(w, r, appCore, caller, agentID)
		case http.MethodDelete:
			handleAgentDelete(w, r, appCore, caller, agentID)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported agent method", nil), http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/v1/agents/capabilities", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		writeJSON(w, agentdiscovery.BuildIndex(appCore.AgentRegistry.ListByTenant(caller.TenantID)), http.StatusOK)
	}))
	mux.HandleFunc("/v1/evals/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/evals/")
		kind, id, ok := strings.Cut(path, "/")
		if !ok || id == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval lookup requires suites/{suite_id} or results/{eval_run_id}", nil), http.StatusBadRequest)
			return
		}
		switch kind {
		case "suites":
			suite, found, err := appCore.Evals.GetSuite(r.Context(), contracts.EvalSuiteID(id))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			if !found {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval suite not found", nil), http.StatusNotFound)
				return
			}
			if suite.TenantID != caller.TenantID {
				writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "eval suite tenant does not match caller tenant", nil), http.StatusForbidden)
				return
			}
			writeJSON(w, suite, http.StatusOK)
		case "results":
			result, found, err := appCore.Evals.GetResult(r.Context(), contracts.EvalRunID(id))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			if !found {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval result not found", nil), http.StatusNotFound)
				return
			}
			if result.TenantID != caller.TenantID {
				writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "eval result tenant does not match caller tenant", nil), http.StatusForbidden)
				return
			}
			writeJSON(w, result, http.StatusOK)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown eval lookup", map[string]any{"kind": kind}), http.StatusNotFound)
		}
	}))
	mux.HandleFunc("/v1/handoffs/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/handoffs/")
		handoffID, suffix, _ := strings.Cut(path, "/")
		handoff, ok := appCore.Handoffs.Get(contracts.HandoffID(handoffID))
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "handoff not found", nil), http.StatusNotFound)
			return
		}
		if !sameTenant(handoff.TenantID, caller.TenantID) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "handoff tenant does not match caller tenant", nil), http.StatusForbidden)
			return
		}
		if suffix == "trace" {
			events, err := appCore.TaskEvents.ListByTask(r.Context(), handoff.ParentTaskID)
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"handoff": handoff, "parent_task_events": events}, http.StatusOK)
			return
		}
		writeJSON(w, handoff, http.StatusOK)
	}))
	mux.HandleFunc("/v1/external-tasks/", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/external-tasks/")
		provider, externalID, ok := strings.Cut(path, "/")
		if !ok || provider == "" || externalID == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "external task lookup requires provider and id", nil), http.StatusBadRequest)
			return
		}
		ref := contracts.ExternalTaskRef{Provider: provider, ExternalTaskID: contracts.ExternalTaskID(externalID)}
		decision, err := appCore.ArrayBridge.CheckAccess(r.Context(), contracts.CollaborationAccessRequest{
			Ref:      ref,
			TenantID: caller.TenantID,
			CallerID: caller.CallerID,
			Action:   "read",
		})
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		if decision == nil || !decision.Allowed {
			reason := "external task access denied"
			if decision != nil && decision.Reason != "" {
				reason = decision.Reason
			}
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, reason, nil), http.StatusForbidden)
			return
		}
		summary, err := appCore.ArrayBridge.GetTask(r.Context(), ref)
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		writeJSON(w, summary, http.StatusOK)
	}))
	mux.HandleFunc("/v1/release/go-no-go", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		writeJSON(w, releasereport.BuildGoNoGo(r.Context(), appCore, "migrations", nil), http.StatusOK)
	}))
	mux.HandleFunc("/v1/usage/evidence", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		handleUsageEvidence(w, r, appCore, caller)
	}))
	mux.HandleFunc("/v1/agent-packages/canary-hits", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		agentID := contracts.AgentID(r.URL.Query().Get("agent_id"))
		if agentID == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent_id is required", nil), http.StatusBadRequest)
			return
		}
		hits, err := appCore.Packages.ListCanaryHits(r.Context(), caller.TenantID, agentID)
		if err != nil {
			writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"hits": hits}, http.StatusOK)
	}))
	return withMaxBodyBytes(recoverPanic(logRequests(mux, logger, metrics), logger), cfg.HTTPMaxBodyBytes)
}
