package strategy

import (
	"reflect"
	"strings"

	"znt/internal/contracts"
	"znt/pkg/hash"
)

type Defaults struct {
	Context contracts.ContextStrategy
}

type EffectiveRunConfig struct {
	Prompt        contracts.PromptStrategy
	Model         contracts.ModelStrategy
	Context       contracts.ContextStrategy
	Tools         contracts.ToolUseStrategy
	Skills        contracts.SkillUseStrategy
	Collaboration contracts.CollaborationStrategy
	Memory        contracts.MemoryUseStrategy
	Knowledge     contracts.KnowledgeUseStrategy
	Runtime       contracts.RuntimeStrategy
	Repair        contracts.RepairStrategy
	Output        contracts.OutputStrategy

	Policy contracts.PolicySet
}

type GuardrailAdjustment struct {
	Path      string `json:"path"`
	Reason    string `json:"reason,omitempty"`
	Requested any    `json:"requested,omitempty"`
	Effective any    `json:"effective,omitempty"`
}

type Report struct {
	StrategyHash string                `json:"strategy_hash,omitempty"`
	Adjustments  []GuardrailAdjustment `json:"guardrail_adjustments,omitempty"`
}

func DefaultContextStrategy() contracts.ContextStrategy {
	return contracts.ContextStrategy{
		Mode:                "balanced",
		RecentMessageLimit:  contracts.IntPtr(20),
		RetrievalMaxResults: contracts.IntPtr(8),
		TaskHistoryMaxItems: contracts.IntPtr(30),
		ContextTokenBudget:  contracts.IntPtr(4000),
		Compression: contracts.ContextCompressionStrategy{
			Enabled: true,
			Mode:    "truncate",
		},
	}
}

func DefaultValues() Defaults {
	return Defaults{Context: DefaultContextStrategy()}
}

func Resolve(agent contracts.AgentDefinition, policy contracts.PolicySet, defaults Defaults) (EffectiveRunConfig, Report, error) {
	if isZeroContextStrategy(defaults.Context) {
		defaults.Context = DefaultContextStrategy()
	}

	effective := EffectiveRunConfig{
		Prompt:        agent.Strategies.Prompt,
		Model:         mergeModelStrategy(agent.Strategies.Model),
		Context:       agent.Strategies.Context,
		Tools:         mergeToolUseStrategy(agent.Tools, agent.Runtime, agent.Strategies.Tools),
		Skills:        agent.Strategies.Skills,
		Collaboration: mergeCollaborationStrategy(agent.Runtime, agent.Strategies.Collaboration),
		Memory:        mergeMemoryUseStrategy(agent.Strategies.Memory),
		Knowledge:     agent.Strategies.Knowledge,
		Runtime:       mergeRuntimeStrategy(agent.Runtime, agent.Strategies.Runtime),
		Repair:        mergeRepairStrategy(agent.Runtime, agent.Strategies.Repair),
		Output:        agent.Strategies.Output,
		Policy:        policy,
	}
	effective.Context = mergeContextDefaults(effective.Context, defaults.Context)

	var adjustments []GuardrailAdjustment
	effective.Context, adjustments = applyContextGovernance(effective.Context, policy.ContextGovernancePolicy)
	effective.Runtime, effective.Tools, effective.Repair, adjustments = applyRuntimeGovernance(effective.Runtime, effective.Tools, effective.Repair, policy.RuntimePolicy, adjustments)
	effective.Policy, effective.Tools, adjustments = applyToolGovernance(effective.Policy, effective.Tools, adjustments)
	effective.Policy, effective.Repair, adjustments = applyRepairGovernance(effective.Policy, effective.Repair, adjustments)
	effective.Policy, effective.Collaboration, adjustments = applyCollaborationGovernance(effective.Policy, effective.Collaboration, adjustments)
	effective.Policy, effective.Memory, adjustments = applyMemoryGovernance(effective.Policy, effective.Memory, adjustments)

	strategyHash, err := hash.StableJSON(map[string]any{
		"agent_id":                agent.AgentID,
		"agent_version":           agent.Version,
		"agent_strategies":        agent.Strategies,
		"effective_prompt":        effective.Prompt,
		"effective_model":         effective.Model,
		"effective_context":       effective.Context,
		"effective_tools":         effective.Tools,
		"effective_skills":        effective.Skills,
		"effective_collaboration": effective.Collaboration,
		"effective_memory":        effective.Memory,
		"effective_knowledge":     effective.Knowledge,
		"effective_runtime":       effective.Runtime,
		"effective_repair":        effective.Repair,
		"effective_output":        effective.Output,
		"effective_policy":        effective.Policy,
		"policy_set_id":           policy.PolicySetID,
		"policy_version":          policy.Version,
		"adjustments":             adjustments,
	})
	if err != nil {
		return EffectiveRunConfig{}, Report{}, err
	}
	return effective, Report{StrategyHash: strategyHash, Adjustments: adjustments}, nil
}

