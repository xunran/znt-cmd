package agentpackage

import (
	"fmt"
	"strings"
	"time"

	"znt/internal/contracts"
)

type CompileError struct {
	Path    string
	Message string
}

func (e CompileError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

func Compile(agentID contracts.AgentID, version contracts.AgentVersion, source AgentPackageSource) (contracts.AgentDefinition, error) {
	sourceKind, err := normalizeSourceKind(source.SourceKind)
	if err != nil {
		return contracts.AgentDefinition{}, err
	}
	if agentID == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "agent.yaml", Message: "agent_id is required"}
	}
	if version == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "agent.yaml", Message: "version is required"}
	}
	if sourceKind == contracts.AgentSourceKindPlugin && strings.TrimSpace(source.ProviderID) == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "plugin.provider_id", Message: "provider_id is required for plugin_service source"}
	}
	if err := ValidateSourceMetadata(source.Metadata); err != nil {
		return contracts.AgentDefinition{}, err
	}
	if sourceKind == contracts.AgentSourceKindPlugin {
		if err := ValidatePluginSourceMetadata(source.Metadata); err != nil {
			return contracts.AgentDefinition{}, err
		}
	}
	if strings.TrimSpace(source.AgentsMD) == "" && strings.TrimSpace(source.Prompt) == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "AGENTS.md", Message: "agents_md or prompt is required"}
	}
	if err := validateToolBindings(source.ToolBindings); err != nil {
		return contracts.AgentDefinition{}, err
	}
	if err := validateCollaborators(source.Collaborators); err != nil {
		return contracts.AgentDefinition{}, err
	}
	if err := validateRuntimeHooks(source.RuntimeHooks); err != nil {
		return contracts.AgentDefinition{}, err
	}
	if err := validateAgentStrategies(source.Strategies); err != nil {
		return contracts.AgentDefinition{}, err
	}
	exports := source.Exports
	if err := validateExports(&exports); err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition := contracts.AgentDefinition{
		AgentID:          agentID,
		Version:          version,
		SourceKind:       sourceKind,
		SourceProviderID: strings.TrimSpace(source.ProviderID),
		ManifestVersion:  strings.TrimSpace(source.ManifestVersion),
		ManifestHash:     metadataString(source.Metadata, "manifest_hash", ""),
		Name:             metadataString(source.Metadata, "name", string(agentID)),
		Description:      metadataString(source.Metadata, "description", firstLine(source.AgentsMD, "Published agent package definition.")),
		IdentityPrompt:   strings.TrimSpace(source.Prompt),
		SystemPrompt:     "Return decisions as JSON.",
		DeveloperPrompt:  "",
		Tools:            source.ToolBindings,
		Skills:           append([]contracts.SkillDefinitionRef(nil), source.Skills...),
		Collaborators:    source.Collaborators,
		Exports:          exports,
		RuntimeHooks:     source.RuntimeHooks,
		PolicyRefs: contracts.AgentPolicyRefs{
			PolicySetID: contracts.PolicySetID(metadataString(source.Metadata, "policy_set_id", "policy_default")),
		},
		Strategies: source.Strategies,
		Runtime: contracts.RuntimeLimits{
			MaxSteps:                   4,
			MaxToolCalls:               2,
			MaxDuration:                60 * time.Second,
			MaxPromptTokens:            4000,
			MaxHandoffDepth:            0,
			MaxChildTasks:              0,
			MaxModelRetries:            1,
			MaxRepairAttempts:          1,
			MaxConsecutiveToolFailures: 2,
		},
		ContractVersion: "v1.0-alpha",
		CreatedAt:       time.Now().UTC(),
	}
	if definition.IdentityPrompt == "" {
		definition.IdentityPrompt = strings.TrimSpace(source.AgentsMD)
	}
	applyCompiledStrategies(&definition, source.Strategies)
	if err := validateRuntimeLimits(definition.Runtime); err != nil {
		return contracts.AgentDefinition{}, err
	}
	if err := validateSkillRefs(definition.Skills); err != nil {
		return contracts.AgentDefinition{}, err
	}
	skillDefinitions, err := normalizeSkillDefinitions(source.SkillDefinitions)
	if err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition.SkillDefinitions = skillDefinitions
	return definition, nil
}

func CompilePlugin(agentID contracts.AgentID, version contracts.AgentVersion, source AgentPluginSource) (contracts.AgentDefinition, error) {
	return Compile(agentID, version, PackageSourceFromPlugin(source))
}

func normalizeSourceKind(kind contracts.AgentSourceKind) (contracts.AgentSourceKind, error) {
	switch kind {
	case "", contracts.AgentSourceKindPackage:
		return contracts.AgentSourceKindPackage, nil
	case contracts.AgentSourceKindPlugin:
		return contracts.AgentSourceKindPlugin, nil
	default:
		return "", CompileError{Path: "source_kind", Message: fmt.Sprintf("unknown agent source kind %q", kind)}
	}
}

