package readiness

import (
	"context"
	"testing"

	"znt/internal/app/config"
	"znt/internal/app/core"
)

func BenchmarkReadinessBuild(b *testing.B) {
	appCore, err := core.New(config.Default())
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		report := Build(ctx, appCore, "../../migrations")
		if report.Status != "ready" {
			b.Fatalf("expected ready report, got %#v", report)
		}
	}
}
