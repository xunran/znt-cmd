package promptpolicy

import (
	"strings"

	contextcompressor "znt/internal/context/compressor"
	"znt/internal/contracts"
)

func ApplySafetyPolicy(policy contracts.PromptPolicy, bundle contracts.PromptBundle) error {
	for _, phrase := range policy.BlockedPhrases {
		phrase = strings.TrimSpace(phrase)
		if phrase == "" {
			continue
		}
		if strings.Contains(strings.ToLower(Text(bundle)), strings.ToLower(phrase)) {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "prompt policy blocked phrase", map[string]any{"phrase": phrase})
		}
	}
	return nil
}

func ApplyLimitPolicy(policy contracts.PolicySet, definition contracts.AgentDefinition, bundle contracts.PromptBundle) error {
	limit := TokenLimit(policy, definition)
	if limit > 0 && contextcompressor.EstimatePromptTokens(bundle) > limit {
		return contracts.NewRuntimeError(contracts.CodeModelError, "max prompt tokens exceeded", map[string]any{"max_prompt_tokens": limit})
	}
	return nil
}

func TokenLimit(policy contracts.PolicySet, definition contracts.AgentDefinition) int {
	limit := definition.Runtime.MaxPromptTokens
	if policy.PromptPolicy.MaxPromptTokens > 0 && (limit == 0 || policy.PromptPolicy.MaxPromptTokens < limit) {
		limit = policy.PromptPolicy.MaxPromptTokens
	}
	return limit
}

func Text(bundle contracts.PromptBundle) string {
	return strings.Join([]string{bundle.System, bundle.Developer, bundle.Task, bundle.Context}, "\n")
}