func ValidatePluginSourceMetadata(metadata map[string]any) error {
	for _, key := range []string{"service_connection_id", "connection_id", "base_url", "endpoint", "auth_ref", "token_ref", "secret_ref"} {
		if _, ok := metadata[key]; ok {
			return CompileError{
				Path:    "plugin.metadata." + key,
				Message: "connection facts for plugin_service sources must come from ToolProvider.service_connection_id",
			}
		}
	}
	return nil
}

func ValidateSourceMetadata(metadata map[string]any) error {
	for _, key := range []string{
		"identity_prompt",
		"system_prompt",
		"developer_prompt",
		"skills",
		"skill_definitions",
		"tool_bindings",
		"collaborators",
		"exports",
		"runtime_hooks",
		"strategies",
		"runtime",
		"max_steps",
		"max_tool_calls",
		"max_duration_seconds",
		"max_prompt_tokens",
		"max_handoff_depth",
		"max_child_tasks",
		"max_model_retries",
		"max_repair_attempts",
		"max_consecutive_tool_failures",
	} {
		if _, ok := metadata[key]; ok {
			return CompileError{
				Path:    "metadata." + key,
				Message: "agent behavior fields must use structured source fields or strategies",
			}
		}
	}
	return nil
}

func validateToolBindings(tools contracts.AgentToolsConfig) error {
	denied := map[string]struct{}{}
	for _, id := range tools.DeniedToolIDs {
		if strings.TrimSpace(id) == "" {
			return CompileError{Path: "tool-bindings.yaml", Message: "denied_tool_ids contains empty tool id"}
		}
		denied[id] = struct{}{}
	}
	for _, id := range tools.AllowedToolIDs {
		if strings.TrimSpace(id) == "" {
			return CompileError{Path: "tool-bindings.yaml", Message: "allowed_tool_ids contains empty tool id"}
		}
		if _, blocked := denied[id]; blocked {
			return CompileError{Path: "tool-bindings.yaml", Message: fmt.Sprintf("tool %q appears in both allowed_tool_ids and denied_tool_ids", id)}
		}
	}
	for _, id := range tools.ExposedToolIDs {
		if strings.TrimSpace(id) == "" {
			return CompileError{Path: "tool-bindings.yaml", Message: "exposed_tool_ids contains empty tool id"}
		}
		if _, blocked := denied[id]; blocked {
			return CompileError{Path: "tool-bindings.yaml", Message: fmt.Sprintf("tool %q appears in both exposed_tool_ids and denied_tool_ids", id)}
		}
	}
	deniedGroups := map[string]struct{}{}
	for _, id := range tools.DeniedToolGroupIDs {
		if strings.TrimSpace(id) == "" {
			return CompileError{Path: "tool-bindings.yaml", Message: "denied_tool_group_ids contains empty group id"}
		}
		deniedGroups[id] = struct{}{}
	}
	for _, id := range tools.AllowedToolGroupIDs {
		if strings.TrimSpace(id) == "" {
			return CompileError{Path: "tool-bindings.yaml", Message: "allowed_tool_group_ids contains empty group id"}
		}
		if _, blocked := deniedGroups[id]; blocked {
			return CompileError{Path: "tool-bindings.yaml", Message: fmt.Sprintf("tool group %q appears in both allowed_tool_group_ids and denied_tool_group_ids", id)}
		}
	}
	return nil
}

func validateCollaborators(collaborators []contracts.AgentCollaboratorRef) error {
	seen := map[contracts.AgentID]struct{}{}
	for _, collaborator := range collaborators {
		if collaborator.AgentID == "" {
			return CompileError{Path: "collaborators", Message: "agent_id is required"}
		}
		if _, ok := seen[collaborator.AgentID]; ok {
			return CompileError{Path: "collaborators", Message: fmt.Sprintf("duplicate collaborator %q", collaborator.AgentID)}
		}
		seen[collaborator.AgentID] = struct{}{}
		if collaborator.DefaultHandoffMode != "" {
			if err := collaborator.DefaultHandoffMode.Validate(); err != nil {
				return CompileError{Path: "collaborators." + string(collaborator.AgentID) + ".default_handoff_mode", Message: err.Error()}
			}
		}
		for _, mode := range collaborator.AllowedHandoffModes {
			if err := mode.Validate(); err != nil {
				return CompileError{Path: "collaborators." + string(collaborator.AgentID) + ".allowed_handoff_modes", Message: err.Error()}
			}
		}
		if collaborator.MaxContextTokens < 0 {
			return CompileError{Path: "collaborators." + string(collaborator.AgentID) + ".max_context_tokens", Message: "must be greater than or equal to 0"}
		}
		if collaborator.Status != "" && !validCollaboratorStatus(collaborator.Status) {
			return CompileError{Path: "collaborators." + string(collaborator.AgentID) + ".status", Message: fmt.Sprintf("unknown collaborator status %q", collaborator.Status)}
		}
	}
	return nil
}

func validCollaboratorStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "enabled", "active", "disabled", "deleted":
		return true
	default:
		return false
	}
}

func validateRuntimeHooks(runtimeHooks contracts.AgentRuntimeHooks) error {
	mode := strings.TrimSpace(runtimeHooks.Mode)
	if mode == "" && len(runtimeHooks.Hooks) == 0 {
		return nil
	}
	if mode != "" {
		switch mode {
		case "disabled", "data_hooks", "strategy_hooks":
		default:
			return CompileError{Path: "runtime_hooks.mode", Message: fmt.Sprintf("unknown runtime_hooks mode %q", mode)}
		}
	}
	seen := map[string]struct{}{}
	for _, hook := range runtimeHooks.Hooks {
		if strings.TrimSpace(hook.HookID) == "" {
			return CompileError{Path: "runtime_hooks.hooks", Message: "hook_id is required"}
		}
		if _, ok := seen[hook.HookID]; ok {
			return CompileError{Path: "runtime_hooks.hooks", Message: fmt.Sprintf("duplicate hook %q", hook.HookID)}
		}
		seen[hook.HookID] = struct{}{}
		if strings.TrimSpace(hook.Phase) == "" {
			return CompileError{Path: "runtime_hooks.hooks." + hook.HookID, Message: "phase is required"}
		}
		switch strings.TrimSpace(hook.Phase) {
		case "before_context_build", "after_candidate_retrieval", "before_model_call", "before_memory_write":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hook.HookID + ".phase", Message: fmt.Sprintf("unknown runtime hook phase %q", hook.Phase)}
		}
		switch strings.TrimSpace(hook.ProviderType) {
		case "", "go", "static_hook_host":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hook.HookID + ".provider_type", Message: fmt.Sprintf("unknown runtime hook provider_type %q", hook.ProviderType)}
		}
		switch strings.TrimSpace(hook.FailurePolicy) {
		case "", "ignore", "reject":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hook.HookID + ".failure_policy", Message: fmt.Sprintf("unknown runtime hook failure_policy %q", hook.FailurePolicy)}
		}
		if err := validateRuntimeHookApprovalPolicy(hook.HookID, hook.ApprovalPolicy); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeHookApprovalPolicy(hookID string, policy contracts.RuntimeHookApprovalPolicy) error {
	for _, providerType := range policy.ProviderTypes {
		switch strings.TrimSpace(providerType) {
		case "", "go", "static_hook_host":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hookID + ".approval_policy.provider_types", Message: fmt.Sprintf("unknown runtime hook provider_type %q", providerType)}
		}
	}
	for _, phase := range policy.Phases {
		switch strings.TrimSpace(phase) {
		case "", "before_context_build", "after_candidate_retrieval", "before_model_call", "before_memory_write":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hookID + ".approval_policy.phases", Message: fmt.Sprintf("unknown runtime hook phase %q", phase)}
		}
	}
	for _, failurePolicy := range policy.FailurePolicies {
		switch strings.TrimSpace(failurePolicy) {
		case "", "ignore", "reject":
		default:
			return CompileError{Path: "runtime_hooks.hooks." + hookID + ".approval_policy.failure_policies", Message: fmt.Sprintf("unknown runtime hook failure_policy %q", failurePolicy)}
		}
	}
	return nil
}

func validateAgentStrategies(strategies contracts.AgentStrategies) error {
	if err := validateContextStrategy(strategies.Context); err != nil {
		return err
	}
	if err := validateModelStrategy(strategies.Model); err != nil {
		return err
	}
	if err := validateToolUseStrategy(strategies.Tools); err != nil {
		return err
	}
	if err := validateSkillUseStrategy(strategies.Skills); err != nil {
		return err
	}
	if err := validateRuntimeStrategy(strategies.Runtime); err != nil {
		return err
	}
	if err := validateRepairStrategy(strategies.Repair); err != nil {
		return err
	}
	if err := validateCollaborationStrategy(strategies.Collaboration); err != nil {
		return err
	}
	if err := validateMemoryStrategy(strategies.Memory); err != nil {
		return err
	}
	if err := validateKnowledgeStrategy(strategies.Knowledge); err != nil {
		return err
	}
	if err := validateOutputStrategy(strategies.Output); err != nil {
		return err
	}
	return nil
}

func validateModelStrategy(strategy contracts.ModelStrategy) error {
	if strings.TrimSpace(strategy.Provider) == "" && strings.TrimSpace(strategy.Model) == "" && strategy.MaxOutputTokens == 0 && strategy.Temperature == nil && strings.TrimSpace(strategy.Thinking) == "" && strings.TrimSpace(strategy.ReasoningEffort) == "" && strategy.TimeoutMS == 0 && strategy.Streaming == nil {
		return nil
	}
	if strategy.MaxOutputTokens < 0 {
		return CompileError{Path: "strategies.model.max_output_tokens", Message: "must be greater than or equal to 0"}
	}
	if strategy.TimeoutMS < 0 {
		return CompileError{Path: "strategies.model.timeout_ms", Message: "must be greater than or equal to 0"}
	}
	if strategy.Temperature != nil && (*strategy.Temperature < 0 || *strategy.Temperature > 2) {
		return CompileError{Path: "strategies.model.temperature", Message: "must be between 0 and 2"}
	}
	return nil
}

