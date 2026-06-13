package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	modelclient "znt/internal/model/client"
)

func TestModelJudgeParsesAddressingAssessment(t *testing.T) {
	model := &capturingJudgeModel{responses: []string{`{"addressed_to_agent":true,"confidence":0.92,"reason":"延续上一轮","signals":["previous_speaker_is_agent"],"addressee_ids":["test-agent"],"suggested_action":"enter_main_agent"}`}}
	result, err := (ModelJudge{Model: model, Timeout: 1500 * time.Millisecond}).JudgeAddressing(context.Background(), contracts.ConversationContext{
		Kind: KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "那你继续",
		},
	}, loader.TestAgentDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if !result.AddressedToAgent || result.DecisionSource != "llm" || result.SuggestedAction != ActionEnterMainAgent {
		t.Fatalf("unexpected addressing result: %#v", result)
	}
	if len(model.requests) != 1 || !strings.Contains(model.requests[0].OutputContract, "addressed_to_agent") {
		t.Fatalf("expected addressing output contract, got %#v", model.requests)
	}
	if model.requests[0].Timeout != 1500*time.Millisecond {
		t.Fatalf("expected addressing timeout to be forwarded, got %s", model.requests[0].Timeout)
	}
}

func TestModelJudgeParsesSufficiencyAssessment(t *testing.T) {
	model := &capturingJudgeModel{responses: []string{`{"phase":"pre_addressing","sufficient":false,"confidence":0.81,"reason":"缺少第二个问题定义","missing_facts":["第二个问题定义"],"retrieval_needed":true,"queries":[{"query":"第二个问题","max_results":0}],"suggested_action":"retrieve_context"}`}}
	result, err := (ModelJudge{Model: model}).JudgeSufficiency(context.Background(), contracts.ConversationContext{
		Kind: KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题呢？",
		},
	}, PhasePreAddressing)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sufficient || !result.RetrievalNeeded || result.SuggestedAction != ActionRetrieve || len(result.Queries) != 1 {
		t.Fatalf("unexpected sufficiency result: %#v", result)
	}
	if len(model.requests) != 1 || !strings.Contains(model.requests[0].OutputContract, "retrieval_needed") {
		t.Fatalf("expected sufficiency output contract, got %#v", model.requests)
	}
	if strings.Contains(model.requests[0].OutputContract, `"max_results":8`) || !strings.Contains(model.requests[0].OutputContract, `"max_results":0`) {
		t.Fatalf("expected sufficiency output contract to avoid hard-coded retrieval window, got %s", model.requests[0].OutputContract)
	}
}

func TestHybridJudgeKeepsHighConfidenceRule(t *testing.T) {
	model := &capturingJudgeModel{responses: []string{`{"addressed_to_agent":true,"confidence":0.99,"reason":"model should not be used","suggested_action":"enter_main_agent"}`}}
	result, err := (HybridJudge{Model: ModelJudge{Model: model}}).JudgeAddressing(context.Background(), contracts.ConversationContext{
		Kind: KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:        "user_1",
			SpeakerType:      "user",
			Text:             "那你继续",
			ReplyToMessageID: "msg_1",
		},
		RecentMessages: []contracts.ConversationMessage{{
			MessageID:   "msg_1",
			SpeakerID:   "test-agent",
			SpeakerType: "agent",
			Text:        "我可以继续。",
		}},
	}, loader.TestAgentDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if !result.AddressedToAgent || result.DecisionSource != "rule" {
		t.Fatalf("expected rule result, got %#v", result)
	}
	if len(model.requests) != 0 {
		t.Fatalf("expected model not to be called, got %d calls", len(model.requests))
	}
}

type capturingJudgeModel struct {
	responses []string
	requests  []modelclient.ModelRequest
}

func (m *capturingJudgeModel) Complete(_ context.Context, request modelclient.ModelRequest) (modelclient.ModelResponse, error) {
	m.requests = append(m.requests, request)
	response := `{}`
	if len(m.responses) >= len(m.requests) {
		response = m.responses[len(m.requests)-1]
	}
	return modelclient.ModelResponse{RawDecisionJSON: []byte(response), ModelProvider: "stub", ModelName: "judge-stub"}, nil
}

func (m *capturingJudgeModel) Stream(ctx context.Context, request modelclient.ModelRequest) (<-chan modelclient.ModelStreamEvent, error) {
	response, err := m.Complete(ctx, request)
	ch := make(chan modelclient.ModelStreamEvent, 1)
	go func() {
		defer close(ch)
		if err != nil {
			ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamError, Err: err}
			return
		}
		ch <- modelclient.ModelStreamEvent{Type: modelclient.ModelStreamCompleted, RawDecision: response.RawDecisionJSON}
	}()
	return ch, nil
}
