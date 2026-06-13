package catalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	serviceconnection "znt/internal/serviceconnection"
	"znt/internal/tool/registry"
)

type memoryStore struct {
	providers  []ToolProvider
	operations []AdapterOperation
	groups     []ToolGroup
	manifests  []ToolManifest
	cache      map[string]string
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

func (s *memoryStore) UpsertAdapterOperation(_ context.Context, operation AdapterOperation) error {
	for i, existing := range s.operations {
		if existing.TenantID == operation.TenantID && existing.ProviderID == operation.ProviderID && existing.OperationID == operation.OperationID {
			s.operations[i] = operation
			return nil
		}
	}
	s.operations = append(s.operations, operation)
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

func (s *memoryStore) ListAdapterOperations(context.Context) ([]AdapterOperation, error) {
	return append([]AdapterOperation(nil), s.operations...), nil
}

func (s *memoryStore) ListManifests(context.Context) ([]ToolManifest, error) {
	return append([]ToolManifest(nil), s.manifests...), nil
}

func attachProviderConnection(t *testing.T, service *Service, tenantID contracts.TenantID, providerID string, baseURL string, options ...func(*serviceconnection.ServiceConnection)) string {
	t.Helper()
	service.mu.Lock()
	if service.connections == nil {
		service.connections = serviceconnection.NewServiceWithStore(nil)
	}
	connections := service.connections
	service.mu.Unlock()
	connectionID := strings.TrimSpace(providerID) + "_connection"
	connection := serviceconnection.ServiceConnection{
		TenantID:           tenantID,
		ConnectionID:       connectionID,
		Name:               connectionID,
		ConnectionType:     serviceconnection.TypeHTTPAPI,
		Status:             serviceconnection.StatusEnabled,
		BaseURL:            baseURL,
		HealthCheckEnabled: true,
	}
	for _, option := range options {
		option(&connection)
	}
	if _, err := connections.Upsert(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	return connectionID
}

func withConnectionAuthRef(authRef string) func(*serviceconnection.ServiceConnection) {
	return func(connection *serviceconnection.ServiceConnection) {
		connection.AuthType = serviceconnection.AuthTypeAPIKey
		connection.AuthRef = authRef
	}
}

func withConnectionRetry(timeoutMS int, retryMax int) func(*serviceconnection.ServiceConnection) {
	return func(connection *serviceconnection.ServiceConnection) {
		connection.TimeoutMS = timeoutMS
		connection.RetryMax = retryMax
	}
}

func withConnectionNetworkScope(scope string) func(*serviceconnection.ServiceConnection) {
	return func(connection *serviceconnection.ServiceConnection) {
		connection.NetworkScope = scope
	}
}

func withConnectionStatus(status string) func(*serviceconnection.ServiceConnection) {
	return func(connection *serviceconnection.ServiceConnection) {
		connection.Status = status
	}
}

func containsAny(values []any, expected string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == expected {
			return true
		}
	}
	return false
}

func TestUpsertManifestRegistersProviderTool(t *testing.T) {
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

	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		Name:         "CRM lookup",
		Description:  "Look up a customer in CRM.",
		WhenToUse:    []string{"crm", "customer lookup"},
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
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
	var profile struct {
		NetworkPolicy struct {
			AllowedHosts []string `json:"allowed_hosts"`
		} `json:"network_policy"`
	}
	if err := json.Unmarshal([]byte(manifest.ExecutionProfile), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.NetworkPolicy.AllowedHosts) != 1 || profile.NetworkPolicy.AllowedHosts[0] == "" {
		t.Fatalf("expected execution profile to inherit connection base_url host, got %s", manifest.ExecutionProfile)
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

func TestUpsertManifestRegistersAgentPluginServiceTool(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/invoke" {
			t.Fatalf("expected /tools/invoke, got %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["operation"] != "lookup" {
			t.Fatalf("expected lookup operation, got %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{"source": "agent_plugin_service", "arguments": payload["arguments"]},
		})
	}))
	defer remote.Close()

	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_plugin", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm_plugin",
		ProviderType:        ProviderTypeAgentPlugin,
		Name:                "CRM plugin",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.plugin.lookup",
		Name:         "CRM plugin lookup",
		Description:  "Look up a customer through AgentPlugin Service.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeAgentPlugin,
			ProviderID: "crm_plugin",
			Operation:  "lookup",
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Executor.Type != ExecutorTypeAgentPlugin {
		t.Fatalf("expected agent_plugin_service executor, got %s", manifest.Executor.Type)
	}
	if manifest.ExecutionProfile == "" || !strings.Contains(manifest.ExecutionProfile, "provider-http") {
		t.Fatalf("expected HTTP execution profile, got %s", manifest.ExecutionProfile)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.plugin.lookup")
	if !ok {
		t.Fatal("expected agent plugin tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "crm.plugin.lookup",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"customer_id": "c_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output["source"] != "agent_plugin_service" {
		t.Fatalf("unexpected output %#v", output)
	}
}

func TestAgentPluginToolInvokeRejectsTopLevelControlFields(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/invoke" {
			t.Fatalf("expected /tools/invoke, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"ok":true},"run_status":"completed"}`))
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm_plugin",
		ProviderType:        ProviderTypeAgentPlugin,
		Name:                "CRM plugin",
		ServiceConnectionID: attachProviderConnection(t, service, "tenant_1", "crm_plugin", remote.URL),
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.plugin.lookup",
		Name:         "CRM plugin lookup",
		Description:  "Look up a customer through AgentPlugin Service.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeAgentPlugin,
			ProviderID: "crm_plugin",
			Operation:  "lookup",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", "crm.plugin.lookup")
	if !ok {
		t.Fatal("expected agent plugin tool registered")
	}
	_, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "crm.plugin.lookup",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"customer_id": "c_1"},
	})
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeToolExecutionFailed {
		t.Fatalf("expected top-level control field to fail tool execution, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), `unknown field "run_status"`) {
		t.Fatalf("expected unknown field detail, got %v", err)
	}
}

func TestConnectionNetworkScopeFeedsExecutionProfile(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", "https://crm.example.test", withConnectionNetworkScope(`["crm.example.test","api.example.com","*.corp.example"]`))
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		Name:         "CRM lookup",
		Description:  "Look up a customer in CRM.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest.ExecutionProfile, `"api.example.com"`) || !strings.Contains(manifest.ExecutionProfile, `"*.corp.example"`) {
		t.Fatalf("expected execution profile to inherit network_scope allowed hosts, got %s", manifest.ExecutionProfile)
	}
}

func TestListProvidersAndManifestsApplyFilters(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	tenantID := contracts.TenantID("tenant_1")
	crmConnectionID := attachProviderConnection(t, service, tenantID, "crm", "https://crm.example.test")
	billingConnectionID := attachProviderConnection(t, service, tenantID, "billing", "https://billing.example.test")

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            tenantID,
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "Customer tools",
		Description:         "CRM provider",
		ServiceConnectionID: crmConnectionID,
		Status:              StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            tenantID,
		ProviderID:          "billing",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "Billing tools",
		Description:         "Invoices and refunds",
		ServiceConnectionID: billingConnectionID,
		Status:              StatusDraft,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.setProviderHealth(context.Background(), tenantID, "crm", HealthHealthy, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.setProviderHealth(context.Background(), tenantID, "billing", HealthUnhealthy, "timeout"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            tenantID,
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customer.search",
		ToolID:              "customer.search",
		Name:                "Customer search",
		Description:         "Search customers.",
		ServiceConnectionID: crmConnectionID,
		Method:              "GET",
		Path:                "/customers",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
		RiskLevel:           contracts.RiskLow,
		Visibility:          contracts.ToolProtected,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}

	providers := service.ListProvidersFiltered(tenantID, ProviderListFilter{})
	if len(providers) != 2 {
		t.Fatalf("expected public provider list to exclude managed adapters, got %#v", providers)
	}
	providers = service.ListProvidersFiltered(tenantID, ProviderListFilter{ProviderType: ProviderTypeHTTPAPIAdapter})
	if len(providers) != 0 {
		t.Fatalf("expected managed adapter filter to require include managed, got %#v", providers)
	}
	providers = service.ListProvidersFiltered(tenantID, ProviderListFilter{IncludeManaged: true})
	if len(providers) != 3 {
		t.Fatalf("expected include managed provider list, got %#v", providers)
	}

	providers = service.ListProvidersFiltered(tenantID, ProviderListFilter{
		Query:        "customer",
		ProviderType: ProviderTypeStaticToolHost,
		Status:       StatusEnabled,
		HealthStatus: HealthHealthy,
	})
	if len(providers) != 1 || providers[0].ProviderID != "crm" {
		t.Fatalf("expected filtered crm provider, got %#v", providers)
	}
	providers = service.ListProvidersFiltered(tenantID, ProviderListFilter{Cursor: "billing", PageSize: 1})
	if len(providers) != 1 || providers[0].ProviderID != "crm" {
		t.Fatalf("expected cursor after billing to return crm, got %#v", providers)
	}

	for _, manifest := range []ToolManifest{
		{
			TenantID:     tenantID,
			ToolID:       "crm.lookup",
			Name:         "Customer lookup",
			Description:  "Find a customer by id.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			RiskLevel:    contracts.RiskLow,
			Visibility:   contracts.ToolProtected,
			Status:       StatusEnabled,
			Executor: ExecutorSpec{
				Type:       ExecutorTypeStaticToolHost,
				ProviderID: "crm",
				Operation:  "lookup",
			},
		},
		{
			TenantID:     tenantID,
			ToolID:       "crm.export",
			Name:         "Customer export",
			Description:  "Export customer records.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			RiskLevel:    contracts.RiskMedium,
			Visibility:   contracts.ToolExposed,
			Status:       StatusEnabled,
			Executor: ExecutorSpec{
				Type:       ExecutorTypeStaticToolHost,
				ProviderID: "crm",
				Operation:  "export",
			},
		},
		{
			TenantID:     tenantID,
			ToolID:       "billing.refund",
			Name:         "Refund lookup",
			Description:  "Inspect refund state.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			RiskLevel:    contracts.RiskHigh,
			Visibility:   contracts.ToolPrivate,
			Status:       StatusDraft,
			Executor: ExecutorSpec{
				Type:       ExecutorTypeStaticToolHost,
				ProviderID: "billing",
				Operation:  "refund",
			},
		},
	} {
		if _, err := service.UpsertManifest(context.Background(), manifest, "optimizer_1"); err != nil {
			t.Fatal(err)
		}
	}

	manifests := service.ListManifestsFiltered(tenantID, ManifestListFilter{
		ProviderID:   "crm",
		ExecutorType: ExecutorTypeStaticToolHost,
		Status:       StatusEnabled,
	})
	if len(manifests) != 2 || manifests[0].ToolID != "crm.export" || manifests[1].ToolID != "crm.lookup" {
		t.Fatalf("expected two sorted crm manifests, got %#v", manifests)
	}
	manifests = service.ListManifestsFiltered(tenantID, ManifestListFilter{
		Query:      "refund",
		RiskLevel:  contracts.RiskHigh,
		Visibility: contracts.ToolPrivate,
	})
	if len(manifests) != 1 || manifests[0].ToolID != "billing.refund" {
		t.Fatalf("expected refund manifest, got %#v", manifests)
	}
	manifests = service.ListManifestsFiltered(tenantID, ManifestListFilter{Cursor: "billing.refund", PageSize: 1})
	if len(manifests) != 1 || manifests[0].ToolID != "crm.export" {
		t.Fatalf("expected cursor after billing.refund to return crm.export, got %#v", manifests)
	}
}

func TestUpsertManifestRejectsHTTPDirectExecutor(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.delete",
		Name:         "Delete customer",
		Description:  "Delete a customer through direct HTTP.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		RiskLevel:    contracts.RiskHigh,
		Executor: ExecutorSpec{
			Type: "http_direct",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid {
		t.Fatalf("expected http_direct executor rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.delete"); ok {
		t.Fatal("rejected manifest should not be registered")
	}
}

func TestUpsertManifestRejectsHTTPAPIAdapterExecutor(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "customers.search",
		Name:         "Customer search",
		Description:  "Search customers through an HTTP API adapter.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeHTTPAPIAdapter,
			ProviderID: ManagedHTTPAPIAdapterID,
			Operation:  "customers.search",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid {
		t.Fatalf("expected http_api_adapter executor rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.search"); ok {
		t.Fatal("rejected manifest should not be registered")
	}
}

func TestUpsertManifestRejectsDatabaseAdapterExecutor(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "customers.by_status",
		Name:         "Customers by status",
		Description:  "Read customers by status.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeDatabaseAdapter,
			ProviderID: ManagedDatabaseAdapterID,
			Operation:  "customers.by_status",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid {
		t.Fatalf("expected database_adapter executor rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.by_status"); ok {
		t.Fatal("rejected manifest should not be registered")
	}
}

func TestUpsertManifestRequiresOutputSchemaForEnabledTool(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "draftable.lookup",
		Name:        "Draftable lookup",
		Description: "Enabled tools must declare output schema.",
		InputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeAgentTool,
			ProviderID: "agent_provider",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "output_schema") {
		t.Fatalf("expected output_schema rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "draftable.lookup"); ok {
		t.Fatal("invalid manifest should not be registered")
	}
}

func TestUpsertManifestRejectsInvalidJSONSchema(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:    "tenant_1",
		ToolID:      "crm.lookup",
		Name:        "Lookup customer",
		Description: "Lookup customer records.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"customer_id": map[string]any{"type": "string"},
			},
			"required": "customer_id",
		},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "input_schema.required") {
		t.Fatalf("expected invalid input_schema rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("invalid manifest should not be registered")
	}
}

func TestProviderRequiresServiceConnection(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)

	_, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: "missing_connection",
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected service connection service denial, got %T %v", err, err)
	}
}

func TestProviderRejectsMissingServiceConnection(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	service.SetServiceConnections(serviceconnection.NewServiceWithStore(nil))

	_, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: "missing_connection",
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolNotFound {
		t.Fatalf("expected missing service connection rejection, got %T %v", err, err)
	}
}

func TestDisabledServiceConnectionAllowsProviderSaveButBlocksInstall(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", "https://tools.example.test", withConnectionStatus(serviceconnection.StatusDisabled))

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
		},
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "crm.lookup"); ok {
		t.Fatal("disabled service connection should keep provider tool uninstalled")
	}
	if err := service.CheckToolAvailability(context.Background(), "tenant_1", contracts.ToolDefinition{ToolID: "crm.lookup"}); err == nil {
		t.Fatal("expected disabled service connection to deny availability")
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
					"tool_id":       "crm.search",
					"operation":     "search",
					"name":          "CRM search",
					"description":   "Search CRM records.",
					"input_schema":  map[string]any{"type": "object"},
					"output_schema": map[string]any{"type": "object"},
					"risk_level":    "low",
					"visibility":    "protected",
					"version":       "v1",
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

	connectionID := attachProviderConnection(t, service, "", "crm", remote.URL, withConnectionNetworkScope(urlHost(remote.URL)+",crm.internal.example"))
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
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
	if !strings.Contains(manifests[0].ExecutionProfile, `"crm.internal.example"`) {
		t.Fatalf("expected synced manifest to inherit connection network_scope, got %s", manifests[0].ExecutionProfile)
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
			invoked = event.Payload["provider_id"] == "crm" && event.Payload["operation"] == "search" && event.Payload["connection_id"] == connectionID
		case contracts.TraceToolProviderCompleted:
			completed = event.Payload["provider_id"] == "crm" && event.Payload["operation"] == "search" && event.Payload["connection_id"] == connectionID
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
				"tool_key":      "lookup_customer",
				"operation":     "lookup",
				"name":          "CRM lookup",
				"description":   "Look up a customer through a ToolHost.",
				"parameters":    map[string]any{"type": "object"},
				"output_schema": map[string]any{"type": "object"},
				"risk":          "low",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: attachProviderConnection(t, service, "", "crm", remote.URL),
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
		t.Fatalf("expected normalized schemas, got input=%#v output=%#v", manifest.InputSchema, manifest.OutputSchema)
	}
	if manifest.Visibility != contracts.ToolProtected || manifest.Version != "v1" {
		t.Fatalf("expected default visibility/version, got %#v", manifest)
	}
	if len(catalogPaths) < 2 || catalogPaths[0] != "/tools/catalog" || catalogPaths[1] != "/tools" {
		t.Fatalf("expected /tools fallback after /tools/catalog, got %#v", catalogPaths)
	}
}

func TestProviderSyncInstallsAgentPluginCatalog(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tools/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tools": []map[string]any{{
					"tool_id":       "finance.approve",
					"operation":     "approve",
					"name":          "Approve finance request",
					"description":   "Approve a finance request through AgentPlugin Service.",
					"input_schema":  map[string]any{"type": "object"},
					"output_schema": map[string]any{"type": "object"},
					"risk_level":    "high",
					"visibility":    "protected",
				}},
			})
		case "/tools/invoke":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["operation"] != "approve" {
				t.Fatalf("expected approve operation, got %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{"approved": true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "finance_plugin",
		ProviderType:        ProviderTypeAgentPlugin,
		Name:                "Finance AgentPlugin Service",
		ServiceConnectionID: attachProviderConnection(t, service, "tenant_1", "finance_plugin", remote.URL),
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifests, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "finance_plugin", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || manifests[0].Executor.Type != ExecutorTypeAgentPlugin {
		t.Fatalf("expected agent_plugin_service manifest, got %#v", manifests)
	}
	tool, ok := reg.GetForTenant("tenant_1", "finance.approve")
	if !ok {
		t.Fatal("expected agent plugin synced tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		ToolID:    "finance.approve",
		TenantID:  "tenant_1",
		Arguments: map[string]any{"request_id": "r_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output["approved"] != true {
		t.Fatalf("unexpected output %#v", output)
	}
}

func TestUpsertProviderDoesNotAcceptHealthObservationFields(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewService(reg, nil)
	tenantID := contracts.TenantID("tenant_1")
	connectionID := attachProviderConnection(t, service, tenantID, "crm", "https://crm.example.test")

	provider, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            tenantID,
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
		HealthStatus:        HealthHealthy,
		LastHealthError:     "client supplied",
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if provider.HealthStatus != HealthUnknown || provider.LastHealthCheckAt != nil || provider.LastHealthError != "" {
		t.Fatalf("provider upsert should ignore submitted health observation fields, got %#v", provider)
	}

	provider, err = service.setProviderHealth(context.Background(), tenantID, "crm", HealthUnhealthy, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if provider.HealthStatus != HealthUnhealthy || provider.LastHealthCheckAt == nil || provider.LastHealthError != "timeout" {
		t.Fatalf("expected internal health write to persist observation, got %#v", provider)
	}

	updated, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            tenantID,
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider renamed",
		ServiceConnectionID: connectionID,
		HealthStatus:        HealthHealthy,
		LastHealthError:     "overwrite attempt",
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "CRM provider renamed" || updated.HealthStatus != HealthUnhealthy || updated.LastHealthError != "timeout" || updated.LastHealthCheckAt == nil {
		t.Fatalf("provider config upsert should preserve existing health observation, got %#v", updated)
	}

	nextConnectionID := attachProviderConnection(t, service, tenantID, "crm_next", "https://crm-next.example.test")
	updated, err = service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            tenantID,
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider renamed",
		ServiceConnectionID: nextConnectionID,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.HealthStatus != HealthUnknown || updated.LastHealthError != "" || updated.LastHealthCheckAt != nil {
		t.Fatalf("provider upsert should reset health observation when connection scope changes, got %#v", updated)
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
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: attachProviderConnection(t, service, "tenant_1", "crm", remote.URL),
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
					"tool_id":       "crm.retry",
					"operation":     "retry_lookup",
					"name":          "CRM retry",
					"description":   "Retrying CRM lookup.",
					"input_schema":  map[string]any{"type": "object"},
					"output_schema": map[string]any{"type": "object"},
					"risk_level":    "low",
					"visibility":    "protected",
					"version":       "v1",
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
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: attachProviderConnection(t, service, "tenant_1", "crm", remote.URL, withConnectionAuthRef("cred/crm-tools"), withConnectionRetry(1000, 1)),
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

	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertGroup(context.Background(), ToolGroup{
		TenantID: "tenant_1",
		GroupID:  "crm",
		Name:     "CRM",
		Status:   StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		GroupID:      "crm",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
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
		ToolID:       "crm.global",
		GroupID:      "crm",
		Name:         "Global CRM",
		Description:  "Global CRM helper.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
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
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", "https://tools.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		GroupID:      "crm",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Executor: ExecutorSpec{
			Type:       ExecutorTypeStaticToolHost,
			ProviderID: "crm",
			Operation:  "lookup",
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

	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
		Status:              StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		GroupID:      "crm",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
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
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
		Status:              StatusDisabled,
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

	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
		Status:              StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
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
						"outputSchema": map[string]any{"type": "object"},
						"annotations":  map[string]any{"readOnlyHint": true},
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

	connectionID := attachProviderConnection(t, service, "tenant_1", "calc", remote.URL+"/mcp", withConnectionAuthRef("auth_ref_1"))
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "calc",
		ProviderType:        ProviderTypeMCP,
		Name:                "Calculator MCP",
		ServiceConnectionID: connectionID,
		Status:              StatusEnabled,
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
	if output["total"] != float64(5) || callCalls != 1 || listCalls < 2 {
		t.Fatalf("unexpected MCP execution output=%#v listCalls=%d callCalls=%d", output, listCalls, callCalls)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_mcp_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != contracts.TraceToolProviderInvoked || events[1].Type != contracts.TraceToolProviderCompleted {
		t.Fatalf("expected MCP provider invoke/completed trace, got %#v", events)
	}
	for _, event := range events {
		if event.Payload["provider_id"] != "calc" || event.Payload["provider_type"] != ProviderTypeMCP || event.Payload["operation"] != "sum" || event.Payload["connection_id"] != connectionID {
			t.Fatalf("expected MCP trace to include provider/operation/connection, got %#v", event.Payload)
		}
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

func TestMCPExecutorKeepsContentWhenStructuredContentMissing(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"content":[{"type":"text","text":"hello"}]}}`))
	}))
	defer remote.Close()
	executor := MCPExecutor{
		Endpoint:   remote.URL,
		ProviderID: "calc",
		Operation:  "echo",
		Client:     remote.Client(),
		TenantID:   "tenant_1",
	}
	output, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID:  "tenant_1",
		ToolID:    "calc.echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := output["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["text"] != "hello" {
		t.Fatalf("expected MCP content output, got %#v", output)
	}
}

func TestHTTPAPIAdapterOperationPublishesAndExecutes(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/customers/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get(providerAuthRefHeader); got != "secret://crm" {
			t.Fatalf("expected auth ref header, got %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["email"] != "a@example.com" {
			t.Fatalf("unexpected body %#v", body)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"customer_id": "cus_1", "matched": true})
	}))
	defer remote.Close()

	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	traceRecorder := trace.NewInMemoryRecorder()
	service.SetTraceRecorder(traceRecorder)
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", remote.URL, withConnectionAuthRef("secret://crm"))
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodPost,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, operation.OperationID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Executor.Type != ExecutorTypeHTTPAPIAdapter || manifest.Executor.Operation != operation.OperationID {
		t.Fatalf("unexpected manifest executor %#v", manifest.Executor)
	}
	if host := urlHost(remote.URL); host == "" || !strings.Contains(manifest.ExecutionProfile, host) {
		t.Fatalf("expected adapter manifest to inherit connection base_url host, got %s", manifest.ExecutionProfile)
	}
	tool, ok := reg.GetForTenant("tenant_1", "customers.search")
	if !ok {
		t.Fatal("expected adapter tool registered")
	}
	out, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		TenantID:   "tenant_1",
		ToolID:     "customers.search",
		ToolCallID: "call_customers_search",
		Arguments:  map[string]any{"email": "a@example.com"},
		TraceID:    "trace_http_api_adapter_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["customer_id"] != "cus_1" || out["matched"] != true {
		t.Fatalf("unexpected output %#v", out)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_http_api_adapter_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != contracts.TraceToolProviderInvoked || events[1].Type != contracts.TraceToolProviderCompleted {
		t.Fatalf("expected HTTP adapter invoke/completed trace, got %#v", events)
	}
	for _, event := range events {
		if event.Payload["provider_id"] != ManagedHTTPAPIAdapterID || event.Payload["operation"] != operation.OperationID || event.Payload["connection_id"] != connectionID {
			t.Fatalf("expected HTTP adapter trace to include provider/operation/connection, got %#v", event.Payload)
		}
	}
}

func TestHTTPAPIAdapterRecordsProviderFailureForMappingErrors(t *testing.T) {
	traceRecorder := trace.NewInMemoryRecorder()
	executor := HTTPAPIAdapterExecutor{
		Endpoint:   "https://crm.example.test",
		ProviderID: ManagedHTTPAPIAdapterID,
		Operation: AdapterOperation{
			OperationID:         "customers.get",
			ServiceConnectionID: "crm_http",
			Method:              http.MethodGet,
			Path:                "/customers/{customer_id}",
		},
		TenantID: "tenant_1",
		Trace:    traceRecorder,
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
	}
	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID:   "tenant_1",
		ToolID:     "customers.get",
		ToolCallID: "call_customers_get",
		TraceID:    "trace_http_api_adapter_mapping_error",
		Arguments:  map[string]any{},
	})
	if err == nil {
		t.Fatal("expected unresolved path parameter error")
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_http_api_adapter_mapping_error")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != contracts.TraceToolProviderInvoked || events[1].Type != contracts.TraceToolProviderFailed {
		t.Fatalf("expected HTTP adapter invoke/failed trace, got %#v", events)
	}
	if events[1].Payload["provider_id"] != ManagedHTTPAPIAdapterID || events[1].Payload["operation"] != "customers.get" ||
		events[1].Payload["connection_id"] != "crm_http" || events[1].Payload["error_code"] != string(contracts.CodeToolArgumentInvalid) {
		t.Fatalf("unexpected failed trace payload %#v", events[1].Payload)
	}
}

func TestAdapterOperationLifecycleAudits(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer remote.Close()

	reg := registry.NewInMemoryRegistry()
	auditLogger := audit.NewInMemoryLogger()
	service := NewServiceWithStore(reg, auditLogger, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.audit",
		ToolID:              "customers.audit",
		Name:                "Audit customers",
		Description:         "Audit customer operation lifecycle.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodPost,
		Path:                "/customers/audit",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestAdapterOperation(context.Background(), "tenant_1", operation, map[string]any{"email": "a@example.com"}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, operation.OperationID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	events, err := auditLogger.Search(context.Background(), audit.Filter{
		TenantID:     "tenant_1",
		ResourceType: auditResourceToolAdapterOperation,
		ResourceID:   operation.OperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]contracts.AuditEvent{}
	for _, event := range events {
		seen[event.Action] = event
	}
	for _, action := range []string{
		contracts.AuditToolAdapterOperationUpserted,
		contracts.AuditToolAdapterOperationTested,
		contracts.AuditToolAdapterOperationPublished,
	} {
		event, ok := seen[action]
		if !ok {
			t.Fatalf("expected audit action %s in %#v", action, events)
		}
		if event.ActorID != "optimizer_1" || event.Decision != "allowed" {
			t.Fatalf("unexpected audit event for %s: %#v", action, event)
		}
	}
	if seen[contracts.AuditToolAdapterOperationPublished].Reason != operation.ToolID {
		t.Fatalf("expected publish audit reason to include tool id, got %#v", seen[contracts.AuditToolAdapterOperationPublished])
	}
}

func TestHTTPAPIAdapterAppliesRequestAndResponseMapping(t *testing.T) {
	var seenBody map[string]any
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/customers/search" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		filters, _ := seenBody["filters"].(map[string]any)
		if filters["email"] != "a@example.com" {
			t.Fatalf("unexpected mapped request body %#v", seenBody)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"customer":{"id":"cus_1","active":true}}}`))
	}))
	defer remote.Close()

	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodPost,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string"}}, "required": []string{"email"}},
		OutputSchema:        map[string]any{"type": "object", "properties": map[string]any{"customer_id": map[string]any{"type": "string"}, "active": map[string]any{"type": "boolean"}}},
		RequestMapping: map[string]any{
			"body": map[string]any{
				"filters": map[string]any{
					"email": "email",
				},
			},
		},
		ResponseMapping: map[string]any{
			"body_path": "data.customer",
			"output": map[string]any{
				"customer_id": "selected.id",
				"active":      "selected.active",
			},
		},
		Status: StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, operation.OperationID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", manifest.ToolID)
	if !ok {
		t.Fatal("expected adapter tool registered")
	}
	out, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		TenantID:  "tenant_1",
		ToolID:    manifest.ToolID,
		Arguments: map[string]any{"email": "a@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["customer_id"] != "cus_1" || out["active"] != true {
		t.Fatalf("unexpected mapped output %#v", out)
	}
}

func TestHTTPAPIAdapterAppliesPathParamsQueryAndHeaderMapping(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/customers/cus 1/orders" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Fatalf("expected mapped state query, got %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Workspace"); got != "workspace_1" {
			t.Fatalf("expected mapped workspace header, got %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"orders":[{"id":"ord_1"}]}}`))
	}))
	defer remote.Close()

	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", remote.URL)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.orders.list",
		ToolID:              "customers.orders.list",
		Name:                "List customer orders",
		Description:         "List customer orders by customer ID.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/{customer_id}/orders",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"customer":  map[string]any{"type": "object"},
			"state":     map[string]any{"type": "string"},
			"workspace": map[string]any{"type": "string"},
		}},
		OutputSchema: map[string]any{"type": "object"},
		RequestMapping: map[string]any{
			"path_params": map[string]any{
				"customer_id": "customer.id",
			},
			"query": map[string]any{
				"state": "state",
			},
			"headers": map[string]any{
				"X-Workspace": "workspace",
			},
		},
		ResponseMapping: map[string]any{
			"body_path": "data",
		},
		Status: StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, operation.OperationID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.GetForTenant("tenant_1", manifest.ToolID)
	if !ok {
		t.Fatal("expected adapter tool registered")
	}
	out, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		ToolID:   manifest.ToolID,
		Arguments: map[string]any{
			"customer":  map[string]any{"id": "cus 1"},
			"state":     "open",
			"workspace": "workspace_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	orders, ok := out["orders"].([]any)
	if !ok || len(orders) != 1 {
		t.Fatalf("unexpected mapped output %#v", out)
	}
}

