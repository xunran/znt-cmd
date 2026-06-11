package runtime

import (
	"context"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/asset/artifact"
	"znt/internal/contracts"
	"znt/internal/execution/domain"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/internal/policy/toolpolicy"
	"znt/internal/tool/registry"
)

func TestInvokeEchoTool(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	auditLogger := audit.NewInMemoryLogger()
	traceRecorder := trace.NewInMemoryRecorder()
	rt := New(reg, toolpolicy.New(auditLogger), traceRecorder)
	rt.Now = func() time.Time { return time.Unix(1, 0).UTC() }

	agent := loader.TestAgentDefinition()
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{ToolPolicy: contracts.ToolPolicy{}},
		Call: contracts.ToolCall{
			ToolCallID:     "toolcall_1",
			ToolID:         "echo",
			Name:           "echo",
			Arguments:      map[string]any{"message": "hello"},
			RunID:          "run_1",
			TaskID:         "task_1",
			IdempotencyKey: "idem_1",
			CreatedAt:      time.Unix(1, 0).UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultSucceeded || result.Output["echo"] == nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{Action: "tool.policy_checked"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Decision != "allowed" {
		t.Fatalf("expected allowed audit, got %#v", audits)
	}
}

func TestInvokeDeniedToolReturnsDeniedResult(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	agent := loader.TestAgentDefinition()
	agent.Tools.DeniedToolIDs = []string{"echo"}
	rt := New(reg, toolpolicy.New(audit.NewInMemoryLogger()), trace.NewInMemoryRecorder())
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID:     "toolcall_1",
			ToolID:         "echo",
			Name:           "echo",
			Arguments:      map[string]any{},
			RunID:          "run_1",
			IdempotencyKey: "idem_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultDenied {
		t.Fatalf("expected denied result, got %#v", result)
	}
}

func TestInvokeRejectsDefaultDisabledFutureExecutionDomain(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "remote.worker",
			Name:             "remote.worker",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "worker:queue-a",
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			t.Fatal("executor should not run for a disabled future domain")
			return nil, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"remote.worker"}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_worker",
			ToolID:     "remote.worker",
			Name:       "remote.worker",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeExecutionDomainUnavailable {
		t.Fatalf("expected disabled domain failure, got %#v", result)
	}
	if result.Error.Details["production_status"] != domain.DisabledStatus || result.Error.Details["enabled"] != false {
		t.Fatalf("expected disabled production metadata, got %#v", result.Error.Details)
	}
}

func TestInvokeValidatesArgumentsBeforePolicy(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	auditLogger := audit.NewInMemoryLogger()
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"artifact.create"}
	rt := New(reg, toolpolicy.New(auditLogger), trace.NewInMemoryRecorder())
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_1",
			ToolID:     "artifact.create",
			Name:       "artifact.create",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeToolArgumentInvalid {
		t.Fatalf("expected argument validation failure, got %#v", result)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{Action: "tool.policy_checked"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 0 {
		t.Fatalf("expected schema validation before policy audit, got %#v", audits)
	}
}

func TestInvokeArtifactCreateStoresArtifactRef(t *testing.T) {
	store := artifact.NewInMemoryStore()
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltinsWithArtifacts(reg, store); err != nil {
		t.Fatal(err)
	}
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"artifact.create"}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())
	rt.Now = func() time.Time { return time.Unix(1, 0).UTC() }

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID:     "toolcall_1",
			ToolID:         "artifact.create",
			Name:           "artifact.create",
			Arguments:      map[string]any{"name": "report", "content": "hello report"},
			RunID:          "run_1",
			TaskID:         "task_1",
			IdempotencyKey: "idem_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultSucceeded || len(result.ArtifactRefs) != 1 {
		t.Fatalf("unexpected artifact result: %#v", result)
	}
	stored, err := store.GetArtifact(context.Background(), "tenant_1", result.ArtifactRefs[0].ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "report" || stored.Hash == "" || stored.TenantID != "tenant_1" {
		t.Fatalf("unexpected stored artifact: %#v", stored)
	}
}

type workerAdapterFunc func(context.Context, domain.ExecutionRequest) (domain.ExecutionResult, error)

func (f workerAdapterFunc) DispatchTool(ctx context.Context, req domain.ExecutionRequest) (domain.ExecutionResult, error) {
	return f(ctx, req)
}

type credentialResolverFunc func(context.Context, domain.CredentialResolveRequest) (domain.ResolvedCredential, error)

func (f credentialResolverFunc) ResolveCredential(ctx context.Context, req domain.CredentialResolveRequest) (domain.ResolvedCredential, error) {
	return f(ctx, req)
}

type executorFunc func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error)

func (f executorFunc) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return f(ctx, call)
}

type availabilityFunc func(context.Context, contracts.TenantID, contracts.ToolDefinition) error

func (f availabilityFunc) CheckToolAvailability(ctx context.Context, tenantID contracts.TenantID, tool contracts.ToolDefinition) error {
	return f(ctx, tenantID, tool)
}

