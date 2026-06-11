package kernel

import (
	"context"
	"sort"
	"strings"
	"time"

	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	"znt/pkg/idgen"
)

func (c Coordinator) conversationContext(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, events []contracts.TaskEvent, memories []contracts.MemorySummary, artifacts []contracts.ArtifactRef, tools []contracts.ToolResultSummary, userInput string) *contracts.ConversationContext {
	conversation := buildConversationContext(envelope, definition, userInput, c.Now(), c.EnableDirectConversation)
	if conversation == nil {
		return nil
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextBuilt, map[string]any{
		"conversation_kind":  conversation.Kind,
		"current_speaker_id": conversation.CurrentMessage.SpeakerID,
		"message_id":         conversation.CurrentMessage.MessageID,
		"recent_count":       len(conversation.RecentMessages),
		"participant_count":  len(conversation.Participants),
	})

	addressingJudge := c.AddressingJudge
	if addressingJudge == nil {
		addressingJudge = contextconversation.HeuristicAddressingJudge{}
	}
	addressing, err := addressingJudge.JudgeAddressing(ctx, *conversation, definition)
	if err != nil {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
			"reason": "conversation_addressing_judge_failed",
			"error":  err.Error(),
		})
		addressing, _ = (contextconversation.HeuristicAddressingJudge{}).JudgeAddressing(ctx, *conversation, definition)
	}
	conversation.Addressing = &addressing
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationAddresseeJudged, map[string]any{
		"addressed_to_agent": addressing.AddressedToAgent,
		"confidence":         addressing.Confidence,
		"reason":             addressing.Reason,
		"signals":            addressing.Signals,
		"suggested_action":   addressing.SuggestedAction,
		"source":             addressing.DecisionSource,
	})

	sufficiencyJudge := c.SufficiencyJudge
	if sufficiencyJudge == nil {
		sufficiencyJudge = contextconversation.HeuristicSufficiencyJudge{}
	}
	phase := contextconversation.PhasePreAddressing
	if addressing.SuggestedAction == contextconversation.ActionEnterMainAgent || addressing.AddressedToAgent {
		phase = contextconversation.PhasePreDecision
	}
	sufficiency, err := sufficiencyJudge.JudgeSufficiency(ctx, *conversation, phase)
	if err != nil {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
			"reason": "conversation_sufficiency_judge_failed",
			"error":  err.Error(),
		})
		sufficiency, _ = (contextconversation.HeuristicSufficiencyJudge{}).JudgeSufficiency(ctx, *conversation, phase)
	}
	conversation.Sufficiency = &sufficiency
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationSufficiencyJudged, map[string]any{
		"phase":             sufficiency.Phase,
		"sufficient":        sufficiency.Sufficient,
		"confidence":        sufficiency.Confidence,
		"reason":            sufficiency.Reason,
		"missing_facts":     sufficiency.MissingFacts,
		"retrieval_needed":  sufficiency.RetrievalNeeded,
		"retrieval_queries": retrievalQueryStrings(sufficiency.Queries),
		"suggested_action":  sufficiency.SuggestedAction,
	})

	if sufficiency.RetrievalNeeded && len(sufficiency.Queries) > 0 {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalRequested, map[string]any{
			"queries": retrievalQueryStrings(sufficiency.Queries),
			"phase":   sufficiency.Phase,
		})
		if c.DisableConversationRetrieval {
			c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
				"reason":  "conversation_context_retrieval_disabled",
				"queries": retrievalQueryStrings(sufficiency.Queries),
			})
			return conversation
		}
		retriever := c.ContextRetriever
		if retriever == nil {
			retriever = contextconversation.BasicRetriever{}
		}
		retrieved, err := retriever.Retrieve(ctx, sufficiency.Queries, contextconversation.RetrievalInput{
			Conversation: *conversation,
			TaskEvents:   events,
			Memory:       memories,
			Artifacts:    artifacts,
			ToolResults:  tools,
			Now:          c.Now(),
		})
		if err != nil {
			c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
				"reason":  "conversation_context_retrieval_failed",
				"error":   err.Error(),
				"queries": retrievalQueryStrings(sufficiency.Queries),
			})
			return conversation
		}
		originalRetrievedCount := len(retrieved)
		if c.ConversationMaxRetrieved > 0 && len(retrieved) > c.ConversationMaxRetrieved {
			retrieved = retrieved[:c.ConversationMaxRetrieved]
		}
		conversation.Retrieved = retrieved
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalCompleted, map[string]any{
			"retrieved_count":              len(retrieved),
			"retrieved_count_before_limit": originalRetrievedCount,
			"max_retrieved":                c.ConversationMaxRetrieved,
			"sources":                      retrievedSources(retrieved),
		})
		if len(retrieved) > 0 {
			updated := sufficiency
			updated.Sufficient = true
			updated.Confidence = contextconversation.MaxFloat(updated.Confidence, 0.82)
			updated.RetrievalNeeded = false
			updated.SuggestedAction = "continue"
			updated.Reason = "历史上下文已召回，可基于 retrieved_context 继续判断。"
			conversation.Sufficiency = &updated
			rejudged, err := addressingJudge.JudgeAddressing(ctx, *conversation, definition)
			if err != nil {
				c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
					"reason": "conversation_rejudge_failed",
					"error":  err.Error(),
				})
				rejudged = addressing
			}
			if addressing.SuggestedAction == contextconversation.ActionRetrieve && rejudged.Confidence < addressing.Confidence {
				rejudged = addressing
			}
			conversation.Addressing = &rejudged
			c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationRetrievedContextMerged, map[string]any{
				"retrieved_count":       len(retrieved),
				"addressed_to_agent":    rejudged.AddressedToAgent,
				"addressing_confidence": rejudged.Confidence,
				"addressing_reason":     rejudged.Reason,
				"suggested_action":      rejudged.SuggestedAction,
			})
		}
	}
	return conversation
}

