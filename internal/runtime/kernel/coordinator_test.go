package kernel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/asset/artifact"
	"znt/internal/contracts"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/trace"
	modelclient "znt/internal/model/client"
	policyengine "znt/internal/policy/engine"
	"znt/internal/policy/toolpolicy"
	runtimehook "znt/internal/runtime/hook"
	runrepo "znt/internal/runtime/run"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	"znt/internal/tool/registry"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
)

func TestCoordinatorReplyRun(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, modelclient.StubModelClient{})

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "ok" {
		t.Fatalf("unexpected run result: %#v", result)
	}
	events, err := taskService.Events(context.Background(), result.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 {
		t.Fatalf("expected task lifecycle events, got %d", len(events))
	}
	traces, err := traceRecorder.ListByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) == 0 {
		t.Fatal("expected trace events")
	}
	if !hasTraceType(traces, contracts.TraceModelDelta) || !hasTraceType(traces, contracts.TraceDecisionCompleted) {
		t.Fatalf("expected streaming and decision completion traces, got %#v", traces)
	}
}

func TestCoordinatorRunsExistingTaskFromEnvelopeContext(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	existing := taskrepo.NewTask("task_existing_1", "tenant_1", "test-agent", "v1", "policy_default", "existing", "use existing task", nowForTest())
	if _, err := taskService.CreateTask(context.Background(), existing, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, modelclient.StubModelClient{})

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_existing_1",
		TraceID:    "trace_existing_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", TaskID: existing.TaskID, UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != existing.TaskID || result.Status != contracts.RunCompleted {
		t.Fatalf("expected existing task run, got %#v", result)
	}
	task, err := taskRepo.Get(context.Background(), existing.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != contracts.TaskCompleted {
		t.Fatalf("expected existing task completed, got %#v", task)
	}
}

func TestCoordinatorToolLoopThenReply(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"hello"}}]}`), ModelProvider: "stub", ModelName: "scripted"},
		{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"tool done"}}`), ModelProvider: "stub", ModelName: "scripted"},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "use echo"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "tool done" {
		t.Fatalf("unexpected run result: %#v", result)
	}
	results, err := coordinator.ToolRepo.ListResultsByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != contracts.ToolResultSucceeded {
		t.Fatalf("expected one successful tool result, got %#v", results)
	}
	calls, err := coordinator.ToolRepo.ListCallsByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].ToolVersion == "" || calls[0].ExecutionProfile == "" {
		t.Fatalf("expected tool call execution snapshot, got %#v", calls)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), "trace_1")
	if err != nil {
		t.Fatal(err)
	}
	var foundModelCapabilities bool
	for _, event := range events {
		if event.Type == contracts.TraceModelCalled && event.Payload["model_capabilities_hash"] != "" && event.Payload["model_capabilities"] != nil {
			foundModelCapabilities = true
			break
		}
	}
	if !foundModelCapabilities {
		t.Fatalf("expected model.called trace to include model capabilities, got %#v", events)
	}
	if model.Calls != 2 {
		t.Fatalf("expected two model calls, got %d", model.Calls)
	}
}

func TestRuntimeHooksObserveAndPatchData(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	model := &capturingModel{}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	hooks := &recordingHook{}
	coordinator.RuntimeHooks = runtimehook.Chain{Observers: []runtimehook.Observer{hooks}, Transformers: []runtimehook.Transformer{hooks}}

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_hook_1",
		TraceID:    "trace_hook_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted {
		t.Fatalf("unexpected result %#v", result)
	}
	if !hooks.saw(runtimehook.OnRunStarted) || !hooks.saw(runtimehook.OnContextBuilt) || !hooks.saw(runtimehook.OnModelDecision) || !hooks.saw(runtimehook.OnRunFinished) {
		t.Fatalf("missing observations %#v", hooks.events)
	}
	if !strings.Contains(model.lastRequest.PromptBundle.Context, "hook block") {
		t.Fatalf("expected hook context in prompt, got %s", model.lastRequest.PromptBundle.Context)
	}
}

func TestRuntimeHooksPatchPromptPreview(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, modelclient.StubModelClient{})
	hooks := &recordingHook{}
	coordinator.RuntimeHooks = runtimehook.Chain{Transformers: []runtimehook.Transformer{hooks}}

	preview, err := coordinator.PreviewPromptBundle(context.Background(), PromptPreviewRequest{
		Target:  contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Input:   "hello",
		Context: contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.PromptBundle.Context, "hook block") {
		t.Fatalf("expected hook context in preview, got %s", preview.PromptBundle.Context)
	}
	if len(preview.WorkView.Constraints) == 0 || !strings.Contains(strings.Join(preview.WorkView.Constraints, "\n"), "planner hint") {
		t.Fatalf("expected planner hint in preview work view, got %#v", preview.WorkView.Constraints)
	}
}