func TestHTTPAPIAdapterRequiresPathParamMapping(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.get",
		ToolID:              "customers.get",
		Name:                "Get customer",
		Description:         "Get customer by ID.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/{customer_id}",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "path_params") {
		t.Fatalf("expected path_params rejection, got %T %v", err, err)
	}
}

func TestHTTPAPIAdapterOperationFromDiscoveredResource(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: serviceconnection.TypeHTTPAPI,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "https://crm.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "crm_api", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "GET /customers/{customer_id}/tickets",
		ResourceType: "http_operation",
		Name:         "List customer tickets",
		Schema: map[string]any{
			"method":       "GET",
			"path":         "/customers/{customer_id}/tickets",
			"operation_id": "customers.tickets.list",
			"summary":      "List customer tickets",
			"description":  "List support tickets for a customer.",
			"parameters": []any{
				map[string]any{"name": "customer_id", "in": "path", "schema": map[string]any{"type": "string"}},
				map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string"}},
				map[string]any{"name": "X-Workspace", "in": "header", "schema": map[string]any{"type": "string"}},
			},
			"responses": map[string]any{
				"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					},
				}}}},
			},
		},
		Metadata: map[string]any{
			"source":       "openapi",
			"operation_id": "customers.tickets.list",
			"method":       "GET",
			"path":         "/customers/{customer_id}/tickets",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)

	operation, err := service.UpsertHTTPAPIAdapterOperationFromResource(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, AdapterOperationFromResourceRequest{
		ServiceConnectionID: "crm_api",
		ResourceID:          "GET /customers/{customer_id}/tickets",
		Status:              StatusDraft,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if operation.OperationID != "customers_tickets_list" || operation.ToolID != "customers_tickets_list" || operation.Method != http.MethodGet || operation.Path != "/customers/{customer_id}/tickets" {
		t.Fatalf("unexpected generated operation: %#v", operation)
	}
	pathParams, _ := operation.RequestMapping["path_params"].(map[string]any)
	query, _ := operation.RequestMapping["query"].(map[string]any)
	headers, _ := operation.RequestMapping["headers"].(map[string]any)
	if pathParams["customer_id"] != "customer_id" || query["status"] != "status" || headers["X-Workspace"] != "X-Workspace" {
		t.Fatalf("unexpected generated mapping: %#v", operation.RequestMapping)
	}
	required, _ := operation.InputSchema["required"].([]any)
	if !containsAny(required, "customer_id") {
		t.Fatalf("expected path parameter to be required, got %#v", operation.InputSchema)
	}
	properties, _ := operation.OutputSchema["properties"].(map[string]any)
	if _, ok := properties["items"]; !ok {
		t.Fatalf("expected output schema from response, got %#v", operation.OutputSchema)
	}
	if _, ok := reg.GetForTenant("tenant_1", operation.ToolID); ok {
		t.Fatal("draft generated operation should not register a runtime tool")
	}
}

func TestDatabaseAdapterOperationFromDiscoveredResource(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "warehouse_connection",
		Name:           "Warehouse",
		ConnectionType: serviceconnection.TypeDatabase,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "postgres://warehouse.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "warehouse_connection", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "public.customers",
		ResourceType: "table",
		Name:         "public.customers",
		Schema: map[string]any{
			"type": "object",
			"columns": []map[string]any{
				{"name": "id", "json_schema": map[string]any{"type": "integer"}},
				{"name": "email", "json_schema": map[string]any{"type": "string"}},
			},
		},
		Metadata: map[string]any{
			"schema":     "public",
			"table_name": "customers",
			"driver":     "postgres",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)

	operation, err := service.UpsertAdapterOperationFromResource(context.Background(), "tenant_1", ManagedDatabaseAdapterID, AdapterOperationFromResourceRequest{
		ServiceConnectionID: "warehouse_connection",
		ResourceID:          "public.customers",
		OperationID:         "customers.read",
		ToolID:              "customers.read",
		RedactColumns:       []string{"email"},
		Status:              StatusDraft,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if operation.ProviderID != ManagedDatabaseAdapterID || operation.OperationID != "customers.read" || operation.ToolID != "customers.read" {
		t.Fatalf("unexpected generated operation identity: %#v", operation)
	}
	if operation.ResourceID != "public.customers" || operation.QueryTemplate != `select * from "public"."customers" limit 100` || !operation.ReadOnly {
		t.Fatalf("unexpected generated database operation: %#v", operation)
	}
	if operation.ParameterSchema["type"] != "object" || operation.InputSchema["type"] != "object" {
		t.Fatalf("expected object schemas, got input=%#v parameter=%#v", operation.InputSchema, operation.ParameterSchema)
	}
	properties, _ := operation.OutputSchema["properties"].(map[string]any)
	rowsSchema, _ := properties["rows"].(map[string]any)
	rowItems, _ := rowsSchema["items"].(map[string]any)
	rowProperties, _ := rowItems["properties"].(map[string]any)
	if rowProperties["id"].(map[string]any)["type"] != "integer" || rowProperties["email"].(map[string]any)["type"] != "string" {
		t.Fatalf("expected output schema columns from resource, got %#v", operation.OutputSchema)
	}
	if len(operation.RedactColumns) != 1 || operation.RedactColumns[0] != "email" {
		t.Fatalf("expected generated operation to preserve normalized redact columns, got %#v", operation.RedactColumns)
	}
	if _, ok := reg.GetForTenant("tenant_1", operation.ToolID); ok {
		t.Fatal("draft generated database operation should not register a runtime tool")
	}
}

func TestAdapterOperationRequiresOutputSchemaForEnabledOperation(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "output_schema") {
		t.Fatalf("expected output_schema rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.search"); ok {
		t.Fatal("invalid adapter operation should not register a tool")
	}
}

func TestAdapterOperationRejectsInvalidJSONSchema(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"email": map[string]any{"type": "banana"},
			},
		},
		OutputSchema: map[string]any{"type": "object"},
		Status:       StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "unsupported JSON schema type") {
		t.Fatalf("expected invalid adapter schema rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.search"); ok {
		t.Fatal("invalid adapter operation should not register a tool")
	}
}

func TestHTTPAPIAdapterOperationRejectsDatabaseFields(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		QueryTemplate:       "select id from customers",
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || runtimeErr.Details["field"] != "query_template" {
		t.Fatalf("expected HTTP adapter to reject database field, got %T %v", err, err)
	}
}

func TestHTTPAPIAdapterOperationDisableUnregistersPublishedTool(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedHTTPAPIAdapterID,
		ProviderType: ProviderTypeHTTPAPIAdapter,
		Name:         "Managed HTTP",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedHTTPAPIAdapterID, operation.OperationID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.search"); !ok {
		t.Fatal("expected adapter tool registered")
	}
	operation.Status = StatusDisabled
	if _, err := service.UpsertAdapterOperation(context.Background(), operation, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.search"); ok {
		t.Fatal("expected disabled adapter operation to unregister published tool")
	}
}