func mergeModelStrategy(strategy contracts.ModelStrategy) contracts.ModelStrategy {
	if strategy.Streaming == nil {
		strategy.Streaming = contracts.BoolPtr(true)
	}
	return strategy
}

func mergeToolUseStrategy(tools contracts.AgentToolsConfig, runtime contracts.RuntimeLimits, strategy contracts.ToolUseStrategy) contracts.ToolUseStrategy {
	if len(strategy.AllowedToolIDs) == 0 {
		strategy.AllowedToolIDs = append([]string(nil), tools.AllowedToolIDs...)
	}
	if len(strategy.AllowedToolGroupIDs) == 0 {
		strategy.AllowedToolGroupIDs = append([]string(nil), tools.AllowedToolGroupIDs...)
	}
	if len(strategy.DeniedToolIDs) == 0 {
		strategy.DeniedToolIDs = append([]string(nil), tools.DeniedToolIDs...)
	}
	if len(strategy.DeniedToolGroupIDs) == 0 {
		strategy.DeniedToolGroupIDs = append([]string(nil), tools.DeniedToolGroupIDs...)
	}
	if strategy.MaxToolCalls == nil && hasRuntimeLimits(runtime) {
		strategy.MaxToolCalls = contracts.IntPtr(runtime.MaxToolCalls)
	}
	if strategy.ToolChoiceMode == "" {
		strategy.ToolChoiceMode = "auto"
	}
	return strategy
}

func mergeRuntimeStrategy(runtime contracts.RuntimeLimits, strategy contracts.RuntimeStrategy) contracts.RuntimeStrategy {
	if hasRuntimeLimits(runtime) {
		if strategy.MaxSteps == nil {
			strategy.MaxSteps = contracts.IntPtr(runtime.MaxSteps)
		}
		if strategy.MaxDurationSeconds == nil && runtime.MaxDuration > 0 {
			strategy.MaxDurationSeconds = contracts.IntPtr(int(runtime.MaxDuration.Seconds()))
		}
		if strategy.MaxModelRetries == nil {
			strategy.MaxModelRetries = contracts.IntPtr(runtime.MaxModelRetries)
		}
		if strategy.MaxConsecutiveToolFailures == nil {
			strategy.MaxConsecutiveToolFailures = contracts.IntPtr(runtime.MaxConsecutiveToolFailures)
		}
	}
	if strategy.ExecutionMode == "" {
		strategy.ExecutionMode = "sync"
	}
	return strategy
}

func mergeRepairStrategy(runtime contracts.RuntimeLimits, strategy contracts.RepairStrategy) contracts.RepairStrategy {
	if strategy.MaxRepairAttempts == nil && hasRuntimeLimits(runtime) {
		strategy.MaxRepairAttempts = contracts.IntPtr(runtime.MaxRepairAttempts)
	}
	return strategy
}

func mergeCollaborationStrategy(runtime contracts.RuntimeLimits, strategy contracts.CollaborationStrategy) contracts.CollaborationStrategy {
	if hasRuntimeLimits(runtime) {
		if strategy.MaxHandoffDepth == nil {
			strategy.MaxHandoffDepth = contracts.IntPtr(runtime.MaxHandoffDepth)
		}
		if strategy.MaxChildTasks == nil {
			strategy.MaxChildTasks = contracts.IntPtr(runtime.MaxChildTasks)
		}
	}
	if strategy.DelegationMode == "" {
		strategy.DelegationMode = "auto"
	}
	return strategy
}