func TestInvokeChecksToolAvailabilityBeforeExecution(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	executed := false
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "stale.remote",
			Name:             "stale.remote",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			executed = true
			return map[string]any{"ok": true}, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"stale.remote"}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())
	rt.Availability = availabilityFunc(func(context.Context, contracts.TenantID, contracts.ToolDefinition) error {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", nil)
	})
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_stale",
			ToolID:     "stale.remote",
			Name:       "stale.remote",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultDenied || result.Error == nil || result.Error.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected availability denial, got %#v", result)
	}
	if executed {
		t.Fatal("availability denied tool should not execute")
	}
}

func TestInvokeAuditsExecutorPolicyDenial(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "origin.agent.delegate",
			Name:             "origin.agent.delegate",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskMedium,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			return nil, nil, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "max handoff depth exceeded", nil)
		}),
	}); err != nil {
		t.Fatal(err)
	}
	auditLogger := audit.NewInMemoryLogger()
	rt := New(reg, toolpolicy.New(auditLogger), trace.NewInMemoryRecorder())
	rt.Audit = auditLogger
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"origin.agent.delegate"}

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_delegate",
			ToolID:     "origin.agent.delegate",
			Name:       "origin.agent.delegate",
			Arguments:  map[string]any{},
			RunID:      "run_1",
			TaskID:     "task_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeHandoffDenied {
		t.Fatalf("expected handoff denial failure, got %#v", result)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{Action: contracts.AuditToolPolicyDenied})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Decision != "denied" || audits[0].TaskID != "task_1" {
		t.Fatalf("expected execution denial audit, got %#v", audits)
	}
}

func TestInvokeUsesExecutionDomainProfileAndTracesMetadata(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "remote.echo",
			Name:             "remote.echo",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: `{"id":"worker-low","domain_id":"worker","worker_ref":"queue-a","resource_limits":{"memory_mb":128},"credential_scope":{"allowed_credential_refs":["cred/github"]},"data_boundary":{"allowed_tenant_ids":["tenant_1"],"allowed_data_scopes":["repo:read"]}}`,
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			return map[string]any{"local": true}, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	traceRecorder := trace.NewInMemoryRecorder()
	auditLogger := audit.NewInMemoryLogger()
	rt := New(reg, toolpolicy.New(auditLogger), traceRecorder)
	rt.Audit = auditLogger
	rt.Credentials = credentialResolverFunc(func(_ context.Context, req domain.CredentialResolveRequest) (domain.ResolvedCredential, error) {
		if req.TenantID != "tenant_1" || req.CredentialRef != "cred/github" || req.ToolID != "remote.echo" {
			t.Fatalf("unexpected credential request: %#v", req)
		}
		return domain.ResolvedCredential{
			Ref:       req.CredentialRef,
			TenantID:  req.TenantID,
			SecretRef: "secret://tenant_1/github",
			Scopes:    []string{"repo:read"},
		}, nil
	})
	rt.Domains = domain.NewResolver(domain.LocalExecutionDomain{}, domain.WorkerExecutionDomain{
		Adapter: workerAdapterFunc(func(_ context.Context, req domain.ExecutionRequest) (domain.ExecutionResult, error) {
			if req.Profile.WorkerRef != "queue-a" || req.Tool.ToolID != "remote.echo" || len(req.Profile.CredentialScope.AllowedCredentialRefs) != 1 ||
				len(req.Credentials) != 1 || req.Credentials[0].SecretRef != "secret://tenant_1/github" {
				t.Fatalf("unexpected worker request: %#v", req)
			}
			return domain.ExecutionResult{
				Output:   map[string]any{"worker": req.Call.Arguments["message"]},
				Metadata: map[string]any{"worker_job_id": "job_1"},
			}, nil
		}),
	})
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"remote.echo"}
	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_1",
			ToolID:     "remote.echo",
			Name:       "remote.echo",
			Arguments:  map[string]any{"message": "hello"},
			RunID:      "run_1",
			TaskID:     "task_1",
			CreatedAt:  time.Unix(1, 0).UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultSucceeded || result.Output["worker"] != "hello" {
		t.Fatalf("unexpected result: %#v", result)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	var invoked, credential, completed contracts.TraceEvent
	for _, event := range events {
		switch event.Type {
		case contracts.TraceToolInvoked:
			invoked = event
		case contracts.TraceCredentialUsed:
			credential = event
		case contracts.TraceToolCompleted:
			completed = event
		}
	}
	if invoked.Payload["domain_id"] != "worker" || invoked.Payload["worker_ref"] != "queue-a" {
		t.Fatalf("execution profile was not traced: %#v", invoked.Payload)
	}
	if credential.Payload["credential_ref"] != "cred/github" {
		t.Fatalf("credential scope was not traced: %#v", events)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{Action: contracts.AuditCredentialUsed})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != "cred/github" {
		t.Fatalf("credential use was not audited: %#v", audits)
	}
	metadata, ok := completed.Payload["execution_metadata"].(map[string]any)
	if !ok || metadata["worker_job_id"] != "job_1" || metadata["domain_id"] != "worker" {
		t.Fatalf("execution metadata was not traced: %#v", completed.Payload)
	}
}

