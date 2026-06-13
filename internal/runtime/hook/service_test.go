package hook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"znt/internal/contracts"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/trace"
	serviceconnection "znt/internal/serviceconnection"
)

func TestServiceAppliesAgentRuntimeHookConfigPatch(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Tools: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo"},
		},
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:       "rank-echo",
				ProviderType: string(ProviderTypeGo),
				Phase:        string(AfterCandidateRetrieval),
				Enabled:      true,
				Config: map[string]any{
					"patch": map[string]any{
						"tool_rank_adjustments": []any{map[string]any{
							"tool_id": "echo",
							"boost":   true,
						}},
					},
				},
			}},
		},
	}
	patch, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    AfterCandidateRetrieval,
		Payload:  map[string]any{"objective": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.ToolRankAdjustments) != 1 || patch.ToolRankAdjustments[0].ToolID != "echo" {
		t.Fatalf("expected rank adjustment patch, got %#v", patch)
	}
}

func TestServiceRejectsPatchForUnapprovedTool(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:        "rank-secret",
				ProviderType:  string(ProviderTypeGo),
				Phase:         string(AfterCandidateRetrieval),
				Enabled:       true,
				FailurePolicy: "reject",
				Config: map[string]any{
					"patch": map[string]any{
						"tool_rank_adjustments": []any{map[string]any{
							"tool_id": "secret",
							"boost":   true,
						}},
					},
				},
			}},
		},
	}
	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    AfterCandidateRetrieval,
	})
	if err == nil {
		t.Fatal("expected unapproved tool patch to be rejected")
	}
}

func TestServiceRejectsSensitiveHookPatchContent(t *testing.T) {
	store := NewInMemoryStore()
	service := NewService(store, nil, nil)
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:        "inject-secret",
				ProviderType:  string(ProviderTypeGo),
				Phase:         string(BeforeModelCall),
				Enabled:       true,
				FailurePolicy: "reject",
				Config: map[string]any{
					"patch": map[string]any{
						"add_context_blocks": []any{map[string]any{
							"id":      "secret",
							"content": "sk-test-secret-token-value",
						}},
					},
				},
			}},
		},
	}
	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil {
		t.Fatal("expected sensitive hook patch to be rejected")
	}
	events, err := store.ListEvents(context.Background(), "tenant_1", "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		for _, block := range event.Patch.AddContextBlocks {
			if block.Content == "sk-test-secret-token-value" {
				t.Fatalf("sensitive hook patch leaked into event store: %#v", events)
			}
		}
	}
}

func TestServiceAnnotatesPluginContextBlockSources(t *testing.T) {
	store := NewInMemoryStore()
	service := NewService(store, nil, nil)
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "crm-plugin",
		Name:         "CRM Plugin",
		ProviderType: ProviderTypeGo,
		Status:       StatusEnabled,
		HealthStatus: HealthHealthy,
	}); err != nil {
		t.Fatal(err)
	}
	agent := contracts.AgentDefinition{
		AgentID:          "agent_1",
		Version:          "v1",
		SourceKind:       contracts.AgentSourceKindPlugin,
		SourceProviderID: "crm-plugin",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:       "crm-context",
				ProviderType: string(ProviderTypeGo),
				ProviderID:   "crm-plugin",
				Phase:        string(BeforeModelCall),
				Enabled:      true,
				Config: map[string]any{
					"patch": map[string]any{
						"add_context_blocks": []any{map[string]any{
							"id":      "account-42",
							"title":   "account context",
							"content": "ACME renewal is active.",
						}},
					},
				},
			}},
		},
	}
	patch, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.AddContextBlocks) != 1 {
		t.Fatalf("expected one context block, got %#v", patch.AddContextBlocks)
	}
	metadata := patch.AddContextBlocks[0].Metadata
	if metadata["source_type"] != "agent_plugin_context" ||
		metadata["provider_id"] != "crm-plugin" ||
		metadata["hook_id"] != "crm-context" ||
		metadata["trust_level"] != "untrusted_external_context" {
		t.Fatalf("expected annotated plugin context metadata, got %#v", metadata)
	}
	sourceRef, ok := metadata["source_ref"].(string)
	if !ok || sourceRef == "" {
		t.Fatalf("expected source_ref metadata, got %#v", metadata)
	}
}