func TestManagedAdapterProviderRejectsProviderLevelServiceConnection(t *testing.T) {
	service := NewServiceWithStore(registry.NewInMemoryRegistry(), nil, &memoryStore{})
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		ProviderType:        ProviderTypeHTTPAPIAdapter,
		Name:                "Managed HTTP",
		ServiceConnectionID: "wrong-layer",
		Status:              StatusEnabled,
	}, "optimizer_1"); err == nil {
		t.Fatal("expected managed adapter provider to reject provider-level service_connection_id")
	}
}

func TestAdapterOperationAutoEnsuresManagedProvider(t *testing.T) {
	service := NewServiceWithStore(registry.NewInMemoryRegistry(), nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")

	if _, ok := service.GetProvider("tenant_1", ManagedHTTPAPIAdapterID); ok {
		t.Fatal("managed provider should not exist before the first adapter operation")
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedHTTPAPIAdapterID,
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := service.GetProvider("tenant_1", ManagedHTTPAPIAdapterID)
	if !ok {
		t.Fatal("expected adapter operation to create the managed HTTP API adapter provider")
	}
	if provider.ProviderType != ProviderTypeHTTPAPIAdapter || provider.ServiceConnectionID != "" {
		t.Fatalf("unexpected managed provider after operation upsert: %#v", provider)
	}
	if operation.ProviderID != ManagedHTTPAPIAdapterID {
		t.Fatalf("unexpected operation provider id: %#v", operation)
	}
}

func TestAdapterOperationRejectsNonCanonicalManagedProviderID(t *testing.T) {
	service := NewServiceWithStore(registry.NewInMemoryRegistry(), nil, &memoryStore{})
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm_http", "https://crm.example.test")

	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          "crm-http-adapter",
		OperationID:         "customers.search",
		ToolID:              "customers.search",
		Name:                "Search customers",
		Description:         "Search customers by email.",
		ServiceConnectionID: connectionID,
		Method:              http.MethodGet,
		Path:                "/customers/search",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "managed adapter provider") {
		t.Fatalf("expected canonical managed provider id rejection, got %T %v", err, err)
	}
}

func TestDatabaseAdapterOperationPublishesAndExecutesReadOnlyQuery(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	scenarioName := registerCatalogDBScenario(t, catalogDBScenario{
		columns: []string{"id", "name", "email"},
		rows: [][]driver.Value{
			{int64(1), "Alice", "alice@example.com"},
			{int64(2), "Bob", "bob@example.com"},
		},
	})
	service.openDB = func(driverName string, dataSourceName string) (*sql.DB, error) {
		if driverName != "pgx" {
			t.Fatalf("expected pgx driver, got %s", driverName)
		}
		return sql.Open(catalogTestDBDriverName, scenarioName)
	}
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "warehouse_connection",
		Name:           "Warehouse",
		ConnectionType: serviceconnection.TypeDatabase,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "postgres://warehouse.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "warehouse_connection", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "public.customers",
		ResourceType: "table",
		Name:         "public.customers",
		Schema: map[string]any{
			"type": "object",
			"columns": []map[string]any{
				{"name": "id", "json_schema": map[string]any{"type": "integer"}},
				{"name": "name", "json_schema": map[string]any{"type": "string"}},
				{"name": "email", "json_schema": map[string]any{"type": "string"}},
			},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)

	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedDatabaseAdapterID,
		ProviderType: ProviderTypeDatabaseAdapter,
		Name:         "Managed Database",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	operation, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedDatabaseAdapterID,
		OperationID:         "customers.by_status",
		ToolID:              "customers.by_status",
		Name:                "Customers by status",
		Description:         "Read customers by status.",
		ServiceConnectionID: "warehouse_connection",
		ResourceID:          "public.customers",
		QueryTemplate:       "select id, name from public.customers where status = :status limit :limit",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		ParameterSchema:     map[string]any{"type": "object"},
		MaxRows:             10,
		RedactColumns:       []string{" email ", "EMAIL"},
		ReadOnly:            true,
		Status:              StatusEnabled,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	testOutput, err := service.TestAdapterOperation(context.Background(), "tenant_1", operation, map[string]any{"status": "active", "limit": 2}, "optimizer_1")
	if err != nil {
		t.Fatalf("test database adapter operation: %v", err)
	}
	if testOutput["row_count"] != 2 {
		t.Fatalf("expected two rows from adapter test, got %#v", testOutput)
	}
	published, err := service.PublishAdapterOperation(context.Background(), "tenant_1", ManagedDatabaseAdapterID, operation.OperationID, "optimizer_1")
	if err != nil {
		t.Fatalf("publish database adapter operation: %v", err)
	}
	if published.Executor.Type != ExecutorTypeDatabaseAdapter {
		t.Fatalf("expected database_adapter executor, got %#v", published.Executor)
	}
	tool, ok := reg.GetForTenant("tenant_1", "customers.by_status")
	if !ok {
		t.Fatal("expected database adapter tool registered")
	}
	output, _, err := tool.Executor.Execute(context.Background(), contracts.ToolCall{
		TenantID:  "tenant_1",
		ToolID:    "customers.by_status",
		Arguments: map[string]any{"status": "active", "limit": 2},
	})
	if err != nil {
		t.Fatalf("execute database adapter tool: %v", err)
	}
	rows, ok := output["rows"].([]map[string]any)
	if !ok || len(rows) != 2 || rows[0]["name"] != "Alice" || rows[0]["email"] != "[REDACTED]" {
		t.Fatalf("unexpected database rows: %#v", output["rows"])
	}
	if rows[1]["email"] != "[REDACTED]" {
		t.Fatalf("expected configured database columns to be redacted, got %#v", rows[1])
	}
	if len(operation.RedactColumns) != 1 || operation.RedactColumns[0] != "email" {
		t.Fatalf("expected normalized redact columns, got %#v", operation.RedactColumns)
	}
	scenarioValue, _ := catalogDBScenarios.Load(scenarioName)
	scenario := scenarioValue.(*catalogDBScenario)
	if scenario.lastQuery != "select id, name from public.customers where status = $1 limit $2" {
		t.Fatalf("unexpected query: %s", scenario.lastQuery)
	}
	if len(scenario.lastArgs) != 2 || scenario.lastArgs[0].Value != "active" || fmt.Sprint(scenario.lastArgs[1].Value) != "2" {
		t.Fatalf("unexpected args: %#v", scenario.lastArgs)
	}
	operation.QueryTemplate = "delete from customers where status = :status"
	if _, err := service.UpsertAdapterOperation(context.Background(), operation, "optimizer_1"); err == nil {
		t.Fatal("expected write SQL to be rejected")
	}
	operation.QueryTemplate = "select id, name from public.orders where status = :status"
	_, err = service.UpsertAdapterOperation(context.Background(), operation, "optimizer_1")
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeToolPolicyDenied || !strings.Contains(runtimeErr.Message, "bound resource_id") {
		t.Fatalf("expected bound resource query rejection, got %T %v", err, err)
	}
}

func TestDatabaseAdapterOperationRequiresDiscoveredResource(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "warehouse_connection",
		Name:           "Warehouse",
		ConnectionType: serviceconnection.TypeDatabase,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "postgres://warehouse.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "warehouse_connection", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "public.orders",
		ResourceType: "table",
		Name:         "public.orders",
		Schema: map[string]any{
			"type": "object",
			"columns": []map[string]any{
				{"name": "id", "json_schema": map[string]any{"type": "integer"}},
			},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedDatabaseAdapterID,
		ProviderType: ProviderTypeDatabaseAdapter,
		Name:         "Managed Database",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedDatabaseAdapterID,
		OperationID:         "customers.by_status",
		ToolID:              "customers.by_status",
		Name:                "Customers by status",
		Description:         "Read customers by status.",
		ServiceConnectionID: "warehouse_connection",
		ResourceID:          "public.customers",
		QueryTemplate:       "select id from customers",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		ParameterSchema:     map[string]any{"type": "object"},
		ReadOnly:            true,
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "resource_id") {
		t.Fatalf("expected discovered resource rejection, got %T %v", err, err)
	}
	if _, ok := reg.GetForTenant("tenant_1", "customers.by_status"); ok {
		t.Fatal("invalid database adapter operation should not register a tool")
	}
}

func TestDatabaseAdapterOperationRejectsHTTPFields(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "warehouse_connection",
		Name:           "Warehouse",
		ConnectionType: serviceconnection.TypeDatabase,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "postgres://warehouse.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "warehouse_connection", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "public.customers",
		ResourceType: "table",
		Name:         "public.customers",
		Schema: map[string]any{
			"type": "object",
			"columns": []map[string]any{
				{"name": "id", "json_schema": map[string]any{"type": "integer"}},
			},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedDatabaseAdapterID,
		ProviderType: ProviderTypeDatabaseAdapter,
		Name:         "Managed Database",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedDatabaseAdapterID,
		OperationID:         "customers.by_status",
		ToolID:              "customers.by_status",
		Name:                "Customers by status",
		Description:         "Read customers by status.",
		ServiceConnectionID: "warehouse_connection",
		Method:              http.MethodPost,
		ResourceID:          "public.customers",
		QueryTemplate:       "select id from customers where status = :status",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		ParameterSchema:     map[string]any{"type": "object"},
		ReadOnly:            true,
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || runtimeErr.Details["field"] != "method" {
		t.Fatalf("expected database adapter to reject HTTP field, got %T %v", err, err)
	}
}

func TestDatabaseAdapterOperationValidatesRedactColumns(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, &memoryStore{})
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "warehouse_connection",
		Name:           "Warehouse",
		ConnectionType: serviceconnection.TypeDatabase,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        "postgres://warehouse.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := connections.ReplaceResources(context.Background(), "tenant_1", "warehouse_connection", []serviceconnection.ServiceConnectionResource{{
		ResourceID:   "public.customers",
		ResourceType: "table",
		Name:         "public.customers",
		Schema: map[string]any{
			"type": "object",
			"columns": []map[string]any{
				{"name": "id", "json_schema": map[string]any{"type": "integer"}},
			},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)
	if _, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:     "tenant_1",
		ProviderID:   ManagedDatabaseAdapterID,
		ProviderType: ProviderTypeDatabaseAdapter,
		Name:         "Managed Database",
		Status:       StatusEnabled,
	}, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	_, err := service.UpsertAdapterOperation(context.Background(), AdapterOperation{
		TenantID:            "tenant_1",
		ProviderID:          ManagedDatabaseAdapterID,
		OperationID:         "customers.by_status",
		ToolID:              "customers.by_status",
		Name:                "Customers by status",
		Description:         "Read customers by status.",
		ServiceConnectionID: "warehouse_connection",
		ResourceID:          "public.customers",
		QueryTemplate:       "select id, email from public.customers",
		InputSchema:         map[string]any{"type": "object"},
		OutputSchema:        map[string]any{"type": "object"},
		ParameterSchema:     map[string]any{"type": "object"},
		RedactColumns:       []string{"email"},
		ReadOnly:            true,
		Status:              StatusEnabled,
	}, "optimizer_1")
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeToolArgumentInvalid || !strings.Contains(runtimeErr.Message, "redact_columns") {
		t.Fatalf("expected redact_columns schema rejection, got %T %v", err, err)
	}
}

