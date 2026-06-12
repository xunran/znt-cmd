package server

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/config"
	"znt/internal/app/core"
	"znt/internal/app/logging"
	"znt/internal/contracts"
	"znt/internal/eval"
	modelclient "znt/internal/model/client"
	runtimehook "znt/internal/runtime/hook"
	toolcatalog "znt/internal/tool/catalog"
)

func TestCommandAgentRunAndQueries(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName: "clean-core",
		Version:     "test",
		Env:         "test",
		HTTPAddr:    ":0",
		LogLevel:    "error",
		Readiness:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_api_1",
		"target": map[string]any{
			"agent_id": "test-agent",
			"version":  "v1",
		},
		"command": "agent.run",
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		RunID  contracts.AgentRunID `json:"run_id"`
		TaskID contracts.TaskID     `json:"task_id"`
		Status contracts.RunStatus  `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.TaskID == "" {
		t.Fatalf("unexpected result: %#v", result)
	}

	traceResp := doJSON(handler, "GET", "/v1/traces/trace_api_1", nil)
	if traceResp.Code != http.StatusOK {
		t.Fatalf("unexpected trace status %d body %s", traceResp.Code, traceResp.Body.String())
	}
	replayResp := doJSON(handler, "GET", "/v1/traces/trace_api_1/replay", nil)
	if replayResp.Code != http.StatusOK || !bytes.Contains(replayResp.Body.Bytes(), []byte(`"status":"ok"`)) {
		t.Fatalf("unexpected replay status %d body %s", replayResp.Code, replayResp.Body.String())
	}
	taskResp := doJSON(handler, "GET", "/v1/tasks/"+string(result.TaskID)+"/timeline", nil)
	if taskResp.Code != http.StatusOK {
		t.Fatalf("unexpected task status %d body %s", taskResp.Code, taskResp.Body.String())
	}
	recoveryResp := doJSON(handler, "GET", "/v1/tasks/"+string(result.TaskID)+"/recovery", nil)
	if recoveryResp.Code != http.StatusOK {
		t.Fatalf("unexpected recovery status %d body %s", recoveryResp.Code, recoveryResp.Body.String())
	}
	readyResp := doJSON(handler, "GET", "/v1/readiness/report", nil)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("unexpected readiness status %d body %s", readyResp.Code, readyResp.Body.String())
	}
	if !bytes.Contains(readyResp.Body.Bytes(), []byte(`"domain_id":"worker"`)) || !bytes.Contains(readyResp.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("expected readiness report to expose disabled future execution domains, body %s", readyResp.Body.String())
	}
	goNoGoResp := doJSON(handler, "GET", "/v1/release/go-no-go", nil)
	if goNoGoResp.Code != http.StatusOK {
		t.Fatalf("unexpected go/no-go status %d body %s", goNoGoResp.Code, goNoGoResp.Body.String())
	}
}

func TestOptimizerCommandDispatchCoverageForContractWarnings(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName: "clean-core",
		Version:     "test",
		Env:         "test",
		HTTPAddr:    ":0",
		LogLevel:    "error",
		Readiness:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	commands := []string{
		"agent.package.collaborator.remove",
		"agent.package.collaborator.replace",
		"agent.package.collaborator.update",
		"agent.package.draft.patch_agents_md",
		"agent.package.exported_tool.remove",
		"agent.package.exported_tool.replace",
		"agent.package.exported_tool.update",
		"agent.package.proposal.reject",
		"agent.package.review",
		"agent.package.runtime_hooks.update",
		"agent.package.skill.add",
		"agent.package.skill.remove",
		"agent.package.skill.update",
		"agent.package.tool_binding.update",
		"approval.reject",
		"permission.policy.upsert",
		"policy.canary",
		"policy.review",
		"policy.rollback",
		"policy.update",
		"runtime_hook.binding.list",
		"runtime_hook.preview",
		"runtime_hook.provider.list",
		"runtime_hook.provider.upsert",
		"tool.group.list",
		"tool.group.upsert",
		"tool.provider.sync",
		"tool.provider.upsert",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			resp := doJSONWithHeaders(handler, http.MethodPost, "/v1/commands", map[string]any{
				"command": command,
				"payload": map[string]any{},
				"context": map[string]any{"tenant_id": "tenant_1"},
			}, map[string]string{"X-Roles": "admin,optimizer,runtime_caller"})
			body := resp.Body.String()
			if resp.Code == http.StatusForbidden && strings.Contains(body, "caller role is not allowed") {
				t.Fatalf("command %s was rejected by role gate: %d %s", command, resp.Code, body)
			}
			if strings.Contains(body, "unsupported command") {
				t.Fatalf("command %s did not reach dispatch handler: %d %s", command, resp.Code, body)
			}
		})
	}
}

func TestReadyzDeepModeFailsWhenDatabaseHandleMissing(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:   "clean-core",
		Version:       "test",
		Env:           "test",
		HTTPAddr:      ":0",
		LogLevel:      "error",
		Readiness:     true,
		ReadinessMode: "deep",
	})
	if err != nil {
		t.Fatal(err)
	}
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = nil
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "GET", "/readyz", nil)
	if resp.Code != http.StatusServiceUnavailable || !bytes.Contains(resp.Body.Bytes(), []byte(`"status":"not_ready"`)) {
		t.Fatalf("expected deep readyz to fail without db handle, got %d body %s", resp.Code, resp.Body.String())
	}
}

func TestReadyzDeepModeFailsWhenDatabasePingFails(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:   "clean-core",
		Version:       "test",
		Env:           "test",
		HTTPAddr:      ":0",
		LogLevel:      "error",
		Readiness:     true,
		ReadinessMode: "deep",
	})
	if err != nil {
		t.Fatal(err)
	}
	db := openMetricsTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = db
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "GET", "/readyz", nil)
	if resp.Code != http.StatusServiceUnavailable || !bytes.Contains(resp.Body.Bytes(), []byte(`"status":"not_ready"`)) {
		t.Fatalf("expected deep readyz to fail when db ping fails, got %d body %s", resp.Code, resp.Body.String())
	}
	reportResp := doJSON(handler, "GET", "/v1/readiness/report", nil)
	if reportResp.Code != http.StatusOK || !bytes.Contains(reportResp.Body.Bytes(), []byte(`"status":"not_ready"`)) || !bytes.Contains(reportResp.Body.Bytes(), []byte(`"database"`)) {
		t.Fatalf("expected readiness report to expose db failure, got %d body %s", reportResp.Code, reportResp.Body.String())
	}
}

func TestReadyzShallowModeDoesNotRequireDatabase(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:   "clean-core",
		Version:       "test",
		Env:           "test",
		HTTPAddr:      ":0",
		LogLevel:      "error",
		Readiness:     true,
		ReadinessMode: "shallow",
	})
	if err != nil {
		t.Fatal(err)
	}
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = nil
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "GET", "/readyz", nil)
	if resp.Code != http.StatusOK || !bytes.Contains(resp.Body.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("expected shallow readyz to stay ready, got %d body %s", resp.Code, resp.Body.String())
	}
}

func TestReadyzExplicitDeepQueryOverridesShallowMode(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:   "clean-core",
		Version:       "test",
		Env:           "test",
		HTTPAddr:      ":0",
		LogLevel:      "error",
		Readiness:     true,
		ReadinessMode: "shallow",
	})
	if err != nil {
		t.Fatal(err)
	}
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = nil
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	for _, path := range []string{"/readyz?deep=1", "/readyz?deep=true", "/readyz?mode=deep"} {
		t.Run(path, func(t *testing.T) {
			resp := doJSON(handler, "GET", path, nil)
			if resp.Code != http.StatusServiceUnavailable ||
				!bytes.Contains(resp.Body.Bytes(), []byte(`"status":"not_ready"`)) ||
				!bytes.Contains(resp.Body.Bytes(), []byte(`"mode":"deep"`)) {
				t.Fatalf("expected explicit deep readyz to fail without db handle, got %d body %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestCommandBodyLimitRejectsLargeRequest(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:      "clean-core",
		Version:          "test",
		Env:              "test",
		HTTPAddr:         ":0",
		LogLevel:         "error",
		Readiness:        true,
		HTTPMaxBodyBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := `{"command":"agent.run","payload":{"input":"` + strings.Repeat("x", 256) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/commands", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant_1")
	req.Header.Set("X-Caller-ID", "user_1")
	req.Header.Set("X-Caller-Type", "user")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge || !bytes.Contains(resp.Body.Bytes(), []byte("request body too large")) {
		t.Fatalf("expected request body to be rejected, got %d body %s", resp.Code, resp.Body.String())
	}
	metricsResp := doJSON(handler, "GET", "/metrics", nil)
	metrics := decodeMetrics(t, metricsResp)
	if got := metricNumber(t, metrics, "http_request_body_rejected_total"); got != 1 {
		t.Fatalf("expected rejected body metric to be 1, got %v metrics %#v", got, metrics)
	}
}

func TestAgentRunAdmissionRejectsWhenLimitReached(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:      "clean-core",
		Version:          "test",
		Env:              "test",
		HTTPAddr:         ":0",
		LogLevel:         "error",
		Readiness:        true,
		RunMaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Admission.AcquireRun("tenant_1", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, http.MethodPost, "/v1/commands", map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if resp.Code != http.StatusTooManyRequests || !bytes.Contains(resp.Body.Bytes(), []byte(string(contracts.CodeAdmissionRejected))) {
		t.Fatalf("expected admission rejection, got %d body %s", resp.Code, resp.Body.String())
	}
	metricsResp := doJSON(handler, "GET", "/metrics", nil)
	metrics := decodeMetrics(t, metricsResp)
	if got := metricNumber(t, metrics, "agent_runs_rejected_total"); got != 1 {
		t.Fatalf("expected one rejected run, got %v metrics %#v", got, metrics)
	}
	if got := metricNumber(t, metrics, "agent_runs_running"); got != 1 {
		t.Fatalf("expected one running admitted slot, got %v metrics %#v", got, metrics)
	}
	if got := metricNumber(t, metrics, "run_max_concurrent"); got != 1 {
		t.Fatalf("expected global run limit metric, got %v metrics %#v", got, metrics)
	}
}

func TestMetricsExposeHTTPAgentRunReadinessAndMigration(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName: "clean-core",
		Version:     "test",
		Env:         "test",
		HTTPAddr:    ":0",
		LogLevel:    "error",
		Readiness:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, http.MethodPost, "/v1/commands", map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"input": "hello metrics"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	metricsResp := doJSON(handler, "GET", "/metrics", nil)
	metrics := decodeMetrics(t, metricsResp)
	if got := metricNumber(t, metrics, "http_requests_total"); got != 1 {
		t.Fatalf("expected one observed request before /metrics, got %v metrics %#v", got, metrics)
	}
	if got := metricNumber(t, metrics, "agent_runs_total"); got != 1 {
		t.Fatalf("expected one observed agent run, got %v metrics %#v", got, metrics)
	}
	if got := metricNumber(t, metrics, "agent_run_failures_total"); got != 0 {
		t.Fatalf("expected no failed agent runs, got %v metrics %#v", got, metrics)
	}
	if metricString(t, metrics, "readiness_status") == "" || metricString(t, metrics, "migration_status") == "" {
		t.Fatalf("expected readiness and migration status metrics, got %#v", metrics)
	}
	agentRuns := metricObject(t, metrics, "agent_runs")
	if got := metricNumber(t, agentRuns, "total"); got != 1 {
		t.Fatalf("expected nested agent run total, got %v object %#v", got, agentRuns)
	}
	if metricObject(t, metrics, "http") == nil || metricObject(t, metrics, "readiness") == nil || metricObject(t, metrics, "migration") == nil {
		t.Fatalf("expected nested metrics groups, got %#v", metrics)
	}
}

func TestMetricsRequiresAuthWhenConfigured(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:     "clean-core",
		Version:         "test",
		Env:             "test",
		HTTPAddr:        ":0",
		LogLevel:        "error",
		Readiness:       true,
		ServiceToken:    "secret",
		MetricsAuthMode: "required",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	unauthReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	unauthResp := httptest.NewRecorder()
	handler.ServeHTTP(unauthResp, unauthReq)
	if unauthResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated metrics to be rejected, got %d body %s", unauthResp.Code, unauthResp.Body.String())
	}

	wrongToken := doJSONWithHeaders(handler, http.MethodGet, "/metrics", nil, map[string]string{"Authorization": "Bearer wrong"})
	if wrongToken.Code != http.StatusUnauthorized {
		t.Fatalf("expected wrong metrics token to be rejected, got %d body %s", wrongToken.Code, wrongToken.Body.String())
	}

	metricsResp := doJSONWithHeaders(handler, http.MethodGet, "/metrics", nil, map[string]string{"Authorization": "Bearer secret"})
	metrics := decodeMetrics(t, metricsResp)
	if metricObject(t, metrics, "http") == nil {
		t.Fatalf("expected authenticated metrics response, got %#v", metrics)
	}
}

func TestMetricsAuthIsRequiredByDefaultInProduction(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:  "clean-core",
		Version:      "test",
		Env:          "production",
		HTTPAddr:     ":0",
		LogLevel:     "error",
		Readiness:    true,
		ServiceToken: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	unauthReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	unauthResp := httptest.NewRecorder()
	handler.ServeHTTP(unauthResp, unauthReq)
	if unauthResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected production metrics to require auth, got %d body %s", unauthResp.Code, unauthResp.Body.String())
	}
}

func TestMetricsExposeDatabasePoolStats(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName: "clean-core",
		Version:     "test",
		Env:         "test",
		HTTPAddr:    ":0",
		LogLevel:    "error",
		Readiness:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	db := openMetricsTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(7)
	readinessDB := openMetricsTestDB(t)
	defer readinessDB.Close()
	readinessDB.SetMaxOpenConns(3)
	appCore.DB = db
	appCore.ReadinessDB = readinessDB

	handler := NewHandlerWithCore(appCore, logging.New("error"))
	metricsResp := doJSON(handler, "GET", "/metrics", nil)
	metrics := decodeMetrics(t, metricsResp)
	if got := metricNumber(t, metrics, "db_max_open_connections"); got != 7 {
		t.Fatalf("expected DB max open metric, got %v metrics %#v", got, metrics)
	}
	databaseMetrics := metricObject(t, metrics, "database")
	if got := metricNumber(t, databaseMetrics, "max_open_connections"); got != 7 {
		t.Fatalf("expected nested DB max open metric, got %v metrics %#v", got, databaseMetrics)
	}
	if got := metricNumber(t, metrics, "db_readiness_max_open_connections"); got != 3 {
		t.Fatalf("expected readiness DB max open metric, got %v metrics %#v", got, metrics)
	}
	readinessDatabaseMetrics := metricObject(t, metrics, "readiness_database")
	if got := metricNumber(t, readinessDatabaseMetrics, "max_open_connections"); got != 3 {
		t.Fatalf("expected nested readiness DB max open metric, got %v metrics %#v", got, readinessDatabaseMetrics)
	}
}

func TestAgentRunAsyncModeReturnsPreparedRunAndCompletesInBackground(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:            "clean-core",
		Version:                "test",
		Env:                    "test",
		HTTPAddr:               ":0",
		LogLevel:               "error",
		Readiness:              true,
		AgentRunExecutionMode:  "async",
		RunMaxConcurrent:       1,
		TenantRunMaxConcurrent: 1,
		AgentRunMaxConcurrent:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, http.MethodPost, "/v1/commands", map[string]any{
		"trace_id": "trace_async_1",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"input": "hello async"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("async agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		RunID  contracts.AgentRunID `json:"run_id"`
		TaskID contracts.TaskID     `json:"task_id"`
		Status contracts.RunStatus  `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.RunID == "" || result.TaskID == "" || result.Status != contracts.RunCreated {
		t.Fatalf("expected prepared run response, got %#v", result)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		run, err := appCore.Runs.Get(context.Background(), result.RunID)
		if err == nil && run.Status == contracts.RunCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected async run to complete, last err=%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentRunAsyncModeDoesNotBlockHTTPWhileBackgroundRunIsExecuting(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:            "clean-core",
		Version:                "test",
		Env:                    "test",
		HTTPAddr:               ":0",
		LogLevel:               "error",
		Readiness:              true,
		AgentRunExecutionMode:  "async",
		RunMaxConcurrent:       1,
		TenantRunMaxConcurrent: 1,
		AgentRunMaxConcurrent:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	model := newBlockingStreamModel()
	appCore.Coordinator.Model = model
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	respCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		respCh <- doJSON(handler, http.MethodPost, "/v1/commands", map[string]any{
			"trace_id": "trace_async_blocking_1",
			"command":  "agent.run",
			"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
			"payload":  map[string]any{"input": "hello blocked async"},
			"context":  map[string]any{"tenant_id": "tenant_1"},
		})
	}()
	var resp *httptest.ResponseRecorder
	select {
	case resp = <-respCh:
	case <-time.After(500 * time.Millisecond):
		model.releaseRun()
		t.Fatal("async agent.run HTTP response blocked behind background execution")
	}
	if resp.Code != http.StatusOK {
		model.releaseRun()
		t.Fatalf("async agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		RunID  contracts.AgentRunID `json:"run_id"`
		Status contracts.RunStatus  `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		model.releaseRun()
		t.Fatal(err)
	}
	if result.RunID == "" || result.Status != contracts.RunCreated {
		model.releaseRun()
		t.Fatalf("expected prepared async run, got %#v", result)
	}
	select {
	case <-model.started:
	case <-time.After(time.Second):
		model.releaseRun()
		t.Fatal("expected background model stream to start")
	}
	health := doJSON(handler, http.MethodGet, "/healthz", nil)
	if health.Code != http.StatusOK {
		model.releaseRun()
		t.Fatalf("healthz blocked or failed while async run was executing: %d %s", health.Code, health.Body.String())
	}
	metricsResp := doJSON(handler, http.MethodGet, "/metrics", nil)
	if metricsResp.Code != http.StatusOK {
		model.releaseRun()
		t.Fatalf("metrics blocked or failed while async run was executing: %d %s", metricsResp.Code, metricsResp.Body.String())
	}
	metrics := decodeMetrics(t, metricsResp)
	if got := metricNumber(t, metrics, "agent_runs_running"); got != 1 {
		model.releaseRun()
		t.Fatalf("expected one admitted async run while model blocks, got %v metrics %#v", got, metrics)
	}
	model.releaseRun()
	deadline := time.Now().Add(2 * time.Second)
	for {
		run, err := appCore.Runs.Get(context.Background(), result.RunID)
		if err == nil && run.Status == contracts.RunCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected async run to complete after releasing model, last err=%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUsageEvidenceIsTraceScopedAndTenantIsolated(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, event := range []contracts.TraceEvent{
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceModelCalled, CreatedAt: now},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceModelCompleted, Payload: map[string]any{"model_provider": "stub", "model_name": "usage-model", "prompt_tokens": 11, "completion_tokens": 7}, CreatedAt: now.Add(time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceToolInvoked, Payload: map[string]any{"tool_id": "crm.lookup"}, CreatedAt: now.Add(2 * time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceToolPendingApproval, Payload: map[string]any{"tool_id": "crm.lookup"}, CreatedAt: now.Add(3 * time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceApprovalRequested, Payload: map[string]any{"tool_id": "crm.lookup"}, CreatedAt: now.Add(4 * time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceApprovalResolved, Payload: map[string]any{"tool_id": "crm.lookup"}, CreatedAt: now.Add(5 * time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceToolProviderInvoked, Payload: map[string]any{"tool_id": "crm.lookup", "provider_id": "crm"}, CreatedAt: now.Add(6 * time.Millisecond)},
		{TraceID: "trace_usage_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceToolCompleted, Payload: map[string]any{"tool_id": "crm.lookup", "artifact_refs": []any{map[string]any{"artifact_id": "artifact_1"}}}, CreatedAt: now.Add(7 * time.Millisecond)},
		{TraceID: "trace_usage_2", TenantID: "tenant_2", RunID: "run_other", TaskID: "task_other", Type: contracts.TraceModelCalled, CreatedAt: now},
	} {
		if err := appCore.Trace.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "GET", "/v1/usage/evidence?trace_id=trace_usage_1", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("usage evidence failed %d body %s", resp.Code, resp.Body.String())
	}
	var body struct {
		UsageEvidence usageEvidenceResponse `json:"usage_evidence"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	evidence := body.UsageEvidence
	if evidence.Source != "trace" || evidence.Ledger != "external_metering" || evidence.ModelCalls != 1 || evidence.PromptTokens != 11 || evidence.CompletionTokens != 7 {
		t.Fatalf("unexpected model usage evidence: %#v", evidence)
	}
	if evidence.ToolInvocations != 1 || evidence.ToolCompletions != 1 || evidence.ToolApprovalWaits != 1 || evidence.ToolProviderCalls != 1 || evidence.ApprovalRequests != 1 || evidence.ApprovalResolutions != 1 {
		t.Fatalf("unexpected tool usage evidence: %#v", evidence)
	}
	if len(evidence.RunIDs) != 1 || evidence.RunIDs[0] != "run_1" || len(evidence.TaskIDs) != 1 || evidence.TaskIDs[0] != "task_1" {
		t.Fatalf("unexpected run/task usage evidence: %#v", evidence)
	}
	if len(evidence.ToolIDs) != 1 || evidence.ToolIDs[0] != "crm.lookup" || len(evidence.ArtifactRefs) != 1 || evidence.ArtifactRefs[0] != "artifact_1" {
		t.Fatalf("unexpected tool/artifact evidence: %#v", evidence)
	}
	crossTenant := doJSONWithHeaders(handler, "GET", "/v1/usage/evidence?trace_id=trace_usage_1", nil, map[string]string{"X-Tenant-ID": "tenant_2"})
	if crossTenant.Code != http.StatusForbidden {
		t.Fatalf("expected cross-tenant usage evidence to be forbidden, got %d body %s", crossTenant.Code, crossTenant.Body.String())
	}
}

func TestGovernanceProcessResourceAPIs(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	templateResp := doJSON(handler, "POST", "/v1/governance/templates", map[string]any{
		"template_id": "tpl_review",
		"name":        "Generic review",
		"gates": []any{
			map[string]any{
				"gate_id":            "quality",
				"review_mode":        "multi",
				"consensus_policy":   "majority",
				"escalation_policy":  "orchestrator",
				"required_reviewers": 2,
			},
		},
	})
	if templateResp.Code != http.StatusCreated {
		t.Fatalf("template create failed %d body %s", templateResp.Code, templateResp.Body.String())
	}
	runResp := doJSON(handler, "POST", "/v1/governance/runs", map[string]any{
		"template_id":  "tpl_review",
		"subject_type": "artifact",
		"subject_id":   "artifact_1",
		"trace_id":     "trace_gov_api",
	})
	if runResp.Code != http.StatusCreated {
		t.Fatalf("run create failed %d body %s", runResp.Code, runResp.Body.String())
	}
	var runBody struct {
		Snapshot contracts.GovernanceProcessSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(runResp.Body.Bytes(), &runBody); err != nil {
		t.Fatal(err)
	}
	if len(runBody.Snapshot.Gates) != 1 || runBody.Snapshot.ProcessRun.Status != contracts.GovernanceRunActive {
		t.Fatalf("unexpected run snapshot: %#v", runBody.Snapshot)
	}
	runID := runBody.Snapshot.ProcessRun.RunID
	gateRunID := runBody.Snapshot.Gates[0].GateRunID

	openResp := doJSON(handler, "POST", "/v1/governance/runs/"+string(runID)+"/gates/open", map[string]any{
		"gate_run_id": string(gateRunID),
		"evidence_refs": []any{
			map[string]any{"type": "trace", "trace_id": "trace_gov_api", "summary": "candidate output"},
		},
	})
	if openResp.Code != http.StatusOK {
		t.Fatalf("gate open failed %d body %s", openResp.Code, openResp.Body.String())
	}
	for _, reviewer := range []string{"reviewer_a", "reviewer_b"} {
		reviewResp := doJSON(handler, "POST", "/v1/governance/gates/"+string(gateRunID)+"/reviews", map[string]any{
			"reviewer_id": reviewer,
			"decision":    "approve",
			"reason":      "accepted",
		})
		if reviewResp.Code != http.StatusOK {
			t.Fatalf("review failed %d body %s", reviewResp.Code, reviewResp.Body.String())
		}
	}
	snapshotResp := doJSON(handler, "GET", "/v1/governance/runs/"+string(runID), nil)
	if snapshotResp.Code != http.StatusOK {
		t.Fatalf("snapshot failed %d body %s", snapshotResp.Code, snapshotResp.Body.String())
	}
	var snapshotBody struct {
		Snapshot contracts.GovernanceProcessSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(snapshotResp.Body.Bytes(), &snapshotBody); err != nil {
		t.Fatal(err)
	}
	if snapshotBody.Snapshot.Gates[0].Status != contracts.GovernanceGatePassed || snapshotBody.Snapshot.ProcessRun.Status != contracts.GovernanceRunCompleted {
		t.Fatalf("expected completed governed run, got %#v", snapshotBody.Snapshot)
	}
	traceResp := doJSON(handler, "GET", "/v1/traces/trace_gov_api", nil)
	if traceResp.Code != http.StatusOK || !bytes.Contains(traceResp.Body.Bytes(), []byte("governance.review.submitted")) {
		t.Fatalf("expected governance trace evidence %d body %s", traceResp.Code, traceResp.Body.String())
	}
}

func TestToolsInvokeRequiresExposedToolAndIsIdempotent(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_tool_1",
		"target": map[string]any{
			"agent_id": "test-agent",
			"version":  "v1",
		},
		"command": "tools.invoke",
		"payload": map[string]any{
			"tool_id":   "echo",
			"arguments": map[string]any{"message": "hello"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	first := doJSONWithHeaders(handler, "POST", "/v1/commands", body, map[string]string{"Idempotency-Key": "same-key"})
	second := doJSONWithHeaders(handler, "POST", "/v1/commands", body, map[string]string{"Idempotency-Key": "same-key"})
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("unexpected statuses %d/%d bodies %s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var firstResult contracts.ToolResult
	var secondResult contracts.ToolResult
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if firstResult.ToolResultID != secondResult.ToolResultID {
		t.Fatalf("expected idempotent result, got %s and %s", firstResult.ToolResultID, secondResult.ToolResultID)
	}
	trace := doJSON(handler, "GET", "/v1/tools/"+string(firstResult.ToolCallID)+"/trace", nil)
	if trace.Code != http.StatusOK {
		t.Fatalf("tool trace failed %d body %s", trace.Code, trace.Body.String())
	}
	changedBody := map[string]any{
		"trace_id": "trace_tool_1",
		"target": map[string]any{
			"agent_id": "test-agent",
			"version":  "v1",
		},
		"command": "tools.invoke",
		"payload": map[string]any{
			"tool_id":   "echo",
			"arguments": map[string]any{"message": "different"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	changed := doJSON(handler, "POST", "/v1/commands", changedBody)
	if changed.Code != http.StatusOK {
		t.Fatalf("changed tool invoke failed %d body %s", changed.Code, changed.Body.String())
	}
	var changedResult contracts.ToolResult
	if err := json.Unmarshal(changed.Body.Bytes(), &changedResult); err != nil {
		t.Fatal(err)
	}
	if changedResult.ToolResultID == firstResult.ToolResultID {
		t.Fatalf("expected request hash to separate changed arguments, got reused result %s", changedResult.ToolResultID)
	}
	external := doJSON(handler, "GET", "/v1/external-tasks/array/ext_1", nil)
	if external.Code != http.StatusOK {
		t.Fatalf("external task lookup failed %d body %s", external.Code, external.Body.String())
	}
	if _, err := appCore.ArrayBridge.BindTask(context.Background(), contracts.ExternalTaskBinding{
		Provider:       "array",
		ExternalTaskID: "ext_bound",
		CoreTaskID:     "task_1",
		TenantID:       "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	crossTenantExternal := doJSONWithHeaders(handler, "GET", "/v1/external-tasks/array/ext_bound", nil, map[string]string{"X-Tenant-ID": "tenant_2"})
	if crossTenantExternal.Code != http.StatusForbidden {
		t.Fatalf("expected external task tenant guard, got %d body %s", crossTenantExternal.Code, crossTenantExternal.Body.String())
	}
}

func TestTaskStartCommandCreatesTask(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"command": "task.start",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"title": "external task", "objective": "do work"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("task.start failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Task contracts.Task `json:"task"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Task.Status != contracts.TaskCreated || result.Task.Objective != "do work" {
		t.Fatalf("unexpected task.start result: %#v", result)
	}
}

func TestIntakePolicyAPIsCRUDAndEvaluate(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSON(handler, http.MethodPost, "/v1/intake/policies", map[string]any{
		"name":            "Refund acknowledgement",
		"status":          "enabled",
		"priority":        10,
		"channel":         "web",
		"match_type":      "contains",
		"pattern":         "refund",
		"reply_text":      "Received. We are checking your refund request.",
		"reply_kind":      "acknowledgement",
		"continue_to_run": true,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create intake policy failed %d body %s", create.Code, create.Body.String())
	}
	var created struct {
		Policy struct {
			PolicyID string `json:"policy_id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Policy.PolicyID == "" {
		t.Fatalf("expected generated policy id, got %s", create.Body.String())
	}
	list := doJSON(handler, http.MethodGet, "/v1/intake/policies", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(created.Policy.PolicyID)) {
		t.Fatalf("list intake policies failed %d body %s", list.Code, list.Body.String())
	}
	get := doJSON(handler, http.MethodGet, "/v1/intake/policies/"+created.Policy.PolicyID, nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("Refund acknowledgement")) {
		t.Fatalf("get intake policy failed %d body %s", get.Code, get.Body.String())
	}
	update := doJSON(handler, http.MethodPut, "/v1/intake/policies/"+created.Policy.PolicyID, map[string]any{
		"name":            "Refund acknowledgement updated",
		"status":          "enabled",
		"priority":        20,
		"channel":         "web",
		"match_type":      "contains",
		"pattern":         "refund",
		"reply_text":      "Updated refund acknowledgement.",
		"reply_kind":      "status_update",
		"continue_to_run": false,
	})
	if update.Code != http.StatusOK || !bytes.Contains(update.Body.Bytes(), []byte("Updated refund acknowledgement.")) {
		t.Fatalf("update intake policy failed %d body %s", update.Code, update.Body.String())
	}
	evaluate := doJSON(handler, http.MethodPost, "/v1/intake/evaluate", map[string]any{
		"trace_id": "trace_intake_api_1",
		"channel":  "web",
		"input":    "I need a refund for order 123",
	})
	if evaluate.Code != http.StatusOK {
		t.Fatalf("evaluate intake failed %d body %s", evaluate.Code, evaluate.Body.String())
	}
	if !bytes.Contains(evaluate.Body.Bytes(), []byte(`"matched":true`)) ||
		!bytes.Contains(evaluate.Body.Bytes(), []byte(`"dispatch":"external_channel"`)) ||
		!bytes.Contains(evaluate.Body.Bytes(), []byte(`"continue_to_run":false`)) {
		t.Fatalf("unexpected intake evaluate response %s", evaluate.Body.String())
	}
	events, err := appCore.Trace.ListByTrace(context.Background(), "trace_intake_api_1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTraceEvent(events, contracts.TraceIntakePreReplyEvaluated) {
		t.Fatalf("expected intake trace event, got %#v", events)
	}
	deleteResp := doJSON(handler, http.MethodDelete, "/v1/intake/policies/"+created.Policy.PolicyID, nil)
	if deleteResp.Code != http.StatusOK || !bytes.Contains(deleteResp.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("delete intake policy failed %d body %s", deleteResp.Code, deleteResp.Body.String())
	}
	missing := doJSON(handler, http.MethodGet, "/v1/intake/policies/"+created.Policy.PolicyID, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected missing intake policy after delete, got %d body %s", missing.Code, missing.Body.String())
	}
}

func TestExternalWritebackFailureIsObservable(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "remote down", http.StatusBadGateway)
	}))
	defer remote.Close()
	appCore, err := core.New(config.Config{
		ServiceName:           "clean-core",
		Version:               "test",
		Env:                   "test",
		HTTPAddr:              ":0",
		LogLevel:              "error",
		Readiness:             true,
		ExternalBridgeBaseURL: remote.URL,
		ExternalBridgeToken:   "",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	start := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"command": "task.start",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"title": "external task", "objective": "do work"},
		"context": map[string]any{
			"tenant_id": "tenant_1",
			"collaboration": map[string]any{
				"provider":         "array",
				"external_task_id": "ext_writeback_1",
				"caller_id":        "user_1",
				"caller_type":      "user",
			},
		},
	})
	if start.Code != http.StatusOK {
		t.Fatalf("task.start failed %d body %s", start.Code, start.Body.String())
	}
	var started struct {
		Task contracts.Task `json:"task"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	run := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_writeback_1",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"input": "hello"},
		"context":  map[string]any{"tenant_id": "tenant_1", "task_id": started.Task.TaskID},
	})
	if run.Code != http.StatusOK {
		t.Fatalf("agent.run failed %d body %s", run.Code, run.Body.String())
	}
	events, err := appCore.Trace.ListByTrace(context.Background(), "trace_writeback_1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTraceEvent(events, contracts.TraceExternalWritebackFailed) {
		t.Fatalf("expected writeback failure trace, got %#v", events)
	}
	auditResp := doJSON(handler, "GET", "/v1/audit?action=external.writeback_failed", nil)
	if auditResp.Code != http.StatusOK || !bytes.Contains(auditResp.Body.Bytes(), []byte("ext_writeback_1")) {
		t.Fatalf("expected writeback failure audit, got %d body %s", auditResp.Code, auditResp.Body.String())
	}
	metricsResp := doJSON(handler, "GET", "/v1/metrics/governance?trace_id=trace_writeback_1", nil)
	if metricsResp.Code != http.StatusOK || !bytes.Contains(metricsResp.Body.Bytes(), []byte(`"external_writeback_failures_total":1`)) {
		t.Fatalf("expected writeback failure metric, got %d body %s", metricsResp.Code, metricsResp.Body.String())
	}
	binding, ok, err := appCore.ArrayBridge.GetBinding(context.Background(), "array", "ext_writeback_1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || binding.Status != "writeback_failed" || binding.LastError == "" {
		t.Fatalf("expected failed external binding, got %#v", binding)
	}
}

func TestCommandRequiresTenant(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/commands", bytes.NewReader([]byte(`{}`)))
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", resp.Code)
	}
}

func TestCommandRoleDenied(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"command": "agent.run",
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSONWithHeaders(handler, "POST", "/v1/commands", body, map[string]string{"X-Roles": "optimizer"})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for denied role, got %d body %s", resp.Code, resp.Body.String())
	}
}

func TestPackageReleaseCommandsAndEvalRun(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	publishBody := map[string]any{
		"command": "agent.package.publish",
		"payload": map[string]any{
			"agent_id": "test-agent",
			"version":  "v2",
			"prompt":   "new prompt",
			"tool_bindings": map[string]any{
				"allowed_tool_ids": []any{"echo"},
				"exposed_tool_ids": []any{"echo"},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", publishBody, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var release contracts.AgentPackageVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	runPublishedBody := map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v2"},
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	runPublished := doJSON(handler, "POST", "/v1/commands", runPublishedBody)
	if runPublished.Code != http.StatusBadRequest {
		t.Fatalf("expected published version to be blocked before canary, got %d body %s", runPublished.Code, runPublished.Body.String())
	}
	canaryBody := map[string]any{
		"command": "agent.package.canary",
		"payload": map[string]any{"package_version_id": release.PackageVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	canary := doJSONWithHeaders(handler, "POST", "/v1/commands", canaryBody, map[string]string{"X-Roles": "optimizer"})
	if canary.Code != http.StatusOK {
		t.Fatalf("canary failed %d body %s", canary.Code, canary.Body.String())
	}
	runCanary := doJSON(handler, "POST", "/v1/commands", runPublishedBody)
	if runCanary.Code != http.StatusOK {
		t.Fatalf("expected canary version to run, got %d body %s", runCanary.Code, runCanary.Body.String())
	}
	stableBody := map[string]any{
		"command": "agent.package.stable",
		"payload": map[string]any{"package_version_id": release.PackageVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	stableBeforeEval := doJSONWithHeaders(handler, "POST", "/v1/commands", stableBody, map[string]string{"X-Roles": "optimizer"})
	if stableBeforeEval.Code != http.StatusBadRequest {
		t.Fatalf("expected stable before eval to fail, got %d body %s", stableBeforeEval.Code, stableBeforeEval.Body.String())
	}
	evalBody := map[string]any{
		"command": "eval.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v2"},
		"payload": map[string]any{
			"package_version_id":   release.PackageVersionID,
			"input":                "hello",
			"final_reply_contains": []any{"ok"},
			"should_end_status":    "completed",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	evalResp := doJSONWithHeaders(handler, "POST", "/v1/commands", evalBody, map[string]string{"X-Roles": "optimizer"})
	if evalResp.Code != http.StatusOK {
		t.Fatalf("eval failed %d body %s", evalResp.Code, evalResp.Body.String())
	}
	var evalResult struct {
		EvalRunID contracts.EvalRunID `json:"eval_run_id"`
		TraceID   contracts.TraceID   `json:"trace_id"`
	}
	if err := json.Unmarshal(evalResp.Body.Bytes(), &evalResult); err != nil {
		t.Fatal(err)
	}
	evalLookupResp := doJSON(handler, "GET", "/v1/evals/results/"+string(evalResult.EvalRunID), nil)
	if evalLookupResp.Code != http.StatusOK || !bytes.Contains(evalLookupResp.Body.Bytes(), []byte(evalResult.EvalRunID)) {
		t.Fatalf("expected eval.run result to be queryable, got %d body %s", evalLookupResp.Code, evalLookupResp.Body.String())
	}
	events, err := appCore.Trace.ListByTrace(context.Background(), evalResult.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTraceEvent(events, contracts.TraceEvalSummaryCreated) {
		t.Fatalf("expected single eval summary trace, got %#v", events)
	}
	mismatchEval := map[string]any{
		"command": "eval.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"package_version_id": release.PackageVersionID,
			"input":              "hello",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	if resp := doJSONWithHeaders(handler, "POST", "/v1/commands", mismatchEval, map[string]string{"X-Roles": "optimizer"}); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected eval target mismatch to fail, got %d body %s", resp.Code, resp.Body.String())
	}
	stable := doJSONWithHeaders(handler, "POST", "/v1/commands", stableBody, map[string]string{"X-Roles": "optimizer"})
	if stable.Code != http.StatusOK {
		t.Fatalf("stable failed %d body %s", stable.Code, stable.Body.String())
	}
	assertDefaultRunVersion(t, handler, appCore, "v2")
	rollbackWithoutReason := map[string]any{
		"command": "agent.package.rollback",
		"payload": map[string]any{"package_version_id": release.PackageVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	if resp := doJSONWithHeaders(handler, "POST", "/v1/commands", rollbackWithoutReason, map[string]string{"X-Roles": "optimizer"}); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected rollback without reason to fail, got %d body %s", resp.Code, resp.Body.String())
	}
	rollbackBody := map[string]any{
		"command": "agent.package.rollback",
		"payload": map[string]any{"package_version_id": release.PackageVersionID, "reason": "smoke"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	rollback := doJSONWithHeaders(handler, "POST", "/v1/commands", rollbackBody, map[string]string{"X-Roles": "optimizer"})
	if rollback.Code != http.StatusOK {
		t.Fatalf("rollback failed %d body %s", rollback.Code, rollback.Body.String())
	}
	assertDefaultRunVersion(t, handler, appCore, "v1")
}

func TestEvalSuiteAddCaseAcceptsConversationContext(t *testing.T) {
	appCore, err := core.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	suiteBody := map[string]any{
		"command": "eval.suite.create",
		"payload": map[string]any{"name": "conversation-suite"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	suiteResp := doJSONWithHeaders(handler, "POST", "/v1/commands", suiteBody, map[string]string{"X-Roles": "optimizer"})
	if suiteResp.Code != http.StatusOK {
		t.Fatalf("suite create failed %d body %s", suiteResp.Code, suiteResp.Body.String())
	}
	var suite eval.Suite
	if err := json.Unmarshal(suiteResp.Body.Bytes(), &suite); err != nil {
		t.Fatal(err)
	}
	addBody := map[string]any{
		"command": "eval.suite.add_case",
		"payload": map[string]any{
			"suite_id": suite.SuiteID,
			"name":     "group_reply",
			"input":    "那你帮我安排一下",
			"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
			"context": map[string]any{
				"tenant_id": "tenant_1",
				"user_id":   "user_1",
				"collaboration": map[string]any{
					"provider":             "eval",
					"conversation_kind":    "group",
					"external_group_id":    "group_1",
					"external_message_id":  "msg_2",
					"caller_id":            "user_1",
					"caller_type":          "user",
					"reply_to_message_id":  "msg_1",
					"current_speaker_name": "张三",
					"recent_messages": []any{
						map[string]any{
							"message_id":   "msg_1",
							"speaker_id":   "test-agent",
							"speaker_type": "agent",
							"text":         "我可以继续。",
						},
					},
				},
			},
			"should_end_status": "waiting_input",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	addResp := doJSONWithHeaders(handler, "POST", "/v1/commands", addBody, map[string]string{"X-Roles": "optimizer"})
	if addResp.Code != http.StatusOK {
		t.Fatalf("add case failed %d body %s", addResp.Code, addResp.Body.String())
	}
	var updated eval.Suite
	if err := json.Unmarshal(addResp.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Cases) != 1 || updated.Cases[0].Context.Collaboration == nil {
		t.Fatalf("expected case conversation context, got %#v", updated.Cases)
	}
	collab := updated.Cases[0].Context.Collaboration
	if collab.ConversationKind != "group" || collab.ReplyToMessageID != "msg_1" || len(collab.RecentMessages) != 1 {
		t.Fatalf("unexpected collaboration context: %#v", collab)
	}
}

func TestPackageDraftCommandsExposeStagedFlow(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.create",
		"payload": map[string]any{
			"agent_id": "test-agent",
			"version":  "v3",
			"prompt":   "draft prompt",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("draft create failed %d body %s", create.Code, create.Body.String())
	}
	var draft agentpackage.Draft
	if err := json.Unmarshal(create.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	patch := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.patch_prompt",
		"payload": map[string]any{"draft_id": draft.DraftID, "prompt": "patched prompt"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if patch.Code != http.StatusOK {
		t.Fatalf("draft patch failed %d body %s", patch.Code, patch.Body.String())
	}
	patchDeveloper := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.patch_developer_prompt",
		"payload": map[string]any{"draft_id": draft.DraftID, "developer_prompt": "developer strategy"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if patchDeveloper.Code != http.StatusOK {
		t.Fatalf("developer prompt patch failed %d body %s", patchDeveloper.Code, patchDeveloper.Body.String())
	}
	patchSystem := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.patch_system_prompt",
		"payload": map[string]any{"draft_id": draft.DraftID, "system_prompt": "system contract"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if patchSystem.Code != http.StatusOK {
		t.Fatalf("system prompt patch failed %d body %s", patchSystem.Code, patchSystem.Body.String())
	}
	validate := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.validate",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if validate.Code != http.StatusOK {
		t.Fatalf("draft validate failed %d body %s", validate.Code, validate.Body.String())
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.publish",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("draft publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var release contracts.AgentPackageVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if release.Version != "v3" || release.Status != contracts.ReleasePublished {
		t.Fatalf("unexpected release: %#v", release)
	}
}

func TestPackageCollaboratorAndExportedToolCommands(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	providerDraftResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.create",
		"payload": map[string]any{
			"agent_id": "crm-agent",
			"version":  "v1",
			"prompt":   "crm provider prompt",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if providerDraftResp.Code != http.StatusOK {
		t.Fatalf("provider draft create failed %d body %s", providerDraftResp.Code, providerDraftResp.Body.String())
	}
	var providerDraft agentpackage.Draft
	if err := json.Unmarshal(providerDraftResp.Body.Bytes(), &providerDraft); err != nil {
		t.Fatal(err)
	}
	exportResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.exported_tool.add",
		"payload": map[string]any{
			"draft_id":      providerDraft.DraftID,
			"tool_id":       "crm-agent.customer.summary",
			"name":          "Customer summary",
			"description":   "Summarize customer records.",
			"input_schema":  map[string]any{"type": "object"},
			"output_schema": map[string]any{"type": "object"},
			"when_to_use":   []any{"customer history"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if exportResp.Code != http.StatusOK {
		t.Fatalf("exported tool add failed %d body %s", exportResp.Code, exportResp.Body.String())
	}
	validate := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.validate",
		"payload": map[string]any{"draft_id": providerDraft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if validate.Code != http.StatusOK {
		t.Fatalf("provider draft validate failed %d body %s", validate.Code, validate.Body.String())
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.publish",
		"payload": map[string]any{"draft_id": providerDraft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("provider publish failed %d body %s", publish.Code, publish.Body.String())
	}
	if _, ok := appCore.Tools.GetForTenant("tenant_1", "crm-agent.customer.summary"); !ok {
		t.Fatal("expected exported tool synced into runtime registry")
	}

	callerDraftResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.create",
		"payload": map[string]any{
			"agent_id": "caller-agent",
			"version":  "v1",
			"prompt":   "caller prompt",
			"tool_bindings": map[string]any{
				"allowed_tool_ids": []any{"crm-agent.customer.summary"},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if callerDraftResp.Code != http.StatusOK {
		t.Fatalf("caller draft create failed %d body %s", callerDraftResp.Code, callerDraftResp.Body.String())
	}
	var callerDraft agentpackage.Draft
	if err := json.Unmarshal(callerDraftResp.Body.Bytes(), &callerDraft); err != nil {
		t.Fatal(err)
	}
	collaboratorResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.collaborator.add",
		"payload": map[string]any{
			"draft_id":     callerDraft.DraftID,
			"agent_id":     "crm-agent",
			"name":         "CRM Agent",
			"description":  "Handles customer history.",
			"when_to_use":  []any{"customer history"},
			"capabilities": []any{"crm"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if collaboratorResp.Code != http.StatusOK {
		t.Fatalf("collaborator add failed %d body %s", collaboratorResp.Code, collaboratorResp.Body.String())
	}
	preview := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "prompt.preview",
		"payload": map[string]any{"draft_id": callerDraft.DraftID, "input": "customer history"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if preview.Code != http.StatusOK || !bytes.Contains(preview.Body.Bytes(), []byte("retrieved collaborator card")) || !bytes.Contains(preview.Body.Bytes(), []byte("crm-agent.customer.summary")) {
		t.Fatalf("expected collaborator and exported tool in preview, got %d body %s", preview.Code, preview.Body.String())
	}
}

func TestPackageProposalCommandsExposeReviewFlow(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.create",
		"payload": map[string]any{
			"agent_id": "test-agent",
			"version":  "v30",
			"prompt":   "proposal prompt",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("draft create failed %d body %s", create.Code, create.Body.String())
	}
	var draft agentpackage.Draft
	if err := json.Unmarshal(create.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	validate := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.validate",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if validate.Code != http.StatusOK {
		t.Fatalf("draft validate failed %d body %s", validate.Code, validate.Body.String())
	}
	createProposal := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.proposal.create",
		"payload": map[string]any{"draft_id": draft.DraftID, "proposal_type": "PromptPatchProposal", "title": "Improve prompt", "patch": map[string]any{"prompt": "proposal prompt v2"}},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if createProposal.Code != http.StatusOK {
		t.Fatalf("proposal create failed %d body %s", createProposal.Code, createProposal.Body.String())
	}
	var proposal agentpackage.Proposal
	if err := json.Unmarshal(createProposal.Body.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	publishEarly := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.proposal.publish",
		"payload": map[string]any{"proposal_id": proposal.ProposalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publishEarly.Code == http.StatusOK {
		t.Fatalf("expected unapproved proposal publish to fail, body %s", publishEarly.Body.String())
	}
	for _, command := range []string{"agent.package.proposal.submit", "agent.package.proposal.approve"} {
		resp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
			"command": command,
			"payload": map[string]any{"proposal_id": proposal.ProposalID},
			"context": map[string]any{"tenant_id": "tenant_1"},
		}, map[string]string{"X-Roles": "optimizer"})
		if resp.Code != http.StatusOK {
			t.Fatalf("%s failed %d body %s", command, resp.Code, resp.Body.String())
		}
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.proposal.publish",
		"payload": map[string]any{"proposal_id": proposal.ProposalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("proposal publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var release contracts.AgentPackageVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if release.Version != "v30" || release.Status != contracts.ReleasePublished {
		t.Fatalf("unexpected proposal release: %#v", release)
	}
}

func TestPromptPreviewCommandBuildsBundleWithoutModelCall(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "prompt.preview",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"input": "what can you do"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if resp.Code != http.StatusOK {
		t.Fatalf("prompt.preview failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		PromptBundle struct {
			Hash      string `json:"hash"`
			System    string `json:"system"`
			Developer string `json:"developer"`
			Task      string `json:"task"`
		} `json:"prompt_bundle"`
		TokenEstimate int `json:"token_estimate"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.PromptBundle.Hash == "" || result.TokenEstimate <= 0 {
		t.Fatalf("expected prompt hash and token estimate, got %#v", result)
	}
	if !bytes.Contains([]byte(result.PromptBundle.Developer), []byte("agent package instructions")) {
		t.Fatalf("expected preview to include package instructions, got %s", result.PromptBundle.Developer)
	}
}

func TestPromptPreviewCommandSupportsDraftID(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.draft.create",
		"payload": map[string]any{
			"agent_id": "test-agent",
			"version":  "v-preview",
			"prompt":   "draft identity prompt",
			"metadata": map[string]any{
				"system_prompt":    "draft system prompt",
				"developer_prompt": "draft developer prompt",
				"max_tool_calls":   0,
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("draft create failed %d body %s", create.Code, create.Body.String())
	}
	var draft agentpackage.Draft
	if err := json.Unmarshal(create.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	resp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "prompt.preview",
		"payload": map[string]any{"draft_id": draft.DraftID, "input": "preview draft"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if resp.Code != http.StatusOK {
		t.Fatalf("draft prompt.preview failed %d body %s", resp.Code, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("draft identity prompt")) || !bytes.Contains(resp.Body.Bytes(), []byte("draft developer prompt")) {
		t.Fatalf("expected preview to include draft prompts, got %s", resp.Body.String())
	}
}

func TestPackageCanaryRoutesDefaultTrafficAndRecordsHit(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	policy := appCore.LoadPolicySet(context.Background(), "tenant_1", "policy_default")
	policy.ReleasePolicy.MaxCanaryPercentWithoutApproval = 100
	if err := appCore.Policies.Put(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.publish",
		"payload": map[string]any{
			"agent_id": "test-agent",
			"version":  "v9",
			"prompt":   "canary prompt",
			"tool_bindings": map[string]any{
				"allowed_tool_ids": []any{"echo"},
				"exposed_tool_ids": []any{"echo"},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var release contracts.AgentPackageVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	canary := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "agent.package.canary",
		"payload": map[string]any{"package_version_id": release.PackageVersionID, "canary_percent": 100},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if canary.Code != http.StatusOK || !bytes.Contains(canary.Body.Bytes(), []byte(`"canary_percent":100`)) {
		t.Fatalf("canary failed %d body %s", canary.Code, canary.Body.String())
	}
	resp := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_canary_route_1",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "test-agent"},
		"payload":  map[string]any{"input": "hello"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("default agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		RunID contracts.AgentRunID `json:"run_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	run, err := appCore.Runs.Get(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentVersion != "v9" {
		t.Fatalf("expected canary route to v9, got %#v", run)
	}
	events, err := appCore.Trace.ListByTrace(context.Background(), "trace_canary_route_1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == contracts.TraceCanaryRouted {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected canary routed trace event, got %#v", events)
	}
}

func TestPolicyManagementCommandsRequireEvalBeforeStable(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.draft.create",
		"payload": map[string]any{
			"policy_set_id": "policy_test",
			"version":       "v1",
			"policy": map[string]any{
				"policy_set_id": "policy_test",
				"version":       "v1",
				"runtime_policy": map[string]any{
					"max_steps":      4,
					"max_tool_calls": 2,
				},
				"release_policy":  map[string]any{"require_rollback_reason": true},
				"memory_policy":   map[string]any{"allow_write": true, "allow_read": true},
				"artifact_policy": map[string]any{"allow_read": true},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("policy draft create failed %d body %s", create.Code, create.Body.String())
	}
	var draft contracts.PolicyDraft
	if err := json.Unmarshal(create.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	validate := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.draft.validate",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if validate.Code != http.StatusOK {
		t.Fatalf("policy validate failed %d body %s", validate.Code, validate.Body.String())
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.publish",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("policy publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var version contracts.PolicyVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	stableBeforeEval := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.stable",
		"payload": map[string]any{"policy_version_id": version.PolicyVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if stableBeforeEval.Code != http.StatusBadRequest {
		t.Fatalf("expected stable before eval to fail, got %d body %s", stableBeforeEval.Code, stableBeforeEval.Body.String())
	}
	evalResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "eval.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"policy_version_id":    version.PolicyVersionID,
			"input":                "hello",
			"final_reply_contains": []any{"ok"},
			"should_end_status":    "completed",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if evalResp.Code != http.StatusOK {
		t.Fatalf("policy eval failed %d body %s", evalResp.Code, evalResp.Body.String())
	}
	stable := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.stable",
		"payload": map[string]any{"policy_version_id": version.PolicyVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if stable.Code != http.StatusOK {
		t.Fatalf("policy stable failed %d body %s", stable.Code, stable.Body.String())
	}
	active := appCore.LoadPolicySet(context.Background(), "tenant_1", "policy_test")
	if active.Version != "v1" {
		t.Fatalf("expected active policy v1, got %#v", active)
	}
	auditResp := doJSON(handler, "GET", "/v1/audit?action=policy.stable", nil)
	if auditResp.Code != http.StatusOK || !bytes.Contains(auditResp.Body.Bytes(), []byte("policy.stable")) {
		t.Fatalf("expected policy stable audit, got %d body %s", auditResp.Code, auditResp.Body.String())
	}
}

func TestEvalSuiteRunRecordsSummaryTrace(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "eval.suite.create",
		"payload": map[string]any{"name": "smoke"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("eval suite create failed %d body %s", create.Code, create.Body.String())
	}
	var suite struct {
		SuiteID contracts.EvalSuiteID `json:"suite_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &suite); err != nil {
		t.Fatal(err)
	}
	addCase := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "eval.suite.add_case",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"suite_id":             suite.SuiteID,
			"name":                 "reply",
			"input":                "hello",
			"final_reply_contains": []any{"ok"},
			"should_end_status":    "completed",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if addCase.Code != http.StatusOK {
		t.Fatalf("eval suite add case failed %d body %s", addCase.Code, addCase.Body.String())
	}
	run := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_eval_suite_1",
		"command":  "eval.suite.run",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"suite_id": suite.SuiteID},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if run.Code != http.StatusOK {
		t.Fatalf("eval suite run failed %d body %s", run.Code, run.Body.String())
	}
	events, err := appCore.Trace.ListByTrace(context.Background(), "trace_eval_suite_1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == contracts.TraceEvalSummaryCreated {
			found = true
			if event.Payload["case_count"] != float64(1) && event.Payload["case_count"] != 1 {
				t.Fatalf("expected case_count in eval summary trace, got %#v", event.Payload)
			}
		}
	}
	if !found {
		t.Fatalf("expected eval summary trace, got %#v", events)
	}
}

func TestPolicyStableRequiresConcreteApprovalRequest(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	active := appCore.LoadPolicySet(context.Background(), "tenant_1", "policy_default")
	active.PolicySetID = "policy_approval"
	active.ReleasePolicy.RequireApprovalForStable = true
	active.ReleasePolicy.RequireCanaryBeforeStable = false
	if err := appCore.Policies.Put(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.draft.create",
		"payload": map[string]any{
			"policy_set_id": "policy_approval",
			"version":       "v2",
			"policy": map[string]any{
				"policy_set_id": "policy_approval",
				"version":       "v2",
				"release_policy": map[string]any{
					"require_approval_for_stable":  true,
					"require_canary_before_stable": false,
				},
				"memory_policy":   map[string]any{"allow_write": true, "allow_read": true},
				"artifact_policy": map[string]any{"allow_read": true},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if create.Code != http.StatusOK {
		t.Fatalf("policy draft create failed %d body %s", create.Code, create.Body.String())
	}
	var draft contracts.PolicyDraft
	if err := json.Unmarshal(create.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	validate := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.draft.validate",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if validate.Code != http.StatusOK {
		t.Fatalf("policy validate failed %d body %s", validate.Code, validate.Body.String())
	}
	publish := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.publish",
		"payload": map[string]any{"draft_id": draft.DraftID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if publish.Code != http.StatusOK {
		t.Fatalf("policy publish failed %d body %s", publish.Code, publish.Body.String())
	}
	var version contracts.PolicyVersion
	if err := json.Unmarshal(publish.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	evalResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "eval.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"policy_version_id":    version.PolicyVersionID,
			"input":                "hello",
			"final_reply_contains": []any{"ok"},
			"should_end_status":    "completed",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if evalResp.Code != http.StatusOK {
		t.Fatalf("policy eval failed %d body %s", evalResp.Code, evalResp.Body.String())
	}
	request := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.stable",
		"payload": map[string]any{"policy_version_id": version.PolicyVersionID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if request.Code != http.StatusOK || !bytes.Contains(request.Body.Bytes(), []byte(`"status":"approval_required"`)) {
		t.Fatalf("expected approval request, got %d body %s", request.Code, request.Body.String())
	}
	var requested struct {
		Approval contracts.ApprovalRequest `json:"approval"`
	}
	if err := json.Unmarshal(request.Body.Bytes(), &requested); err != nil {
		t.Fatal(err)
	}
	forged := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.stable",
		"payload": map[string]any{"policy_version_id": version.PolicyVersionID, "approved": true},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if forged.Code != http.StatusOK || !bytes.Contains(forged.Body.Bytes(), []byte(`"status":"approval_required"`)) {
		t.Fatalf("expected forged approval flag to be ignored, got %d body %s", forged.Code, forged.Body.String())
	}
	approve := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "approval.approve",
		"payload": map[string]any{"approval_id": requested.Approval.ApprovalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if approve.Code != http.StatusOK {
		t.Fatalf("approval approve failed %d body %s", approve.Code, approve.Body.String())
	}
	stable := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "policy.stable",
		"payload": map[string]any{"policy_version_id": version.PolicyVersionID, "approval_id": requested.Approval.ApprovalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if stable.Code != http.StatusOK || !bytes.Contains(stable.Body.Bytes(), []byte(`"status":"stable"`)) {
		t.Fatalf("expected stable after concrete approval, got %d body %s", stable.Code, stable.Body.String())
	}
}

func TestArtifactCommandsEnforcePolicyAndAudit(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := appCore.Artifacts.CreateArtifact(context.Background(), contracts.Artifact{
		ArtifactID: "artifact_policy_1",
		TenantID:   "tenant_1",
		Type:       "text",
		Name:       "policy artifact",
		StorageURI: "memory://artifact_policy_1",
		SizeBytes:  12,
		Hash:       "hash_1",
		CreatedBy:  "tester",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	readResp := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_artifact_policy_1",
		"command":  "artifact.read",
		"payload":  map[string]any{"artifact_id": "artifact_policy_1"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if readResp.Code != http.StatusOK {
		t.Fatalf("artifact read failed %d body %s", readResp.Code, readResp.Body.String())
	}
	deleteDenied := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_artifact_policy_1",
		"command":  "artifact.delete",
		"payload":  map[string]any{"artifact_id": "artifact_policy_1", "reason": "cleanup"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if deleteDenied.Code != http.StatusBadRequest {
		t.Fatalf("expected artifact delete to be denied by policy, got %d body %s", deleteDenied.Code, deleteDenied.Body.String())
	}
	policy := appCore.LoadPolicySet(context.Background(), "tenant_1", "policy_default")
	policy.ArtifactPolicy.AllowDelete = true
	if err := appCore.Policies.Put(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	deleteResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_artifact_policy_1",
		"command":  "artifact.delete",
		"payload":  map[string]any{"artifact_id": "artifact_policy_1", "reason": "cleanup"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("artifact delete failed %d body %s", deleteResp.Code, deleteResp.Body.String())
	}
	auditResp := doJSON(handler, "GET", "/v1/audit?action=artifact.delete", nil)
	if auditResp.Code != http.StatusOK || !bytes.Contains(auditResp.Body.Bytes(), []byte("artifact.delete")) {
		t.Fatalf("expected artifact delete audit, got %d body %s", auditResp.Code, auditResp.Body.String())
	}
}

func TestAgentRunRecordsRequiredTraceEvents(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_required_1",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"input": "hello"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	traceResp := doJSON(handler, "GET", "/v1/traces/trace_required_1", nil)
	for _, eventType := range []string{
		contracts.TraceInputReceived,
		contracts.TraceAgentLoaded,
		contracts.TraceTaskCreated,
		contracts.TraceCapabilityRetrieved,
		contracts.TraceRunCreated,
	} {
		if !bytes.Contains(traceResp.Body.Bytes(), []byte(eventType)) {
			t.Fatalf("expected trace to include %s, got %s", eventType, traceResp.Body.String())
		}
	}
}

func TestEnsureRunnableAgentVersionChecksTenantDefaultVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := loader.TestAgentDefinition()
	definition.TenantID = "tenant_1"
	definition.Version = "v2"
	appCore.AgentRegistry.Put(definition)
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v2", agentPackageSourceForTest(), "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraft(context.Background(), draft.DraftID); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraft(context.Background(), draft.DraftID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkCanary(context.Background(), release.PackageVersionID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if err := appCore.AgentRegistry.SetDefaultForTenant("tenant_1", "test-agent", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.Rollback(context.Background(), release.PackageVersionID, "optimizer_1", "bad release"); err != nil {
		t.Fatal(err)
	}
	if err := ensureRunnableAgentVersion(appCore, "tenant_1", contracts.AgentTarget{AgentID: "test-agent"}); err == nil {
		t.Fatal("expected rolled back tenant default version to be denied")
	}
}

func TestReleaseForAgentVersionSelectsLatestRelease(t *testing.T) {
	oldTime := time.Unix(1, 0).UTC()
	newTime := time.Unix(2, 0).UTC()
	release, ok := releaseForAgentVersion([]contracts.AgentPackageVersion{
		{
			PackageVersionID: "pkg_old",
			TenantID:         "tenant_1",
			AgentID:          "test-agent",
			Version:          "v2",
			Status:           contracts.ReleasePublished,
			CreatedAt:        oldTime,
			PublishedAt:      &oldTime,
		},
		{
			PackageVersionID: "pkg_new",
			TenantID:         "tenant_1",
			AgentID:          "test-agent",
			Version:          "v2",
			Status:           contracts.ReleaseStable,
			CreatedAt:        newTime,
			PublishedAt:      &newTime,
		},
	}, "tenant_1", "test-agent", "v2")
	if !ok || release.PackageVersionID != "pkg_new" {
		t.Fatalf("expected latest release, got %#v ok=%v", release, ok)
	}
}

func agentPackageSourceForTest() agentpackage.AgentPackageSource {
	return agentpackage.AgentPackageSource{Prompt: "new prompt"}
}

func assertDefaultRunVersion(t *testing.T, handler http.Handler, appCore *core.Core, want contracts.AgentVersion) {
	t.Helper()
	runBody := map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent"},
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", runBody)
	if resp.Code != http.StatusOK {
		t.Fatalf("agent.run failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		RunID contracts.AgentRunID `json:"run_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	run, err := appCore.Runs.Get(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentVersion != want {
		t.Fatalf("expected default run version %s, got %#v", want, run)
	}
}

func TestToolManifestUpsertRegistersHTTPTool(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{"arguments": payload["arguments"]},
		})
	}))
	defer remote.Close()
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_tool_catalog_1",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"command":  "tool.manifest.upsert",
		"payload": map[string]any{
			"tool_id":      "http.echo",
			"name":         "HTTP echo",
			"description":  "Echo arguments through HTTP.",
			"input_schema": map[string]any{"type": "object"},
			"executor": map[string]any{
				"type": "http_direct",
				"url":  remote.URL,
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSONWithHeaders(handler, "POST", "/v1/commands", body, map[string]string{"X-Roles": "optimizer"})
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body %s", resp.Code, resp.Body.String())
	}
	tool, ok := appCore.Tools.GetForTenant("tenant_1", "http.echo")
	if !ok {
		t.Fatal("expected dynamic tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "http.echo",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output["arguments"] == nil {
		t.Fatalf("unexpected output %#v", output)
	}
	listResp := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_tool_catalog_2",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"command":  "tool.manifest.list",
		"context":  map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if listResp.Code != http.StatusOK || !bytes.Contains(listResp.Body.Bytes(), []byte("http.echo")) {
		t.Fatalf("unexpected list status %d body %s", listResp.Code, listResp.Body.String())
	}
}

func TestOriginAgentDelegateCommand(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := loader.TestAgentDefinition()
	definition.TenantID = "tenant_1"
	definition.Collaborators = []contracts.AgentCollaboratorRef{{AgentID: "target-agent", WhenToUse: []string{"review", "delegate"}}}
	appCore.AgentRegistry.Put(definition)
	target := loader.TestAgentDefinition()
	target.TenantID = "tenant_1"
	target.AgentID = "target-agent"
	appCore.AgentRegistry.Put(target)
	parent, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_delegate_1",
		"command":  "origin.agent.delegate",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"parent_task_id": parent.TaskID,
			"to_agent_id":    "target-agent",
			"objective":      "review this",
			"reason":         "specialized check",
			"handoff_mode":   "hybrid",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("delegate failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Handoff contracts.AgentHandoff `json:"handoff"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Handoff.Status != contracts.HandoffCompleted || result.Handoff.ChildTaskID == nil {
		t.Fatalf("unexpected handoff: %#v", result)
	}
	trace := doJSON(handler, "GET", "/v1/handoffs/"+string(result.Handoff.HandoffID)+"/trace", nil)
	if trace.Code != http.StatusOK {
		t.Fatalf("handoff trace failed %d body %s", trace.Code, trace.Body.String())
	}
	runTrace := doJSON(handler, "GET", "/v1/traces/trace_delegate_1", nil)
	if runTrace.Code != http.StatusOK || !bytes.Contains(runTrace.Body.Bytes(), []byte("handoff.completed")) {
		t.Fatalf("expected handoff trace events, got %d body %s", runTrace.Code, runTrace.Body.String())
	}
}

func TestOriginAgentDelegateCommandRequiresTargetAgent(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_delegate_missing_target",
		"command":  "origin.agent.delegate",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"parent_task_id": parent.TaskID,
			"objective":      "review this",
			"reason":         "specialized check",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d body %s", resp.Code, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("to_agent_id")) {
		t.Fatalf("expected to_agent_id error, got %s", resp.Body.String())
	}
}

func TestAgentResourceAPIDisableBlocksRunHandoffAndAgentTool(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	source := loader.TestAgentDefinition()
	source.TenantID = "tenant_1"
	source.Collaborators = []contracts.AgentCollaboratorRef{{AgentID: "blocked-agent", WhenToUse: []string{"delegate", "review"}}}
	source.Tools.AllowedToolIDs = append(source.Tools.AllowedToolIDs, "blocked.summary")
	source.Tools.ExposedToolIDs = append(source.Tools.ExposedToolIDs, "blocked.summary")
	appCore.AgentRegistry.Put(source)
	target := loader.TestAgentDefinition()
	target.TenantID = "tenant_1"
	target.AgentID = "blocked-agent"
	target.Exports = contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
		ToolID:      "blocked.summary",
		Operation:   "summary",
		Name:        "Blocked summary",
		Description: "Summarize through blocked agent.",
		InputSchema: map[string]any{"type": "object"},
		Status:      "enabled",
	}}}
	appCore.AgentRegistry.Put(target)
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	create := doJSON(handler, "POST", "/v1/agents", map[string]any{
		"agent_id":    "blocked-agent",
		"name":        "Blocked Agent",
		"description": "disabled fixture",
		"owner_id":    "owner_1",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("agent create failed %d body %s", create.Code, create.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/blocked-agent", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("Blocked Agent")) {
		t.Fatalf("agent get failed %d body %s", get.Code, get.Body.String())
	}
	list := doJSON(handler, "GET", "/v1/agents", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("blocked-agent")) {
		t.Fatalf("agent list failed %d body %s", list.Code, list.Body.String())
	}
	patch := doJSON(handler, "PATCH", "/v1/agents/blocked-agent", map[string]any{"status": "disabled"})
	if patch.Code != http.StatusOK || !bytes.Contains(patch.Body.Bytes(), []byte("disabled")) {
		t.Fatalf("agent disable failed %d body %s", patch.Code, patch.Body.String())
	}

	run := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_run",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "blocked-agent", "version": "v1"},
		"payload":  map[string]any{"input": "hello"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if run.Code != http.StatusBadRequest || !bytes.Contains(run.Body.Bytes(), []byte("not active")) {
		t.Fatalf("expected disabled run to fail, got %d body %s", run.Code, run.Body.String())
	}

	parent, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}
	delegate := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_handoff",
		"command":  "origin.agent.delegate",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"parent_task_id": parent.TaskID,
			"to_agent_id":    "blocked-agent",
			"objective":      "delegate review",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if delegate.Code != http.StatusOK || !bytes.Contains(delegate.Body.Bytes(), []byte("failed")) || !bytes.Contains(delegate.Body.Bytes(), []byte("not active")) {
		t.Fatalf("expected disabled handoff to fail, got %d body %s", delegate.Code, delegate.Body.String())
	}

	manifest := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_manifest",
		"command":  "tool.manifest.upsert",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"tool_id":      "blocked.summary",
			"name":         "Blocked summary",
			"description":  "Summarize through blocked agent.",
			"input_schema": map[string]any{"type": "object"},
			"executor": map[string]any{
				"type":        "agent_tool",
				"provider_id": "blocked-agent",
				"operation":   "summary",
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if manifest.Code != http.StatusOK {
		t.Fatalf("agent_tool manifest failed %d body %s", manifest.Code, manifest.Body.String())
	}
	toolInvoke := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_agent_tool",
		"command":  "tools.invoke",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"tool_id": "blocked.summary", "arguments": map[string]any{}},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	})
	if toolInvoke.Code != http.StatusOK || !bytes.Contains(toolInvoke.Body.Bytes(), []byte("failed")) || !bytes.Contains(toolInvoke.Body.Bytes(), []byte("not active")) {
		t.Fatalf("expected disabled agent_tool to fail, got %d body %s", toolInvoke.Code, toolInvoke.Body.String())
	}
}

func TestAgentCreateDoesNotRequireSkillOrToolIDs(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	create := doJSON(handler, "POST", "/v1/agents", map[string]any{
		"agent_id": "agent-without-skills",
		"name":     "Agent Without Skills",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("agent create without skill_id failed %d body %s", create.Code, create.Body.String())
	}
	if !bytes.Contains(create.Body.Bytes(), []byte("agent-without-skills")) {
		t.Fatalf("expected created agent response, got %s", create.Body.String())
	}

	skill := doJSON(handler, "POST", "/v1/agents/agent-without-skills/skills", map[string]any{
		"name": "Missing Skill ID",
	})
	if skill.Code != http.StatusBadRequest || !bytes.Contains(skill.Body.Bytes(), []byte("skill_id")) {
		t.Fatalf("expected skill upsert to require skill_id, got %d body %s", skill.Code, skill.Body.String())
	}

	exportedTool := doJSON(handler, "POST", "/v1/agents/agent-without-skills/exported-tools", map[string]any{
		"name":        "Missing Tool ID",
		"description": "Exported tool without an id.",
	})
	if exportedTool.Code != http.StatusBadRequest || !bytes.Contains(exportedTool.Body.Bytes(), []byte("tool_id")) {
		t.Fatalf("expected exported tool upsert to require tool_id, got %d body %s", exportedTool.Code, exportedTool.Body.String())
	}
}

func TestAgentResourceAPIDisableBlocksSourceHandoffEntrypoints(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	source := loader.TestAgentDefinition()
	source.TenantID = "tenant_1"
	source.Collaborators = []contracts.AgentCollaboratorRef{{AgentID: "target-agent", WhenToUse: []string{"delegate", "review"}}}
	appCore.AgentRegistry.Put(source)
	target := loader.TestAgentDefinition()
	target.TenantID = "tenant_1"
	target.AgentID = "target-agent"
	appCore.AgentRegistry.Put(target)
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	create := doJSON(handler, "POST", "/v1/agents", map[string]any{
		"agent_id": "test-agent",
		"name":     "Disabled Source",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("agent create failed %d body %s", create.Code, create.Body.String())
	}
	patch := doJSON(handler, "PATCH", "/v1/agents/test-agent", map[string]any{"status": "disabled"})
	if patch.Code != http.StatusOK {
		t.Fatalf("agent disable failed %d body %s", patch.Code, patch.Body.String())
	}
	parent, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}

	delegate := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_source_delegate",
		"command":  "origin.agent.delegate",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"parent_task_id": parent.TaskID,
			"to_agent_id":    "target-agent",
			"objective":      "delegate review",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if delegate.Code != http.StatusBadRequest || !bytes.Contains(delegate.Body.Bytes(), []byte("not active")) {
		t.Fatalf("expected disabled source delegate to fail, got %d body %s", delegate.Code, delegate.Body.String())
	}

	legacy := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_blocked_source_handoff_command",
		"command":  "task.command",
		"payload": map[string]any{
			"task_id":     parent.TaskID,
			"command":     "create_handoff",
			"to_agent_id": "target-agent",
			"objective":   "delegate review",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if legacy.Code != http.StatusBadRequest || !bytes.Contains(legacy.Body.Bytes(), []byte("not active")) {
		t.Fatalf("expected disabled source handoff command to fail, got %d body %s", legacy.Code, legacy.Body.String())
	}
}

func TestAgentToolInvokeRunsProviderAgent(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	source := loader.TestAgentDefinition()
	source.TenantID = "tenant_1"
	source.Tools.AllowedToolIDs = []string{"customer.lookup"}
	source.Tools.ExposedToolIDs = []string{"customer.lookup"}
	appCore.AgentRegistry.Put(source)
	provider := loader.TestAgentDefinition()
	provider.TenantID = "tenant_1"
	provider.AgentID = "provider-agent"
	provider.Exports = contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
		ToolID:      "customer.lookup",
		Operation:   "lookup",
		Name:        "Customer lookup",
		Description: "Look up customer context.",
		InputSchema: map[string]any{"type": "object"},
		Status:      "enabled",
	}}}
	appCore.AgentRegistry.Put(provider)
	if _, err := appCore.ToolCatalog.UpsertManifest(context.Background(), toolcatalog.ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "customer.lookup",
		Name:        "Customer lookup",
		Description: "Look up customer context.",
		InputSchema: map[string]any{"type": "object"},
		RiskLevel:   contracts.RiskLow,
		Visibility:  contracts.ToolProtected,
		Executor: toolcatalog.ExecutorSpec{
			Type:       toolcatalog.ExecutorTypeAgentTool,
			ProviderID: "provider-agent",
			Operation:  "lookup",
		},
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	invoke := doJSON(handler, "POST", "/v1/commands", map[string]any{
		"trace_id": "trace_agent_tool_provider_run",
		"command":  "tools.invoke",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"tool_id":   "customer.lookup",
			"arguments": map[string]any{"input": "find ACME"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	})
	if invoke.Code != http.StatusOK || !bytes.Contains(invoke.Body.Bytes(), []byte(`"reply_text":"ok"`)) || !bytes.Contains(invoke.Body.Bytes(), []byte(`"provider_agent_id":"provider-agent"`)) {
		t.Fatalf("expected agent_tool to run provider agent, got %d body %s", invoke.Code, invoke.Body.String())
	}
	if traces, err := appCore.Trace.ListByTrace(context.Background(), "trace_agent_tool_provider_run"); err != nil {
		t.Fatal(err)
	} else {
		found := false
		for _, event := range traces {
			if event.Type == "agent_tool.completed" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected agent_tool.completed trace, got %#v", traces)
		}
	}
}

func TestHighRiskAgentToolInvokeRequiresApproval(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	source := loader.TestAgentDefinition()
	source.TenantID = "tenant_1"
	source.Tools.AllowedToolIDs = []string{"customer.delete"}
	source.Tools.ExposedToolIDs = []string{"customer.delete"}
	appCore.AgentRegistry.Put(source)
	provider := loader.TestAgentDefinition()
	provider.TenantID = "tenant_1"
	provider.AgentID = "provider-agent"
	provider.Exports = contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
		ToolID:      "customer.delete",
		Operation:   "delete",
		Name:        "Customer delete",
		Description: "Delete customer data.",
		InputSchema: map[string]any{"type": "object"},
		RiskLevel:   contracts.RiskHigh,
		Visibility:  contracts.ToolProtected,
		Status:      "enabled",
		Version:     "v1",
	}}}
	appCore.AgentRegistry.Put(provider)
	if err := syncAgentExportedTools(context.Background(), appCore, provider, "tester"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	invokeBody := map[string]any{
		"trace_id": "trace_agent_tool_approval",
		"command":  "tools.invoke",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"tool_id":   "customer.delete",
			"arguments": map[string]any{"input": "delete ACME"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	pending := doJSON(handler, "POST", "/v1/commands", invokeBody)
	if pending.Code != http.StatusOK || !bytes.Contains(pending.Body.Bytes(), []byte(`"status":"approval_required"`)) {
		t.Fatalf("expected high-risk agent_tool approval request, got %d body %s", pending.Code, pending.Body.String())
	}
	var pendingResp struct {
		Status     string                    `json:"status"`
		Approval   contracts.ApprovalRequest `json:"approval"`
		ToolResult contracts.ToolResult      `json:"tool_result"`
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &pendingResp); err != nil {
		t.Fatal(err)
	}
	if pendingResp.Approval.ResourceType != "tool" || pendingResp.Approval.ResourceID != "customer.delete" || pendingResp.Approval.Action != "tools.invoke" {
		t.Fatalf("unexpected approval request: %#v", pendingResp.Approval)
	}
	if pendingResp.ToolResult.Status != contracts.ToolResultPendingApproval {
		t.Fatalf("expected pending tool result, got %#v", pendingResp.ToolResult)
	}
	approved := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "approval.approve",
		"payload": map[string]any{"approval_id": pendingResp.Approval.ApprovalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if approved.Code != http.StatusOK {
		t.Fatalf("approval approve failed %d body %s", approved.Code, approved.Body.String())
	}
	approvedBody := map[string]any{
		"trace_id": "trace_agent_tool_approval",
		"command":  "tools.invoke",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"tool_id":     "customer.delete",
			"approval_id": pendingResp.Approval.ApprovalID,
			"arguments":   map[string]any{"input": "delete ACME"},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	executed := doJSON(handler, "POST", "/v1/commands", approvedBody)
	if executed.Code != http.StatusOK || !bytes.Contains(executed.Body.Bytes(), []byte(`"reply_text":"ok"`)) || !bytes.Contains(executed.Body.Bytes(), []byte(`"provider_agent_id":"provider-agent"`)) {
		t.Fatalf("expected approved high-risk agent_tool to run provider agent, got %d body %s", executed.Code, executed.Body.String())
	}
}

func TestResourceAPIsExposeToolCatalogAndRuntimeHooks(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	toolHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer toolHost.Close()

	provider := doJSON(handler, "POST", "/v1/tool-providers", map[string]any{
		"provider_id":   "crm-provider",
		"provider_type": "static_tool_host",
		"name":          "CRM Provider",
		"endpoint":      toolHost.URL,
	})
	if provider.Code != http.StatusCreated || !bytes.Contains(provider.Body.Bytes(), []byte("crm-provider")) {
		t.Fatalf("tool provider create failed %d body %s", provider.Code, provider.Body.String())
	}
	health := doJSON(handler, "POST", "/v1/tool-providers/crm-provider/health", nil)
	if health.Code != http.StatusOK || !bytes.Contains(health.Body.Bytes(), []byte(`"health_status":"healthy"`)) {
		t.Fatalf("tool provider health failed %d body %s", health.Code, health.Body.String())
	}
	group := doJSON(handler, "POST", "/v1/tool-groups", map[string]any{
		"group_id": "crm",
		"name":     "CRM",
	})
	if group.Code != http.StatusCreated || !bytes.Contains(group.Body.Bytes(), []byte("crm")) {
		t.Fatalf("tool group create failed %d body %s", group.Code, group.Body.String())
	}
	manifest := doJSON(handler, "POST", "/v1/tool-manifests", map[string]any{
		"tool_id":      "crm.lookup",
		"group_id":     "crm",
		"name":         "CRM lookup",
		"description":  "Lookup CRM records.",
		"input_schema": map[string]any{"type": "object"},
		"executor": map[string]any{
			"type":        "static_tool_host",
			"provider_id": "crm-provider",
			"operation":   "lookup",
		},
	})
	if manifest.Code != http.StatusCreated || !bytes.Contains(manifest.Body.Bytes(), []byte("crm.lookup")) {
		t.Fatalf("tool manifest create failed %d body %s", manifest.Code, manifest.Body.String())
	}
	if list := doJSON(handler, "GET", "/v1/tool-manifests", nil); list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("crm.lookup")) {
		t.Fatalf("tool manifest list failed %d body %s", list.Code, list.Body.String())
	}
	if get := doJSON(handler, "GET", "/v1/tool-providers/crm-provider", nil); get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("CRM Provider")) {
		t.Fatalf("tool provider get failed %d body %s", get.Code, get.Body.String())
	}

	hookProvider := doJSON(handler, "POST", "/v1/runtime-hook-providers", map[string]any{
		"provider_id":   "local-hooks",
		"provider_type": "go",
		"name":          "Local Hooks",
	})
	if hookProvider.Code != http.StatusCreated || !bytes.Contains(hookProvider.Body.Bytes(), []byte("local-hooks")) {
		t.Fatalf("runtime hook provider create failed %d body %s", hookProvider.Code, hookProvider.Body.String())
	}
	hookHealth := doJSON(handler, "POST", "/v1/runtime-hook-providers/local-hooks/health", nil)
	if hookHealth.Code != http.StatusOK || !bytes.Contains(hookHealth.Body.Bytes(), []byte(`"health_status":"healthy"`)) {
		t.Fatalf("runtime hook provider health failed %d body %s", hookHealth.Code, hookHealth.Body.String())
	}
	hookHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/catalog" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hooks": []map[string]any{{
				"hook_id": "remote-rerank",
				"name":    "Remote rerank",
				"phase":   "after_candidate_retrieval",
			}},
		})
	}))
	defer hookHost.Close()
	staticHookProvider := doJSON(handler, "POST", "/v1/runtime-hook-providers", map[string]any{
		"provider_id":   "static-hooks",
		"provider_type": "static_hook_host",
		"name":          "Static Hooks",
		"endpoint":      hookHost.URL,
	})
	if staticHookProvider.Code != http.StatusCreated || !bytes.Contains(staticHookProvider.Body.Bytes(), []byte("static-hooks")) {
		t.Fatalf("runtime hook static provider create failed %d body %s", staticHookProvider.Code, staticHookProvider.Body.String())
	}
	hookCatalog := doJSON(handler, "POST", "/v1/runtime-hook-providers/static-hooks/catalog", nil)
	if hookCatalog.Code != http.StatusOK || !bytes.Contains(hookCatalog.Body.Bytes(), []byte("remote-rerank")) {
		t.Fatalf("runtime hook provider catalog failed %d body %s", hookCatalog.Code, hookCatalog.Body.String())
	}
	hookSync := doJSON(handler, "POST", "/v1/runtime-hook-providers/static-hooks/catalog/sync", nil)
	if hookSync.Code != http.StatusOK || !bytes.Contains(hookSync.Body.Bytes(), []byte("remote-rerank")) {
		t.Fatalf("runtime hook provider catalog sync failed %d body %s", hookSync.Code, hookSync.Body.String())
	}
	hookManifests := doJSON(handler, "GET", "/v1/runtime-hook-manifests?provider_id=static-hooks", nil)
	if hookManifests.Code != http.StatusOK || !bytes.Contains(hookManifests.Body.Bytes(), []byte("remote-rerank")) {
		t.Fatalf("runtime hook manifest list failed %d body %s", hookManifests.Code, hookManifests.Body.String())
	}
	hookManifest := doJSON(handler, "GET", "/v1/runtime-hook-manifests/remote-rerank", nil)
	if hookManifest.Code != http.StatusOK || !bytes.Contains(hookManifest.Body.Bytes(), []byte(`"phase":"after_candidate_retrieval"`)) {
		t.Fatalf("runtime hook manifest get failed %d body %s", hookManifest.Code, hookManifest.Body.String())
	}
	localHookV1 := doJSON(handler, "POST", "/v1/runtime-hook-manifests", map[string]any{
		"hook_id": "local-rerank",
		"name":    "Local rerank v1",
		"phase":   "before_model_call",
		"version": "r1",
		"status":  "enabled",
	})
	if localHookV1.Code != http.StatusCreated || !bytes.Contains(localHookV1.Body.Bytes(), []byte("Local rerank v1")) {
		t.Fatalf("runtime hook manifest v1 create failed %d body %s", localHookV1.Code, localHookV1.Body.String())
	}
	localHookV2 := doJSON(handler, "PUT", "/v1/runtime-hook-manifests/local-rerank", map[string]any{
		"hook_id": "local-rerank",
		"name":    "Local rerank v2",
		"phase":   "before_model_call",
		"version": "r2",
		"status":  "enabled",
	})
	if localHookV2.Code != http.StatusOK || !bytes.Contains(localHookV2.Body.Bytes(), []byte("Local rerank v2")) {
		t.Fatalf("runtime hook manifest v2 update failed %d body %s", localHookV2.Code, localHookV2.Body.String())
	}
	localHookVersions := doJSON(handler, "GET", "/v1/runtime-hook-manifests/local-rerank/versions", nil)
	if localHookVersions.Code != http.StatusOK || !bytes.Contains(localHookVersions.Body.Bytes(), []byte(`"r1"`)) || !bytes.Contains(localHookVersions.Body.Bytes(), []byte(`"r2"`)) {
		t.Fatalf("runtime hook manifest versions failed %d body %s", localHookVersions.Code, localHookVersions.Body.String())
	}
	localHookVersion := doJSON(handler, "GET", "/v1/runtime-hook-manifests/local-rerank/versions/r1", nil)
	if localHookVersion.Code != http.StatusOK || !bytes.Contains(localHookVersion.Body.Bytes(), []byte("Local rerank v1")) {
		t.Fatalf("runtime hook manifest version get failed %d body %s", localHookVersion.Code, localHookVersion.Body.String())
	}
	localHookActivateV1 := doJSON(handler, "POST", "/v1/runtime-hook-manifests/local-rerank/versions/r1/activate", nil)
	if localHookActivateV1.Code != http.StatusOK || !bytes.Contains(localHookActivateV1.Body.Bytes(), []byte(`"active":true`)) || !bytes.Contains(localHookActivateV1.Body.Bytes(), []byte("Local rerank v1")) {
		t.Fatalf("runtime hook manifest version activate failed %d body %s", localHookActivateV1.Code, localHookActivateV1.Body.String())
	}
	localHookV3Disabled := doJSON(handler, "PUT", "/v1/runtime-hook-manifests/local-rerank", map[string]any{
		"hook_id": "local-rerank",
		"name":    "Local rerank disabled",
		"phase":   "before_model_call",
		"version": "r3",
		"status":  "disabled",
	})
	if localHookV3Disabled.Code != http.StatusOK || !bytes.Contains(localHookV3Disabled.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("runtime hook manifest disabled version update failed %d body %s", localHookV3Disabled.Code, localHookV3Disabled.Body.String())
	}
	rejectedHookActivate := doJSON(handler, "POST", "/v1/runtime-hook-manifests/local-rerank/versions/r3/activate", nil)
	if rejectedHookActivate.Code != http.StatusBadRequest || !bytes.Contains(rejectedHookActivate.Body.Bytes(), []byte("must be enabled")) {
		t.Fatalf("disabled runtime hook manifest version should reject activation, got %d body %s", rejectedHookActivate.Code, rejectedHookActivate.Body.String())
	}
	localHookActivateV2 := doJSON(handler, "POST", "/v1/runtime-hook-manifests/local-rerank/versions/r2/activate", nil)
	if localHookActivateV2.Code != http.StatusOK || !bytes.Contains(localHookActivateV2.Body.Bytes(), []byte("Local rerank v2")) {
		t.Fatalf("runtime hook manifest re-activate failed %d body %s", localHookActivateV2.Code, localHookActivateV2.Body.String())
	}
	if bindings := doJSON(handler, "GET", "/v1/agents/test-agent/runtime-hooks", nil); bindings.Code != http.StatusOK || bytes.Contains(bindings.Body.Bytes(), []byte("remote-rerank")) {
		t.Fatalf("runtime hook catalog should not auto-install bindings, got %d body %s", bindings.Code, bindings.Body.String())
	}
	disabledHookManifest := doJSON(handler, "DELETE", "/v1/runtime-hook-manifests/remote-rerank", nil)
	if disabledHookManifest.Code != http.StatusOK || !bytes.Contains(disabledHookManifest.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("runtime hook manifest disable failed %d body %s", disabledHookManifest.Code, disabledHookManifest.Body.String())
	}
	rejectedStaticBinding := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":        "remote-rerank",
		"provider_type":  "static_hook_host",
		"provider_id":    "static-hooks",
		"phase":          "after_candidate_retrieval",
		"enabled":        true,
		"failure_policy": "reject",
	})
	if rejectedStaticBinding.Code != http.StatusBadRequest || !bytes.Contains(rejectedStaticBinding.Body.Bytes(), []byte("hook manifest is not enabled")) {
		t.Fatalf("disabled runtime hook manifest should reject binding, got %d body %s", rejectedStaticBinding.Code, rejectedStaticBinding.Body.String())
	}
	binding := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":       "local-rerank",
		"provider_type": "go",
		"phase":         "before_model_call",
		"enabled":       true,
	})
	if binding.Code != http.StatusOK || !bytes.Contains(binding.Body.Bytes(), []byte("local-rerank")) {
		t.Fatalf("runtime hook binding upsert failed %d body %s", binding.Code, binding.Body.String())
	}
	if bindings := doJSON(handler, "GET", "/v1/agents/test-agent/runtime-hooks", nil); bindings.Code != http.StatusOK || !bytes.Contains(bindings.Body.Bytes(), []byte("local-rerank")) {
		t.Fatalf("runtime hook binding list failed %d body %s", bindings.Code, bindings.Body.String())
	}
	providerBinding := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":       "local-provider-rerank",
		"provider_type": "go",
		"provider_id":   "local-hooks",
		"phase":         "before_model_call",
		"enabled":       true,
		"config": map[string]any{
			"patch": map[string]any{
				"planner_hints": []any{map[string]any{"content": "prefer local hook provider"}},
			},
		},
	})
	if providerBinding.Code != http.StatusOK || !bytes.Contains(providerBinding.Body.Bytes(), []byte("local-provider-rerank")) {
		t.Fatalf("runtime hook provider binding upsert failed %d body %s", providerBinding.Code, providerBinding.Body.String())
	}
	_, _, _ = appCore.RuntimeHooks.Invoke(context.Background(), runtimehook.InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_hook_events",
		Agent: contracts.AgentDefinition{
			AgentID: "test-agent",
			Version: "v1",
			RuntimeHooks: contracts.AgentRuntimeHooks{
				Mode: "data_hooks",
				Hooks: []contracts.AgentRuntimeHookBinding{{
					HookID:        "rejected-rerank",
					ProviderType:  string(runtimehook.ProviderTypeGo),
					Phase:         string(runtimehook.BeforeModelCall),
					Enabled:       true,
					FailurePolicy: "ignore",
					Config: map[string]any{
						"patch": map[string]any{
							"tool_rank_adjustments": []any{map[string]any{"tool_id": "not-allowed", "boost": true}},
						},
					},
				}},
			},
		},
		Policy: contracts.PolicySet{},
		Phase:  runtimehook.BeforeModelCall,
	})
	hookEvents := doJSON(handler, "GET", "/v1/runtime-hook-events?trace_id=trace_hook_events&status=rejected", nil)
	if hookEvents.Code != http.StatusOK || !bytes.Contains(hookEvents.Body.Bytes(), []byte("rejected-rerank")) {
		t.Fatalf("runtime hook events query failed %d body %s", hookEvents.Code, hookEvents.Body.String())
	}
	providerEvents := doJSON(handler, "GET", "/v1/runtime-hook-events?trace_id=trace_hook_events&provider_id=local-hooks", nil)
	if providerEvents.Code != http.StatusOK || !bytes.Contains(providerEvents.Body.Bytes(), []byte("local-provider-rerank")) || !bytes.Contains(providerEvents.Body.Bytes(), []byte(`"provider_id":"local-hooks"`)) {
		t.Fatalf("runtime hook provider events query failed %d body %s", providerEvents.Code, providerEvents.Body.String())
	}
	governance := doJSON(handler, "GET", "/v1/runtime-hook-governance?trace_id=trace_hook_events&provider_id=local-hooks", nil)
	if governance.Code != http.StatusOK || !bytes.Contains(governance.Body.Bytes(), []byte(`"total_events":1`)) || !bytes.Contains(governance.Body.Bytes(), []byte(`"ok_events_total":1`)) || !bytes.Contains(governance.Body.Bytes(), []byte(`"provider_matrix"`)) || !bytes.Contains(governance.Body.Bytes(), []byte("local-provider-rerank")) {
		t.Fatalf("runtime hook governance summary failed %d body %s", governance.Code, governance.Body.String())
	}
	governanceTrend := doJSON(handler, "GET", "/v1/runtime-hook-governance?trace_id=trace_hook_events&provider_id=local-hooks&from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z&interval=24h", nil)
	if governanceTrend.Code != http.StatusOK || !bytes.Contains(governanceTrend.Body.Bytes(), []byte(`"trend_interval":"24h"`)) || !bytes.Contains(governanceTrend.Body.Bytes(), []byte(`"trend":[`)) {
		t.Fatalf("runtime hook governance trend failed %d body %s", governanceTrend.Code, governanceTrend.Body.String())
	}
}

func TestToolProviderSyncCreatesDefaultToolGroupResource(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	toolHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/tools/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{"tools": []map[string]any{{
				"tool_id":      "lookup",
				"operation":    "lookup",
				"name":         "CRM lookup",
				"description":  "Lookup CRM records.",
				"input_schema": map[string]any{"type": "object"},
				"risk_level":   "low",
				"visibility":   "protected",
				"version":      "v1",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer toolHost.Close()

	provider := doJSON(handler, "POST", "/v1/tool-providers", map[string]any{
		"provider_id":   "crm-provider",
		"provider_type": "static_tool_host",
		"name":          "CRM Provider",
		"endpoint":      toolHost.URL,
	})
	if provider.Code != http.StatusCreated {
		t.Fatalf("tool provider create failed %d body %s", provider.Code, provider.Body.String())
	}
	health := doJSON(handler, "POST", "/v1/tool-providers/crm-provider/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("tool provider health failed %d body %s", health.Code, health.Body.String())
	}
	syncResp := doJSON(handler, "POST", "/v1/tool-providers/crm-provider/sync", nil)
	if syncResp.Code != http.StatusOK ||
		!bytes.Contains(syncResp.Body.Bytes(), []byte(`"group_id":"crm-provider-default"`)) ||
		!bytes.Contains(syncResp.Body.Bytes(), []byte(`"provider_id":"crm-provider"`)) ||
		!bytes.Contains(syncResp.Body.Bytes(), []byte(`"tool_ids":["crm-provider.lookup"]`)) {
		t.Fatalf("tool provider sync did not expose default group %d body %s", syncResp.Code, syncResp.Body.String())
	}
	groups := doJSON(handler, "GET", "/v1/tool-groups", nil)
	if groups.Code != http.StatusOK || !bytes.Contains(groups.Body.Bytes(), []byte(`"group_id":"crm-provider-default"`)) {
		t.Fatalf("tool groups list missing provider default group %d body %s", groups.Code, groups.Body.String())
	}
}

func TestToolProviderPatchMergesSecretRefWithoutResettingProvider(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	toolHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer toolHost.Close()

	provider := doJSON(handler, "POST", "/v1/tool-providers", map[string]any{
		"provider_id":   "crm-provider",
		"provider_type": "static_tool_host",
		"name":          "CRM Provider",
		"endpoint":      toolHost.URL,
		"auth_ref":      "auth_ref://toolhost/crm",
	})
	if provider.Code != http.StatusCreated {
		t.Fatalf("tool provider create failed %d body %s", provider.Code, provider.Body.String())
	}
	health := doJSON(handler, "POST", "/v1/tool-providers/crm-provider/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("tool provider health failed %d body %s", health.Code, health.Body.String())
	}
	patch := doJSON(handler, "PATCH", "/v1/tool-providers/crm-provider", map[string]any{
		"secret_ref": "secret://toolhost/crm/signing",
	})
	if patch.Code != http.StatusOK ||
		!bytes.Contains(patch.Body.Bytes(), []byte(`"secret_ref":"secret://toolhost/crm/signing"`)) ||
		!bytes.Contains(patch.Body.Bytes(), []byte(`"health_status":"healthy"`)) ||
		!bytes.Contains(patch.Body.Bytes(), []byte(`"endpoint":"`+toolHost.URL+`"`)) {
		t.Fatalf("tool provider patch should merge existing fields, got %d body %s", patch.Code, patch.Body.String())
	}
}

func TestToolProviderGovernanceMatrixIncludesCatalogAndTraceEvidence(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	for _, provider := range []map[string]any{
		{
			"provider_id":   "crm-provider",
			"provider_type": "static_tool_host",
			"name":          "CRM Provider",
			"endpoint":      "http://crm.local",
			"health_status": "healthy",
		},
		{
			"provider_id":       "billing-provider",
			"provider_type":     "static_tool_host",
			"name":              "Billing Provider",
			"endpoint":          "http://billing.local",
			"health_status":     "unhealthy",
			"last_health_error": "timeout",
		},
	} {
		resp := doJSON(handler, "POST", "/v1/tool-providers", provider)
		if resp.Code != http.StatusCreated {
			t.Fatalf("tool provider create failed %d body %s", resp.Code, resp.Body.String())
		}
	}
	group := doJSON(handler, "POST", "/v1/tool-groups", map[string]any{
		"group_id": "blocked",
		"name":     "Blocked",
		"status":   "disabled",
	})
	if group.Code != http.StatusCreated {
		t.Fatalf("tool group create failed %d body %s", group.Code, group.Body.String())
	}
	for _, manifest := range []map[string]any{
		{
			"tool_id":      "crm.lookup",
			"name":         "CRM lookup",
			"description":  "Lookup CRM records.",
			"input_schema": map[string]any{"type": "object"},
			"risk_level":   "high",
			"executor": map[string]any{
				"type":        "static_tool_host",
				"provider_id": "crm-provider",
				"operation":   "lookup",
			},
		},
		{
			"tool_id":      "billing.charge",
			"name":         "Billing charge",
			"description":  "Charge a customer.",
			"input_schema": map[string]any{"type": "object"},
			"risk_level":   "critical",
			"executor": map[string]any{
				"type":        "static_tool_host",
				"provider_id": "billing-provider",
				"operation":   "charge",
			},
		},
		{
			"tool_id":      "crm.blocked",
			"group_id":     "blocked",
			"name":         "Blocked CRM lookup",
			"description":  "Lookup CRM records through a disabled group.",
			"input_schema": map[string]any{"type": "object"},
			"executor": map[string]any{
				"type":        "static_tool_host",
				"provider_id": "crm-provider",
				"operation":   "blocked_lookup",
			},
		},
		{
			"tool_id":      "orphan.lookup",
			"name":         "Orphan lookup",
			"description":  "References a missing provider.",
			"input_schema": map[string]any{"type": "object"},
			"executor": map[string]any{
				"type":        "static_tool_host",
				"provider_id": "missing-provider",
				"operation":   "lookup",
			},
		},
	} {
		resp := doJSON(handler, "POST", "/v1/tool-manifests", manifest)
		if resp.Code != http.StatusCreated {
			t.Fatalf("tool manifest create failed %d body %s", resp.Code, resp.Body.String())
		}
	}
	traceID := contracts.TraceID("trace_tool_provider_governance")
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	for _, event := range []contracts.TraceEvent{
		{TraceID: traceID, TenantID: "tenant_1", Type: contracts.TraceToolProviderInvoked, Payload: map[string]any{"provider_id": "crm-provider", "tool_id": "crm.lookup"}, CreatedAt: base},
		{TraceID: traceID, TenantID: "tenant_1", Type: contracts.TraceToolProviderCompleted, Payload: map[string]any{"provider_id": "crm-provider", "tool_id": "crm.lookup", "latency_ms": 17}, CreatedAt: base.Add(17 * time.Millisecond)},
		{TraceID: traceID, TenantID: "tenant_1", Type: contracts.TraceToolProviderInvoked, Payload: map[string]any{"provider_id": "billing-provider", "tool_id": "billing.charge"}, CreatedAt: base.Add(20 * time.Millisecond)},
		{TraceID: traceID, TenantID: "tenant_1", Type: contracts.TraceToolProviderFailed, Payload: map[string]any{"provider_id": "billing-provider", "tool_id": "billing.charge", "latency_ms": 29, "error_code": "tool_execution_failed"}, CreatedAt: base.Add(49 * time.Millisecond)},
		{TraceID: traceID, TenantID: "tenant_1", Type: contracts.TraceToolProviderHealthChecked, Payload: map[string]any{"provider_id": "billing-provider", "health_status": "unhealthy", "latency_ms": 3}, CreatedAt: base.Add(52 * time.Millisecond)},
	} {
		if err := appCore.Trace.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	resp := doJSON(handler, "GET", "/v1/tool-provider-governance?trace_id=trace_tool_provider_governance", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("tool provider governance failed %d body %s", resp.Code, resp.Body.String())
	}
	var parsed struct {
		Governance struct {
			Summary struct {
				ProvidersTotal                   int `json:"providers_total"`
				UnhealthyProvidersTotal          int `json:"unhealthy_providers_total"`
				ToolsTotal                       int `json:"tools_total"`
				BlockedToolsTotal                int `json:"blocked_tools_total"`
				MissingProviderToolsTotal        int `json:"missing_provider_tools_total"`
				HighRiskToolsTotal               int `json:"high_risk_tools_total"`
				CriticalRiskToolsTotal           int `json:"critical_risk_tools_total"`
				TraceEventsTotal                 int `json:"trace_events_total"`
				TraceProviderInvocationsTotal    int `json:"trace_provider_invocations_total"`
				TraceProviderFailuresTotal       int `json:"trace_provider_failures_total"`
				TraceProviderHealthChecksTotal   int `json:"trace_provider_health_checks_total"`
				TraceProviderHealthFailuresTotal int `json:"trace_provider_health_failures_total"`
				TraceProviderLatencyMS           int `json:"trace_provider_latency_ms"`
			} `json:"summary"`
			ProviderMatrix []struct {
				ProviderID     string   `json:"provider_id"`
				Runnable       bool     `json:"runnable"`
				BlockedReasons []string `json:"blocked_reasons"`
				ToolsTotal     int      `json:"tools_total"`
				TraceEvidence  *struct {
					EventsTotal int `json:"events_total"`
				} `json:"trace_evidence,omitempty"`
			} `json:"provider_matrix"`
			ToolMatrix []struct {
				ToolID         string   `json:"tool_id"`
				Runnable       bool     `json:"runnable"`
				BlockedReasons []string `json:"blocked_reasons"`
			} `json:"tool_matrix"`
		} `json:"governance"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	summary := parsed.Governance.Summary
	if summary.ProvidersTotal != 2 || summary.UnhealthyProvidersTotal != 1 || summary.ToolsTotal != 4 || summary.BlockedToolsTotal != 3 || summary.MissingProviderToolsTotal != 1 {
		t.Fatalf("unexpected governance summary: %#v body %s", summary, resp.Body.String())
	}
	if summary.HighRiskToolsTotal != 1 || summary.CriticalRiskToolsTotal != 1 {
		t.Fatalf("unexpected risk summary: %#v", summary)
	}
	if summary.TraceEventsTotal != 5 || summary.TraceProviderInvocationsTotal != 2 || summary.TraceProviderFailuresTotal != 1 ||
		summary.TraceProviderHealthChecksTotal != 1 || summary.TraceProviderHealthFailuresTotal != 1 || summary.TraceProviderLatencyMS != 49 {
		t.Fatalf("unexpected trace summary: %#v body %s", summary, resp.Body.String())
	}
	providers := map[string]struct {
		Runnable       bool
		BlockedReasons []string
		ToolsTotal     int
		TraceEvents    int
	}{}
	for _, row := range parsed.Governance.ProviderMatrix {
		traceEvents := 0
		if row.TraceEvidence != nil {
			traceEvents = row.TraceEvidence.EventsTotal
		}
		providers[row.ProviderID] = struct {
			Runnable       bool
			BlockedReasons []string
			ToolsTotal     int
			TraceEvents    int
		}{Runnable: row.Runnable, BlockedReasons: row.BlockedReasons, ToolsTotal: row.ToolsTotal, TraceEvents: traceEvents}
	}
	if providers["crm-provider"].ToolsTotal != 2 || providers["crm-provider"].TraceEvents != 2 || !providers["crm-provider"].Runnable {
		t.Fatalf("unexpected crm provider row: %#v", providers["crm-provider"])
	}
	if providers["billing-provider"].Runnable || !contains(providers["billing-provider"].BlockedReasons, "provider_unhealthy") || providers["billing-provider"].TraceEvents != 3 {
		t.Fatalf("unexpected billing provider row: %#v", providers["billing-provider"])
	}
	tools := map[string][]string{}
	for _, row := range parsed.Governance.ToolMatrix {
		if row.Runnable {
			continue
		}
		tools[row.ToolID] = row.BlockedReasons
	}
	if !contains(tools["billing.charge"], "provider_unhealthy") || !contains(tools["crm.blocked"], "group_status:disabled") || !contains(tools["orphan.lookup"], "provider_missing") {
		t.Fatalf("unexpected tool blocked reasons: %#v", tools)
	}
	filtered := doJSON(handler, "GET", "/v1/tool-provider-governance?trace_id=trace_tool_provider_governance&provider_id=crm-provider", nil)
	if filtered.Code != http.StatusOK || !bytes.Contains(filtered.Body.Bytes(), []byte(`"providers_total":1`)) ||
		!bytes.Contains(filtered.Body.Bytes(), []byte(`"tools_total":2`)) || !bytes.Contains(filtered.Body.Bytes(), []byte(`"trace_events_total":2`)) {
		t.Fatalf("tool provider governance filter failed %d body %s", filtered.Code, filtered.Body.String())
	}
}

func TestRuntimeHookBindingApprovalGateForResourceAndCommand(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	manifest := doJSON(handler, "POST", "/v1/runtime-hook-manifests", map[string]any{
		"hook_id":           "approval-rerank",
		"name":              "Approval rerank",
		"phase":             "before_model_call",
		"status":            "enabled",
		"requires_approval": true,
	})
	if manifest.Code != http.StatusCreated || !bytes.Contains(manifest.Body.Bytes(), []byte(`"requires_approval":true`)) {
		t.Fatalf("runtime hook manifest with approval create failed %d body %s", manifest.Code, manifest.Body.String())
	}
	pending := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":       "approval-rerank",
		"provider_type": "go",
		"phase":         "before_model_call",
		"enabled":       true,
		"trace_id":      "trace_hook_binding_approval",
	})
	if pending.Code != http.StatusOK || !bytes.Contains(pending.Body.Bytes(), []byte(`"status":"approval_required"`)) {
		t.Fatalf("expected runtime hook binding approval request, got %d body %s", pending.Code, pending.Body.String())
	}
	if bindings := doJSON(handler, "GET", "/v1/agents/test-agent/runtime-hooks", nil); bindings.Code != http.StatusOK || bytes.Contains(bindings.Body.Bytes(), []byte("approval-rerank")) {
		t.Fatalf("runtime hook binding should not persist before approval, got %d body %s", bindings.Code, bindings.Body.String())
	}
	var pendingResp struct {
		Approval contracts.ApprovalRequest `json:"approval"`
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &pendingResp); err != nil {
		t.Fatal(err)
	}
	if pendingResp.Approval.ResourceType != "runtime_hook_binding" || pendingResp.Approval.Action != "runtime_hook.binding.upsert" || pendingResp.Approval.TraceID != "trace_hook_binding_approval" {
		t.Fatalf("unexpected runtime hook binding approval request: %#v", pendingResp.Approval)
	}
	approved := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "approval.approve",
		"payload": map[string]any{"approval_id": pendingResp.Approval.ApprovalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if approved.Code != http.StatusOK || !bytes.Contains(approved.Body.Bytes(), []byte(`"status":"approved"`)) {
		t.Fatalf("runtime hook binding approval approve failed %d body %s", approved.Code, approved.Body.String())
	}
	binding := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":       "approval-rerank",
		"provider_type": "go",
		"phase":         "before_model_call",
		"enabled":       true,
		"approval_id":   pendingResp.Approval.ApprovalID,
	})
	if binding.Code != http.StatusOK || !bytes.Contains(binding.Body.Bytes(), []byte("approval-rerank")) {
		t.Fatalf("runtime hook binding with approval failed %d body %s", binding.Code, binding.Body.String())
	}

	policyManifest := doJSON(handler, "POST", "/v1/runtime-hook-manifests", map[string]any{
		"hook_id": "policy-rerank",
		"name":    "Policy rerank",
		"phase":   "before_model_call",
		"status":  "enabled",
		"approval_policy": map[string]any{
			"require_approval": true,
			"provider_types":   []any{"go"},
			"phases":           []any{"before_model_call"},
			"failure_policies": []any{"reject"},
		},
	})
	if policyManifest.Code != http.StatusCreated || !bytes.Contains(policyManifest.Body.Bytes(), []byte(`"approval_policy"`)) {
		t.Fatalf("runtime hook manifest approval policy create failed %d body %s", policyManifest.Code, policyManifest.Body.String())
	}
	policyNonMatch := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":        "policy-rerank",
		"provider_type":  "go",
		"phase":          "before_model_call",
		"enabled":        true,
		"failure_policy": "ignore",
	})
	if policyNonMatch.Code != http.StatusOK || bytes.Contains(policyNonMatch.Body.Bytes(), []byte(`"approval_required"`)) {
		t.Fatalf("runtime hook approval policy should not match ignore failure policy, got %d body %s", policyNonMatch.Code, policyNonMatch.Body.String())
	}
	policyMatch := doJSON(handler, "PUT", "/v1/agents/test-agent/runtime-hooks", map[string]any{
		"hook_id":        "policy-rerank",
		"provider_type":  "go",
		"phase":          "before_model_call",
		"enabled":        true,
		"failure_policy": "reject",
		"trace_id":       "trace_hook_binding_policy_approval",
	})
	if policyMatch.Code != http.StatusOK || !bytes.Contains(policyMatch.Body.Bytes(), []byte(`"status":"approval_required"`)) || !bytes.Contains(policyMatch.Body.Bytes(), []byte("approval policy")) {
		t.Fatalf("runtime hook approval policy should require approval, got %d body %s", policyMatch.Code, policyMatch.Body.String())
	}
	pendingApprovals := doJSON(handler, "GET", "/v1/runtime-hook-approvals?status=pending&trace_id=trace_hook_binding_policy_approval&hook_id=policy-rerank", nil)
	if pendingApprovals.Code != http.StatusOK || !bytes.Contains(pendingApprovals.Body.Bytes(), []byte("policy-rerank")) || !bytes.Contains(pendingApprovals.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("runtime hook pending approval list failed %d body %s", pendingApprovals.Code, pendingApprovals.Body.String())
	}
	usedApprovals := doJSON(handler, "GET", "/v1/runtime-hook-approvals?status=used&hook_id=approval-rerank", nil)
	if usedApprovals.Code != http.StatusOK || !bytes.Contains(usedApprovals.Body.Bytes(), []byte(pendingResp.Approval.ApprovalID)) || !bytes.Contains(usedApprovals.Body.Bytes(), []byte(`"status":"used"`)) {
		t.Fatalf("runtime hook used approval list failed %d body %s", usedApprovals.Code, usedApprovals.Body.String())
	}

	commandPending := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command":  "runtime_hook.binding.upsert",
		"trace_id": "trace_hook_binding_command_approval",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"agent_id":          "test-agent",
			"hook_id":           "cmd-approval-rerank",
			"provider_type":     "go",
			"phase":             "before_model_call",
			"enabled":           true,
			"requires_approval": true,
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if commandPending.Code != http.StatusOK || !bytes.Contains(commandPending.Body.Bytes(), []byte(`"status":"approval_required"`)) {
		t.Fatalf("expected command runtime hook binding approval request, got %d body %s", commandPending.Code, commandPending.Body.String())
	}
	var commandPendingResp struct {
		Approval contracts.ApprovalRequest `json:"approval"`
	}
	if err := json.Unmarshal(commandPending.Body.Bytes(), &commandPendingResp); err != nil {
		t.Fatal(err)
	}
	if commandPendingResp.Approval.ResourceType != "runtime_hook_binding" || commandPendingResp.Approval.Action != "runtime_hook.binding.upsert" || commandPendingResp.Approval.TraceID != "trace_hook_binding_command_approval" {
		t.Fatalf("unexpected command runtime hook binding approval request: %#v", commandPendingResp.Approval)
	}
	commandApproved := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "approval.approve",
		"payload": map[string]any{"approval_id": commandPendingResp.Approval.ApprovalID},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if commandApproved.Code != http.StatusOK || !bytes.Contains(commandApproved.Body.Bytes(), []byte(`"status":"approved"`)) {
		t.Fatalf("command runtime hook binding approval approve failed %d body %s", commandApproved.Code, commandApproved.Body.String())
	}
	commandBinding := doJSONWithHeaders(handler, "POST", "/v1/commands", map[string]any{
		"command": "runtime_hook.binding.upsert",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{
			"agent_id":          "test-agent",
			"hook_id":           "cmd-approval-rerank",
			"provider_type":     "go",
			"phase":             "before_model_call",
			"enabled":           true,
			"requires_approval": true,
			"approval_id":       commandPendingResp.Approval.ApprovalID,
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}, map[string]string{"X-Roles": "optimizer"})
	if commandBinding.Code != http.StatusOK || !bytes.Contains(commandBinding.Body.Bytes(), []byte("cmd-approval-rerank")) {
		t.Fatalf("command runtime hook binding with approval failed %d body %s", commandBinding.Code, commandBinding.Body.String())
	}
}

func TestTaskStartResourceAPI(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	resp := doJSON(handler, "POST", "/v1/tasks/start", map[string]any{
		"agent_id":  "test-agent",
		"title":     "resource task",
		"objective": "start through resource api",
	})
	if resp.Code != http.StatusCreated || !bytes.Contains(resp.Body.Bytes(), []byte("resource task")) {
		t.Fatalf("task start resource failed %d body %s", resp.Code, resp.Body.String())
	}
}

func TestAgentDraftSubresourceAPIs(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	reviewAgent := loader.TestAgentDefinition()
	reviewAgent.TenantID = "tenant_1"
	reviewAgent.AgentID = "review-agent"
	appCore.AgentRegistry.Put(reviewAgent)
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v2", agentpackage.AgentPackageSource{Prompt: "base prompt"}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	prompt := doJSON(handler, "PUT", "/v1/agents/test-agent/prompt-profile", map[string]any{
		"draft_id":         draft.DraftID,
		"prompt":           "updated prompt",
		"system_prompt":    "updated system",
		"developer_prompt": "updated developer",
	})
	if prompt.Code != http.StatusOK || !bytes.Contains(prompt.Body.Bytes(), []byte("updated prompt")) {
		t.Fatalf("prompt profile update failed %d body %s", prompt.Code, prompt.Body.String())
	}
	promptDraft := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile?draft_id="+draft.DraftID, nil)
	if promptDraft.Code != http.StatusOK || !bytes.Contains(promptDraft.Body.Bytes(), []byte("updated developer")) || !bytes.Contains(promptDraft.Body.Bytes(), []byte(`"draft_id"`)) {
		t.Fatalf("prompt profile draft get failed %d body %s", promptDraft.Code, promptDraft.Body.String())
	}
	if preview := doJSON(handler, "POST", "/v1/agents/test-agent/prompt-profile/preview", map[string]any{"draft_id": draft.DraftID, "input": "hello"}); preview.Code != http.StatusOK || !bytes.Contains(preview.Body.Bytes(), []byte("prompt_bundle")) {
		t.Fatalf("prompt profile preview failed %d body %s", preview.Code, preview.Body.String())
	}
	bindings := doJSON(handler, "PUT", "/v1/agents/test-agent/tool-bindings", map[string]any{
		"draft_id": draft.DraftID,
		"tool_bindings": map[string]any{
			"allowed_tool_ids":       []any{"echo"},
			"allowed_tool_group_ids": []any{"crm"},
			"exposed_tool_ids":       []any{"echo"},
		},
	})
	if bindings.Code != http.StatusOK || !bytes.Contains(bindings.Body.Bytes(), []byte("crm")) {
		t.Fatalf("tool bindings update failed %d body %s", bindings.Code, bindings.Body.String())
	}
	bindingsDraft := doJSON(handler, "GET", "/v1/agents/test-agent/tool-bindings?draft_id="+draft.DraftID, nil)
	if bindingsDraft.Code != http.StatusOK || !bytes.Contains(bindingsDraft.Body.Bytes(), []byte("crm")) {
		t.Fatalf("tool bindings draft get failed %d body %s", bindingsDraft.Code, bindingsDraft.Body.String())
	}
	collaborator := doJSON(handler, "PUT", "/v1/agents/test-agent/collaborators/review-agent", map[string]any{
		"draft_id":              draft.DraftID,
		"name":                  "Review Agent",
		"when_to_use":           []any{"review"},
		"default_handoff_mode":  "hybrid",
		"max_context_tokens":    300,
		"requires_approval":     true,
		"allowed_handoff_modes": []any{"hybrid"},
	})
	if collaborator.Code != http.StatusOK || !bytes.Contains(collaborator.Body.Bytes(), []byte("review-agent")) || !bytes.Contains(collaborator.Body.Bytes(), []byte("max_context_tokens")) {
		t.Fatalf("collaborator upsert failed %d body %s", collaborator.Code, collaborator.Body.String())
	}
	exportedTool := doJSON(handler, "PUT", "/v1/agents/test-agent/exported-tools/customer.lookup", map[string]any{
		"draft_id":     draft.DraftID,
		"name":         "Customer lookup",
		"description":  "Look up customer context.",
		"when_to_use":  []any{"customer lookup"},
		"input_schema": map[string]any{"type": "object"},
		"risk_level":   "low",
		"visibility":   "protected",
	})
	if exportedTool.Code != http.StatusOK || !bytes.Contains(exportedTool.Body.Bytes(), []byte("customer.lookup")) {
		t.Fatalf("exported tool upsert failed %d body %s", exportedTool.Code, exportedTool.Body.String())
	}
	if manifest := doJSON(handler, "GET", "/v1/tool-manifests/customer.lookup", nil); manifest.Code != http.StatusOK || !bytes.Contains(manifest.Body.Bytes(), []byte("agent_tool")) {
		t.Fatalf("exported tool manifest sync failed %d body %s", manifest.Code, manifest.Body.String())
	}
	skill := doJSON(handler, "PUT", "/v1/agents/test-agent/skills/customer-summary", map[string]any{
		"draft_id":                  draft.DraftID,
		"version":                   "v1",
		"name":                      "Customer summary",
		"instruction":               "Summarize customer context.",
		"risk_level":                "low",
		"when_to_use":               []any{"customer summary"},
		"recommended_tools":         []any{"artifact.read"},
		"allowed_tools":             []any{"echo"},
		"recommended_memory_reads":  []any{"customer_profile"},
		"recommended_memory_writes": []any{"customer_summary"},
		"recommended_handoffs":      []any{"review-agent"},
		"completion_criteria":       []any{"summary reviewed"},
		"output_schema":             map[string]any{"type": "object"},
	})
	if skill.Code != http.StatusOK || !bytes.Contains(skill.Body.Bytes(), []byte("customer-summary")) || !bytes.Contains(skill.Body.Bytes(), []byte("recommended_tools")) || !bytes.Contains(skill.Body.Bytes(), []byte("output_schema")) {
		t.Fatalf("skill upsert failed %d body %s", skill.Code, skill.Body.String())
	}
	skillsDraft := doJSON(handler, "GET", "/v1/agents/test-agent/skills?draft_id="+draft.DraftID, nil)
	if skillsDraft.Code != http.StatusOK || !bytes.Contains(skillsDraft.Body.Bytes(), []byte("customer-summary")) {
		t.Fatalf("skills draft list failed %d body %s", skillsDraft.Code, skillsDraft.Body.String())
	}
	skillDraft := doJSON(handler, "GET", "/v1/agents/test-agent/skills/customer-summary?draft_id="+draft.DraftID, nil)
	if skillDraft.Code != http.StatusOK || !bytes.Contains(skillDraft.Body.Bytes(), []byte("summary reviewed")) {
		t.Fatalf("skill draft get failed %d body %s", skillDraft.Code, skillDraft.Body.String())
	}
	collaboratorsDraft := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators?draft_id="+draft.DraftID, nil)
	if collaboratorsDraft.Code != http.StatusOK || !bytes.Contains(collaboratorsDraft.Body.Bytes(), []byte("review-agent")) {
		t.Fatalf("collaborators draft list failed %d body %s", collaboratorsDraft.Code, collaboratorsDraft.Body.String())
	}
	collaboratorDraft := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators/review-agent?draft_id="+draft.DraftID, nil)
	if collaboratorDraft.Code != http.StatusOK || !bytes.Contains(collaboratorDraft.Body.Bytes(), []byte("max_context_tokens")) {
		t.Fatalf("collaborator draft get failed %d body %s", collaboratorDraft.Code, collaboratorDraft.Body.String())
	}
	exportedToolsDraft := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools?draft_id="+draft.DraftID, nil)
	if exportedToolsDraft.Code != http.StatusOK || !bytes.Contains(exportedToolsDraft.Body.Bytes(), []byte("customer.lookup")) {
		t.Fatalf("exported tools draft list failed %d body %s", exportedToolsDraft.Code, exportedToolsDraft.Body.String())
	}
	exportedToolDraft := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools/customer.lookup?draft_id="+draft.DraftID, nil)
	if exportedToolDraft.Code != http.StatusOK || !bytes.Contains(exportedToolDraft.Body.Bytes(), []byte("Customer lookup")) {
		t.Fatalf("exported tool draft get failed %d body %s", exportedToolDraft.Code, exportedToolDraft.Body.String())
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/skills/customer-summary?draft_id="+draft.DraftID+"&version=v1", nil)
	if remove.Code != http.StatusOK || bytes.Contains(remove.Body.Bytes(), []byte("customer-summary")) {
		t.Fatalf("skill delete failed %d body %s", remove.Code, remove.Body.String())
	}
	removeCollaborator := doJSON(handler, "DELETE", "/v1/agents/test-agent/collaborators/review-agent?draft_id="+draft.DraftID, nil)
	if removeCollaborator.Code != http.StatusOK || bytes.Contains(removeCollaborator.Body.Bytes(), []byte("review-agent")) {
		t.Fatalf("collaborator delete failed %d body %s", removeCollaborator.Code, removeCollaborator.Body.String())
	}
	removeExportedTool := doJSON(handler, "DELETE", "/v1/agents/test-agent/exported-tools/customer.lookup?draft_id="+draft.DraftID, nil)
	if removeExportedTool.Code != http.StatusOK || bytes.Contains(removeExportedTool.Body.Bytes(), []byte("customer.lookup")) {
		t.Fatalf("exported tool delete failed %d body %s", removeExportedTool.Code, removeExportedTool.Body.String())
	}
}

func TestAgentSubresourceGETPrefersProjectionStore(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	store := &projectionPackageStore{
		profile: agentpackage.PromptProfileProjection{
			TenantID:         "tenant_1",
			AgentID:          "test-agent",
			Version:          "v9",
			PackageVersionID: "pkg_projection",
			IdentityPrompt:   "projection prompt",
			DeveloperPrompt:  "projection developer",
			Status:           contracts.ReleaseStable,
		},
		binding: agentpackage.ToolBindingProjection{
			TenantID: "tenant_1",
			AgentID:  "test-agent",
			Version:  "v9",
			Status:   contracts.ReleaseStable,
			Bindings: contracts.AgentToolsConfig{AllowedToolIDs: []string{"projection.tool"}},
		},
		skills: []agentpackage.SkillDefinitionProjection{{
			TenantID: "tenant_1",
			AgentID:  "test-agent",
			Version:  "v9",
			Status:   contracts.ReleaseStable,
			SkillID:  "projection-skill",
			Definition: contracts.SkillDefinition{Card: contracts.SkillCard{
				SkillID: "projection-skill",
				Name:    "Projection Skill",
				Version: "v1",
			}},
		}},
		collaborators: []agentpackage.CollaboratorProjection{{
			TenantID:            "tenant_1",
			AgentID:             "test-agent",
			Version:             "v9",
			Status:              contracts.ReleaseStable,
			CollaboratorAgentID: "projection-agent",
			Collaborator: contracts.AgentCollaboratorRef{
				AgentID: "projection-agent",
				Name:    "Projection Agent",
			},
		}},
		exportedTools: []agentpackage.ExportedToolProjection{{
			TenantID: "tenant_1",
			AgentID:  "test-agent",
			Version:  "v9",
			Status:   contracts.ReleaseStable,
			ToolID:   "projection.tool",
			Tool: contracts.AgentExportedTool{
				ToolID:      "projection.tool",
				Name:        "Projection Tool",
				Description: "Projection exported tool.",
				InputSchema: map[string]any{"type": "object"},
				RiskLevel:   contracts.RiskLow,
				Visibility:  contracts.ToolProtected,
			},
		}},
	}
	appCore.Packages = agentpackage.NewServiceWithStore(nil, store)
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	prompt := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile?agent_version=v9", nil)
	if prompt.Code != http.StatusOK || !bytes.Contains(prompt.Body.Bytes(), []byte("projection prompt")) {
		t.Fatalf("expected prompt profile projection, got %d body %s", prompt.Code, prompt.Body.String())
	}
	bindings := doJSON(handler, "GET", "/v1/agents/test-agent/tool-bindings?agent_version=v9", nil)
	if bindings.Code != http.StatusOK || !bytes.Contains(bindings.Body.Bytes(), []byte("projection.tool")) {
		t.Fatalf("expected tool binding projection, got %d body %s", bindings.Code, bindings.Body.String())
	}
	skills := doJSON(handler, "GET", "/v1/agents/test-agent/skills?agent_version=v9", nil)
	if skills.Code != http.StatusOK || !bytes.Contains(skills.Body.Bytes(), []byte("projection-skill")) {
		t.Fatalf("expected skill projection, got %d body %s", skills.Code, skills.Body.String())
	}
	skill := doJSON(handler, "GET", "/v1/agents/test-agent/skills/projection-skill?agent_version=v9", nil)
	if skill.Code != http.StatusOK || !bytes.Contains(skill.Body.Bytes(), []byte("Projection Skill")) {
		t.Fatalf("expected single skill projection, got %d body %s", skill.Code, skill.Body.String())
	}
	collaborators := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators?agent_version=v9", nil)
	if collaborators.Code != http.StatusOK || !bytes.Contains(collaborators.Body.Bytes(), []byte("projection-agent")) {
		t.Fatalf("expected collaborator projection, got %d body %s", collaborators.Code, collaborators.Body.String())
	}
	collaborator := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators/projection-agent?agent_version=v9", nil)
	if collaborator.Code != http.StatusOK || !bytes.Contains(collaborator.Body.Bytes(), []byte("Projection Agent")) {
		t.Fatalf("expected single collaborator projection, got %d body %s", collaborator.Code, collaborator.Body.String())
	}
	exportedTools := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools?agent_version=v9", nil)
	if exportedTools.Code != http.StatusOK || !bytes.Contains(exportedTools.Body.Bytes(), []byte("projection.tool")) {
		t.Fatalf("expected exported tool projection, got %d body %s", exportedTools.Code, exportedTools.Body.String())
	}
	exportedTool := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools/projection.tool?agent_version=v9", nil)
	if exportedTool.Code != http.StatusOK || !bytes.Contains(exportedTool.Body.Bytes(), []byte("Projection Tool")) {
		t.Fatalf("expected single exported tool projection, got %d body %s", exportedTool.Code, exportedTool.Body.String())
	}
}

func TestAgentSubresourceGovernanceViewsIncludeStandaloneDraftAndRelease(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := agentpackage.AgentPackageSource{
		Prompt: "governance package prompt",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs:      []string{"echo"},
			AllowedToolGroupIDs: []string{"core-tools"},
			DeniedToolIDs:       []string{"danger.delete"},
			ExposedToolIDs:      []string{"echo"},
		},
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID:          "review-agent",
			Version:          "v1",
			Name:             "Review Agent",
			RequiresApproval: true,
		}},
		Exports: contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
			ToolID:      "customer.lookup",
			Operation:   "lookup",
			Name:        "Customer lookup",
			Description: "Lookup a customer record.",
			InputSchema: map[string]any{"type": "object"},
			RiskLevel:   contracts.RiskHigh,
			Visibility:  contracts.ToolProtected,
			Version:     "v1",
		}}},
		Metadata: map[string]any{
			"skill_definitions": []any{map[string]any{
				"skill_id":             "governance-skill",
				"version":              "v1",
				"name":                 "Governance Skill",
				"description":          "Exercise governance views.",
				"instruction":          "Use governance evidence.",
				"risk_level":           "medium",
				"when_to_use":          []any{"governance"},
				"recommended_tools":    []any{"artifact.read"},
				"allowed_tools":        []any{"echo"},
				"recommended_handoffs": []any{"review-agent"},
			}},
		},
	}
	draft, err := appCore.Packages.CreateDraft(ctx, "tenant_1", "test-agent", "v20", source, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(ctx, "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(ctx, "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)
	if _, err := appCore.Packages.MarkEvalResult(ctx, release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(ctx, release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.EnsureAgentAssetVersionForTenant(ctx, "tenant_1", "test-agent", "v20", "tester"); err != nil {
		t.Fatal(err)
	}
	if err := appCore.AgentRegistry.SetDefaultForTenant("tenant_1", "test-agent", "v20"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.UpsertPromptProfileProjection(ctx, "tenant_1", "test-agent", "v20", "standalone governance prompt", "", "", "", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.UpsertToolBindingProjection(ctx, "tenant_1", "test-agent", "v20", contracts.AgentToolsConfig{AllowedToolIDs: []string{"standalone.tool"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.UpsertSkillDefinitionProjection(ctx, "tenant_1", "test-agent", "v20", contracts.SkillDefinition{
		Card: contracts.SkillCard{
			SkillID:   "governance-skill",
			Version:   "v2",
			Name:      "Standalone Governance Skill",
			RiskLevel: contracts.RiskMedium,
		},
		Instruction:            contracts.SkillInstruction{SkillID: "governance-skill", Content: "Prefer standalone guidance."},
		RecommendedTools:       []string{"standalone.tool"},
		AllowedTools:           []string{"standalone.tool"},
		RecommendedHandoffs:    []string{"review-agent"},
		CompletionCriteria:     []string{"evidence present"},
		OutputSchema:           map[string]any{"type": "object"},
		RecommendedMemoryReads: []string{"governance_notes"},
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.UpsertCollaboratorProjection(ctx, "tenant_1", "test-agent", "v20", contracts.AgentCollaboratorRef{
		AgentID:          "review-agent",
		Version:          "v2",
		Name:             "Standalone Reviewer",
		RequiresApproval: true,
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.UpsertExportedToolProjection(ctx, "tenant_1", "test-agent", "v20", contracts.AgentExportedTool{
		ToolID:      "customer.lookup",
		Operation:   "lookup",
		Name:        "Standalone customer lookup",
		Description: "Standalone lookup.",
		InputSchema: map[string]any{"type": "object"},
		RiskLevel:   contracts.RiskCritical,
		Visibility:  contracts.ToolProtected,
		Version:     "v2",
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	assertGovernance := func(path string, expected ...string) {
		t.Helper()
		resp := doJSON(handler, http.MethodGet, path, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("governance %s failed %d body %s", path, resp.Code, resp.Body.String())
		}
		for _, text := range expected {
			if !bytes.Contains(resp.Body.Bytes(), []byte(text)) {
				t.Fatalf("governance %s missing %q body %s", path, text, resp.Body.String())
			}
		}
	}
	assertGovernance("/v1/agents/test-agent/prompt-profile/governance",
		`"resource_type":"prompt_profile"`,
		`"source_kind":"profile"`,
		`"source_kind":"draft"`,
		`"source_kind":"release"`,
		`"standalone_active":1`,
		`"active_version":"v20"`,
		"standalone governance prompt",
	)
	assertGovernance("/v1/agents/test-agent/tool-bindings/governance",
		`"resource_type":"tool_binding"`,
		`"allowed_tool_count":1`,
		`"allowed_group_count":1`,
		`"source_kind":"tool_binding"`,
	)
	assertGovernance("/v1/agents/test-agent/skills/governance?skill_id=governance-skill",
		`"resource_type":"skill"`,
		`"resource_id":"governance-skill"`,
		`"source_kind":"skill"`,
		`"recommended_tool_count":1`,
		`"recommended_handoff_count":1`,
	)
	assertGovernance("/v1/agents/test-agent/skills/governance-skill/governance",
		`"resource_id":"governance-skill"`,
		`"resource_version":"v2"`,
	)
	assertGovernance("/v1/agents/test-agent/collaborators/review-agent/governance",
		`"resource_type":"collaborator"`,
		`"resource_id":"review-agent"`,
		`"requires_approval":true`,
	)
	assertGovernance("/v1/agents/test-agent/exported-tools/customer.lookup/governance",
		`"resource_type":"exported_tool"`,
		`"resource_id":"customer.lookup"`,
		`"risk_level":"critical"`,
		`"visibility":"protected"`,
	)
}

func TestAgentDraftLifecycleResourceAPIs(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSON(handler, "POST", "/v1/agents/test-agent/drafts", map[string]any{
		"version": "v3",
		"prompt":  "draft rest prompt",
		"tool_bindings": map[string]any{
			"allowed_tool_ids": []any{"echo"},
		},
	})
	if create.Code != http.StatusCreated || !bytes.Contains(create.Body.Bytes(), []byte("draft rest prompt")) {
		t.Fatalf("agent draft create failed %d body %s", create.Code, create.Body.String())
	}
	var created struct {
		Draft agentpackage.Draft `json:"draft"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	list := doJSON(handler, "GET", "/v1/agents/test-agent/drafts", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(created.Draft.DraftID)) {
		t.Fatalf("agent draft list failed %d body %s", list.Code, list.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/test-agent/drafts/"+created.Draft.DraftID, nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("v3")) {
		t.Fatalf("agent draft get failed %d body %s", get.Code, get.Body.String())
	}
	validate := doJSON(handler, "POST", "/v1/agents/test-agent/drafts/"+created.Draft.DraftID+"/validate", nil)
	if validate.Code != http.StatusOK || !bytes.Contains(validate.Body.Bytes(), []byte("validated")) {
		t.Fatalf("agent draft validate failed %d body %s", validate.Code, validate.Body.String())
	}
	review := doJSON(handler, "POST", "/v1/agents/test-agent/drafts/"+created.Draft.DraftID+"/review", nil)
	if review.Code != http.StatusOK || !bytes.Contains(review.Body.Bytes(), []byte("reviewed")) {
		t.Fatalf("agent draft review failed %d body %s", review.Code, review.Body.String())
	}
	publish := doJSON(handler, "POST", "/v1/agents/test-agent/drafts/"+created.Draft.DraftID+"/publish", nil)
	if publish.Code != http.StatusOK || !bytes.Contains(publish.Body.Bytes(), []byte(`"status":"published"`)) {
		t.Fatalf("agent draft publish failed %d body %s", publish.Code, publish.Body.String())
	}
	versions := doJSON(handler, "GET", "/v1/agents/test-agent/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte(`"v3"`)) {
		t.Fatalf("expected published draft in version list, got %d body %s", versions.Code, versions.Body.String())
	}
	prompt := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile?agent_version=v3", nil)
	if prompt.Code != http.StatusOK || !bytes.Contains(prompt.Body.Bytes(), []byte("draft rest prompt")) {
		t.Fatalf("expected published draft to register agent definition, got %d body %s", prompt.Code, prompt.Body.String())
	}
}

func TestAgentVersionResourceAPIActivatesStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v2", agentpackage.AgentPackageSource{Prompt: "version api prompt"}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/versions/v2/activate", nil)
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	list := doJSON(handler, "GET", "/v1/agents/test-agent/versions", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"v2"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"stable"`)) {
		t.Fatalf("agent version list failed %d body %s", list.Code, list.Body.String())
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/versions/v2/activate", nil)
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v2"`)) || !bytes.Contains(activate.Body.Bytes(), []byte(`"default_version":"v2"`)) {
		t.Fatalf("agent version activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v2" {
		t.Fatalf("expected registry default v2, got %s", got)
	}
	prompt := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile", nil)
	if prompt.Code != http.StatusOK || !bytes.Contains(prompt.Body.Bytes(), []byte("version api prompt")) {
		t.Fatalf("expected activated prompt profile by default, got %d body %s", prompt.Code, prompt.Body.String())
	}
}

func TestPromptProfileVersionResourceAPIsActivateStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v4", agentpackage.AgentPackageSource{
		Prompt: "prompt profile resource prompt",
		Metadata: map[string]any{
			"system_prompt":    "prompt profile resource system",
			"developer_prompt": "prompt profile resource developer",
		},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)

	versions := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte("prompt profile resource prompt")) || !bytes.Contains(versions.Body.Bytes(), []byte(`"v4"`)) {
		t.Fatalf("prompt profile versions failed %d body %s", versions.Code, versions.Body.String())
	}
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/prompt-profile/activate", map[string]any{"agent_version": "v4"})
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable prompt profile activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/prompt-profile/activate", map[string]any{"version": "v4"})
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v4"`)) || !bytes.Contains(activate.Body.Bytes(), []byte("prompt profile resource developer")) {
		t.Fatalf("prompt profile activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v4" {
		t.Fatalf("expected registry default v4, got %s", got)
	}
	prompt := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile", nil)
	if prompt.Code != http.StatusOK || !bytes.Contains(prompt.Body.Bytes(), []byte("prompt profile resource prompt")) {
		t.Fatalf("expected activated prompt profile by default, got %d body %s", prompt.Code, prompt.Body.String())
	}
}

func TestPromptProfileStandaloneCRUDOverlaysRuntimeLoader(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	upsert := doJSON(handler, "POST", "/v1/agents/test-agent/prompt-profile", map[string]any{
		"identity_prompt":  "standalone identity",
		"system_prompt":    "standalone system",
		"developer_prompt": "standalone developer",
	})
	if upsert.Code != http.StatusOK || !bytes.Contains(upsert.Body.Bytes(), []byte(`"source_kind":"profile"`)) || !bytes.Contains(upsert.Body.Bytes(), []byte("standalone developer")) {
		t.Fatalf("standalone prompt profile upsert failed %d body %s", upsert.Code, upsert.Body.String())
	}
	profile := doJSON(handler, "GET", "/v1/agents/test-agent/prompt-profile", nil)
	if profile.Code != http.StatusOK || !bytes.Contains(profile.Body.Bytes(), []byte("standalone identity")) || !bytes.Contains(profile.Body.Bytes(), []byte(`"source_id":"test-agent"`)) {
		t.Fatalf("standalone prompt profile get failed %d body %s", profile.Code, profile.Body.String())
	}
	preview := doJSON(handler, "POST", "/v1/agents/test-agent/prompt-profile/preview", map[string]any{"input": "hello"})
	if preview.Code != http.StatusOK || !bytes.Contains(preview.Body.Bytes(), []byte("standalone developer")) {
		t.Fatalf("standalone prompt profile preview did not use runtime loader overlay %d body %s", preview.Code, preview.Body.String())
	}
	loaded, err := appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IdentityPrompt != "standalone identity" || loaded.DeveloperPrompt != "standalone developer" {
		t.Fatalf("expected runtime loader overlay, got identity %q developer %q", loaded.IdentityPrompt, loaded.DeveloperPrompt)
	}

	patch := doJSON(handler, "PATCH", "/v1/agents/test-agent/prompt-profile", map[string]any{
		"system_prompt": "standalone system patched",
	})
	if patch.Code != http.StatusOK || !bytes.Contains(patch.Body.Bytes(), []byte("standalone identity")) || !bytes.Contains(patch.Body.Bytes(), []byte("standalone system patched")) {
		t.Fatalf("standalone prompt profile patch failed %d body %s", patch.Code, patch.Body.String())
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/prompt-profile", nil)
	if remove.Code != http.StatusOK || !bytes.Contains(remove.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("standalone prompt profile delete failed %d body %s", remove.Code, remove.Body.String())
	}
	loaded, err = appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IdentityPrompt == "standalone identity" || loaded.DeveloperPrompt == "standalone developer" {
		t.Fatalf("expected runtime loader to fall back after delete, got identity %q developer %q", loaded.IdentityPrompt, loaded.DeveloperPrompt)
	}
}

func TestSkillStandaloneCRUDOverlaysRuntimeLoader(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	upsert := doJSON(handler, "POST", "/v1/agents/test-agent/skills", map[string]any{
		"skill_id":          "standalone-skill",
		"version":           "v1",
		"name":              "Standalone Skill",
		"description":       "Use independent skill resources.",
		"instruction":       "Prefer the standalone skill path.",
		"risk_level":        "low",
		"when_to_use":       []any{"standalone skill"},
		"recommended_tools": []any{"echo"},
		"allowed_tools":     []any{"echo"},
	})
	if upsert.Code != http.StatusOK || !bytes.Contains(upsert.Body.Bytes(), []byte("standalone-skill")) || !bytes.Contains(upsert.Body.Bytes(), []byte("recommended_tools")) {
		t.Fatalf("standalone skill upsert failed %d body %s", upsert.Code, upsert.Body.String())
	}
	list := doJSON(handler, "GET", "/v1/agents/test-agent/skills", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("Standalone Skill")) {
		t.Fatalf("standalone skill list failed %d body %s", list.Code, list.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/test-agent/skills/standalone-skill", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("Prefer the standalone skill path.")) {
		t.Fatalf("standalone skill get failed %d body %s", get.Code, get.Body.String())
	}
	loaded, err := appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SkillDefinitions) != 1 || loaded.SkillDefinitions[0].Card.SkillID != "standalone-skill" || len(loaded.Skills) != 1 {
		t.Fatalf("expected runtime loader standalone skill overlay, got defs %#v refs %#v", loaded.SkillDefinitions, loaded.Skills)
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/skills/standalone-skill", nil)
	if remove.Code != http.StatusOK || !bytes.Contains(remove.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("standalone skill delete failed %d body %s", remove.Code, remove.Body.String())
	}
	loaded, err = appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SkillDefinitions) != 0 {
		t.Fatalf("expected runtime loader to drop standalone skill after delete, got %#v", loaded.SkillDefinitions)
	}
}

func TestToolBindingStandaloneCRUDOverlaysRuntimeLoader(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	upsert := doJSON(handler, "PUT", "/v1/agents/test-agent/tool-bindings", map[string]any{
		"tool_bindings": map[string]any{
			"allowed_tool_ids":       []any{"echo"},
			"allowed_tool_group_ids": []any{"standalone.group"},
			"denied_tool_ids":        []any{"origin.agent.delegate"},
			"exposed_tool_ids":       []any{"echo"},
		},
	})
	if upsert.Code != http.StatusOK || !bytes.Contains(upsert.Body.Bytes(), []byte("standalone.group")) || !bytes.Contains(upsert.Body.Bytes(), []byte("origin.agent.delegate")) {
		t.Fatalf("standalone tool binding upsert failed %d body %s", upsert.Code, upsert.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/test-agent/tool-bindings", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("standalone.group")) {
		t.Fatalf("standalone tool binding get failed %d body %s", get.Code, get.Body.String())
	}
	loaded, err := appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tools.AllowedToolIDs) != 1 || loaded.Tools.AllowedToolIDs[0] != "echo" || len(loaded.Tools.DeniedToolIDs) != 1 {
		t.Fatalf("expected runtime loader standalone tool binding overlay, got %#v", loaded.Tools)
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/tool-bindings", nil)
	if remove.Code != http.StatusOK || !bytes.Contains(remove.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("standalone tool binding delete failed %d body %s", remove.Code, remove.Body.String())
	}
	loaded, err = appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tools.DeniedToolIDs) != 0 || len(loaded.Tools.AllowedToolIDs) < 2 {
		t.Fatalf("expected runtime loader to fall back after tool binding delete, got %#v", loaded.Tools)
	}
}

func TestCollaboratorStandaloneCRUDOverlaysRuntimeLoader(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	reviewAgent := loader.TestAgentDefinition()
	reviewAgent.TenantID = "tenant_1"
	reviewAgent.AgentID = "review-agent"
	reviewAgent.Name = "Review Agent"
	appCore.AgentRegistry.Put(reviewAgent)
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	upsert := doJSON(handler, "POST", "/v1/agents/test-agent/collaborators/review-agent", map[string]any{
		"name":                  "Review Agent",
		"description":           "Reviews standalone collaborator work.",
		"when_to_use":           []any{"review standalone work"},
		"capabilities":          []any{"review"},
		"default_handoff_mode":  "hybrid",
		"allowed_handoff_modes": []any{"hybrid"},
		"max_context_tokens":    512,
		"requires_approval":     true,
	})
	if upsert.Code != http.StatusOK || !bytes.Contains(upsert.Body.Bytes(), []byte("review-agent")) || !bytes.Contains(upsert.Body.Bytes(), []byte("max_context_tokens")) {
		t.Fatalf("standalone collaborator upsert failed %d body %s", upsert.Code, upsert.Body.String())
	}
	list := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("review standalone work")) {
		t.Fatalf("standalone collaborator list failed %d body %s", list.Code, list.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators/review-agent", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("requires_approval")) {
		t.Fatalf("standalone collaborator get failed %d body %s", get.Code, get.Body.String())
	}
	loaded, err := appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Collaborators) != 1 || loaded.Collaborators[0].AgentID != "review-agent" || !loaded.Collaborators[0].RequiresApproval {
		t.Fatalf("expected runtime loader standalone collaborator overlay, got %#v", loaded.Collaborators)
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/collaborators/review-agent", nil)
	if remove.Code != http.StatusOK || !bytes.Contains(remove.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("standalone collaborator delete failed %d body %s", remove.Code, remove.Body.String())
	}
	loaded, err = appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Collaborators) != 0 {
		t.Fatalf("expected runtime loader to drop standalone collaborator after delete, got %#v", loaded.Collaborators)
	}
}

func TestExportedToolStandaloneCRUDOverlaysRuntimeLoaderAndManifest(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))

	upsert := doJSON(handler, "POST", "/v1/agents/test-agent/exported-tools/customer.lookup", map[string]any{
		"name":          "Customer lookup",
		"description":   "Look up standalone customer context.",
		"when_to_use":   []any{"customer lookup"},
		"input_schema":  map[string]any{"type": "object"},
		"output_schema": map[string]any{"type": "object"},
		"risk_level":    "low",
		"visibility":    "protected",
	})
	if upsert.Code != http.StatusOK || !bytes.Contains(upsert.Body.Bytes(), []byte("customer.lookup")) || !bytes.Contains(upsert.Body.Bytes(), []byte("Look up standalone customer context")) {
		t.Fatalf("standalone exported tool upsert failed %d body %s", upsert.Code, upsert.Body.String())
	}
	list := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("customer.lookup")) {
		t.Fatalf("standalone exported tool list failed %d body %s", list.Code, list.Body.String())
	}
	get := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools/customer.lookup", nil)
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("Customer lookup")) {
		t.Fatalf("standalone exported tool get failed %d body %s", get.Code, get.Body.String())
	}
	manifest := doJSON(handler, "GET", "/v1/tool-manifests/customer.lookup", nil)
	if manifest.Code != http.StatusOK || !bytes.Contains(manifest.Body.Bytes(), []byte("agent_tool")) || !bytes.Contains(manifest.Body.Bytes(), []byte("test-agent")) {
		t.Fatalf("standalone exported tool manifest sync failed %d body %s", manifest.Code, manifest.Body.String())
	}
	loaded, err := appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exports.Tools) != 1 || loaded.Exports.Tools[0].ToolID != "customer.lookup" {
		t.Fatalf("expected runtime loader standalone exported tool overlay, got %#v", loaded.Exports.Tools)
	}
	remove := doJSON(handler, "DELETE", "/v1/agents/test-agent/exported-tools/customer.lookup", nil)
	if remove.Code != http.StatusOK || !bytes.Contains(remove.Body.Bytes(), []byte(`"deleted":true`)) {
		t.Fatalf("standalone exported tool delete failed %d body %s", remove.Code, remove.Body.String())
	}
	loaded, err = appCore.Agents.Load(context.Background(), "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exports.Tools) != 0 {
		t.Fatalf("expected runtime loader to drop standalone exported tool after delete, got %#v", loaded.Exports.Tools)
	}
	disabled := doJSON(handler, "GET", "/v1/tool-manifests/customer.lookup", nil)
	if disabled.Code != http.StatusOK || !bytes.Contains(disabled.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("expected deleted standalone exported tool manifest to be disabled, got %d body %s", disabled.Code, disabled.Body.String())
	}
}

func TestToolBindingVersionResourceAPIsActivateStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v5", agentpackage.AgentPackageSource{
		Prompt: "tool binding resource prompt",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs:      []string{"echo"},
			AllowedToolGroupIDs: []string{"crm.group"},
			DeniedToolGroupIDs:  []string{"danger.group"},
		},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)

	versions := doJSON(handler, "GET", "/v1/agents/test-agent/tool-bindings/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte("crm.group")) || !bytes.Contains(versions.Body.Bytes(), []byte(`"v5"`)) {
		t.Fatalf("tool binding versions failed %d body %s", versions.Code, versions.Body.String())
	}
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/tool-bindings/activate", map[string]any{"agent_version": "v5"})
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable tool binding activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/tool-bindings/activate", map[string]any{"version": "v5"})
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v5"`)) || !bytes.Contains(activate.Body.Bytes(), []byte("danger.group")) {
		t.Fatalf("tool binding activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v5" {
		t.Fatalf("expected registry default v5, got %s", got)
	}
	bindings := doJSON(handler, "GET", "/v1/agents/test-agent/tool-bindings", nil)
	if bindings.Code != http.StatusOK || !bytes.Contains(bindings.Body.Bytes(), []byte("crm.group")) {
		t.Fatalf("expected activated tool bindings by default, got %d body %s", bindings.Code, bindings.Body.String())
	}
}

func TestSkillVersionResourceAPIsActivateStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v6", agentpackage.AgentPackageSource{
		Prompt: "skill resource prompt",
		Metadata: map[string]any{
			"skill_definitions": []any{
				map[string]any{
					"skill_id":          "skill.lifecycle",
					"version":           "s1",
					"name":              "Lifecycle Skill",
					"instruction":       "Use lifecycle skill.",
					"risk_level":        "low",
					"recommended_tools": []any{"echo"},
				},
			},
		},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)

	versions := doJSON(handler, "GET", "/v1/agents/test-agent/skills/skill.lifecycle/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte("Lifecycle Skill")) || !bytes.Contains(versions.Body.Bytes(), []byte(`"v6"`)) {
		t.Fatalf("skill versions failed %d body %s", versions.Code, versions.Body.String())
	}
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/skills/skill.lifecycle/activate", map[string]any{"agent_version": "v6"})
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable skill activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := appCore.AgentRegistry.SetDefaultForTenant("tenant_1", "test-agent", "v1"); err != nil {
		t.Fatal(err)
	}
	missing := doJSON(handler, "POST", "/v1/agents/test-agent/skills/skill.missing/activate", map[string]any{"version": "v6"})
	if missing.Code != http.StatusNotFound || !bytes.Contains(missing.Body.Bytes(), []byte("skill not found")) {
		t.Fatalf("expected missing skill activation to fail before default switch, got %d body %s", missing.Code, missing.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got == "v6" {
		t.Fatal("missing skill activation must not switch default version")
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/skills/skill.lifecycle/activate", map[string]any{"version": "v6"})
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v6"`)) || !bytes.Contains(activate.Body.Bytes(), []byte("Lifecycle Skill")) {
		t.Fatalf("skill activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v6" {
		t.Fatalf("expected registry default v6, got %s", got)
	}
	skill := doJSON(handler, "GET", "/v1/agents/test-agent/skills/skill.lifecycle", nil)
	if skill.Code != http.StatusOK || !bytes.Contains(skill.Body.Bytes(), []byte("Use lifecycle skill.")) {
		t.Fatalf("expected activated skill by default, got %d body %s", skill.Code, skill.Body.String())
	}
}

func TestKnowledgeAndCrossGroupResourceAPIs(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	create := doJSONWithHeaders(handler, "POST", "/v1/knowledge-bases", map[string]any{
		"owner_group_id": "group-a",
		"name":           "Shared KB",
		"visibility":     contracts.VisibilityShared,
		"index_type":     contracts.KnowledgeIndexHybrid,
	}, map[string]string{"X-Roles": "admin"})
	if create.Code != http.StatusCreated || !bytes.Contains(create.Body.Bytes(), []byte(`"search_mode":"hybrid"`)) {
		t.Fatalf("knowledge base create failed %d body %s", create.Code, create.Body.String())
	}
	var created struct {
		KnowledgeBase contracts.KnowledgeBase `json:"knowledge_base"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	ingest := doJSONWithHeaders(handler, "POST", "/v1/knowledge-bases/"+string(created.KnowledgeBase.KnowledgeBaseID)+"/documents", map[string]any{
		"source_group_id": "group-a",
		"title":           "Launch Contacts",
		"content":         "Launch owner is alex@example.com and release window is 123456.",
		"visibility":      contracts.VisibilityShared,
	}, map[string]string{"X-Roles": "admin"})
	if ingest.Code != http.StatusCreated || !bytes.Contains(ingest.Body.Bytes(), []byte(`"status":"completed"`)) {
		t.Fatalf("knowledge document ingest failed %d body %s", ingest.Code, ingest.Body.String())
	}
	jobs := doJSON(handler, "GET", "/v1/knowledge-bases/"+string(created.KnowledgeBase.KnowledgeBaseID)+"/index-jobs", nil)
	if jobs.Code != http.StatusOK || !bytes.Contains(jobs.Body.Bytes(), []byte(`"search_mode":"hybrid"`)) {
		t.Fatalf("knowledge index jobs failed %d body %s", jobs.Code, jobs.Body.String())
	}
	ownSearch := doJSONWithHeaders(handler, "POST", "/v1/knowledge-search", map[string]any{
		"requester_group_id": "group-a",
		"query":              "Launch",
		"search_mode":        contracts.KnowledgeSearchHybrid,
	}, map[string]string{"X-Roles": "admin"})
	if ownSearch.Code != http.StatusOK || !bytes.Contains(ownSearch.Body.Bytes(), []byte(`"search_mode":"hybrid"`)) {
		t.Fatalf("knowledge search failed %d body %s", ownSearch.Code, ownSearch.Body.String())
	}
	denied := doJSONWithHeaders(handler, "POST", "/v1/cross-groups/search", map[string]any{
		"request_group_id": "group-b",
		"source_group_id":  "group-a",
		"query":            "Launch",
	}, map[string]string{"X-Roles": "admin"})
	if denied.Code != http.StatusBadRequest || !bytes.Contains(denied.Body.Bytes(), []byte("no_matching_policy")) {
		t.Fatalf("expected cross-group search to be denied before permission, got %d body %s", denied.Code, denied.Body.String())
	}
	if err := appCore.GroupPermissions.UpsertPolicy(context.Background(), contracts.GroupPermissionPolicy{
		TenantID:       "tenant_1",
		GroupID:        "group-b",
		SubjectType:    contracts.PermissionSubjectRole,
		SubjectID:      "admin",
		Actions:        []string{contracts.PermissionActionCrossGroupSearch, contracts.PermissionActionKnowledgeSearch},
		ResourceScopes: []string{"group-a"},
	}); err != nil {
		t.Fatal(err)
	}
	permissionOnly := doJSONWithHeaders(handler, "POST", "/v1/cross-groups/search", map[string]any{
		"request_group_id": "group-b",
		"source_group_id":  "group-a",
		"query":            "Launch",
	}, map[string]string{"X-Roles": "admin"})
	if permissionOnly.Code != http.StatusBadRequest || !bytes.Contains(permissionOnly.Body.Bytes(), []byte("no_share_policy")) {
		t.Fatalf("expected share policy to be required, got %d body %s", permissionOnly.Code, permissionOnly.Body.String())
	}
	policy := doJSONWithHeaders(handler, "POST", "/v1/cross-group-share-policies", map[string]any{
		"source_group_id":    "group-a",
		"target_group_id":    "group-b",
		"knowledge_base_ids": []any{string(created.KnowledgeBase.KnowledgeBaseID)},
		"redaction_policy":   contracts.RedactionPolicyMaskEmails,
		"status":             contracts.CrossGroupShareEnabled,
	}, map[string]string{"X-Roles": "admin"})
	if policy.Code != http.StatusCreated || !bytes.Contains(policy.Body.Bytes(), []byte(`"redaction_policy":"mask_emails"`)) {
		t.Fatalf("cross-group share policy create failed %d body %s", policy.Code, policy.Body.String())
	}
	allowed := doJSONWithHeaders(handler, "POST", "/v1/cross-groups/search", map[string]any{
		"request_group_id": "group-b",
		"source_group_id":  "group-a",
		"query":            "Launch",
	}, map[string]string{"X-Roles": "admin"})
	if allowed.Code != http.StatusOK || !bytes.Contains(allowed.Body.Bytes(), []byte(`"redacted":true`)) || !bytes.Contains(allowed.Body.Bytes(), []byte("[redacted-email]")) {
		t.Fatalf("expected redacted cross-group result, got %d body %s", allowed.Code, allowed.Body.String())
	}
}

func TestCollaboratorVersionResourceAPIsActivateStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	target := loader.TestAgentDefinition()
	target.TenantID = "tenant_1"
	target.AgentID = "review-agent"
	target.Version = "v1"
	appCore.AgentRegistry.Put(target)
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v7", agentpackage.AgentPackageSource{
		Prompt: "collaborator resource prompt",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID:            "review-agent",
			Version:            "v1",
			Name:               "Review Agent",
			WhenToUse:          []string{"review"},
			DefaultHandoffMode: "hybrid",
		}},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)

	versions := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators/review-agent/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte("Review Agent")) || !bytes.Contains(versions.Body.Bytes(), []byte(`"v7"`)) {
		t.Fatalf("collaborator versions failed %d body %s", versions.Code, versions.Body.String())
	}
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/collaborators/review-agent/activate", map[string]any{"agent_version": "v7"})
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable collaborator activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := appCore.AgentRegistry.SetDefaultForTenant("tenant_1", "test-agent", "v1"); err != nil {
		t.Fatal(err)
	}
	missing := doJSON(handler, "POST", "/v1/agents/test-agent/collaborators/missing-agent/activate", map[string]any{"version": "v7"})
	if missing.Code != http.StatusNotFound || !bytes.Contains(missing.Body.Bytes(), []byte("collaborator not found")) {
		t.Fatalf("expected missing collaborator activation to fail before default switch, got %d body %s", missing.Code, missing.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got == "v7" {
		t.Fatal("missing collaborator activation must not switch default version")
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/collaborators/review-agent/activate", map[string]any{"version": "v7"})
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v7"`)) || !bytes.Contains(activate.Body.Bytes(), []byte("Review Agent")) {
		t.Fatalf("collaborator activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v7" {
		t.Fatalf("expected registry default v7, got %s", got)
	}
	collaborator := doJSON(handler, "GET", "/v1/agents/test-agent/collaborators/review-agent", nil)
	if collaborator.Code != http.StatusOK || !bytes.Contains(collaborator.Body.Bytes(), []byte("hybrid")) {
		t.Fatalf("expected activated collaborator by default, got %d body %s", collaborator.Code, collaborator.Body.String())
	}
}

func TestExportedToolVersionResourceAPIsActivateStableVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", "test-agent", "v8", agentpackage.AgentPackageSource{
		Prompt: "exported tool resource prompt",
		Exports: contracts.AgentExports{Tools: []contracts.AgentExportedTool{{
			ToolID:      "customer.lookup",
			Name:        "Customer lookup",
			Description: "Look up customer context.",
			WhenToUse:   []string{"customer lookup"},
			InputSchema: map[string]any{"type": "object"},
			RiskLevel:   contracts.RiskLow,
			Visibility:  contracts.ToolProtected,
			Status:      "enabled",
			Version:     "t1",
		}}},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	appCore.AgentRegistry.Put(compiled)

	versions := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools/customer.lookup/versions", nil)
	if versions.Code != http.StatusOK || !bytes.Contains(versions.Body.Bytes(), []byte("Customer lookup")) || !bytes.Contains(versions.Body.Bytes(), []byte(`"v8"`)) {
		t.Fatalf("exported tool versions failed %d body %s", versions.Code, versions.Body.String())
	}
	rejected := doJSON(handler, "POST", "/v1/agents/test-agent/exported-tools/customer.lookup/activate", map[string]any{"agent_version": "v8"})
	if rejected.Code != http.StatusBadRequest || !bytes.Contains(rejected.Body.Bytes(), []byte("must be stable")) {
		t.Fatalf("expected non-stable exported tool activation to fail, got %d body %s", rejected.Code, rejected.Body.String())
	}
	if _, err := appCore.Packages.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.MarkStable(context.Background(), release.PackageVersionID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := appCore.AgentRegistry.SetDefaultForTenant("tenant_1", "test-agent", "v1"); err != nil {
		t.Fatal(err)
	}
	missing := doJSON(handler, "POST", "/v1/agents/test-agent/exported-tools/missing.tool/activate", map[string]any{"version": "v8"})
	if missing.Code != http.StatusNotFound || !bytes.Contains(missing.Body.Bytes(), []byte("exported tool not found")) {
		t.Fatalf("expected missing exported tool activation to fail before default switch, got %d body %s", missing.Code, missing.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got == "v8" {
		t.Fatal("missing exported tool activation must not switch default version")
	}
	activate := doJSON(handler, "POST", "/v1/agents/test-agent/exported-tools/customer.lookup/activate", map[string]any{"version": "v8"})
	if activate.Code != http.StatusOK || !bytes.Contains(activate.Body.Bytes(), []byte(`"active_version":"v8"`)) || !bytes.Contains(activate.Body.Bytes(), []byte("Customer lookup")) {
		t.Fatalf("exported tool activate failed %d body %s", activate.Code, activate.Body.String())
	}
	if got := appCore.AgentRegistry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v8" {
		t.Fatalf("expected registry default v8, got %s", got)
	}
	tool := doJSON(handler, "GET", "/v1/agents/test-agent/exported-tools/customer.lookup", nil)
	if tool.Code != http.StatusOK || !bytes.Contains(tool.Body.Bytes(), []byte("customer lookup")) {
		t.Fatalf("expected activated exported tool by default, got %d body %s", tool.Code, tool.Body.String())
	}
}

func TestOriginAgentDelegateDecisionToolCall(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := loader.TestAgentDefinition()
	definition.TenantID = "tenant_1"
	definition.Collaborators = []contracts.AgentCollaboratorRef{{AgentID: "target-agent", WhenToUse: []string{"delegate"}}}
	appCore.AgentRegistry.Put(definition)
	target := loader.TestAgentDefinition()
	target.TenantID = "tenant_1"
	target.AgentID = "target-agent"
	appCore.AgentRegistry.Put(target)
	appCore.Model = &scriptedServerModel{responses: [][]byte{
		[]byte(`{"type":"tool_call","tool_calls":[{"tool_id":"origin.agent.delegate","name":"origin.agent.delegate","arguments":{"objective":"delegate this","to_agent_id":"target-agent","reason":"specialized check"}}]}`),
		[]byte(`{"type":"reply","reply":{"kind":"answer","text":"delegated"}}`),
		[]byte(`{"type":"reply","reply":{"kind":"answer","text":"done"}}`),
	}}
	appCore.Coordinator.Model = appCore.Model
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"trace_id": "trace_delegate_tool_1",
		"command":  "agent.run",
		"target":   map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload":  map[string]any{"input": "please delegate"},
		"context":  map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("agent.run delegate tool failed %d body %s", resp.Code, resp.Body.String())
	}
	runTrace := doJSON(handler, "GET", "/v1/traces/trace_delegate_tool_1", nil)
	if runTrace.Code != http.StatusOK || !bytes.Contains(runTrace.Body.Bytes(), []byte("handoff.completed")) || !bytes.Contains(runTrace.Body.Bytes(), []byte("tool.policy_checked")) {
		t.Fatalf("expected handoff and tool trace events, got %d body %s", runTrace.Code, runTrace.Body.String())
	}
}

func TestTaskCommandCreatePlanAndQuerySnapshot(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"command": "task.command",
		"payload": map[string]any{
			"task_id":   task.TaskID,
			"command":   "create_plan",
			"objective": "write report",
			"steps": []any{
				map[string]any{"title": "Collect facts", "expected_tool_hints": []any{"echo"}},
				map[string]any{"title": "Write report"},
			},
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("create plan failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Plan  contracts.TaskPlan   `json:"plan"`
		Steps []contracts.PlanStep `json:"steps"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Plan.Status != contracts.PlanRunning || len(result.Steps) != 2 {
		t.Fatalf("unexpected plan result: %#v", result)
	}
	planResp := doJSON(handler, "GET", "/v1/tasks/"+string(task.TaskID)+"/plan", nil)
	if planResp.Code != http.StatusOK {
		t.Fatalf("plan query failed %d body %s", planResp.Code, planResp.Body.String())
	}
	if !bytes.Contains(planResp.Body.Bytes(), []byte("Collect facts")) {
		t.Fatalf("expected plan snapshot to include step, got %s", planResp.Body.String())
	}
}

func TestTaskCommandUpgradeAgentVersion(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", LogLevel: "error", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := loader.TestAgentDefinition()
	definition.Version = "v2"
	definition.PolicyRefs.PolicySetID = "policy_v2"
	appCore.AgentRegistry.Put(definition)
	task, err := appCore.TaskRuntime.CreateTask(nilContext(), newServerTestTask(), "user_1", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	body := map[string]any{
		"command": "task.command",
		"payload": map[string]any{
			"task_id":       task.TaskID,
			"command":       "upgrade_agent_version",
			"agent_version": "v2",
		},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	resp := doJSON(handler, "POST", "/v1/commands", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("upgrade_agent_version failed %d body %s", resp.Code, resp.Body.String())
	}
	var result struct {
		Task contracts.Task `json:"task"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Task.AgentVersion != "v2" || result.Task.PolicySetID != "policy_v2" {
		t.Fatalf("expected upgraded task agent version, got %#v", result.Task)
	}
	runBody := map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent"},
		"context": map[string]any{"tenant_id": "tenant_1", "task_id": task.TaskID},
	}
	runResp := doJSON(handler, "POST", "/v1/commands", runBody)
	if runResp.Code != http.StatusOK {
		t.Fatalf("run upgraded task failed %d body %s", runResp.Code, runResp.Body.String())
	}
	var runResult struct {
		RunID contracts.AgentRunID `json:"run_id"`
	}
	if err := json.Unmarshal(runResp.Body.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	run, err := appCore.Runs.Get(context.Background(), runResult.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AgentVersion != "v2" {
		t.Fatalf("expected run to use upgraded task version, got %#v", run)
	}
}

func TestReleaseSwitchesDenyCommands(t *testing.T) {
	appCore, err := core.New(config.Config{
		ServiceName:                "clean-core",
		Version:                    "test",
		Env:                        "test",
		HTTPAddr:                   ":0",
		LogLevel:                   "error",
		Readiness:                  true,
		DisabledAgentIDs:           []string{"test-agent"},
		DisabledToolIDs:            []string{"echo"},
		DisableHandoff:             true,
		DisableExternalToolsInvoke: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCore(appCore, logging.New("error"))
	agentRun := map[string]any{
		"command": "agent.run",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"input": "hello"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	if resp := doJSON(handler, "POST", "/v1/commands", agentRun); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected disabled agent to be denied, got %d body %s", resp.Code, resp.Body.String())
	}
	toolInvoke := map[string]any{
		"command": "tools.invoke",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"tool_id": "echo"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	if resp := doJSON(handler, "POST", "/v1/commands", toolInvoke); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected external tools.invoke to be denied, got %d body %s", resp.Code, resp.Body.String())
	}
	delegate := map[string]any{
		"command": "origin.agent.delegate",
		"target":  map[string]any{"agent_id": "test-agent", "version": "v1"},
		"payload": map[string]any{"parent_task_id": "task_1", "to_agent_id": "test-agent", "objective": "x"},
		"context": map[string]any{"tenant_id": "tenant_1"},
	}
	if resp := doJSON(handler, "POST", "/v1/commands", delegate); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected handoff to be denied, got %d body %s", resp.Code, resp.Body.String())
	}
}

func doJSON(handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	return doJSONWithHeaders(handler, method, path, body, nil)
}

func decodeMetrics(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics failed %d body %s", resp.Code, resp.Body.String())
	}
	var metrics map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &metrics); err != nil {
		t.Fatal(err)
	}
	return metrics
}

func metricNumber(t *testing.T, metrics map[string]any, key string) float64 {
	t.Helper()
	value, ok := metrics[key]
	if !ok {
		t.Fatalf("missing metric %s in %#v", key, metrics)
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		out, err := typed.Float64()
		if err != nil {
			t.Fatalf("metric %s is not numeric: %v", key, err)
		}
		return out
	default:
		t.Fatalf("metric %s is not numeric: %#v", key, value)
		return 0
	}
}

func metricString(t *testing.T, metrics map[string]any, key string) string {
	t.Helper()
	value, ok := metrics[key]
	if !ok {
		t.Fatalf("missing metric %s in %#v", key, metrics)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("metric %s is not a string: %#v", key, value)
	}
	return text
}

func metricObject(t *testing.T, metrics map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := metrics[key]
	if !ok {
		t.Fatalf("missing metric group %s in %#v", key, metrics)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("metric group %s is not an object: %#v", key, value)
	}
	return object
}

var registerMetricsTestDriver sync.Once

func openMetricsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	registerMetricsTestDriver.Do(func() {
		sql.Register("znt_metrics_test", metricsTestDriver{})
	})
	db, err := sql.Open("znt_metrics_test", "")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

type metricsTestDriver struct{}

func (metricsTestDriver) Open(string) (driver.Conn, error) {
	return metricsTestConn{}, nil
}

type metricsTestConn struct{}

func (metricsTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("metrics test driver does not support prepared statements")
}

func (metricsTestConn) Close() error {
	return nil
}

func (metricsTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("metrics test driver does not support transactions")
}

func (metricsTestConn) Ping(context.Context) error {
	return nil
}

func (metricsTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "information_schema.tables"):
		return metricsTestRows{columns: []string{"table_name"}}, nil
	case strings.Contains(query, "information_schema.columns"):
		return metricsTestRows{columns: []string{"column_name", "is_nullable", "data_type"}}, nil
	case strings.Contains(query, "pg_indexes"):
		return metricsTestRows{columns: []string{"indexname"}}, nil
	default:
		return metricsTestRows{columns: []string{"version", "name", "checksum", "applied_at"}}, nil
	}
}

type metricsTestRows struct {
	columns []string
}

func (r metricsTestRows) Columns() []string {
	return r.columns
}

func (metricsTestRows) Close() error {
	return nil
}

func (metricsTestRows) Next([]driver.Value) error {
	return io.EOF
}

func nilContext() context.Context {
	return context.Background()
}

func hasTraceEvent(events []contracts.TraceEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func newServerTestTask() contracts.Task {
	return contracts.Task{
		TaskID:       "parent_task_1",
		TenantID:     "tenant_1",
		Title:        "parent",
		Objective:    "parent objective",
		Status:       contracts.TaskCreated,
		AgentID:      "test-agent",
		AgentVersion: "v1",
		PolicySetID:  "policy_default",
	}
}

type scriptedServerModel struct {
	responses [][]byte
	calls     int
}

type projectionPackageStore struct {
	profile       agentpackage.PromptProfileProjection
	binding       agentpackage.ToolBindingProjection
	skills        []agentpackage.SkillDefinitionProjection
	collaborators []agentpackage.CollaboratorProjection
	exportedTools []agentpackage.ExportedToolProjection
}

func (s *projectionPackageStore) SaveAgentAsset(context.Context, agentpackage.AgentAsset) error {
	return nil
}

func (s *projectionPackageStore) GetAgentAsset(context.Context, contracts.TenantID, contracts.AgentID) (agentpackage.AgentAsset, bool, error) {
	return agentpackage.AgentAsset{}, false, nil
}

func (s *projectionPackageStore) ListAgentAssets(context.Context, contracts.TenantID) ([]agentpackage.AgentAsset, error) {
	return nil, nil
}

func (s *projectionPackageStore) SaveDraft(context.Context, agentpackage.Draft) error {
	return nil
}

func (s *projectionPackageStore) GetDraft(context.Context, string) (agentpackage.Draft, bool, error) {
	return agentpackage.Draft{}, false, nil
}

func (s *projectionPackageStore) ListDrafts(context.Context, contracts.TenantID, contracts.AgentID) ([]agentpackage.Draft, error) {
	return nil, nil
}

func (s *projectionPackageStore) SaveProposal(context.Context, agentpackage.Proposal) error {
	return nil
}

func (s *projectionPackageStore) GetProposal(context.Context, contracts.ProposalID) (agentpackage.Proposal, bool, error) {
	return agentpackage.Proposal{}, false, nil
}

func (s *projectionPackageStore) SaveRelease(context.Context, contracts.AgentPackageVersion, agentpackage.AgentPackageSource, contracts.AgentDefinition) error {
	return nil
}

func (s *projectionPackageStore) UpdateReleaseStatus(context.Context, contracts.PackageVersionID, contracts.ReleaseStatus) error {
	return nil
}

func (s *projectionPackageStore) UpdateReleaseCanary(context.Context, contracts.PackageVersionID, contracts.ReleaseStatus, int, []string) error {
	return nil
}

func (s *projectionPackageStore) MarkEvalResult(context.Context, contracts.PackageVersionID, bool) error {
	return nil
}

func (s *projectionPackageStore) GetRelease(context.Context, contracts.PackageVersionID) (contracts.AgentPackageVersion, bool, error) {
	return contracts.AgentPackageVersion{}, false, nil
}

func (s *projectionPackageStore) ListReleases(context.Context) ([]contracts.AgentPackageVersion, error) {
	return nil, nil
}

func (s *projectionPackageStore) RecordCanaryHit(context.Context, contracts.CanaryHit) error {
	return nil
}

func (s *projectionPackageStore) ListCanaryHits(context.Context, contracts.TenantID, contracts.AgentID) ([]contracts.CanaryHit, error) {
	return nil, nil
}

func (s *projectionPackageStore) GetPromptProfileProjection(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (agentpackage.PromptProfileProjection, bool, error) {
	if draftID == "" && s.profile.TenantID == tenantID && s.profile.AgentID == agentID && s.profile.Version == version {
		return s.profile, true, nil
	}
	return agentpackage.PromptProfileProjection{}, false, nil
}

func (s *projectionPackageStore) GetToolBindingProjection(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (agentpackage.ToolBindingProjection, bool, error) {
	if draftID == "" && s.binding.TenantID == tenantID && s.binding.AgentID == agentID && s.binding.Version == version {
		return s.binding, true, nil
	}
	return agentpackage.ToolBindingProjection{}, false, nil
}

func (s *projectionPackageStore) ListSkillDefinitionProjections(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.SkillDefinitionProjection, error) {
	if draftID != "" {
		return nil, nil
	}
	out := make([]agentpackage.SkillDefinitionProjection, 0, len(s.skills))
	for _, skill := range s.skills {
		if skill.TenantID == tenantID && skill.AgentID == agentID && skill.Version == version {
			out = append(out, skill)
		}
	}
	return out, nil
}

func (s *projectionPackageStore) GetSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, skillID string) (agentpackage.SkillDefinitionProjection, bool, error) {
	skills, err := s.ListSkillDefinitionProjections(ctx, tenantID, agentID, version, draftID)
	if err != nil {
		return agentpackage.SkillDefinitionProjection{}, false, err
	}
	for _, skill := range skills {
		if skill.SkillID == skillID {
			return skill, true, nil
		}
	}
	return agentpackage.SkillDefinitionProjection{}, false, nil
}

func (s *projectionPackageStore) ListCollaboratorProjections(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.CollaboratorProjection, error) {
	if draftID != "" {
		return nil, nil
	}
	out := make([]agentpackage.CollaboratorProjection, 0, len(s.collaborators))
	for _, collaborator := range s.collaborators {
		if collaborator.TenantID == tenantID && collaborator.AgentID == agentID && collaborator.Version == version {
			out = append(out, collaborator)
		}
	}
	return out, nil
}

func (s *projectionPackageStore) GetCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, collaboratorAgentID contracts.AgentID) (agentpackage.CollaboratorProjection, bool, error) {
	collaborators, err := s.ListCollaboratorProjections(ctx, tenantID, agentID, version, draftID)
	if err != nil {
		return agentpackage.CollaboratorProjection{}, false, err
	}
	for _, collaborator := range collaborators {
		if collaborator.CollaboratorAgentID == collaboratorAgentID {
			return collaborator, true, nil
		}
	}
	return agentpackage.CollaboratorProjection{}, false, nil
}

func (s *projectionPackageStore) ListExportedToolProjections(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.ExportedToolProjection, error) {
	if tenantID != "tenant_1" || agentID != "test-agent" || (version != "v9" && draftID == "") {
		return nil, nil
	}
	return append([]agentpackage.ExportedToolProjection(nil), s.exportedTools...), nil
}

func (s *projectionPackageStore) GetExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, toolID string) (agentpackage.ExportedToolProjection, bool, error) {
	tools, err := s.ListExportedToolProjections(ctx, tenantID, agentID, version, draftID)
	if err != nil {
		return agentpackage.ExportedToolProjection{}, false, err
	}
	for _, tool := range tools {
		if tool.ToolID == toolID {
			return tool, true, nil
		}
	}
	return agentpackage.ExportedToolProjection{}, false, nil
}

func (m *scriptedServerModel) Complete(context.Context, modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	if m.calls >= len(m.responses) {
		return modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"ok"}}`)}, nil
	}
	response := m.responses[m.calls]
	m.calls++
	return modelclient.ModelResponse{RawDecisionJSON: response, ModelProvider: "stub", ModelName: "scripted-server"}, nil
}

func (m *scriptedServerModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	resp, err := m.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 2)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{
			Type:          modelclient.ModelStreamDelta,
			Delta:         string(resp.RawDecisionJSON),
			ModelProvider: resp.ModelProvider,
			ModelName:     resp.ModelName,
		}
		ch <- modelclient.ModelStreamEvent{
			Type:          modelclient.ModelStreamCompleted,
			RawDecision:   resp.RawDecisionJSON,
			ModelProvider: resp.ModelProvider,
			ModelName:     resp.ModelName,
			Usage:         resp.Usage,
		}
	}()
	return ch, nil
}

type blockingStreamModel struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStreamModel() *blockingStreamModel {
	return &blockingStreamModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (m *blockingStreamModel) Capabilities() modelclient.ModelCapabilityDescriptor {
	return modelclient.ModelCapabilityDescriptor{
		Provider:                 "blocking",
		Model:                    "blocking-stream",
		APIStyle:                 "test",
		StructuredOutput:         true,
		Streaming:                true,
		SupportsJSONResponseMode: true,
	}
}

func (m *blockingStreamModel) Complete(ctx context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	events, err := m.Stream(ctx, request)
	if err != nil {
		return modelclient.ModelResponse{}, err
	}
	var raw []byte
	for event := range events {
		if event.Type == modelclient.ModelStreamError {
			return modelclient.ModelResponse{}, event.Err
		}
		if event.Type == modelclient.ModelStreamCompleted {
			raw = event.RawDecision
		}
	}
	return modelclient.ModelResponse{RawDecisionJSON: raw, ModelProvider: "blocking", ModelName: "blocking-stream"}, nil
}

func (m *blockingStreamModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	ch := make(chan modelclient.ModelStreamEvent, 2)
	go func() {
		defer close(ch)
		m.once.Do(func() { close(m.started) })
		select {
		case <-m.release:
		case <-ctx.Done():
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: ctx.Err()}
			return
		}
		decision := []byte(`{"type":"reply","reply":{"kind":"answer","text":"ok"}}`)
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamDelta, Delta: string(decision), ModelProvider: "blocking", ModelName: "blocking-stream"}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: decision, ModelProvider: "blocking", ModelName: "blocking-stream"}
	}()
	return ch, nil
}

func (m *blockingStreamModel) releaseRun() {
	select {
	case <-m.release:
	default:
		close(m.release)
	}
}

func doJSONWithHeaders(handler http.Handler, method string, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant_1")
	req.Header.Set("X-Caller-ID", "user_1")
	req.Header.Set("X-Caller-Type", "user")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
