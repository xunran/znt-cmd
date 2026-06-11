package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"znt/internal/contracts"
	"znt/internal/governance/trace"
	"znt/internal/tool/registry"
)

type memoryStore struct {
	providers []ToolProvider
	groups    []ToolGroup
	manifests []ToolManifest
	cache     map[string]string
}

func (s *memoryStore) UpsertProvider(_ context.Context, provider ToolProvider) error {
	for i, existing := range s.providers {
		if existing.TenantID == provider.TenantID && existing.ProviderID == provider.ProviderID {
			s.providers[i] = provider
			return nil
		}
	}
	s.providers = append(s.providers, provider)
	return nil
}

func (s *memoryStore) UpsertGroup(_ context.Context, group ToolGroup) error {
	for i, existing := range s.groups {
		if existing.TenantID == group.TenantID && existing.GroupID == group.GroupID {
			s.groups[i] = group
			return nil
		}
	}
	s.groups = append(s.groups, group)
	return nil
}

func (s *memoryStore) UpsertManifest(_ context.Context, manifest ToolManifest) error {
	for i, existing := range s.manifests {
		if existing.TenantID == manifest.TenantID && existing.ToolID == manifest.ToolID {
			s.manifests[i] = manifest
			return nil
		}
	}
	s.manifests = append(s.manifests, manifest)
	return nil
}

func (s *memoryStore) UpsertRuntimeCache(_ context.Context, tenantID contracts.TenantID, toolID string, _ string, status string) error {
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	s.cache[string(tenantID)+"\x00"+toolID] = status
	return nil
}

func (s *memoryStore) ListProviders(context.Context) ([]ToolProvider, error) {
	return append([]ToolProvider(nil), s.providers...), nil
}

func (s *memoryStore) ListGroups(context.Context) ([]ToolGroup, error) {
	return append([]ToolGroup(nil), s.groups...), nil
}

func (s *memoryStore) ListManifests(context.Context) ([]ToolManifest, error) {
	return append([]ToolManifest(nil), s.manifests...), nil
}

func TestUpsertManifestRegistersHTTPTool(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{"echo": payload["arguments"]},
		})
	}))
	defer remote.Close()

	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		Name:        "CRM lookup",
		Description: "Look up a customer in CRM.",
		WhenToUse:   []string{"crm", "customer lookup"},
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type: ExecutorTypeHTTPDirect,
			URL:  remote.URL,
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RiskLevel != contracts.RiskLow || manifest.Visibility != contracts.ToolProtected {
		t.Fatalf("expected normalized defaults, got risk=%s visibility=%s", manifest.RiskLevel, manifest.Visibility)
	}
	if manifest.ExecutionProfile == "" || manifest.ExecutionProfile == "local" {
		t.Fatalf("expected http execution profile, got %q", manifest.ExecutionProfile)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.lookup")
	if !ok {
		t.Fatal("expected tool registered")
	}
	if _, ok := reg.GetForTenant("tenant_2", "crm.lookup"); ok {
		t.Fatal("expected tenant-scoped tool to be hidden from other tenants")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "crm.lookup",
		TenantID:  "tenant_2",
		Arguments: map[string]any{"customer_id": "c_1"},
	})
	if err == nil {
		t.Fatal("expected tenant guard to reject mismatched call tenant")
	}
	output, _, err = tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "crm.lookup",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"customer_id": "c_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output["echo"] == nil {
		t.Fatalf("unexpected output %#v", output)
	}
	if err := reg.Upsert(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID: "crm.lookup",
			Name:   "global lookup",
		},
		Executor: fakeExecutor{},
	}); err == nil {
		t.Fatal("expected global tool id to conflict with tenant-scoped tool")
	}
}

func TestUpsertManifestRejectsHighRiskHTTPDirect(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.delete",
		Name:        "Delete customer",
		Description: "Delete a customer through direct HTTP.",
		InputSchema: map[string]any{"type": "object"},
		RiskLevel:   contracts.RiskHigh,
		Executor: ExecutorSpec{
			Type: ExecutorTypeHTTPDirect,
			URL:  "https://crm.example.test/delete",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected http_direct high risk policy denial, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.delete"); ok {
		t.Fatal("rejected http_direct manifest should not be registered")
	}
}

