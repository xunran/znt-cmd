package tool

import (
	"sort"
	"strings"

	"znt/internal/contracts"
)

func ApplyToolUseStrategy(candidates CandidateSet, strategy contracts.ToolUseStrategy) CandidateSet {
	mode := strings.TrimSpace(strategy.ToolChoiceMode)
	if mode == "no_tools" {
		candidates.Tools = nil
		candidates.Capabilities = filterNonToolCapabilities(candidates.Capabilities)
		return candidates
	}
	if mode == "tool_first" {
		sort.SliceStable(candidates.Capabilities, func(i, j int) bool {
			leftTool := candidates.Capabilities[i].Type == "tool"
			rightTool := candidates.Capabilities[j].Type == "tool"
			return leftTool && !rightTool
		})
	}
	preferred := map[string]struct{}{}
	for _, id := range strategy.PreferredToolIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			preferred[id] = struct{}{}
		}
	}
	if len(preferred) == 0 && mode != "conservative" {
		return candidates
	}
	sort.SliceStable(candidates.Tools, func(i, j int) bool {
		leftPreferred := toolPreferred(candidates.Tools[i], preferred)
		rightPreferred := toolPreferred(candidates.Tools[j], preferred)
		if leftPreferred != rightPreferred {
			return leftPreferred
		}
		if mode == "conservative" {
			leftRisk := toolRiskRank(candidates.Tools[i].RiskLevel)
			rightRisk := toolRiskRank(candidates.Tools[j].RiskLevel)
			if leftRisk != rightRisk {
				return leftRisk < rightRisk
			}
		}
		return false
	})
	return candidates
}

func ApplySkillUseStrategy(candidates CandidateSet, strategy contracts.SkillUseStrategy) CandidateSet {
	enabled := stringSet(strategy.EnabledSkillIDs)
	disabled := stringSet(strategy.DisabledSkillIDs)
	explicitOnly := strings.TrimSpace(strategy.SelectionMode) == "explicit_only"
	if len(enabled) == 0 && len(disabled) == 0 && !explicitOnly && strategy.MaxSelectedSkills <= 0 {
		return candidates
	}
	skills := make([]contracts.SkillCard, 0, len(candidates.Skills))
	known := map[string]struct{}{}
	selected := map[string]struct{}{}
	for _, skill := range candidates.Skills {
		id := strings.TrimSpace(skill.SkillID)
		if id == "" {
			continue
		}
		known[id] = struct{}{}
		if strings.TrimSpace(skill.Status) == "disabled" {
			continue
		}
		if _, ok := disabled[id]; ok {
			continue
		}
		if len(enabled) > 0 {
			if _, ok := enabled[id]; !ok {
				continue
			}
		} else if explicitOnly {
			continue
		}
		if strategy.MaxSelectedSkills > 0 && len(skills) >= strategy.MaxSelectedSkills {
			continue
		}
		skills = append(skills, skill)
		selected[id] = struct{}{}
	}
	candidates.Skills = skills
	candidates.SkillInstructions = filterSkillInstructions(candidates.SkillInstructions, selected)
	candidates.Capabilities = filterSkillCapabilities(candidates.Capabilities, selected, known, enabled, disabled, explicitOnly)
	return candidates
}

func ApplyKnowledgeUseStrategy(candidates CandidateSet, strategy contracts.KnowledgeUseStrategy) CandidateSet {
	if strategy.Enabled == nil || *strategy.Enabled {
		return candidates
	}
	tools := make([]contracts.ToolCard, 0, len(candidates.Tools))
	for _, tool := range candidates.Tools {
		if isKnowledgeToolID(tool.ToolID) {
			continue
		}
		tools = append(tools, tool)
	}
	candidates.Tools = tools
	capabilities := make([]contracts.CapabilityCard, 0, len(candidates.Capabilities))
	for _, capability := range candidates.Capabilities {
		if capability.Type == "tool" && isKnowledgeToolID(capability.ID) {
			continue
		}
		capabilities = append(capabilities, capability)
	}
	candidates.Capabilities = capabilities
	return candidates
}

func isKnowledgeToolID(toolID string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolID), "origin.knowledge.")
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func filterNonToolCapabilities(capabilities []contracts.CapabilityCard) []contracts.CapabilityCard {
	out := make([]contracts.CapabilityCard, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability.Type != "tool" {
			out = append(out, capability)
		}
	}
	return out
}

func filterSkillInstructions(instructions []contracts.SkillInstruction, selected map[string]struct{}) []contracts.SkillInstruction {
	if len(selected) == 0 {
		return nil
	}
	out := make([]contracts.SkillInstruction, 0, len(instructions))
	for _, instruction := range instructions {
		if _, ok := selected[strings.TrimSpace(instruction.SkillID)]; ok {
			out = append(out, instruction)
		}
	}
	return out
}

func filterSkillCapabilities(capabilities []contracts.CapabilityCard, selected map[string]struct{}, known map[string]struct{}, enabled map[string]struct{}, disabled map[string]struct{}, explicitOnly bool) []contracts.CapabilityCard {
	if len(capabilities) == 0 {
		return nil
	}
	out := make([]contracts.CapabilityCard, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability.Type != "skill" {
			out = append(out, capability)
			continue
		}
		id := skillCapabilityID(capability)
		if _, ok := disabled[id]; ok {
			continue
		}
		if len(enabled) > 0 {
			if _, ok := enabled[id]; !ok {
				continue
			}
		} else if explicitOnly {
			continue
		}
		if _, ok := known[id]; ok {
			if _, ok := selected[id]; ok {
				out = append(out, capability)
			}
			continue
		}
		out = append(out, capability)
	}
	return out
}

func skillCapabilityID(capability contracts.CapabilityCard) string {
	id := strings.TrimSpace(capability.ID)
	if strings.HasPrefix(id, "skill.") {
		return strings.TrimPrefix(id, "skill.")
	}
	return id
}

func toolPreferred(tool contracts.ToolCard, preferred map[string]struct{}) bool {
	if len(preferred) == 0 {
		return false
	}
	if _, ok := preferred[tool.ToolID]; ok {
		return true
	}
	_, ok := preferred[tool.Name]
	return ok
}

func toolRiskRank(level contracts.RiskLevel) int {
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
