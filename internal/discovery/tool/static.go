package tool

import (
	"context"
	"sort"
	"strings"

	"znt/internal/contracts"
)

type CandidateSet struct {
	Capabilities      []contracts.CapabilityCard   `json:"capabilities,omitempty"`
	Skills            []contracts.SkillCard        `json:"skills,omitempty"`
	SkillInstructions []contracts.SkillInstruction `json:"skill_instructions,omitempty"`
	Tools             []contracts.ToolCard         `json:"tools,omitempty"`
	Collaborators     []contracts.CollaboratorCard `json:"collaborators,omitempty"`
}

type CandidateProvider interface {
	Candidates(ctx context.Context, agent contracts.AgentDefinition, policy contracts.PolicySet, objective string) (CandidateSet, error)
}

type StaticCandidateProvider struct {
	Capabilities []contracts.CapabilityCard
	Skills       []contracts.SkillCard
	Cards        []contracts.ToolCard
	Registry     interface {
		Cards() []contracts.ToolCard
		CardsForTenant(tenantID contracts.TenantID) []contracts.ToolCard
	}
}

func (p StaticCandidateProvider) Candidates(_ context.Context, agent contracts.AgentDefinition, policy contracts.PolicySet, objective string) (CandidateSet, error) {
	tenantID := agent.TenantID
	if tenantID == "" {
		tenantID = policy.TenantID
	}
	skills, instructions := p.skillCandidates(agent, objective)
	collaborators := p.collaboratorCandidates(agent, objective)
	tools := p.toolCandidates(tenantID, agent, policy, objective, skills)
	capabilities := p.capabilityCandidates(objective, skills, tools)
	return CandidateSet{
		Capabilities:      capabilities,
		Skills:            skills,
		SkillInstructions: instructions,
		Tools:             tools,
		Collaborators:     collaborators,
	}, nil
}

func (p StaticCandidateProvider) skillCandidates(agent contracts.AgentDefinition, objective string) ([]contracts.SkillCard, []contracts.SkillInstruction) {
	allowed := map[string]string{}
	for _, ref := range agent.Skills {
		allowed[ref.SkillID] = ref.Version
	}
	out := make([]contracts.SkillCard, 0)
	instructionsByKey := map[string]contracts.SkillInstruction{}
	cards := make([]contracts.SkillCard, 0, len(agent.SkillDefinitions)+len(p.Skills))
	for _, definition := range agent.SkillDefinitions {
		cards = append(cards, definition.Card)
		if definition.Instruction.Content != "" {
			instructionsByKey[skillKey(definition.Card.SkillID, definition.Card.Version)] = definition.Instruction
		}
		if _, ok := allowed[definition.Card.SkillID]; !ok {
			allowed[definition.Card.SkillID] = definition.Card.Version
		}
	}
	cards = append(cards, p.Skills...)
	seen := map[string]struct{}{}
	for _, card := range cards {
		if _, ok := seen[card.SkillID+"@"+card.Version]; ok {
			continue
		}
		seen[card.SkillID+"@"+card.Version] = struct{}{}
		if len(allowed) > 0 {
			version, ok := allowed[card.SkillID]
			if !ok {
				continue
			}
			if version != "" && version != card.Version {
				continue
			}
		}
		score := matchScore(objective, card.Name, card.Description, append(card.Tags, card.WhenToUse...)...)
		if score == 0 && len(allowed) == 0 {
			continue
		}
		out = append(out, card)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := matchScore(objective, out[i].Name, out[i].Description, append(out[i].Tags, out[i].WhenToUse...)...)
		right := matchScore(objective, out[j].Name, out[j].Description, append(out[j].Tags, out[j].WhenToUse...)...)
		if left == right {
			return out[i].SkillID < out[j].SkillID
		}
		return left > right
	})
	instructions := make([]contracts.SkillInstruction, 0)
	for _, card := range out {
		if instruction, ok := instructionsByKey[skillKey(card.SkillID, card.Version)]; ok {
			instructions = append(instructions, instruction)
		}
	}
	return out, instructions
}

func skillKey(skillID string, version string) string {
	return skillID + "@" + version
}

func (p StaticCandidateProvider) collaboratorCandidates(agent contracts.AgentDefinition, objective string) []contracts.CollaboratorCard {
	out := make([]contracts.CollaboratorCard, 0, len(agent.Collaborators))
	for _, ref := range agent.Collaborators {
		if ref.AgentID == "" || ref.Status == "disabled" {
			continue
		}
		card := contracts.CollaboratorCard{
			AgentID:      ref.AgentID,
			Version:      ref.Version,
			Alias:        ref.Alias,
			Name:         ref.Name,
			Description:  ref.Description,
			WhenToUse:    ref.WhenToUse,
			Capabilities: ref.Capabilities,
		}
		if card.Name == "" {
			card.Name = string(ref.AgentID)
		}
		if len(agent.Collaborators) > 0 && matchScore(objective, card.Name, card.Description, append(card.WhenToUse, card.Capabilities...)...) == 0 {
			continue
		}
		out = append(out, card)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := matchScore(objective, out[i].Name, out[i].Description, append(out[i].WhenToUse, out[i].Capabilities...)...)
		right := matchScore(objective, out[j].Name, out[j].Description, append(out[j].WhenToUse, out[j].Capabilities...)...)
		if left == right {
			return out[i].AgentID < out[j].AgentID
		}
		return left > right
	})
	return out
}

