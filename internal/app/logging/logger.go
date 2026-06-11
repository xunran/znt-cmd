package logging

import (
	"context"
	"log/slog"
	"os"

	"znt/internal/contracts"
)

type runtimeFieldsKey struct{}

type RuntimeFields struct {
	TraceID contracts.TraceID
	RunID   contracts.AgentRunID
	TaskID  contracts.TaskID
}

func New(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}

func WithRuntimeFields(ctx context.Context, fields RuntimeFields) context.Context {
	return context.WithValue(ctx, runtimeFieldsKey{}, fields)
}

func RuntimeAttrs(ctx context.Context) []slog.Attr {
	fields, ok := ctx.Value(runtimeFieldsKey{}).(RuntimeFields)
	if !ok {
		return []slog.Attr{
			slog.String("trace_id", ""),
			slog.String("run_id", ""),
			slog.String("task_id", ""),
		}
	}
	return []slog.Attr{
		slog.String("trace_id", string(fields.TraceID)),
		slog.String("run_id", string(fields.RunID)),
		slog.String("task_id", string(fields.TaskID)),
	}
}