func mergeMemoryUseStrategy(strategy contracts.MemoryUseStrategy) contracts.MemoryUseStrategy {
	if strategy.ReadEnabled == nil {
		strategy.ReadEnabled = contracts.BoolPtr(true)
	}
	if strategy.WriteEnabled == nil {
		strategy.WriteEnabled = contracts.BoolPtr(true)
	}
	if strategy.AutoWriteMode == "" {
		strategy.AutoWriteMode = "explicit_intent"
	}
	return strategy
}

func hasRuntimeLimits(runtime contracts.RuntimeLimits) bool {
	return runtime.MaxSteps > 0 || runtime.MaxToolCalls > 0 || runtime.MaxDuration > 0 || runtime.MaxPromptTokens > 0 || runtime.MaxHandoffDepth > 0 || runtime.MaxChildTasks > 0 || runtime.MaxRepairAttempts > 0 || runtime.MaxModelRetries > 0 || runtime.MaxConsecutiveToolFailures > 0
}

func mergeContextDefaults(strategy contracts.ContextStrategy, defaults contracts.ContextStrategy) contracts.ContextStrategy {
	if isZeroContextStrategy(strategy) {
		return defaults
	}
	if strategy.Mode == "" {
		strategy.Mode = defaults.Mode
	}
	if strategy.Mode != "full_debug" {
		if strategy.RecentMessageLimit == nil {
			strategy.RecentMessageLimit = defaults.RecentMessageLimit
		}
		if strategy.RetrievalMaxResults == nil {
			strategy.RetrievalMaxResults = defaults.RetrievalMaxResults
		}
		if strategy.TaskHistoryMaxItems == nil {
			strategy.TaskHistoryMaxItems = defaults.TaskHistoryMaxItems
		}
		if strategy.MemoryMaxItems == nil {
			strategy.MemoryMaxItems = defaults.MemoryMaxItems
		}
		if strategy.ArtifactRefMaxItems == nil {
			strategy.ArtifactRefMaxItems = defaults.ArtifactRefMaxItems
		}
		if strategy.ToolResultMaxItems == nil {
			strategy.ToolResultMaxItems = defaults.ToolResultMaxItems
		}
		if strategy.ContextTokenBudget == nil {
			strategy.ContextTokenBudget = defaults.ContextTokenBudget
		}
	} else {
		if strategy.RecentMessageLimit == nil {
			strategy.RecentMessageLimit = contracts.IntPtr(0)
		}
		if strategy.RetrievalMaxResults == nil {
			strategy.RetrievalMaxResults = contracts.IntPtr(0)
		}
		if strategy.TaskHistoryMaxItems == nil {
			strategy.TaskHistoryMaxItems = contracts.IntPtr(0)
		}
		if strategy.MemoryMaxItems == nil {
			strategy.MemoryMaxItems = contracts.IntPtr(0)
		}
		if strategy.ArtifactRefMaxItems == nil {
			strategy.ArtifactRefMaxItems = contracts.IntPtr(0)
		}
		if strategy.ToolResultMaxItems == nil {
			strategy.ToolResultMaxItems = contracts.IntPtr(0)
		}
		if strategy.ContextTokenBudget == nil {
			strategy.ContextTokenBudget = contracts.IntPtr(0)
		}
	}
	if len(strategy.EnabledSources) == 0 {
		strategy.EnabledSources = defaults.EnabledSources
	}
	if strategy.SourceBudgets == nil {
		strategy.SourceBudgets = defaults.SourceBudgets
	}
	if isZeroCompressionStrategy(strategy.Compression) {
		strategy.Compression = defaults.Compression
	} else if strategy.Compression.Mode == "" {
		strategy.Compression.Mode = defaults.Compression.Mode
	}
	if strategy.Compression.Mode == "none" {
		strategy.Compression.Enabled = false
	} else if strategy.Compression.Mode != "" {
		strategy.Compression.Enabled = true
	}
	return strategy
}

