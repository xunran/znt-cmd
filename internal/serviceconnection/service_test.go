package serviceconnection

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"znt/internal/contracts"
)

func TestServiceConnectionCRUDAndFilters(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")

	created, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		Environment:    "prod",
		BaseURL:        "https://crm.example.test",
	})
	if err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if created.ConnectionID != "crm_api" || created.Environment != "prod" {
		t.Fatalf("unexpected connection: %+v", created)
	}

	got, ok, err := service.Get(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if !ok || got.Name != "CRM API" {
		t.Fatalf("expected stored connection, got ok=%v connection=%+v", ok, got)
	}

	filtered, err := service.List(ctx, tenantID, ListFilter{ConnectionType: TypeHTTPAPI, Status: StatusDraft, Environment: "prod"})
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ConnectionID != "crm_api" {
		t.Fatalf("unexpected filtered connections: %+v", filtered)
	}

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "billing_db",
		Name:           "Billing DB",
		ConnectionType: TypeDatabase,
		Status:         StatusDraft,
		Environment:    "prod",
		BaseURL:        "postgres://billing.example.test/db",
	}); err != nil {
		t.Fatalf("upsert billing connection: %v", err)
	}
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "support_api",
		Name:           "Support API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		Environment:    "staging",
		BaseURL:        "https://support.example.test",
	}); err != nil {
		t.Fatalf("upsert support connection: %v", err)
	}
	if _, err := service.setConnectionHealth(ctx, tenantID, "support_api", HealthHealthy, ""); err != nil {
		t.Fatalf("set support health: %v", err)
	}

	filtered, err = service.List(ctx, tenantID, ListFilter{Query: "support", ConnectionType: TypeHTTPAPI})
	if err != nil {
		t.Fatalf("list query connections: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ConnectionID != "support_api" {
		t.Fatalf("unexpected query filtered connections: %+v", filtered)
	}
	filtered, err = service.List(ctx, tenantID, ListFilter{HealthStatus: HealthHealthy})
	if err != nil {
		t.Fatalf("list health filtered connections: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ConnectionID != "support_api" {
		t.Fatalf("unexpected health filtered connections: %+v", filtered)
	}
	filtered, err = service.List(ctx, tenantID, ListFilter{Cursor: "billing_db", PageSize: 1})
	if err != nil {
		t.Fatalf("list paged connections: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ConnectionID != "crm_api" {
		t.Fatalf("unexpected paged connections: %+v", filtered)
	}

	if _, err := service.Enable(ctx, tenantID, "crm_api"); err != nil {
		t.Fatalf("enable connection: %v", err)
	}
	enabled, ok, err := service.Get(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("get enabled connection: %v", err)
	}
	if !ok || enabled.Status != StatusEnabled {
		t.Fatalf("expected enabled connection, got ok=%v connection=%+v", ok, enabled)
	}

	if err := service.Delete(ctx, tenantID, "crm_api"); err != nil {
		t.Fatalf("delete connection: %v", err)
	}
	if _, ok, err := service.Get(ctx, tenantID, "crm_api"); err != nil || ok {
		t.Fatalf("expected deleted connection, ok=%v err=%v", ok, err)
	}

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "mcp_connection",
		Name:           "MCP is a provider",
		ConnectionType: "mcp",
		Status:         StatusDraft,
	}); err == nil || !strings.Contains(err.Error(), "unsupported connection_type") {
		t.Fatalf("expected mcp connection_type rejection, got %v", err)
	}
}