func TestHTTPDirectCanBeDisabledByReleaseSwitch(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	service.DisableHTTPDirect = true

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		Name:        "CRM lookup",
		Description: "Look up a customer through direct HTTP.",
		InputSchema: map[string]any{"type": "object"},
		RiskLevel:   contracts.RiskLow,
		Executor: ExecutorSpec{
			Type: ExecutorTypeHTTPDirect,
			URL:  "https://crm.example.test/lookup",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected http_direct release switch denial, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("disabled http_direct manifest should not be registered")
	}
}

func TestRestoreDoesNotInstallDisabledHTTPDirect(t *testing.T) {
	store := &memoryStore{
		manifests: []ToolManifest{{
			TenantID:    "tenant_1",
			ToolID:      "crm.lookup",
			Name:        "CRM lookup",
			Description: "Look up a customer through direct HTTP.",
			InputSchema: map[string]any{"type": "object"},
			RiskLevel:   contracts.RiskLow,
			Executor: ExecutorSpec{
				Type: ExecutorTypeHTTPDirect,
				URL:  "https://crm.example.test/lookup",
			},
			Status:  StatusEnabled,
			Version: "v1",
		}},
	}
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, store)
	service.DisableHTTPDirect = true

	if err := service.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("disabled http_direct should not be restored into runtime registry")
	}
	if status := store.cache["tenant_1\x00crm.lookup"]; status != StatusDisabled {
		t.Fatalf("expected disabled runtime cache, got %q", status)
	}
	if err := service.CheckToolAvailability(context.Background(), "tenant_1", contracts.ToolDefinition{ToolID: "crm.lookup"}); err == nil {
		t.Fatal("expected stale http_direct availability denied")
	}
}

