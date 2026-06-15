package strategy

import (
	"testing"

	"znt/internal/contracts"
)

func TestResolveDefaultsContextStrategy(t *testing.T) {
	effective, report, err := Resolve(contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
	}, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Mode != "balanced" || contracts.IntValue(effective.Context.RecentMessageLimit) != 20 || contracts.IntValue(effective.Context.RetrievalMaxResults) != 8 {
		t.Fatalf("expected default context strategy, got %#v", effective.Context)
	}
	if effective.Model.Streaming == nil || !*effective.Model.Streaming {
		t.Fatalf("expected default model streaming to be enabled, got %#v", effective.Model.Streaming)
	}
	if report.StrategyHash == "" {
		t.Fatal("expected strategy hash")
	}
}

func TestResolveAppliesContextGovernance(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode:                "full_debug",
				RecentMessageLimit:  contracts.IntPtr(100),
				RetrievalMaxResults: contracts.IntPtr(50),
				TaskHistoryMaxItems: contracts.IntPtr(0),
				MemoryMaxItems:      contracts.IntPtr(100),
				ArtifactRefMaxItems: contracts.IntPtr(100),
				ToolResultMaxItems:  contracts.IntPtr(100),
				ContextTokenBudget:  contracts.IntPtr(20000),
				Compression: contracts.ContextCompressionStrategy{
					Enabled:   true,
					Mode:      "llm",
					ModelName: "large-compressor",
				},
			},
		},
	}
	policy := contracts.PolicySet{
		PolicySetID: "policy_default",
		Version:     "v1",
		ContextGovernancePolicy: contracts.ContextGovernancePolicy{
			MaxContextTokenBudget: 8000,
			MaxRecentMessageLimit: 20,
			MaxRetrievalResults:   8,
			MaxTaskHistoryItems:   30,
			MaxMemoryItems:        12,
			MaxArtifactRefItems:   6,
			MaxToolResultItems:    4,
			AllowFullDebugMode:    false,
			AllowLLMCompression:   false,
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Mode != "balanced" {
		t.Fatalf("expected full_debug to be downgraded, got %#v", effective.Context.Mode)
	}
	if contracts.IntValue(effective.Context.RecentMessageLimit) != 20 || contracts.IntValue(effective.Context.RetrievalMaxResults) != 8 || contracts.IntValue(effective.Context.TaskHistoryMaxItems) != 30 || contracts.IntValue(effective.Context.ContextTokenBudget) != 8000 {
		t.Fatalf("expected policy caps, got %#v", effective.Context)
	}
	if contracts.IntValue(effective.Context.MemoryMaxItems) != 12 || contracts.IntValue(effective.Context.ArtifactRefMaxItems) != 6 || contracts.IntValue(effective.Context.ToolResultMaxItems) != 4 {
		t.Fatalf("expected source item policy caps, got %#v", effective.Context)
	}
	if effective.Context.Compression.Mode != "truncate" || effective.Context.Compression.ModelName != "" {
		t.Fatalf("expected LLM compression to be downgraded, got %#v", effective.Context.Compression)
	}
	if len(report.Adjustments) < 8 {
		t.Fatalf("expected guardrail adjustments, got %#v", report.Adjustments)
	}
}

func TestResolveEmptyContextGovernanceDoesNotDowngradeLLMCompression(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled: true,
					Mode:    "llm",
				},
			},
		},
	}
	effective, report, err := Resolve(agent, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Compression.Mode != "llm" || !effective.Context.Compression.Enabled {
		t.Fatalf("expected empty governance policy not to downgrade LLM compression, got %#v", effective.Context.Compression)
	}
	if len(report.Adjustments) != 0 {
		t.Fatalf("expected no guardrail adjustments, got %#v", report.Adjustments)
	}
}

