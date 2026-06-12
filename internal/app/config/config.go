package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultServiceName = "clean-core"
	DefaultVersion     = "0.1.0-alpha"
	DefaultHTTPAddr    = ":8080"
	DefaultEnv         = "local"
	DefaultLogLevel    = "info"

	DefaultReadinessMode                      = "shallow"
	DefaultMetricsAuthMode                    = "auto"
	DefaultAgentRunExecutionMode              = "sync"
	DefaultDBMaxOpenConns                     = 25
	DefaultDBMaxIdleConns                     = 10
	DefaultDBReadinessMaxOpenConns            = 2
	DefaultDBReadinessMaxIdleConns            = 2
	DefaultDBConnMaxLifetimeSeconds           = 1800
	DefaultDBConnMaxIdleTimeSeconds           = 300
	DefaultHTTPReadHeaderTimeoutSeconds       = 5
	DefaultHTTPReadTimeoutSeconds             = 30
	DefaultHTTPWriteTimeoutSeconds            = 300
	DefaultHTTPIdleTimeoutSeconds             = 120
	DefaultHTTPMaxBodyBytes             int64 = 4 * 1024 * 1024
)

type Config struct {
	ServiceName                  string   `json:"service_name"`
	Version                      string   `json:"version"`
	Env                          string   `json:"env"`
	HTTPAddr                     string   `json:"http_addr"`
	LogLevel                     string   `json:"log_level"`
	DatabaseURL                  string   `json:"database_url,omitempty"`
	Readiness                    bool     `json:"readiness"`
	ReadinessMode                string   `json:"readiness_mode,omitempty"`
	MetricsAuthMode              string   `json:"metrics_auth_mode,omitempty"`
	ServiceToken                 string   `json:"service_token,omitempty"`
	DBMaxOpenConns               int      `json:"db_max_open_conns,omitempty"`
	DBMaxIdleConns               int      `json:"db_max_idle_conns,omitempty"`
	DBReadinessMaxOpenConns      int      `json:"db_readiness_max_open_conns,omitempty"`
	DBReadinessMaxIdleConns      int      `json:"db_readiness_max_idle_conns,omitempty"`
	DBConnMaxLifetimeSeconds     int      `json:"db_conn_max_lifetime_seconds,omitempty"`
	DBConnMaxIdleTimeSeconds     int      `json:"db_conn_max_idle_time_seconds,omitempty"`
	HTTPReadHeaderTimeoutSeconds int      `json:"http_read_header_timeout_seconds,omitempty"`
	HTTPReadTimeoutSeconds       int      `json:"http_read_timeout_seconds,omitempty"`
	HTTPWriteTimeoutSeconds      int      `json:"http_write_timeout_seconds,omitempty"`
	HTTPIdleTimeoutSeconds       int      `json:"http_idle_timeout_seconds,omitempty"`
	HTTPMaxBodyBytes             int64    `json:"http_max_body_bytes,omitempty"`
	RunMaxConcurrent             int      `json:"run_max_concurrent,omitempty"`
	TenantRunMaxConcurrent       int      `json:"tenant_run_max_concurrent,omitempty"`
	AgentRunMaxConcurrent        int      `json:"agent_run_max_concurrent,omitempty"`
	AgentRunExecutionMode        string   `json:"agent_run_execution_mode,omitempty"`
	ModelProvider                string   `json:"model_provider,omitempty"`
	ModelBaseURL                 string   `json:"model_base_url,omitempty"`
	ModelAPIKey                  string   `json:"model_api_key,omitempty"`
	ModelName                    string   `json:"model_name,omitempty"`
	ModelMaxTokens               int      `json:"model_max_tokens,omitempty"`
	ModelTemperature             *float64 `json:"model_temperature,omitempty"`
	ModelThinking                string   `json:"model_thinking,omitempty"`
	ModelReasoningEffort         string   `json:"model_reasoning_effort,omitempty"`
	ConversationJudgeMode        string   `json:"conversation_judge_mode,omitempty"`
	ConversationJudgeTimeoutMS   int      `json:"conversation_judge_timeout_ms,omitempty"`
	ConversationDirectEnabled    bool     `json:"conversation_direct_enabled,omitempty"`
	ConversationRetrievalEnabled bool     `json:"conversation_retrieval_enabled,omitempty"`
	ConversationMaxRetrieved     int      `json:"conversation_max_retrieved,omitempty"`
	ExternalBridgeProvider       string   `json:"external_bridge_provider,omitempty"`
	ExternalBridgeBaseURL        string   `json:"external_bridge_base_url,omitempty"`
	ExternalBridgeToken          string   `json:"external_bridge_token,omitempty"`

	DisabledAgentIDs           []string `json:"disabled_agent_ids,omitempty"`
	DisabledToolIDs            []string `json:"disabled_tool_ids,omitempty"`
	DisableHandoff             bool     `json:"disable_handoff,omitempty"`
	DisableExternalToolsInvoke bool     `json:"disable_external_tools_invoke,omitempty"`

	readinessSet                    bool
	conversationRetrievalEnabledSet bool
	disableHandoffSet               bool
	disableExternalToolsInvokeSet   bool
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = Config(decoded)
	_, c.readinessSet = raw["readiness"]
	_, c.conversationRetrievalEnabledSet = raw["conversation_retrieval_enabled"]
	_, c.disableHandoffSet = raw["disable_handoff"]
	_, c.disableExternalToolsInvokeSet = raw["disable_external_tools_invoke"]
	return nil
}