func TestProviderSyncInstallsToolHostCatalog(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	traceRecorder := trace.NewInMemoryRecorder()
	service.SetTraceRecorder(traceRecorder)
	var invokePayload map[string]any
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tools/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tools": []map[string]any{{
					"tool_id":      "crm.search",
					"operation":    "search",
					"name":         "CRM search",
					"description":  "Search CRM records.",
					"input_schema": map[string]any{"type": "object"},
					"risk_level":   "low",
					"visibility":   "protected",
					"version":      "v1",
				}},
			})
		case "/tools/invoke":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			invokePayload = payload
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{"operation": payload["operation"]},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifests, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "crm", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || manifests[0].ToolID != "crm.search" {
		t.Fatalf("unexpected manifests %#v", manifests)
	}
	if manifests[0].ExecutionProfile == "local" || manifests[0].ExecutionProfile == "" {
		t.Fatalf("expected remote execution profile, got %q", manifests[0].ExecutionProfile)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.search")
	if !ok {
		t.Fatal("expected synced tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:         "crm.search",
		TenantID:       "tenant_1",
		Arguments:      map[string]any{"q": "alice"},
		TraceID:        "trace_1",
		RunID:          "run_1",
		TaskID:         "task_1",
		IdempotencyKey: "idem_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if output["operation"] != "search" {
		t.Fatalf("unexpected output %#v", output)
	}
	if invokePayload["trace_id"] != "trace_1" || invokePayload["run_id"] != "run_1" || invokePayload["task_id"] != "task_1" || invokePayload["idempotency_key"] != "idem_1" {
		t.Fatalf("expected runtime context in tool host payload, got %#v", invokePayload)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	var invoked, completed bool
	for _, event := range events {
		switch event.Type {
		case contracts.TraceToolProviderInvoked:
			invoked = event.Payload["provider_id"] == "crm" && event.Payload["operation"] == "search"
		case contracts.TraceToolProviderCompleted:
			completed = event.Payload["provider_id"] == "crm" && event.Payload["operation"] == "search"
			if _, ok := event.Payload["latency_ms"].(int); !ok {
				t.Fatalf("expected provider latency evidence, got %#v", event.Payload)
			}
		}
	}
	if !invoked || !completed {
		t.Fatalf("expected provider invoke trace events, got %#v", events)
	}
}

func TestProviderSyncAcceptsAlternateToolHostCatalogShape(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	var catalogPaths []string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalogPaths = append(catalogPaths, r.URL.Path)
		switch r.URL.Path {
		case "/tools/catalog":
			http.NotFound(w, r)
		case "/tools":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"tool_key":    "lookup_customer",
				"operation":   "lookup",
				"name":        "CRM lookup",
				"description": "Look up a customer through a ToolHost.",
				"parameters":  map[string]any{"type": "object"},
				"risk":        "low",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifests, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "crm", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected one manifest, got %#v", manifests)
	}
	manifest := manifests[0]
	if manifest.ToolID != "crm.lookup_customer" || manifest.Executor.Operation != "lookup" {
		t.Fatalf("unexpected normalized manifest %#v", manifest)
	}
	if manifest.InputSchema["type"] != "object" || manifest.OutputSchema["type"] != "object" {
		t.Fatalf("expected defaulted schemas, got input=%#v output=%#v", manifest.InputSchema, manifest.OutputSchema)
	}
	if manifest.Visibility != contracts.ToolProtected || manifest.Version != "v1" {
		t.Fatalf("expected default visibility/version, got %#v", manifest)
	}
	if len(catalogPaths) < 2 || catalogPaths[0] != "/tools/catalog" || catalogPaths[1] != "/tools" {
		t.Fatalf("expected /tools fallback after /tools/catalog, got %#v", catalogPaths)
	}
}

func TestProviderHealthCanRecordTraceEvidence(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	traceRecorder := trace.NewInMemoryRecorder()
	service.SetTraceRecorder(traceRecorder)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckProviderHealthForTrace(context.Background(), "tenant_1", "crm", "optimizer_1", "trace_health_1"); err != nil {
		t.Fatal(err)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_health_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != contracts.TraceToolProviderHealthChecked || events[0].Payload["health_status"] != HealthHealthy {
		t.Fatalf("expected provider health trace evidence, got %#v", events)
	}
	if events[0].TenantID != "tenant_1" {
		t.Fatalf("expected provider health trace to use caller tenant, got %q", events[0].TenantID)
	}
	if _, ok := events[0].Payload["latency_ms"].(int); !ok {
		t.Fatalf("expected health latency evidence, got %#v", events[0].Payload)
	}
}

func TestToolHostProviderAuthTimeoutAndRetry(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	catalogCalls := 0
	invokeCalls := 0
	healthCalls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(providerAuthRefHeader); got != "cred/crm-tools" {
			t.Fatalf("expected provider auth ref header, got %q", got)
		}
		switch r.URL.Path {
		case "/tools/catalog":
			catalogCalls++
			if catalogCalls == 1 {
				http.Error(w, "temporary catalog failure", http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tools": []map[string]any{{
					"tool_id":      "crm.retry",
					"operation":    "retry_lookup",
					"name":         "CRM retry",
					"description":  "Retrying CRM lookup.",
					"input_schema": map[string]any{"type": "object"},
					"risk_level":   "low",
					"visibility":   "protected",
					"version":      "v1",
				}},
			})
		case "/tools/invoke":
			invokeCalls++
			if invokeCalls == 1 {
				http.Error(w, "temporary invoke failure", http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"ok": true}})
		case "/healthz":
			healthCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
		AuthRef:    "cred/crm-tools",
		TimeoutMS:  1000,
		RetryMax:   1,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckProviderHealth(context.Background(), "tenant_1", "crm", "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if healthCalls != 1 {
		t.Fatalf("expected health probe, got %d", healthCalls)
	}
	manifests, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "crm", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || catalogCalls != 2 {
		t.Fatalf("expected retried catalog sync, calls=%d manifests=%#v", catalogCalls, manifests)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.retry")
	if !ok {
		t.Fatal("expected synced tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "crm.retry",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"q": "alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if invokeCalls != 2 || output["ok"] != true {
		t.Fatalf("expected retried invoke, calls=%d output=%#v", invokeCalls, output)
	}
}

func TestToolGroupDisableUnregistersTools(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"ok": true}})
	}))
	defer remote.Close()

	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "CRM",
		Status:   StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		GroupID:     "crm",
		Name:        "CRM lookup",
		Description: "Look up customers.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type: ExecutorTypeHTTPDirect,
			URL:  remote.URL,
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.GroupID != "crm" {
		t.Fatalf("expected manifest group id, got %#v", manifest)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.lookup")
	if !ok || tool.Definition.GroupID != "crm" {
		t.Fatalf("expected registered grouped tool, got %#v", tool)
	}
	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "CRM",
		Status:   StatusDisabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("expected disabled group to unregister tool")
	}
	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "CRM",
		Status:   StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); !ok {
		t.Fatal("expected enabled group to reinstall tool")
	}
}

