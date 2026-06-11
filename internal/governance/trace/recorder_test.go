package trace

import (
	"context"
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestInMemoryRecorderQueries(t *testing.T) {
	rec := NewInMemoryRecorder()
	event := contracts.TraceEvent{
		TraceID:   contracts.TraceID("trace_1"),
		SpanID:    contracts.SpanID("span_1"),
		RunID:     contracts.AgentRunID("run_1"),
		TaskID:    contracts.TaskID("task_1"),
		Type:      contracts.TraceRunCreated,
		Payload:   map[string]any{"ok": true},
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	if err := rec.Record(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	byTrace, err := rec.ListByTrace(context.Background(), event.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byTrace) != 1 || byTrace[0].Type != contracts.TraceRunCreated {
		t.Fatalf("unexpected trace events: %#v", byTrace)
	}
	byRun, _ := rec.ListByRun(context.Background(), event.RunID)
	byTask, _ := rec.ListByTask(context.Background(), event.TaskID)
	if len(byRun) != 1 || len(byTask) != 1 {
		t.Fatalf("expected run and task indexes to match")
	}
}

func TestInMemoryRecorderListFilterAndPaging(t *testing.T) {
	rec := NewInMemoryRecorder()
	now := time.Unix(10, 0).UTC()
	events := []contracts.TraceEvent{
		{TraceID: "trace_1", SpanID: "span_1", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceRunCreated, CreatedAt: now},
		{TraceID: "trace_1", SpanID: "span_2", TenantID: "tenant_1", RunID: "run_1", TaskID: "task_1", Type: contracts.TraceModelCalled, CreatedAt: now.Add(time.Second)},
		{TraceID: "trace_2", SpanID: "span_3", TenantID: "tenant_2", RunID: "run_2", TaskID: "task_2", Type: contracts.TraceRunCreated, CreatedAt: now.Add(2 * time.Second)},
	}
	for _, event := range events {
		if err := rec.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := rec.List(context.Background(), ListFilter{TenantID: "tenant_1", TraceID: "trace_1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Type != contracts.TraceModelCalled {
		t.Fatalf("expected newest matching event, got %#v", listed)
	}
	filtered, err := rec.List(context.Background(), ListFilter{TenantID: "tenant_1", Type: contracts.TraceRunCreated})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].TraceID != "trace_1" {
		t.Fatalf("expected tenant-scoped run.created event, got %#v", filtered)
	}
}
