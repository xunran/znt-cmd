package workview

import (
	"context"
	"testing"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
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

func hasWorkViewConstraint(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
