package outputpolicy

import (
	"strings"
	"testing"

	"znt/internal/contracts"
	decisionvalidator "znt/internal/decision/validator"
)

func TestParseDecisionStrictJSONRejectsUnknownFields(t *testing.T) {
	_, err := ParseDecision([]byte(`{"type":"reply","extra":"nope","reply":{"kind":"answer","text":"ok"}}`), contracts.OutputStrategy{StrictJSON: true})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict json to reject unknown field, got %v", err)
	}
}

func TestValidateDecisionStrictJSONRejectsNormalizationWarnings(t *testing.T) {
	result, err := decisionvalidator.New().Normalize(contracts.Decision{
		Type:  contracts.DecisionTypeReply,
		Reply: &contracts.DecisionReply{Text: "ok"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected validator warning for defaulted reply kind, got %#v", result)
	}
	err = ValidateDecision(contracts.OutputStrategy{StrictJSON: true}, result)
	if err == nil || !strings.Contains(err.Error(), "strict_json") {
		t.Fatalf("expected strict json output policy to reject normalization, got %v", err)
	}
}

func TestApplyPromptBundleAddsOutputStrategyConstraints(t *testing.T) {
	bundle := ApplyPromptBundle(contracts.PromptBundle{}, contracts.OutputStrategy{
		OutputMode: "decision_json",
		StrictJSON: true,
	})
	if len(bundle.Constraints) != 2 || !strings.Contains(strings.Join(bundle.Constraints, "\n"), "strict_json") {
		t.Fatalf("expected output strategy constraints, got %#v", bundle.Constraints)
	}
}