func Default() Config {
	return Config{
		ServiceName:                  DefaultServiceName,
		Version:                      DefaultVersion,
		Env:                          DefaultEnv,
		HTTPAddr:                     DefaultHTTPAddr,
		LogLevel:                     DefaultLogLevel,
		Readiness:                    true,
		ReadinessMode:                DefaultReadinessMode,
		MetricsAuthMode:              DefaultMetricsAuthMode,
		DBMaxOpenConns:               DefaultDBMaxOpenConns,
		DBMaxIdleConns:               DefaultDBMaxIdleConns,
		DBReadinessMaxOpenConns:      DefaultDBReadinessMaxOpenConns,
		DBReadinessMaxIdleConns:      DefaultDBReadinessMaxIdleConns,
		DBConnMaxLifetimeSeconds:     DefaultDBConnMaxLifetimeSeconds,
		DBConnMaxIdleTimeSeconds:     DefaultDBConnMaxIdleTimeSeconds,
		HTTPReadHeaderTimeoutSeconds: DefaultHTTPReadHeaderTimeoutSeconds,
		HTTPReadTimeoutSeconds:       DefaultHTTPReadTimeoutSeconds,
		HTTPWriteTimeoutSeconds:      DefaultHTTPWriteTimeoutSeconds,
		HTTPIdleTimeoutSeconds:       DefaultHTTPIdleTimeoutSeconds,
		HTTPMaxBodyBytes:             DefaultHTTPMaxBodyBytes,
		AgentRunExecutionMode:        DefaultAgentRunExecutionMode,
		ConversationRetrievalEnabled: true,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		fileCfg, err := loadFile(path)
		if err != nil {
			return Config{}, err
		}
		cfg = merge(cfg, fileCfg)
	}
	dotenv, err := loadDotenv(dotenvPath())
	if err != nil {
		return Config{}, err
	}
	cfg = applyEnvMap(cfg, dotenv)
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ConversationRetrievalIsEnabled() bool {
	if c.conversationRetrievalEnabledSet {
		return c.ConversationRetrievalEnabled
	}
	if !c.ConversationRetrievalEnabled {
		return true
	}
	return c.ConversationRetrievalEnabled
}

func (c Config) EffectiveReadinessMode() string {
	switch strings.ToLower(strings.TrimSpace(c.ReadinessMode)) {
	case "deep":
		return "deep"
	default:
		return DefaultReadinessMode
	}
}

func (c Config) EffectiveMetricsAuthRequired() bool {
	switch strings.ToLower(strings.TrimSpace(c.MetricsAuthMode)) {
	case "required":
		return true
	case "disabled", "public", "none":
		return false
	default:
		return isProduction(c.Env)
	}
}

func (c Config) EffectiveAgentRunExecutionMode() string {
	switch strings.ToLower(strings.TrimSpace(c.AgentRunExecutionMode)) {
	case "async":
		return "async"
	default:
		return DefaultAgentRunExecutionMode
	}
}

func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.ServiceName) == "" {
		missing = append(missing, "service_name")
	}
	if strings.TrimSpace(c.Version) == "" {
		missing = append(missing, "version")
	}
	if strings.TrimSpace(c.HTTPAddr) == "" {
		missing = append(missing, "http_addr")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	if isProduction(c.Env) {
		var prodMissing []string
		if strings.TrimSpace(c.DatabaseURL) == "" {
			prodMissing = append(prodMissing, "database_url")
		}
		if strings.TrimSpace(c.ServiceToken) == "" {
			prodMissing = append(prodMissing, "service_token")
		}
		if strings.TrimSpace(c.ModelBaseURL) == "" {
			prodMissing = append(prodMissing, "model_base_url")
		}
		if strings.TrimSpace(c.ModelName) == "" {
			prodMissing = append(prodMissing, "model_name")
		}
		if len(prodMissing) > 0 {
			return fmt.Errorf("production config requires %s", strings.Join(prodMissing, ", "))
		}
	}
	if strings.TrimSpace(c.ExternalBridgeToken) != "" && strings.TrimSpace(c.ExternalBridgeBaseURL) == "" {
		return fmt.Errorf("external_bridge_token requires external_bridge_base_url")
	}
	if err := validateOneOf("external_bridge_provider", c.ExternalBridgeProvider, "", "array", "a2a"); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(c.ConversationJudgeMode)) {
	case "", "heuristic", "model", "hybrid":
	default:
		return fmt.Errorf("conversation_judge_mode must be heuristic, model, or hybrid")
	}
	if c.ConversationJudgeTimeoutMS < 0 {
		return fmt.Errorf("conversation_judge_timeout_ms must be non-negative")
	}
	if c.ConversationMaxRetrieved < 0 {
		return fmt.Errorf("conversation_max_retrieved must be non-negative")
	}
	if err := validateOneOf("readiness_mode", c.ReadinessMode, "", "shallow", "deep"); err != nil {
		return err
	}
	if err := validateOneOf("metrics_auth_mode", c.MetricsAuthMode, "", "auto", "required", "disabled", "public", "none"); err != nil {
		return err
	}
	if err := validateOneOf("agent_run_execution_mode", c.AgentRunExecutionMode, "", "sync", "async"); err != nil {
		return err
	}
	if c.DBMaxOpenConns < 0 {
		return fmt.Errorf("db_max_open_conns must be non-negative")
	}
	if c.DBMaxIdleConns < 0 {
		return fmt.Errorf("db_max_idle_conns must be non-negative")
	}
	if c.DBMaxOpenConns > 0 && c.DBMaxIdleConns > c.DBMaxOpenConns {
		return fmt.Errorf("db_max_idle_conns must be less than or equal to db_max_open_conns")
	}
	if c.DBReadinessMaxOpenConns < 0 {
		return fmt.Errorf("db_readiness_max_open_conns must be non-negative")
	}
	if c.DBReadinessMaxIdleConns < 0 {
		return fmt.Errorf("db_readiness_max_idle_conns must be non-negative")
	}
	if c.DBReadinessMaxOpenConns > 0 && c.DBReadinessMaxIdleConns > c.DBReadinessMaxOpenConns {
		return fmt.Errorf("db_readiness_max_idle_conns must be less than or equal to db_readiness_max_open_conns")
	}
	for name, value := range map[string]int{
		"db_conn_max_lifetime_seconds":     c.DBConnMaxLifetimeSeconds,
		"db_conn_max_idle_time_seconds":    c.DBConnMaxIdleTimeSeconds,
		"http_read_header_timeout_seconds": c.HTTPReadHeaderTimeoutSeconds,
		"http_read_timeout_seconds":        c.HTTPReadTimeoutSeconds,
		"http_write_timeout_seconds":       c.HTTPWriteTimeoutSeconds,
		"http_idle_timeout_seconds":        c.HTTPIdleTimeoutSeconds,
		"run_max_concurrent":               c.RunMaxConcurrent,
		"tenant_run_max_concurrent":        c.TenantRunMaxConcurrent,
		"agent_run_max_concurrent":         c.AgentRunMaxConcurrent,
	} {
		if value < 0 {
			return fmt.Errorf("%s must be non-negative", name)
		}
	}
	if c.HTTPMaxBodyBytes < 0 {
		return fmt.Errorf("http_max_body_bytes must be non-negative")
	}
	return nil
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse json config: %w", err)
		}
		return cfg, nil
	case ".yaml", ".yml":
		return parseSimpleYAML(data)
	default:
		return Config{}, fmt.Errorf("unsupported config extension %q", filepath.Ext(path))
	}
}

