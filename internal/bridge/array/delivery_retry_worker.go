package array

import (
	"context"
	"log/slog"
	"time"

	"znt/internal/contracts"
)

const (
	defaultDeliveryRetryInterval  = time.Minute
	defaultDeliveryRetryBatchSize = 50
)

type DeliveryRetryWorker struct {
	Bridge      *Bridge
	Logger      *slog.Logger
	Interval    time.Duration
	BatchSize   int
	MaxAttempts int
	Statuses    []string
}

func (w DeliveryRetryWorker) Run(ctx context.Context) {
	interval := w.effectiveInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.logInfo("external delivery retry worker started",
		slog.Duration("interval", interval),
		slog.Int("batch_size", w.effectiveBatchSize()),
		slog.Int("max_attempts", w.MaxAttempts),
	)
	for {
		if _, err := w.RunOnce(ctx); err != nil {
			w.logError("external delivery retry worker tick failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			w.logInfo("external delivery retry worker stopped")
			return
		case <-ticker.C:
		}
	}
}

func (w DeliveryRetryWorker) RunOnce(ctx context.Context) ([]contracts.ExternalDeliveryOutboxItem, error) {
	if w.Bridge == nil {
		return nil, nil
	}
	statuses := w.Statuses
	if len(statuses) == 0 {
		statuses = []string{"failed", "pending"}
	}
	items, err := w.Bridge.ReplayDueDeliveriesWithOptions(ctx, contracts.ExternalDeliveryReplayOptions{
		Statuses:    statuses,
		Limit:       w.effectiveBatchSize(),
		MaxAttempts: w.effectiveMaxAttempts(),
	})
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		w.logInfo("external delivery retry worker replayed due items", slog.Int("count", len(items)))
	}
	return items, nil
}

func (w DeliveryRetryWorker) effectiveInterval() time.Duration {
	if w.Interval > 0 {
		return w.Interval
	}
	return defaultDeliveryRetryInterval
}

func (w DeliveryRetryWorker) effectiveBatchSize() int {
	if w.BatchSize > 0 {
		return w.BatchSize
	}
	return defaultDeliveryRetryBatchSize
}

func (w DeliveryRetryWorker) effectiveMaxAttempts() int {
	if w.MaxAttempts < 0 {
		return 0
	}
	return w.MaxAttempts
}

func (w DeliveryRetryWorker) logInfo(msg string, attrs ...slog.Attr) {
	if w.Logger == nil {
		return
	}
	w.Logger.LogAttrs(context.Background(), slog.LevelInfo, msg, attrs...)
}

func (w DeliveryRetryWorker) logError(msg string, attrs ...slog.Attr) {
	if w.Logger == nil {
		return
	}
	w.Logger.LogAttrs(context.Background(), slog.LevelError, msg, attrs...)
}