func applyCompiledStrategies(definition *contracts.AgentDefinition, strategies contracts.AgentStrategies) {
	applyPromptStrategy(definition, strategies.Prompt)
	applyToolUseStrategy(definition, strategies.Tools)
	applyRuntimeStrategy(definition, strategies.Runtime)
	applyRepairStrategy(definition, strategies.Repair)
	applyCollaborationStrategy(definition, strategies.Collaboration)
}

func applyPromptStrategy(definition *contracts.AgentDefinition, strategy contracts.PromptStrategy) {
	if strings.TrimSpace(strategy.IdentityPrompt) != "" {
		definition.IdentityPrompt = strings.TrimSpace(strategy.IdentityPrompt)
	}
	if strings.TrimSpace(strategy.SystemPrompt) != "" {
		definition.SystemPrompt = strings.TrimSpace(strategy.SystemPrompt)
	}
	if strings.TrimSpace(strategy.DeveloperPrompt) != "" {
		definition.DeveloperPrompt = strings.TrimSpace(strategy.DeveloperPrompt)
	}
}

func applyToolUseStrategy(definition *contracts.AgentDefinition, strategy contracts.ToolUseStrategy) {
	if len(strategy.AllowedToolIDs) > 0 {
		definition.Tools.AllowedToolIDs = append([]string(nil), strategy.AllowedToolIDs...)
	}
	if len(strategy.AllowedToolGroupIDs) > 0 {
		definition.Tools.AllowedToolGroupIDs = append([]string(nil), strategy.AllowedToolGroupIDs...)
	}
	if len(strategy.DeniedToolIDs) > 0 {
		definition.Tools.DeniedToolIDs = append([]string(nil), strategy.DeniedToolIDs...)
	}
	if len(strategy.DeniedToolGroupIDs) > 0 {
		definition.Tools.DeniedToolGroupIDs = append([]string(nil), strategy.DeniedToolGroupIDs...)
	}
	if strategy.MaxToolCalls != nil {
		definition.Runtime.MaxToolCalls = *strategy.MaxToolCalls
	}
}

func applyRuntimeStrategy(definition *contracts.AgentDefinition, strategy contracts.RuntimeStrategy) {
	if strategy.MaxSteps != nil {
		definition.Runtime.MaxSteps = *strategy.MaxSteps
	}
	if strategy.MaxDurationSeconds != nil {
		definition.Runtime.MaxDuration = time.Duration(*strategy.MaxDurationSeconds) * time.Second
	}
	if strategy.MaxModelRetries != nil {
		definition.Runtime.MaxModelRetries = *strategy.MaxModelRetries
	}
	if strategy.MaxConsecutiveToolFailures != nil {
		definition.Runtime.MaxConsecutiveToolFailures = *strategy.MaxConsecutiveToolFailures
	}
}

func applyRepairStrategy(definition *contracts.AgentDefinition, strategy contracts.RepairStrategy) {
	if strategy.Enabled != nil && !*strategy.Enabled {
		definition.Runtime.MaxRepairAttempts = 0
		return
	}
	if strategy.MaxRepairAttempts != nil {
		definition.Runtime.MaxRepairAttempts = *strategy.MaxRepairAttempts
	}
}

func applyCollaborationStrategy(definition *contracts.AgentDefinition, strategy contracts.CollaborationStrategy) {
	if strategy.MaxHandoffDepth != nil {
		definition.Runtime.MaxHandoffDepth = *strategy.MaxHandoffDepth
	}
	if strategy.MaxChildTasks != nil {
		definition.Runtime.MaxChildTasks = *strategy.MaxChildTasks
	}
	if strings.TrimSpace(strategy.DelegationMode) == "disabled" {
		definition.Tools.DeniedToolIDs = appendUniqueString(definition.Tools.DeniedToolIDs, "origin.agent.delegate")
	}
}