func buildConversationContext(envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, userInput string, now time.Time, allowDirect bool) *contracts.ConversationContext {
	collab := envelope.Context.Collaboration
	if collab == nil && (!allowDirect || envelope.Caller.CallerID == "") {
		return nil
	}
	kind := contextconversation.KindDirect
	if collab != nil {
		switch strings.TrimSpace(collab.ConversationKind) {
		case contextconversation.KindDirect, contextconversation.KindGroup, contextconversation.KindThread:
			kind = strings.TrimSpace(collab.ConversationKind)
		case "":
			if collab.ExternalGroupID != "" || collab.ExternalChannelID != "" {
				kind = contextconversation.KindGroup
			}
			if collab.ExternalThreadID != "" {
				kind = contextconversation.KindThread
			}
		default:
			kind = strings.TrimSpace(collab.ConversationKind)
		}
	}
	speakerID := envelope.Caller.CallerID
	speakerType := envelope.Caller.CallerType
	if collab != nil {
		if collab.CallerID != "" {
			speakerID = collab.CallerID
		}
		if collab.CallerType != "" {
			speakerType = collab.CallerType
		}
	}
	if speakerType == "" {
		speakerType = "user"
	}
	current := contracts.ConversationMessage{
		SpeakerID:   speakerID,
		SpeakerType: speakerType,
		Text:        userInput,
		CreatedAt:   envelope.CreatedAt,
	}
	if current.CreatedAt.IsZero() {
		current.CreatedAt = now
	}
	if collab != nil {
		current.MessageID = collab.ExternalMessageID
		current.ExternalMessageID = collab.ExternalMessageID
		current.SpeakerName = collab.CurrentSpeakerName
		current.ReplyToMessageID = contextconversation.FirstNonEmpty(collab.ReplyToMessageID, replyTargetID(collab.ReplyTarget))
		current.ThreadID = contextconversation.FirstNonEmpty(collab.ThreadID, collab.ExternalThreadID)
		current.Mentions = append(current.Mentions, collab.MentionedAgentIDs...)
	}
	if current.MessageID == "" {
		current.MessageID = string(envelope.EnvelopeID)
	}
	participants := participantsWithAgent(definition, nil)
	recent := []contracts.ConversationMessage(nil)
	if collab != nil {
		recent = append(recent, collab.RecentMessages...)
		participants = participantsWithAgent(definition, collab.Participants)
	}
	recent = normalizeRecentMessages(recent, current, 20)
	return &contracts.ConversationContext{
		Kind:           kind,
		CurrentMessage: current,
		RecentMessages: recent,
		Participants:   participants,
	}
}

