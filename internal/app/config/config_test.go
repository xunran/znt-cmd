package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileCanOverrideDefaultTrueBooleanToFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"service_name":"clean-core","version":"test","http_addr":":0","readiness":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Readiness {
		t.Fatalf("expected readiness=false from file override")
	}
}

func TestProductionConfigRequiresPersistentAuthAndModelSettings(t *testing.T) {
	cfg := Config{
		ServiceName:  "clean-core",
		Version:      "test",
		Env:          "production",
		HTTPAddr:     ":0",
		ModelBaseURL: "https://models.example.test",
		ModelName:    "model",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production config to require database_url and service_token")
	}
	cfg.DatabaseURL = "postgres://example"
	cfg.ServiceToken = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected complete production config to validate: %v", err)
	}
}

func TestLoadModelRequestOptionsFromEnv(t *testing.T) {
	t.Setenv("CLEAN_CORE_MODEL_MAX_TOKENS", "2048")
	t.Setenv("CLEAN_CORE_MODEL_TEMPERATURE", "0")
	t.Setenv("CLEAN_CORE_MODEL_THINKING", "enabled")
	t.Setenv("CLEAN_CORE_MODEL_REASONING_EFFORT", "low")
	t.Setenv("CLEAN_CORE_CONVERSATION_JUDGE_MODE", "hybrid")
	t.Setenv("CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS", "1500")
	t.Setenv("CLEAN_CORE_CONVERSATION_DIRECT_ENABLED", "true")
	t.Setenv("CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED", "false")
	t.Setenv("CLEAN_CORE_EXTERNAL_BRIDGE_PROVIDER", "a2a")
	t.Setenv("CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL", "https://bridge.example.test")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelMaxTokens != 2048 {
		t.Fatalf("expected model_max_tokens from env, got %d", cfg.ModelMaxTokens)
	}
	if cfg.ModelTemperature == nil || *cfg.ModelTemperature != 0 {
		t.Fatalf("expected model_temperature=0 from env, got %#v", cfg.ModelTemperature)
	}
	if cfg.ModelThinking != "enabled" || cfg.ModelReasoningEffort != "low" {
		t.Fatalf("expected model options from env, got %#v", cfg)
	}
	if cfg.ConversationJudgeMode != "hybrid" {
		t.Fatalf("expected conversation judge mode from env, got %#v", cfg.ConversationJudgeMode)
	}
	if cfg.ConversationJudgeTimeoutMS != 1500 || !cfg.ConversationDirectEnabled || cfg.ConversationRetrievalIsEnabled() {
		t.Fatalf("expected conversation runtime options from env, got %#v", cfg)
	}
	if cfg.ExternalBridgeProvider != "a2a" || cfg.ExternalBridgeBaseURL != "https://bridge.example.test" {
		t.Fatalf("expected external bridge options from env, got %#v", cfg)
	}
}

func TestContextDefaultStrategyUsesServiceDefaults(t *testing.T) {
	strategy := Default().ContextDefaultStrategy()
	if strategy.Mode != "balanced" {
		t.Fatalf("expected balanced context mode, got %q", strategy.Mode)
	}
	if strategy.RecentMessageLimit == nil || *strategy.RecentMessageLimit != 20 {
		t.Fatalf("expected recent message default 20, got %#v", strategy.RecentMessageLimit)
	}
	if strategy.RetrievalMaxResults == nil || *strategy.RetrievalMaxResults != 8 {
		t.Fatalf("expected retrieval default 8, got %#v", strategy.RetrievalMaxResults)
	}
	if strategy.TaskHistoryMaxItems == nil || *strategy.TaskHistoryMaxItems != 30 {
		t.Fatalf("expected task history default 30, got %#v", strategy.TaskHistoryMaxItems)
	}
	if strategy.ContextTokenBudget == nil || *strategy.ContextTokenBudget != 4000 {
		t.Fatalf("expected token budget default 4000, got %#v", strategy.ContextTokenBudget)
	}
	if !strategy.Compression.Enabled || strategy.Compression.Mode != "truncate" {
		t.Fatalf("expected truncate compression default, got %#v", strategy.Compression)
	}
}

