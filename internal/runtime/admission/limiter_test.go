package admission

import (
	"errors"
	"testing"

	"znt/internal/contracts"
)

func TestLimiterRejectsGlobalTenantAndAgentLimits(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		limiter := New(Config{MaxRunningRuns: 1})
		release, err := limiter.AcquireRun("tenant_1", "agent_1")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if _, err := limiter.AcquireRun("tenant_2", "agent_2"); !isAdmissionRejected(err) {
			t.Fatalf("expected global admission rejection, got %v", err)
		}
	})

	t.Run("tenant", func(t *testing.T) {
		limiter := New(Config{TenantMaxRunningRuns: 1})
		release, err := limiter.AcquireRun("tenant_1", "agent_1")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if _, err := limiter.AcquireRun("tenant_1", "agent_2"); !isAdmissionRejected(err) {
			t.Fatalf("expected tenant admission rejection, got %v", err)
		}
	})

	t.Run("agent", func(t *testing.T) {
		limiter := New(Config{AgentMaxRunningRuns: 1})
		release, err := limiter.AcquireRun("tenant_1", "agent_1")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if _, err := limiter.AcquireRun("tenant_1", "agent_1"); !isAdmissionRejected(err) {
			t.Fatalf("expected agent admission rejection, got %v", err)
		}
	})
}

func TestLimiterReleaseAllowsNextRun(t *testing.T) {
	limiter := New(Config{MaxRunningRuns: 1})
	release, err := limiter.AcquireRun("tenant_1", "agent_1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if releaseNext, err := limiter.AcquireRun("tenant_1", "agent_1"); err != nil {
		t.Fatalf("expected release to free slot: %v", err)
	} else {
		releaseNext()
	}
}

func TestLimiterStatsTrackRunningAndRejected(t *testing.T) {
	limiter := New(Config{MaxRunningRuns: 1, TenantMaxRunningRuns: 1, AgentMaxRunningRuns: 1})
	release, err := limiter.AcquireRun("tenant_1", "agent_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.AcquireRun("tenant_1", "agent_1"); !isAdmissionRejected(err) {
		t.Fatalf("expected admission rejection, got %v", err)
	}
	stats := limiter.Stats()
	if stats.RunningRuns != 1 || stats.RejectedRunsTotal != 1 || stats.MaxRunningRuns != 1 {
		t.Fatalf("unexpected stats while running: %#v", stats)
	}
	release()
	stats = limiter.Stats()
	if stats.RunningRuns != 0 || stats.RejectedRunsTotal != 1 {
		t.Fatalf("unexpected stats after release: %#v", stats)
	}
}

func isAdmissionRejected(err error) bool {
	var runtimeErr *contracts.RuntimeError
	return errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeAdmissionRejected
}
