package agentpackage

import (
	"testing"
	"time"

	"znt/internal/contracts"
)

func TestCompileReadsStructuredSkillDefinitions(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo"},
		},
		Skills: []contracts.SkillDefinitionRef{{SkillID: "pkg.report", Version: "v1"}},
		SkillDefinitions: []contracts.SkillDefinition{
			{
				Card: contracts.SkillCard{
					SkillID:     "pkg.report",
					Version:     "v1",
					Name:        "Package report",
					Description: "Write package reports",
					Tags:        []string{"report"},
					WhenToUse:   []string{"report writing"},
					RiskLevel:   contracts.RiskLow,
				},
				Instruction: contracts.SkillInstruction{
					SkillID:            "pkg.report",
					Content:            "Use package report format.",
					OutputRequirements: []string{"include summary"},
					Constraints:        []string{"cite artifacts"},
				},
				Resources:               []contracts.SkillResourceRef{{ResourceID: "template_1", Type: "template", URI: "memory://template_1", LoadPolicy: "on_demand"}},
				RecommendedTools:        []string{"artifact.read"},
				AllowedTools:            []string{"echo"},
				RecommendedMemoryReads:  []string{"project_notes"},
				RecommendedMemoryWrites: []string{"report_summary"},
				RecommendedHandoffs:     []string{"review-agent"},
				CompletionCriteria:      []string{"summary accepted"},
				OutputSchema:            map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.SkillDefinitions) != 1 {
		t.Fatalf("expected compiled skill definition, got %#v", definition.SkillDefinitions)
	}
	skill := definition.SkillDefinitions[0]
	if skill.Card.SkillID != "pkg.report" || skill.Instruction.Content != "Use package report format." {
		t.Fatalf("unexpected skill definition: %#v", skill)
	}
	if len(skill.Instruction.OutputRequirements) != 1 || len(skill.Instruction.Constraints) != 1 {
		t.Fatalf("expected output requirements and constraints, got %#v", skill.Instruction)
	}
	if len(skill.Resources) != 1 || skill.Resources[0].URI != "memory://template_1" {
		t.Fatalf("expected skill resources, got %#v", skill.Resources)
	}
	if len(skill.Card.ResourceRefs) != 1 || skill.Card.ResourceRefs[0] != "template_1" {
		t.Fatalf("expected card resource refs, got %#v", skill.Card.ResourceRefs)
	}
	if len(skill.RecommendedTools) != 1 || len(skill.AllowedTools) != 1 || len(skill.RecommendedMemoryReads) != 1 || len(skill.RecommendedMemoryWrites) != 1 || len(skill.RecommendedHandoffs) != 1 || len(skill.CompletionCriteria) != 1 {
		t.Fatalf("expected V3 skill guidance fields, got %#v", skill)
	}
	if skill.OutputSchema["type"] != "object" {
		t.Fatalf("expected output schema to compile, got %#v", skill.OutputSchema)
	}
}

func TestCompileRejectsInvalidStructuredSkillDefinition(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		SkillDefinitions: []contracts.SkillDefinition{{
			Card: contracts.SkillCard{
				SkillID:   "pkg.report",
				Version:   "v1",
				RiskLevel: "extreme",
			},
		}},
	})
	if err == nil {
		t.Fatal("expected invalid risk level to fail compilation")
	}
}

func TestCompileRejectsDuplicateSkillDefinitions(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		SkillDefinitions: []contracts.SkillDefinition{
			{Card: contracts.SkillCard{SkillID: "pkg.report", Version: "v1"}},
			{Card: contracts.SkillCard{SkillID: "pkg.report", Version: "v1"}},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate skill definition to fail compilation")
	}
}

func TestCompileRejectsSkillDefinitionsFromMetadata(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"skill_definitions": []any{
				map[string]any{"skill_id": "legacy.skill", "version": "v1"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected metadata skill fields to fail compilation")
	}
}

func TestCompileRejectsRuntimeLimitsFromMetadata(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"max_steps": -1,
		},
	})
	if err == nil {
		t.Fatal("expected metadata runtime fields to fail compilation")
	}
}

