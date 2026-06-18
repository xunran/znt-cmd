package server

import (
	"encoding/json"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/auth"
	"znt/internal/contracts"
	taskplan "znt/internal/task/plan"
)

func parsePlanSteps(value any) []taskplan.StepInput {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	steps := make([]taskplan.StepInput, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title, _ := row["title"].(string)
		description, _ := row["description"].(string)
		steps = append(steps, taskplan.StepInput{
			Title:             title,
			Description:       description,
			ExpectedToolHints: stringSlice(row["expected_tool_hints"]),
		})
	}
	return steps
}

func parseArtifactRefs(value any) []contracts.ArtifactRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.ArtifactRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		artifactID, _ := row["artifact_id"].(string)
		artifactType, _ := row["type"].(string)
		uri, _ := row["uri"].(string)
		summary, _ := row["summary"].(string)
		refHash, _ := row["hash"].(string)
		refs = append(refs, contracts.ArtifactRef{
			ArtifactID: contracts.ArtifactID(artifactID),
			Type:       artifactType,
			URI:        uri,
			Summary:    summary,
			Hash:       refHash,
			Metadata:   parseMetadata(row["metadata"]),
		})
	}
	return refs
}

func parseToolsPayload(value any) contracts.AgentToolsConfig {
	if raw, ok := value.(map[string]any); ok {
		return parseToolsConfig(raw)
	}
	return contracts.AgentToolsConfig{}
}

func parseCollaboratorsPayload(value any) []contracts.AgentCollaboratorRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contracts.AgentCollaboratorRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contracts.AgentCollaboratorRef{
			AgentID:             contracts.AgentID(payloadString(row, "agent_id")),
			Version:             contracts.AgentVersion(payloadString(row, "version")),
			Alias:               payloadString(row, "alias"),
			Name:                payloadString(row, "name"),
			Description:         payloadString(row, "description"),
			WhenToUse:           stringSlice(row["when_to_use"]),
			Capabilities:        stringSlice(row["capabilities"]),
			AllowedHandoffModes: handoffModeSlice(row["allowed_handoff_modes"]),
			DefaultHandoffMode:  contracts.HandoffMode(payloadString(row, "default_handoff_mode")),
			MaxContextTokens:    intValue(row["max_context_tokens"], 0),
			RequiresApproval:    boolValue(row["requires_approval"], false),
			Status:              payloadString(row, "status"),
		})
	}
	return out
}

func parseCollaboratorPayload(payload map[string]any) contracts.AgentCollaboratorRef {
	return contracts.AgentCollaboratorRef{
		AgentID:             contracts.AgentID(payloadString(payload, "agent_id")),
		Version:             contracts.AgentVersion(payloadString(payload, "version")),
		Alias:               payloadString(payload, "alias"),
		Name:                payloadString(payload, "name"),
		Description:         payloadString(payload, "description"),
		WhenToUse:           stringSlice(payload["when_to_use"]),
		Capabilities:        stringSlice(payload["capabilities"]),
		AllowedHandoffModes: handoffModeSlice(payload["allowed_handoff_modes"]),
		DefaultHandoffMode:  contracts.HandoffMode(payloadString(payload, "default_handoff_mode")),
		MaxContextTokens:    intValue(payload["max_context_tokens"], 0),
		RequiresApproval:    boolValue(payload["requires_approval"], false),
		Status:              payloadString(payload, "status"),
	}
}

func parseAgentExportsPayload(value any) contracts.AgentExports {
	raw, ok := value.(map[string]any)
	if !ok {
		return contracts.AgentExports{}
	}
	toolsRaw, _ := raw["tools"].([]any)
	tools := make([]contracts.AgentExportedTool, 0, len(toolsRaw))
	for _, item := range toolsRaw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tools = append(tools, contracts.AgentExportedTool{
			ToolID:       payloadString(row, "tool_id"),
			GroupID:      payloadString(row, "group_id"),
			Operation:    payloadString(row, "operation"),
			Name:         payloadString(row, "name"),
			Description:  payloadString(row, "description"),
			WhenToUse:    stringSlice(row["when_to_use"]),
			InputSchema:  parseMetadata(row["input_schema"]),
			OutputSchema: parseMetadata(row["output_schema"]),
			RiskLevel:    contracts.RiskLevel(payloadString(row, "risk_level")),
			Visibility:   contracts.ToolVisibility(payloadString(row, "visibility")),
			Status:       payloadString(row, "status"),
			Version:      payloadString(row, "version"),
		})
	}
	return contracts.AgentExports{Tools: tools}
}

