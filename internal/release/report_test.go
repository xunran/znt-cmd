package release

import (
	"context"
	"testing"

	"znt/internal/app/config"
	"znt/internal/app/core"
)

func TestBuildGoNoGo(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	report := BuildGoNoGo(context.Background(), appCore, "../../migrations", nil)
	if report.Decision != "go" {
		t.Fatalf("expected go, got %#v", report)
	}
	if len(report.Contract.Frozen) == 0 {
		t.Fatal("expected frozen contract list")
	}
}
