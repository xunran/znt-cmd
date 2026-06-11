package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/readiness"
)

type metricsState struct {
	requests                atomic.Int64
	failures                atomic.Int64
	bodyRejected            atomic.Int64
	durationTotalMS         atomic.Int64
	durationMaxMS           atomic.Int64
	agentRunsTotal          atomic.Int64
	agentRunFailures        atomic.Int64
	agentRunDurationTotalMS atomic.Int64
	agentRunDurationMaxMS   atomic.Int64
}

func (m *metricsState) observe(status int, duration time.Duration) {
	m.requests.Add(1)
	if status >= 500 {
		m.failures.Add(1)
	}
	if status == http.StatusRequestEntityTooLarge {
		m.bodyRejected.Add(1)
	}
	durationMS := duration.Milliseconds()
	m.durationTotalMS.Add(durationMS)
	observeAtomicMax(&m.durationMaxMS, durationMS)
}

func (m *metricsState) observeAgentRun(duration time.Duration, failed bool) {
	durationMS := duration.Milliseconds()
	m.agentRunsTotal.Add(1)
	if failed {
		m.agentRunFailures.Add(1)
	}
	m.agentRunDurationTotalMS.Add(durationMS)
	observeAtomicMax(&m.agentRunDurationMaxMS, durationMS)
}