func validateContextStrategy(strategy contracts.ContextStrategy) error {
	if !validStrategyMode(strategy.Mode, "", "auto", "concise", "balanced", "long_context", "full_debug") {
		return CompileError{Path: "strategies.context.mode", Message: fmt.Sprintf("unknown context mode %q", strategy.Mode)}
	}
	checks := []struct {
		path  string
		value *int
	}{
		{"recent_message_limit", strategy.RecentMessageLimit},
		{"retrieval_max_results", strategy.RetrievalMaxResults},
		{"task_history_max_items", strategy.TaskHistoryMaxItems},
		{"memory_max_items", strategy.MemoryMaxItems},
		{"artifact_ref_max_items", strategy.ArtifactRefMaxItems},
		{"tool_result_max_items", strategy.ToolResultMaxItems},
		{"context_token_budget", strategy.ContextTokenBudget},
	}
	for _, check := range checks {
		if check.value != nil && *check.value < 0 {
			return CompileError{Path: "strategies.context." + check.path, Message: "must be greater than or equal to 0"}
		}
	}
	for _, source := range strategy.EnabledSources {
		if strings.TrimSpace(source) == "" {
			return CompileError{Path: "strategies.context.enabled_sources", Message: "contains empty source"}
		}
	}
	for source, budget := range strategy.SourceBudgets {
		if strings.TrimSpace(source) == "" {
			return CompileError{Path: "strategies.context.source_budgets", Message: "contains empty source"}
		}
		if budget < 0 {
			return CompileError{Path: "strategies.context.source_budgets." + source, Message: "must be greater than or equal to 0"}
		}
	}
	if err := validateContextCompressionStrategy(strategy.Compression); err != nil {
		return err
	}
	return nil
}

func validateContextCompressionStrategy(strategy contracts.ContextCompressionStrategy) error {
	if !validStrategyMode(strategy.Mode, "", "none", "truncate", "llm", "llm_then_truncate") {
		return CompileError{Path: "strategies.context.compression.mode", Message: fmt.Sprintf("unknown compression mode %q", strategy.Mode)}
	}
	if !validStrategyMode(strategy.FailureMode, "", "continue", "reject") {
		return CompileError{Path: "strategies.context.compression.failure_mode", Message: fmt.Sprintf("unknown compression failure_mode %q", strategy.FailureMode)}
	}
	checks := []struct {
		path  string
		value int
	}{
		{"trigger_ratio", strategy.TriggerRatio},
		{"target_tokens", strategy.TargetTokens},
		{"max_tokens", strategy.MaxTokens},
	}
	for _, check := range checks {
		if check.value < 0 {
			return CompileError{Path: "strategies.context.compression." + check.path, Message: "must be greater than or equal to 0"}
		}
	}
	if strategy.TriggerRatio > 100 {
		return CompileError{Path: "strategies.context.compression.trigger_ratio", Message: "must be less than or equal to 100"}
	}
	if strategy.WriteSummaryToMemory {
		return CompileError{Path: "strategies.context.compression.write_summary_to_memory", Message: "is reserved for a future memory summary feature"}
	}
	if strategy.InlinePrompt != nil {
		if strings.TrimSpace(strategy.InlinePrompt.ProfileID) == "" {
			return CompileError{Path: "strategies.context.compression.inline_prompt.profile_id", Message: "profile_id is required"}
		}
		if strings.TrimSpace(strategy.InlinePrompt.SystemPrompt) == "" {
			return CompileError{Path: "strategies.context.compression.inline_prompt.system_prompt", Message: "system_prompt is required"}
		}
	}
	return nil
}

func validateToolUseStrategy(strategy contracts.ToolUseStrategy) error {
	if !validStrategyMode(strategy.ToolChoiceMode, "", "auto", "conservative", "tool_first", "no_tools") {
		return CompileError{Path: "strategies.tools.tool_choice_mode", Message: fmt.Sprintf("unknown tool_choice_mode %q", strategy.ToolChoiceMode)}
	}
	if strategy.MaxToolCalls != nil && *strategy.MaxToolCalls < 0 {
		return CompileError{Path: "strategies.tools.max_tool_calls", Message: "must be greater than or equal to 0"}
	}
	if strategy.RequireApprovalAtRiskLevel != "" {
		if err := strategy.RequireApprovalAtRiskLevel.Validate(); err != nil {
			return CompileError{Path: "strategies.tools.require_approval_at_risk_level", Message: err.Error()}
		}
	}
	if err := validateStringIDs("strategies.tools.allowed_tool_ids", strategy.AllowedToolIDs); err != nil {
		return err
	}
	if err := validateStringIDs("strategies.tools.allowed_tool_group_ids", strategy.AllowedToolGroupIDs); err != nil {
		return err
	}
	if err := validateStringIDs("strategies.tools.denied_tool_ids", strategy.DeniedToolIDs); err != nil {
		return err
	}
	if err := validateStringIDs("strategies.tools.denied_tool_group_ids", strategy.DeniedToolGroupIDs); err != nil {
		return err
	}
	if err := validateStringIDs("strategies.tools.preferred_tool_ids", strategy.PreferredToolIDs); err != nil {
		return err
	}
	if conflict := firstStringConflict(strategy.AllowedToolIDs, strategy.DeniedToolIDs); conflict != "" {
		return CompileError{Path: "strategies.tools", Message: fmt.Sprintf("tool %q appears in both allowed_tool_ids and denied_tool_ids", conflict)}
	}
	if conflict := firstStringConflict(strategy.AllowedToolGroupIDs, strategy.DeniedToolGroupIDs); conflict != "" {
		return CompileError{Path: "strategies.tools", Message: fmt.Sprintf("tool group %q appears in both allowed_tool_group_ids and denied_tool_group_ids", conflict)}
	}
	return nil
}

