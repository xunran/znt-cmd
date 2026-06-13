package plugin

import (
	"strings"
	"testing"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
	toolcatalog "znt/internal/tool/catalog"
)

func TestBuildSourceFromAgentPluginManifest(t *testing.T) {
	manifest := AgentPluginManifest{
		ManifestVersion: "2026-06-12",
		ProviderID:      "crm-plugin",
		Agent: AgentPluginAgentManifest{
			AgentID:     "crm-agent",
			Version:     "v1",
			Name:        "CRM Agent",
			Description: "Handles CRM work.",
			Prompt:      "You are a CRM agent.",
		},
		Tools: []AgentPluginToolManifest{{
			ToolID:       "crm.lookup",
			Name:         "CRM lookup",
			Description:  "Lookup a customer.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
		}},
		Hooks: []runtimehook.HookManifest{{
			HookID: "crm-context",
			Name:   "CRM context",
			Phase:  runtimehook.BeforeContextBuild,
			Status: "enabled",
		}},
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode: "balanced",
			},
		},
	}

	source, err := BuildSource(SourceInput{
		Manifest: manifest,
		Overrides: agentpackage.AgentPluginSource{
			Strategies: contracts.AgentStrategies{
				Context: contracts.ContextStrategy{
					Mode:                "long_context",
					RetrievalMaxResults: contracts.IntPtr(6),
				},
			},
			Metadata: map[string]any{"manifest_hash": "sha256:override"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.ProviderID != "crm-plugin" || source.ManifestVersion != "2026-06-12" || source.Prompt != "You are a CRM agent." {
		t.Fatalf("unexpected source identity: %#v", source)
	}
	if source.Metadata["name"] != "CRM Agent" || source.Metadata["description"] != "Handles CRM work." {
		t.Fatalf("expected agent metadata from manifest, got %#v", source.Metadata)
	}
	if source.Metadata["manifest_hash"] != ManifestHash(manifest) || source.Metadata["manifest_hash"] == "sha256:override" {
		t.Fatalf("expected canonical manifest hash metadata, got %#v", source.Metadata)
	}
	if len(source.ToolBindings.AllowedToolIDs) != 1 || source.ToolBindings.AllowedToolIDs[0] != "crm.lookup" {
		t.Fatalf("expected manifest tools to become tool bindings, got %#v", source.ToolBindings)
	}
	if len(source.RuntimeHooks.Hooks) != 1 || source.RuntimeHooks.Hooks[0].ProviderID != "crm-plugin" || source.RuntimeHooks.Hooks[0].Phase != string(runtimehook.BeforeContextBuild) {
		t.Fatalf("expected manifest hook to become runtime hook binding, got %#v", source.RuntimeHooks)
	}
	if source.Strategies.Context.Mode != "long_context" || contracts.IntValue(source.Strategies.Context.RetrievalMaxResults) != 6 {
		t.Fatalf("expected override strategy to win, got %#v", source.Strategies.Context)
	}
}

func TestToolManifestsFromAgentPluginManifest(t *testing.T) {
	manifest := AgentPluginManifest{
		ManifestVersion: "v1",
		ProviderID:      "crm-plugin",
		Tools: []AgentPluginToolManifest{{
			ToolID:       "crm.lookup",
			Operation:    "customers.lookup",
			GroupID:      "crm",
			Name:         "CRM lookup",
			Description:  "Lookup a customer.",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
		}},
	}

	tools := ToolManifests("", manifest)
	if len(tools) != 1 {
		t.Fatalf("expected one tool manifest, got %#v", tools)
	}
	tool := tools[0]
	if tool.Executor.Type != toolcatalog.ExecutorTypeAgentPlugin || tool.Executor.ProviderID != "crm-plugin" || tool.Executor.Operation != "customers.lookup" {
		t.Fatalf("expected agent plugin executor, got %#v", tool.Executor)
	}
	if tool.RiskLevel != contracts.RiskLow || tool.Visibility != contracts.ToolProtected || tool.Status != toolcatalog.StatusEnabled || tool.Version != "v1" {
		t.Fatalf("expected tool defaults, got %#v", tool)
	}
}

func TestDecodeManifestRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeManifest([]byte(`{"agent":`)); err == nil {
		t.Fatal("expected invalid json to fail")
	}
}

func TestDecodeManifestRejectsUnknownFields(t *testing.T) {
	_, err := DecodeManifest([]byte(`{"agent":{"agent_id":"crm-agent","prompt":"ok"},"unexpected":true}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "unexpected"`) {
		t.Fatalf("expected unknown manifest field rejection, got %v", err)
	}
}

func TestBuildSourceRejectsInvalidManifestTool(t *testing.T) {
	_, err := BuildSource(SourceInput{
		ProviderID: "crm-plugin",
		Manifest: AgentPluginManifest{
			Agent: AgentPluginAgentManifest{Prompt: "You are a CRM agent."},
			Tools: []AgentPluginToolManifest{{
				Name:         "CRM lookup",
				Description:  "Lookup a customer.",
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object"},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "plugin.tools[0].tool_id") {
		t.Fatalf("expected invalid tool path, got %v", err)
	}
}

func TestBuildSourceRejectsInvalidManifestHook(t *testing.T) {
	_, err := BuildSource(SourceInput{
		ProviderID: "crm-plugin",
		Manifest: AgentPluginManifest{
			Agent: AgentPluginAgentManifest{Prompt: "You are a CRM agent."},
			Hooks: []runtimehook.HookManifest{{
				HookID: "crm-context",
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "plugin.hooks[0].phase") {
		t.Fatalf("expected invalid hook path, got %v", err)
	}
}
