package kernel

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/trace"
	modelclient "znt/internal/model/client"
	"znt/internal/policy/toolpolicy"
	runrepo "znt/internal/runtime/run"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	"znt/internal/tool/registry"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
)

func BenchmarkKernelStubReply(b *testing.B) {
	ctx := context.Background()
	coordinator := newBenchmarkCoordinator(b, modelclient.StubModelClient{})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := coordinator.HandleEnvelope(ctx, benchmarkEnvelope(i, "hello"))
		if err != nil {
			b.Fatal(err)
		}
		if result.Status != contracts.RunCompleted || result.Reply == nil {
			b.Fatalf("unexpected result: %#v", result)
		}
	}
}

func BenchmarkKernelToolCall(b *testing.B) {
	ctx := context.Background()
	coordinator := newBenchmarkCoordinator(b, newBenchmarkToolLoopModel())
	reg := registry.NewInMemoryRegistry()
	if err := registry.RegisterBuiltins(reg); err != nil {
		b.Fatal(err)
	}
	coordinator.Tools = tooldiscovery.StaticCandidateProvider{
		Capabilities: tooldiscovery.DefaultCapabilities(),
		Skills:       tooldiscovery.DefaultSkills(),
		Cards:        reg.Cards(),
		Registry:     reg,
	}
	coordinator.ToolRepo = toolrepo.NewInMemoryRepository()
	coordinator.ToolRuntime = toolruntime.New(reg, toolpolicy.New(nil), coordinator.Trace)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := coordinator.HandleEnvelope(ctx, benchmarkEnvelope(i, "use echo"))
		if err != nil {
			b.Fatal(err)
		}
		if result.Status != contracts.RunCompleted || result.Reply == nil || result.Reply.Text != "tool done" {
			b.Fatalf("unexpected result: %#v", result)
		}
	}
}

func newBenchmarkCoordinator(b *testing.B, model modelclient.ModelClient) Coordinator {
	b.Helper()
	taskRepo := taskrepo.NewInMemoryTaskRepository()
	eventRepo := taskrepo.NewInMemoryEventRepository()
	taskService := taskruntime.NewService(taskRepo, eventRepo)
	runRepo := runrepo.NewInMemoryRepository()
	traceRecorder := trace.NewInMemoryRecorder()
	agents := loader.NewStaticLoader(loader.TestAgentDefinition())
	return NewCoordinator(agents, runRepo, taskService, taskRepo, traceRecorder, model)
}

func benchmarkEnvelope(i int, input string) contracts.AgentEnvelope {
	suffix := strconv.Itoa(i)
	return contracts.AgentEnvelope{
		EnvelopeID: "env_bench_" + suffix,
		TraceID:    contracts.TraceID("trace_bench_" + suffix),
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": input},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	}
}

type benchmarkToolLoopModel struct {
	mu    sync.Mutex
	calls map[contracts.AgentRunID]int
}

func newBenchmarkToolLoopModel() *benchmarkToolLoopModel {
	return &benchmarkToolLoopModel{calls: map[contracts.AgentRunID]int{}}
}

func (m *benchmarkToolLoopModel) Complete(_ context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	m.mu.Lock()
	call := m.calls[request.RunID]
	m.calls[request.RunID] = call + 1
	m.mu.Unlock()
	if call == 0 {
		return modelclient.ModelResponse{
			RawDecisionJSON: []byte(`{"type":"tool_call","tool_calls":[{"tool_id":"echo","name":"echo","arguments":{"message":"hello"}}]}`),
			ModelProvider:   "bench",
			ModelName:       "tool-loop",
		}, nil
	}
	return modelclient.ModelResponse{
		RawDecisionJSON: []byte(`{"type":"reply","reply":{"kind":"answer","text":"tool done"}}`),
		ModelProvider:   "bench",
		ModelName:       "tool-loop",
	}, nil
}

func (m *benchmarkToolLoopModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
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
