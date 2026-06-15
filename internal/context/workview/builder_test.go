package workview

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	contextcollector "znt/internal/context/collector"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
)

func TestBuilderAddsRiskMarksForHighRiskCandidates(t *testing.T) {
	agent := loader.TestAgentDefinition()
	view, err := NewBuilder().Build(context.Background(), BuildInput{
		Run:   contracts.AgentRun{RunID: "run_1"},
		Task:  contracts.Task{TaskID: "task_1", Status: contracts.TaskRunning},
		Agent: agent,
		Tools: []contracts.ToolCard{{
			ToolID:    "danger.write",
			RiskLevel: contracts.RiskHigh,
		}},
		Skills: []contracts.SkillCard{{
			SkillID:   "pkg.admin",
			RiskLevel: contracts.RiskCritical,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.RiskMarks) != 2 {
		t.Fatalf("expected candidate risk marks, got %#v", view.RiskMarks)
	}
	if !hasWorkViewConstraint(view.Constraints, "candidate risk marks must be considered before tool or handoff decisions") {
		t.Fatalf("expected risk constraint, got %#v", view.Constraints)
	}
}

func TestBuilderCarriesConversationContext(t *testing.T) {
	agent := loader.TestAgentDefinition()
	conversation := &contracts.ConversationContext{
		Kind: "group",
		CurrentMessage: contracts.ConversationMessage{
			MessageID:   "msg_2",
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题呢？",
		},
		Addressing: &contracts.AddressingAssessment{
			AddressedToAgent: true,
			Confidence:       0.86,
			Reason:           "reply_to_agent_message",
			SuggestedAction:  "enter_main_agent",
		},
	}
	view, err := NewBuilder().Build(context.Background(), BuildInput{
		Run:          contracts.AgentRun{RunID: "run_1"},
		Task:         contracts.Task{TaskID: "task_1", Status: contracts.TaskRunning},
		Agent:        agent,
		UserInput:    "第二个问题呢？",
		Conversation: conversation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.ConversationContext == nil || view.ConversationContext.CurrentMessage.MessageID != "msg_2" {
		t.Fatalf("expected conversation context in work view, got %#v", view.ConversationContext)
	}
}

func TestApplyRuntimeHookPatchDropsSourcesAndAddsExternalContext(t *testing.T) {
	view := contracts.WorkView{
		ConversationContext: &contracts.ConversationContext{},
		MemorySummaries:     []contracts.MemorySummary{{MemoryID: "memory_1"}},
		ArtifactRefs:        []contracts.ArtifactRef{{ArtifactID: "artifact_1"}},
		CandidateTools:      []contracts.ToolCard{{ToolID: "tool_1"}},
		Constraints:         []string{"base"},
	}

	ApplyRuntimeHookPatch(&view, runtimehook.Patch{
		DropContextRefs: []string{"memory", "tools"},
		AddContextBlocks: []runtimehook.ContextBlock{{
			ID:      "crm_ctx",
			Title:   "CRM context",
			Content: "renewal is active",
			Metadata: map[string]any{
				"source_type": contextcollector.SourceAgentPluginContext,
				"source_ref":  "crm://account/42",
				"provider_id": "crm-plugin",
				"hook_id":     "crm-context",
			},
		}},
		PlannerHints: []runtimehook.PlannerHint{{Content: "prefer concise answer"}},
	})

	if len(view.MemorySummaries) != 0 || len(view.CandidateTools) != 0 {
		t.Fatalf("expected dropped memory and tools, got memory=%#v tools=%#v", view.MemorySummaries, view.CandidateTools)
	}
	if view.ConversationContext == nil {
		t.Fatal("did not expect conversation to be dropped")
	}
	if len(view.ArtifactRefs) != 2 {
		t.Fatalf("expected original plus hook artifact refs, got %#v", view.ArtifactRefs)
	}
	added := view.ArtifactRefs[1]
	if added.ArtifactID != "crm_ctx" || added.Type != contextcollector.SourceAgentPluginContext || added.Summary != "CRM context: renewal is active" {
		t.Fatalf("expected normalized hook artifact ref, got %#v", added)
	}
	if added.Metadata["source_ref"] != "crm://account/42" || added.Metadata["provider_id"] != "crm-plugin" || added.Metadata["hook_id"] != "crm-context" || added.Metadata["trust_level"] != "untrusted_external_context" {
		t.Fatalf("expected hook metadata, got %#v", added.Metadata)
	}
	if !hasWorkViewConstraint(view.Constraints, "planner hint: prefer concise answer") {
		t.Fatalf("expected planner hint constraint, got %#v", view.Constraints)
	}
}

func hasWorkViewConstraint(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
