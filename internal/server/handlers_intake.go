package server

import (
	"net/http"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/intake"
)

func handleIntakePolicies(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.Intake == nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, "intake service is not configured", nil), http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		policies, err := appCore.Intake.List(r.Context(), caller.TenantID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policies": policies}, http.StatusOK)
	case http.MethodPost:
		var policy intake.Policy
		if !decodeJSONPayload(w, r, &policy, "invalid intake policy payload") {
			return
		}
		policy.TenantID = caller.TenantID
		saved, err := appCore.Intake.Upsert(r.Context(), policy, caller.CallerID, caller.CallerType)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": saved}, http.StatusCreated)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
	}
}

func handleIntakePolicyResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, policyID string) {
	if appCore.Intake == nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, "intake service is not configured", nil), http.StatusInternalServerError)
		return
	}
	if policyID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "policy_id is required", nil), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		policy, ok, err := appCore.Intake.Get(r.Context(), caller.TenantID, policyID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "intake policy not found", map[string]any{"policy_id": policyID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"policy": policy}, http.StatusOK)
	case http.MethodPut:
		var policy intake.Policy
		if !decodeJSONPayload(w, r, &policy, "invalid intake policy payload") {
			return
		}
		policy.TenantID = caller.TenantID
		policy.PolicyID = policyID
		saved, err := appCore.Intake.Upsert(r.Context(), policy, caller.CallerID, caller.CallerType)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"policy": saved}, http.StatusOK)
	case http.MethodDelete:
		policy, ok, err := appCore.Intake.Delete(r.Context(), caller.TenantID, policyID, caller.CallerID, caller.CallerType)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "intake policy not found", map[string]any{"policy_id": policyID}), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"policy": policy, "deleted": true}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
	}
}

func handleIntakeEvaluate(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.Intake == nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, "intake service is not configured", nil), http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "method not allowed", nil), http.StatusMethodNotAllowed)
		return
	}
	var req intake.EvaluateRequest
	if !decodeJSONPayload(w, r, &req, "invalid intake evaluate payload") {
		return
	}
	req.TenantID = caller.TenantID
	if req.CallerID == "" {
		req.CallerID = caller.CallerID
	}
	if req.CallerType == "" {
		req.CallerType = caller.CallerType
	}
	result, err := appCore.Intake.Evaluate(r.Context(), req)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"pre_reply": result}, http.StatusOK)
}
