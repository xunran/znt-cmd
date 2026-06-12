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
	t.Setenv("CLEAN_CORE_CONVERSATION_MAX_RETRIEVED", "3")
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
	if cfg.ConversationJudgeTimeoutMS != 1500 || !cfg.ConversationDirectEnabled || cfg.ConversationRetrievalIsEnabled() || cfg.ConversationMaxRetrieved != 3 {
		t.Fatalf("expected conversation runtime options from env, got %#v", cfg)
	}
	if cfg.ExternalBridgeProvider != "a2a" || cfg.ExternalBridgeBaseURL != "https://bridge.example.test" {
		t.Fatalf("expected external bridge options from env, got %#v", cfg)
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
conversation_max_retrieved: 4
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
	if cfg.ConversationJudgeTimeoutMS != 2500 || !cfg.ConversationDirectEnabled || cfg.ConversationRetrievalIsEnabled() || cfg.ConversationMaxRetrieved != 4 {
		t.Fatalf("expected yaml conversation runtime options, got %#v", cfg)
	}
	if cfg.ExternalBridgeProvider != "a2a" || cfg.ExternalBridgeBaseURL != "https://bridge.example.test" {
		t.Fatalf("expected yaml external bridge options, got %#v", cfg)
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
	cfg = Default()
	cfg.ConversationMaxRetrieved = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative conversation_max_retrieved to fail validation")
	}
}

func TestExternalBridgeProviderValidation(t *testing.T) {
	cfg := Default()
	cfg.ExternalBridgeProvider = "grpc"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid external_bridge_provider to fail validation")
	}
}