func TestBuildReadOnlySQLIgnoresPostgresTypeCasts(t *testing.T) {
	query, args, err := buildReadOnlySQL(AdapterOperation{
		OperationID:   "orders.by_day",
		QueryTemplate: "select created_at::date as day from orders where status = :status",
	}, map[string]any{"status": "paid"})
	if err != nil {
		t.Fatal(err)
	}
	if query != "select created_at::date as day from orders where status = $1" {
		t.Fatalf("unexpected query: %s", query)
	}
	if len(args) != 1 || args[0] != "paid" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestDatabaseQueryRelationIdentifiers(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "schema qualified",
			query: "select id from public.customers where status = :status",
			want:  []string{"public.customers"},
		},
		{
			name:  "quoted join",
			query: `select c.id from "public"."customers" c join public.orders o on o.customer_id = c.id`,
			want:  []string{"public.customers", "public.orders"},
		},
		{
			name:  "comma join",
			query: "select c.id from public.customers c, public.orders o where o.customer_id = c.id",
			want:  []string{"public.customers", "public.orders"},
		},
		{
			name:  "cte reference skipped",
			query: "with scoped as (select id from public.customers) select id from scoped",
			want:  []string{"public.customers"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := databaseQueryRelationIdentifiers(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected relations: got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestRestoreInstallsEnabledPersistedTools(t *testing.T) {
	store := &memoryStore{
		providers: []ToolProvider{{
			TenantID:            "tenant_1",
			ProviderID:          "crm",
			ProviderType:        ProviderTypeStaticToolHost,
			Name:                "CRM provider",
			ServiceConnectionID: "crm_connection",
			Status:              StatusEnabled,
			Version:             "v1",
		}},
		manifests: []ToolManifest{{
			TenantID:     "tenant_1",
			ToolID:       "crm.lookup",
			Name:         "CRM lookup",
			Description:  "Look up customers.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
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
	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:           "tenant_1",
		ConnectionID:       "crm_connection",
		Name:               "CRM connection",
		ConnectionType:     serviceconnection.TypeHTTPAPI,
		Status:             serviceconnection.StatusEnabled,
		BaseURL:            "https://tools.example.test",
		HealthCheckEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	service.SetServiceConnections(connections)

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
	if !strings.Contains(tool.Definition.ExecutionProfile, "tools.example.test") {
		t.Fatalf("expected restored install to inherit service connection network boundary, got %s", tool.Definition.ExecutionProfile)
	}
	if status := store.cache["tenant_1\x00crm.lookup"]; status != StatusEnabled {
		t.Fatalf("expected runtime cache enabled, got %q", status)
	}
}

func TestStoreBackedServicePersistsCatalogChanges(t *testing.T) {
	store := &memoryStore{}
	reg := registry.NewInMemoryRegistry()
	service := NewServiceWithStore(reg, nil, store)
	connectionID := attachProviderConnection(t, service, "tenant_1", "crm", "https://tools.example.test")
	provider, err := service.UpsertProvider(context.Background(), ToolProvider{
		TenantID:            "tenant_1",
		ProviderID:          "crm",
		ProviderType:        ProviderTypeStaticToolHost,
		Name:                "CRM provider",
		ServiceConnectionID: connectionID,
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.UpsertManifest(context.Background(), ToolManifest{
		TenantID:     "tenant_1",
		ToolID:       "crm.lookup",
		Name:         "CRM lookup",
		Description:  "Look up customers.",
		WhenToUse:    []string{"customer lookup"},
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
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

const catalogTestDBDriverName = "znt_catalog_test_db"

var catalogDBDriverOnce sync.Once
var catalogDBScenarioSeq atomic.Int64
var catalogDBScenarios sync.Map

type catalogDBScenario struct {
	columns   []string
	queryErr  error
	rows      [][]driver.Value
	lastQuery string
	lastArgs  []driver.NamedValue
}

func registerCatalogDBScenario(t *testing.T, scenario catalogDBScenario) string {
	t.Helper()
	catalogDBDriverOnce.Do(func() {
		sql.Register(catalogTestDBDriverName, catalogTestDriver{})
	})
	name := "scenario_" + strings.ReplaceAll(t.Name(), "/", "_") + "_" + fmt.Sprint(catalogDBScenarioSeq.Add(1))
	catalogDBScenarios.Store(name, &scenario)
	t.Cleanup(func() {
		catalogDBScenarios.Delete(name)
	})
	return name
}

type catalogTestDriver struct{}

func (catalogTestDriver) Open(name string) (driver.Conn, error) {
	value, ok := catalogDBScenarios.Load(name)
	if !ok {
		return nil, errors.New("unknown catalog db scenario")
	}
	return &catalogTestConn{scenario: value.(*catalogDBScenario)}, nil
}

type catalogTestConn struct {
	scenario *catalogDBScenario
}

func (c *catalogTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("catalog test driver does not support prepared statements")
}

func (c *catalogTestConn) Close() error {
	return nil
}

func (c *catalogTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("catalog test driver does not support transactions")
}

func (c *catalogTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.scenario.lastQuery = query
	c.scenario.lastArgs = append([]driver.NamedValue(nil), args...)
	if c.scenario.queryErr != nil {
		return nil, c.scenario.queryErr
	}
	columns := c.scenario.columns
	if len(columns) == 0 {
		columns = []string{"id", "name"}
	}
	return &catalogTestRows{
		columns: columns,
		rows:    c.scenario.rows,
	}, nil
}

type catalogTestRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *catalogTestRows) Columns() []string {
	return r.columns
}

func (r *catalogTestRows) Close() error {
	return nil
}

func (r *catalogTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