func TestCompileUsesBusinessFlowRuntimeDefaults(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Runtime.MaxSteps < 8 || definition.Runtime.MaxToolCalls < 8 {
		t.Fatalf("expected business flow runtime defaults, got %#v", definition.Runtime)
	}
}

func TestCompileReadsContextStrategy(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode:                "long_context",
				RecentMessageLimit:  contracts.IntPtr(40),
				RetrievalMaxResults: contracts.IntPtr(16),
				TaskHistoryMaxItems: contracts.IntPtr(20),
				ContextTokenBudget:  contracts.IntPtr(12000),
				EnabledSources:      []string{"conversation_recent", "agent_plugin_context"},
				SourceBudgets: map[string]int{
					"agent_plugin_context": 2000,
				},
				Compression: contracts.ContextCompressionStrategy{
					Enabled:         true,
					Mode:            "llm_then_truncate",
					TriggerRatio:    80,
					TargetTokens:    4000,
					PromptProfileID: "context.compression.factual_v1",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	contextStrategy := definition.Strategies.Context
	if contextStrategy.Mode != "long_context" || contracts.IntValue(contextStrategy.RecentMessageLimit) != 40 || contracts.IntValue(contextStrategy.RetrievalMaxResults) != 16 {
		t.Fatalf("expected context strategy to compile, got %#v", contextStrategy)
	}
	if contextStrategy.Compression.Mode != "llm_then_truncate" || contextStrategy.Compression.TargetTokens != 4000 {
		t.Fatalf("expected compression strategy to compile, got %#v", contextStrategy.Compression)
	}
	if contextStrategy.SourceBudgets["agent_plugin_context"] != 2000 {
		t.Fatalf("expected source budget to compile, got %#v", contextStrategy.SourceBudgets)
	}
}

func TestCompileAppliesRuntimeToolAndRepairStrategies(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"legacy.echo"},
		},
		Strategies: contracts.AgentStrategies{
			Model: contracts.ModelStrategy{
				Provider:        "openai-compatible",
				Model:           "agent-model",
				MaxOutputTokens: 256,
				TimeoutMS:       1200,
				Streaming:       contracts.BoolPtr(false),
			},
			Prompt: contracts.PromptStrategy{
				SystemPrompt:    "Return one JSON decision.",
				DeveloperPrompt: "Prefer CRM tools.",
			},
			Tools: contracts.ToolUseStrategy{
				AllowedToolIDs:             []string{"crm.lookup"},
				DeniedToolIDs:              []string{"crm.delete"},
				PreferredToolIDs:           []string{"crm.lookup"},
				ToolChoiceMode:             "conservative",
				MaxToolCalls:               contracts.IntPtr(0),
				RequireApprovalAtRiskLevel: contracts.RiskMedium,
			},
			Runtime: contracts.RuntimeStrategy{
				MaxSteps:                   contracts.IntPtr(6),
				MaxDurationSeconds:         contracts.IntPtr(25),
				MaxModelRetries:            contracts.IntPtr(0),
				MaxConsecutiveToolFailures: contracts.IntPtr(1),
				ExecutionMode:              "sync",
			},
			Repair: contracts.RepairStrategy{
				MaxRepairAttempts: contracts.IntPtr(0),
			},
			Collaboration: contracts.CollaborationStrategy{
				DelegationMode:     "disabled",
				MaxHandoffDepth:    contracts.IntPtr(2),
				MaxChildTasks:      contracts.IntPtr(3),
				DefaultHandoffMode: contracts.HandoffHybrid,
				MaxContextTokens:   contracts.IntPtr(500),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.SystemPrompt != "Return one JSON decision." || definition.DeveloperPrompt != "Prefer CRM tools." {
		t.Fatalf("expected prompt strategy to compile, got system=%q developer=%q", definition.SystemPrompt, definition.DeveloperPrompt)
	}
	if definition.Strategies.Model.Provider != "openai-compatible" || definition.Strategies.Model.Model != "agent-model" || definition.Strategies.Model.MaxOutputTokens != 256 {
		t.Fatalf("expected model strategy to compile, got %#v", definition.Strategies.Model)
	}
	if definition.Strategies.Model.Streaming == nil || *definition.Strategies.Model.Streaming {
		t.Fatalf("expected streaming=false model strategy to compile, got %#v", definition.Strategies.Model.Streaming)
	}
	if len(definition.Tools.AllowedToolIDs) != 1 || definition.Tools.AllowedToolIDs[0] != "crm.lookup" || !containsTestString(definition.Tools.DeniedToolIDs, "crm.delete") {
		t.Fatalf("expected tool strategy to override bindings, got %#v", definition.Tools)
	}
	if definition.Runtime.MaxToolCalls != 0 || definition.Runtime.MaxSteps != 6 || definition.Runtime.MaxDuration != 25*time.Second {
		t.Fatalf("expected runtime limits from strategies, got %#v", definition.Runtime)
	}
	if definition.Runtime.MaxModelRetries != 0 || definition.Runtime.MaxRepairAttempts != 0 || definition.Runtime.MaxConsecutiveToolFailures != 1 {
		t.Fatalf("expected zero and failure limits from strategies, got %#v", definition.Runtime)
	}
	if definition.Runtime.MaxHandoffDepth != 2 || definition.Runtime.MaxChildTasks != 3 || !containsTestString(definition.Tools.DeniedToolIDs, "origin.agent.delegate") {
		t.Fatalf("expected collaboration strategy to compile, got runtime=%#v tools=%#v", definition.Runtime, definition.Tools)
	}
}

func TestCompilePluginSource(t *testing.T) {
	definition, err := CompilePlugin("agent_1", "v1", AgentPluginSource{
		ProviderID:      "crm-plugin",
		ManifestVersion: "2026-06-12",
		Prompt:          "You are plugin backed agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode:                "balanced",
				RecentMessageLimit:  contracts.IntPtr(0),
				RetrievalMaxResults: contracts.IntPtr(4),
			},
		},
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"crm.lookup"},
		},
		Metadata: map[string]any{"manifest_hash": "sha256:manifest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.SourceKind != contracts.AgentSourceKindPlugin || definition.SourceProviderID != "crm-plugin" || definition.ManifestVersion != "2026-06-12" {
		t.Fatalf("expected plugin source metadata, got %#v", definition)
	}
	if definition.ManifestHash != "sha256:manifest" {
		t.Fatalf("expected plugin manifest hash to compile, got %#v", definition)
	}
	if contracts.IntValue(definition.Strategies.Context.RecentMessageLimit) != 0 || len(definition.Tools.AllowedToolIDs) != 1 {
		t.Fatalf("expected plugin strategy and tool binding to compile, got %#v", definition)
	}
}