func (m *metricsState) snapshot(ctx context.Context, appCore *core.Core) map[string]any {
	if ctx == nil {
		ctx = context.Background()
	}
	httpSnapshot := map[string]any{
		"requests_total":              m.requests.Load(),
		"failures_total":              m.failures.Load(),
		"request_body_rejected_total": m.bodyRejected.Load(),
		"duration_ms_total":           m.durationTotalMS.Load(),
		"duration_ms_max":             m.durationMaxMS.Load(),
	}
	agentRunSnapshot := map[string]any{
		"total":             m.agentRunsTotal.Load(),
		"failures_total":    m.agentRunFailures.Load(),
		"duration_ms_total": m.agentRunDurationTotalMS.Load(),
		"duration_ms_max":   m.agentRunDurationMaxMS.Load(),
		"queued":            int64(0),
		"running":           int64(0),
		"rejected_total":    int64(0),
	}
	admissionSnapshot := map[string]any{
		"run_max_concurrent":        int64(0),
		"tenant_run_max_concurrent": int64(0),
		"agent_run_max_concurrent":  int64(0),
		"agent_runs_running":        int64(0),
		"agent_runs_rejected_total": int64(0),
	}
	toolCallSnapshot := map[string]any{
		"running":        int64(0),
		"rejected_total": int64(0),
	}
	databaseSnapshot := map[string]any{
		"configured":           false,
		"max_open_connections": int64(0),
		"open_connections":     int64(0),
		"in_use_connections":   int64(0),
		"idle_connections":     int64(0),
		"wait_count":           int64(0),
		"wait_duration_ms":     int64(0),
	}
	readinessDatabaseSnapshot := map[string]any{
		"configured":           false,
		"max_open_connections": int64(0),
		"open_connections":     int64(0),
		"in_use_connections":   int64(0),
		"idle_connections":     int64(0),
		"wait_count":           int64(0),
		"wait_duration_ms":     int64(0),
	}
	readinessSnapshot := map[string]any{
		"status":        "unknown",
		"ready":         false,
		"checks_total":  int64(0),
		"failed_checks": int64(0),
		"warn_checks":   int64(0),
	}
	migrationSnapshot := map[string]any{
		"status": "unknown",
		"ready":  false,
	}
	out := map[string]any{
		"http":                              httpSnapshot,
		"agent_runs":                        agentRunSnapshot,
		"admission":                         admissionSnapshot,
		"tool_calls":                        toolCallSnapshot,
		"database":                          databaseSnapshot,
		"readiness_database":                readinessDatabaseSnapshot,
		"readiness":                         readinessSnapshot,
		"migration":                         migrationSnapshot,
		"http_requests_total":               httpSnapshot["requests_total"],
		"http_failures_total":               httpSnapshot["failures_total"],
		"http_request_body_rejected_total":  httpSnapshot["request_body_rejected_total"],
		"http_request_duration_ms_total":    httpSnapshot["duration_ms_total"],
		"http_request_duration_ms_max":      httpSnapshot["duration_ms_max"],
		"agent_runs_total":                  agentRunSnapshot["total"],
		"agent_run_failures_total":          agentRunSnapshot["failures_total"],
		"agent_run_duration_ms_total":       agentRunSnapshot["duration_ms_total"],
		"agent_run_duration_ms_max":         agentRunSnapshot["duration_ms_max"],
		"agent_runs_queued":                 agentRunSnapshot["queued"],
		"agent_runs_running":                agentRunSnapshot["running"],
		"agent_runs_rejected_total":         agentRunSnapshot["rejected_total"],
		"tool_calls_running":                toolCallSnapshot["running"],
		"tool_calls_rejected_total":         toolCallSnapshot["rejected_total"],
		"db_max_open_connections":           databaseSnapshot["max_open_connections"],
		"db_open_connections":               databaseSnapshot["open_connections"],
		"db_in_use_connections":             databaseSnapshot["in_use_connections"],
		"db_idle_connections":               databaseSnapshot["idle_connections"],
		"db_wait_count":                     databaseSnapshot["wait_count"],
		"db_wait_duration_ms":               databaseSnapshot["wait_duration_ms"],
		"db_readiness_max_open_connections": readinessDatabaseSnapshot["max_open_connections"],
		"db_readiness_open_connections":     readinessDatabaseSnapshot["open_connections"],
		"db_readiness_in_use_connections":   readinessDatabaseSnapshot["in_use_connections"],
		"db_readiness_idle_connections":     readinessDatabaseSnapshot["idle_connections"],
		"db_readiness_wait_count":           readinessDatabaseSnapshot["wait_count"],
		"db_readiness_wait_duration_ms":     readinessDatabaseSnapshot["wait_duration_ms"],
		"readiness_status":                  readinessSnapshot["status"],
		"readiness_ready":                   int64(0),
		"migration_status":                  migrationSnapshot["status"],
		"migration_ready":                   int64(0),
	}
	if appCore != nil {
		if appCore.Admission != nil {
			stats := appCore.Admission.Stats()
			admissionSnapshot["run_max_concurrent"] = int64(stats.MaxRunningRuns)
			admissionSnapshot["tenant_run_max_concurrent"] = int64(stats.TenantMaxRunningRuns)
			admissionSnapshot["agent_run_max_concurrent"] = int64(stats.AgentMaxRunningRuns)
			admissionSnapshot["agent_runs_running"] = int64(stats.RunningRuns)
			admissionSnapshot["agent_runs_rejected_total"] = stats.RejectedRunsTotal
			agentRunSnapshot["running"] = int64(stats.RunningRuns)
			agentRunSnapshot["rejected_total"] = stats.RejectedRunsTotal
		}
		if appCore.DB != nil {
			stats := appCore.DB.Stats()
			databaseSnapshot["configured"] = true
			databaseSnapshot["max_open_connections"] = int64(stats.MaxOpenConnections)
			databaseSnapshot["open_connections"] = int64(stats.OpenConnections)
			databaseSnapshot["in_use_connections"] = int64(stats.InUse)
			databaseSnapshot["idle_connections"] = int64(stats.Idle)
			databaseSnapshot["wait_count"] = stats.WaitCount
			databaseSnapshot["wait_duration_ms"] = stats.WaitDuration.Milliseconds()
		}
		if appCore.ReadinessDB != nil {
			stats := appCore.ReadinessDB.Stats()
			readinessDatabaseSnapshot["configured"] = true
			readinessDatabaseSnapshot["max_open_connections"] = int64(stats.MaxOpenConnections)
			readinessDatabaseSnapshot["open_connections"] = int64(stats.OpenConnections)
			readinessDatabaseSnapshot["in_use_connections"] = int64(stats.InUse)
			readinessDatabaseSnapshot["idle_connections"] = int64(stats.Idle)
			readinessDatabaseSnapshot["wait_count"] = stats.WaitCount
			readinessDatabaseSnapshot["wait_duration_ms"] = stats.WaitDuration.Milliseconds()
		}
		readinessCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		report := readiness.Build(readinessCtx, appCore, "migrations")
		readinessSnapshot["status"] = report.Status
		readinessSnapshot["ready"] = report.Status == "ready"
		readinessSnapshot["checks_total"] = int64(len(report.Checks))
		var failedChecks int64
		var warnChecks int64
		for _, check := range report.Checks {
			if check.Status == readiness.CheckFail {
				failedChecks++
			}
			if check.Status == readiness.CheckWarn {
				warnChecks++
			}
		}
		readinessSnapshot["failed_checks"] = failedChecks
		readinessSnapshot["warn_checks"] = warnChecks
		if report.Status == "ready" {
			out["readiness_ready"] = int64(1)
		}
		migrationStatus := "ready"
		for _, check := range report.Checks {
			if check.Name == "migration.files" || check.Name == "migration.schema" || check.Name == "migration.live_schema" {
				if check.Status == readiness.CheckFail {
					migrationStatus = "not_ready"
					break
				}
				if check.Status == readiness.CheckWarn && migrationStatus == "ready" {
					migrationStatus = "degraded"
				}
			}
		}
		migrationSnapshot["status"] = migrationStatus
		migrationSnapshot["ready"] = migrationStatus == "ready"
		out["migration_status"] = migrationStatus
		if migrationStatus == "ready" {
			out["migration_ready"] = int64(1)
		}
		out["run_max_concurrent"] = admissionSnapshot["run_max_concurrent"]
		out["tenant_run_max_concurrent"] = admissionSnapshot["tenant_run_max_concurrent"]
		out["agent_run_max_concurrent"] = admissionSnapshot["agent_run_max_concurrent"]
		out["agent_runs_running"] = agentRunSnapshot["running"]
		out["agent_runs_rejected_total"] = agentRunSnapshot["rejected_total"]
		out["db_max_open_connections"] = databaseSnapshot["max_open_connections"]
		out["db_open_connections"] = databaseSnapshot["open_connections"]
		out["db_in_use_connections"] = databaseSnapshot["in_use_connections"]
		out["db_idle_connections"] = databaseSnapshot["idle_connections"]
		out["db_wait_count"] = databaseSnapshot["wait_count"]
		out["db_wait_duration_ms"] = databaseSnapshot["wait_duration_ms"]
		out["db_readiness_max_open_connections"] = readinessDatabaseSnapshot["max_open_connections"]
		out["db_readiness_open_connections"] = readinessDatabaseSnapshot["open_connections"]
		out["db_readiness_in_use_connections"] = readinessDatabaseSnapshot["in_use_connections"]
		out["db_readiness_idle_connections"] = readinessDatabaseSnapshot["idle_connections"]
		out["db_readiness_wait_count"] = readinessDatabaseSnapshot["wait_count"]
		out["db_readiness_wait_duration_ms"] = readinessDatabaseSnapshot["wait_duration_ms"]
		out["readiness_status"] = readinessSnapshot["status"]
	}
	if _, ok := out["run_max_concurrent"]; !ok {
		out["run_max_concurrent"] = admissionSnapshot["run_max_concurrent"]
		out["tenant_run_max_concurrent"] = admissionSnapshot["tenant_run_max_concurrent"]
		out["agent_run_max_concurrent"] = admissionSnapshot["agent_run_max_concurrent"]
	}
	return out
}

func observeAtomicMax(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current {
			return
		}
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func logRequests(next http.Handler, logger *slog.Logger, metrics *metricsState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		duration := time.Since(start)
		if metrics != nil {
			metrics.observe(recorder.status, duration)
		}
		logger.Info("http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.String("tenant_id", r.Header.Get("X-Tenant-ID")),
			slog.String("trace_id", r.Header.Get("X-Trace-ID")),
			slog.Duration("duration", duration),
		)
	})
}

func recoverPanic(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", recovered),
				)
				writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, "internal server error", nil), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