func TestRuntimeHookServiceWritesMemoryIntentsThroughPolicy(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.RuntimeHooks = contracts.AgentRuntimeHooks{
		Mode: "data_hooks",
		Hooks: []contracts.AgentRuntimeHookBinding{{
			HookID:       "memory-hook",
			ProviderType: "go",
			Phase:        "before_memory_write",
			Enabled:      true,
			Config: map[string]any{
				"patch": map[string]any{
					"memory_write_intents": []any{map[string]any{
						"scope":   "user",
						"summary": "language preference",
						"content": "User prefers concise Chinese replies.",
						"metadata": map[string]any{
							"visibility": "private",
							"confidence": 0.9,
						},
					}},
				},
			},
		}},
	}
	agents := loader.NewStaticLoader(def)
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, modelclient.StubModelClient{})
	coordinator.RuntimeHooks = runtimehook.NewService(runtimehook.NewInMemoryStore(), traceRecorder, nil)
	coordinator.Policies = policyengine.NewInMemoryStore(policyengine.DefaultPolicySet())
	memory := artifact.NewInMemoryMemoryStore(nil)
	coordinator.Memory = memory

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_memory_hook_1",
		TraceID:    "trace_memory_hook_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted {
		t.Fatalf("unexpected result %#v", result)
	}
	written, err := memory.ListMemory(context.Background(), "tenant_1", "test-agent", "user_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].Summary != "language preference" {
		t.Fatalf("expected memory intent to be written, got %#v", written)
	}
}