func validateSkillUseStrategy(strategy contracts.SkillUseStrategy) error {
	if !validStrategyMode(strategy.SelectionMode, "", "auto", "explicit_only", "all_enabled") {
		return CompileError{Path: "strategies.skills.selection_mode", Message: fmt.Sprintf("unknown selection_mode %q", strategy.SelectionMode)}
	}
	if strategy.MaxSelectedSkills < 0 {
		return CompileError{Path: "strategies.skills.max_selected_skills", Message: "must be greater than or equal to 0"}
	}
	if err := validateStringIDs("strategies.skills.enabled_skill_ids", strategy.EnabledSkillIDs); err != nil {
		return err
	}
	if err := validateStringIDs("strategies.skills.disabled_skill_ids", strategy.DisabledSkillIDs); err != nil {
		return err
	}
	if conflict := firstStringConflict(strategy.EnabledSkillIDs, strategy.DisabledSkillIDs); conflict != "" {
		return CompileError{Path: "strategies.skills", Message: fmt.Sprintf("skill %q appears in both enabled_skill_ids and disabled_skill_ids", conflict)}
	}
	return nil
}

func validateRuntimeStrategy(strategy contracts.RuntimeStrategy) error {
	if strategy.MaxSteps != nil && *strategy.MaxSteps <= 0 {
		return CompileError{Path: "strategies.runtime.max_steps", Message: "must be greater than 0"}
	}
	if strategy.MaxDurationSeconds != nil && *strategy.MaxDurationSeconds <= 0 {
		return CompileError{Path: "strategies.runtime.max_duration_seconds", Message: "must be greater than 0"}
	}
	if strategy.MaxModelRetries != nil && *strategy.MaxModelRetries < 0 {
		return CompileError{Path: "strategies.runtime.max_model_retries", Message: "must be greater than or equal to 0"}
	}
	if strategy.MaxConsecutiveToolFailures != nil && *strategy.MaxConsecutiveToolFailures < 0 {
		return CompileError{Path: "strategies.runtime.max_consecutive_tool_failures", Message: "must be greater than or equal to 0"}
	}
	if !validStrategyMode(strategy.ExecutionMode, "", "sync", "async") {
		return CompileError{Path: "strategies.runtime.execution_mode", Message: fmt.Sprintf("unknown execution_mode %q", strategy.ExecutionMode)}
	}
	return nil
}

func validateRepairStrategy(strategy contracts.RepairStrategy) error {
	if strategy.MaxRepairAttempts != nil && *strategy.MaxRepairAttempts < 0 {
		return CompileError{Path: "strategies.repair.max_repair_attempts", Message: "must be greater than or equal to 0"}
	}
	if !validStrategyMode(strategy.FailureMode, "", "stop", "continue", "request_model_repair") {
		return CompileError{Path: "strategies.repair.failure_mode", Message: fmt.Sprintf("unknown failure_mode %q", strategy.FailureMode)}
	}
	for _, code := range strategy.RepairableErrorCodes {
		if strings.TrimSpace(code) == "" {
			return CompileError{Path: "strategies.repair.repairable_error_codes", Message: "contains empty error code"}
		}
	}
	return nil
}

func validateCollaborationStrategy(strategy contracts.CollaborationStrategy) error {
	if !validStrategyMode(strategy.DelegationMode, "", "disabled", "explicit", "auto") {
		return CompileError{Path: "strategies.collaboration.delegation_mode", Message: fmt.Sprintf("unknown delegation_mode %q", strategy.DelegationMode)}
	}
	if strategy.MaxHandoffDepth != nil && *strategy.MaxHandoffDepth < 0 {
		return CompileError{Path: "strategies.collaboration.max_handoff_depth", Message: "must be greater than or equal to 0"}
	}
	if strategy.MaxChildTasks != nil && *strategy.MaxChildTasks < 0 {
		return CompileError{Path: "strategies.collaboration.max_child_tasks", Message: "must be greater than or equal to 0"}
	}
	if strategy.MaxContextTokens != nil && *strategy.MaxContextTokens < 0 {
		return CompileError{Path: "strategies.collaboration.max_context_tokens", Message: "must be greater than or equal to 0"}
	}
	if strategy.DefaultHandoffMode != "" {
		if err := strategy.DefaultHandoffMode.Validate(); err != nil {
			return CompileError{Path: "strategies.collaboration.default_handoff_mode", Message: err.Error()}
		}
	}
	return nil
}