func TestResolveAppliesCompressionModelAllowlistWhenModelOmitted(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled: true,
					Mode:    "llm",
				},
			},
		},
	}
	policy := contracts.PolicySet{
		ContextGovernancePolicy: contracts.ContextGovernancePolicy{
			AllowLLMCompression:      true,
			AllowedCompressionModels: []string{"small-compressor"},
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Compression.ModelName != "small-compressor" {
		t.Fatalf("expected policy allowlist to select compression model, got %#v", effective.Context.Compression)
	}
	if len(report.Adjustments) == 0 || report.Adjustments[0].Path != "context.compression.model_name" {
		t.Fatalf("expected compression model guardrail adjustment, got %#v", report.Adjustments)
	}
}

func TestResolveAppliesCompressionBaseURLAllowlist(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled:      true,
					Mode:         "llm",
					ModelName:    "small-compressor",
					ModelBaseURL: "https://unapproved-compressor.example.test/v1",
				},
			},
		},
	}
	policy := contracts.PolicySet{
		ContextGovernancePolicy: contracts.ContextGovernancePolicy{
			AllowLLMCompression:      true,
			AllowedCompressionModels: []string{"small-compressor"},
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Compression.ModelBaseURL != "" {
		t.Fatalf("expected unapproved compression base URL to be cleared, got %#v", effective.Context.Compression)
	}
	if !hasAdjustment(report.Adjustments, "context.compression.model_base_url") {
		t.Fatalf("expected compression base URL guardrail adjustment, got %#v", report.Adjustments)
	}

	allowedPolicy := policy
	allowedPolicy.ContextGovernancePolicy.AllowedCompressionBaseURLs = []string{"https://approved-compressor.example.test/v1"}
	agent.Strategies.Context.Compression.ModelBaseURL = "https://approved-compressor.example.test/v1"
	effective, report, err = Resolve(agent, allowedPolicy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Compression.ModelBaseURL != "https://approved-compressor.example.test/v1" {
		t.Fatalf("expected approved compression base URL to be preserved, got %#v", effective.Context.Compression)
	}
	if hasAdjustment(report.Adjustments, "context.compression.model_base_url") {
		t.Fatalf("did not expect compression base URL guardrail adjustment, got %#v", report.Adjustments)
	}
}

func TestResolveStrategyHashIncludesCreatorStrategyFamilies(t *testing.T) {
	base := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
	}
	_, baseReport, err := Resolve(base, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	withStrategies := base
	withStrategies.Strategies.Knowledge = contracts.KnowledgeUseStrategy{Enabled: &enabled, InjectMode: "tool_only"}
	withStrategies.Strategies.Output = contracts.OutputStrategy{OutputMode: "decision_json", StrictJSON: true}
	_, strategyReport, err := Resolve(withStrategies, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if baseReport.StrategyHash == "" || strategyReport.StrategyHash == "" || baseReport.StrategyHash == strategyReport.StrategyHash {
		t.Fatalf("expected creator strategy families to affect strategy hash, base=%q strategy=%q", baseReport.StrategyHash, strategyReport.StrategyHash)
	}
}

func TestResolveMergesPartialContextStrategyWithDefaults(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Compression: contracts.ContextCompressionStrategy{
					Enabled: true,
					Mode:    "llm",
				},
			},
		},
	}
	effective, _, err := Resolve(agent, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Mode != "balanced" || contracts.IntValue(effective.Context.RecentMessageLimit) != 20 || contracts.IntValue(effective.Context.RetrievalMaxResults) != 8 || contracts.IntValue(effective.Context.TaskHistoryMaxItems) != 30 {
		t.Fatalf("expected partial context strategy to inherit defaults, got %#v", effective.Context)
	}
	if effective.Context.Compression.Mode != "llm" || !effective.Context.Compression.Enabled {
		t.Fatalf("expected explicit compression mode to be kept, got %#v", effective.Context.Compression)
	}
}

func TestResolveKeepsExplicitUnlimitedContextLimits(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode:                "balanced",
				RecentMessageLimit:  contracts.IntPtr(0),
				RetrievalMaxResults: contracts.IntPtr(0),
				ContextTokenBudget:  contracts.IntPtr(0),
			},
		},
	}
	effective, _, err := Resolve(agent, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if contracts.IntValue(effective.Context.RecentMessageLimit) != 0 || contracts.IntValue(effective.Context.RetrievalMaxResults) != 0 || contracts.IntValue(effective.Context.ContextTokenBudget) != 0 {
		t.Fatalf("expected explicit zero limits to remain unlimited, got %#v", effective.Context)
	}
	if contracts.IntValue(effective.Context.TaskHistoryMaxItems) != 30 {
		t.Fatalf("expected omitted limits to inherit defaults, got %#v", effective.Context)
	}
}