func applyContextGovernance(strategy contracts.ContextStrategy, policy contracts.ContextGovernancePolicy) (contracts.ContextStrategy, []GuardrailAdjustment) {
	var adjustments []GuardrailAdjustment
	if reflect.DeepEqual(policy, contracts.ContextGovernancePolicy{}) {
		return strategy, adjustments
	}
	if strategy.Mode == "full_debug" && !policy.AllowFullDebugMode {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "context.mode",
			Reason:    "full_debug mode is not allowed by policy",
			Requested: strategy.Mode,
			Effective: "balanced",
		})
		strategy.Mode = "balanced"
	}

	strategy.ContextTokenBudget, adjustments = capIntPtr(adjustments, "context.context_token_budget", strategy.ContextTokenBudget, policy.MaxContextTokenBudget)
	strategy.RecentMessageLimit, adjustments = capIntPtr(adjustments, "context.recent_message_limit", strategy.RecentMessageLimit, policy.MaxRecentMessageLimit)
	strategy.RetrievalMaxResults, adjustments = capIntPtr(adjustments, "context.retrieval_max_results", strategy.RetrievalMaxResults, policy.MaxRetrievalResults)
	strategy.TaskHistoryMaxItems, adjustments = capIntPtr(adjustments, "context.task_history_max_items", strategy.TaskHistoryMaxItems, policy.MaxTaskHistoryItems)
	strategy.MemoryMaxItems, adjustments = capIntPtr(adjustments, "context.memory_max_items", strategy.MemoryMaxItems, policy.MaxMemoryItems)
	strategy.ArtifactRefMaxItems, adjustments = capIntPtr(adjustments, "context.artifact_ref_max_items", strategy.ArtifactRefMaxItems, policy.MaxArtifactRefItems)
	strategy.ToolResultMaxItems, adjustments = capIntPtr(adjustments, "context.tool_result_max_items", strategy.ToolResultMaxItems, policy.MaxToolResultItems)

	if compressionUsesLLM(strategy.Compression.Mode) && !policy.AllowLLMCompression {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "context.compression.mode",
			Reason:    "LLM compression is not allowed by policy",
			Requested: strategy.Compression.Mode,
			Effective: "truncate",
		})
		strategy.Compression.Mode = "truncate"
		strategy.Compression.ModelProvider = ""
		strategy.Compression.ModelBaseURL = ""
		strategy.Compression.ModelName = ""
	}
	allowedCompressionModels := nonEmptyStrings(policy.AllowedCompressionModels)
	if compressionUsesLLM(strategy.Compression.Mode) && len(allowedCompressionModels) > 0 && !contains(allowedCompressionModels, strategy.Compression.ModelName) {
		replacement := allowedCompressionModels[0]
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "context.compression.model_name",
			Reason:    "compression model is not allowed by policy",
			Requested: strategy.Compression.ModelName,
			Effective: replacement,
		})
		strategy.Compression.ModelName = replacement
	}
	allowedCompressionBaseURLs := nonEmptyStrings(policy.AllowedCompressionBaseURLs)
	if compressionUsesLLM(strategy.Compression.Mode) && strings.TrimSpace(strategy.Compression.ModelBaseURL) != "" && !contains(allowedCompressionBaseURLs, strategy.Compression.ModelBaseURL) {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "context.compression.model_base_url",
			Reason:    "compression model base_url is not allowed by policy",
			Requested: strategy.Compression.ModelBaseURL,
			Effective: "",
		})
		strategy.Compression.ModelBaseURL = ""
	}
	return strategy, adjustments
}

