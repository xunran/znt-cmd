package domain

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

type fakeExecutor struct{}

func (fakeExecutor) Execute(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return map[string]any{"ok": true}, nil, nil
}

type networkExecutor struct {
	host string
}

func (e networkExecutor) Execute(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return map[string]any{"ok": true}, nil, nil
}

func (e networkExecutor) NetworkTargetHost() string {
	return e.host
}

type fakeWorkerAdapter struct {
	seen ExecutionRequest
}

func (a *fakeWorkerAdapter) DispatchTool(_ context.Context, req ExecutionRequest) (ExecutionResult, error) {
	a.seen = req
	return ExecutionResult{
		Output:   map[string]any{"queued": req.Profile.WorkerRef},
		Metadata: map[string]any{"worker_job_id": "job_1"},
	}, nil
}

func TestParseProfileSupportsJSONAndShorthand(t *testing.T) {
	profile, err := ParseProfile(`{"id":"p1","domain_id":"sandbox","sandbox":"firecracker","resource_limits":{"memory_mb":256,"timeout_ms":5000},"network_policy":{"allow_network":true,"allowed_hosts":["api.example.com"]},"credential_scope":{"allowed_credential_refs":["cred/github"]},"data_boundary":{"allowed_tenant_ids":["tenant_1"],"allowed_data_scopes":["repo:read"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DomainID != "sandbox" || profile.Sandbox != "firecracker" || profile.TimeoutMS != 0 || profile.Timeout == 0 {
		t.Fatalf("unexpected json profile: %#v", profile)
	}
	if !profile.NetworkPolicy.AllowNetwork || len(profile.NetworkPolicy.AllowedHosts) != 1 {
		t.Fatalf("unexpected network policy: %#v", profile.NetworkPolicy)
	}
	if len(profile.CredentialScope.AllowedCredentialRefs) != 1 || len(profile.DataBoundary.AllowedTenantIDs) != 1 {
		t.Fatalf("unexpected credential/data boundary: %#v %#v", profile.CredentialScope, profile.DataBoundary)
	}

	shorthand, err := ParseProfile("worker:queue-a")
	if err != nil {
		t.Fatal(err)
	}
	if shorthand.DomainID != "worker" || shorthand.WorkerRef != "queue-a" {
		t.Fatalf("unexpected shorthand profile: %#v", shorthand)
	}
}

func TestLocalDomainRejectsCredentialScopeAndDataBoundary(t *testing.T) {
	for _, raw := range []string{
		`{"domain_id":"local","credential_scope":{"allowed_credential_refs":["cred/github"]}}`,
		`{"domain_id":"local","data_boundary":{"allow_external_data":true}}`,
	} {
		profile, err := ParseProfile(raw)
		if err != nil {
			t.Fatal(err)
		}
		_, err = LocalExecutionDomain{}.Execute(context.Background(), ExecutionRequest{
			Profile:  profile,
			Executor: fakeExecutor{},
		})
		if err == nil {
			t.Fatalf("expected local domain to reject %s", raw)
		}
		runtimeErr, ok := err.(*contracts.RuntimeError)
		if !ok || runtimeErr.Code != contracts.CodeExecutionDomainUnavailable {
			t.Fatalf("unexpected error: %#v", err)
		}
	}
}

func TestResolverDispatchesWorkerAdapter(t *testing.T) {
	adapter := &fakeWorkerAdapter{}
	resolver := NewResolver(LocalExecutionDomain{}, WorkerExecutionDomain{Adapter: adapter})
	domain, profile, err := resolver.ResolveProfile("worker:queue-a")
	if err != nil {
		t.Fatal(err)
	}
	result, err := domain.Execute(context.Background(), ExecutionRequest{
		Profile:  profile,
		Tool:     contracts.ToolDefinition{ToolID: "tool_1"},
		Call:     contracts.ToolCall{ToolCallID: "call_1"},
		Executor: fakeExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["queued"] != "queue-a" || result.Metadata["worker_job_id"] != "job_1" || result.Metadata["domain_id"] != "worker" {
		t.Fatalf("unexpected worker result: %#v", result)
	}
	if adapter.seen.Profile.WorkerRef != "queue-a" {
		t.Fatalf("adapter did not receive parsed profile: %#v", adapter.seen.Profile)
	}
}

func TestDefaultNonCoreDomainsAreDisabledForSingleNodeProduction(t *testing.T) {
	for _, raw := range []string{`{"domain_id":"database"}`, "worker:queue-a", "sandbox:firecracker", "managed:lambda"} {
		t.Run(raw, func(t *testing.T) {
			resolver := NewResolver(LocalExecutionDomain{}, HTTPDomain(), AgentToolDomain(), DatabaseDomain(), WorkerDomain(), SandboxDomain(), ManagedDomain())
			execDomain, profile, err := resolver.ResolveProfile(raw)
			if err != nil {
				t.Fatal(err)
			}
			_, err = execDomain.Execute(context.Background(), ExecutionRequest{
				Profile:  profile,
				Executor: fakeExecutor{},
			})
			if err == nil {
				t.Fatal("expected disabled future domain to fail")
			}
			runtimeErr, ok := err.(*contracts.RuntimeError)
			if !ok || runtimeErr.Code != contracts.CodeExecutionDomainUnavailable {
				t.Fatalf("unexpected error: %#v", err)
			}
			if runtimeErr.Details["production_status"] != DisabledStatus || runtimeErr.Details["enabled"] != false {
				t.Fatalf("expected explicit disabled metadata, got %#v", runtimeErr.Details)
			}
		})
	}
}

func TestSingleNodeProductionStatusesListDisabledFutureDomains(t *testing.T) {
	statuses := SingleNodeProductionStatuses()
	byDomain := map[string]ProductionStatus{}
	for _, status := range statuses {
		byDomain[status.DomainID] = status
	}
	for _, domainID := range []string{"local", "http", "agent_tool"} {
		if !byDomain[domainID].Enabled || byDomain[domainID].Status != ProductionReadyStatus {
			t.Fatalf("expected %s production ready, got %#v", domainID, byDomain[domainID])
		}
	}
	for _, domainID := range []string{"database", "worker", "sandbox", "managed"} {
		if byDomain[domainID].Enabled || byDomain[domainID].Status != DisabledStatus || byDomain[domainID].Details == "" {
			t.Fatalf("expected %s disabled with details, got %#v", domainID, byDomain[domainID])
		}
	}
}

func TestLocalDomainRejectsNetworkGrant(t *testing.T) {
	profile, err := ParseProfile(`{"domain_id":"local","network_policy":{"allow_network":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = LocalExecutionDomain{}.Execute(context.Background(), ExecutionRequest{
		Profile:  profile,
		Executor: fakeExecutor{},
	})
	if err == nil {
		t.Fatal("expected local domain to reject network grant")
	}
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok || runtimeErr.Code != contracts.CodeExecutionDomainUnavailable {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestHTTPDomainAllowsNetworkProfile(t *testing.T) {
	profile, err := ParseProfile(`{"domain_id":"http","network_policy":{"allow_network":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := HTTPExecutionDomain{}.Execute(context.Background(), ExecutionRequest{
		Profile:  profile,
		Executor: fakeExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["ok"] != true || result.Metadata["domain_id"] != "http" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestHTTPDomainEnforcesNetworkPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		host    string
		wantErr bool
	}{
		{
			name:    "requires explicit network grant",
			profile: `{"domain_id":"http"}`,
			host:    "api.example.com",
			wantErr: true,
		},
		{
			name:    "allows exact host",
			profile: `{"domain_id":"http","network_policy":{"allow_network":true,"allowed_hosts":["api.example.com"]}}`,
			host:    "api.example.com",
		},
		{
			name:    "allows wildcard suffix",
			profile: `{"domain_id":"http","network_policy":{"allow_network":true,"allowed_hosts":["*.example.com"]}}`,
			host:    "crm.example.com",
		},
		{
			name:    "rejects unlisted host",
			profile: `{"domain_id":"http","network_policy":{"allow_network":true,"allowed_hosts":["api.example.com"]}}`,
			host:    "evil.example.net",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := ParseProfile(tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			_, err = HTTPExecutionDomain{}.Execute(context.Background(), ExecutionRequest{
				Profile:  profile,
				Executor: networkExecutor{host: tc.host},
			})
			if tc.wantErr && err == nil {
				t.Fatal("expected network policy error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected network policy error: %v", err)
			}
		})
	}
}

func TestAgentToolDomainExecutesLocalAgentToolExecutor(t *testing.T) {
	profile, err := ParseProfile(`{"domain_id":"agent_tool"}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := AgentToolExecutionDomain{}.Execute(context.Background(), ExecutionRequest{
		Profile:  profile,
		Executor: fakeExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output["ok"] != true || result.Metadata["domain_id"] != "agent_tool" {
		t.Fatalf("unexpected result %#v", result)
	}
}