func TestTenantToolGroupDoesNotAffectGlobalTools(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		ToolID:      "crm.global",
		GroupID:     "crm",
		Name:        "Global CRM",
		Description: "Global CRM helper.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeAgentTool,
			ProviderID: "agent_provider",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("", "crm.global"); !ok {
		t.Fatal("expected global grouped tool registered")
	}

	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "Tenant CRM",
		Status:   StatusDisabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("", "crm.global"); !ok {
		t.Fatal("tenant scoped group should not unregister global tool")
	}
}

func TestGlobalToolGroupAffectsTenantToolsWithoutOverride(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		GroupID:     "crm",
		Name:        "CRM lookup",
		Description: "Look up customers.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type: ExecutorTypeHTTPDirect,
			URL:  "https://tools.example.test/invoke",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); !ok {
		t.Fatal("expected grouped tenant tool registered before global group policy")
	}
	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		GroupID: "crm",
		Name:    "CRM",
		Status:  StatusDisabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("expected global disabled group to unregister tenant tool without override")
	}
	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "Tenant CRM",
		Status:   StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); !ok {
		t.Fatal("expected tenant group override to reinstall tool")
	}
}

func TestProviderDisableUnregistersAndAvailabilityDeniesStaleTools(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"ok": true}})
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
		Status:     StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		GroupID:     "crm",
		Name:        "CRM lookup",
		Description: "Look up customers.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.lookup")
	if !ok {
		t.Fatal("expected provider tool registered")
	}
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
		Status:     StatusDisabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("expected disabled provider to unregister tool")
	}
	if err := service.CheckToolAvailability(context.Background(), "tenant_1", tool.Definition); err == nil {
		t.Fatal("expected stale provider tool availability denied")
	}
}

func TestProviderHealthUnregistersAndRestoresToolHostTools(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	healthy := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			if !healthy {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/tools/invoke":
			_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"ok": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   remote.URL,
		Status:     StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		Name:        "CRM lookup",
		Description: "Look up customers.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.lookup")
	if !ok {
		t.Fatal("expected unknown-health provider to allow install")
	}

	unhealthy, err := service.CheckProviderHealth(context.Background(), "tenant_1", "crm", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if unhealthy.HealthStatus != HealthUnhealthy || unhealthy.LastHealthCheckAt == nil || unhealthy.LastHealthError == "" {
		t.Fatalf("expected unhealthy provider evidence, got %#v", unhealthy)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("expected unhealthy provider to unregister tool")
	}
	if err := service.CheckToolAvailability(context.Background(), "tenant_1", tool.Definition); err == nil {
		t.Fatal("expected unhealthy provider to deny stale tool execution")
	}

	healthy = true
	restored, err := service.CheckProviderHealth(context.Background(), "tenant_1", "crm", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if restored.HealthStatus != HealthHealthy || restored.LastHealthError != "" {
		t.Fatalf("expected healthy provider, got %#v", restored)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); !ok {
		t.Fatal("expected healthy provider to reinstall tool")
	}
}

func TestMCPProviderSyncsJSONRPCToolsAndExecutesCall(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	traceRecorder := trace.NewInMemoryRecorder()
	service := NewService(reg, nil)
	service.SetTraceRecorder(traceRecorder)
	var listCalls int
	var callCalls int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(providerAuthRefHeader) != "auth_ref_1" {
			t.Fatalf("expected provider auth ref header, got %q", r.Header.Get(providerAuthRefHeader))
		}
		var req struct {
			JSONRPC string         `json:"jsonrpc"`
			ID      any            `json:"id"`
			Method  string         `json:"method"`
			Params  map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "tools/list":
			listCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "sum",
						"title":       "Sum",
						"description": "Add two numbers.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"a": map[string]any{"type": "number"},
								"b": map[string]any{"type": "number"},
							},
						},
						"annotations": map[string]any{"readOnlyHint": true},
					}},
				},
			})
		case "tools/call":
			callCalls++
			if req.Params["name"] != "sum" {
				t.Fatalf("expected tools/call name=sum, got %#v", req.Params)
			}
			args, _ := req.Params["arguments"].(map[string]any)
			total := args["a"].(float64) + args["b"].(float64)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"structuredContent": map[string]any{"total": total},
					"content":           []map[string]any{{"type": "text", "text": "5"}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method %s", req.Method)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   "calc",
		ProviderType: ProviderTypeMCP,
		Name:         "Calculator MCP",
		Endpoint:     remote.URL + "/mcp",
		AuthRef:      "auth_ref_1",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	healthy, err := service.CheckProviderHealth(context.Background(), "tenant_1", "calc", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if healthy.HealthStatus != HealthHealthy {
		t.Fatalf("expected healthy MCP provider, got %#v", healthy)
	}
	tools, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "calc", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].ToolID != "calc.sum" || tools[0].Executor.Type != ExecutorTypeMCP || tools[0].Executor.Operation != "sum" {
		t.Fatalf("unexpected synced MCP tools: %#v", tools)
	}
	tool, ok := reg.GetForTenant("tenant_1", "calc.sum")
	if !ok {
		t.Fatal("expected MCP tool to be installed in runtime registry")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolCallID: "call_1",
		TenantID:   "tenant_1",
		ToolID:     "calc.sum",
		Arguments:  map[string]any{"a": 2, "b": 3},
		TraceID:    "trace_mcp_1",
		RunID:      "run_mcp_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := output["structuredContent"].(map[string]any)
	if structured["total"] != float64(5) || callCalls != 1 || listCalls < 2 {
		t.Fatalf("unexpected MCP execution output=%#v listCalls=%d callCalls=%d", output, listCalls, callCalls)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_mcp_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != contracts.TraceToolProviderInvoked || events[1].Type != contracts.TraceToolProviderCompleted {
		t.Fatalf("expected MCP provider invoke/completed trace, got %#v", events)
	}
}

func TestMCPExecutorMapsIsErrorResultToRuntimeError(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "tools/call" {
			t.Fatalf("unexpected method %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"isError": true,
				"content": []map[string]any{{
					"type": "text",
					"text": "remote tool failed",
				}},
			},
		})
	}))
	defer remote.Close()

	executor := MCPExecutor{
		Endpoint:   remote.URL,
		ProviderID: "calc",
		Operation:  "fail",
		Client:     remote.Client(),
		TenantID:   "tenant_1",
	}
	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		ToolCallID: "call_fail_1",
		TenantID:   "tenant_1",
		ToolID:     "calc.fail",
		Arguments:  map[string]any{},
	})
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeToolExecutionFailed {
		t.Fatalf("expected MCP isError to map to tool execution failure, got %T %v", err, err)
	}
}