func (p StaticCandidateProvider) toolCandidates(tenantID contracts.TenantID, agent contracts.AgentDefinition, policy contracts.PolicySet, objective string, skills []contracts.SkillCard) []contracts.ToolCard {
	agentAllowed := map[string]struct{}{}
	for _, id := range agent.Tools.AllowedToolIDs {
		agentAllowed[id] = struct{}{}
	}
	agentAllowedGroups := map[string]struct{}{}
	for _, id := range agent.Tools.AllowedToolGroupIDs {
		agentAllowedGroups[id] = struct{}{}
	}
	policyAllowed := map[string]struct{}{}
	for _, id := range policy.ToolPolicy.AllowedToolIDs {
		policyAllowed[id] = struct{}{}
	}
	policyAllowedGroups := map[string]struct{}{}
	for _, id := range policy.ToolPolicy.AllowedToolGroupIDs {
		policyAllowedGroups[id] = struct{}{}
	}
	denied := map[string]struct{}{}
	for _, id := range agent.Tools.DeniedToolIDs {
		denied[id] = struct{}{}
	}
	for _, id := range policy.ToolPolicy.DeniedToolIDs {
		denied[id] = struct{}{}
	}
	deniedGroups := map[string]struct{}{}
	for _, id := range agent.Tools.DeniedToolGroupIDs {
		deniedGroups[id] = struct{}{}
	}
	for _, id := range policy.ToolPolicy.DeniedToolGroupIDs {
		deniedGroups[id] = struct{}{}
	}
	skillTerms := make([]string, 0)
	for _, skill := range skills {
		skillTerms = append(skillTerms, skill.Tags...)
		skillTerms = append(skillTerms, skill.WhenToUse...)
	}
	skillAllowedTools, skillRecommendedTools := skillToolGuidance(agent, skills)
	groupBoosts := toolGroupBoosts(agentAllowedGroups, policyAllowedGroups)
	out := make([]contracts.ToolCard, 0)
	for _, card := range p.cards(tenantID) {
		if card.Visibility == contracts.ToolPrivate {
			continue
		}
		if _, ok := denied[card.ToolID]; ok {
			continue
		}
		if card.GroupID != "" {
			if _, ok := deniedGroups[card.GroupID]; ok {
				continue
			}
		}
		if len(agentAllowed) > 0 || len(agentAllowedGroups) > 0 {
			if _, ok := agentAllowed[card.ToolID]; !ok {
				if card.GroupID == "" {
					continue
				}
				if _, ok := agentAllowedGroups[card.GroupID]; !ok {
					continue
				}
			}
		}
		if len(policyAllowed) > 0 || len(policyAllowedGroups) > 0 {
			if _, ok := policyAllowed[card.ToolID]; !ok {
				if card.GroupID == "" {
					continue
				}
				if _, ok := policyAllowedGroups[card.GroupID]; !ok {
					continue
				}
			}
		}
		if len(skillAllowedTools) > 0 {
			if _, ok := skillAllowedTools[card.ToolID]; !ok {
				continue
			}
		}
		if len(agentAllowed) == 0 && len(agentAllowedGroups) == 0 && len(policyAllowed) == 0 && len(policyAllowedGroups) == 0 && toolCandidateScore(objective, card, skillTerms, skillRecommendedTools, skillAllowedTools, groupBoosts) == 0 {
			continue
		}
		out = append(out, card)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := toolCandidateScore(objective, out[i], skillTerms, skillRecommendedTools, skillAllowedTools, groupBoosts)
		right := toolCandidateScore(objective, out[j], skillTerms, skillRecommendedTools, skillAllowedTools, groupBoosts)
		if left == right {
			return out[i].ToolID < out[j].ToolID
		}
		return left > right
	})
	return out
}

func skillToolGuidance(agent contracts.AgentDefinition, skills []contracts.SkillCard) (map[string]struct{}, map[string]struct{}) {
	selected := map[string]struct{}{}
	for _, skill := range skills {
		selected[skillKey(skill.SkillID, skill.Version)] = struct{}{}
	}
	allowed := map[string]struct{}{}
	recommended := map[string]struct{}{}
	for _, definition := range agent.SkillDefinitions {
		if _, ok := selected[skillKey(definition.Card.SkillID, definition.Card.Version)]; !ok {
			continue
		}
		for _, id := range definition.AllowedTools {
			if id != "" {
				allowed[id] = struct{}{}
			}
		}
		for _, id := range definition.RecommendedTools {
			if id != "" {
				recommended[id] = struct{}{}
			}
		}
	}
	return allowed, recommended
}