func (c Coordinator) recordConversationInput(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, input string) {
	if c.Tasks == nil || taskID == "" || strings.TrimSpace(input) == "" {
		return
	}
	payload := map[string]any{
		"input": input,
	}
	if collab := envelope.Context.Collaboration; collab != nil {
		payload["provider"] = collab.Provider
		payload["conversation_kind"] = collab.ConversationKind
		payload["external_group_id"] = collab.ExternalGroupID
		payload["external_channel_id"] = collab.ExternalChannelID
		payload["external_thread_id"] = collab.ExternalThreadID
		payload["external_message_id"] = collab.ExternalMessageID
		payload["caller_id"] = collab.CallerID
		payload["caller_type"] = collab.CallerType
		payload["reply_to_message_id"] = contextconversation.FirstNonEmpty(collab.ReplyToMessageID, replyTargetID(collab.ReplyTarget))
	}
	event := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    taskID,
		TenantID:  envelope.Context.TenantID,
		Type:      "conversation.input",
		ActorID:   envelope.Caller.CallerID,
		ActorType: envelope.Caller.CallerType,
		Payload:   payload,
		RunID:     runID,
		CreatedAt: c.Now(),
	}
	if event.ActorID == "" {
		event.ActorID = string(envelope.Context.UserID)
	}
	if event.ActorType == "" {
		event.ActorType = "user"
	}
	if err := c.Tasks.AppendEvent(ctx, event); err != nil {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
			"reason": "record_conversation_input_failed",
			"error":  err.Error(),
		})
		return
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationInputRecorded, map[string]any{
		"event_id": event.EventID,
	})
}

func participantsWithAgent(definition contracts.AgentDefinition, participants []contracts.ConversationParticipant) []contracts.ConversationParticipant {
	seen := map[string]struct{}{}
	out := make([]contracts.ConversationParticipant, 0, len(participants)+1)
	for _, participant := range participants {
		if participant.ID == "" {
			continue
		}
		seen[participant.ID] = struct{}{}
		out = append(out, participant)
	}
	agentID := string(definition.AgentID)
	if agentID != "" {
		if _, ok := seen[agentID]; !ok {
			out = append(out, contracts.ConversationParticipant{
				ID:   agentID,
				Type: "agent",
				Name: definition.Name,
				Role: "origin-coordinator",
			})
		}
	}
	return out
}

func normalizeRecentMessages(messages []contracts.ConversationMessage, current contracts.ConversationMessage, limit int) []contracts.ConversationMessage {
	out := make([]contracts.ConversationMessage, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Text) == "" && message.MessageID == "" {
			continue
		}
		if message.MessageID != "" && message.MessageID == current.MessageID {
			continue
		}
		out = append(out, message)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.IsZero() || out[j].CreatedAt.IsZero() {
			return i < j
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func replyTargetID(target *contracts.ReplyTarget) string {
	if target == nil {
		return ""
	}
	return target.ID
}

func retrievalQueryStrings(queries []contracts.ContextRetrievalQuery) []string {
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		if query.Query != "" {
			out = append(out, query.Query)
		}
	}
	return out
}

func retrievedSources(items []contracts.RetrievedContext) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, item := range items {
		if item.SourceType == "" {
			continue
		}
		if _, ok := seen[item.SourceType]; ok {
			continue
		}
		seen[item.SourceType] = struct{}{}
		out = append(out, item.SourceType)
	}
	sort.Strings(out)
	return out
}

func shouldNoOpByConversationGuard(conversation *contracts.ConversationContext) bool {
	if conversation == nil || conversation.Addressing == nil {
		return false
	}
	return (conversation.Kind == contextconversation.KindGroup || conversation.Kind == contextconversation.KindThread) &&
		!conversation.Addressing.AddressedToAgent &&
		conversation.Addressing.SuggestedAction == contextconversation.ActionNoOp &&
		conversation.Addressing.Confidence >= 0.85
}

func retrievedContextCount(conversation *contracts.ConversationContext) int {
	if conversation == nil {
		return 0
	}
	return len(conversation.Retrieved)
}
