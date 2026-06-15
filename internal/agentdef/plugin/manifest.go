package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
	runtimehook "znt/internal/runtime/hook"
	serviceconnection "znt/internal/serviceconnection"
	toolcatalog "znt/internal/tool/catalog"
)

const providerAuthRefHeader = "X-Origin-Provider-Auth-Ref"

type AgentPluginManifest struct {
	ManifestVersion string `json:"manifest_version,omitempty"`
	ProviderID      string `json:"provider_id,omitempty"`

	Agent AgentPluginAgentManifest   `json:"agent"`
	Tools []AgentPluginToolManifest  `json:"tools,omitempty"`
	Hooks []runtimehook.HookManifest `json:"hooks,omitempty"`

	Collaborators []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
	Exports       contracts.AgentExports           `json:"exports,omitempty"`
	Strategies    contracts.AgentStrategies        `json:"strategies,omitempty"`
}

type AgentPluginAgentManifest struct {
	AgentID     contracts.AgentID      `json:"agent_id"`
	Version     contracts.AgentVersion `json:"version,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	AgentsMD    string                 `json:"agents_md,omitempty"`
	Prompt      string                 `json:"prompt,omitempty"`
}

type AgentPluginToolManifest struct {
	ToolID       string                   `json:"tool_id"`
	Operation    string                   `json:"operation,omitempty"`
	GroupID      string                   `json:"group_id,omitempty"`
	Name         string                   `json:"name"`
	Description  string                   `json:"description"`
	WhenToUse    []string                 `json:"when_to_use,omitempty"`
	InputSchema  map[string]any           `json:"input_schema"`
	OutputSchema map[string]any           `json:"output_schema,omitempty"`
	RiskLevel    contracts.RiskLevel      `json:"risk_level,omitempty"`
	Visibility   contracts.ToolVisibility `json:"visibility,omitempty"`
	Version      string                   `json:"version,omitempty"`
}

type SourceInput struct {
	ProviderID string
	Manifest   AgentPluginManifest
	Overrides  agentpackage.AgentPluginSource
}

func DecodeManifest(data []byte) (AgentPluginManifest, error) {
	var manifest AgentPluginManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return AgentPluginManifest{}, err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return AgentPluginManifest{}, fmt.Errorf("agent plugin manifest contains multiple JSON values")
		}
		return AgentPluginManifest{}, err
	}
	return NormalizeManifest(manifest), nil
}

func ManifestHash(manifest AgentPluginManifest) string {
	data, _ := json.Marshal(NormalizeManifest(manifest))
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func FetchManifest(ctx context.Context, client *http.Client, connection serviceconnection.ServiceConnection) (AgentPluginManifest, error) {
	if strings.TrimSpace(connection.BaseURL) == "" {
		return AgentPluginManifest{}, agentpackage.CompileError{Path: "plugin.service_connection.base_url", Message: "base_url is required"}
	}
	if client == nil {
		timeout := 10 * time.Second
		if connection.TimeoutMS > 0 {
			timeout = time.Duration(connection.TimeoutMS) * time.Millisecond
		}
		client = &http.Client{Timeout: timeout}
	}
	endpoint, err := joinURL(connection.BaseURL, "/.well-known/agent-plugin.json")
	if err != nil {
		return AgentPluginManifest{}, err
	}
	retryMax := connection.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	var lastErr error
	for attempt := 0; attempt <= retryMax; attempt++ {
		manifest, err := fetchManifestOnce(ctx, client, endpoint, connection)
		if err == nil {
			return manifest, nil
		}
		lastErr = err
	}
	return AgentPluginManifest{}, lastErr
}

func fetchManifestOnce(ctx context.Context, client *http.Client, endpoint string, connection serviceconnection.ServiceConnection) (AgentPluginManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AgentPluginManifest{}, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(connection.AuthRef) != "" {
		req.Header.Set(providerAuthRefHeader, strings.TrimSpace(connection.AuthRef))
	}
	resp, err := client.Do(req)
	if err != nil {
		return AgentPluginManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AgentPluginManifest{}, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "agent plugin manifest fetch failed", map[string]any{"status_code": resp.StatusCode})
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return AgentPluginManifest{}, err
	}
	manifest, err := DecodeManifest(data)
	if err != nil {
		return AgentPluginManifest{}, err
	}
	return manifest, nil
}

func joinURL(base string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid service connection base_url")
	}
	relative, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return parsed.ResolveReference(relative).String(), nil
}

func NormalizeManifest(manifest AgentPluginManifest) AgentPluginManifest {
	manifest.ManifestVersion = strings.TrimSpace(manifest.ManifestVersion)
	manifest.ProviderID = strings.TrimSpace(manifest.ProviderID)
	manifest.Agent.AgentID = contracts.AgentID(strings.TrimSpace(string(manifest.Agent.AgentID)))
	manifest.Agent.Version = contracts.AgentVersion(strings.TrimSpace(string(manifest.Agent.Version)))
	manifest.Agent.Name = strings.TrimSpace(manifest.Agent.Name)
	manifest.Agent.Description = strings.TrimSpace(manifest.Agent.Description)
	manifest.Agent.AgentsMD = strings.TrimSpace(manifest.Agent.AgentsMD)
	manifest.Agent.Prompt = strings.TrimSpace(manifest.Agent.Prompt)
	for i := range manifest.Tools {
		manifest.Tools[i] = normalizeTool(manifest.Tools[i])
	}
	for i := range manifest.Hooks {
		manifest.Hooks[i] = normalizeHook(manifest.Hooks[i], manifest.ProviderID)
	}
	return manifest
}

func BuildSource(input SourceInput) (agentpackage.AgentPluginSource, error) {
	manifest := NormalizeManifest(input.Manifest)
	if err := ValidateManifest(manifest); err != nil {
		return agentpackage.AgentPluginSource{}, err
	}
	providerID := firstNonEmpty(input.Overrides.ProviderID, input.ProviderID, manifest.ProviderID)
	metadata := mergeMetadata(manifestMetadata(manifest), input.Overrides.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["manifest_hash"] = ManifestHash(manifest)
	source := agentpackage.AgentPluginSource{
		ProviderID:       providerID,
		ManifestVersion:  firstNonEmpty(input.Overrides.ManifestVersion, manifest.ManifestVersion),
		AgentsMD:         firstNonEmpty(input.Overrides.AgentsMD, manifest.Agent.AgentsMD),
		Prompt:           firstNonEmpty(input.Overrides.Prompt, manifest.Agent.Prompt),
		Strategies:       mergeStrategies(manifest.Strategies, input.Overrides.Strategies),
		ToolBindings:     mergeToolBindings(toolBindingsForManifest(manifest.Tools), input.Overrides.ToolBindings),
		Skills:           append([]contracts.SkillDefinitionRef(nil), input.Overrides.Skills...),
		SkillDefinitions: append([]contracts.SkillDefinition(nil), input.Overrides.SkillDefinitions...),
		Collaborators:    firstCollaborators(input.Overrides.Collaborators, manifest.Collaborators),
		Exports:          firstExports(input.Overrides.Exports, manifest.Exports),
		RuntimeHooks:     firstRuntimeHooks(input.Overrides.RuntimeHooks, runtimeHooksForManifest(providerID, manifest.Hooks)),
		Metadata:         metadata,
	}
	if strings.TrimSpace(source.ProviderID) == "" {
		return agentpackage.AgentPluginSource{}, agentpackage.CompileError{Path: "plugin.provider_id", Message: "provider_id is required"}
	}
	if source.Prompt == "" && source.AgentsMD == "" {
		return agentpackage.AgentPluginSource{}, agentpackage.CompileError{Path: "plugin.agent.prompt", Message: "agent prompt or agents_md is required"}
	}
	return source, nil
}

func ValidateManifest(manifest AgentPluginManifest) error {
	manifest = NormalizeManifest(manifest)
	seenTools := map[string]struct{}{}
	for i, tool := range manifest.Tools {
		path := fmt.Sprintf("plugin.tools[%d]", i)
		if tool.ToolID == "" {
			return agentpackage.CompileError{Path: path + ".tool_id", Message: "tool_id is required"}
		}
		if _, ok := seenTools[tool.ToolID]; ok {
			return agentpackage.CompileError{Path: path + ".tool_id", Message: "duplicate tool_id"}
		}
		seenTools[tool.ToolID] = struct{}{}
		if tool.Name == "" {
			return agentpackage.CompileError{Path: path + ".name", Message: "name is required"}
		}
		if tool.Description == "" {
			return agentpackage.CompileError{Path: path + ".description", Message: "description is required"}
		}
		if err := validateManifestSchema(path+".input_schema", tool.InputSchema); err != nil {
			return err
		}
		if err := validateManifestSchema(path+".output_schema", tool.OutputSchema); err != nil {
			return err
		}
	}
	seenHooks := map[string]struct{}{}
	for i, hook := range manifest.Hooks {
		path := fmt.Sprintf("plugin.hooks[%d]", i)
		if strings.TrimSpace(hook.HookID) == "" {
			return agentpackage.CompileError{Path: path + ".hook_id", Message: "hook_id is required"}
		}
		if _, ok := seenHooks[hook.HookID]; ok {
			return agentpackage.CompileError{Path: path + ".hook_id", Message: "duplicate hook_id"}
		}
		seenHooks[hook.HookID] = struct{}{}
		if hook.Phase == "" {
			return agentpackage.CompileError{Path: path + ".phase", Message: "phase is required"}
		}
		if !validManifestHookPhase(hook.Phase) {
			return agentpackage.CompileError{Path: path + ".phase", Message: "unsupported hook phase"}
		}
		if hook.Status != "" && hook.Status != runtimehook.StatusEnabled && hook.Status != runtimehook.StatusDisabled {
			return agentpackage.CompileError{Path: path + ".status", Message: "unsupported hook status"}
		}
		if hook.TimeoutMS < 0 {
			return agentpackage.CompileError{Path: path + ".timeout_ms", Message: "timeout_ms cannot be negative"}
		}
		if hook.FailurePolicy != "" && hook.FailurePolicy != "ignore" && hook.FailurePolicy != "reject" {
			return agentpackage.CompileError{Path: path + ".failure_policy", Message: "unsupported hook failure_policy"}
		}
	}
	return nil
}

func validateManifestSchema(path string, schema map[string]any) error {
	if schema == nil {
		return agentpackage.CompileError{Path: path, Message: "schema is required"}
	}
	rawType, ok := schema["type"]
	if !ok {
		return agentpackage.CompileError{Path: path + ".type", Message: "type is required"}
	}
	switch typed := rawType.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return agentpackage.CompileError{Path: path + ".type", Message: "type is required"}
		}
	case []any:
		if len(typed) == 0 {
			return agentpackage.CompileError{Path: path + ".type", Message: "type is required"}
		}
		for i, item := range typed {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return agentpackage.CompileError{Path: fmt.Sprintf("%s.type[%d]", path, i), Message: "type must be a non-empty string"}
			}
		}
	default:
		return agentpackage.CompileError{Path: path + ".type", Message: "type must be a string or string array"}
	}
	return nil
}

func validManifestHookPhase(phase runtimehook.HookPoint) bool {
	switch phase {
	case runtimehook.BeforeContextBuild, runtimehook.AfterCandidateRetrieval, runtimehook.BeforeModelCall, runtimehook.BeforeMemoryWrite:
		return true
	default:
		return false
	}
}

func ToolManifests(providerID string, manifest AgentPluginManifest) []toolcatalog.ToolManifest {
	manifest = NormalizeManifest(manifest)
	providerID = firstNonEmpty(providerID, manifest.ProviderID)
	out := make([]toolcatalog.ToolManifest, 0, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		if tool.ToolID == "" {
			continue
		}
		operation := firstNonEmpty(tool.Operation, tool.ToolID)
		risk := tool.RiskLevel
		if risk == "" {
			risk = contracts.RiskLow
		}
		visibility := tool.Visibility
		if visibility == "" {
			visibility = contracts.ToolProtected
		}
		version := tool.Version
		if version == "" {
			version = manifest.ManifestVersion
		}
		out = append(out, toolcatalog.ToolManifest{
			ToolID:       tool.ToolID,
			GroupID:      tool.GroupID,
			Name:         firstNonEmpty(tool.Name, tool.ToolID),
			Description:  tool.Description,
			WhenToUse:    append([]string(nil), tool.WhenToUse...),
			InputSchema:  cloneMap(tool.InputSchema),
			OutputSchema: cloneMap(tool.OutputSchema),
			RiskLevel:    risk,
			Visibility:   visibility,
			Executor: toolcatalog.ExecutorSpec{
				Type:       toolcatalog.ExecutorTypeAgentPlugin,
				ProviderID: providerID,
				Operation:  operation,
			},
			Status:  toolcatalog.StatusEnabled,
			Version: version,
		})
	}
	return out
}

func normalizeTool(tool AgentPluginToolManifest) AgentPluginToolManifest {
	tool.ToolID = strings.TrimSpace(tool.ToolID)
	tool.Operation = strings.TrimSpace(tool.Operation)
	tool.GroupID = strings.TrimSpace(tool.GroupID)
	tool.Name = strings.TrimSpace(tool.Name)
	tool.Description = strings.TrimSpace(tool.Description)
	tool.Version = strings.TrimSpace(tool.Version)
	return tool
}

func normalizeHook(hook runtimehook.HookManifest, providerID string) runtimehook.HookManifest {
	hook.HookID = strings.TrimSpace(hook.HookID)
	hook.ProviderID = firstNonEmpty(hook.ProviderID, providerID)
	hook.Name = strings.TrimSpace(hook.Name)
	hook.Description = strings.TrimSpace(hook.Description)
	hook.Version = strings.TrimSpace(hook.Version)
	hook.Status = strings.TrimSpace(hook.Status)
	hook.FailurePolicy = strings.TrimSpace(hook.FailurePolicy)
	return hook
}

func toolBindingsForManifest(tools []AgentPluginToolManifest) contracts.AgentToolsConfig {
	ids := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.ToolID) != "" {
			ids = append(ids, strings.TrimSpace(tool.ToolID))
		}
	}
	return contracts.AgentToolsConfig{AllowedToolIDs: ids}
}

func runtimeHooksForManifest(providerID string, hooks []runtimehook.HookManifest) contracts.AgentRuntimeHooks {
	if len(hooks) == 0 {
		return contracts.AgentRuntimeHooks{}
	}
	bindings := make([]contracts.AgentRuntimeHookBinding, 0, len(hooks))
	for _, hook := range hooks {
		if strings.TrimSpace(hook.HookID) == "" {
			continue
		}
		bindings = append(bindings, contracts.AgentRuntimeHookBinding{
			HookID:           strings.TrimSpace(hook.HookID),
			ProviderType:     "static_hook_host",
			ProviderID:       firstNonEmpty(hook.ProviderID, providerID),
			Phase:            string(hook.Phase),
			Version:          hook.Version,
			Enabled:          hook.Status == "" || hook.Status == runtimehook.StatusEnabled,
			TimeoutMS:        hook.TimeoutMS,
			FailurePolicy:    hook.FailurePolicy,
			RequiresApproval: hook.RequiresApproval,
			ApprovalPolicy:   contractRuntimeHookApprovalPolicy(hook.ApprovalPolicy),
		})
	}
	if len(bindings) == 0 {
		return contracts.AgentRuntimeHooks{}
	}
	return contracts.AgentRuntimeHooks{Mode: "plugin_hooks", Hooks: bindings}
}

func contractRuntimeHookApprovalPolicy(policy runtimehook.ApprovalPolicy) contracts.RuntimeHookApprovalPolicy {
	out := contracts.RuntimeHookApprovalPolicy{
		RequireApproval: policy.RequireApproval,
		FailurePolicies: append([]string(nil), policy.FailurePolicies...),
	}
	for _, providerType := range policy.ProviderTypes {
		out.ProviderTypes = append(out.ProviderTypes, string(providerType))
	}
	for _, phase := range policy.Phases {
		out.Phases = append(out.Phases, string(phase))
	}
	return out
}

func manifestMetadata(manifest AgentPluginManifest) map[string]any {
	metadata := map[string]any{}
	if manifest.Agent.Name != "" {
		metadata["name"] = manifest.Agent.Name
	}
	if manifest.Agent.Description != "" {
		metadata["description"] = manifest.Agent.Description
	}
	return metadata
}

func mergeMetadata(base map[string]any, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func mergeToolBindings(base contracts.AgentToolsConfig, override contracts.AgentToolsConfig) contracts.AgentToolsConfig {
	if !reflect.DeepEqual(override, contracts.AgentToolsConfig{}) {
		return override
	}
	return base
}

func mergeStrategies(base contracts.AgentStrategies, override contracts.AgentStrategies) contracts.AgentStrategies {
	if reflect.DeepEqual(override, contracts.AgentStrategies{}) {
		return base
	}
	if !reflect.DeepEqual(override.Prompt, contracts.PromptStrategy{}) {
		base.Prompt = override.Prompt
	}
	if !reflect.DeepEqual(override.Model, contracts.ModelStrategy{}) {
		base.Model = override.Model
	}
	if !reflect.DeepEqual(override.Context, contracts.ContextStrategy{}) {
		base.Context = override.Context
	}
	if !reflect.DeepEqual(override.Tools, contracts.ToolUseStrategy{}) {
		base.Tools = override.Tools
	}
	if !reflect.DeepEqual(override.Skills, contracts.SkillUseStrategy{}) {
		base.Skills = override.Skills
	}
	if !reflect.DeepEqual(override.Collaboration, contracts.CollaborationStrategy{}) {
		base.Collaboration = override.Collaboration
	}
	if !reflect.DeepEqual(override.Memory, contracts.MemoryUseStrategy{}) {
		base.Memory = override.Memory
	}
	if !reflect.DeepEqual(override.Knowledge, contracts.KnowledgeUseStrategy{}) {
		base.Knowledge = override.Knowledge
	}
	if !reflect.DeepEqual(override.Runtime, contracts.RuntimeStrategy{}) {
		base.Runtime = override.Runtime
	}
	if !reflect.DeepEqual(override.Repair, contracts.RepairStrategy{}) {
		base.Repair = override.Repair
	}
	if !reflect.DeepEqual(override.Output, contracts.OutputStrategy{}) {
		base.Output = override.Output
	}
	return base
}

func firstCollaborators(override []contracts.AgentCollaboratorRef, base []contracts.AgentCollaboratorRef) []contracts.AgentCollaboratorRef {
	if len(override) > 0 {
		return append([]contracts.AgentCollaboratorRef(nil), override...)
	}
	return append([]contracts.AgentCollaboratorRef(nil), base...)
}

func firstExports(override contracts.AgentExports, base contracts.AgentExports) contracts.AgentExports {
	if !reflect.DeepEqual(override, contracts.AgentExports{}) {
		return override
	}
	return base
}

func firstRuntimeHooks(override contracts.AgentRuntimeHooks, base contracts.AgentRuntimeHooks) contracts.AgentRuntimeHooks {
	if !reflect.DeepEqual(override, contracts.AgentRuntimeHooks{}) {
		return override
	}
	return base
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
