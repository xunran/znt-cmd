package contracts

func IntPtr(value int) *int {
	return &value
}

func IntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func BoolPtr(value bool) *bool {
	return &value
}

type AgentStrategies struct {
	Prompt        PromptStrategy        `json:"prompt,omitempty"`
	Model         ModelStrategy         `json:"model,omitempty"`
	Context       ContextStrategy       `json:"context,omitempty"`
	Tools         ToolUseStrategy       `json:"tools,omitempty"`
	Skills        SkillUseStrategy      `json:"skills,omitempty"`
	Collaboration CollaborationStrategy `json:"collaboration,omitempty"`
	Memory        MemoryUseStrategy     `json:"memory,omitempty"`
	Knowledge     KnowledgeUseStrategy  `json:"knowledge,omitempty"`
	Runtime       RuntimeStrategy       `json:"runtime,omitempty"`
	Repair        RepairStrategy        `json:"repair,omitempty"`
	Output        OutputStrategy        `json:"output,omitempty"`
}

type PromptStrategy struct {
	SystemPrompt    string `json:"system_prompt,omitempty"`
	DeveloperPrompt string `json:"developer_prompt,omitempty"`
	IdentityPrompt  string `json:"identity_prompt,omitempty"`
}

type ModelStrategy struct {
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	Thinking        string   `json:"thinking,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	TimeoutMS       int      `json:"timeout_ms,omitempty"`
	Streaming       *bool    `json:"streaming,omitempty"`
}

type ContextStrategy struct {
	Mode string `json:"mode,omitempty"`

	RecentMessageLimit  *int `json:"recent_message_limit,omitempty"`
	RetrievalMaxResults *int `json:"retrieval_max_results,omitempty"`
	TaskHistoryMaxItems *int `json:"task_history_max_items,omitempty"`
	MemoryMaxItems      *int `json:"memory_max_items,omitempty"`
	ArtifactRefMaxItems *int `json:"artifact_ref_max_items,omitempty"`
	ToolResultMaxItems  *int `json:"tool_result_max_items,omitempty"`
	ContextTokenBudget  *int `json:"context_token_budget,omitempty"`

	EnabledSources []string       `json:"enabled_sources,omitempty"`
	SourceBudgets  map[string]int `json:"source_budgets,omitempty"`

	Compression ContextCompressionStrategy `json:"compression,omitempty"`
}

type ContextCompressionStrategy struct {
	Enabled bool `json:"enabled"`

	TriggerRatio int `json:"trigger_ratio,omitempty"`
	TargetTokens int `json:"target_tokens,omitempty"`

	Mode string `json:"mode,omitempty"`

	FailureMode string `json:"failure_mode,omitempty"` // empty/continue, reject

	ModelProvider string   `json:"model_provider,omitempty"`
	ModelBaseURL  string   `json:"model_base_url,omitempty"`
	ModelName     string   `json:"model_name,omitempty"`
	MaxTokens     int      `json:"max_tokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`

	PromptProfileID string                    `json:"prompt_profile_id,omitempty"`
	InlinePrompt    *CompressionPromptProfile `json:"inline_prompt,omitempty"`

	Preserve []string `json:"preserve,omitempty"`
	Forbid   []string `json:"forbid,omitempty"`

	WriteSummaryToMemory bool `json:"write_summary_to_memory,omitempty"`
}

type CompressionPromptProfile struct {
	ProfileID string `json:"profile_id"`
	Version   string `json:"version,omitempty"`
	Name      string `json:"name,omitempty"`

	SystemPrompt    string `json:"system_prompt"`
	DeveloperPrompt string `json:"developer_prompt,omitempty"`

	OutputSchema map[string]any `json:"output_schema,omitempty"`

	Preserve []string `json:"preserve,omitempty"`
	Forbid   []string `json:"forbid,omitempty"`
}

type ContextAssemblyReport struct {
	StrategyHash string `json:"strategy_hash,omitempty"`
	Mode         string `json:"mode,omitempty"`

	Sources []ContextSourceReport `json:"sources,omitempty"`

	TokenBudget        int `json:"token_budget,omitempty"`
	EstimatedTokensIn  int `json:"estimated_tokens_in,omitempty"`
	EstimatedTokensOut int `json:"estimated_tokens_out,omitempty"`

	Compression *ContextCompressionReport `json:"compression,omitempty"`
}

type ContextSourceReport struct {
	SourceType string `json:"source_type"`
	SourceRef  string `json:"source_ref,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	HookID     string `json:"hook_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	TrustLevel string `json:"trust_level,omitempty"`

	CandidateCount int    `json:"candidate_count"`
	SelectedCount  int    `json:"selected_count"`
	DroppedCount   int    `json:"dropped_count"`
	Limit          int    `json:"limit,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type ContextCompressionReport struct {
	Applied         bool   `json:"applied"`
	Mode            string `json:"mode,omitempty"`
	ModelProvider   string `json:"model_provider,omitempty"`
	ModelName       string `json:"model_name,omitempty"`
	PromptProfileID string `json:"prompt_profile_id,omitempty"`
	InputTokens     int    `json:"input_tokens,omitempty"`
	OutputTokens    int    `json:"output_tokens,omitempty"`
	SummaryHash     string `json:"summary_hash,omitempty"`
	FailureReason   string   `json:"failure_reason,omitempty"`
	SourceRefs      []string `json:"source_refs,omitempty"`
}

type CompressedContext struct {
	Summary      string   `json:"summary"`
	SourceRefs   []string `json:"source_refs,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
}

