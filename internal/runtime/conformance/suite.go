package conformance

import (
	"context"
	"errors"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/config"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/model/client"
	runtimedriver "znt/internal/runtime/driver"
)

type Harness struct {
	Core *core.Core
}

func NewHarness(t *testing.T) Harness {
	t.Helper()
	appCore, err := core.New(config.Config{
		ServiceName: "clean-core",
		Version:     "test",
		Env:         "test",
		HTTPAddr:    ":0",
		LogLevel:    "error",
		Readiness:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	appCore.Model = client.StubModelClient{}
	appCore.Coordinator.Model = appCore.Model
	return Harness{Core: appCore}
}

func AssertDriverRegistered(t *testing.T, registry *runtimedriver.Registry, kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) runtimedriver.Driver {
	t.Helper()
	driver, err := registry.Get(kind, contract)
	if err != nil {
		t.Fatal(err)
	}
	if driver.Kind() != kind || driver.Contract() != contract {
		t.Fatalf("expected driver %s/%s, got %s/%s", kind, contract, driver.Kind(), driver.Contract())
	}
	return driver
}

func AssertUnsupportedFailsClosed(t *testing.T, registry *runtimedriver.Registry, kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) {
	t.Helper()
	_, err := registry.Get(kind, contract)
	if err == nil {
		t.Fatalf("expected %s/%s to fail closed", kind, contract)
	}
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeAgentRuntimeDriverUnavailable {
		t.Fatalf("expected %s, got %#v", contracts.CodeAgentRuntimeDriverUnavailable, err)
	}
}

func AssertRunEvidence(t *testing.T, appCore *core.Core, driver runtimedriver.Driver, envelope contracts.AgentEnvelope, kind contracts.AgentCarrierKind, contract contracts.RuntimeContractKind) contracts.AgentRun {
	t.Helper()
	result, err := driver.StartRun(context.Background(), runtimedriver.StartRunRequest{Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	run, err := appCore.Runs.Get(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.CarrierKind != kind || run.RuntimeContract != contract {
		t.Fatalf("expected run carrier snapshot %s/%s, got %#v", kind, contract, run)
	}
	if run.VersionSnapshot.CarrierKind != kind || run.VersionSnapshot.RuntimeContract != contract {
		t.Fatalf("expected version snapshot carrier %s/%s, got %#v", kind, contract, run.VersionSnapshot)
	}
	events, err := appCore.Trace.ListByRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTraceType(events, contracts.TraceRunCreated) || !hasTraceType(events, contracts.TraceDecisionCompleted) {
		t.Fatalf("expected run trace lifecycle evidence, got %#v", events)
	}
	return run
}

func PublishDraft(t *testing.T, appCore *core.Core, source agentpackage.AgentPackageSource, agentID contracts.AgentID, version contracts.AgentVersion) contracts.AgentPackageVersion {
	t.Helper()
	draft, err := appCore.Packages.CreateDraft(context.Background(), "tenant_1", agentID, version, source, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appCore.Packages.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester"); err != nil {
		t.Fatal(err)
	}
	release, err := appCore.Packages.PublishDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func hasTraceType(events []contracts.TraceEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
