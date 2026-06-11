package validator

import (
	"testing"

	"znt/internal/contracts"
)

func TestValidatorReply(t *testing.T) {
	v := New()
	err := v.Validate(contracts.Decision{
		Type: contracts.DecisionTypeReply,
		Reply: &contracts.DecisionReply{
			Kind: contracts.ReplyAnswer,
			Text: "hello",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidatorRejectsMissingToolCandidate(t *testing.T) {
	v := New()
	err := v.Validate(contracts.Decision{
		Type: contracts.DecisionTypeToolCall,
		ToolCalls: []contracts.ToolCall{{
			ToolID: "delete",
			Name:   "delete",
		}},
	}, []contracts.ToolCard{{ToolID: "echo", Name: "echo"}})
	if err == nil {
		t.Fatal("expected missing candidate tool to fail")
	}
}

func TestValidatorNormalizesNameOnlyToolCall(t *testing.T) {
	v := New()
	result, err := v.Normalize(contracts.Decision{
		Type:      contracts.DecisionTypeToolCall,
		ToolCalls: []contracts.ToolCall{{Name: "echo"}},
	}, []contracts.ToolCard{{ToolID: "echo", Name: "echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.ToolCalls[0].ToolID != "echo" || len(result.Warnings) == 0 {
		t.Fatalf("expected normalized tool call, got %#v", result)
	}
}

func TestValidatorNormalizesToolNameUsedAsToolID(t *testing.T) {
	v := New()
	result, err := v.Normalize(contracts.Decision{
		Type: contracts.DecisionTypeToolCall,
		ToolCalls: []contracts.ToolCall{{
			ToolID: "ask_tiqianguan_backend_tool",
		}},
	}, []contracts.ToolCard{{ToolID: "znt-guan.ask_tiqianguan_backend_tool", Name: "ask_tiqianguan_backend_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	call := result.Decision.ToolCalls[0]
	if call.ToolID != "znt-guan.ask_tiqianguan_backend_tool" || call.Name != "ask_tiqianguan_backend_tool" || len(result.Warnings) == 0 {
		t.Fatalf("expected short tool name to normalize to provider-qualified tool id, got %#v", result)
	}
}

func TestValidatorRejectsInvalidConfidence(t *testing.T) {
	v := New()
	err := v.Validate(contracts.Decision{
		Type:       contracts.DecisionTypeReply,
		Confidence: 1.5,
		Reply:      &contracts.DecisionReply{Kind: contracts.ReplyAnswer, Text: "hello"},
	}, nil)
	if err == nil {
		t.Fatal("expected invalid confidence to fail")
	}
}
