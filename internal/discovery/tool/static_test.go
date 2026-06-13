package tool

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
)

func TestStaticCandidateProviderFiltersAndRanks(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Skills = []contracts.SkillDefinitionRef{{SkillID: "clean-core.artifact-report", Version: "v1"}}
	agent.Tools.AllowedToolIDs = []string{"artifact.create"}

	provider := StaticCandidateProvider{
		Capabilities: DefaultCapabilities(),
		Skills:       DefaultSkills(),
		Cards:        DefaultCards(),
	}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "create an artifact report summary")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Skills) != 1 || set.Skills[0].SkillID != "clean-core.artifact-report" {
		t.Fatalf("unexpected skills: %#v", set.Skills)
	}
	if len(set.Tools) != 1 || set.Tools[0].ToolID != "artifact.create" {
		t.Fatalf("unexpected tools: %#v", set.Tools)
	}
	if len(set.Capabilities) == 0 {
		t.Fatal("expected capabilities to be included")
	}
}

func TestStaticCandidateProviderHonorsDeniedTools(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"echo", "artifact.create"}
	agent.Tools.DeniedToolIDs = []string{"echo"}

	provider := StaticCandidateProvider{Cards: DefaultCards()}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "debugging")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range set.Tools {
		if tool.ToolID == "echo" {
			t.Fatalf("denied tool returned: %#v", set.Tools)
		}
	}
}