func validateMemoryStrategy(strategy contracts.MemoryUseStrategy) error {
	if strategy.MaxMemoryItems != nil && *strategy.MaxMemoryItems < 0 {
		return CompileError{Path: "strategies.memory.max_memory_items", Message: "must be greater than or equal to 0"}
	}
	if !validStrategyMode(strategy.AutoWriteMode, "", "disabled", "explicit_intent") {
		return CompileError{Path: "strategies.memory.auto_write_mode", Message: fmt.Sprintf("unknown auto_write_mode %q", strategy.AutoWriteMode)}
	}
	for _, scope := range strategy.ReadScopes {
		if strings.TrimSpace(scope) == "" {
			return CompileError{Path: "strategies.memory.read_scopes", Message: "contains empty scope"}
		}
	}
	for _, scope := range strategy.WriteScopes {
		if strings.TrimSpace(scope) == "" {
			return CompileError{Path: "strategies.memory.write_scopes", Message: "contains empty scope"}
		}
	}
	return nil
}

func validateKnowledgeStrategy(strategy contracts.KnowledgeUseStrategy) error {
	if !validStrategyMode(strategy.SearchMode, "", contracts.KnowledgeSearchBM25, contracts.KnowledgeSearchEmbedding, contracts.KnowledgeSearchHybrid) {
		return CompileError{Path: "strategies.knowledge.search_mode", Message: fmt.Sprintf("unknown search_mode %q", strategy.SearchMode)}
	}
	if !validStrategyMode(strategy.InjectMode, "", "tool_only") {
		return CompileError{Path: "strategies.knowledge.inject_mode", Message: fmt.Sprintf("unknown inject_mode %q", strategy.InjectMode)}
	}
	if strategy.MaxResults < 0 {
		return CompileError{Path: "strategies.knowledge.max_results", Message: "must be greater than or equal to 0"}
	}
	if err := validateKnowledgeBaseIDs("strategies.knowledge.knowledge_base_ids", strategy.KnowledgeBaseIDs); err != nil {
		return err
	}
	return nil
}

func validateOutputStrategy(strategy contracts.OutputStrategy) error {
	if !validStrategyMode(strategy.OutputMode, "", "decision_json") {
		return CompileError{Path: "strategies.output.output_mode", Message: fmt.Sprintf("unknown output_mode %q", strategy.OutputMode)}
	}
	if len(strategy.OutputSchema) > 0 {
		return CompileError{Path: "strategies.output.output_schema", Message: "agent-level output schema is reserved for a future output strategy feature"}
	}
	return nil
}

func validateStringIDs(path string, values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return CompileError{Path: path, Message: "contains empty value"}
		}
		if _, ok := seen[value]; ok {
			return CompileError{Path: path, Message: fmt.Sprintf("contains duplicate value %q", value)}
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateKnowledgeBaseIDs(path string, values []contracts.KnowledgeBaseID) error {
	seen := map[contracts.KnowledgeBaseID]struct{}{}
	for _, value := range values {
		value = contracts.KnowledgeBaseID(strings.TrimSpace(string(value)))
		if value == "" {
			return CompileError{Path: path, Message: "contains empty value"}
		}
		if _, ok := seen[value]; ok {
			return CompileError{Path: path, Message: fmt.Sprintf("contains duplicate value %q", value)}
		}
		seen[value] = struct{}{}
	}
	return nil
}

func firstStringConflict(left []string, right []string) string {
	seen := map[string]struct{}{}
	for _, value := range left {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range right {
		value = strings.TrimSpace(value)
		if _, ok := seen[value]; ok {
			return value
		}
	}
	return ""
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func validStrategyMode(value string, allowed ...string) bool {
	value = strings.TrimSpace(value)
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func validateExports(exports *contracts.AgentExports) error {
	seen := map[string]struct{}{}
	for i := range exports.Tools {
		tool := &exports.Tools[i]
		if strings.TrimSpace(tool.ToolID) == "" {
			return CompileError{Path: "exports.tools", Message: "tool_id is required"}
		}
		if _, ok := seen[tool.ToolID]; ok {
			return CompileError{Path: "exports.tools", Message: fmt.Sprintf("duplicate exported tool %q", tool.ToolID)}
		}
		seen[tool.ToolID] = struct{}{}
		if strings.TrimSpace(tool.Name) == "" {
			return CompileError{Path: "exports.tools." + tool.ToolID, Message: "name is required"}
		}
		if strings.TrimSpace(tool.Description) == "" {
			return CompileError{Path: "exports.tools." + tool.ToolID, Message: "description is required"}
		}
		if tool.InputSchema == nil {
			return CompileError{Path: "exports.tools." + tool.ToolID, Message: "input_schema is required"}
		}
		if tool.RiskLevel == "" {
			tool.RiskLevel = contracts.RiskLow
		}
		if err := tool.RiskLevel.Validate(); err != nil {
			return CompileError{Path: "exports.tools." + tool.ToolID + ".risk_level", Message: err.Error()}
		}
		if tool.Visibility == "" {
			tool.Visibility = contracts.ToolProtected
		}
		if !validToolVisibility(tool.Visibility) {
			return CompileError{Path: "exports.tools." + tool.ToolID + ".visibility", Message: fmt.Sprintf("unknown tool visibility %q", tool.Visibility)}
		}
		if tool.Status == "" {
			tool.Status = "enabled"
		}
		if tool.Version == "" {
			tool.Version = "v1"
		}
		if tool.Operation == "" {
			tool.Operation = tool.ToolID
		}
	}
	return nil
}

func validToolVisibility(visibility contracts.ToolVisibility) bool {
	switch visibility {
	case contracts.ToolPrivate, contracts.ToolProtected, contracts.ToolExposed:
		return true
	default:
		return false
	}
}

func validateRuntimeLimits(limits contracts.RuntimeLimits) error {
	if limits.MaxSteps <= 0 {
		return CompileError{Path: "agent.yaml.runtime.max_steps", Message: "must be greater than 0"}
	}
	if limits.MaxToolCalls < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_tool_calls", Message: "must be greater than or equal to 0"}
	}
	if limits.MaxDuration <= 0 {
		return CompileError{Path: "agent.yaml.runtime.max_duration_seconds", Message: "must be greater than 0"}
	}
	if limits.MaxPromptTokens <= 0 {
		return CompileError{Path: "agent.yaml.runtime.max_prompt_tokens", Message: "must be greater than 0"}
	}
	if limits.MaxHandoffDepth < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_handoff_depth", Message: "must be greater than or equal to 0"}
	}
	if limits.MaxChildTasks < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_child_tasks", Message: "must be greater than or equal to 0"}
	}
	if limits.MaxModelRetries < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_model_retries", Message: "must be greater than or equal to 0"}
	}
	if limits.MaxRepairAttempts < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_repair_attempts", Message: "must be greater than or equal to 0"}
	}
	if limits.MaxConsecutiveToolFailures < 0 {
		return CompileError{Path: "agent.yaml.runtime.max_consecutive_tool_failures", Message: "must be greater than or equal to 0"}
	}
	return nil
}