func applyRuntimeGovernance(runtime contracts.RuntimeStrategy, tools contracts.ToolUseStrategy, repair contracts.RepairStrategy, policy contracts.RuntimePolicy, adjustments []GuardrailAdjustment) (contracts.RuntimeStrategy, contracts.ToolUseStrategy, contracts.RepairStrategy, []GuardrailAdjustment) {
	runtime.MaxSteps, adjustments = capConfiguredIntPtr(adjustments, "runtime.max_steps", runtime.MaxSteps, policy.MaxSteps, true)
	tools.MaxToolCalls, adjustments = capConfiguredIntPtr(adjustments, "tools.max_tool_calls", tools.MaxToolCalls, policy.MaxToolCalls, true)
	runtime.MaxModelRetries, adjustments = capConfiguredIntPtr(adjustments, "runtime.max_model_retries", runtime.MaxModelRetries, policy.MaxModelRetries, false)
	repair.MaxRepairAttempts, adjustments = capConfiguredIntPtr(adjustments, "repair.max_repair_attempts", repair.MaxRepairAttempts, policy.MaxRepairAttempts, false)
	runtime.MaxConsecutiveToolFailures, adjustments = capConfiguredIntPtr(adjustments, "runtime.max_consecutive_tool_failures", runtime.MaxConsecutiveToolFailures, policy.MaxConsecutiveToolFailures, true)
	return runtime, tools, repair, adjustments
}

func applyToolGovernance(policy contracts.PolicySet, tools contracts.ToolUseStrategy, adjustments []GuardrailAdjustment) (contracts.PolicySet, contracts.ToolUseStrategy, []GuardrailAdjustment) {
	threshold := strictestRiskThreshold(policy.ToolPolicy.RequireApprovalAtRiskLevel, tools.RequireApprovalAtRiskLevel)
	if threshold == "" {
		return policy, tools, adjustments
	}
	if tools.RequireApprovalAtRiskLevel != "" && policy.ToolPolicy.RequireApprovalAtRiskLevel != "" && threshold != tools.RequireApprovalAtRiskLevel {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "tools.require_approval_at_risk_level",
			Reason:    "approval threshold is limited by policy",
			Requested: tools.RequireApprovalAtRiskLevel,
			Effective: threshold,
		})
	}
	tools.RequireApprovalAtRiskLevel = threshold
	policy.ToolPolicy.RequireApprovalAtRiskLevel = threshold
	return policy, tools, adjustments
}

func applyRepairGovernance(policy contracts.PolicySet, repair contracts.RepairStrategy, adjustments []GuardrailAdjustment) (contracts.PolicySet, contracts.RepairStrategy, []GuardrailAdjustment) {
	if repair.Enabled != nil {
		if !*repair.Enabled {
			policy.ToolRepairPolicy.Enabled = false
			repair.MaxRepairAttempts = contracts.IntPtr(0)
		} else if !policy.ToolRepairPolicy.Enabled {
			disabled := false
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "repair.enabled",
				Reason:    "repair is disabled by policy",
				Requested: true,
				Effective: false,
			})
			repair.Enabled = &disabled
			repair.MaxRepairAttempts = contracts.IntPtr(0)
		}
	}
	if repair.MaxRepairAttempts != nil {
		policy.ToolRepairPolicy.MaxRepairAttempts = *repair.MaxRepairAttempts
	}
	if repair.RequestModelRepairOnFail != nil {
		if !*repair.RequestModelRepairOnFail {
			policy.ToolRepairPolicy.RequestModelRepairOnFail = false
		} else if !policy.ToolRepairPolicy.RequestModelRepairOnFail {
			disabled := false
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "repair.request_model_repair_on_fail",
				Reason:    "model repair on tool failure is disabled by policy",
				Requested: true,
				Effective: false,
			})
			repair.RequestModelRepairOnFail = &disabled
		}
	}
	if repair.StopOnDenied != nil {
		if *repair.StopOnDenied {
			policy.ToolRepairPolicy.StopOnDenied = true
		} else if policy.ToolRepairPolicy.StopOnDenied {
			enabled := true
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "repair.stop_on_denied",
				Reason:    "policy requires denied tool results to stop",
				Requested: false,
				Effective: true,
			})
			repair.StopOnDenied = &enabled
		}
	}
	if len(repair.RepairableErrorCodes) > 0 {
		effectiveCodes := repair.RepairableErrorCodes
		if len(policy.ToolRepairPolicy.RepairableErrorCodes) > 0 {
			effectiveCodes = intersectStrings(repair.RepairableErrorCodes, policy.ToolRepairPolicy.RepairableErrorCodes)
			if len(effectiveCodes) != len(repair.RepairableErrorCodes) {
				adjustments = append(adjustments, GuardrailAdjustment{
					Path:      "repair.repairable_error_codes",
					Reason:    "repairable error codes are limited by policy",
					Requested: repair.RepairableErrorCodes,
					Effective: effectiveCodes,
				})
			}
		}
		repair.RepairableErrorCodes = effectiveCodes
		policy.ToolRepairPolicy.RepairableErrorCodes = effectiveCodes
	}
	if repair.FailureMode == "stop" {
		policy.ToolRepairPolicy.RequestModelRepairOnFail = false
		if repair.MaxRepairAttempts == nil {
			repair.MaxRepairAttempts = contracts.IntPtr(0)
			policy.ToolRepairPolicy.MaxRepairAttempts = 0
		}
	}
	return policy, repair, adjustments
}