func TestServiceRejectsSensitiveHookConfigValues(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "bad-secret",
		ProviderType:  ProviderTypeGo,
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
		Config: map[string]any{
			"api_key": "sk-test-secret-token-value",
		},
	}); err == nil {
		t.Fatal("expected sensitive config value to be rejected")
	}
}

func TestServiceAllowsSecretReferencesInHookConfig(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:     "tenant_1",
		AgentID:      "agent_1",
		HookID:       "secret-ref",
		ProviderType: ProviderTypeGo,
		Phase:        BeforeModelCall,
		Enabled:      true,
		Config: map[string]any{
			"credential_ref": "cred/runtime-hook",
			"secret_ref":     "secret://tenant_1/runtime-hook",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsHookPatchOverQuota(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	hints := make([]any, 0, maxHookPlannerHints+1)
	for i := 0; i < maxHookPlannerHints+1; i++ {
		hints = append(hints, map[string]any{"key": "hint", "content": "keep it short"})
	}
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:        "too-many-hints",
				ProviderType:  string(ProviderTypeGo),
				Phase:         string(BeforeModelCall),
				Enabled:       true,
				FailurePolicy: "reject",
				Config: map[string]any{
					"patch": map[string]any{"planner_hints": hints},
				},
			}},
		},
	}
	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil {
		t.Fatal("expected over-quota hook patch to be rejected")
	}
}

func TestStaticHookTimeoutRecordsTimeoutTrace(t *testing.T) {
	traceRecorder := trace.NewInMemoryRecorder()
	service := NewService(NewInMemoryStore(), traceRecorder, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(25 * time.Millisecond)
		_ = r.Body.Close()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer remote.Close()
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "slow-hooks",
		Name:         "Slow Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "slow-before-model",
		ProviderID: "slow-hooks",
		Name:       "Slow before model",
		Phase:      BeforeModelCall,
		Status:     StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "slow-before-model",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "slow-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		TimeoutMS:     1,
		FailurePolicy: "reject",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    contracts.AgentDefinition{AgentID: "agent_1", Version: "v1"},
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil {
		t.Fatal("expected static hook timeout")
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	var timeoutEvent contracts.TraceEvent
	for _, event := range events {
		if event.Type == contracts.TraceRuntimeHookTimeout {
			timeoutEvent = event
			break
		}
	}
	if timeoutEvent.Type == "" || timeoutEvent.Payload["provider_id"] != "slow-hooks" {
		t.Fatalf("expected timeout trace with provider evidence, got %#v", events)
	}
	if _, ok := timeoutEvent.Payload["latency_ms"].(int); !ok {
		t.Fatalf("expected timeout latency evidence, got %#v", timeoutEvent.Payload)
	}
}

func TestStaticHookProviderUsesServiceConnection(t *testing.T) {
	authRefCh := make(chan string, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/invoke" {
			http.Error(w, "unexpected hook path", http.StatusNotFound)
			return
		}
		select {
		case authRefCh <- r.Header.Get("X-Origin-Provider-Auth-Ref"):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","patch":{"planner_hints":[{"content":"prefer account context"}]}}`))
	}))
	defer remote.Close()

	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "hook-connection",
		Name:           "Hook Connection",
		ConnectionType: serviceconnection.TypeHTTPAPI,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        remote.URL,
		AuthType:       serviceconnection.AuthTypeAPIKey,
		AuthRef:        "secret://tenant_1/hook",
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(NewInMemoryStore(), nil, nil)
	service.SetServiceConnections(connections)
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:            "tenant_1",
		ProviderID:          "service-hooks",
		Name:                "Service Hooks",
		ProviderType:        ProviderTypeStaticHookHost,
		ServiceConnectionID: "hook-connection",
		Status:              StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "service-before-model",
		ProviderID: "service-hooks",
		Name:       "Service before model",
		Phase:      BeforeModelCall,
		Status:     StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "service-before-model",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "service-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
	}); err != nil {
		t.Fatal(err)
	}

	patch, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		Agent:    contracts.AgentDefinition{AgentID: "agent_1", Version: "v1"},
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.PlannerHints) != 1 || patch.PlannerHints[0].Content != "prefer account context" {
		t.Fatalf("expected service connection hook patch, got %#v", patch)
	}
	select {
	case authRef := <-authRefCh:
		if authRef != "secret://tenant_1/hook" {
			t.Fatalf("expected service connection auth ref header, got %q", authRef)
		}
	default:
		t.Fatalf("expected static hook invoke through service connection")
	}
}