func rejectResourceEnvelopePayload(payload map[string]any, fields ...string) error {
	for _, field := range fields {
		if _, ok := payload[field]; ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "resource envelope payload is not supported; submit fields at top level", map[string]any{"field": field})
		}
	}
	return nil
}

func parseExportedToolPayload(payload map[string]any) contracts.AgentExportedTool {
	return contracts.AgentExportedTool{
		ToolID:       payloadString(payload, "tool_id"),
		GroupID:      payloadString(payload, "group_id"),
		Operation:    payloadString(payload, "operation"),
		Name:         payloadString(payload, "name"),
		Description:  payloadString(payload, "description"),
		WhenToUse:    stringSlice(payload["when_to_use"]),
		InputSchema:  parseMetadata(payload["input_schema"]),
		OutputSchema: parseMetadata(payload["output_schema"]),
		RiskLevel:    contracts.RiskLevel(payloadString(payload, "risk_level")),
		Visibility:   contracts.ToolVisibility(payloadString(payload, "visibility")),
		Status:       payloadString(payload, "status"),
		Version:      payloadString(payload, "version"),
	}
}

func skillDraftInput(payload map[string]any) (agentpackage.SkillDraftInput, error) {
	input := agentpackage.SkillDraftInput{
		SkillID:                 payloadString(payload, "skill_id"),
		Version:                 payloadString(payload, "skill_version"),
		Name:                    payloadString(payload, "name"),
		Description:             payloadString(payload, "description"),
		Instruction:             payloadString(payload, "instruction"),
		Status:                  payloadString(payload, "status"),
		RiskLevel:               contracts.RiskLevel(payloadString(payload, "risk_level")),
		Tags:                    stringSlice(payload["tags"]),
		WhenToUse:               stringSlice(payload["when_to_use"]),
		OutputRequirements:      stringSlice(payload["output_requirements"]),
		Constraints:             stringSlice(payload["constraints"]),
		Resources:               parseSkillResources(payload["resources"]),
		RecommendedTools:        stringSlice(payload["recommended_tools"]),
		AllowedTools:            stringSlice(payload["allowed_tools"]),
		RecommendedMemoryReads:  stringSlice(payload["recommended_memory_reads"]),
		RecommendedMemoryWrites: stringSlice(payload["recommended_memory_writes"]),
		RecommendedHandoffs:     stringSlice(payload["recommended_handoffs"]),
		CompletionCriteria:      stringSlice(payload["completion_criteria"]),
		OutputSchema:            parseMetadata(payload["output_schema"]),
	}
	if input.Version == "" {
		input.Version = payloadString(payload, "version")
	}
	if input.SkillID == "" {
		return input, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.package.skill command requires skill_id", nil)
	}
	return input, nil
}

func skillDefinitionFromDraftInput(input agentpackage.SkillDraftInput) (contracts.SkillDefinition, error) {
	if input.SkillID == "" {
		return contracts.SkillDefinition{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill definition requires skill_id", nil)
	}
	if input.Version == "" {
		input.Version = "v1"
	}
	if input.Name == "" {
		input.Name = input.SkillID
	}
	if input.Description == "" {
		input.Description = input.Name
	}
	if input.RiskLevel == "" {
		input.RiskLevel = contracts.RiskLow
	}
	if err := input.RiskLevel.Validate(); err != nil {
		return contracts.SkillDefinition{}, err
	}
	for _, resource := range input.Resources {
		if resource.ResourceID == "" || resource.Type == "" || resource.URI == "" {
			return contracts.SkillDefinition{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill resource requires resource_id, type, and uri", map[string]any{"skill_id": input.SkillID})
		}
	}
	return contracts.SkillDefinition{
		Card: contracts.SkillCard{
			SkillID:      input.SkillID,
			Version:      input.Version,
			Name:         input.Name,
			Description:  input.Description,
			Tags:         input.Tags,
			Status:       input.Status,
			WhenToUse:    input.WhenToUse,
			RiskLevel:    input.RiskLevel,
			ResourceRefs: skillResourceIDs(input.Resources),
		},
		Instruction: contracts.SkillInstruction{
			SkillID:            input.SkillID,
			Content:            input.Instruction,
			OutputRequirements: input.OutputRequirements,
			Constraints:        input.Constraints,
		},
		Resources:               input.Resources,
		RecommendedTools:        input.RecommendedTools,
		AllowedTools:            input.AllowedTools,
		RecommendedMemoryReads:  input.RecommendedMemoryReads,
		RecommendedMemoryWrites: input.RecommendedMemoryWrites,
		RecommendedHandoffs:     input.RecommendedHandoffs,
		CompletionCriteria:      input.CompletionCriteria,
		OutputSchema:            input.OutputSchema,
	}, nil
}

func parseSkillRefsPayload(value any) ([]contracts.SkillDefinitionRef, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var refs []contracts.SkillDefinitionRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid skills payload", map[string]any{"error": err.Error()})
	}
	return refs, nil
}

func parseSkillDefinitionsPayload(value any) ([]contracts.SkillDefinition, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill_definitions must be an array", nil)
	}
	out := make([]contracts.SkillDefinition, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "skill_definitions entries must be objects", nil)
		}
		if _, structured := row["card"]; structured {
			data, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			var definition contracts.SkillDefinition
			if err := json.Unmarshal(data, &definition); err != nil {
				return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid skill definition payload", map[string]any{"error": err.Error()})
			}
			out = append(out, definition)
			continue
		}
		input, err := skillDraftInput(row)
		if err != nil {
			return nil, err
		}
		definition, err := skillDefinitionFromDraftInput(input)
		if err != nil {
			return nil, err
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

func parseSkillResources(value any) []contracts.SkillResourceRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contracts.SkillResourceRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contracts.SkillResourceRef{
			ResourceID: payloadString(row, "resource_id"),
			Type:       payloadString(row, "type"),
			URI:        payloadString(row, "uri"),
			LoadPolicy: payloadString(row, "load_policy"),
		})
	}
	return out
}

