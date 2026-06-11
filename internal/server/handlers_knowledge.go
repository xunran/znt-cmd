package server

import (
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/knowledge"
)

func handleKnowledgeBases(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.Knowledge == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "knowledge service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		bases, err := appCore.Knowledge.ListKnowledgeBases(r.Context(), caller.TenantID, contracts.GroupID(r.URL.Query().Get("owner_group_id")))
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"knowledge_bases": bases}, http.StatusOK)
	case http.MethodPost:
		payload, ok := decodeMapPayload(w, r, "invalid knowledge base json")
		if !ok {
			return
		}
		base, err := appCore.Knowledge.CreateKnowledgeBase(r.Context(), knowledge.CreateKnowledgeBaseInput{
			TenantID:    caller.TenantID,
			GroupID:     contracts.GroupID(payloadString(payload, "owner_group_id")),
			RequestedBy: firstNonEmpty(payloadString(payload, "created_by"), caller.CallerID),
			Roles:       callerRoles(caller),
			Name:        payloadString(payload, "name"),
			Visibility:  payloadString(payload, "visibility"),
			SourceType:  payloadString(payload, "source_type"),
			IndexType:   payloadString(payload, "index_type"),
			TraceID:     contracts.TraceID(payloadString(payload, "trace_id")),
		})
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"knowledge_base": base}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported knowledge bases method", nil), http.StatusMethodNotAllowed)
	}
}

func handleKnowledgeBaseResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.Knowledge == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "knowledge service is unavailable", nil))
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "knowledge_base_id is required", nil), http.StatusBadRequest)
		return
	}
	baseID := contracts.KnowledgeBaseID(parts[0])
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported knowledge base method", nil), http.StatusMethodNotAllowed)
			return
		}
		base, ok, err := appCore.Knowledge.GetKnowledgeBase(r.Context(), caller.TenantID, baseID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "knowledge base not found", map[string]any{"knowledge_base_id": baseID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"knowledge_base": base}, http.StatusOK)
		return
	}
	switch parts[1] {
	case "documents":
		handleKnowledgeBaseDocuments(w, r, appCore, caller, baseID)
	case "index-jobs":
		handleKnowledgeBaseIndexJobs(w, r, appCore, caller, baseID, parts[2:])
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown knowledge base resource path", map[string]any{"path": path}), http.StatusNotFound)
	}
}

func handleKnowledgeBaseDocuments(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, baseID contracts.KnowledgeBaseID) {
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported knowledge document method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid knowledge document json")
	if !ok {
		return
	}
	doc, err := appCore.Knowledge.IngestDocument(r.Context(), contracts.KnowledgeDocument{
		TenantID:        caller.TenantID,
		KnowledgeBaseID: baseID,
		SourceGroupID:   contracts.GroupID(payloadString(payload, "source_group_id")),
		Title:           payloadString(payload, "title"),
		Content:         payloadString(payload, "content"),
		SourceURI:       payloadString(payload, "source_uri"),
		Visibility:      payloadString(payload, "visibility"),
		Metadata:        mapPayload(payload["metadata"]),
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	jobs, _ := appCore.Knowledge.ListIngestionJobs(r.Context(), caller.TenantID, baseID)
	var latest any
	if len(jobs) > 0 {
		latest = jobs[0]
	}
	writeJSON(w, map[string]any{"document": doc, "index_job": latest}, http.StatusCreated)
}

func handleKnowledgeBaseIndexJobs(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, baseID contracts.KnowledgeBaseID, parts []string) {
	if r.Method != http.MethodGet {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported index jobs method", nil), http.StatusMethodNotAllowed)
		return
	}
	if len(parts) == 0 || parts[0] == "" {
		jobs, err := appCore.Knowledge.ListIngestionJobs(r.Context(), caller.TenantID, baseID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"index_jobs": jobs}, http.StatusOK)
		return
	}
	job, ok, err := appCore.Knowledge.GetIngestionJob(r.Context(), caller.TenantID, contracts.KnowledgeIngestionJobID(parts[0]))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if !ok || job.KnowledgeBaseID != baseID {
		writeError(w, contracts.NewRuntimeError(contracts.CodeToolNotFound, "knowledge ingestion job not found", map[string]any{"job_id": parts[0]}), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"index_job": job}, http.StatusOK)
}

func handleKnowledgeSearch(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported knowledge search method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid knowledge search json")
	if !ok {
		return
	}
	results, err := appCore.Knowledge.Search(r.Context(), knowledge.SearchInput{
		TenantID:         caller.TenantID,
		RequesterGroupID: contracts.GroupID(payloadString(payload, "requester_group_id")),
		RequestedBy:      firstNonEmpty(payloadString(payload, "requested_by"), caller.CallerID),
		Roles:            callerRoles(caller),
		Query:            payloadString(payload, "query"),
		KnowledgeBaseIDs: knowledgeBaseIDsFromAny(payload["knowledge_base_ids"]),
		SourceGroupID:    contracts.GroupID(payloadString(payload, "source_group_id")),
		Limit:            payloadInt(payload, "limit"),
		AllowCrossGroup:  payloadBool(payload, "allow_cross_group"),
		SearchMode:       payloadString(payload, "search_mode"),
		TraceID:          contracts.TraceID(payloadString(payload, "trace_id")),
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"results": results}, http.StatusOK)
}