func TestInvokeRejectsCredentialScopeWithoutResolver(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "remote.echo",
			Name:             "remote.echo",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: `{"domain_id":"worker","credential_scope":{"allowed_credential_refs":["cred/github"]}}`,
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			t.Fatal("executor should not run when credential resolver is missing")
			return nil, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	auditLogger := audit.NewInMemoryLogger()
	rt := New(reg, toolpolicy.New(auditLogger), trace.NewInMemoryRecorder())
	rt.Audit = auditLogger
	rt.Domains = domain.NewResolver(domain.WorkerExecutionDomain{
		Adapter: workerAdapterFunc(func(context.Context, domain.ExecutionRequest) (domain.ExecutionResult, error) {
			t.Fatal("worker adapter should not run when credential resolver is missing")
			return domain.ExecutionResult{}, nil
		}),
	})
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"remote.echo"}

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_missing_credential",
			ToolID:     "remote.echo",
			Name:       "remote.echo",
			Arguments:  map[string]any{},
			RunID:      "run_1",
			TaskID:     "task_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected credential policy failure, got %#v", result)
	}
	audits, err := auditLogger.Search(context.Background(), audit.Filter{Action: contracts.AuditToolPolicyDenied})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != "remote.echo" {
		t.Fatalf("expected denied audit, got %#v", audits)
	}
}

func TestInvokeRejectsCredentialOutsideTenantBoundary(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "remote.crm",
			Name:             "remote.crm",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: `{"domain_id":"worker","credential_scope":{"allowed_credential_refs":["cred/crm"]},"data_boundary":{"allowed_tenant_ids":["tenant_1"],"allowed_data_scopes":["crm:read"]}}`,
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			t.Fatal("executor should not run when credential is out of boundary")
			return nil, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())
	rt.Credentials = credentialResolverFunc(func(_ context.Context, req domain.CredentialResolveRequest) (domain.ResolvedCredential, error) {
		return domain.ResolvedCredential{
			Ref:       req.CredentialRef,
			TenantID:  "tenant_2",
			SecretRef: "secret://tenant_2/crm",
			Scopes:    []string{"crm:read"},
		}, nil
	})
	rt.Domains = domain.NewResolver(domain.WorkerExecutionDomain{
		Adapter: workerAdapterFunc(func(context.Context, domain.ExecutionRequest) (domain.ExecutionResult, error) {
			t.Fatal("worker adapter should not run when credential is out of boundary")
			return domain.ExecutionResult{}, nil
		}),
	})
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"remote.crm"}

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_cross_tenant_credential",
			ToolID:     "remote.crm",
			Name:       "remote.crm",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected credential boundary failure, got %#v", result)
	}
}

func TestInvokeRejectsDataBoundaryOutsideCallerTenant(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "remote.report",
			Name:             "remote.report",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: `{"domain_id":"worker","data_boundary":{"allowed_tenant_ids":["tenant_2"]}}`,
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			t.Fatal("executor should not run when caller tenant is outside data boundary")
			return nil, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())
	rt.Domains = domain.NewResolver(domain.WorkerExecutionDomain{
		Adapter: workerAdapterFunc(func(context.Context, domain.ExecutionRequest) (domain.ExecutionResult, error) {
			t.Fatal("worker adapter should not run when caller tenant is outside data boundary")
			return domain.ExecutionResult{}, nil
		}),
	})
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"remote.report"}

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_bad_boundary",
			ToolID:     "remote.report",
			Name:       "remote.report",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeToolPolicyDenied {
		t.Fatalf("expected data boundary failure, got %#v", result)
	}
}

func TestInvokeValidatesToolResultOutputSchema(t *testing.T) {
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "bad.output",
			Name:        "bad.output",
			InputSchema: map[string]any{"type": "object"},
			OutputSchema: map[string]any{
				"type":     "object",
				"required": []any{"message"},
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executorFunc(func(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			return map[string]any{"message": 42}, nil, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"bad.output"}
	rt := New(reg, toolpolicy.New(nil), trace.NewInMemoryRecorder())

	result, err := rt.Invoke(context.Background(), InvokeRequest{
		TenantID:  "tenant_1",
		TraceID:   "trace_1",
		ActorID:   "agent_1",
		ActorType: "agent",
		Agent:     agent,
		PolicySet: contracts.PolicySet{},
		Call: contracts.ToolCall{
			ToolCallID: "toolcall_bad_output",
			ToolID:     "bad.output",
			Name:       "bad.output",
			Arguments:  map[string]any{},
			RunID:      "run_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.ToolResultFailed || result.Error == nil || result.Error.Code != contracts.CodeToolExecutionFailed {
		t.Fatalf("expected output schema failure, got %#v", result)
	}
}