func TestResolveFullDebugKeepsUnlimitedContextLimits(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Context: contracts.ContextStrategy{
				Mode: "full_debug",
			},
		},
	}
	effective, _, err := Resolve(agent, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Context.Mode != "full_debug" || contracts.IntValue(effective.Context.RecentMessageLimit) != 0 || contracts.IntValue(effective.Context.RetrievalMaxResults) != 0 || contracts.IntValue(effective.Context.TaskHistoryMaxItems) != 0 {
		t.Fatalf("expected full_debug to keep unlimited limits, got %#v", effective.Context)
	}
}

func TestResolveAppliesRuntimeToolAndRepairGovernance(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Tools: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo", "crm.lookup"},
		},
		Runtime: contracts.RuntimeLimits{
			MaxSteps:                   12,
			MaxToolCalls:               9,
			MaxModelRetries:            4,
			MaxRepairAttempts:          3,
			MaxConsecutiveToolFailures: 5,
		},
		Strategies: contracts.AgentStrategies{
			Tools: contracts.ToolUseStrategy{
				RequireApprovalAtRiskLevel: contracts.RiskHigh,
			},
		},
	}
	policy := contracts.PolicySet{
		PolicySetID: "policy_default",
		Version:     "v1",
		RuntimePolicy: contracts.RuntimePolicy{
			MaxSteps:                   4,
			MaxToolCalls:               2,
			MaxModelRetries:            1,
			MaxRepairAttempts:          1,
			MaxConsecutiveToolFailures: 2,
		},
		ToolPolicy: contracts.ToolPolicy{
			RequireApprovalAtRiskLevel: contracts.RiskMedium,
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if contracts.IntValue(effective.Runtime.MaxSteps) != 4 || contracts.IntValue(effective.Tools.MaxToolCalls) != 2 {
		t.Fatalf("expected runtime and tool call caps, got runtime=%#v tools=%#v", effective.Runtime, effective.Tools)
	}
	if contracts.IntValue(effective.Runtime.MaxModelRetries) != 1 || contracts.IntValue(effective.Repair.MaxRepairAttempts) != 1 || contracts.IntValue(effective.Runtime.MaxConsecutiveToolFailures) != 2 {
		t.Fatalf("expected retry and repair caps, got runtime=%#v repair=%#v", effective.Runtime, effective.Repair)
	}
	if effective.Tools.RequireApprovalAtRiskLevel != contracts.RiskMedium || effective.Policy.ToolPolicy.RequireApprovalAtRiskLevel != contracts.RiskMedium {
		t.Fatalf("expected stricter approval threshold, got tools=%#v policy=%#v", effective.Tools, effective.Policy.ToolPolicy)
	}
	if len(report.Adjustments) < 5 {
		t.Fatalf("expected runtime guardrail adjustments, got %#v", report.Adjustments)
	}
}

func TestResolveKeepsExplicitUnlimitedToolCallsWithoutPolicyCap(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Runtime: contracts.RuntimeLimits{
			MaxSteps:     4,
			MaxToolCalls: 2,
		},
		Strategies: contracts.AgentStrategies{
			Tools: contracts.ToolUseStrategy{
				MaxToolCalls: contracts.IntPtr(0),
			},
		},
	}
	effective, report, err := Resolve(agent, contracts.PolicySet{}, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Tools.MaxToolCalls == nil || contracts.IntValue(effective.Tools.MaxToolCalls) != 0 {
		t.Fatalf("expected explicit unlimited max_tool_calls, got %#v", effective.Tools)
	}
	if len(report.Adjustments) != 0 {
		t.Fatalf("expected no adjustments, got %#v", report.Adjustments)
	}
}

func TestResolveDoesNotRaiseZeroRetriesToPolicyCap(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Runtime: contracts.RuntimeLimits{
			MaxSteps:          4,
			MaxToolCalls:      2,
			MaxModelRetries:   1,
			MaxRepairAttempts: 1,
		},
		Strategies: contracts.AgentStrategies{
			Tools: contracts.ToolUseStrategy{
				MaxToolCalls: contracts.IntPtr(0),
			},
			Runtime: contracts.RuntimeStrategy{
				MaxModelRetries: contracts.IntPtr(0),
			},
			Repair: contracts.RepairStrategy{
				MaxRepairAttempts: contracts.IntPtr(0),
			},
		},
	}
	policy := contracts.PolicySet{
		RuntimePolicy: contracts.RuntimePolicy{
			MaxToolCalls:      3,
			MaxModelRetries:   1,
			MaxRepairAttempts: 1,
		},
	}
	effective, _, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if contracts.IntValue(effective.Tools.MaxToolCalls) != 3 {
		t.Fatalf("expected unlimited tool calls to be capped by policy, got %#v", effective.Tools)
	}
	if contracts.IntValue(effective.Runtime.MaxModelRetries) != 0 || contracts.IntValue(effective.Repair.MaxRepairAttempts) != 0 {
		t.Fatalf("expected zero retries/repairs to remain zero, got runtime=%#v repair=%#v", effective.Runtime, effective.Repair)
	}
}

