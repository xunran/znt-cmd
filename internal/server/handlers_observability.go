package server

import (
	"net/http"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	auditlog "znt/internal/governance/audit"
	tracequery "znt/internal/governance/trace"
)

func handleTraceList(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	limit, offset := queryLimitOffset(query, 30, 200)
	from, err := queryTimePtr(firstQueryValue(query, "from", "created_from", "started_from"))
	if err != nil {
		writeInvalidTimeFilter(w, "from")
		return
	}
	to, err := queryTimePtr(firstQueryValue(query, "to", "created_to", "started_to"))
	if err != nil {
		writeInvalidTimeFilter(w, "to")
		return
	}
	filter := tracequery.SummaryFilter{
		TenantID: caller.TenantID,
		TraceID:  contracts.TraceID(firstQueryValue(query, "trace_id", "traceId")),
		RunID:    contracts.AgentRunID(firstQueryValue(query, "run_id", "runId")),
		TaskID:   contracts.TaskID(firstQueryValue(query, "task_id", "taskId")),
		Type:     normalizedListFilter(firstQueryValue(query, "event_type", "eventType", "type")),
		Status:   contracts.RunStatus(normalizedListFilter(query.Get("status"))),
		Query:    firstQueryValue(query, "q", "search"),
		From:     from,
		To:       to,
		Limit:    limit,
		Offset:   offset,
	}
	summaries, err := appCore.Trace.ListSummaries(r.Context(), filter)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	total, err := appCore.Trace.CountSummaries(r.Context(), filter)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	meta := runResponseMeta(caller, filter.TraceID)
	meta["limit"] = limit
	meta["offset"] = offset
	meta["count"] = len(summaries)
	meta["total"] = total
	if filter.TraceID == "" {
		delete(meta, "trace_id")
	}
	if filter.Query != "" {
		meta["q"] = filter.Query
	}
	if filter.Type != "" {
		meta["event_type"] = filter.Type
	}
	if filter.Status != "" {
		meta["status"] = filter.Status
	}
	writeJSON(w, map[string]any{"traces": summaries, "total": total, "meta": meta}, http.StatusOK)
}

func handleAuditList(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	limit, offset := queryLimitOffset(query, 30, 200)
	from, err := queryTime(firstQueryValue(query, "from", "created_from", "started_from"))
	if err != nil {
		writeInvalidTimeFilter(w, "from")
		return
	}
	to, err := queryTime(firstQueryValue(query, "to", "created_to", "started_to"))
	if err != nil {
		writeInvalidTimeFilter(w, "to")
		return
	}
	filter := auditlog.Filter{
		TenantID:     caller.TenantID,
		Action:       normalizedListFilter(firstQueryValue(query, "action", "event_type", "eventType", "type")),
		ResourceID:   firstQueryValue(query, "resource_id", "resourceId"),
		ResourceType: normalizedListFilter(firstQueryValue(query, "resource_type", "resourceType")),
		TraceID:      contracts.TraceID(firstQueryValue(query, "trace_id", "traceId")),
		RunID:        contracts.AgentRunID(firstQueryValue(query, "run_id", "runId")),
		TaskID:       contracts.TaskID(firstQueryValue(query, "task_id", "taskId")),
		ActorID:      firstQueryValue(query, "actor_id", "actorId", "actor"),
		Decision:     normalizedListFilter(firstQueryValue(query, "decision")),
		Query:        firstQueryValue(query, "q", "search"),
		From:         from,
		To:           to,
		Limit:        limit,
		Offset:       offset,
		Desc:         true,
	}
	events, err := appCore.Audit.Search(r.Context(), filter)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	total, err := appCore.Audit.Count(r.Context(), filter)
	if err != nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
		return
	}
	meta := runResponseMeta(caller, filter.TraceID)
	meta["limit"] = limit
	meta["offset"] = offset
	meta["count"] = len(events)
	meta["total"] = total
	if filter.TraceID == "" {
		delete(meta, "trace_id")
	}
	if filter.Query != "" {
		meta["q"] = filter.Query
	}
	if filter.Action != "" {
		meta["action"] = filter.Action
	}
	if filter.Decision != "" {
		meta["decision"] = filter.Decision
	}
	writeJSON(w, map[string]any{"events": events, "total": total, "meta": meta}, http.StatusOK)
}

func queryTimePtr(raw string) (*time.Time, error) {
	value, err := queryTime(raw)
	if err != nil || value.IsZero() {
		return nil, err
	}
	return &value, nil
}

func normalizedListFilter(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.EqualFold(value, "all") {
		return ""
	}
	return value
}

func writeInvalidTimeFilter(w http.ResponseWriter, parameter string) {
	writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid time filter", map[string]any{
		"parameter":        parameter,
		"accepted_formats": []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"},
	}), http.StatusBadRequest)
}
