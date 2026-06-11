package agentpackage

import (
	"testing"

	"znt/internal/contracts"
)

func TestCompileReadsSkillDefinitionsFromMetadata(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo"},
		},
		Metadata: map[string]any{
			"skill_definitions": []any{
				map[string]any{
					"skill_id":                  "pkg.report",
					"version":                   "v1",
					"name":                      "Package report",
					"description":               "Write package reports",
					"instruction":               "Use package report format.",
					"output_requirements":       []any{"include summary"},
					"constraints":               []any{"cite artifacts"},
					"tags":                      []any{"report"},
					"when_to_use":               []any{"report writing"},
					"recommended_tools":         []any{"artifact.read"},
					"allowed_tools":             []any{"echo"},
					"recommended_memory_reads":  []any{"project_notes"},
					"recommended_memory_writes": []any{"report_summary"},
					"recommended_handoffs":      []any{"review-agent"},
					"completion_criteria":       []any{"summary accepted"},
					"output_schema": map[string]any{
						"type": "object",
					},
					"resources": []any{
						map[string]any{"resource_id": "template_1", "type": "template", "uri": "memory://template_1", "load_policy": "on_demand"},
					},
				},
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

func TestCompileRejectsInvalidSkillMetadata(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"skill_definitions": []any{
				map[string]any{
					"skill_id":   "pkg.report",
					"version":    "v1",
					"risk_level": "extreme",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid risk level to fail compilation")
	}
}

func TestCompileRejectsDuplicateSkillDefinitions(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"skill_definitions": []any{
				map[string]any{"skill_id": "pkg.report", "version": "v1"},
				map[string]any{"skill_id": "pkg.report", "version": "v1"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate skill definition to fail compilation")
	}
}

func TestCompileRejectsInvalidRuntimeLimits(t *testing.T) {
	_, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"max_steps": -1,
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime limits to fail compilation")
	}
}

func TestCompileReadsRepairAttemptsFromMetadata(t *testing.T) {
	definition, err := Compile("agent_1", "v1", AgentPackageSource{
		Prompt: "You are agent 1.",
		Metadata: map[string]any{
			"max_repair_attempts": 2,
			"max_handoff_depth":   3,
			"max_child_tasks":     4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Runtime.MaxRepairAttempts != 2 {
		t.Fatalf("expected max_repair_attempts from metadata, got %#v", definition.Runtime)
	}
	if definition.Runtime.MaxHandoffDepth != 3 || definition.Runtime.MaxChildTasks != 4 {
		t.Fatalf("expected handoff runtime limits from metadata, got %#v", definition.Runtime)
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