func merge(base Config, override Config) Config {
	if override.ServiceName != "" {
		base.ServiceName = override.ServiceName
	}
	if override.Version != "" {
		base.Version = override.Version
	}
	if override.Env != "" {
		base.Env = override.Env
	}
	if override.HTTPAddr != "" {
		base.HTTPAddr = override.HTTPAddr
	}
	if override.LogLevel != "" {
		base.LogLevel = override.LogLevel
	}
	if override.DatabaseURL != "" {
		base.DatabaseURL = override.DatabaseURL
	}
	if override.ReadinessMode != "" {
		base.ReadinessMode = override.ReadinessMode
	}
	if override.MetricsAuthMode != "" {
		base.MetricsAuthMode = override.MetricsAuthMode
	}
	if override.ServiceToken != "" {
		base.ServiceToken = override.ServiceToken
	}
	if override.DBMaxOpenConns > 0 {
		base.DBMaxOpenConns = override.DBMaxOpenConns
	}
	if override.DBMaxIdleConns > 0 {
		base.DBMaxIdleConns = override.DBMaxIdleConns
	}
	if override.DBReadinessMaxOpenConns > 0 {
		base.DBReadinessMaxOpenConns = override.DBReadinessMaxOpenConns
	}
	if override.DBReadinessMaxIdleConns > 0 {
		base.DBReadinessMaxIdleConns = override.DBReadinessMaxIdleConns
	}
	if override.DBConnMaxLifetimeSeconds > 0 {
		base.DBConnMaxLifetimeSeconds = override.DBConnMaxLifetimeSeconds
	}
	if override.DBConnMaxIdleTimeSeconds > 0 {
		base.DBConnMaxIdleTimeSeconds = override.DBConnMaxIdleTimeSeconds
	}
	if override.HTTPReadHeaderTimeoutSeconds > 0 {
		base.HTTPReadHeaderTimeoutSeconds = override.HTTPReadHeaderTimeoutSeconds
	}
	if override.HTTPReadTimeoutSeconds > 0 {
		base.HTTPReadTimeoutSeconds = override.HTTPReadTimeoutSeconds
	}
	if override.HTTPWriteTimeoutSeconds > 0 {
		base.HTTPWriteTimeoutSeconds = override.HTTPWriteTimeoutSeconds
	}
	if override.HTTPIdleTimeoutSeconds > 0 {
		base.HTTPIdleTimeoutSeconds = override.HTTPIdleTimeoutSeconds
	}
	if override.HTTPMaxBodyBytes > 0 {
		base.HTTPMaxBodyBytes = override.HTTPMaxBodyBytes
	}
	if override.RunMaxConcurrent > 0 {
		base.RunMaxConcurrent = override.RunMaxConcurrent
	}
	if override.TenantRunMaxConcurrent > 0 {
		base.TenantRunMaxConcurrent = override.TenantRunMaxConcurrent
	}
	if override.AgentRunMaxConcurrent > 0 {
		base.AgentRunMaxConcurrent = override.AgentRunMaxConcurrent
	}
	if override.AgentRunExecutionMode != "" {
		base.AgentRunExecutionMode = override.AgentRunExecutionMode
	}
	if override.ModelProvider != "" {
		base.ModelProvider = override.ModelProvider
	}
	if override.ModelBaseURL != "" {
		base.ModelBaseURL = override.ModelBaseURL
	}
	if override.ModelAPIKey != "" {
		base.ModelAPIKey = override.ModelAPIKey
	}
	if override.ModelName != "" {
		base.ModelName = override.ModelName
	}
	if override.ModelMaxTokens > 0 {
		base.ModelMaxTokens = override.ModelMaxTokens
	}
	if override.ModelTemperature != nil {
		base.ModelTemperature = override.ModelTemperature
	}
	if override.ModelThinking != "" {
		base.ModelThinking = override.ModelThinking
	}
	if override.ModelReasoningEffort != "" {
		base.ModelReasoningEffort = override.ModelReasoningEffort
	}
	if override.ConversationJudgeMode != "" {
		base.ConversationJudgeMode = override.ConversationJudgeMode
	}
	if override.ConversationJudgeTimeoutMS > 0 {
		base.ConversationJudgeTimeoutMS = override.ConversationJudgeTimeoutMS
	}
	if override.ConversationDirectEnabled {
		base.ConversationDirectEnabled = true
	}
	if override.ConversationMaxRetrieved > 0 {
		base.ConversationMaxRetrieved = override.ConversationMaxRetrieved
	}
	if override.conversationRetrievalEnabledSet {
		base.ConversationRetrievalEnabled = override.ConversationRetrievalEnabled
		base.conversationRetrievalEnabledSet = true
	}
	if override.ExternalBridgeBaseURL != "" {
		base.ExternalBridgeBaseURL = override.ExternalBridgeBaseURL
	}
	if override.ExternalBridgeProvider != "" {
		base.ExternalBridgeProvider = override.ExternalBridgeProvider
	}
	if override.ExternalBridgeToken != "" {
		base.ExternalBridgeToken = override.ExternalBridgeToken
	}
	if len(override.DisabledAgentIDs) > 0 {
		base.DisabledAgentIDs = override.DisabledAgentIDs
	}
	if len(override.DisabledToolIDs) > 0 {
		base.DisabledToolIDs = override.DisabledToolIDs
	}
	if override.disableHandoffSet {
		base.DisableHandoff = override.DisableHandoff
	}
	if override.disableExternalToolsInvokeSet {
		base.DisableExternalToolsInvoke = override.DisableExternalToolsInvoke
	}
	if override.readinessSet {
		base.Readiness = override.Readiness
	}
	return base
}

