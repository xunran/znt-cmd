package source

import (
	"context"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
)

func TestSourceAdaptersCompileCarrierEvidence(t *testing.T) {
	registry := DefaultRegistry()
	packageAdapter, err := registry.Get(contracts.AgentSourceKindPackage)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := packageAdapter.Normalize(context.Background(), NormalizeRequest{
		AgentID: "agent_1",
		Version: "v1",
		Source:  agentpackage.AgentPackageSource{Prompt: "prompt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := packageAdapter.Compile(context.Background(), normalized)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.CarrierKind != contracts.AgentCarrierKindNativeAgent || compiled.RuntimeContract != contracts.RuntimeContractManaged {
		t.Fatalf("expected native managed carrier, got %#v", compiled)
	}

	pluginAdapter, err := registry.Get(contracts.AgentSourceKindPlugin)
	if err != nil {
		t.Fatal(err)
	}
	pluginSource, err := pluginAdapter.Normalize(context.Background(), NormalizeRequest{
		AgentID: "agent_1",
		Version: "v1",
		Plugin: agentpackage.AgentPluginSource{
			ProviderID: "crm-plugin",
			Prompt:     "plugin prompt",
			Metadata:   map[string]any{"manifest_hash": "sha256:manifest"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginCompiled, err := pluginAdapter.Compile(context.Background(), pluginSource)
	if err != nil {
		t.Fatal(err)
	}
	if pluginCompiled.CarrierKind != contracts.AgentCarrierKindAgentPluginSource ||
		pluginCompiled.RuntimeContract != contracts.RuntimeContractManaged ||
		pluginCompiled.ManifestHash != "sha256:manifest" {
		t.Fatalf("expected plugin source carrier, got %#v", pluginCompiled)
	}
}
