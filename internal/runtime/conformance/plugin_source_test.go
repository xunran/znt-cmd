package conformance

import (
	"context"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

func TestPluginSourceManagedConformance(t *testing.T) {
	harness := NewHarness(t)
	driver := AssertDriverRegistered(t, harness.Core.RuntimeDrivers, contracts.AgentCarrierKindAgentPluginSource, contracts.RuntimeContractManaged)

	source := agentpackage.PackageSourceFromPlugin(agentpackage.AgentPluginSource{
		ProviderID:      "crm-plugin",
		ManifestVersion: "2026-06-12",
		Prompt:          "plugin managed prompt",
		ToolBindings:    contracts.AgentToolsConfig{},
		RuntimeHooks:    contracts.AgentRuntimeHooks{},
		Metadata: map[string]any{
			"name":          "Plugin Source Agent",
			"manifest_hash": "sha256:plugin-conformance",
		},
	})
	release := PublishDraft(t, harness.Core, source, "conformance-plugin", "v1")
	if release.CarrierKind != contracts.AgentCarrierKindAgentPluginSource ||
		release.RuntimeContract != contracts.RuntimeContractManaged ||
		release.SourceKind != contracts.AgentSourceKindPlugin ||
		release.SourceProviderID != "crm-plugin" ||
		release.ManifestVersion != "2026-06-12" ||
		release.ManifestHash != "sha256:plugin-conformance" {
		t.Fatalf("expected plugin source release evidence, got %#v", release)
	}
	if _, err := harness.Core.Packages.EnsureAgentAssetVersionForTenant(context.Background(), "tenant_1", "conformance-plugin", "v1", "tester"); err != nil {
		t.Fatal(err)
	}
	compiled, err := agentpackage.Compile("conformance-plugin", "v1", source)
	if err != nil {
		t.Fatal(err)
	}
	compiled.TenantID = "tenant_1"
	compiled.PackageVersionID = release.PackageVersionID
	harness.Core.AgentRegistry.Put(compiled)
	if err := harness.Core.AgentRegistry.SetDefaultForTenant("tenant_1", "conformance-plugin", "v1"); err != nil {
		t.Fatal(err)
	}

	run := AssertRunEvidence(t, harness.Core, driver, contracts.AgentEnvelope{
		EnvelopeID: "env_conformance_plugin",
		TraceID:    "trace_conformance_plugin",
		Target:     contracts.AgentTarget{AgentID: "conformance-plugin", Version: "v1"},
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "plugin conformance"},
		Context:    contracts.RuntimeContext{TenantID: "tenant_1", UserID: "user_1"},
	}, contracts.AgentCarrierKindAgentPluginSource, contracts.RuntimeContractManaged)

	if run.SourceKind != contracts.AgentSourceKindPlugin ||
		run.SourceProviderID != "crm-plugin" ||
		run.ManifestHash != "sha256:plugin-conformance" ||
		run.VersionSnapshot.SourceKind != contracts.AgentSourceKindPlugin ||
		run.VersionSnapshot.SourceProviderID != "crm-plugin" {
		t.Fatalf("expected plugin source run snapshot, got %#v", run)
	}

	assets, err := harness.Core.Packages.ListAgentAssets(context.Background(), "tenant_1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, asset := range assets {
		if asset.AgentID == "conformance-plugin" {
			found = true
			if asset.CarrierKind != contracts.AgentCarrierKindAgentPluginSource ||
				asset.RuntimeContract != contracts.RuntimeContractManaged ||
				asset.SourceKind != contracts.AgentSourceKindPlugin ||
				asset.SourceProviderID != "crm-plugin" ||
				asset.ManifestHash != "sha256:plugin-conformance" {
				t.Fatalf("expected plugin carrier asset evidence, got %#v", asset)
			}
		}
	}
	if !found {
		t.Fatal("expected published plugin asset")
	}
}
