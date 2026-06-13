package promptbundle

import (
	"fmt"
	"strings"

	contextcollector "znt/internal/context/collector"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
)

func ApplyRuntimeHookPatch(bundle contracts.PromptBundle, patch runtimehook.Patch) (contracts.PromptBundle, error) {
	for _, block := range patch.AddContextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		title := block.Title
		if title == "" {
			title = block.ID
		}
		if title == "" {
			title = "runtime hook context"
		}
		bundle.Context = strings.TrimSpace(bundle.Context + "\n" + contextBlockPromptOpenTag(block) + "\n" + title + "\n" + block.Content + "\n</external_context>")
	}
	for _, hint := range patch.PlannerHints {
		if strings.TrimSpace(hint.Content) != "" {
			bundle.Constraints = append(bundle.Constraints, "planner hint: "+hint.Content)
		}
	}
	if err := RefreshHash(&bundle); err != nil {
		return contracts.PromptBundle{}, err
	}
	return bundle, nil
}

func contextBlockPromptOpenTag(block runtimehook.ContextBlock) string {
	attrs := []string{
		fmt.Sprintf("source_type=%q", contextcollector.ContextBlockSourceType(block)),
		fmt.Sprintf("source_ref=%q", contextcollector.ContextBlockSourceRef(block)),
		fmt.Sprintf("trust_level=%q", contextcollector.ContextBlockTrustLevel(block)),
	}
	if providerID := contextcollector.ContextBlockMetadataString(block, "provider_id"); providerID != "" {
		attrs = append(attrs, fmt.Sprintf("provider_id=%q", providerID))
	}
	if hookID := contextcollector.ContextBlockMetadataString(block, "hook_id"); hookID != "" {
		attrs = append(attrs, fmt.Sprintf("hook_id=%q", hookID))
	}
	if toolCallID := contextcollector.ContextBlockMetadataString(block, "tool_call_id"); toolCallID != "" {
		attrs = append(attrs, fmt.Sprintf("tool_call_id=%q", toolCallID))
	}
	return "<external_context " + strings.Join(attrs, " ") + ">"
}