func TestLoadContextDefaultsFromEnv(t *testing.T) {
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_MODE", "long_context")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RECENT_MESSAGE_LIMIT", "42")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RETRIEVAL_MAX_RESULTS", "12")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TASK_HISTORY_MAX_ITEMS", "64")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TOKEN_BUDGET", "9000")
	t.Setenv("CLEAN_CORE_CONTEXT_COMPRESSION_DEFAULT_ENABLED", "false")
	t.Setenv("CLEAN_CORE_CONTEXT_COMPRESSION_DEFAULT_MODE", "none")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	strategy := cfg.ContextDefaultStrategy()
	if strategy.Mode != "long_context" {
		t.Fatalf("expected env context mode, got %q", strategy.Mode)
	}
	if strategy.RecentMessageLimit == nil || *strategy.RecentMessageLimit != 42 {
		t.Fatalf("expected env recent limit, got %#v", strategy.RecentMessageLimit)
	}
	if strategy.RetrievalMaxResults == nil || *strategy.RetrievalMaxResults != 12 {
		t.Fatalf("expected env retrieval limit, got %#v", strategy.RetrievalMaxResults)
	}
	if strategy.TaskHistoryMaxItems == nil || *strategy.TaskHistoryMaxItems != 64 {
		t.Fatalf("expected env task history limit, got %#v", strategy.TaskHistoryMaxItems)
	}
	if strategy.ContextTokenBudget == nil || *strategy.ContextTokenBudget != 9000 {
		t.Fatalf("expected env token budget, got %#v", strategy.ContextTokenBudget)
	}
	if strategy.Compression.Enabled || strategy.Compression.Mode != "none" {
		t.Fatalf("expected env compression none, got %#v", strategy.Compression)
	}
}

func TestLoadContextDefaultExplicitZeroFromJSONMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLEAN_CORE_ENV_FILE", filepath.Join(dir, "missing.env"))
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RECENT_MESSAGE_LIMIT", "")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RETRIEVAL_MAX_RESULTS", "")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TASK_HISTORY_MAX_ITEMS", "")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TOKEN_BUDGET", "")
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{
		"context_default_recent_message_limit": 0,
		"context_default_retrieval_max_results": 0,
		"context_default_task_history_max_items": 0,
		"context_default_token_budget": 0
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	strategy := cfg.ContextDefaultStrategy()
	if strategy.RecentMessageLimit == nil || *strategy.RecentMessageLimit != 0 {
		t.Fatalf("expected explicit zero recent limit to mean unlimited, got %#v", strategy.RecentMessageLimit)
	}
	if strategy.RetrievalMaxResults == nil || *strategy.RetrievalMaxResults != 0 {
		t.Fatalf("expected explicit zero retrieval limit to mean unlimited, got %#v", strategy.RetrievalMaxResults)
	}
	if strategy.TaskHistoryMaxItems == nil || *strategy.TaskHistoryMaxItems != 0 {
		t.Fatalf("expected explicit zero task history limit to mean unlimited, got %#v", strategy.TaskHistoryMaxItems)
	}
	if strategy.ContextTokenBudget == nil || *strategy.ContextTokenBudget != 0 {
		t.Fatalf("expected explicit zero token budget to mean unlimited, got %#v", strategy.ContextTokenBudget)
	}
}

func TestLoadContextDefaultExplicitZeroFromEnvMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLEAN_CORE_ENV_FILE", filepath.Join(dir, "missing.env"))
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RECENT_MESSAGE_LIMIT", "0")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_RETRIEVAL_MAX_RESULTS", "0")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TASK_HISTORY_MAX_ITEMS", "0")
	t.Setenv("CLEAN_CORE_CONTEXT_DEFAULT_TOKEN_BUDGET", "0")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	strategy := cfg.ContextDefaultStrategy()
	if strategy.RecentMessageLimit == nil || *strategy.RecentMessageLimit != 0 {
		t.Fatalf("expected env zero recent limit to mean unlimited, got %#v", strategy.RecentMessageLimit)
	}
	if strategy.RetrievalMaxResults == nil || *strategy.RetrievalMaxResults != 0 {
		t.Fatalf("expected env zero retrieval limit to mean unlimited, got %#v", strategy.RetrievalMaxResults)
	}
	if strategy.TaskHistoryMaxItems == nil || *strategy.TaskHistoryMaxItems != 0 {
		t.Fatalf("expected env zero task history limit to mean unlimited, got %#v", strategy.TaskHistoryMaxItems)
	}
	if strategy.ContextTokenBudget == nil || *strategy.ContextTokenBudget != 0 {
		t.Fatalf("expected env zero token budget to mean unlimited, got %#v", strategy.ContextTokenBudget)
	}
}

func TestLoadDotenvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(`
CLEAN_CORE_MODEL_PROVIDER=openai-compatible
CLEAN_CORE_MODEL_BASE_URL=https://api.deepseek.com
CLEAN_CORE_MODEL_API_KEY=placeholder
CLEAN_CORE_MODEL_NAME=deepseek-v4-flash
CLEAN_CORE_CONVERSATION_JUDGE_MODE=hybrid
CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED=false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLEAN_CORE_ENV_FILE", path)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelProvider != "openai-compatible" || cfg.ModelBaseURL != "https://api.deepseek.com" || cfg.ModelName != "deepseek-v4-flash" {
		t.Fatalf("expected dotenv model settings, got %#v", cfg)
	}
	if cfg.ModelAPIKey != "placeholder" {
		t.Fatalf("expected dotenv api key")
	}
	if cfg.ConversationJudgeMode != "hybrid" || cfg.ConversationRetrievalIsEnabled() {
		t.Fatalf("expected dotenv conversation settings, got %#v", cfg)
	}
}

func TestProcessEnvOverridesDotenvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(`
CLEAN_CORE_MODEL_PROVIDER=openai-compatible
CLEAN_CORE_MODEL_NAME=from-dotenv
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLEAN_CORE_ENV_FILE", path)
	t.Setenv("CLEAN_CORE_MODEL_PROVIDER", "stub")
	t.Setenv("CLEAN_CORE_MODEL_NAME", "from-process")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelProvider != "stub" || cfg.ModelName != "from-process" {
		t.Fatalf("expected process env to override dotenv, got %#v", cfg)
	}
}

func TestLoadModelRequestOptionsFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
model_max_tokens: 1024
model_temperature: 0.2
model_thinking: disabled
model_reasoning_effort: medium
conversation_judge_mode: model
conversation_judge_timeout_ms: 2500
conversation_direct_enabled: true
conversation_retrieval_enabled: false
context_default_mode: concise
context_default_recent_message_limit: 5
context_default_retrieval_max_results: 3
context_default_task_history_max_items: 9
context_default_token_budget: 1200
context_compression_default_enabled: false
context_compression_default_mode: truncate
external_bridge_provider: a2a
external_bridge_base_url: https://bridge.example.test
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelMaxTokens != 1024 || cfg.ModelTemperature == nil || *cfg.ModelTemperature != 0.2 {
		t.Fatalf("expected yaml model options, got %#v", cfg)
	}
	if cfg.ModelThinking != "disabled" || cfg.ModelReasoningEffort != "medium" {
		t.Fatalf("expected yaml passthrough options, got %#v", cfg)
	}
	if cfg.ConversationJudgeMode != "model" {
		t.Fatalf("expected yaml conversation judge mode, got %#v", cfg)
	}
	if cfg.ConversationJudgeTimeoutMS != 2500 || !cfg.ConversationDirectEnabled || cfg.ConversationRetrievalIsEnabled() {
		t.Fatalf("expected yaml conversation runtime options, got %#v", cfg)
	}
	if cfg.ExternalBridgeProvider != "a2a" || cfg.ExternalBridgeBaseURL != "https://bridge.example.test" {
		t.Fatalf("expected yaml external bridge options, got %#v", cfg)
	}
	strategy := cfg.ContextDefaultStrategy()
	if strategy.Mode != "concise" {
		t.Fatalf("expected yaml context mode, got %q", strategy.Mode)
	}
	if strategy.RecentMessageLimit == nil || *strategy.RecentMessageLimit != 5 {
		t.Fatalf("expected yaml recent limit, got %#v", strategy.RecentMessageLimit)
	}
	if strategy.RetrievalMaxResults == nil || *strategy.RetrievalMaxResults != 3 {
		t.Fatalf("expected yaml retrieval limit, got %#v", strategy.RetrievalMaxResults)
	}
	if strategy.TaskHistoryMaxItems == nil || *strategy.TaskHistoryMaxItems != 9 {
		t.Fatalf("expected yaml task history limit, got %#v", strategy.TaskHistoryMaxItems)
	}
	if strategy.ContextTokenBudget == nil || *strategy.ContextTokenBudget != 1200 {
		t.Fatalf("expected yaml token budget, got %#v", strategy.ContextTokenBudget)
	}
	if strategy.Compression.Enabled || strategy.Compression.Mode != "truncate" {
		t.Fatalf("expected yaml compression override, got %#v", strategy.Compression)
	}
}

func TestConversationJudgeModeValidation(t *testing.T) {
	cfg := Default()
	cfg.ConversationJudgeMode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid conversation judge mode to fail validation")
	}
}

func TestConversationRuntimeOptionValidation(t *testing.T) {
	cfg := Default()
	cfg.ConversationJudgeTimeoutMS = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative conversation_judge_timeout_ms to fail validation")
	}
}

func TestContextDefaultValidation(t *testing.T) {
	cfg := Default()
	cfg.ContextDefaultTokenBudget = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative context_default_token_budget to fail validation")
	}
	cfg = Default()
	cfg.ContextDefaultMode = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid context_default_mode to fail validation")
	}
	cfg = Default()
	cfg.ContextCompressionDefaultMode = "summarize"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid context_compression_default_mode to fail validation")
	}
}

func TestExternalBridgeProviderValidation(t *testing.T) {
	cfg := Default()
	cfg.ExternalBridgeProvider = "grpc"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid external_bridge_provider to fail validation")
	}
}
