package conformance

import (
	"context"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

func TestNativeManagedConformance(t *testing.T) {
	harness := NewHarness(t)
	driver := AssertDriverRegistered(t, harness.Core.RuntimeDrivers, contracts.AgentCarrierKindNativeAgent, contracts.RuntimeContractManaged)

	run := AssertRunEvidence(t, harness.Core, driver, contracts.AgentEnvelope{
		EnvelopeID: "env_conformance_native",
		TraceID:    "trace_conformance_native",
		Target:     contracts.AgentTarget{AgentID: "test-agent", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "native conformance"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	}, contracts.AgentCarrierKindNativeAgent, contracts.RuntimeContractManaged)

	if run.SourceKind != contracts.AgentSourceKindPackage || run.VersionSnapshot.SourceKind != contracts.AgentSourceKindPackage {
		t.Fatalf("expected native package source snapshot, got %#v", run)
	}
	if run.Status != contracts.RunCompleted {
		t.Fatalf("expected completed native run, got %#v", run)
	}
}

func agentPackageSourceForConformance() agentpackage.AgentPackageSource {
	return agentpackage.AgentPackageSource{
		Prompt: "conformance prompt",
		Metadata: map[string]any{
			"name":          "Conformance Agent",
			"manifest_hash": "sha256:native-conformance",
		},
	}
}

func TestUnsupportedRuntimeContractFailsClosed(t *testing.T) {
	harness := NewHarness(t)
	AssertUnsupportedFailsClosed(t, harness.Core.RuntimeDrivers, contracts.AgentCarrierKindExternalRuntime, contracts.RuntimeContractConnected)
	AssertUnsupportedFailsClosed(t, harness.Core.RuntimeDrivers, contracts.AgentCarrierKindWorkflowGraph, contracts.RuntimeContractManaged)
}

func TestNativeCarrierReleaseEvidence(t *testing.T) {
	harness := NewHarness(t)
	release := PublishDraft(t, harness.Core, agentPackageSourceForConformance(), "conformance-native", "v1")
	if _, err := harness.Core.Packages.EnsureAgentAssetVersionForTenant(context.Background(), "tenant_1", "conformance-native", "v1", "tester"); err != nil {
		t.Fatal(err)
	}
	if release.CarrierKind != contracts.AgentCarrierKindNativeAgent ||
		release.RuntimeContract != contracts.RuntimeContractManaged ||
		release.SourceKind != contracts.AgentSourceKindPackage ||
		release.ConformanceStatus != contracts.RuntimeConformanceUnknown {
		t.Fatalf("expected native release carrier evidence, got %#v", release)
	}
	assets, err := harness.Core.Packages.ListAgentAssets(context.Background(), "tenant_1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, asset := range assets {
		if asset.AgentID == "conformance-native" {
			found = true
			if asset.CarrierKind != contracts.AgentCarrierKindNativeAgent ||
				asset.RuntimeContract != contracts.RuntimeContractManaged ||
				asset.SourceKind != contracts.AgentSourceKindPackage ||
				asset.ManifestHash != release.ManifestHash {
				t.Fatalf("expected native carrier asset evidence, got %#v", asset)
			}
		}
	}
	if !found {
		t.Fatal("expected published native asset")
	}
}