func TestToolCallIdempotencyKeyHashesRunStepToolAndArguments(t *testing.T) {
	first, err := toolCallIdempotencyKey("run_1", "step_1", contracts.ToolCall{
		ToolID:    "echo",
		Arguments: map[string]any{"message": "hello", "count": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	same, err := toolCallIdempotencyKey("run_1", "step_1", contracts.ToolCall{
		ToolID:    "echo",
		Arguments: map[string]any{"count": 1, "message": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := toolCallIdempotencyKey("run_1", "step_1", contracts.ToolCall{
		ToolID:    "echo",
		Arguments: map[string]any{"message": "changed", "count": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != same {
		t.Fatalf("expected stable idempotency hash for equivalent args: %s != %s", first, same)
	}
	if first == changed {
		t.Fatalf("expected changed args to produce a different idempotency key")
	}
}

func TestVersionSnapshotIncludesAgentPackage(t *testing.T) {
	def := loader.TestAgentDefinition()
	def.PackageVersionID = "pkg_1"
	def.RuntimeHooks = contracts.AgentRuntimeHooks{
		Mode: "data_hooks",
		Hooks: []contracts.AgentRuntimeHookBinding{{
			HookID:  "rank-echo",
			Phase:   string(runtimehook.AfterCandidateRetrieval),
			Enabled: true,
		}},
	}
	policies := policyengine.NewInMemoryStore(policyengine.DefaultPolicySet())
	coordinator := NewCoordinator(
		loader.NewStaticLoader(def),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskrepo.NewInMemoryTaskRepository(), taskrepo.NewInMemoryEventRepository()),
		taskrepo.NewInMemoryTaskRepository(),
		trace.NewInMemoryRecorder(),
		modelclient.StubModelClient{},
	)
	coordinator.Policies = policies
	snapshot := coordinator.versionSnapshot(context.Background(), "tenant_1", def, "objective")
	if snapshot.AgentPackage != "pkg_1" {
		t.Fatalf("expected package version in snapshot, got %#v", snapshot)
	}
	if snapshot.PolicySetVersion != "v1" {
		t.Fatalf("expected policy version in snapshot, got %#v", snapshot)
	}
	if snapshot.AdditionalAttributes["runtime_hooks_hash"] == "" {
		t.Fatalf("expected runtime hook hash in snapshot, got %#v", snapshot)
	}
	if snapshot.AdditionalAttributes["model_capabilities_hash"] == "" {
		t.Fatalf("expected model capabilities hash in snapshot, got %#v", snapshot)
	}
}

func TestCoordinatorPinsPromptBundleHashInRunSnapshot(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, modelclient.StubModelClient{})
	coordinator.Policies = policyengine.NewInMemoryStore(policyengine.DefaultPolicySet())

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_snapshot_1",
		TraceID:    "trace_snapshot_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runRepo.Get(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.VersionSnapshot.PromptBundleHash == "" || run.VersionSnapshot.PolicySetVersion != "v1" {
		t.Fatalf("expected prompt hash and policy version in snapshot, got %#v", run.VersionSnapshot)
	}
}

func TestCoordinatorPinsPolicyVersionAcrossRunSteps(t *testing.T) {
	ctx := context.Background()
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	policies := policyengine.NewInMemoryStore()
	oldPolicy := policyengine.DefaultPolicySet()
	oldPolicy.TenantID = "tenant_1"
	oldPolicy.ToolPolicy.AllowedToolIDs = []string{"echo"}
	oldVersion := contracts.PolicyVersion{
		PolicyVersionID: "policyver_old",
		TenantID:        "tenant_1",
		PolicySetID:     oldPolicy.PolicySetID,
		Version:         oldPolicy.Version,
		Status:          contracts.ReleaseStable,
		CreatedAt:       time.Unix(1, 0).UTC(),
	}
	if err := policies.SaveVersion(ctx, oldVersion, oldPolicy); err != nil {
		t.Fatal(err)
	}
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"old policy should allow"}}]}`)},
		{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"done"}}`)},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	coordinator.Policies = policies
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)
	coordinator.DisabledToolIDs = map[string]struct{}{}
	coordinator.Now = func() time.Time { return time.Unix(2, 0).UTC() }
	run := contracts.AgentRun{
		RunID:        "run_policy_pin",
		TraceID:      "trace_policy_pin",
		TenantID:     "tenant_1",
		AgentID:      "test-agent",
		AgentVersion: "v1",
		TaskID:       "task_policy_pin",
		Status:       contracts.RunCreated,
		PolicySetID:  oldPolicy.PolicySetID,
		VersionSnapshot: contracts.VersionSnapshot{
			AgentDefinition:  "v1",
			PolicySet:        oldPolicy.PolicySetID,
			PolicyVersionID:  oldVersion.PolicyVersionID,
			PolicySetVersion: oldPolicy.Version,
		},
		StartedAt: time.Unix(2, 0).UTC(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	task := taskrepo.NewTask("task_policy_pin", "tenant_1", "test-agent", "v1", oldPolicy.PolicySetID, "pin", "use echo", time.Unix(2, 0).UTC())
	if _, err := taskService.CreateTask(ctx, task, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []contracts.TaskCommand{contracts.CmdAccept, contracts.CmdPlanStarted, contracts.CmdRunStarted} {
		if _, _, _, err := taskService.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    task.TaskID,
			Command:   command,
			ActorID:   "test-agent",
			ActorType: "agent",
			RunID:     run.RunID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	newPolicy := oldPolicy
	newPolicy.Version = "v2"
	newPolicy.ToolPolicy.DeniedToolIDs = []string{"echo"}
	newVersion := contracts.PolicyVersion{
		PolicyVersionID: "policyver_new",
		TenantID:        "tenant_1",
		PolicySetID:     newPolicy.PolicySetID,
		Version:         newPolicy.Version,
		Status:          contracts.ReleaseStable,
		CreatedAt:       time.Unix(3, 0).UTC(),
	}
	if err := policies.SaveVersion(ctx, newVersion, newPolicy); err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.ResumeRun(ctx, contracts.AgentEnvelope{
		EnvelopeID: "env_policy_pin",
		TraceID:    "trace_policy_pin_resume",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", TaskID: "task_policy_pin", UserID: "user_1"},
	}, "run_policy_pin", "task_policy_pin")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted {
		t.Fatalf("expected pinned old policy to allow run completion, got %#v", result)
	}
	updated, err := runRepo.Get(ctx, "run_policy_pin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.VersionSnapshot.PolicyVersionID != oldVersion.PolicyVersionID || updated.VersionSnapshot.PolicySetVersion != oldPolicy.Version {
		t.Fatalf("expected run snapshot to remain pinned to old policy, got %#v", updated.VersionSnapshot)
	}
}

func TestCoordinatorStopsWhenMaxToolCallsExceeded(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Runtime.MaxToolCalls = 1
	agents := loader.NewStaticLoader(def)
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"one"}}]}`)},
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"two"}}]}`)},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "loop tools"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err == nil {
		t.Fatal("expected max tool calls error")
	}
	if result.Status != contracts.RunFailed || result.Error == nil {
		t.Fatalf("expected failed result with error, got %#v", result)
	}
}

func TestCoordinatorRetriesModelError(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Runtime.MaxModelRetries = 1
	agents := loader.NewStaticLoader(def)
	model := &flakyModelClient{}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || model.calls != 2 {
		t.Fatalf("expected retry then completed run, got result=%#v calls=%d", result, model.calls)
	}
}