func TestServiceConnectionValidatesAuthTypeAndRefPairing(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")

	plain, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "plain",
		Name:           "Plain API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        "https://plain.example.test",
	})
	if err != nil {
		t.Fatalf("upsert plain connection: %v", err)
	}
	if plain.AuthType != AuthTypeNone || plain.AuthRef != "" {
		t.Fatalf("expected unauthenticated connection to normalize to auth_type none, got %+v", plain)
	}

	valid, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        "https://crm.example.test",
		AuthType:       " API_KEY ",
		AuthRef:        " secret://tenant_1/crm ",
	})
	if err != nil {
		t.Fatalf("upsert valid auth connection: %v", err)
	}
	if valid.AuthType != AuthTypeAPIKey || valid.AuthRef != "secret://tenant_1/crm" {
		t.Fatalf("expected normalized auth fields, got %+v", valid)
	}

	cases := []struct {
		name       string
		connection ServiceConnection
		want       string
	}{
		{
			name: "auth ref requires auth type",
			connection: ServiceConnection{
				TenantID:       tenantID,
				ConnectionID:   "missing_auth_type",
				Name:           "Missing Auth Type",
				ConnectionType: TypeHTTPAPI,
				Status:         StatusDraft,
				BaseURL:        "https://missing.example.test",
				AuthRef:        "secret://tenant_1/missing",
			},
			want: "auth_type is required",
		},
		{
			name: "unsupported auth type",
			connection: ServiceConnection{
				TenantID:       tenantID,
				ConnectionID:   "bad_auth_type",
				Name:           "Bad Auth Type",
				ConnectionType: TypeHTTPAPI,
				Status:         StatusDraft,
				BaseURL:        "https://bad.example.test",
				AuthType:       "token",
				AuthRef:        "secret://tenant_1/bad",
			},
			want: "unsupported auth_type",
		},
		{
			name: "auth type requires auth ref",
			connection: ServiceConnection{
				TenantID:       tenantID,
				ConnectionID:   "missing_auth_ref",
				Name:           "Missing Auth Ref",
				ConnectionType: TypeHTTPAPI,
				Status:         StatusDraft,
				BaseURL:        "https://missing-ref.example.test",
				AuthType:       AuthTypeBearer,
			},
			want: "auth_ref is required",
		},
		{
			name: "auth none rejects auth ref",
			connection: ServiceConnection{
				TenantID:       tenantID,
				ConnectionID:   "none_with_ref",
				Name:           "None With Ref",
				ConnectionType: TypeHTTPAPI,
				Status:         StatusDraft,
				BaseURL:        "https://none.example.test",
				AuthType:       AuthTypeNone,
				AuthRef:        "secret://tenant_1/none",
			},
			want: "auth_type none must not set auth_ref",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Upsert(ctx, tc.connection); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestServiceConnectionUpsertClearsResourcesAtomicallyWhenScopeChanges(t *testing.T) {
	store := newSpyServiceConnectionStore()
	service := NewServiceWithStore(store)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        "https://crm-v1.example.test",
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if store.upsertConnectionCalls != 1 || store.upsertWithResourcesCalls != 0 {
		t.Fatalf("unexpected initial store calls: %+v", store)
	}
	if err := service.ReplaceResources(ctx, tenantID, "crm_api", []ServiceConnectionResource{{
		TenantID:     tenantID,
		ConnectionID: "crm_api",
		ResourceID:   "GET /customers",
		ResourceType: "http_operation",
		Name:         "List customers",
	}}); err != nil {
		t.Fatalf("replace resources: %v", err)
	}

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        "https://crm-v2.example.test",
	}); err != nil {
		t.Fatalf("upsert connection with changed scope: %v", err)
	}
	if store.upsertWithResourcesCalls != 1 {
		t.Fatalf("expected atomic upsert/resource replace, got %d calls", store.upsertWithResourcesCalls)
	}
	resources, err := service.ListResources(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected resource catalog to be cleared, got %+v", resources)
	}
}

