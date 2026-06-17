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
		rendered := contextBlockPromptOpenTag(block) + "\n" + title + "\n" + block.Content + "\n</external_context>"
		bundle.Context = strings.TrimSpace(bundle.Context + "\n" + rendered)
		bundle.AssemblySteps = append(bundle.AssemblySteps, contracts.PromptAssemblyStep{
			StepID:         firstNonEmpty(block.ID, block.Title, "runtime-hook-context"),
			Title:          "加入运行时 Hook 上下文",
			SourceType:     "runtime_hook_context",
			SourceLabel:    "运行时 Hook",
			EditTarget:     "Runtime Hook 配置",
			MessageRole:    "user",
			PromptSection:  "context",
			Reason:         "Hook 在模型调用前追加上下文",
			ContentPreview: previewText(rendered, 320),
			TokensEstimate: estimateTokens(rendered),
			Included:       true,
		})
	}
	for _, hint := range patch.PlannerHints {
		if strings.TrimSpace(hint.Content) != "" {
			bundle.Constraints = append(bundle.Constraints, "planner hint: "+hint.Content)
			rendered := "planner hint: " + hint.Content
			bundle.AssemblySteps = append(bundle.AssemblySteps, contracts.PromptAssemblyStep{
				StepID:         firstNonEmpty(hint.Key, "planner-hint"),
				Title:          "加入运行时规划提示",
				SourceType:     "runtime_hook_planner_hint",
				SourceLabel:    "运行时 Hook",
				EditTarget:     "Runtime Hook 配置",
				MessageRole:    "system",
				PromptSection:  "constraints",
				Reason:         "Hook 在模型调用前追加规划提示",
				ContentPreview: previewText(rendered, 320),
				TokensEstimate: estimateTokens(rendered),
				Included:       true,
			})
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
