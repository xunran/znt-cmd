package kernel

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"time"

	agentstrategy "znt/internal/agentdef/strategy"
	contextcollector "znt/internal/context/collector"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/pkg/idgen"
)

const (
	contextSourceConversationRecent    = contextcollector.SourceConversationRecent
	contextSourceConversationRetrieval = contextcollector.SourceConversationRetrieval
	contextSourceTaskHistory           = contextcollector.SourceTaskHistory
	contextSourceMemorySummary         = contextcollector.SourceMemorySummary
	contextSourceArtifactRefs          = contextcollector.SourceArtifactRefs
	contextSourceToolResults           = contextcollector.SourceToolResults
	contextSourceRuntimeHookContext    = contextcollector.SourceRuntimeHookContext
	contextSourceAgentPluginContext    = contextcollector.SourceAgentPluginContext
)

func (c Coordinator) conversationContext(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, strategy contracts.ContextStrategy, runID contracts.AgentRunID, taskID contracts.TaskID, events []contracts.TaskEvent, memories []contracts.MemorySummary, artifacts []contracts.ArtifactRef, tools []contracts.ToolResultSummary, userInput string) *contracts.ConversationContext {
	if reflect.DeepEqual(strategy, contracts.ContextStrategy{}) {
		strategy = agentstrategy.DefaultContextStrategy()
	}
	var recentFromStore []contracts.ConversationMessage
	if contextSourceEnabled(strategy, contextSourceConversationRecent) {
		recentFromStore = c.storedConversationMessages(ctx, envelope, contracts.IntValue(strategy.RecentMessageLimit))
	}
	conversation := buildConversationContext(envelope, definition, userInput, c.Now(), c.EnableDirectConversation, recentFromStore, contracts.IntValue(strategy.RecentMessageLimit))
	if conversation == nil {
		return nil
	}
	if !contextSourceEnabled(strategy, contextSourceConversationRecent) {
		conversation.RecentMessages = nil
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
	retrievalMaxResults := contracts.IntValue(strategy.RetrievalMaxResults)
	sufficiency.Queries = contextQueriesWithMaxResults(sufficiency.Queries, retrievalMaxResults)
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

	if sufficiency.RetrievalNeeded && len(sufficiency.Queries) > 0 && contextSourceEnabled(strategy, contextSourceConversationRetrieval) {
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
		if retrievalMaxResults > 0 && len(retrieved) > retrievalMaxResults {
			retrieved = retrieved[:retrievalMaxResults]
		}
		conversation.Retrieved = retrieved
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationContextRetrievalCompleted, map[string]any{
			"retrieved_count":              len(retrieved),
			"retrieved_count_before_limit": originalRetrievedCount,
			"max_retrieved":                retrievalMaxResults,
			"sources":                      retrievedSources(retrieved),
		})
		if len(retrieved) > 0 {
			updated := sufficiency
			updated.Sufficient = true
			updated.Confidence = contextconversation.MaxFloat(updated.Confidence, 0.82)
			updated.RetrievalNeeded = false
			updated.SuggestedAction = "continue"
			updated.Reason = "historical context retrieved; continue with retrieved_context"
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

func buildConversationContext(envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, userInput string, now time.Time, allowDirect bool, storedRecent []contracts.ConversationMessage, recentLimit int) *contracts.ConversationContext {
	runtimeConversation := envelope.Context.Conversation
	if runtimeConversation == nil && (!allowDirect || envelope.Caller.CallerID == "") {
		return nil
	}
	kind := contextconversation.KindDirect
	if runtimeConversation != nil {
		switch strings.TrimSpace(runtimeConversation.Kind) {
		case contextconversation.KindDirect, contextconversation.KindGroup, contextconversation.KindThread:
			kind = strings.TrimSpace(runtimeConversation.Kind)
		default:
			kind = strings.TrimSpace(runtimeConversation.Kind)
		}
	}
	speakerID := envelope.Caller.CallerID
	speakerType := envelope.Caller.CallerType
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
	if runtimeConversation != nil && runtimeConversation.CurrentMessage != nil {
		message := runtimeConversation.CurrentMessage
		current.MessageID = message.MessageID
		current.ExternalMessageID = message.ExternalMessageID
		current.SpeakerID = contextconversation.FirstNonEmpty(message.SpeakerID, current.SpeakerID)
		current.SpeakerType = contextconversation.FirstNonEmpty(message.SpeakerType, current.SpeakerType)
		current.SpeakerName = message.SpeakerName
		current.ReplyToMessageID = message.ReplyToMessageID
		current.ThreadID = contextconversation.FirstNonEmpty(message.ThreadID, runtimeConversation.ThreadID)
		current.Mentions = append(current.Mentions, message.Mentions...)
		if !message.CreatedAt.IsZero() {
			current.CreatedAt = message.CreatedAt
		}
	}
	if runtimeConversation != nil && current.ThreadID == "" {
		current.ThreadID = runtimeConversation.ThreadID
	}
	if current.MessageID == "" {
		current.MessageID = string(envelope.EnvelopeID)
	}
	participants := participantsWithAgent(definition, nil)
	recent := []contracts.ConversationMessage(nil)
	if runtimeConversation != nil {
		recent = append(recent, storedRecent...)
		recent = append(recent, runtimeConversation.RecentMessages...)
		participants = participantsWithAgent(definition, runtimeConversation.Participants)
	}
	recent = normalizeRecentMessages(recent, current, recentLimit)
	return &contracts.ConversationContext{
		Kind:           kind,
		CurrentMessage: current,
		RecentMessages: recent,
		Participants:   participants,
	}
}

func (c Coordinator) recordPreparedInputFacts(ctx context.Context, envelope contracts.AgentEnvelope, run contracts.AgentRun, task contracts.Task, input string) error {
	if err := c.appendConversationInputEvent(ctx, envelope, run.RunID, task.TaskID, task.Objective, input, "conversation.input"); err != nil {
		return err
	}
	return c.persistConversationMessage(ctx, envelope, run, input)
}

func (c Coordinator) recordResumeInputFacts(ctx context.Context, envelope contracts.AgentEnvelope, run contracts.AgentRun, task contracts.Task, input string) error {
	if err := c.appendConversationInputEvent(ctx, envelope, run.RunID, task.TaskID, task.Objective, input, "run.resumed_input"); err != nil {
		return err
	}
	return c.persistConversationMessage(ctx, envelope, run, input)
}

func (c Coordinator) recordConversationInput(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, input string) {
	_ = c.appendConversationInputEvent(ctx, envelope, runID, taskID, "", input, "conversation.input")
}

func (c Coordinator) appendConversationInputEvent(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, taskObjective string, input string, eventType string) error {
	if c.Tasks == nil || taskID == "" || strings.TrimSpace(input) == "" {
		return nil
	}
	payload := map[string]any{
		"input":            input,
		"task_objective":   taskObjective,
		"run_id":           runID,
		"auth_caller_id":   envelope.Caller.CallerID,
		"auth_caller_type": envelope.Caller.CallerType,
	}
	actorID := envelope.Caller.CallerID
	actorType := envelope.Caller.CallerType
	if conversation := envelope.Context.Conversation; conversation != nil {
		payload["provider"] = conversation.Provider
		payload["conversation_kind"] = conversation.Kind
		payload["conversation_id"] = conversation.ConversationID
		payload["thread_id"] = conversation.ThreadID
		if current := conversation.CurrentMessage; current != nil {
			payload["message_id"] = current.MessageID
			payload["external_message_id"] = current.ExternalMessageID
			payload["reply_to_message_id"] = current.ReplyToMessageID
			payload["speaker_id"] = current.SpeakerID
			payload["speaker_type"] = current.SpeakerType
			actorID = contextconversation.FirstNonEmpty(current.SpeakerID, actorID)
			actorType = contextconversation.FirstNonEmpty(current.SpeakerType, actorType)
		}
	}
	event := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    taskID,
		TenantID:  envelope.Context.TenantID,
		Type:      eventType,
		ActorID:   actorID,
		ActorType: actorType,
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
		return err
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationInputRecorded, map[string]any{
		"event_id": event.EventID,
	})
	return nil
}

func (c Coordinator) persistConversationMessage(ctx context.Context, envelope contracts.AgentEnvelope, run contracts.AgentRun, input string) error {
	if c.ConversationStore == nil || envelope.Context.Conversation == nil {
		return nil
	}
	conversation := envelope.Context.Conversation
	now := c.Now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	message := runtimeConversationMessage(envelope, input, now)
	thread := conversationstore.Thread{
		TenantID:       envelope.Context.TenantID,
		ConversationID: conversation.ConversationID,
		ThreadID:       conversation.ThreadID,
		Kind:           conversation.Kind,
		Provider:       conversation.Provider,
		ExternalRefs:   conversation.ExternalRefs,
		LastMessageAt:  message.CreatedAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := c.ConversationStore.UpsertThread(ctx, thread); err != nil {
		return err
	}
	return c.ConversationStore.AppendMessage(ctx, conversationstore.MessageRecord{
		TenantID:       envelope.Context.TenantID,
		ConversationID: conversation.ConversationID,
		ThreadID:       conversation.ThreadID,
		Message:        message,
		Metadata: map[string]any{
			"run_id":  run.RunID,
			"task_id": run.TaskID,
		},
	})
}

func (c Coordinator) storedConversationMessages(ctx context.Context, envelope contracts.AgentEnvelope, limit int) []contracts.ConversationMessage {
	if c.ConversationStore == nil || envelope.Context.Conversation == nil {
		return nil
	}
	conversation := envelope.Context.Conversation
	messages, err := c.ConversationStore.RecentMessages(ctx, envelope.Context.TenantID, conversation.ConversationID, conversation.ThreadID, limit)
	if err != nil {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, "", envelope.Context.TaskID, contracts.TraceConversationContextRetrievalFailed, map[string]any{
			"reason":          "conversation_store_recent_messages_failed",
			"error":           err.Error(),
			"conversation_id": conversation.ConversationID,
			"thread_id":       conversation.ThreadID,
		})
		return nil
	}
	return messages
}

func runtimeConversationID(envelope contracts.AgentEnvelope) string {
	if envelope.Context.Conversation == nil {
		return ""
	}
	return envelope.Context.Conversation.ConversationID
}

func runtimeConversationThreadID(envelope contracts.AgentEnvelope) string {
	if envelope.Context.Conversation == nil {
		return ""
	}
	return envelope.Context.Conversation.ThreadID
}

func runtimeConversationMessageID(envelope contracts.AgentEnvelope) string {
	if envelope.Context.Conversation == nil || envelope.Context.Conversation.CurrentMessage == nil {
		return ""
	}
	return envelope.Context.Conversation.CurrentMessage.MessageID
}

func runtimeConversationMessage(envelope contracts.AgentEnvelope, input string, now time.Time) contracts.ConversationMessage {
	message := contracts.ConversationMessage{
		MessageID:   string(envelope.EnvelopeID),
		SpeakerID:   envelope.Caller.CallerID,
		SpeakerType: envelope.Caller.CallerType,
		Text:        input,
		CreatedAt:   envelope.CreatedAt,
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	if message.SpeakerID == "" {
		message.SpeakerID = string(envelope.Context.UserID)
	}
	if message.SpeakerType == "" {
		message.SpeakerType = "user"
	}
	if conversation := envelope.Context.Conversation; conversation != nil {
		message.ThreadID = conversation.ThreadID
		if current := conversation.CurrentMessage; current != nil {
			message.MessageID = contextconversation.FirstNonEmpty(current.MessageID, message.MessageID)
			message.ExternalMessageID = current.ExternalMessageID
			message.SpeakerID = contextconversation.FirstNonEmpty(current.SpeakerID, message.SpeakerID)
			message.SpeakerType = contextconversation.FirstNonEmpty(current.SpeakerType, message.SpeakerType)
			message.SpeakerName = current.SpeakerName
			message.ReplyToMessageID = current.ReplyToMessageID
			message.ThreadID = contextconversation.FirstNonEmpty(current.ThreadID, message.ThreadID)
			message.Mentions = append(message.Mentions, current.Mentions...)
			if !current.CreatedAt.IsZero() {
				message.CreatedAt = current.CreatedAt
			}
		}
	}
	return message
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

func retrievalQueryStrings(queries []contracts.ContextRetrievalQuery) []string {
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		if query.Query != "" {
			out = append(out, query.Query)
		}
	}
	return out
}

func contextQueriesWithMaxResults(queries []contracts.ContextRetrievalQuery, maxResults int) []contracts.ContextRetrievalQuery {
	if len(queries) == 0 {
		return nil
	}
	out := make([]contracts.ContextRetrievalQuery, len(queries))
	copy(out, queries)
	for i := range out {
		out[i].MaxResults = maxResults
	}
	return out
}

func contextSourceEnabled(strategy contracts.ContextStrategy, source string) bool {
	return contextcollector.SourceEnabled(strategy, source)
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