func TestCompileRejectsPluginSourceWithoutProviderID(t *testing.T) {
	_, err := CompilePlugin("agent_1", "v1", AgentPluginSource{
		Prompt: "You are plugin backed agent 1.",
	})
	if err == nil {
		t.Fatal("expected plugin source without provider_id to fail compilation")
	}
}

func TestCompileRejectsPluginSourceConnectionMetadata(t *testing.T) {
	_, err := CompilePlugin("agent_1", "v1", AgentPluginSource{
		ProviderID: "crm-plugin",
		Prompt:     "You are plugin backed agent 1.",
		Metadata: map[string]any{
			"service_connection_id": "duplicated-connection",
		},
	})
	if err == nil {
		t.Fatal("expected plugin source connection metadata to fail compilation")
	}
}

func TestCompileRejectsUnknownSourceKind(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		SourceKind: "sidecar",
		Prompt:     "You are agent 1.",
	})
	if err == nil {
		t.Fatal("expected unknown source kind to fail compilation")
	}
}

func TestCompileRejectsInvalidContextStrategy(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode:               "everything",
				RecentMessageLimit: contracts.IntPtr(-1),
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid context strategy to fail compilation")
	}
}

func TestCompileRejectsInvalidCompressionStrategy(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Mode:         "magic",
					TriggerRatio: 120,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid compression strategy to fail compilation")
	}
}

