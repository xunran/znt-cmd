package tool

import (
	"testing"

	"znt/internal/contracts"
)

func TestApplyToolUseStrategyNoTools(t *testing.T) {
	candidates := CandidateSet{
		Tools: []contracts.ToolCard{{ToolID: "crm.lookup"}},
		Capabilities: []contracts.CapabilityCard{
			{ID: "tool.crm.lookup", Type: "tool"},
			{ID: "skill.plan", Type: "skill"},
		},
	}
	got := ApplyToolUseStrategy(candidates, contracts.ToolUseStrategy{ToolChoiceMode: "no_tools"})
	if len(got.Tools) != 0 {
		t.Fatalf("expected tools to be removed, got %#v", got.Tools)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0].Type != "skill" {
		t.Fatalf("expected non-tool capabilities to remain, got %#v", got.Capabilities)
	}
}

func TestApplyToolUseStrategyPreferredTools(t *testing.T) {
	candidates := CandidateSet{
		Tools: []contracts.ToolCard{
			{ToolID: "mail.send", Name: "Mail"},
			{ToolID: "crm.lookup", Name: "CRM"},
		},
	}
	got := ApplyToolUseStrategy(candidates, contracts.ToolUseStrategy{
		PreferredToolIDs: []string{"crm.lookup"},
		ToolChoiceMode:   "auto",
	})
	if len(got.Tools) != 2 || got.Tools[0].ToolID != "crm.lookup" {
		t.Fatalf("expected preferred tool first, got %#v", got.Tools)
	}
}

func TestApplyToolUseStrategyToolFirstPrioritizesToolCapabilities(t *testing.T) {
	candidates := CandidateSet{
		Capabilities: []contracts.CapabilityCard{
			{ID: "skill.plan", Type: "skill"},
			{ID: "tool.crm.lookup", Type: "tool"},
		},
	}
	got := ApplyToolUseStrategy(candidates, contracts.ToolUseStrategy{ToolChoiceMode: "tool_first"})
	if len(got.Capabilities) != 2 || got.Capabilities[0].Type != "tool" {
		t.Fatalf("expected tool capabilities first, got %#v", got.Capabilities)
	}
}

func TestApplySkillUseStrategyFiltersInstructionsAndCapabilities(t *testing.T) {
	candidates := CandidateSet{
		Skills: []contracts.SkillCard{
			{SkillID: "plan", Name: "Plan"},
			{SkillID: "research", Name: "Research"},
		},
		SkillInstructions: []contracts.SkillInstruction{
			{SkillID: "plan", Content: "plan"},
			{SkillID: "research", Content: "research"},
		},
		Capabilities: []contracts.CapabilityCard{
			{ID: "skill.plan", Type: "skill"},
			{ID: "skill.research", Type: "skill"},
			{ID: "tool.crm", Type: "tool"},
		},
	}
	got := ApplySkillUseStrategy(candidates, contracts.SkillUseStrategy{
		EnabledSkillIDs:   []string{"plan", "research"},
		MaxSelectedSkills: 1,
	})
	if len(got.Skills) != 1 || got.Skills[0].SkillID != "plan" {
		t.Fatalf("expected one selected skill, got %#v", got.Skills)
	}
	if len(got.SkillInstructions) != 1 || got.SkillInstructions[0].SkillID != "plan" {
		t.Fatalf("expected matching skill instruction, got %#v", got.SkillInstructions)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[0].ID != "skill.plan" || got.Capabilities[1].Type != "tool" {
		t.Fatalf("expected selected skill capability and non-skill capability, got %#v", got.Capabilities)
	}
}

func TestApplySkillUseStrategyFiltersDisabledSkills(t *testing.T) {
	candidates := CandidateSet{
		Skills: []contracts.SkillCard{
			{SkillID: "plan", Name: "Plan"},
			{SkillID: "research", Name: "Research"},
		},
		SkillInstructions: []contracts.SkillInstruction{
			{SkillID: "plan", Content: "plan"},
			{SkillID: "research", Content: "research"},
		},
		Capabilities: []contracts.CapabilityCard{
			{ID: "plan", Type: "skill"},
			{ID: "research", Type: "skill"},
			{ID: "tool.crm", Type: "tool"},
		},
	}
	got := ApplySkillUseStrategy(candidates, contracts.SkillUseStrategy{DisabledSkillIDs: []string{"research"}})
	if len(got.Skills) != 1 || got.Skills[0].SkillID != "plan" {
		t.Fatalf("expected disabled skill removed, got %#v", got.Skills)
	}
	if len(got.SkillInstructions) != 1 || got.SkillInstructions[0].SkillID != "plan" {
		t.Fatalf("expected disabled skill instruction removed, got %#v", got.SkillInstructions)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[0].ID != "plan" || got.Capabilities[1].Type != "tool" {
		t.Fatalf("expected disabled skill capability removed, got %#v", got.Capabilities)
	}
}

func TestApplyKnowledgeUseStrategyDisabledRemovesKnowledgeTools(t *testing.T) {
	enabled := false
	candidates := CandidateSet{
		Tools: []contracts.ToolCard{
			{ToolID: "origin.knowledge.search"},
			{ToolID: "crm.lookup"},
		},
		Capabilities: []contracts.CapabilityCard{
			{ID: "origin.knowledge.search", Type: "tool"},
			{ID: "tool.crm.lookup", Type: "tool"},
		},
	}
	got := ApplyKnowledgeUseStrategy(candidates, contracts.KnowledgeUseStrategy{Enabled: &enabled})
	if len(got.Tools) != 1 || got.Tools[0].ToolID != "crm.lookup" {
		t.Fatalf("expected knowledge tool removed, got %#v", got.Tools)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0].ID != "tool.crm.lookup" {
		t.Fatalf("expected knowledge capability removed, got %#v", got.Capabilities)
	}
}