func applyCollaborationGovernance(policy contracts.PolicySet, collaboration contracts.CollaborationStrategy, adjustments []GuardrailAdjustment) (contracts.PolicySet, contracts.CollaborationStrategy, []GuardrailAdjustment) {
	if reflect.DeepEqual(policy.HandoffPolicy, contracts.HandoffPolicy{}) {
		return policy, collaboration, adjustments
	}
	if collaboration.DefaultHandoffMode != "" {
		effectiveMode := collaboration.DefaultHandoffMode
		if effectiveMode == contracts.HandoffFullContext && !policy.HandoffPolicy.AllowFullContext {
			effectiveMode = policy.HandoffPolicy.DefaultMode
			if effectiveMode == "" || effectiveMode == contracts.HandoffFullContext {
				effectiveMode = contracts.HandoffHybrid
			}
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "collaboration.default_handoff_mode",
				Reason:    "full-context handoff is not allowed by policy",
				Requested: collaboration.DefaultHandoffMode,
				Effective: effectiveMode,
			})
		}
		collaboration.DefaultHandoffMode = effectiveMode
		policy.HandoffPolicy.DefaultMode = effectiveMode
	}

	if collaboration.MaxContextTokens == nil {
		if policy.HandoffPolicy.MaxContextTokens > 0 {
			collaboration.MaxContextTokens = contracts.IntPtr(policy.HandoffPolicy.MaxContextTokens)
		}
		return policy, collaboration, adjustments
	}

	requested := *collaboration.MaxContextTokens
	if policy.HandoffPolicy.MaxContextTokens > 0 && (requested == 0 || requested > policy.HandoffPolicy.MaxContextTokens) {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      "collaboration.max_context_tokens",
			Reason:    "limited by policy",
			Requested: requested,
			Effective: policy.HandoffPolicy.MaxContextTokens,
		})
		collaboration.MaxContextTokens = contracts.IntPtr(policy.HandoffPolicy.MaxContextTokens)
		return policy, collaboration, adjustments
	}
	if requested > 0 && (policy.HandoffPolicy.MaxContextTokens == 0 || requested < policy.HandoffPolicy.MaxContextTokens) {
		policy.HandoffPolicy.MaxContextTokens = requested
	}
	return policy, collaboration, adjustments
}