func TestRestoreInstallsEnabledPersistedTools(t *testing.T) {
	store := &memoryStore{
		providers: []ToolProvider{{
			TenantID:   "tenant_1",
			ProviderID: "crm",
			Name:       "CRM provider",
			Endpoint:   "https://tools.example.test",
			Status:     StatusEnabled,
			Version:    "v1",
		}},
		manifests: []ToolManifest{{
			TenantID:    "tenant_1",
			ToolID:      "crm.lookup",
			Name:        "CRM lookup",
			Description: "Look up customers.",
			InputSchema: map[string]any{"type": "object"},
			Executor: ExecutorSpec{
				Type:       ExecutorTypeStaticToolHost,
				ProviderID: "crm",
				Operation:  "lookup",
			},
			Status:  StatusEnabled,
			Version: "v1",
		}},
	}
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, store)

	if err := service.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.lookup")
	if !ok {
		t.Fatal("expected restored tool registered")
	}
	if tool.Definition.ExecutionProfile == "" {
		t.Fatal("expected restored manifest normalized before install")
	}
	if status := store.cache["tenant_1\x00crm.lookup"]; status != StatusEnabled {
		t.Fatalf("expected runtime cache enabled, got %q", status)
	}
}

func TestStoreBackedServicePersistsCatalogChanges(t *testing.T) {
	store := &memoryStore{}
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, store)
	provider, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:   "tenant_1",
		ProviderID: "crm",
		Name:       "CRM provider",
		Endpoint:   "https://tools.example.test",
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		Name:        "CRM lookup",
		Description: "Look up customers.",
		WhenToUse:   []string{"customer lookup"},
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.providers) != 1 || !reflect.DeepEqual(store.providers[0], provider) {
		t.Fatalf("expected provider persisted, got %#v", store.providers)
	}
	if len(store.manifests) != 1 || store.manifests[0].ExecutionProfile == "" || !reflect.DeepEqual(store.manifests[0], manifest) {
		t.Fatalf("expected normalized manifest persisted, got %#v", store.manifests)
	}
	if status := store.cache["tenant_1\x00crm.lookup"]; status != StatusEnabled {
		t.Fatalf("expected runtime cache enabled, got %q", status)
	}
}

type fakeExecutor struct{}

func (fakeExecutor) Execute(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return map[string]any{"ok": true}, nil, nil
}