type ToolUseStrategy struct {
	AllowedToolIDs             []string  `json:"allowed_tool_ids,omitempty"`
	AllowedToolGroupIDs        []string  `json:"allowed_tool_group_ids,omitempty"`
	DeniedToolIDs              []string  `json:"denied_tool_ids,omitempty"`
	DeniedToolGroupIDs         []string  `json:"denied_tool_group_ids,omitempty"`
	PreferredToolIDs           []string  `json:"preferred_tool_ids,omitempty"`
	ToolChoiceMode             string    `json:"tool_choice_mode,omitempty"`
	MaxToolCalls               *int      `json:"max_tool_calls,omitempty"`
	RequireApprovalAtRiskLevel RiskLevel `json:"require_approval_at_risk_level,omitempty"`
}

type SkillUseStrategy struct {
	EnabledSkillIDs  []string `json:"enabled_skill_ids,omitempty"`
	DisabledSkillIDs []string `json:"disabled_skill_ids,omitempty"`
	SelectionMode    string   `json:"selection_mode,omitempty"`
	MaxSelectedSkills int      `json:"max_selected_skills,omitempty"`
}

type CollaborationStrategy struct {
	DelegationMode     string      `json:"delegation_mode,omitempty"`
	MaxHandoffDepth    *int        `json:"max_handoff_depth,omitempty"`
	MaxChildTasks      *int        `json:"max_child_tasks,omitempty"`
	DefaultHandoffMode HandoffMode `json:"default_handoff_mode,omitempty"`
	MaxContextTokens   *int        `json:"max_context_tokens,omitempty"`
}

type MemoryUseStrategy struct {
	ReadEnabled          *bool    `json:"read_enabled,omitempty"`
	WriteEnabled         *bool    `json:"write_enabled,omitempty"`
	ReadScopes           []string `json:"read_scopes,omitempty"`
	WriteScopes          []string `json:"write_scopes,omitempty"`
	MaxMemoryItems       *int     `json:"max_memory_items,omitempty"`
	AutoWriteMode        string   `json:"auto_write_mode,omitempty"`
	WritePromptProfileID string   `json:"write_prompt_profile_id,omitempty"`
}

type KnowledgeUseStrategy struct {
	Enabled          *bool             `json:"enabled,omitempty"`
	KnowledgeBaseIDs []KnowledgeBaseID `json:"knowledge_base_ids,omitempty"`
	SearchMode       string            `json:"search_mode,omitempty"`
	MaxResults       int               `json:"max_results,omitempty"`
	AllowCrossGroup  bool              `json:"allow_cross_group,omitempty"`
	InjectMode       string            `json:"inject_mode,omitempty"`
}

type RuntimeStrategy struct {
	MaxSteps                   *int   `json:"max_steps,omitempty"`
	MaxDurationSeconds         *int   `json:"max_duration_seconds,omitempty"`
	MaxModelRetries            *int   `json:"max_model_retries,omitempty"`
	MaxConsecutiveToolFailures *int   `json:"max_consecutive_tool_failures,omitempty"`
	ExecutionMode              string `json:"execution_mode,omitempty"`
}

type RepairStrategy struct {
	Enabled                  *bool    `json:"enabled,omitempty"`
	MaxRepairAttempts        *int     `json:"max_repair_attempts,omitempty"`
	RepairableErrorCodes     []string `json:"repairable_error_codes,omitempty"`
	RequestModelRepairOnFail *bool    `json:"request_model_repair_on_fail,omitempty"`
	StopOnDenied             *bool    `json:"stop_on_denied,omitempty"`
	FailureMode              string   `json:"failure_mode,omitempty"`
}

type OutputStrategy struct {
	OutputMode   string         `json:"output_mode,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	StrictJSON   bool           `json:"strict_json,omitempty"`
}

type ContextGovernancePolicy struct {
	MaxContextTokenBudget int `json:"max_context_token_budget,omitempty"`
	MaxRecentMessageLimit int `json:"max_recent_message_limit,omitempty"`
	MaxRetrievalResults   int `json:"max_retrieval_results,omitempty"`
	MaxTaskHistoryItems   int `json:"max_task_history_items,omitempty"`
	MaxMemoryItems        int `json:"max_memory_items,omitempty"`
	MaxArtifactRefItems   int `json:"max_artifact_ref_items,omitempty"`
	MaxToolResultItems    int `json:"max_tool_result_items,omitempty"`

	AllowFullDebugMode         bool     `json:"allow_full_debug_mode,omitempty"`
	AllowLLMCompression        bool     `json:"allow_llm_compression"`
	AllowedCompressionModels   []string `json:"allowed_compression_models,omitempty"`
	AllowedCompressionBaseURLs []string `json:"allowed_compression_base_urls,omitempty"`
}