func TestStaticCandidateProviderHonorsToolGroups(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolGroupIDs = []string{"crm"}
	agent.Tools.DeniedToolIDs = []string{"crm.delete"}

	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "crm.lookup", GroupID: "crm", Name: "CRM lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "crm.delete", GroupID: "crm", Name: "CRM delete", Description: "Delete customers", RiskLevel: contracts.RiskHigh, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "billing.lookup", GroupID: "billing", Name: "Billing lookup", Description: "Look up invoices", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "lookup customer")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 || set.Tools[0].ToolID != "crm.lookup" {
		t.Fatalf("expected only crm.lookup from allowed group, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderHonorsPolicyDeniedToolGroups(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolGroupIDs = []string{"crm"}
	policy := contracts.PolicySet{ToolPolicy: contracts.ToolPolicy{DeniedToolGroupIDs: []string{"crm"}}}
	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "crm.lookup", GroupID: "crm", Name: "CRM lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, policy, "lookup customer")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 0 {
		t.Fatalf("expected policy denied group to remove tools, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderBoostsAllowedToolGroups(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools.AllowedToolIDs = []string{"aaa.web"}
	agent.Tools.AllowedToolGroupIDs = []string{"crm"}

	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "aaa.web", GroupID: "web", Name: "Customer lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "zzz.crm", GroupID: "crm", Name: "Customer lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "lookup customer")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 2 || set.Tools[0].ToolID != "zzz.crm" {
		t.Fatalf("expected allowed group tool to rank first, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderBoostsObjectiveMatchedToolGroup(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools = contracts.AgentToolsConfig{}

	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "aaa.web", GroupID: "web", Name: "Customer lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "zzz.crm", GroupID: "crm", Name: "Customer lookup", Description: "Look up customers", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "crm lookup customer")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 2 || set.Tools[0].ToolID != "zzz.crm" {
		t.Fatalf("expected objective-matched group tool to rank first, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderIncludesAgentEmbeddedSkillInstructions(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Skills = []contracts.SkillDefinitionRef{{SkillID: "pkg.report", Version: "v1"}}
	agent.SkillDefinitions = []contracts.SkillDefinition{{
		Card: contracts.SkillCard{
			SkillID:     "pkg.report",
			Version:     "v1",
			Name:        "Package report",
			Description: "Write package reports",
			WhenToUse:   []string{"report"},
		},
		Instruction: contracts.SkillInstruction{
			SkillID: "pkg.report",
			Content: "Use the package-specific report format.",
		},
	}}
	provider := StaticCandidateProvider{Cards: DefaultCards()}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "write report")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Skills) != 1 || set.Skills[0].SkillID != "pkg.report" {
		t.Fatalf("expected embedded skill card, got %#v", set.Skills)
	}
	if len(set.SkillInstructions) != 1 || set.SkillInstructions[0].Content == "" {
		t.Fatalf("expected embedded skill instruction, got %#v", set.SkillInstructions)
	}
}

func TestStaticCandidateProviderUsesSkillToolGuidance(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools = contracts.AgentToolsConfig{}
	agent.Skills = []contracts.SkillDefinitionRef{{SkillID: "pkg.customer", Version: "v1"}}
	agent.SkillDefinitions = []contracts.SkillDefinition{{
		Card: contracts.SkillCard{
			SkillID:     "pkg.customer",
			Version:     "v1",
			Name:        "Customer summary",
			Description: "Summarize customer records",
			WhenToUse:   []string{"customer summary"},
		},
		Instruction:      contracts.SkillInstruction{SkillID: "pkg.customer", Content: "Use CRM context."},
		AllowedTools:     []string{"crm.lookup"},
		RecommendedTools: []string{"crm.lookup"},
	}}
	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "crm.lookup", Name: "CRM lookup", Description: "Lookup customer records", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "web.search", Name: "Web search", Description: "Search customer records on the web", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "summarize customer records")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 || set.Tools[0].ToolID != "crm.lookup" {
		t.Fatalf("expected skill allowed tool to guide candidates, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderAppliesSkillStrategyBeforeToolGuidance(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Tools = contracts.AgentToolsConfig{}
	agent.Skills = []contracts.SkillDefinitionRef{{SkillID: "pkg.customer", Version: "v1"}}
	agent.Strategies.Skills.DisabledSkillIDs = []string{"pkg.customer"}
	agent.SkillDefinitions = []contracts.SkillDefinition{{
		Card: contracts.SkillCard{
			SkillID:     "pkg.customer",
			Version:     "v1",
			Name:        "Customer summary",
			Description: "Summarize customer records",
			WhenToUse:   []string{"customer summary"},
		},
		Instruction:  contracts.SkillInstruction{SkillID: "pkg.customer", Content: "Use CRM context."},
		AllowedTools: []string{"crm.lookup"},
	}}
	provider := StaticCandidateProvider{Cards: []contracts.ToolCard{
		{ToolID: "crm.lookup", Name: "CRM lookup", Description: "Lookup customer records", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
		{ToolID: "web.search", Name: "Web search", Description: "Search customer records on the web", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolProtected, Version: "v1"},
	}}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "search customer records on the web")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Skills) != 0 || len(set.SkillInstructions) != 0 {
		t.Fatalf("expected disabled skill to be removed before instructions, got skills=%#v instructions=%#v", set.Skills, set.SkillInstructions)
	}
	foundWeb := false
	for _, tool := range set.Tools {
		if tool.ToolID == "web.search" {
			foundWeb = true
		}
	}
	if !foundWeb {
		t.Fatalf("expected disabled skill allowed-tools guidance not to filter web search, got %#v", set.Tools)
	}
}

func TestStaticCandidateProviderMatchesSkillInstructionVersion(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Skills = []contracts.SkillDefinitionRef{{SkillID: "pkg.report", Version: "v1"}}
	agent.SkillDefinitions = []contracts.SkillDefinition{
		{
			Card: contracts.SkillCard{
				SkillID:     "pkg.report",
				Version:     "v1",
				Name:        "Package report v1",
				Description: "Write package reports",
				WhenToUse:   []string{"report"},
			},
			Instruction: contracts.SkillInstruction{SkillID: "pkg.report", Content: "Use v1 format."},
		},
		{
			Card: contracts.SkillCard{
				SkillID:     "pkg.report",
				Version:     "v2",
				Name:        "Package report v2",
				Description: "Write package reports",
				WhenToUse:   []string{"report"},
			},
			Instruction: contracts.SkillInstruction{SkillID: "pkg.report", Content: "Use v2 format."},
		},
	}
	provider := StaticCandidateProvider{Cards: DefaultCards()}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "write report")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.SkillInstructions) != 1 || set.SkillInstructions[0].Content != "Use v1 format." {
		t.Fatalf("expected v1 instruction, got %#v", set.SkillInstructions)
	}
}

func TestStaticCandidateProviderReturnsMatchingCollaborators(t *testing.T) {
	agent := loader.TestAgentDefinition()
	agent.Collaborators = []contracts.AgentCollaboratorRef{
		{
			AgentID:      "crm-agent",
			Name:         "CRM Agent",
			Description:  "Handles customer history.",
			WhenToUse:    []string{"customer lookup"},
			Capabilities: []string{"crm"},
		},
		{
			AgentID:     "risk-agent",
			Name:        "Risk Agent",
			Description: "Reviews financial risk.",
			Status:      "disabled",
		},
	}
	provider := StaticCandidateProvider{Cards: DefaultCards()}
	set, err := provider.Candidates(context.Background(), agent, contracts.PolicySet{}, "lookup customer history")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Collaborators) != 1 || set.Collaborators[0].AgentID != "crm-agent" {
		t.Fatalf("expected crm collaborator, got %#v", set.Collaborators)
	}
}