func TestCompileRejectsInvalidCompressionFailureMode(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled:     true,
					Mode:        "llm",
					FailureMode: "panic",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid compression failure mode to fail compilation")
	}
}

func TestCompileRejectsCompressionSummaryMemoryWrite(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled:              true,
					Mode:                 "llm",
					WriteSummaryToMemory: true,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected compression memory summary write to fail compilation")
	}
}

func TestCompileRejectsInvalidRuntimeAndToolStrategies(t *testing.T) {
	tests := []struct {
		name       string
		strategies contracts.AgentStrategies
	}{
		{
			name: "conflicting tools",
			strategies: contracts.AgentStrategies{
				Tools: contracts.ToolUseStrategy{
					AllowedToolIDs: []string{"echo"},
					DeniedToolIDs:  []string{"echo"},
				},
			},
		},
		{
			name: "bad tool choice mode",
			strategies: contracts.AgentStrategies{
				Tools: contracts.ToolUseStrategy{ToolChoiceMode: "magic"},
			},
		},
		{
			name: "negative max tool calls",
			strategies: contracts.AgentStrategies{
				Tools: contracts.ToolUseStrategy{MaxToolCalls: contracts.IntPtr(-1)},
			},
		},
		{
			name: "bad skill selection mode",
			strategies: contracts.AgentStrategies{
				Skills: contracts.SkillUseStrategy{SelectionMode: "magic"},
			},
		},
		{
			name: "negative max selected skills",
			strategies: contracts.AgentStrategies{
				Skills: contracts.SkillUseStrategy{MaxSelectedSkills: -1},
			},
		},
		{
			name: "conflicting skills",
			strategies: contracts.AgentStrategies{
				Skills: contracts.SkillUseStrategy{
					EnabledSkillIDs:  []string{"planning"},
					DisabledSkillIDs: []string{"planning"},
				},
			},
		},
		{
			name: "duplicate enabled skill",
			strategies: contracts.AgentStrategies{
				Skills: contracts.SkillUseStrategy{EnabledSkillIDs: []string{"planning", "planning"}},
			},
		},
		{
			name: "zero max steps",
			strategies: contracts.AgentStrategies{
				Runtime: contracts.RuntimeStrategy{MaxSteps: contracts.IntPtr(0)},
			},
		},
		{
			name: "bad approval risk",
			strategies: contracts.AgentStrategies{
				Tools: contracts.ToolUseStrategy{RequireApprovalAtRiskLevel: "extreme"},
			},
		},
		{
			name: "bad model temperature",
			strategies: contracts.AgentStrategies{
				Model: contracts.ModelStrategy{Temperature: floatPtr(3)},
			},
		},
		{
			name: "bad delegation mode",
			strategies: contracts.AgentStrategies{
				Collaboration: contracts.CollaborationStrategy{DelegationMode: "magic"},
			},
		},
		{
			name: "negative handoff context tokens",
			strategies: contracts.AgentStrategies{
				Collaboration: contracts.CollaborationStrategy{MaxContextTokens: contracts.IntPtr(-1)},
			},
		},
		{
			name: "negative max memory items",
			strategies: contracts.AgentStrategies{
				Memory: contracts.MemoryUseStrategy{MaxMemoryItems: contracts.IntPtr(-1)},
			},
		},
		{
			name: "bad memory auto write mode",
			strategies: contracts.AgentStrategies{
				Memory: contracts.MemoryUseStrategy{AutoWriteMode: "always"},
			},
		},
		{
			name: "unsupported memory post run summary",
			strategies: contracts.AgentStrategies{
				Memory: contracts.MemoryUseStrategy{AutoWriteMode: "post_run_summary"},
			},
		},
		{
			name: "empty memory scope",
			strategies: contracts.AgentStrategies{
				Memory: contracts.MemoryUseStrategy{ReadScopes: []string{"user", ""}},
			},
		},
		{
			name: "bad knowledge search mode",
			strategies: contracts.AgentStrategies{
				Knowledge: contracts.KnowledgeUseStrategy{SearchMode: "semantic_magic"},
			},
		},
		{
			name: "bad knowledge inject mode",
			strategies: contracts.AgentStrategies{
				Knowledge: contracts.KnowledgeUseStrategy{InjectMode: "prompt_override"},
			},
		},
		{
			name: "unsupported knowledge retrieved context inject mode",
			strategies: contracts.AgentStrategies{
				Knowledge: contracts.KnowledgeUseStrategy{InjectMode: "retrieved_context"},
			},
		},
		{
			name: "negative knowledge max results",
			strategies: contracts.AgentStrategies{
				Knowledge: contracts.KnowledgeUseStrategy{MaxResults: -1},
			},
		},
		{
			name: "duplicate knowledge base",
			strategies: contracts.AgentStrategies{
				Knowledge: contracts.KnowledgeUseStrategy{KnowledgeBaseIDs: []contracts.KnowledgeBaseID{"kb_1", "kb_1"}},
			},
		},
		{
			name: "bad output mode",
			strategies: contracts.AgentStrategies{
				Output: contracts.OutputStrategy{OutputMode: "artifact_first"},
			},
		},
		{
			name: "unsupported output schema",
			strategies: contracts.AgentStrategies{
				Output: contracts.OutputStrategy{OutputMode: "decision_json", OutputSchema: map[string]any{"type": "object"}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile("agent_1", "v1", AgentPackageSource{
				Prompt:     "You are agent 1.",
				Strategies: tc.strategies,
			})
			if err == nil {
				t.Fatal("expected invalid strategy to fail compilation")
			}
		})
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func containsTestString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestCompileReadsRepairAndCollaborationRuntimeFromStrategies(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Strategies: contracts.AgentStrategies{
			Repair: contracts.RepairStrategy{
				MaxRepairAttempts: contracts.IntPtr(2),
			},
			Collaboration: contracts.CollaborationStrategy{
				MaxHandoffDepth: contracts.IntPtr(3),
				MaxChildTasks:   contracts.IntPtr(4),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Runtime.MaxRepairAttempts != 2 {
		t.Fatalf("expected max_repair_attempts from strategies, got %#v", definition.Runtime)
	}
	if definition.Runtime.MaxHandoffDepth != 3 || definition.Runtime.MaxChildTasks != 4 {
		t.Fatalf("expected handoff runtime limits from strategies, got %#v", definition.Runtime)
	}
}

func TestCompileReadsCollaboratorsAndExports(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID:             "crm-agent",
			Name:                "CRM Agent",
			Description:         "CRM specialist",
			Capabilities:        []string{"customer history"},
			AllowedHandoffModes: []contracts.HandoffMode{contracts.HandoffHybrid},
			DefaultHandoffMode:  contracts.HandoffHybrid,
			MaxContextTokens:    300,
			RequiresApproval:    true,
		}},
		Exports: contracts.AgentExports{
			Tools: []contracts.AgentExportedTool{{
				ToolID:      "agent_1.customer.summary",
				Name:        "Customer summary",
				Description: "Summarize a customer.",
				InputSchema: map[string]any{"type": "object"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Collaborators) != 1 || definition.Collaborators[0].AgentID != "crm-agent" {
		t.Fatalf("expected collaborator, got %#v", definition.Collaborators)
	}
	if definition.Collaborators[0].DefaultHandoffMode != contracts.HandoffHybrid || definition.Collaborators[0].MaxContextTokens != 300 || !definition.Collaborators[0].RequiresApproval {
		t.Fatalf("expected collaborator handoff settings, got %#v", definition.Collaborators[0])
	}
	if len(definition.Exports.Tools) != 1 {
		t.Fatalf("expected exported tool, got %#v", definition.Exports)
	}
	tool := definition.Exports.Tools[0]
	if tool.RiskLevel != contracts.RiskLow || tool.Visibility != contracts.ToolProtected || tool.Status != "enabled" || tool.Operation != tool.ToolID {
		t.Fatalf("expected exported tool defaults, got %#v", tool)
	}
}

func TestCompileRejectsInvalidCollaboratorHandoffMode(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID:             "crm-agent",
			AllowedHandoffModes: []contracts.HandoffMode{"teleport"},
		}},
	})
	if err == nil {
		t.Fatal("expected invalid collaborator handoff mode to fail compilation")
	}
}

func TestCompileReadsToolGroupBindings(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolGroupIDs: []string{"crm"},
			DeniedToolGroupIDs:  []string{"billing"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Tools.AllowedToolGroupIDs) != 1 || definition.Tools.AllowedToolGroupIDs[0] != "crm" {
		t.Fatalf("expected allowed tool group binding, got %#v", definition.Tools)
	}
	if len(definition.Tools.DeniedToolGroupIDs) != 1 || definition.Tools.DeniedToolGroupIDs[0] != "billing" {
		t.Fatalf("expected denied tool group binding, got %#v", definition.Tools)
	}
}

func TestCompileRejectsConflictingToolGroupBindings(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolGroupIDs: []string{"crm"},
			DeniedToolGroupIDs:  []string{"crm"},
		},
	})
	if err == nil {
		t.Fatal("expected conflicting tool group binding to fail compilation")
	}
}

