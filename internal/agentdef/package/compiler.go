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
	if agentID == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "agent.yaml", Message: "agent_id is required"}
	}
	if version == "" {
		return contracts.AgentDefinition{}, CompileError{Path: "agent.yaml", Message: "version is required"}
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
	exports := source.Exports
	if err := validateExports(&exports); err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition := contracts.AgentDefinition{
		AgentID:         agentID,
		Version:         version,
		Name:            metadataString(source.Metadata, "name", string(agentID)),
		Description:     metadataString(source.Metadata, "description", firstLine(source.AgentsMD, "Published agent package definition.")),
		IdentityPrompt:  strings.TrimSpace(source.Prompt),
		SystemPrompt:    metadataString(source.Metadata, "system_prompt", "Return decisions as JSON."),
		DeveloperPrompt: metadataString(source.Metadata, "developer_prompt", ""),
		Tools:           source.ToolBindings,
		Collaborators:   source.Collaborators,
		Exports:         exports,
		RuntimeHooks:    source.RuntimeHooks,
		PolicyRefs: contracts.AgentPolicyRefs{
			PolicySetID: contracts.PolicySetID(metadataString(source.Metadata, "policy_set_id", "policy_default")),
		},
		Runtime: contracts.RuntimeLimits{
			MaxSteps:                   metadataInt(source.Metadata, "max_steps", 4),
			MaxToolCalls:               metadataInt(source.Metadata, "max_tool_calls", 2),
			MaxDuration:                time.Duration(metadataInt(source.Metadata, "max_duration_seconds", 60)) * time.Second,
			MaxPromptTokens:            metadataInt(source.Metadata, "max_prompt_tokens", 4000),
			MaxHandoffDepth:            metadataInt(source.Metadata, "max_handoff_depth", 0),
			MaxChildTasks:              metadataInt(source.Metadata, "max_child_tasks", 0),
			MaxModelRetries:            metadataInt(source.Metadata, "max_model_retries", 1),
			MaxRepairAttempts:          metadataInt(source.Metadata, "max_repair_attempts", 1),
			MaxConsecutiveToolFailures: metadataInt(source.Metadata, "max_consecutive_tool_failures", 2),
		},
		ContractVersion: "v1.0-alpha",
		CreatedAt:       time.Now().UTC(),
	}
	if definition.IdentityPrompt == "" {
		definition.IdentityPrompt = strings.TrimSpace(source.AgentsMD)
	}
	if err := validateRuntimeLimits(definition.Runtime); err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition.Skills = metadataSkills(source.Metadata)
	skillDefinitions, err := metadataSkillDefinitions(source.Metadata)
	if err != nil {
		return contracts.AgentDefinition{}, err
	}
	definition.SkillDefinitions = skillDefinitions
	return definition, nil
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

func metadataInt(metadata map[string]any, key string, fallback int) int {
	if metadata == nil {
		return fallback
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case jsonNumber:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return fallback
}

type jsonNumber interface {
	Int64() (int64, error)
}

func metadataSkills(metadata map[string]any) []contracts.SkillDefinitionRef {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata["skills"].([]any)
	if !ok {
		return nil
	}
	out := make([]contracts.SkillDefinitionRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		skillID, _ := row["skill_id"].(string)
		version, _ := row["version"].(string)
		if skillID == "" {
			continue
		}
		out = append(out, contracts.SkillDefinitionRef{SkillID: skillID, Version: version})
	}
	return out
}

func metadataSkillDefinitions(metadata map[string]any) ([]contracts.SkillDefinition, error) {
	if metadata == nil {
		return nil, nil
	}
	raw, ok := metadata["skill_definitions"].([]any)
	if !ok {
		raw, ok = metadata["skills"].([]any)
		if !ok {
			return nil, nil
		}
	}
	out := make([]contracts.SkillDefinition, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		skillID, _ := row["skill_id"].(string)
		if strings.TrimSpace(skillID) == "" {
			continue
		}
		version, _ := row["version"].(string)
		identity := skillID + "@" + version
		if _, ok := seen[identity]; ok {
			return nil, CompileError{Path: "skills", Message: fmt.Sprintf("duplicate skill definition %q", identity)}
		}
		seen[identity] = struct{}{}
		name := metadataRowString(row, "name", skillID)
		description := metadataRowString(row, "description", name)
		instruction := metadataRowString(row, "instruction", "")
		if instruction == "" {
			instruction = metadataRowString(row, "content", "")
		}
		riskLevel := contracts.RiskLevel(metadataRowString(row, "risk_level", string(contracts.RiskLow)))
		if err := riskLevel.Validate(); err != nil {
			return nil, CompileError{Path: "skills." + skillID + ".risk_level", Message: err.Error()}
		}
		resources, err := metadataResourceRefs(skillID, row["resources"])
		if err != nil {
			return nil, err
		}
		out = append(out, contracts.SkillDefinition{
			Card: contracts.SkillCard{
				SkillID:      skillID,
				Version:      version,
				Name:         name,
				Description:  description,
				Tags:         metadataStringSlice(row["tags"]),
				WhenToUse:    metadataStringSlice(row["when_to_use"]),
				RiskLevel:    riskLevel,
				ResourceRefs: skillResourceIDs(resources),
			},
			Instruction: contracts.SkillInstruction{
				SkillID:            skillID,
				Content:            instruction,
				OutputRequirements: metadataStringSlice(row["output_requirements"]),
				Constraints:        metadataStringSlice(row["constraints"]),
			},
			Resources:               resources,
			RecommendedTools:        metadataStringSlice(row["recommended_tools"]),
			AllowedTools:            metadataStringSlice(row["allowed_tools"]),
			RecommendedMemoryReads:  metadataStringSlice(row["recommended_memory_reads"]),
			RecommendedMemoryWrites: metadataStringSlice(row["recommended_memory_writes"]),
			RecommendedHandoffs:     metadataStringSlice(row["recommended_handoffs"]),
			CompletionCriteria:      metadataStringSlice(row["completion_criteria"]),
			OutputSchema:            metadataMap(row["output_schema"]),
		})
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

func metadataRowString(row map[string]any, key string, fallback string) string {
	if value, ok := row[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func metadataStringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func metadataMap(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(raw))
	for key, item := range raw {
		out[key] = item
	}
	return out
}

func metadataResourceRefs(skillID string, value any) ([]contracts.SkillResourceRef, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]contracts.SkillResourceRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		resource := contracts.SkillResourceRef{
			ResourceID: metadataRowString(row, "resource_id", ""),
			Type:       metadataRowString(row, "type", ""),
			URI:        metadataRowString(row, "uri", ""),
			LoadPolicy: metadataRowString(row, "load_policy", ""),
		}
		if resource.ResourceID == "" || resource.Type == "" || resource.URI == "" {
			return nil, CompileError{Path: "skills." + skillID + ".resources", Message: "resource_id, type, and uri are required"}
		}
		out = append(out, resource)
	}
	return out, nil
}