func TestResolveAppliesRepairStrategyToToolRepairPolicy(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Runtime: contracts.RuntimeLimits{
			MaxSteps:          4,
			MaxToolCalls:      2,
			MaxRepairAttempts: 1,
		},
		Strategies: contracts.AgentStrategies{
			Repair: contracts.RepairStrategy{
				Enabled:                  contracts.BoolPtr(false),
				RequestModelRepairOnFail: contracts.BoolPtr(false),
				RepairableErrorCodes:     []string{string(contracts.CodeToolArgumentInvalid)},
			},
		},
	}
	policy := contracts.PolicySet{
		ToolRepairPolicy: contracts.ToolRepairPolicy{
			Enabled:                  true,
			MaxRepairAttempts:        2,
			RepairableErrorCodes:     []string{string(contracts.CodeToolArgumentInvalid), string(contracts.CodeToolExecutionFailed)},
			RequestModelRepairOnFail: true,
		},
	}
	effective, _, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Policy.ToolRepairPolicy.Enabled || effective.Policy.ToolRepairPolicy.RequestModelRepairOnFail {
		t.Fatalf("expected repair policy to be disabled by agent strategy, got %#v", effective.Policy.ToolRepairPolicy)
	}
	if contracts.IntValue(effective.Repair.MaxRepairAttempts) != 0 || effective.Policy.ToolRepairPolicy.MaxRepairAttempts != 0 {
		t.Fatalf("expected repair attempts to be zero, got repair=%#v policy=%#v", effective.Repair, effective.Policy.ToolRepairPolicy)
	}
	if len(effective.Policy.ToolRepairPolicy.RepairableErrorCodes) != 1 || effective.Policy.ToolRepairPolicy.RepairableErrorCodes[0] != string(contracts.CodeToolArgumentInvalid) {
		t.Fatalf("expected repairable codes to be narrowed, got %#v", effective.Policy.ToolRepairPolicy.RepairableErrorCodes)
	}
}

func TestResolveRepairableCodesCannotExceedPolicy(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Repair: contracts.RepairStrategy{
				RepairableErrorCodes: []string{string(contracts.CodeToolArgumentInvalid), string(contracts.CodeModelError)},
			},
		},
	}
	policy := contracts.PolicySet{
		ToolRepairPolicy: contracts.ToolRepairPolicy{
			Enabled:              true,
			RepairableErrorCodes: []string{string(contracts.CodeToolArgumentInvalid)},
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Repair.RepairableErrorCodes) != 1 || effective.Repair.RepairableErrorCodes[0] != string(contracts.CodeToolArgumentInvalid) {
		t.Fatalf("expected policy-limited repairable codes, got %#v", effective.Repair.RepairableErrorCodes)
	}
	if len(report.Adjustments) == 0 {
		t.Fatalf("expected adjustment for repairable codes, got %#v", report.Adjustments)
	}
}