func TestServiceConnectionUpsertDoesNotAcceptHealthObservationFields(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")

	connection, err := service.Upsert(ctx, ServiceConnection{
		TenantID:        tenantID,
		ConnectionID:    "crm_api",
		Name:            "CRM API",
		ConnectionType:  TypeHTTPAPI,
		Status:          StatusDraft,
		BaseURL:         "https://crm.example.test",
		HealthStatus:    HealthHealthy,
		LastHealthError: "client supplied",
	})
	if err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if connection.HealthStatus != HealthUnknown || connection.LastHealthAt != nil || connection.LastHealthError != "" {
		t.Fatalf("connection upsert should ignore submitted health observation fields, got %+v", connection)
	}

	connection, err = service.setConnectionHealth(ctx, tenantID, "crm_api", HealthUnhealthy, "timeout")
	if err != nil {
		t.Fatalf("set connection health: %v", err)
	}
	if connection.HealthStatus != HealthUnhealthy || connection.LastHealthAt == nil || connection.LastHealthError != "timeout" {
		t.Fatalf("expected internal health write to persist observation, got %+v", connection)
	}

	updated, err := service.Upsert(ctx, ServiceConnection{
		TenantID:        tenantID,
		ConnectionID:    "crm_api",
		Name:            "CRM API renamed",
		ConnectionType:  TypeHTTPAPI,
		Status:          StatusEnabled,
		BaseURL:         "https://crm.example.test",
		HealthStatus:    HealthHealthy,
		LastHealthError: "overwrite attempt",
	})
	if err != nil {
		t.Fatalf("update connection: %v", err)
	}
	if updated.Name != "CRM API renamed" || updated.HealthStatus != HealthUnhealthy || updated.LastHealthAt == nil || updated.LastHealthError != "timeout" {
		t.Fatalf("connection config upsert should preserve existing health observation, got %+v", updated)
	}

	updated, err = service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API renamed",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://crm-next.example.test",
	})
	if err != nil {
		t.Fatalf("update connection endpoint: %v", err)
	}
	if updated.HealthStatus != HealthUnknown || updated.LastHealthAt != nil || updated.LastHealthError != "" {
		t.Fatalf("connection upsert should reset health observation when connection scope changes, got %+v", updated)
	}
}

func TestServiceConnectionUpsertClearsDiscoveredResourcesWhenDatabaseScopeChanges(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "orders_db",
		Name:           "Orders DB",
		ConnectionType: TypeDatabase,
		Status:         StatusDraft,
		BaseURL:        "postgres://orders.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if err := service.ReplaceResources(ctx, tenantID, "orders_db", []ServiceConnectionResource{{
		ResourceID:   "public.orders",
		ResourceType: "table",
		Name:         "public.orders",
	}}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	if resources, err := service.ListResources(ctx, tenantID, "orders_db"); err != nil || len(resources) != 1 {
		t.Fatalf("expected seeded resource, resources=%+v err=%v", resources, err)
	}

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "orders_db",
		Name:           "Orders DB renamed",
		ConnectionType: TypeDatabase,
		Status:         StatusEnabled,
		BaseURL:        "postgres://orders.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatalf("update stable connection fields: %v", err)
	}
	if resources, err := service.ListResources(ctx, tenantID, "orders_db"); err != nil || len(resources) != 1 {
		t.Fatalf("stable database update should preserve resources, resources=%+v err=%v", resources, err)
	}

	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "orders_db",
		Name:           "Orders DB renamed",
		ConnectionType: TypeDatabase,
		Status:         StatusEnabled,
		BaseURL:        "postgres://orders-next.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatalf("update database endpoint: %v", err)
	}
	if resources, err := service.ListResources(ctx, tenantID, "orders_db"); err != nil || len(resources) != 0 {
		t.Fatalf("database scope update should clear discovered resources, resources=%+v err=%v", resources, err)
	}
}

func TestServiceConnectionValidatesNetworkScope(t *testing.T) {
	service := NewServiceWithStore(nil)
	if _, err := service.Upsert(context.Background(), ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://crm.example.test",
		NetworkScope:   `["crm.example.test","api.example.com","*.corp.example"]`,
	}); err != nil {
		t.Fatalf("expected valid network_scope: %v", err)
	}
	if _, err := service.Upsert(context.Background(), ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "outside_scope_api",
		Name:           "Outside Scope API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://outside.example.test",
		NetworkScope:   "api.example.com",
	}); err == nil {
		t.Fatal("expected network_scope that omits base_url host to be rejected")
	}
	if _, err := service.Upsert(context.Background(), ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "wildcard_scope_api",
		Name:           "Wildcard Scope API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://api.corp.example",
		NetworkScope:   "*.corp.example",
	}); err != nil {
		t.Fatalf("expected wildcard network_scope to include base_url host: %v", err)
	}
	if _, err := service.Upsert(context.Background(), ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "bad_api",
		Name:           "Bad API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://bad.example.test",
		NetworkScope:   "*.",
	}); err == nil {
		t.Fatal("expected invalid network_scope to be rejected")
	}
}

