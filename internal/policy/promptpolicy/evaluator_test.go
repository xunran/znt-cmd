package promptpolicy

import (
	"strings"
	"testing"

	"znt/internal/contracts"
)

func TestApplySafetyPolicyBlocksConfiguredPhrase(t *testing.T) {
	err := ApplySafetyPolicy(contracts.PromptPolicy{
		BlockedPhrases: []string{"blocked phrase"},
	}, contracts.PromptBundle{
		System:  "system",
		Task:    "task",
		Context: "user said BLOCKED PHRASE",
	})
	if err == nil {
		t.Fatal("expected blocked phrase error")
	}
}

func TestTokenLimitUsesStricterPromptLimit(t *testing.T) {
	definition := contracts.AgentDefinition{}
	definition.Runtime.MaxPromptTokens = 100
	policy := contracts.PolicySet{PromptPolicy: contracts.PromptPolicy{MaxPromptTokens: 40}}
	if limit := TokenLimit(policy, definition); limit != 40 {
		t.Fatalf("expected stricter policy prompt limit, got %d", limit)
	}

	policy.PromptPolicy.MaxPromptTokens = 200
	if limit := TokenLimit(policy, definition); limit != 100 {
		t.Fatalf("expected stricter agent runtime prompt limit, got %d", limit)
	}
}

func TestApplyLimitPolicyRejectsOversizedPrompt(t *testing.T) {
	err := ApplyLimitPolicy(contracts.PolicySet{
		PromptPolicy: contracts.PromptPolicy{MaxPromptTokens: 4},
	}, contracts.AgentDefinition{}, contracts.PromptBundle{
		System:  "system",
		Task:    "task",
		Context: strings.Repeat("context ", 8),
	})
	if err == nil {
		t.Fatal("expected max prompt token error")
	}
}