func TestResolveAppliesCollaborationGovernance(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Runtime: contracts.RuntimeLimits{
			MaxHandoffDepth: 5,
			MaxChildTasks:   3,
		},
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				DefaultHandoffMode: contracts.HandoffFullContext,
				MaxContextTokens:   contracts.IntPtr(0),
			},
		},
	}
	policy := contracts.PolicySet{
		HandoffPolicy: contracts.HandoffPolicy{
			DefaultMode:      contracts.HandoffSummaryOnly,
			AllowFullContext: false,
			MaxContextTokens: 1000,
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Collaboration.DelegationMode != "auto" {
		t.Fatalf("expected default delegation mode, got %#v", effective.Collaboration)
	}
	if contracts.IntValue(effective.Collaboration.MaxHandoffDepth) != 5 || contracts.IntValue(effective.Collaboration.MaxChildTasks) != 3 {
		t.Fatalf("expected collaboration limits from runtime, got %#v", effective.Collaboration)
	}
	if effective.Collaboration.DefaultHandoffMode != contracts.HandoffSummaryOnly || effective.Policy.HandoffPolicy.DefaultMode != contracts.HandoffSummaryOnly {
		t.Fatalf("expected full-context handoff mode to be downgraded, got collaboration=%#v policy=%#v", effective.Collaboration, effective.Policy.HandoffPolicy)
	}
	if contracts.IntValue(effective.Collaboration.MaxContextTokens) != 1000 || effective.Policy.HandoffPolicy.MaxContextTokens != 1000 {
		t.Fatalf("expected handoff context tokens to be capped, got collaboration=%#v policy=%#v", effective.Collaboration, effective.Policy.HandoffPolicy)
	}
	if len(report.Adjustments) < 2 {
		t.Fatalf("expected collaboration guardrail adjustments, got %#v", report.Adjustments)
	}
}

func TestResolveNarrowsHandoffContextTokensByCollaborationStrategy(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				MaxContextTokens: contracts.IntPtr(500),
			},
		},
	}
	policy := contracts.PolicySet{
		HandoffPolicy: contracts.HandoffPolicy{
			MaxContextTokens: 2000,
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if contracts.IntValue(effective.Collaboration.MaxContextTokens) != 500 || effective.Policy.HandoffPolicy.MaxContextTokens != 500 {
		t.Fatalf("expected collaboration strategy to narrow handoff context tokens, got collaboration=%#v policy=%#v", effective.Collaboration, effective.Policy.HandoffPolicy)
	}
	if len(report.Adjustments) != 0 {
		t.Fatalf("expected no guardrail adjustment for narrower creator limit, got %#v", report.Adjustments)
	}
}

func TestResolveAppliesMemoryGovernance(t *testing.T) {
	agent := contracts.AgentDefinition{
		AgentID: "agent_1",
		Version: "v1",
		Strategies: contracts.AgentStrategies{
			Memory: contracts.MemoryUseStrategy{
				ReadEnabled:    contracts.BoolPtr(true),
				WriteEnabled:   contracts.BoolPtr(true),
				ReadScopes:     []string{"user", "agent"},
				WriteScopes:    []string{"user", "agent"},
				MaxMemoryItems: contracts.IntPtr(5),
				AutoWriteMode:  "explicit_intent",
			},
		},
	}
	policy := contracts.PolicySet{
		MemoryPolicy: contracts.MemoryPolicy{
			AllowRead:  false,
			AllowWrite: false,
			Scopes:     []string{"user"},
		},
	}
	effective, report, err := Resolve(agent, policy, Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.Memory.ReadEnabled == nil || *effective.Memory.ReadEnabled || effective.Policy.MemoryPolicy.AllowRead {
		t.Fatalf("expected memory read to be disabled by policy, got memory=%#v policy=%#v", effective.Memory, effective.Policy.MemoryPolicy)
	}
	if effective.Memory.WriteEnabled == nil || *effective.Memory.WriteEnabled || effective.Policy.MemoryPolicy.AllowWrite {
		t.Fatalf("expected memory write to be disabled by policy, got memory=%#v policy=%#v", effective.Memory, effective.Policy.MemoryPolicy)
	}
	if len(effective.Memory.ReadScopes) != 1 || effective.Memory.ReadScopes[0] != "user" || len(effective.Memory.WriteScopes) != 1 || effective.Memory.WriteScopes[0] != "user" {
		t.Fatalf("expected memory scopes to be limited by policy, got %#v", effective.Memory)
	}
	if contracts.IntValue(effective.Memory.MaxMemoryItems) != 5 {
		t.Fatalf("expected memory max items to be preserved, got %#v", effective.Memory)
	}
	if len(report.Adjustments) < 4 {
		t.Fatalf("expected memory guardrail adjustments, got %#v", report.Adjustments)
	}
}

func hasAdjustment(adjustments []GuardrailAdjustment, path string) bool {
	for _, adjustment := range adjustments {
		if adjustment.Path == path {
			return true
		}
	}
	return false
}
