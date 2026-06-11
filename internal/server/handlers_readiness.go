package server

import (
	"net/http"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/readiness"
)

func registerReadinessRoutes(mux *http.ServeMux, appCore *core.Core, authenticator auth.Authenticator) {
	cfg := appCore.Config
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		value := "ready"
		mode := requestReadinessMode(r, cfg.EffectiveReadinessMode())
		if !cfg.Readiness {
			status = http.StatusServiceUnavailable
			value = "not_ready"
		} else if mode == "deep" {
			report := readiness.Build(r.Context(), appCore, "migrations")
			value = report.Status
			if report.Status != "ready" {
				status = http.StatusServiceUnavailable
			}
		}
		writeJSON(w, healthResponse{
			Service: cfg.ServiceName,
			Version: cfg.Version,
			Status:  value,
			Mode:    mode,
			Time:    time.Now().UTC().Format(time.RFC3339Nano),
		}, status)
	})
	mux.HandleFunc("/v1/readiness/report", requireAuth(authenticator, func(w http.ResponseWriter, r *http.Request, caller auth.CallerIdentity) {
		writeJSON(w, readiness.Build(r.Context(), appCore, "migrations"), http.StatusOK)
	}))
}

func requestReadinessMode(r *http.Request, configured string) string {
	query := r.URL.Query()
	if readinessQueryWantsDeep(query.Get("deep")) ||
		strings.EqualFold(strings.TrimSpace(query.Get("mode")), "deep") ||
		strings.EqualFold(strings.TrimSpace(query.Get("readiness_mode")), "deep") {
		return "deep"
	}
	return configured
}

func readinessQueryWantsDeep(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "yes", "y", "on", "deep":
		return true
	default:
		return false
	}
}