func parseToolsConfig(raw map[string]any) contracts.AgentToolsConfig {
	return contracts.AgentToolsConfig{
		AllowedToolIDs:      stringSlice(raw["allowed_tool_ids"]),
		AllowedToolGroupIDs: stringSlice(raw["allowed_tool_group_ids"]),
		ExposedToolIDs:      stringSlice(raw["exposed_tool_ids"]),
		DeniedToolIDs:       stringSlice(raw["denied_tool_ids"]),
		DeniedToolGroupIDs:  stringSlice(raw["denied_tool_group_ids"]),
	}
}

func parseMetadata(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return raw
}

func parseStringMap(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func handoffModeSlice(value any) []contracts.HandoffMode {
	raw := stringSlice(value)
	if len(raw) == 0 {
		return nil
	}
	out := make([]contracts.HandoffMode, 0, len(raw))
	for _, item := range raw {
		out = append(out, contracts.HandoffMode(item))
	}
	return out
}

func parseMemoryRefs(value any) []contracts.MemoryID {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.MemoryID, 0, len(raw))
	for _, item := range raw {
		if id, ok := item.(string); ok && id != "" {
			refs = append(refs, contracts.MemoryID(id))
		}
	}
	return refs
}

func parseExpectedOutput(value any) contracts.ExpectedOutput {
	row, ok := value.(map[string]any)
	if !ok {
		return contracts.ExpectedOutput{}
	}
	format, _ := row["format"].(string)
	return contracts.ExpectedOutput{
		Format:       format,
		Requirements: stringSlice(row["requirements"]),
	}
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadInt(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func payloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func callerRoles(caller auth.CallerIdentity) []string {
	out := make([]string, 0, len(caller.Roles))
	for _, role := range caller.Roles {
		out = append(out, string(role))
	}
	return out
}

func mapPayload(value any) map[string]any {
	if value == nil {
		return nil
	}
	if parsed, ok := value.(map[string]any); ok {
		out := map[string]any{}
		for key, current := range parsed {
			out[key] = current
		}
		return out
	}
	return nil
}

func knowledgeBaseIDsFromAny(value any) []contracts.KnowledgeBaseID {
	values := stringSlice(value)
	out := make([]contracts.KnowledgeBaseID, 0, len(values))
	for _, value := range values {
		out = append(out, contracts.KnowledgeBaseID(value))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func boolValue(value any, fallback bool) bool {
	parsed, ok := value.(bool)
	if !ok {
		return fallback
	}
	return parsed
}

func intValue(value any, fallback int) int {
	switch current := value.(type) {
	case int:
		return current
	case int64:
		return int(current)
	case float64:
		return int(current)
	case json.Number:
		parsed, err := current.Int64()
		if err != nil {
			return fallback
		}
		return int(parsed)
	default:
		return fallback
	}
}

func payloadFloat(payload map[string]any, key string) float64 {
	return floatFromAny(payload[key])
}

func boolFromAny(value any) bool {
	parsed, _ := value.(bool)
	return parsed
}

func floatFromAny(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		parsed, _ := v.Float64()
		return parsed
	default:
		return 0
	}
}