func toolGroupBoosts(agentAllowedGroups map[string]struct{}, policyAllowedGroups map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for id := range agentAllowedGroups {
		out[id] = struct{}{}
	}
	for id := range policyAllowedGroups {
		out[id] = struct{}{}
	}
	return out
}

func toolCandidateScore(objective string, card contracts.ToolCard, skillTerms []string, recommended map[string]struct{}, allowed map[string]struct{}, groupBoosts map[string]struct{}) int {
	score := matchScore(objective, card.Name, card.Description, append(card.WhenToUse, skillTerms...)...)
	if _, ok := recommended[card.ToolID]; ok {
		score += 100
	}
	if _, ok := allowed[card.ToolID]; ok {
		score += 50
	}
	if card.GroupID != "" {
		if _, ok := groupBoosts[card.GroupID]; ok {
			score += 80
		}
		if matchScore(objective, card.GroupID, "") > 0 {
			score += 40
		}
	}
	return score
}

func (p StaticCandidateProvider) cards(tenantID contracts.TenantID) []contracts.ToolCard {
	if p.Registry != nil {
		return p.Registry.CardsForTenant(tenantID)
	}
	return p.Cards
}

func (p StaticCandidateProvider) capabilityCandidates(objective string, skills []contracts.SkillCard, tools []contracts.ToolCard) []contracts.CapabilityCard {
	out := make([]contracts.CapabilityCard, 0)
	for _, card := range p.Capabilities {
		if matchScore(objective, card.Name, card.Description, card.Tags...) > 0 {
			out = append(out, card)
		}
	}
	for _, skill := range skills {
		out = append(out, contracts.CapabilityCard{
			ID:          skill.SkillID,
			Type:        "skill",
			Name:        skill.Name,
			Description: skill.Description,
			Tags:        skill.Tags,
		})
	}
	for _, tool := range tools {
		out = append(out, contracts.CapabilityCard{
			ID:          tool.ToolID,
			Type:        "tool",
			Name:        tool.Name,
			Description: tool.Description,
		})
	}
	return out
}

func DefaultCards() []contracts.ToolCard {
	return []contracts.ToolCard{
		{
			ToolID:      "echo",
			GroupID:     "core",
			Name:        "echo",
			Description: "Returns the input arguments for runtime smoke tests.",
			WhenToUse:   []string{"debugging", "contract tests"},
			RiskLevel:   contracts.RiskLow,
			Visibility:  contracts.ToolExposed,
			Version:     "v1",
		},
		{
			ToolID:      "artifact.create",
			GroupID:     "core",
			Name:        "artifact.create",
			Description: "Creates a text artifact and returns an ArtifactRef.",
			WhenToUse:   []string{"reports", "summaries", "durable artifacts"},
			RiskLevel:   contracts.RiskLow,
			Visibility:  contracts.ToolProtected,
			Version:     "v1",
		},
	}
}

func DefaultSkills() []contracts.SkillCard {
	return []contracts.SkillCard{
		{
			SkillID:     "clean-core.runtime-smoke",
			Version:     "v1",
			Name:        "Runtime smoke execution",
			Description: "Use echo and lightweight tool calls to validate runtime flow.",
			Tags:        []string{"runtime", "smoke", "debugging", "tool"},
			WhenToUse:   []string{"smoke tests", "debugging", "verify tool loop"},
			RiskLevel:   contracts.RiskLow,
		},
		{
			SkillID:     "clean-core.artifact-report",
			Version:     "v1",
			Name:        "Artifact report creation",
			Description: "Create durable text artifacts for generated summaries and reports.",
			Tags:        []string{"artifact", "report", "summary"},
			WhenToUse:   []string{"reports", "summaries", "artifact output"},
			RiskLevel:   contracts.RiskLow,
		},
	}
}

func DefaultCapabilities() []contracts.CapabilityCard {
	return []contracts.CapabilityCard{
		{ID: "runtime.loop", Type: "runtime", Name: "Decision loop", Description: "Run reply and tool-call loops.", Tags: []string{"runtime", "tool", "reply"}},
		{ID: "artifact.output", Type: "artifact", Name: "Artifact output", Description: "Produce artifact refs instead of embedding large content in prompts.", Tags: []string{"artifact", "report", "summary"}},
	}
}

func matchScore(objective string, name string, description string, terms ...string) int {
	haystack := strings.ToLower(strings.Join(append([]string{name, description}, terms...), " "))
	needles := strings.Fields(strings.ToLower(objective))
	score := 0
	for _, needle := range needles {
		needle = strings.Trim(needle, ".,;:!?()[]{}\"'")
		if len(needle) < 3 {
			continue
		}
		if strings.Contains(haystack, needle) {
			score++
		}
	}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(strings.ToLower(objective), term) {
			score += 2
		}
	}
	return score
}