func applyEnv(cfg *Config) {
	*cfg = applyEnvMap(*cfg, processEnvMap())
}

func processEnvMap() map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func applyEnvMap(cfg Config, env map[string]string) Config {
	setString := func(key string, dst *string) {
		if v := strings.TrimSpace(env[key]); v != "" {
			*dst = v
		}
	}
	setString("CLEAN_CORE_SERVICE_NAME", &cfg.ServiceName)
	setString("CLEAN_CORE_VERSION", &cfg.Version)
	setString("CLEAN_CORE_ENV", &cfg.Env)
	setString("CLEAN_CORE_HTTP_ADDR", &cfg.HTTPAddr)
	setString("CLEAN_CORE_LOG_LEVEL", &cfg.LogLevel)
	setString("CLEAN_CORE_DATABASE_URL", &cfg.DatabaseURL)
	setString("CLEAN_CORE_READINESS_MODE", &cfg.ReadinessMode)
	setString("CLEAN_CORE_METRICS_AUTH_MODE", &cfg.MetricsAuthMode)
	setString("CLEAN_CORE_SERVICE_TOKEN", &cfg.ServiceToken)
	setString("CLEAN_CORE_AGENT_RUN_EXECUTION_MODE", &cfg.AgentRunExecutionMode)
	setString("CLEAN_CORE_MODEL_PROVIDER", &cfg.ModelProvider)
	setString("CLEAN_CORE_MODEL_BASE_URL", &cfg.ModelBaseURL)
	setString("CLEAN_CORE_MODEL_API_KEY", &cfg.ModelAPIKey)
	setString("CLEAN_CORE_MODEL_NAME", &cfg.ModelName)
	setString("CLEAN_CORE_MODEL_THINKING", &cfg.ModelThinking)
	setString("CLEAN_CORE_MODEL_REASONING_EFFORT", &cfg.ModelReasoningEffort)
	setString("CLEAN_CORE_CONVERSATION_JUDGE_MODE", &cfg.ConversationJudgeMode)
	setString("CLEAN_CORE_EXTERNAL_BRIDGE_PROVIDER", &cfg.ExternalBridgeProvider)
	setString("CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL", &cfg.ExternalBridgeBaseURL)
	setString("CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN", &cfg.ExternalBridgeToken)
	if v := strings.TrimSpace(env["CLEAN_CORE_MODEL_MAX_TOKENS"]); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.ModelMaxTokens = parsed
		}
	}
	setInt := func(key string, dst *int) {
		if v := strings.TrimSpace(env[key]); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil {
				*dst = parsed
			}
		}
	}
	setInt64 := func(key string, dst *int64) {
		if v := strings.TrimSpace(env[key]); v != "" {
			if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
				*dst = parsed
			}
		}
	}
	setInt("CLEAN_CORE_DB_MAX_OPEN_CONNS", &cfg.DBMaxOpenConns)
	setInt("CLEAN_CORE_DB_MAX_IDLE_CONNS", &cfg.DBMaxIdleConns)
	setInt("CLEAN_CORE_DB_READINESS_MAX_OPEN_CONNS", &cfg.DBReadinessMaxOpenConns)
	setInt("CLEAN_CORE_DB_READINESS_MAX_IDLE_CONNS", &cfg.DBReadinessMaxIdleConns)
	setInt("CLEAN_CORE_DB_CONN_MAX_LIFETIME_SECONDS", &cfg.DBConnMaxLifetimeSeconds)
	setInt("CLEAN_CORE_DB_CONN_MAX_IDLE_TIME_SECONDS", &cfg.DBConnMaxIdleTimeSeconds)
	setInt("CLEAN_CORE_HTTP_READ_HEADER_TIMEOUT_SECONDS", &cfg.HTTPReadHeaderTimeoutSeconds)
	setInt("CLEAN_CORE_HTTP_READ_TIMEOUT_SECONDS", &cfg.HTTPReadTimeoutSeconds)
	setInt("CLEAN_CORE_HTTP_WRITE_TIMEOUT_SECONDS", &cfg.HTTPWriteTimeoutSeconds)
	setInt("CLEAN_CORE_HTTP_IDLE_TIMEOUT_SECONDS", &cfg.HTTPIdleTimeoutSeconds)
	setInt64("CLEAN_CORE_HTTP_MAX_BODY_BYTES", &cfg.HTTPMaxBodyBytes)
	setInt("CLEAN_CORE_RUN_MAX_CONCURRENT", &cfg.RunMaxConcurrent)
	setInt("CLEAN_CORE_TENANT_RUN_MAX_CONCURRENT", &cfg.TenantRunMaxConcurrent)
	setInt("CLEAN_CORE_AGENT_RUN_MAX_CONCURRENT", &cfg.AgentRunMaxConcurrent)
	if v := strings.TrimSpace(env["CLEAN_CORE_MODEL_TEMPERATURE"]); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.ModelTemperature = &parsed
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS"]); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.ConversationJudgeTimeoutMS = parsed
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_CONVERSATION_DIRECT_ENABLED"]); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ConversationDirectEnabled = parsed
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_CONVERSATION_MAX_RETRIEVED"]); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.ConversationMaxRetrieved = parsed
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED"]); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ConversationRetrievalEnabled = parsed
			cfg.conversationRetrievalEnabledSet = true
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_DISABLED_AGENT_IDS"]); v != "" {
		cfg.DisabledAgentIDs = splitList(v)
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_DISABLED_TOOL_IDS"]); v != "" {
		cfg.DisabledToolIDs = splitList(v)
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_READINESS"]); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Readiness = parsed
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_DISABLE_HANDOFF"]); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.DisableHandoff = parsed
			cfg.disableHandoffSet = true
		}
	}
	if v := strings.TrimSpace(env["CLEAN_CORE_DISABLE_EXTERNAL_TOOLS_INVOKE"]); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.DisableExternalToolsInvoke = parsed
			cfg.disableExternalToolsInvokeSet = true
		}
	}
	return cfg
}

