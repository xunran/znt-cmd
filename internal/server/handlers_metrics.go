package server

import (
	"net/http"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	governancemetrics "znt/internal/governance/metrics"
)

func registerMetricsRoutes(mux *http.ServeMux, appCore *core.Core, authenticator auth.Authenticator, metrics *metricsState) {
	metricsHandler := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metrics.snapshot(r.Context(), appCore), http.StatusOK)
	}
	if appCore != nil && appCore.Config.EffectiveMetricsAuthRequired() {
		mux.HandleFunc("/metrics", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, _ auth.CallerIdentity) {
			metricsHandler(w, r)
		}))
	} else {
		mux.HandleFunc("/metrics", metricsHandler)
	}
	mux.HandleFunc("/v1/metrics/governance", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		traceID := contracts.TraceID(r.URL.Query().Get("trace_id"))
		if traceID == "" {
			traceID = contracts.TraceID(r.URL.Query().Get("trace"))
		}
		if traceID == "" {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "governance metrics requires trace_id", nil), http.StatusBadRequest)
			return
		}
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
		writeJSON(w, governancemetrics.FromTrace(events), http.StatusOK)
	}))
}
