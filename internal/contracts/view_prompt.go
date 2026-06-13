package contracts

import "time"

type CapabilityCard struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

type RiskMark struct {
	Level  RiskLevel `json:"level"`
	Reason string    `json:"reason"`
}

type CollaboratorCard struct {
	AgentID      AgentID      `json:"agent_id"`
	Version      AgentVersion `json:"version,omitempty"`
	Alias        string       `json:"alias,omitempty"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	WhenToUse    []string     `json:"when_to_use,omitempty"`
	Capabilities []string     `json:"capabilities,omitempty"`
}

type WorkView struct {
	RunID  AgentRunID `json:"run_id"`
	TaskID TaskID     `json:"task_id,omitempty"`

	Agent AgentDefinitionSummary `json:"agent"`

	UserInput string `json:"user_input"`

	ConversationContext *ConversationContext `json:"conversation_context,omitempty"`

	TaskSummary     TaskSummary        `json:"task_summary"`
	PlanSummary     *PlanSummary       `json:"plan_summary,omitempty"`
	CurrentPlanStep *PlanStepSummary   `json:"current_plan_step,omitempty"`
	TaskHistory     []RetrievedContext `json:"task_history,omitempty"`

	HandoffContext *HandoffContextSummary `json:"handoff_context,omitempty"`

	MemorySummaries     []MemorySummary     `json:"memory_summaries,omitempty"`
	ArtifactRefs        []ArtifactRef       `json:"artifact_refs,omitempty"`
	ToolResultSummaries []ToolResultSummary `json:"tool_result_summaries,omitempty"`

	ContextAssemblyReport *ContextAssemblyReport `json:"context_assembly_report,omitempty"`

	CandidateCapabilities      []CapabilityCard   `json:"candidate_capabilities,omitempty"`
	CandidateSkills            []SkillCard        `json:"candidate_skills,omitempty"`
	CandidateSkillInstructions []SkillInstruction `json:"candidate_skill_instructions,omitempty"`
	CandidateTools             []ToolCard         `json:"candidate_tools,omitempty"`
	CandidateCollaborators     []CollaboratorCard `json:"candidate_collaborators,omitempty"`

	Constraints []string   `json:"constraints,omitempty"`
	RiskMarks   []RiskMark `json:"risk_marks,omitempty"`
}

type ConversationContext struct {
	Kind string `json:"kind"` // direct, group, thread

	CurrentMessage ConversationMessage   `json:"current_message"`
	RecentMessages []ConversationMessage `json:"recent_messages,omitempty"`

	Participants []ConversationParticipant `json:"participants,omitempty"`

	Addressing  *AddressingAssessment         `json:"addressing,omitempty"`
	Sufficiency *ContextSufficiencyAssessment `json:"sufficiency,omitempty"`
	Retrieved   []RetrievedContext            `json:"retrieved,omitempty"`
}

type ConversationMessage struct {
	MessageID         string `json:"message_id,omitempty"`
	ExternalMessageID string `json:"external_message_id,omitempty"`

	SpeakerID   string `json:"speaker_id"`
	SpeakerType string `json:"speaker_type"` // user, agent, system
	SpeakerName string `json:"speaker_name,omitempty"`

	Text string `json:"text"`

	CreatedAt time.Time `json:"created_at,omitempty"`

	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	ThreadID         string `json:"thread_id,omitempty"`

	Mentions []string `json:"mentions,omitempty"`
}

type ConversationParticipant struct {
	ID   string `json:"id"`
	Type string `json:"type"` // user, agent, system
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

type AddressingAssessment struct {
	AddressedToAgent bool     `json:"addressed_to_agent"`
	Confidence       float64  `json:"confidence"`
	Reason           string   `json:"reason"`
	Signals          []string `json:"signals,omitempty"`
	AddresseeIDs     []string `json:"addressee_ids,omitempty"`
	DecisionSource   string   `json:"decision_source,omitempty"`  // rule, heuristic, llm, hybrid
	SuggestedAction  string   `json:"suggested_action,omitempty"` // enter_main_agent, no_op, ask_if_addressed, retrieve_context
}

type ContextSufficiencyAssessment struct {
	Phase      string  `json:"phase"` // pre_addressing, pre_decision
	Sufficient bool    `json:"sufficient"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`

	MissingFacts    []string                `json:"missing_facts,omitempty"`
	RetrievalNeeded bool                    `json:"retrieval_needed"`
	Queries         []ContextRetrievalQuery `json:"queries,omitempty"`
	SuggestedAction string                  `json:"suggested_action,omitempty"` // continue, retrieve_context, ask_clarification, no_op
}

type ContextRetrievalQuery struct {
	Query           string   `json:"query"`
	Sources         []string `json:"sources,omitempty"` // conversation_history, memory, task_event, artifact, tool_result, handoff
	SpeakerIDs      []string `json:"speaker_ids,omitempty"`
	ThreadID        string   `json:"thread_id,omitempty"`
	ExternalGroupID string   `json:"external_group_id,omitempty"`
	TimeHint        string   `json:"time_hint,omitempty"`
	MaxResults      int      `json:"max_results,omitempty"`
}

type RetrievedContext struct {
	SourceType string `json:"source_type"` // conversation_history, memory, task_event, artifact, tool_result, handoff
	SourceID   string `json:"source_id"`

	SpeakerID   string `json:"speaker_id,omitempty"`
	SpeakerName string `json:"speaker_name,omitempty"`

	CreatedAt time.Time `json:"created_at,omitempty"`

	Summary string `json:"summary,omitempty"`
	Snippet string `json:"snippet,omitempty"`

	Relevance    float64 `json:"relevance,omitempty"`
	RecencyScore float64 `json:"recency_score,omitempty"`
	TrustLevel   string  `json:"trust_level,omitempty"` // untrusted_user_text, untrusted_external_context, system_record, tool_result
	Visibility   string  `json:"visibility,omitempty"`
}

type PromptBundle struct {
	BundleID string     `json:"bundle_id"`
	RunID    AgentRunID `json:"run_id"`

	System    string `json:"system"`
	Developer string `json:"developer,omitempty"`
	Task      string `json:"task"`
	Context   string `json:"context"`

	SkillInstructions []string         `json:"skill_instructions,omitempty"`
	ToolCards         []ToolCard       `json:"tool_cards,omitempty"`
	ToolDefinitions   []ToolDefinition `json:"tool_definitions,omitempty"`

	OutputSchema map[string]any `json:"output_schema,omitempty"`

	Constraints []string `json:"constraints,omitempty"`

	ContextAssemblyReport *ContextAssemblyReport `json:"context_assembly_report,omitempty"`

	Hash string `json:"hash"`

	CreatedAt time.Time `json:"created_at"`
}