func firstLine(value string, fallback string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return fallback
}

func metadataString(metadata map[string]any, key string, fallback string) string {
	if metadata == nil {
		return fallback
	}
	if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func validateSkillRefs(refs []contracts.SkillDefinitionRef) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		skillID := strings.TrimSpace(ref.SkillID)
		if skillID == "" {
			return CompileError{Path: "skills", Message: "skill_id is required"}
		}
		identity := skillID + "@" + strings.TrimSpace(ref.Version)
		if _, ok := seen[identity]; ok {
			return CompileError{Path: "skills", Message: fmt.Sprintf("duplicate skill ref %q", identity)}
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func normalizeSkillDefinitions(definitions []contracts.SkillDefinition) ([]contracts.SkillDefinition, error) {
	out := make([]contracts.SkillDefinition, 0, len(definitions))
	seen := map[string]struct{}{}
	for _, definition := range definitions {
		skillID := strings.TrimSpace(definition.Card.SkillID)
		if skillID == "" {
			return nil, CompileError{Path: "skill_definitions.card.skill_id", Message: "skill_id is required"}
		}
		version := strings.TrimSpace(definition.Card.Version)
		identity := skillID + "@" + version
		if _, ok := seen[identity]; ok {
			return nil, CompileError{Path: "skill_definitions", Message: fmt.Sprintf("duplicate skill definition %q", identity)}
		}
		seen[identity] = struct{}{}
		definition.Card.SkillID = skillID
		definition.Card.Version = version
		if strings.TrimSpace(definition.Card.Name) == "" {
			definition.Card.Name = skillID
		}
		if strings.TrimSpace(definition.Card.Description) == "" {
			definition.Card.Description = definition.Card.Name
		}
		if definition.Card.RiskLevel == "" {
			definition.Card.RiskLevel = contracts.RiskLow
		}
		if err := definition.Card.RiskLevel.Validate(); err != nil {
			return nil, CompileError{Path: "skill_definitions." + skillID + ".risk_level", Message: err.Error()}
		}
		if strings.TrimSpace(definition.Instruction.SkillID) == "" {
			definition.Instruction.SkillID = skillID
		}
		if definition.Instruction.SkillID != skillID {
			return nil, CompileError{Path: "skill_definitions." + skillID + ".instruction.skill_id", Message: "must match card.skill_id"}
		}
		for _, resource := range definition.Resources {
			if strings.TrimSpace(resource.ResourceID) == "" || strings.TrimSpace(resource.Type) == "" || strings.TrimSpace(resource.URI) == "" {
				return nil, CompileError{Path: "skill_definitions." + skillID + ".resources", Message: "resource_id, type, and uri are required"}
			}
		}
		if len(definition.Resources) > 0 && len(definition.Card.ResourceRefs) == 0 {
			definition.Card.ResourceRefs = skillResourceIDs(definition.Resources)
		}
		out = append(out, definition)
	}
	return out, nil
}

func skillResourceIDs(resources []contracts.SkillResourceRef) []string {
	if len(resources) == 0 {
		return nil
	}
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		out = append(out, resource.ResourceID)
	}
	return out
}