func TestServiceConnectionHTTPTestProbesHealthz(t *testing.T) {
	var sawAuthRef bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("expected /healthz probe, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Origin-Provider-Auth-Ref") == "secret/crm" {
			sawAuthRef = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        remote.URL,
		AuthType:       AuthTypeAPIKey,
		AuthRef:        "secret/crm",
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}

	connection, resources, err := service.Test(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if connection.Status != StatusDraft || connection.HealthStatus != HealthHealthy || connection.LastHealthError != "" || connection.LastHealthAt == nil {
		t.Fatalf("expected healthy draft connection, got %+v", connection)
	}
	if len(resources) != 0 {
		t.Fatalf("expected no discovered resources, got %+v", resources)
	}
	if !sawAuthRef {
		t.Fatalf("expected auth_ref header to be sent")
	}
	events, err := service.ListHealthEvents(ctx, tenantID, "crm_api", 10)
	if err != nil {
		t.Fatalf("list health events: %v", err)
	}
	if len(events) != 1 || events[0].HealthStatus != HealthHealthy || events[0].ConnectionID != "crm_api" || events[0].CheckedAt.IsZero() {
		t.Fatalf("expected healthy history event, got %+v", events)
	}
}

func TestServiceConnectionHTTPTestDiscoversOpenAPIResources(t *testing.T) {
	var sawAuthRef bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/openapi.json":
			if r.Header.Get("X-Origin-Provider-Auth-Ref") == "secret/crm" {
				sawAuthRef = true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"openapi":"3.0.3",
				"paths":{
					"/customers":{
						"parameters":[{"name":"X-Workspace","in":"header","schema":{"type":"string"}}],
						"get":{
							"operationId":"customers.search",
							"summary":"Search customers",
							"tags":["customers"],
							"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}],
							"responses":{"200":{"description":"OK"}}
						},
						"post":{
							"operationId":"customers.create",
							"summary":"Create customer",
							"requestBody":{"content":{"application/json":{"schema":{"type":"object"}}}},
							"responses":{"201":{"description":"Created"}}
						}
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        remote.URL,
		AuthType:       AuthTypeAPIKey,
		AuthRef:        "secret/crm",
		Metadata:       map[string]any{"openapi_path": "/openapi.json"},
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if err := service.ReplaceResources(ctx, tenantID, "crm_api", []ServiceConnectionResource{{
		ResourceID:   "stale",
		ResourceType: "http_operation",
		Name:         "Stale resource",
	}}); err != nil {
		t.Fatalf("seed stale resource: %v", err)
	}

	connection, resources, err := service.Test(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if connection.HealthStatus != HealthHealthy || connection.LastHealthError != "" {
		t.Fatalf("expected healthy connection, got %+v", connection)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 discovered resources, got %+v", resources)
	}
	if resources[0].ResourceID != "GET /customers" || resources[0].ResourceType != "http_operation" || resources[0].Name != "Search customers" {
		t.Fatalf("unexpected first HTTP resource: %+v", resources[0])
	}
	if resources[0].Schema["operation_id"] != "customers.search" || resources[0].Schema["method"] != "GET" || resources[0].Schema["path"] != "/customers" {
		t.Fatalf("unexpected first HTTP resource schema: %+v", resources[0].Schema)
	}
	firstParams, _ := resources[0].Schema["parameters"].([]any)
	if len(firstParams) != 2 {
		t.Fatalf("expected first HTTP resource parameters, got %+v", resources[0].Schema["parameters"])
	}
	if resources[0].Metadata["openapi_path"] != "/openapi.json" || resources[0].Metadata["operation_id"] != "customers.search" {
		t.Fatalf("unexpected first HTTP resource metadata: %+v", resources[0].Metadata)
	}
	if resources[1].ResourceID != "POST /customers" || resources[1].Name != "Create customer" {
		t.Fatalf("unexpected second HTTP resource: %+v", resources[1])
	}
	for _, resource := range resources {
		if resource.ResourceID == "stale" {
			t.Fatalf("expected stale resource to be replaced, got %+v", resources)
		}
	}
	if !sawAuthRef {
		t.Fatalf("expected auth_ref header to be sent to openapi discovery")
	}
}

func TestServiceConnectionHTTPTestRejectsAbsoluteOpenAPIPath(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        remote.URL,
		Metadata:       map[string]any{"openapi_path": "https://other.example/openapi.json"},
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}

	connection, _, err := service.Test(ctx, tenantID, "crm_api")
	if err == nil || !strings.Contains(err.Error(), "openapi_path must be an absolute path") {
		t.Fatalf("expected invalid openapi_path error, got %v", err)
	}
	if connection.HealthStatus != HealthUnhealthy || !strings.Contains(connection.LastHealthError, "openapi_path must be an absolute path") {
		t.Fatalf("expected invalid openapi_path to mark connection unhealthy, got %+v", connection)
	}
}

func TestServiceConnectionRotateSecretStoresOnlyRefHashes(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusEnabled,
		BaseURL:        "https://crm.example.test",
		AuthType:       AuthTypeAPIKey,
		AuthRef:        "secret://tenant_1/crm-old",
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if _, err := service.setConnectionHealth(ctx, tenantID, "crm_api", HealthHealthy, ""); err != nil {
		t.Fatalf("set connection health: %v", err)
	}

	connection, rotation, err := service.RotateSecret(ctx, tenantID, "crm_api", ServiceConnectionSecretRotationRequest{
		AuthRef:   "secret://tenant_1/crm-new",
		AuthType:  AuthTypeAPIKey,
		Reason:    "scheduled rotation",
		RotatedBy: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if connection.AuthRef != "secret://tenant_1/crm-new" || connection.HealthStatus != HealthUnknown || connection.LastHealthAt != nil {
		t.Fatalf("unexpected rotated connection: %+v", connection)
	}
	if rotation.PreviousAuthRefHash == "" || rotation.NewAuthRefHash == "" || rotation.PreviousAuthRefHash == rotation.NewAuthRefHash {
		t.Fatalf("expected distinct auth ref hashes, got %+v", rotation)
	}
	if strings.Contains(rotation.PreviousAuthRefHash, "crm-old") || strings.Contains(rotation.NewAuthRefHash, "crm-new") {
		t.Fatalf("rotation leaked auth_ref plaintext: %+v", rotation)
	}
	rotations, err := service.ListSecretRotations(ctx, tenantID, "crm_api", 10)
	if err != nil {
		t.Fatalf("list secret rotations: %v", err)
	}
	if len(rotations) != 1 || rotations[0].RotationID != rotation.RotationID || rotations[0].RotatedBy != "admin@example.com" {
		t.Fatalf("unexpected secret rotation history: %+v", rotations)
	}
	if rotations[0].AuthType != AuthTypeAPIKey {
		t.Fatalf("expected rotation auth type to be retained, got %+v", rotations[0])
	}
	if err := service.AppendSecretRotation(ctx, ServiceConnectionSecretRotation{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		NewAuthRefHash: authRefHash("secret://tenant_1/manual"),
	}); err == nil || !strings.Contains(err.Error(), "auth_type is required") {
		t.Fatalf("expected direct secret rotation append without auth_type to be rejected, got %v", err)
	}
	if _, _, err := service.RotateSecret(ctx, tenantID, "crm_api", ServiceConnectionSecretRotationRequest{
		AuthRef: "secret://tenant_1/crm-later",
	}); err == nil || !strings.Contains(err.Error(), "auth_type is required") {
		t.Fatalf("expected rotate secret without auth_type to be rejected, got %v", err)
	}
}

func TestServiceConnectionHTTPTestFallsBackToBaseURL(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/" {
			t.Fatalf("expected fallback to base URL, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()

	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "webhook",
		Name:           "Webhook",
		ConnectionType: TypeWebhook,
		Status:         StatusDraft,
		BaseURL:        remote.URL,
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}

	connection, _, err := service.Test(ctx, tenantID, "webhook")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if connection.Status != StatusDraft || connection.HealthStatus != HealthHealthy {
		t.Fatalf("expected test to preserve draft status after fallback probe, got %+v", connection)
	}
}

func TestServiceConnectionHTTPTestMarksUnhealthy(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer remote.Close()

	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "crm_api",
		Name:           "CRM API",
		ConnectionType: TypeHTTPAPI,
		Status:         StatusDraft,
		BaseURL:        remote.URL,
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}

	connection, _, err := service.Test(ctx, tenantID, "crm_api")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if connection.Status != StatusDraft || connection.HealthStatus != HealthUnhealthy || !strings.Contains(connection.LastHealthError, "service connection health check failed") {
		t.Fatalf("expected unhealthy 401 connection, got %+v", connection)
	}
}

func TestServiceConnectionDatabaseTestDiscoversPostgresResources(t *testing.T) {
	scenarioName := registerServiceConnectionDBScenario(t, serviceConnectionDBScenario{
		rows: [][]driver.Value{
			{"public", "orders", "BASE TABLE", "id", "integer", "NO", int64(1)},
			{"public", "orders", "BASE TABLE", "total", "numeric", "YES", int64(2)},
			{"public", "order_summary", "VIEW", "order_id", "bigint", "NO", int64(1)},
		},
	})
	service := NewServiceWithStore(nil)
	service.openDB = func(driverName string, dataSourceName string) (*sql.DB, error) {
		if driverName != "pgx" {
			t.Fatalf("expected pgx driver, got %s", driverName)
		}
		return sql.Open(serviceConnectionTestDBDriverName, scenarioName)
	}
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "orders_db",
		Name:           "Orders DB",
		ConnectionType: TypeDatabase,
		Status:         StatusDraft,
		BaseURL:        "postgres://orders.example/db",
		Metadata:       map[string]any{"driver": "postgres"},
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	if err := service.ReplaceResources(ctx, tenantID, "orders_db", []ServiceConnectionResource{{
		ResourceID:   "stale.table",
		ResourceType: "table",
		Name:         "stale.table",
	}}); err != nil {
		t.Fatalf("seed stale resource: %v", err)
	}

	connection, resources, err := service.Test(ctx, tenantID, "orders_db")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if connection.Status != StatusDraft || connection.HealthStatus != HealthHealthy || connection.LastHealthError != "" || connection.LastHealthAt == nil {
		t.Fatalf("expected healthy database connection, got %+v", connection)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 discovered resources, got %+v", resources)
	}
	if resources[0].ResourceID != "public.order_summary" || resources[0].ResourceType != "view" {
		t.Fatalf("expected sorted view resource, got %+v", resources[0])
	}
	if resources[1].ResourceID != "public.orders" || resources[1].ResourceType != "table" {
		t.Fatalf("expected table resource, got %+v", resources[1])
	}
	columns, ok := resources[1].Schema["columns"].([]map[string]any)
	if !ok || len(columns) != 2 {
		t.Fatalf("expected table columns schema, got %#v", resources[1].Schema["columns"])
	}
	if columns[0]["name"] != "id" || columns[0]["json_schema"].(map[string]any)["type"] != "integer" {
		t.Fatalf("expected integer id column schema, got %+v", columns[0])
	}
	if columns[1]["name"] != "total" || columns[1]["json_schema"].(map[string]any)["type"] != "number" {
		t.Fatalf("expected number total column schema, got %+v", columns[1])
	}
	for _, resource := range resources {
		if resource.ResourceID == "stale.table" {
			t.Fatalf("expected stale resource to be replaced, got %+v", resources)
		}
	}
}

func TestServiceConnectionDatabaseTestRejectsUnsupportedDriver(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	if _, err := service.Upsert(ctx, ServiceConnection{
		TenantID:       tenantID,
		ConnectionID:   "orders_db",
		Name:           "Orders DB",
		ConnectionType: TypeDatabase,
		Status:         StatusDraft,
		BaseURL:        "mysql://orders.example/db",
		Metadata:       map[string]any{"driver": "mysql"},
	}); err != nil {
		t.Fatalf("upsert connection: %v", err)
	}

	connection, _, err := service.Test(ctx, tenantID, "orders_db")
	if err == nil || !strings.Contains(err.Error(), "unsupported database driver") {
		t.Fatalf("expected unsupported database driver error, got %v", err)
	}
	if connection.Status != StatusDraft || connection.HealthStatus != HealthUnhealthy || !strings.Contains(connection.LastHealthError, "unsupported database driver") {
		t.Fatalf("expected unsupported database driver result, got %+v", connection)
	}
}

func TestServiceConnectionTestRejectsUnimplementedConnectionType(t *testing.T) {
	service := NewServiceWithStore(nil)
	ctx := context.Background()
	tenantID := contracts.TenantID("tenant_1")
	for _, tc := range []struct {
		connectionID   string
		name           string
		connectionType string
	}{
		{connectionID: "cache", name: "Cache", connectionType: TypeCache},
		{connectionID: "queue", name: "Queue", connectionType: TypeQueue},
		{connectionID: "storage", name: "Storage", connectionType: TypeStorage},
	} {
		t.Run(tc.connectionType, func(t *testing.T) {
			if _, err := service.Upsert(ctx, ServiceConnection{
				TenantID:       tenantID,
				ConnectionID:   tc.connectionID,
				Name:           tc.name,
				ConnectionType: tc.connectionType,
				Status:         StatusDraft,
			}); err != nil {
				t.Fatalf("upsert connection: %v", err)
			}

			connection, resources, err := service.Test(ctx, tenantID, tc.connectionID)
			if err == nil || !strings.Contains(err.Error(), "not implemented") {
				t.Fatalf("expected not implemented error, got %v", err)
			}
			if len(resources) != 0 {
				t.Fatalf("unimplemented connection test must not discover resources, got %+v", resources)
			}
			if connection.HealthStatus != HealthUnknown || !strings.Contains(connection.LastHealthError, "not implemented") {
				t.Fatalf("expected unknown health for unimplemented connection test, got %+v", connection)
			}
			events, err := service.ListHealthEvents(ctx, tenantID, tc.connectionID, 10)
			if err != nil {
				t.Fatalf("list health events: %v", err)
			}
			if len(events) != 1 || events[0].HealthStatus != HealthUnknown || !strings.Contains(events[0].Error, "not implemented") {
				t.Fatalf("expected health event for unimplemented test, got %+v", events)
			}
		})
	}
}

type spyServiceConnectionStore struct {
	connections                map[string]ServiceConnection
	resources                  map[string][]ServiceConnectionResource
	healthEvents               map[string][]ServiceConnectionHealthEvent
	rotations                  map[string][]ServiceConnectionSecretRotation
	upsertConnectionCalls      int
	upsertWithResourcesCalls   int
	replaceResourcesCallTotals int
}

func newSpyServiceConnectionStore() *spyServiceConnectionStore {
	return &spyServiceConnectionStore{
		connections:  map[string]ServiceConnection{},
		resources:    map[string][]ServiceConnectionResource{},
		healthEvents: map[string][]ServiceConnectionHealthEvent{},
		rotations:    map[string][]ServiceConnectionSecretRotation{},
	}
}

func (s *spyServiceConnectionStore) UpsertConnection(_ context.Context, connection ServiceConnection) error {
	s.upsertConnectionCalls++
	s.connections[connectionKey(connection.TenantID, connection.ConnectionID)] = connection
	return nil
}

func (s *spyServiceConnectionStore) UpsertConnectionAndReplaceResources(_ context.Context, connection ServiceConnection, resources []ServiceConnectionResource) error {
	s.upsertWithResourcesCalls++
	key := connectionKey(connection.TenantID, connection.ConnectionID)
	s.connections[key] = connection
	s.resources[key] = append([]ServiceConnectionResource(nil), resources...)
	return nil
}

func (s *spyServiceConnectionStore) GetConnection(_ context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, bool, error) {
	connection, ok := s.connections[connectionKey(tenantID, connectionID)]
	return connection, ok, nil
}

func (s *spyServiceConnectionStore) ListConnections(_ context.Context, tenantID contracts.TenantID, _ ListFilter) ([]ServiceConnection, error) {
	out := make([]ServiceConnection, 0)
	for _, connection := range s.connections {
		if connection.TenantID == tenantID {
			out = append(out, connection)
		}
	}
	return out, nil
}

func (s *spyServiceConnectionStore) DeleteConnection(_ context.Context, tenantID contracts.TenantID, connectionID string) error {
	key := connectionKey(tenantID, connectionID)
	delete(s.connections, key)
	delete(s.resources, key)
	delete(s.healthEvents, key)
	delete(s.rotations, key)
	return nil
}

func (s *spyServiceConnectionStore) UpsertResource(_ context.Context, resource ServiceConnectionResource) error {
	key := connectionKey(resource.TenantID, resource.ConnectionID)
	resources := s.resources[key]
	for i, existing := range resources {
		if existing.ResourceID == resource.ResourceID {
			resources[i] = resource
			s.resources[key] = resources
			return nil
		}
	}
	s.resources[key] = append(resources, resource)
	return nil
}

func (s *spyServiceConnectionStore) ReplaceResources(_ context.Context, tenantID contracts.TenantID, connectionID string, resources []ServiceConnectionResource) error {
	s.replaceResourcesCallTotals++
	s.resources[connectionKey(tenantID, connectionID)] = append([]ServiceConnectionResource(nil), resources...)
	return nil
}

func (s *spyServiceConnectionStore) ListResources(_ context.Context, tenantID contracts.TenantID, connectionID string) ([]ServiceConnectionResource, error) {
	return append([]ServiceConnectionResource(nil), s.resources[connectionKey(tenantID, connectionID)]...), nil
}

func (s *spyServiceConnectionStore) AppendHealthEvent(_ context.Context, event ServiceConnectionHealthEvent) error {
	key := connectionKey(event.TenantID, event.ConnectionID)
	s.healthEvents[key] = append(s.healthEvents[key], event)
	return nil
}

func (s *spyServiceConnectionStore) ListHealthEvents(_ context.Context, tenantID contracts.TenantID, connectionID string, _ int) ([]ServiceConnectionHealthEvent, error) {
	return append([]ServiceConnectionHealthEvent(nil), s.healthEvents[connectionKey(tenantID, connectionID)]...), nil
}

func (s *spyServiceConnectionStore) AppendSecretRotation(_ context.Context, rotation ServiceConnectionSecretRotation) error {
	key := connectionKey(rotation.TenantID, rotation.ConnectionID)
	s.rotations[key] = append(s.rotations[key], rotation)
	return nil
}

func (s *spyServiceConnectionStore) ListSecretRotations(_ context.Context, tenantID contracts.TenantID, connectionID string, _ int) ([]ServiceConnectionSecretRotation, error) {
	return append([]ServiceConnectionSecretRotation(nil), s.rotations[connectionKey(tenantID, connectionID)]...), nil
}

const serviceConnectionTestDBDriverName = "znt_service_connection_test"

var serviceConnectionDBDriverOnce sync.Once
var serviceConnectionDBScenarioSeq atomic.Int64
var serviceConnectionDBScenarios sync.Map

type serviceConnectionDBScenario struct {
	pingErr  error
	queryErr error
	rows     [][]driver.Value
}

func registerServiceConnectionDBScenario(t *testing.T, scenario serviceConnectionDBScenario) string {
	t.Helper()
	serviceConnectionDBDriverOnce.Do(func() {
		sql.Register(serviceConnectionTestDBDriverName, serviceConnectionTestDriver{})
	})
	name := "scenario_" + strings.ReplaceAll(t.Name(), "/", "_") + "_" + strconv.FormatInt(serviceConnectionDBScenarioSeq.Add(1), 10)
	serviceConnectionDBScenarios.Store(name, &scenario)
	t.Cleanup(func() {
		serviceConnectionDBScenarios.Delete(name)
	})
	return name
}

type serviceConnectionTestDriver struct{}

func (serviceConnectionTestDriver) Open(name string) (driver.Conn, error) {
	value, ok := serviceConnectionDBScenarios.Load(name)
	if !ok {
		return nil, errors.New("unknown service connection db scenario")
	}
	return &serviceConnectionTestConn{scenario: value.(*serviceConnectionDBScenario)}, nil
}

type serviceConnectionTestConn struct {
	scenario *serviceConnectionDBScenario
}

func (c *serviceConnectionTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("service connection test driver does not support prepared statements")
}

func (c *serviceConnectionTestConn) Close() error {
	return nil
}

func (c *serviceConnectionTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("service connection test driver does not support transactions")
}

func (c *serviceConnectionTestConn) Ping(context.Context) error {
	return c.scenario.pingErr
}

func (c *serviceConnectionTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.scenario.queryErr != nil {
		return nil, c.scenario.queryErr
	}
	if !strings.Contains(strings.ToLower(query), "information_schema.tables") {
		return nil, errors.New("unsupported service connection test query")
	}
	return &serviceConnectionTestRows{
		columns: []string{"table_schema", "table_name", "table_type", "column_name", "data_type", "is_nullable", "ordinal_position"},
		rows:    c.scenario.rows,
	}, nil
}

type serviceConnectionTestRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *serviceConnectionTestRows) Columns() []string {
	return r.columns
}

func (r *serviceConnectionTestRows) Close() error {
	return nil
}

func (r *serviceConnectionTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