func TestStaticHookRejectsUnknownPatchFields(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/invoke" {
			http.Error(w, "unexpected hook path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","patch":{"planner_hints":[{"content":"prefer account context"}],"run_status":"completed"}}`))
	}))
	defer remote.Close()

	service := NewService(NewInMemoryStore(), nil, nil)
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "strict-hooks",
		Name:         "Strict Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "strict-before-model",
		ProviderID: "strict-hooks",
		Name:       "Strict before model",
		Phase:      BeforeModelCall,
		Status:     StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "strict-before-model",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "strict-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		Agent:    contracts.AgentDefinition{AgentID: "agent_1", Version: "v1"},
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil || !strings.Contains(err.Error(), `unknown field "run_status"`) {
		t.Fatalf("expected unknown patch field rejection, got %v", err)
	}
}

func TestStaticHookProviderBlocksUnhealthyServiceConnection(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer remote.Close()

	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "hook-connection",
		Name:           "Hook Connection",
		ConnectionType: serviceconnection.TypeHTTPAPI,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        remote.URL,
	}); err != nil {
		t.Fatal(err)
	}
	connection, _, err := connections.Test(context.Background(), "tenant_1", "hook-connection")
	if err != nil {
		t.Fatal(err)
	}
	if connection.HealthStatus != serviceconnection.HealthUnhealthy {
		t.Fatalf("expected unhealthy service connection, got %#v", connection)
	}

	service := NewService(NewInMemoryStore(), nil, nil)
	service.SetServiceConnections(connections)
	err = service.UpsertProvider(context.Background(), Provider{
		TenantID:            "tenant_1",
		ProviderID:          "service-hooks",
		Name:                "Service Hooks",
		ProviderType:        ProviderTypeStaticHookHost,
		ServiceConnectionID: "hook-connection",
		Status:              StatusEnabled,
	})
	if err == nil {
		t.Fatal("expected unhealthy service connection to block hook provider")
	}
}

func TestServiceAllowsCandidateToolFromAllowedGroup(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Tools: contracts.AgentToolsConfig{
			AllowedToolGroupIDs: []string{"core"},
		},
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:       "rank-echo",
				ProviderType: string(ProviderTypeGo),
				Phase:        string(AfterCandidateRetrieval),
				Enabled:      true,
				Config: map[string]any{
					"patch": map[string]any{
						"tool_rank_adjustments": []any{map[string]any{
							"tool_id": "echo",
							"boost":   true,
						}},
					},
				},
			}},
		},
	}
	patch, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    agent,
		Policy:   contracts.PolicySet{},
		Phase:    AfterCandidateRetrieval,
		Payload: map[string]any{
			"candidates": tooldiscovery.CandidateSet{
				Tools: []contracts.ToolCard{{ToolID: "echo", GroupID: "core", Version: "v1"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.ToolRankAdjustments) != 1 || patch.ToolRankAdjustments[0].ToolID != "echo" {
		t.Fatalf("expected candidate rank adjustment patch, got %#v", patch)
	}
}

func TestStaticProviderHealthBlocksInvokeWhenUnhealthy(t *testing.T) {
	store := NewInMemoryStore()
	service := NewService(store, nil, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	unhealthy, err := service.CheckProviderHealth(context.Background(), "tenant_1", "static-hooks")
	if err != nil {
		t.Fatal(err)
	}
	if unhealthy.HealthStatus != HealthUnhealthy || unhealthy.LastHealthCheckAt == nil || unhealthy.LastHealthError == "" {
		t.Fatalf("expected unhealthy provider evidence, got %#v", unhealthy)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "static-rerank",
		ProviderID: "static-hooks",
		Name:       "Static rerank",
		Phase:      BeforeModelCall,
		Status:     StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "static-rerank",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "static-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    contracts.AgentDefinition{AgentID: "agent_1", Version: "v1"},
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil {
		t.Fatal("expected unhealthy provider to reject hook invoke")
	}
}

func TestProviderHealthCanRecordTraceEvidence(t *testing.T) {
	traceRecorder := trace.NewInMemoryRecorder()
	service := NewService(NewInMemoryStore(), traceRecorder, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckProviderHealthForTrace(context.Background(), "tenant_1", "static-hooks", "trace_hook_health_1"); err != nil {
		t.Fatal(err)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_hook_health_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != contracts.TraceRuntimeHookProviderHealthChecked || events[0].Payload["health_status"] != HealthHealthy {
		t.Fatalf("expected hook provider health trace evidence, got %#v", events)
	}
	if _, ok := events[0].Payload["latency_ms"].(int); !ok {
		t.Fatalf("expected hook provider health latency evidence, got %#v", events[0].Payload)
	}
}

func TestStaticProviderCatalogIsReadAndValidated(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	catalogRequests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/catalog" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		catalogRequests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"provider_id": "static-hooks",
			"version":     "v2",
			"hooks": []map[string]any{{
				"hook_id":        "remote-rerank",
				"name":           "Remote rerank",
				"description":    "Adjust candidate ranking.",
				"phase":          "after_candidate_retrieval",
				"version":        "v1",
				"failure_policy": "ignore",
				"config_schema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
				},
			}},
		})
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	provider, catalog, err := service.ReadProviderCatalog(context.Background(), "tenant_1", "static-hooks")
	if err != nil {
		t.Fatal(err)
	}
	if provider.ProviderID != "static-hooks" || catalog.ProviderID != "static-hooks" || catalog.Version != "v2" {
		t.Fatalf("unexpected catalog provider metadata: provider=%#v catalog=%#v", provider, catalog)
	}
	if catalogRequests != 1 || len(catalog.Hooks) != 1 {
		t.Fatalf("expected one catalog request and hook, requests=%d catalog=%#v", catalogRequests, catalog)
	}
	hook := catalog.Hooks[0]
	if hook.HookID != "remote-rerank" || hook.Phase != AfterCandidateRetrieval || hook.TimeoutMS != 300 || hook.FailurePolicy != "ignore" {
		t.Fatalf("unexpected hook catalog entry: %#v", hook)
	}
}

func TestStaticProviderCatalogUsesServiceConnection(t *testing.T) {
	authRefCh := make(chan string, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/catalog" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		select {
		case authRefCh <- r.Header.Get("X-Origin-Provider-Auth-Ref"):
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"provider_id": "service-hooks",
			"hooks": []map[string]any{{
				"hook_id": "remote-before-model",
				"name":    "Remote before model",
				"phase":   "before_model_call",
			}},
		})
	}))
	defer remote.Close()

	connections := serviceconnection.NewServiceWithStore(nil)
	if _, err := connections.Upsert(context.Background(), serviceconnection.ServiceConnection{
		TenantID:       "tenant_1",
		ConnectionID:   "hook-connection",
		Name:           "Hook Connection",
		ConnectionType: serviceconnection.TypeHTTPAPI,
		Status:         serviceconnection.StatusEnabled,
		BaseURL:        remote.URL,
		AuthType:       serviceconnection.AuthTypeAPIKey,
		AuthRef:        "secret://tenant_1/hook",
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(NewInMemoryStore(), nil, nil)
	service.SetServiceConnections(connections)
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:            "tenant_1",
		ProviderID:          "service-hooks",
		Name:                "Service Hooks",
		ProviderType:        ProviderTypeStaticHookHost,
		ServiceConnectionID: "hook-connection",
		Status:              StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	_, catalog, err := service.ReadProviderCatalog(context.Background(), "tenant_1", "service-hooks")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Hooks) != 1 || catalog.Hooks[0].ProviderID != "service-hooks" {
		t.Fatalf("expected service connection hook catalog, got %#v", catalog)
	}
	select {
	case authRef := <-authRefCh:
		if authRef != "secret://tenant_1/hook" {
			t.Fatalf("expected service connection auth ref header, got %q", authRef)
		}
	default:
		t.Fatalf("expected catalog fetch through service connection")
	}
}

func TestStaticProviderCatalogRejectsInvalidHookManifest(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hooks": []map[string]any{{
				"hook_id": "bad-phase",
				"name":    "Bad phase",
				"phase":   "after_model_decision",
			}},
		})
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ReadProviderCatalog(context.Background(), "tenant_1", "static-hooks"); err == nil {
		t.Fatal("expected invalid hook catalog phase to be rejected")
	}
}

func TestStaticProviderCatalogSyncPersistsHookManifestsOnly(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime-hooks/catalog" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hooks": []map[string]any{{
				"hook_id": "remote-before-model",
				"name":    "Remote before model",
				"phase":   "before_model_call",
			}},
		})
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	_, hooks, err := service.SyncProviderCatalog(context.Background(), "tenant_1", "static-hooks")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].HookID != "remote-before-model" || hooks[0].ProviderID != "static-hooks" || hooks[0].Status != StatusEnabled {
		t.Fatalf("expected synced hook manifest, got %#v", hooks)
	}
	stored, ok, err := service.GetManifest(context.Background(), "tenant_1", "remote-before-model")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || stored.Phase != BeforeModelCall || stored.TimeoutMS != 300 {
		t.Fatalf("expected stored hook manifest defaults, found=%v manifest=%#v", ok, stored)
	}
	bindings, err := service.ListBindings(context.Background(), "tenant_1", "agent_1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("catalog sync must not auto-install agent bindings, got %#v", bindings)
	}
}

func TestStaticHookBindingRequiresManifest(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     "http://127.0.0.1",
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "missing-manifest",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "static-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
	}); err == nil {
		t.Fatal("expected static hook binding without manifest to be rejected")
	}
}

func TestDisabledHookManifestBlocksExistingBindingInvoke(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	remoteInvoked := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteInvoked = true
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer remote.Close()

	if err := service.UpsertProvider(context.Background(), Provider{
		TenantID:     "tenant_1",
		ProviderID:   "static-hooks",
		Name:         "Static Hooks",
		ProviderType: ProviderTypeStaticHookHost,
		Endpoint:     remote.URL,
		Status:       StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "remote-before-model",
		ProviderID: "static-hooks",
		Name:       "Remote before model",
		Phase:      BeforeModelCall,
		Status:     StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertBinding(context.Background(), Binding{
		TenantID:      "tenant_1",
		AgentID:       "agent_1",
		HookID:        "remote-before-model",
		ProviderType:  ProviderTypeStaticHookHost,
		ProviderID:    "static-hooks",
		Phase:         BeforeModelCall,
		Enabled:       true,
		FailurePolicy: "reject",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID:   "tenant_1",
		HookID:     "remote-before-model",
		ProviderID: "static-hooks",
		Name:       "Remote before model",
		Phase:      BeforeModelCall,
		Status:     StatusDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Preview(context.Background(), InvokeRequest{
		TenantID: "tenant_1",
		TraceID:  "trace_1",
		Agent:    contracts.AgentDefinition{AgentID: "agent_1", Version: "v1"},
		Policy:   contracts.PolicySet{},
		Phase:    BeforeModelCall,
	})
	if err == nil {
		t.Fatal("expected disabled hook manifest to block invoke")
	}
	if remoteInvoked {
		t.Fatal("disabled hook manifest should block before calling remote HookHost")
	}
}

func TestHookManifestVersionActivationRejectsDisabledSnapshot(t *testing.T) {
	service := NewService(NewInMemoryStore(), nil, nil)
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID: "tenant_1",
		HookID:   "rank-context",
		Name:     "Rank context v1",
		Phase:    BeforeModelCall,
		Status:   StatusEnabled,
		Version:  "v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManifest(context.Background(), HookManifest{
		TenantID: "tenant_1",
		HookID:   "rank-context",
		Name:     "Rank context disabled",
		Phase:    BeforeModelCall,
		Status:   StatusDisabled,
		Version:  "v2",
	}); err != nil {
		t.Fatal(err)
	}
	versions, err := service.ListManifestVersions(context.Background(), "tenant_1", "rank-context")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != "v1" || versions[1].Version != "v2" {
		t.Fatalf("expected two ordered manifest versions, got %#v", versions)
	}
	activated, err := service.ActivateManifestVersion(context.Background(), "tenant_1", "rank-context", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !activated.Active || activated.Manifest.Name != "Rank context v1" {
		t.Fatalf("expected v1 to become active, got %#v", activated)
	}
	if _, err := service.ActivateManifestVersion(context.Background(), "tenant_1", "rank-context", "v2"); err == nil {
		t.Fatal("expected disabled hook manifest version activation to be rejected")
	}
	current, ok, err := service.GetManifest(context.Background(), "tenant_1", "rank-context")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || current.Version != "v1" {
		t.Fatalf("disabled activation must not replace current manifest, found=%v manifest=%#v", ok, current)
	}
}

func TestGovernanceSummaryFiltersWindowAndBuildsTrend(t *testing.T) {
	store := NewInMemoryStore()
	service := NewService(store, nil, nil)
	base := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	events := []HookEvent{
		{
			EventID:      "evt_before",
			TenantID:     "tenant_1",
			TraceID:      "trace_1",
			HookID:       "ranker",
			ProviderID:   "provider_a",
			ProviderType: ProviderTypeGo,
			Phase:        BeforeModelCall,
			Status:       "ok",
			LatencyMS:    10,
			CreatedAt:    base.Add(-time.Minute),
		},
		{
			EventID:      "evt_ok",
			TenantID:     "tenant_1",
			TraceID:      "trace_1",
			HookID:       "ranker",
			ProviderID:   "provider_a",
			ProviderType: ProviderTypeGo,
			Phase:        BeforeModelCall,
			Status:       "ok",
			LatencyMS:    20,
			CreatedAt:    base.Add(15 * time.Minute),
		},
		{
			EventID:      "evt_failed",
			TenantID:     "tenant_1",
			TraceID:      "trace_1",
			HookID:       "ranker",
			ProviderID:   "provider_a",
			ProviderType: ProviderTypeGo,
			Phase:        BeforeModelCall,
			Status:       "failed",
			LatencyMS:    40,
			CreatedAt:    base.Add(70 * time.Minute),
		},
		{
			EventID:      "evt_other_provider",
			TenantID:     "tenant_1",
			TraceID:      "trace_1",
			HookID:       "ranker",
			ProviderID:   "provider_b",
			ProviderType: ProviderTypeGo,
			Phase:        BeforeModelCall,
			Status:       "ok",
			LatencyMS:    30,
			CreatedAt:    base.Add(30 * time.Minute),
		},
	}
	for _, event := range events {
		if err := store.SaveEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	from := base
	to := base.Add(2 * time.Hour)
	summary, err := service.GovernanceSummary(context.Background(), "tenant_1", HookEventFilter{
		TraceID:    "trace_1",
		ProviderID: "provider_a",
		From:       &from,
		To:         &to,
		Interval:   time.Hour,
		IntervalID: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalEvents != 2 || summary.OKEvents != 1 || summary.FailedEvents != 1 || summary.FailureRate != 50 {
		t.Fatalf("unexpected governance totals: %#v", summary)
	}
	if summary.LatencyMS != 60 || summary.AverageLatencyMS != 30 {
		t.Fatalf("unexpected governance latency: %#v", summary)
	}
	if len(summary.Trend) != 2 {
		t.Fatalf("expected two trend buckets, got %#v", summary.Trend)
	}
	if !summary.Trend[0].WindowStart.Equal(base) || summary.Trend[0].TotalEvents != 1 || summary.Trend[0].OKEvents != 1 {
		t.Fatalf("unexpected first trend bucket: %#v", summary.Trend[0])
	}
	if !summary.Trend[1].WindowStart.Equal(base.Add(time.Hour)) || summary.Trend[1].TotalEvents != 1 || summary.Trend[1].FailedEvents != 1 {
		t.Fatalf("unexpected second trend bucket: %#v", summary.Trend[1])
	}
	if len(summary.ProviderMatrix) != 1 {
		t.Fatalf("expected one provider matrix row, got %#v", summary.ProviderMatrix)
	}
	matrix := summary.ProviderMatrix[0]
	if matrix.ProviderID != "provider_a" || matrix.TotalEvents != 2 || matrix.FailedEvents != 1 || matrix.FailureRate != 50 || matrix.AverageLatencyMS != 30 {
		t.Fatalf("unexpected provider matrix row: %#v", matrix)
	}
	if len(matrix.Buckets) != 2 {
		t.Fatalf("expected provider matrix status buckets, got %#v", matrix.Buckets)
	}
	if matrix.Buckets[0].Status != "failed" || matrix.Buckets[0].Count != 1 || matrix.Buckets[1].Status != "ok" || matrix.Buckets[1].Count != 1 {
		t.Fatalf("unexpected provider matrix buckets: %#v", matrix.Buckets)
	}
}

func TestInMemoryStoreImplementsStore(t *testing.T) {
	var _ Store = (*InMemoryStore)(nil)
}
