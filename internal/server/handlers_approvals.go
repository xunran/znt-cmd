package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/governance/approval"
)

type approvalRequestPayload struct {
	ResourceType string              `json:"resource_type"`
	ResourceID   string              `json:"resource_id"`
	Action       string              `json:"action"`
	RiskLevel    contracts.RiskLevel `json:"risk_level"`
	Risk         contracts.RiskLevel `json:"risk"`
	Reason       string              `json:"reason"`
	RequestedBy  string              `json:"requested_by"`
	TraceID      contracts.TraceID   `json:"trace_id"`
	ExpiresAt    string              `json:"expires_at"`
}

type approvalResolvePayload struct {
	Status contracts.ApprovalStatus `json:"status"`
}

func handleApprovals(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.Approvals == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil))
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		status := contracts.ApprovalStatus(strings.TrimSpace(query.Get("status")))
		if status != "" && !validApprovalStatus(status) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported approval status", map[string]any{"status": status}), http.StatusBadRequest)
			return
		}
		approvals := appCore.Approvals.List(approval.ListFilter{
			TenantID:     caller.TenantID,
			ResourceType: strings.TrimSpace(query.Get("resource_type")),
			ResourceID:   strings.TrimSpace(query.Get("resource_id")),
			Action:       strings.TrimSpace(query.Get("action")),
			Status:       status,
			TraceID:      contracts.TraceID(strings.TrimSpace(query.Get("trace_id"))),
		})
		sortApprovals(approvals)
		writeJSON(w, map[string]any{"approvals": approvals}, http.StatusOK)
	case http.MethodPost:
		if !canMutateApprovals(caller) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "caller role is not allowed for approval mutation", nil), http.StatusForbidden)
			return
		}
		var payload approvalRequestPayload
		if !decodeJSONPayload(w, r, &payload, "invalid approval json") {
			return
		}
		risk := payload.RiskLevel
		if risk == "" {
			risk = payload.Risk
		}
		var expiresAt *time.Time
		if strings.TrimSpace(payload.ExpiresAt) != "" {
			parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.ExpiresAt))
			if err != nil {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid approval expires_at", map[string]any{"format": time.RFC3339Nano}), http.StatusBadRequest)
				return
			}
			parsed = parsed.UTC()
			expiresAt = &parsed
		}
		requestedBy := strings.TrimSpace(payload.RequestedBy)
		if requestedBy == "" {
			requestedBy = caller.CallerID
		}
		req, err := appCore.Approvals.Request(r.Context(), approval.RequestInput{
			TenantID:     caller.TenantID,
			ResourceType: strings.TrimSpace(payload.ResourceType),
			ResourceID:   strings.TrimSpace(payload.ResourceID),
			Action:       strings.TrimSpace(payload.Action),
			RiskLevel:    risk,
			Reason:       strings.TrimSpace(payload.Reason),
			RequestedBy:  requestedBy,
			TraceID:      payload.TraceID,
			ExpiresAt:    expiresAt,
		})
		if err != nil {
			writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, err.Error(), nil))
			return
		}
		writeJSON(w, map[string]any{"approval": req}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported approvals method", nil), http.StatusMethodNotAllowed)
	}
}

func handleApprovalResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, approvalID contracts.ApprovalID) {
	if appCore.Approvals == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "approval service is unavailable", nil))
		return
	}
	if approvalID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "approval_id is required", nil), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		req, ok := appCore.Approvals.Get(approvalID)
		if !ok || req.TenantID != caller.TenantID {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "approval not found", map[string]any{"approval_id": approvalID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"approval": req}, http.StatusOK)
	case http.MethodPatch:
		if !canMutateApprovals(caller) {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "caller role is not allowed for approval mutation", nil), http.StatusForbidden)
			return
		}
		var payload approvalResolvePayload
		if !decodeJSONPayload(w, r, &payload, "invalid approval patch json") {
			return
		}
		status := payload.Status
		var (
			req contracts.ApprovalRequest
			err error
		)
		switch status {
		case "", contracts.ApprovalApproved:
			req, err = appCore.Approvals.Approve(r.Context(), caller.TenantID, approvalID, caller.CallerID)
		case contracts.ApprovalRejected:
			req, err = appCore.Approvals.Reject(r.Context(), caller.TenantID, approvalID, caller.CallerID)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "approval patch supports only approved or rejected", map[string]any{"status": status}), http.StatusBadRequest)
			return
		}
		if err != nil {
			writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, err.Error(), nil))
			return
		}
		writeJSON(w, map[string]any{"approval": req}, http.StatusOK)
	case http.MethodDelete:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "approval delete is not supported; approve or reject instead", nil), http.StatusMethodNotAllowed)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported approval method", nil), http.StatusMethodNotAllowed)
	}
}

func canMutateApprovals(caller auth.CallerIdentity) bool {
	return caller.HasRole(auth.RoleOptimizer) || caller.HasRole(auth.RoleAdmin)
}

func sortApprovals(approvals []contracts.ApprovalRequest) {
	sort.SliceStable(approvals, func(i, j int) bool {
		if approvals[i].CreatedAt.Equal(approvals[j].CreatedAt) {
			return approvals[i].ApprovalID > approvals[j].ApprovalID
		}
		return approvals[i].CreatedAt.After(approvals[j].CreatedAt)
	})
}
