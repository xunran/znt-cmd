package trace

import (
	"context"
	"strconv"
	"testing"
	"time"

	"znt/internal/contracts"
)

func BenchmarkTraceRecordInMemory(b *testing.B) {
	ctx := context.Background()
	recorder := NewInMemoryRecorder()
	now := time.Unix(1, 0).UTC()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		suffix := strconv.Itoa(i)
		if err := recorder.Record(ctx, contracts.TraceEvent{
			TraceID:   contracts.TraceID("trace_bench_" + suffix),
			TenantID:  "tenant_1",
			SpanID:    contracts.SpanID("span_bench_" + suffix),
			RunID:     contracts.AgentRunID("run_bench_" + suffix),
			TaskID:    contracts.TaskID("task_bench_" + suffix),
			Type:      contracts.TraceModelCalled,
			Payload:   map[string]any{"step": i, "provider": "stub"},
			CreatedAt: now,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTraceListByRunInMemory(b *testing.B) {
	ctx := context.Background()
	recorder := NewInMemoryRecorder()
	now := time.Unix(1, 0).UTC()
	for i := 0; i < 1000; i++ {
		runID := contracts.AgentRunID("run_bench_" + strconv.Itoa(i%50))
		if err := recorder.Record(ctx, contracts.TraceEvent{
			TraceID:   contracts.TraceID("trace_bench_" + strconv.Itoa(i)),
			TenantID:  "tenant_1",
			SpanID:    contracts.SpanID("span_bench_" + strconv.Itoa(i)),
			RunID:     runID,
			TaskID:    contracts.TaskID("task_bench_" + strconv.Itoa(i%50)),
			Type:      contracts.TraceModelCalled,
			Payload:   map[string]any{"step": i, "provider": "stub"},
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events, err := recorder.ListByRun(ctx, contracts.AgentRunID("run_bench_"+strconv.Itoa(i%50)))
		if err != nil {
			b.Fatal(err)
		}
		if len(events) == 0 {
			b.Fatal("expected trace events")
		}
	}
}