func applyMemoryGovernance(policy contracts.PolicySet, memory contracts.MemoryUseStrategy, adjustments []GuardrailAdjustment) (contracts.PolicySet, contracts.MemoryUseStrategy, []GuardrailAdjustment) {
	if reflect.DeepEqual(policy.MemoryPolicy, contracts.MemoryPolicy{}) {
		return policy, memory, adjustments
	}
	if memory.ReadEnabled != nil {
		if !*memory.ReadEnabled {
			policy.MemoryPolicy.AllowRead = false
		} else if !policy.MemoryPolicy.AllowRead {
			disabled := false
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "memory.read_enabled",
				Reason:    "memory read is disabled by policy",
				Requested: true,
				Effective: false,
			})
			memory.ReadEnabled = &disabled
		}
	}
	if memory.WriteEnabled != nil {
		if !*memory.WriteEnabled {
			policy.MemoryPolicy.AllowWrite = false
		} else if !policy.MemoryPolicy.AllowWrite {
			disabled := false
			adjustments = append(adjustments, GuardrailAdjustment{
				Path:      "memory.write_enabled",
				Reason:    "memory write is disabled by policy",
				Requested: true,
				Effective: false,
			})
			memory.WriteEnabled = &disabled
		}
	}
	requestedReadScopes := len(memory.ReadScopes) > 0
	requestedWriteScopes := len(memory.WriteScopes) > 0
	memory.ReadScopes, adjustments = applyMemoryScopeGovernance(adjustments, "memory.read_scopes", memory.ReadScopes, policy.MemoryPolicy.Scopes)
	memory.WriteScopes, adjustments = applyMemoryScopeGovernance(adjustments, "memory.write_scopes", memory.WriteScopes, policy.MemoryPolicy.Scopes)
	if requestedReadScopes && len(policy.MemoryPolicy.Scopes) > 0 && len(memory.ReadScopes) == 0 {
		disabled := false
		memory.ReadEnabled = &disabled
		policy.MemoryPolicy.AllowRead = false
	}
	if requestedWriteScopes && len(policy.MemoryPolicy.Scopes) > 0 && len(memory.WriteScopes) == 0 {
		disabled := false
		memory.WriteEnabled = &disabled
		policy.MemoryPolicy.AllowWrite = false
	}
	if len(memory.WriteScopes) > 0 {
		policy.MemoryPolicy.Scopes = append([]string(nil), memory.WriteScopes...)
	}
	return policy, memory, adjustments
}

func applyMemoryScopeGovernance(adjustments []GuardrailAdjustment, path string, requested []string, policyScopes []string) ([]string, []GuardrailAdjustment) {
	if len(policyScopes) == 0 {
		return requested, adjustments
	}
	if len(requested) == 0 {
		return append([]string(nil), policyScopes...), adjustments
	}
	effective := intersectStrings(requested, policyScopes)
	if len(effective) != len(requested) {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      path,
			Reason:    "memory scopes are limited by policy",
			Requested: requested,
			Effective: effective,
		})
	}
	return effective, adjustments
}

func capIntPtr(adjustments []GuardrailAdjustment, path string, requested *int, max int) (*int, []GuardrailAdjustment) {
	if max <= 0 {
		return requested, adjustments
	}
	value := contracts.IntValue(requested)
	if value == 0 || value > max {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      path,
			Reason:    "limited by policy",
			Requested: value,
			Effective: max,
		})
		return contracts.IntPtr(max), adjustments
	}
	return requested, adjustments
}

func capConfiguredIntPtr(adjustments []GuardrailAdjustment, path string, requested *int, max int, zeroMeansUnlimited bool) (*int, []GuardrailAdjustment) {
	if requested == nil || max <= 0 {
		return requested, adjustments
	}
	value := *requested
	if (zeroMeansUnlimited && value == 0) || value > max {
		adjustments = append(adjustments, GuardrailAdjustment{
			Path:      path,
			Reason:    "limited by policy",
			Requested: value,
			Effective: max,
		})
		return contracts.IntPtr(max), adjustments
	}
	return requested, adjustments
}

func compressionUsesLLM(mode string) bool {
	return mode == "llm" || mode == "llm_then_truncate"
}

func strictestRiskThreshold(left contracts.RiskLevel, right contracts.RiskLevel) contracts.RiskLevel {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if riskRank(left) <= riskRank(right) {
		return left
	}
	return right
}

func riskRank(level contracts.RiskLevel) int {
	switch level {
	case contracts.RiskLow:
		return 1
	case contracts.RiskMedium:
		return 2
	case contracts.RiskHigh:
		return 3
	case contracts.RiskCritical:
		return 4
	default:
		return 100
	}
}

func isZeroContextStrategy(strategy contracts.ContextStrategy) bool {
	return reflect.DeepEqual(strategy, contracts.ContextStrategy{})
}

func isZeroCompressionStrategy(strategy contracts.ContextCompressionStrategy) bool {
	return reflect.DeepEqual(strategy, contracts.ContextCompressionStrategy{})
}

func contains(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func intersectStrings(left []string, right []string) []string {
	allowed := map[string]struct{}{}
	for _, value := range right {
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(left))
	seen := map[string]struct{}{}
	for _, value := range left {
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