func TestCoordinatorDoesNotRetryNonRetryableModelError(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Runtime.MaxModelRetries = 2
	agents := loader.NewStaticLoader(def)
	model := &nonRetryableModelClient{}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)

	_, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err == nil {
		t.Fatal("expected model error")
	}
	if model.calls != 1 {
		t.Fatalf("expected non-retryable error to stop after one call, got %d", model.calls)
	}
}

func TestApplyPromptPolicyBlocksConfiguredPhrase(t *testing.T) {
	def := loader.TestAgentDefinition()
	policy := policyengine.DefaultPolicySet()
	policy.PromptPolicy.BlockedPhrases = []string{"blocked phrase"}
	coordinator := NewCoordinator(
		loader.NewStaticLoader(def),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskrepo.NewInMemoryTaskRepository(), taskrepo.NewInMemoryEventRepository()),
		taskrepo.NewInMemoryTaskRepository(),
		trace.NewInMemoryRecorder(),
		modelclient.StubModelClient{},
	)
	_, err := coordinator.applyPromptPolicy(policy, def, contracts.PromptBundle{
		System:  "system",
		Task:    "task",
		Context: "user said blocked phrase",
	})
	if err == nil {
		t.Fatal("expected prompt policy to block phrase")
	}
}

func TestApplyPromptPolicyCompressesContext(t *testing.T) {
	def := loader.TestAgentDefinition()
	def.Runtime.MaxPromptTokens = 24
	policy := policyengine.DefaultPolicySet()
	policy.PromptPolicy.MaxPromptTokens = 24
	policy.CompressionPolicy.Enabled = true
	policy.CompressionPolicy.MaxContextItems = 1
	coordinator := NewCoordinator(
		loader.NewStaticLoader(def),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskrepo.NewInMemoryTaskRepository(), taskrepo.NewInMemoryEventRepository()),
		taskrepo.NewInMemoryTaskRepository(),
		trace.NewInMemoryRecorder(),
		modelclient.StubModelClient{},
	)
	bundle, err := coordinator.applyPromptPolicy(policy, def, contracts.PromptBundle{
		System:  "system",
		Task:    "task",
		Context: strings.Repeat("context ", 80),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bundle.Context, "context truncated by compression policy") || bundle.Hash == "" {
		t.Fatalf("expected compressed and rehashed prompt bundle, got %#v", bundle)
	}
}

func TestCoordinatorRepairsInvalidDecisionJSON(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Runtime.MaxRepairAttempts = 1
	agents := loader.NewStaticLoader(def)
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`not-json`)},
		{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"repaired"}}`)},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_repair_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "repaired" || model.Calls != 2 {
		t.Fatalf("expected repaired run, got result=%#v calls=%d", result, model.Calls)
	}
	traces, err := traceRecorder.ListByTrace(context.Background(), "trace_repair_1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTraceType(traces, contracts.TraceDecisionRepairRequested) {
		t.Fatalf("expected repair trace, got %#v", traces)
	}
}

func TestCoordinatorRepairAttemptAddsVisibleConstraint(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Runtime.MaxRepairAttempts = 1
	agents := loader.NewStaticLoader(def)
	model := &capturingRepairModelClient{}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_repair_visible_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "repaired" {
		t.Fatalf("expected repaired run, got %#v", result)
	}
	if len(model.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(model.requests))
	}
	constraints := strings.Join(model.requests[1].PromptBundle.Constraints, "\n")
	if !strings.Contains(constraints, "previous decision was invalid") || !strings.Contains(constraints, "return one valid Decision JSON object") {
		t.Fatalf("expected repair constraint in second model request, got %q", constraints)
	}
}

func TestCoordinatorStopsAfterConsecutiveToolFailures(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Tools.AllowedToolIDs = []string{"fail"}
	def.Runtime.MaxToolCalls = 4
	def.Runtime.MaxConsecutiveToolFailures = 1
	agents := loader.NewStaticLoader(def)
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"fail","name":"fail","arguments":{}}]}`)},
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"fail","name":"fail","arguments":{}}]}`)},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "fail",
			Name:             "fail",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: failingExecutor{},
	}); err != nil {
		t.Fatal(err)
	}
	coordinator.Tools = tooldiscovery.StaticCandidateProvider{Cards: reg.Cards()}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "fail twice"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err == nil {
		t.Fatal("expected consecutive tool failure error")
	}
	if result.Status != contracts.RunFailed || result.Error == nil {
		t.Fatalf("expected failed result, got %#v", result)
	}
}

func TestCoordinatorContinuesAfterRepairableToolFailure(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	def := loader.TestAgentDefinition()
	def.Tools.AllowedToolIDs = []string{"fail"}
	def.Runtime.MaxToolCalls = 3
	def.Runtime.MaxConsecutiveToolFailures = 2
	agents := loader.NewStaticLoader(def)
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"fail","name":"fail","arguments":{}}]}`)},
		{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"recovered"}}`)},
	}}
	coordinator := NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
	policy := policyengine.DefaultPolicySet()
	policy.ToolRepairPolicy.MaxRepairAttempts = 1
	coordinator.Policies = policyengine.NewInMemoryStore(policy)
	reg := registry.NewInMemoryRegistry()
	if err := reg.Register(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "fail",
			Name:             "fail",
			InputSchema:      map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: failingExecutor{},
	}); err != nil {
		t.Fatal(err)
	}
	coordinator.Tools = tooldiscovery.StaticCandidateProvider{Cards: reg.Cards()}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)

	result, err := coordinator.HandleEnvelope(context.Background(), contracts.AgentEnvelope{
		EnvelopeID: "env_repair_tool_1",
		TraceID:    "trace_repair_tool_1",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "fail then recover"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "recovered" {
		t.Fatalf("expected repairable tool failure to continue, got %#v", result)
	}
}

func hasTraceType(events []contracts.TraceEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func nowForTest() time.Time {
	return time.Unix(1, 0).UTC()
}

type flakyModelClient struct {
	calls int
}

func (c *flakyModelClient) Complete(context.Context, modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	c.calls++
	if c.calls == 1 {
		return modelclient.ModelResponse{}, errors.New("temporary model failure")
	}
	return modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"ok"}}`)}, nil
}