func dotenvPath() string {
	if path := strings.TrimSpace(os.Getenv("CLEAN_CORE_ENV_FILE")); path != "" {
		return path
	}
	return ".env"
}

func loadDotenv(path string) (map[string]string, error) {
	env := map[string]string{}
	if strings.TrimSpace(path) == "" {
		return env, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return env, nil
		}
		return nil, fmt.Errorf("load env file %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("parse env file %s line %d: expected KEY=value", path, i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("parse env file %s line %d: empty key", path, i+1)
		}
		if !isEnvKey(key) {
			return nil, fmt.Errorf("parse env file %s line %d: invalid key %q", path, i+1, key)
		}
		env[key] = trimEnvValue(value)
	}
	return env, nil
}

func trimEnvValue(value string) string {
	if len(value) >= 2 {
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) || (strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`)) {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func isEnvKey(key string) bool {
	for i, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func parseSimpleYAML(data []byte) (Config, error) {
	cfg := Config{}
	seen := false
	lines := strings.Split(string(data), "\n")
	parseYAMLInt := func(lineNo int, key string, value string) (int, error) {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("parse yaml config line %d %s: %w", lineNo, key, err)
		}
		return parsed, nil
	}
	parseYAMLInt64 := func(lineNo int, key string, value string) (int64, error) {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse yaml config line %d %s: %w", lineNo, key, err)
		}
		return parsed, nil
	}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Config{}, fmt.Errorf("parse yaml config line %d: expected key: value", i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		seen = true
		switch key {
		case "service_name":
			cfg.ServiceName = value
		case "version":
			cfg.Version = value
		case "env":
			cfg.Env = value
		case "http_addr":
			cfg.HTTPAddr = value
		case "log_level":
			cfg.LogLevel = value
		case "database_url":
			cfg.DatabaseURL = value
		case "readiness_mode":
			cfg.ReadinessMode = value
		case "metrics_auth_mode":
			cfg.MetricsAuthMode = value
		case "db_max_open_conns":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBMaxOpenConns = parsed
		case "db_max_idle_conns":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBMaxIdleConns = parsed
		case "db_readiness_max_open_conns":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBReadinessMaxOpenConns = parsed
		case "db_readiness_max_idle_conns":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBReadinessMaxIdleConns = parsed
		case "db_conn_max_lifetime_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBConnMaxLifetimeSeconds = parsed
		case "db_conn_max_idle_time_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.DBConnMaxIdleTimeSeconds = parsed
		case "http_read_header_timeout_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.HTTPReadHeaderTimeoutSeconds = parsed
		case "http_read_timeout_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.HTTPReadTimeoutSeconds = parsed
		case "http_write_timeout_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.HTTPWriteTimeoutSeconds = parsed
		case "http_idle_timeout_seconds":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.HTTPIdleTimeoutSeconds = parsed
		case "http_max_body_bytes":
			parsed, err := parseYAMLInt64(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.HTTPMaxBodyBytes = parsed
		case "run_max_concurrent":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.RunMaxConcurrent = parsed
		case "tenant_run_max_concurrent":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.TenantRunMaxConcurrent = parsed
		case "agent_run_max_concurrent":
			parsed, err := parseYAMLInt(i+1, key, value)
			if err != nil {
				return Config{}, err
			}
			cfg.AgentRunMaxConcurrent = parsed
		case "agent_run_execution_mode":
			cfg.AgentRunExecutionMode = value
		case "model_provider":
			cfg.ModelProvider = value
		case "model_base_url":
			cfg.ModelBaseURL = value
		case "model_api_key":
			cfg.ModelAPIKey = value
		case "model_name":
			cfg.ModelName = value
		case "model_max_tokens":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d model_max_tokens: %w", i+1, err)
			}
			cfg.ModelMaxTokens = parsed
		case "model_temperature":
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d model_temperature: %w", i+1, err)
			}
			cfg.ModelTemperature = &parsed
		case "model_thinking":
			cfg.ModelThinking = value
		case "model_reasoning_effort":
			cfg.ModelReasoningEffort = value
		case "conversation_judge_mode":
			cfg.ConversationJudgeMode = value
		case "conversation_judge_timeout_ms":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d conversation_judge_timeout_ms: %w", i+1, err)
			}
			cfg.ConversationJudgeTimeoutMS = parsed
		case "conversation_direct_enabled":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d conversation_direct_enabled: %w", i+1, err)
			}
			cfg.ConversationDirectEnabled = parsed
		case "conversation_retrieval_enabled":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d conversation_retrieval_enabled: %w", i+1, err)
			}
			cfg.ConversationRetrievalEnabled = parsed
			cfg.conversationRetrievalEnabledSet = true
		case "conversation_max_retrieved":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d conversation_max_retrieved: %w", i+1, err)
			}
			cfg.ConversationMaxRetrieved = parsed
		case "external_bridge_base_url":
			cfg.ExternalBridgeBaseURL = value
		case "external_bridge_provider":
			cfg.ExternalBridgeProvider = value
		case "external_bridge_token":
			cfg.ExternalBridgeToken = value
		case "readiness":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d readiness: %w", i+1, err)
			}
			cfg.Readiness = parsed
			cfg.readinessSet = true
		case "disabled_agent_ids":
			cfg.DisabledAgentIDs = splitList(value)
		case "disabled_tool_ids":
			cfg.DisabledToolIDs = splitList(value)
		case "disable_handoff":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d disable_handoff: %w", i+1, err)
			}
			cfg.DisableHandoff = parsed
			cfg.disableHandoffSet = true
		case "disable_external_tools_invoke":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse yaml config line %d disable_external_tools_invoke: %w", i+1, err)
			}
			cfg.DisableExternalToolsInvoke = parsed
			cfg.disableExternalToolsInvokeSet = true
		default:
			return Config{}, fmt.Errorf("parse yaml config line %d: unknown key %q", i+1, key)
		}
	}
	if !seen {
		return Config{}, errors.New("empty config file")
	}
	return cfg, nil
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func validateOneOf(name string, value string, allowed ...string) error {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if normalized == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", name, strings.Join(allowed, ", "))
}

func isProduction(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
