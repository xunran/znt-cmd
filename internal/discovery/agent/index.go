package agent

import "znt/internal/contracts"

type CapabilityIndex struct {
	Agents []AgentCapability `json:"agents"`
}

type AgentCapability struct {
	AgentID      contracts.AgentID          `json:"agent_id"`
	Version      contracts.AgentVersion     `json:"version"`
	Name         string                     `json:"name"`
	Description  string                     `json:"description"`
	PolicySetID  contracts.PolicySetID      `json:"policy_set_id"`
	Skills       []string                   `json:"skills,omitempty"`
	Tools        []string                   `json:"tools,omitempty"`
	Capabilities []contracts.CapabilityCard `json:"capabilities,omitempty"`
}

func BuildIndex(definitions []contracts.AgentDefinition) CapabilityIndex {
	out := CapabilityIndex{Agents: make([]AgentCapability, 0, len(definitions))}
	for _, definition := range definitions {
		skills := make([]string, 0, len(definition.Skills)+len(definition.SkillDefinitions))
		for _, ref := range definition.Skills {
			if ref.SkillID != "" {
				skills = append(skills, ref.SkillID+"@"+ref.Version)
			}
		}
		for _, skill := range definition.SkillDefinitions {
			if skill.Card.SkillID != "" {
				skills = append(skills, skill.Card.SkillID+"@"+skill.Card.Version)
			}
		}
		tools := append([]string{}, definition.Tools.AllowedToolIDs...)
		out.Agents = append(out.Agents, AgentCapability{
			AgentID:      definition.AgentID,
			Version:      definition.Version,
			Name:         definition.Name,
			Description:  definition.Description,
			PolicySetID:  definition.PolicyRefs.PolicySetID,
			Skills:       skills,
			Tools:        tools,
			Capabilities: capabilitiesFromDefinition(definition),
		})
	}
	return out
}

func capabilitiesFromDefinition(definition contracts.AgentDefinition) []contracts.CapabilityCard {
	out := make([]contracts.CapabilityCard, 0)
	if definition.Description != "" {
		out = append(out, contracts.CapabilityCard{
			ID:          string(definition.AgentID),
			Type:        "agent",
			Name:        definition.Name,
			Description: definition.Description,
		})
	}
	for _, skill := range definition.SkillDefinitions {
		out = append(out, contracts.CapabilityCard{
			ID:          skill.Card.SkillID,
			Type:        "skill",
			Name:        skill.Card.Name,
			Description: skill.Card.Description,
			Tags:        skill.Card.Tags,
		})
	}
	return out
}