func (c *flakyModelClient) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	resp, err := c.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: resp.RawDecisionJSON}
	}()
	return ch, nil
}

type nonRetryableModelClient struct {
	calls int
}

func (c *nonRetryableModelClient) Complete(context.Context, modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	c.calls++
	err := contracts.NewRuntimeError(contracts.CodeModelError, "invalid model configuration", map[string]any{"kind": "invalid_config"})
	err.Retryable = false
	return modelclient.ModelResponse{}, err
}

func (c *nonRetryableModelClient) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	resp, err := c.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: resp.RawDecisionJSON}
	}()
	return ch, nil
}

type failingExecutor struct{}

func (failingExecutor) Execute(context.Context, contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return nil, nil, errors.New("tool failed")
}

type recordingHook struct {
	events []runtimehook.Event
}

func (h *recordingHook) Observe(_ context.Context, observation runtimehook.Observation) error {
	h.events = append(h.events, observation.Event)
	return nil
}

func (h *recordingHook) Apply(_ context.Context, request runtimehook.TransformRequest) (runtimehook.Patch, error) {
	switch request.HookPoint {
	case runtimehook.AfterCandidateRetrieval:
		return runtimehook.Patch{ToolRankAdjustments: []runtimehook.ToolRankAdjustment{{ToolID: "echo", Boost: true}}}, nil
	case runtimehook.BeforeContextBuild:
		return runtimehook.Patch{PlannerHints: []runtimehook.PlannerHint{{Content: "prefer concise answer"}}}, nil
	case runtimehook.BeforeModelCall:
		return runtimehook.Patch{AddContextBlocks: []runtimehook.ContextBlock{{ID: "hook_1", Title: "hook block", Content: "hook block content"}}}, nil
	default:
		return runtimehook.Patch{}, nil
	}
}

func (h *recordingHook) saw(event runtimehook.Event) bool {
	for _, current := range h.events {
		if current == event {
			return true
		}
	}
	return false
}

type capturingModel struct {
	lastRequest modelclient.ModelRequest
}

func (m *capturingModel) Complete(_ context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	m.lastRequest = request
	return modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"ok"}}`)}, nil
}

func (m *capturingModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	resp, err := m.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: resp.RawDecisionJSON}
	}()
	return ch, nil
}

type capturingRepairModelClient struct {
	requests []modelclient.ModelRequest
}

func (c *capturingRepairModelClient) Complete(_ context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	c.requests = append(c.requests, request)
	if len(c.requests) == 1 {
		return modelclient.ModelResponse{RawDecisionJSON: []byte(`not-json`)}, nil
	}
	return modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"repaired"}}`)}, nil
}

func (c *capturingRepairModelClient) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	resp, err := c.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: resp.RawDecisionJSON}
	}()
	return ch, nil
}