func TestCompileReadsRuntimeHooks(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:        "crm-ranker",
				ProviderType:  "static_hook_host",
				ProviderID:    "crm-hook-host",
				Phase:         "after_candidate_retrieval",
				Enabled:       true,
				TimeoutMS:     300,
				FailurePolicy: "ignore",
				ApprovalPolicy: contracts.RuntimeHookApprovalPolicy{
					RequireApproval: true,
					ProviderTypes:   []string{"static_hook_host"},
					Phases:          []string{"after_candidate_retrieval"},
					FailurePolicies: []string{"ignore"},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.RuntimeHooks.Mode != "data_hooks" || len(definition.RuntimeHooks.Hooks) != 1 {
		t.Fatalf("expected runtime hooks to compile, got %#v", definition.RuntimeHooks)
	}
	if definition.RuntimeHooks.Hooks[0].HookID != "crm-ranker" {
		t.Fatalf("unexpected runtime hook binding: %#v", definition.RuntimeHooks.Hooks[0])
	}
	if !definition.RuntimeHooks.Hooks[0].ApprovalPolicy.RequireApproval || len(definition.RuntimeHooks.Hooks[0].ApprovalPolicy.ProviderTypes) != 1 {
		t.Fatalf("expected runtime hook approval policy to compile, got %#v", definition.RuntimeHooks.Hooks[0].ApprovalPolicy)
	}
}

func TestCompileRejectsInvalidRuntimeHookPhase(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:  "bad-hook",
				Phase:   "custom_loop",
				Enabled: true,
			}},
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime hook phase to fail compilation")
	}
}

func TestCompileRejectsInvalidRuntimeHookWithoutMode(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID:  "bad-hook",
				Phase:   "custom_loop",
				Enabled: true,
			}},
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime hook phase to fail compilation")
	}
}

func TestCompileRejectsInvalidRuntimeHookApprovalPolicy(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		RuntimeHooks: contracts.AgentRuntimeHooks{
			Mode: "data_hooks",
			Hooks: []contracts.AgentRuntimeHookBinding{{
				HookID: "bad-approval-policy",
				Phase:  "before_model_call",
				ApprovalPolicy: contracts.RuntimeHookApprovalPolicy{
					RequireApproval: true,
					ProviderTypes:   []string{"shell_script"},
				},
			}},
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime hook approval policy to fail compilation")
	}
}
