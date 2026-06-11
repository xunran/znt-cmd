package promptbundle

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
)

func BenchmarkPromptBundleBuild(b *testing.B) {
	ctx := context.Background()
	agent := loader.TestAgentDefinition()
	view := contracts.WorkView{
		RunID:     "run_bench",
		UserInput: "summarize the customer escalation, identify risks, and propose next actions",
		TaskSummary: contracts.TaskSummary{
			TaskID:    "task_bench",
			Status:    contracts.TaskRunning,
			Title:     "customer escalation",
			Objective: "prepare a grounded escalation summary",
		},
		CandidateCapabilities: []contracts.CapabilityCard{
			{ID: "artifact.output", Type: "artifact", Name: "Artifact output", Description: "Produce artifact refs"},
			{ID: "handoff.delegate", Type: "handoff", Name: "Delegate", Description: "Delegate work to another agent"},
		},
		CandidateSkills: []contracts.SkillCard{
			{SkillID: "skill_report", Name: "Report skill", Description: "Create concise reports", WhenToUse: []string{"summaries", "status reports"}},
			{SkillID: "skill_risk", Name: "Risk review", Description: "Identify operational risks", WhenToUse: []string{"escalations"}},
		},
		CandidateSkillInstructions: []contracts.SkillInstruction{
			{SkillID: "skill_report", Content: "Use short sections with evidence-backed claims.", OutputRequirements: []string{"include summary", "include next actions"}},
			{SkillID: "skill_risk", Content: "Call out uncertainty explicitly.", Constraints: []string{"do not invent facts"}},
		},
		CandidateTools: []contracts.ToolCard{
			{ToolID: "echo", Name: "echo", Description: "Returns input arguments", RiskLevel: contracts.RiskLow, Visibility: contracts.ToolExposed, Version: "v1"},
			{ToolID: "artifact.create", Name: "artifact.create", Description: "Creates a text artifact", RiskLevel: contracts.RiskMedium, Visibility: contracts.ToolProtected, Version: "v1"},
		},
		CandidateCollaborators: []contracts.CollaboratorCard{
			{AgentID: "crm-agent", Version: "v1", Name: "CRM Agent", Description: "Handles customer history", Capabilities: []string{"crm", "account history"}, WhenToUse: []string{"customer context"}},
		},
		RiskMarks: []contracts.RiskMark{
			{Level: contracts.RiskMedium, Reason: "customer escalation requires careful attribution"},
			{Level: contracts.RiskHigh, Reason: "artifact.create writes durable output"},
		},
		MemorySummaries: []contracts.MemorySummary{
			{MemoryID: "memory_1", Summary: "Customer prefers concise weekly updates."},
			{MemoryID: "memory_2", Summary: "Prior escalation involved billing reconciliation."},
		},
		ToolResultSummaries: []contracts.ToolResultSummary{
			{ToolCallID: "toolcall_1", Status: contracts.ToolResultSucceeded, Summary: "retrieved account status"},
		},
	}
	builder := NewBuilder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bundle, err := builder.Build(ctx, agent, view)
		if err != nil {
			b.Fatal(err)
		}
		if bundle.Hash == "" {
			b.Fatal("expected prompt bundle hash")
		}
	}
}
