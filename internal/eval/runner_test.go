package eval

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/governance/trace"
	modelclient "znt/internal/model/client"
	"znt/internal/policy/toolpolicy"
	"znt/internal/runtime/kernel"
	runrepo "znt/internal/runtime/run"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	"znt/internal/tool/registry"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
)

func TestRunnerPassesFinalReplyContains(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	coordinator := kernel.NewCoordinator(
		loader.NewStaticLoader(loader.TestAgentDefinition()),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskRepo, taskrepo.NewInMemoryEventRepository()),
		taskRepo,
		trace.NewInMemoryRecorder(),
		modelclient.StubModelClient{Response: modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"hello eval"}}`)}},
	)
	result := NewRunner(coordinator).Run(context.Background(), Case{
		Name:               "reply",
		Input:              "hello",
		Target:             contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Context:            contracts.RuntimeContext{TenantID: "tenant_1"},
		FinalReplyContains: []string{"eval"},
		ShouldEndStatus:    contracts.RunCompleted,
	})
	if !result.Passed {
		t.Fatalf("expected eval pass, got %#v", result)
	}
	if result.FinalReply != "hello eval" {
		t.Fatalf("expected final reply to be captured, got %#v", result)
	}
	events, err := coordinator.Trace.ListByTrace(context.Background(), result.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	started, completed := false, false
	for _, event := range events {
		if event.Type == contracts.TraceEvalRunStarted {
			started = true
		}
		if event.Type == contracts.TraceEvalCaseCompleted {
			completed = true
		}
	}
	if !started || !completed {
		t.Fatalf("expected eval trace events, got %#v", events)
	}
}

func TestRunnerRecordsFailedCaseTrace(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	coordinator := kernel.NewCoordinator(
		loader.NewStaticLoader(loader.TestAgentDefinition()),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskRepo, taskrepo.NewInMemoryEventRepository()),
		taskRepo,
		traceRecorder,
		modelclient.StubModelClient{Response: modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"hello eval"}}`)}},
	)
	result := NewRunner(coordinator).Run(context.Background(), Case{
		Name:               "reply_mismatch",
		Input:              "hello",
		Target:             contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Context:            contracts.RuntimeContext{TenantID: "tenant_1"},
		FinalReplyContains: []string{"missing"},
		ShouldEndStatus:    contracts.RunCompleted,
	})
	if result.Passed {
		t.Fatalf("expected eval failure, got %#v", result)
	}
	events, err := traceRecorder.ListByTrace(context.Background(), result.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, event := range events {
		if event.Type == contracts.TraceEvalCaseFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected failed eval case trace, got %#v", events)
	}
}

func TestRunnerSupportsFinalReplyNotContains(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	coordinator := kernel.NewCoordinator(
		loader.NewStaticLoader(loader.TestAgentDefinition()),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskRepo, taskrepo.NewInMemoryEventRepository()),
		taskRepo,
		trace.NewInMemoryRecorder(),
		modelclient.StubModelClient{Response: modelclient.ModelResponse{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"safe refusal"}}`)}},
	)
	result := NewRunner(coordinator).Run(context.Background(), Case{
		Name:                  "reply_not_contains",
		Input:                 "hello",
		Target:                contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Context:               contracts.RuntimeContext{TenantID: "tenant_1"},
		FinalReplyNotContains: []string{"system prompt", "developer prompt"},
		ShouldEndStatus:       contracts.RunCompleted,
	})
	if !result.Passed {
		t.Fatalf("expected eval pass, got %#v", result)
	}

	failed := NewRunner(coordinator).Run(context.Background(), Case{
		Name:                  "reply_not_contains_failure",
		Input:                 "hello",
		Target:                contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Context:               contracts.RuntimeContext{TenantID: "tenant_1"},
		FinalReplyNotContains: []string{"safe"},
		ShouldEndStatus:       contracts.RunCompleted,
	})
	if failed.Passed {
		t.Fatalf("expected eval failure, got %#v", failed)
	}
}

func TestRunnerUsesCollaborationCaller(t *testing.T) {
	ctx := contracts.RuntimeContext{
		TenantID: "tenant_1",
		UserID:   "user_1",
		Collaboration: &contracts.CollaborationContext{
			CallerID:   "user_2",
			CallerType: "user",
		},
	}
	caller := evalCaller(ctx)
	if caller.CallerID != "user_2" || caller.CallerType != "user" || caller.TenantID != "tenant_1" {
		t.Fatalf("unexpected eval caller: %#v", caller)
	}
}

func TestRunnerToolAssertions(t *testing.T) {
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	model := &modelclient.ScriptedModelClient{Responses: []modelclient.ModelResponse{
		{RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"hello"}}]}`)},
		{RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"done"}}`)},
	}}
	coordinator := kernel.NewCoordinator(
		loader.NewStaticLoader(loader.TestAgentDefinition()),
		runrepo.NewInMemoryRepository(),
		taskruntime.NewService(taskRepo, taskrepo.NewInMemoryEventRepository()),
		taskRepo,
		traceRecorder,
		model,
	)
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), traceRecorder)
	result := NewRunner(coordinator).Run(context.Background(), Case{
		Name:            "tool",
		Input:           "use tool",
		Target:          contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Context:         contracts.RuntimeContext{TenantID: "tenant_1"},
		MustCallTools:   []string{"echo"},
		MaxToolCalls:    1,
		ShouldEndStatus: contracts.RunCompleted,
	})
	if !result.Passed {
		t.Fatalf("expected eval pass, got %#v", result)
	}
	if len(result.ToolCalls) != 1 || len(result.ToolResults) != 1 || result.ToolCalls[0].ToolID != "echo" {
		t.Fatalf("expected tool trace in eval result, got %#v", result)
	}
}
