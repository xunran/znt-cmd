package kernel

import (
	"context"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	"znt/internal/governance/trace"
	runrepo "znt/internal/runtime/run"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
)

func TestJudgeAddressingReplyToAgent(t *testing.T) {
	agent := loader.TestAgentDefinition()
	conversation := contracts.ConversationContext{
		Kind: contextconversation.KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			MessageID:        "msg_2",
			SpeakerID:        "user_1",
			SpeakerType:      "user",
			Text:             "那你帮我安排一下",
			ReplyToMessageID: "msg_1",
		},
		RecentMessages: []contracts.ConversationMessage{{
			MessageID:   "msg_1",
			SpeakerID:   string(agent.AgentID),
			SpeakerType: "agent",
			Text:        "我可以先帮你协调一下。",
		}},
	}
	result, err := (contextconversation.HeuristicAddressingJudge{}).JudgeAddressing(context.Background(), conversation, agent)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AddressedToAgent || result.SuggestedAction != contextconversation.ActionEnterMainAgent {
		t.Fatalf("expected addressed to agent, got %#v", result)
	}
}

func TestJudgeAddressingReplyToHumanNoOp(t *testing.T) {
	agent := loader.TestAgentDefinition()
	conversation := contracts.ConversationContext{
		Kind: contextconversation.KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			MessageID:        "msg_3",
			SpeakerID:        "user_1",
			SpeakerType:      "user",
			Text:             "那你帮我安排一下",
			ReplyToMessageID: "msg_2",
		},
		RecentMessages: []contracts.ConversationMessage{{
			MessageID:   "msg_2",
			SpeakerID:   "user_2",
			SpeakerType: "user",
			Text:        "我来安排。",
		}},
	}
	result, err := (contextconversation.HeuristicAddressingJudge{}).JudgeAddressing(context.Background(), conversation, agent)
	if err != nil {
		t.Fatal(err)
	}
	if result.AddressedToAgent || result.SuggestedAction != contextconversation.ActionNoOp || result.Confidence < 0.85 {
		t.Fatalf("expected no_op for human reply, got %#v", result)
	}
	if !shouldNoOpByConversationGuard(&contracts.ConversationContext{Kind: contextconversation.KindGroup, Addressing: &result}) {
		t.Fatalf("expected route guard no_op for %#v", result)
	}
}

func TestContextSufficiencyRequestsRetrievalForHistoricalReference(t *testing.T) {
	conversation := contracts.ConversationContext{
		Kind: contextconversation.KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题呢？",
		},
		Addressing: &contracts.AddressingAssessment{
			AddressedToAgent: false,
			Confidence:       0.52,
			SuggestedAction:  contextconversation.ActionRetrieve,
		},
	}
	result, err := (contextconversation.HeuristicSufficiencyJudge{}).JudgeSufficiency(context.Background(), conversation, contextconversation.PhasePreAddressing)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RetrievalNeeded || result.SuggestedAction != contextconversation.ActionRetrieve || len(result.Queries) == 0 {
		t.Fatalf("expected retrieval request, got %#v", result)
	}
}

func TestRetrieveContextFindsOldMessageAndMemory(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	conversation := contracts.ConversationContext{
		Kind: contextconversation.KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题呢？",
			CreatedAt:   now,
		},
		RecentMessages: []contracts.ConversationMessage{{
			MessageID:   "msg_old",
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题是历史上下文主动召回。",
			CreatedAt:   now.Add(-10 * time.Minute),
		}},
	}
	items, err := (contextconversation.BasicRetriever{}).Retrieve(context.Background(), []contracts.ContextRetrievalQuery{{Query: "第二个问题", MaxResults: 5}}, contextconversation.RetrievalInput{
		Conversation: conversation,
		Memory: []contracts.MemorySummary{{
			MemoryID: "mem_1",
			Summary:  "第二个问题也记录在 memory 里。",
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("expected message and memory retrieval, got %#v", items)
	}
	if items[0].SourceType == "" || items[0].Relevance <= 0 {
		t.Fatalf("expected scored retrieved context, got %#v", items[0])
	}
}

func TestRetrieveContextSkipsCurrentInputTaskEvent(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	conversation := contracts.ConversationContext{
		Kind: contextconversation.KindGroup,
		CurrentMessage: contracts.ConversationMessage{
			SpeakerID:   "user_1",
			SpeakerType: "user",
			Text:        "第二个问题呢？",
			CreatedAt:   now,
		},
	}
	items, err := (contextconversation.BasicRetriever{}).Retrieve(context.Background(), []contracts.ContextRetrievalQuery{{Query: "第二个问题", MaxResults: 5}}, contextconversation.RetrievalInput{
		Conversation: conversation,
		TaskEvents: []contracts.TaskEvent{{
			EventID:   "event_current",
			Type:      "conversation.input",
			Payload:   map[string]any{"input": "第二个问题呢？"},
			CreatedAt: now,
		}, {
			EventID:   "event_old",
			Type:      "conversation.input",
			Payload:   map[string]any{"input": "第二个问题是历史上下文主动召回。"},
			CreatedAt: now.Add(-time.Hour),
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected old task event retrieval, got none")
	}
	for _, item := range items {
		if item.SourceID == "event_current" {
			t.Fatalf("should not retrieve current input event: %#v", items)
		}
	}
}

func TestCoordinatorBuildsConversationContextWithRetrieval(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	coordinator := Coordinator{Now: func() time.Time { return now }}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Payload:    map[string]any{"input": "第二个问题呢？"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
			Conversation: &contracts.RuntimeConversation{
				Provider:       "test",
				Kind:           contextconversation.KindGroup,
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:         "msg_now",
					ExternalMessageID: "msg_now",
					SpeakerID:         "user_1",
					SpeakerType:       "user",
					SpeakerName:       "张三",
				},
				RecentMessages: []contracts.ConversationMessage{{
					MessageID:   "msg_old",
					SpeakerID:   "user_1",
					SpeakerName: "张三",
					SpeakerType: "user",
					Text:        "我们刚才先讨论第一个问题。",
					CreatedAt:   now.Add(-time.Minute),
				}},
			},
		},
		CreatedAt: now,
	}
	conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, []contracts.MemorySummary{{
		MemoryID: "mem_1",
		Summary:  "第二个问题是上下文主动召回。",
	}}, nil, nil, "第二个问题呢？")
	if conversation == nil || len(conversation.Retrieved) == 0 {
		t.Fatalf("expected retrieved conversation context, got %#v", conversation)
	}
	if conversation.Sufficiency == nil || !conversation.Sufficiency.Sufficient {
		t.Fatalf("expected sufficiency updated after retrieval, got %#v", conversation.Sufficiency)
	}
}

func TestCoordinatorUsesInjectedConversationEngines(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	addressing := &stubAddressingJudge{result: contracts.AddressingAssessment{
		AddressedToAgent: true,
		Confidence:       0.91,
		Reason:           "stub addressing",
		DecisionSource:   "test",
		SuggestedAction:  contextconversation.ActionEnterMainAgent,
	}}
	sufficiency := &stubSufficiencyJudge{result: contracts.ContextSufficiencyAssessment{
		Phase:           contextconversation.PhasePreDecision,
		Sufficient:      false,
		Confidence:      0.80,
		Reason:          "stub sufficiency",
		RetrievalNeeded: true,
		Queries:         []contracts.ContextRetrievalQuery{{Query: "第二个问题", MaxResults: 3}},
		SuggestedAction: contextconversation.ActionRetrieve,
	}}
	retriever := &stubContextRetriever{result: []contracts.RetrievedContext{{
		SourceType: "memory",
		SourceID:   "mem_1",
		Summary:    "第二个问题来自注入检索器。",
		Relevance:  0.9,
	}}}
	coordinator := Coordinator{
		AddressingJudge:  addressing,
		SufficiencyJudge: sufficiency,
		ContextRetriever: retriever,
		Now:              func() time.Time { return now },
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
			Conversation: &contracts.RuntimeConversation{
				Provider:       "test",
				Kind:           contextconversation.KindGroup,
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:         "msg_now",
					ExternalMessageID: "msg_now",
					SpeakerID:         "user_1",
					SpeakerType:       "user",
				},
			},
		},
		CreatedAt: now,
	}
	conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, nil, nil, nil, "第二个问题呢？")
	if conversation == nil {
		t.Fatal("expected conversation context")
	}
	if addressing.calls < 2 {
		t.Fatalf("expected addressing judge to be used and re-used after retrieval, got %d calls", addressing.calls)
	}
	if sufficiency.calls != 1 {
		t.Fatalf("expected sufficiency judge to be used once, got %d calls", sufficiency.calls)
	}
	if retriever.calls != 1 || len(conversation.Retrieved) != 1 {
		t.Fatalf("expected injected retriever result, calls=%d conversation=%#v", retriever.calls, conversation)
	}
	if conversation.Sufficiency == nil || !conversation.Sufficiency.Sufficient {
		t.Fatalf("expected sufficiency to be marked sufficient after retrieval, got %#v", conversation.Sufficiency)
	}
}

func TestCoordinatorHonorsConversationRetrievalDisabled(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	retriever := &stubContextRetriever{result: []contracts.RetrievedContext{{
		SourceType: "memory",
		SourceID:   "mem_1",
		Summary:    "should not be retrieved",
	}}}
	coordinator := Coordinator{
		AddressingJudge: &stubAddressingJudge{result: contracts.AddressingAssessment{
			AddressedToAgent: false,
			Confidence:       0.52,
			DecisionSource:   "test",
			SuggestedAction:  contextconversation.ActionRetrieve,
		}},
		SufficiencyJudge: &stubSufficiencyJudge{result: contracts.ContextSufficiencyAssessment{
			Phase:           contextconversation.PhasePreAddressing,
			Sufficient:      false,
			Confidence:      0.8,
			RetrievalNeeded: true,
			Queries:         []contracts.ContextRetrievalQuery{{Query: "old context", MaxResults: 3}},
			SuggestedAction: contextconversation.ActionRetrieve,
		}},
		ContextRetriever:             retriever,
		DisableConversationRetrieval: true,
		Now:                          func() time.Time { return now },
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
			Conversation: &contracts.RuntimeConversation{
				Provider:       "test",
				Kind:           contextconversation.KindGroup,
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:   "msg_now",
					SpeakerID:   "user_1",
					SpeakerType: "user",
				},
			},
		},
		CreatedAt: now,
	}
	conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, nil, nil, nil, "old context?")
	if conversation == nil {
		t.Fatal("expected conversation context")
	}
	if retriever.calls != 0 || len(conversation.Retrieved) != 0 {
		t.Fatalf("expected retrieval to be disabled, calls=%d conversation=%#v", retriever.calls, conversation)
	}
	if conversation.Sufficiency == nil || !conversation.Sufficiency.RetrievalNeeded {
		t.Fatalf("expected sufficiency to keep retrieval_needed after disabled retrieval, got %#v", conversation.Sufficiency)
	}
}

func TestCoordinatorSkipsDirectConversationByDefault(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	coordinator := Coordinator{Now: func() time.Time { return now }}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_direct_default",
		TraceID:    "trace_direct_default",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
		},
		CreatedAt: now,
	}
	if conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, nil, nil, nil, "hello"); conversation != nil {
		t.Fatalf("ordinary API run should not build direct conversation by default, got %#v", conversation)
	}
}

func TestCoordinatorAllowsDirectConversationWhenEnabled(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	coordinator := Coordinator{
		EnableDirectConversation: true,
		Now:                      func() time.Time { return now },
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_direct_enabled",
		TraceID:    "trace_direct_enabled",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
		},
		CreatedAt: now,
	}
	conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, nil, nil, nil, "hello")
	if conversation == nil || conversation.Kind != contextconversation.KindDirect || conversation.CurrentMessage.SpeakerID != "user_1" {
		t.Fatalf("expected opt-in direct conversation context, got %#v", conversation)
	}
}

func TestCoordinatorLimitsRetrievedContext(t *testing.T) {
	agent := loader.TestAgentDefinition()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	coordinator := Coordinator{
		AddressingJudge: &stubAddressingJudge{result: contracts.AddressingAssessment{
			AddressedToAgent: true,
			Confidence:       0.91,
			DecisionSource:   "test",
			SuggestedAction:  contextconversation.ActionEnterMainAgent,
		}},
		SufficiencyJudge: &stubSufficiencyJudge{result: contracts.ContextSufficiencyAssessment{
			Phase:           contextconversation.PhasePreDecision,
			Sufficient:      false,
			Confidence:      0.8,
			RetrievalNeeded: true,
			Queries:         []contracts.ContextRetrievalQuery{{Query: "old context", MaxResults: 5}},
			SuggestedAction: contextconversation.ActionRetrieve,
		}},
		ContextRetriever: &stubContextRetriever{result: []contracts.RetrievedContext{{
			SourceType: "memory",
			SourceID:   "mem_1",
			Summary:    "first",
		}, {
			SourceType: "memory",
			SourceID:   "mem_2",
			Summary:    "second",
		}}},
		ConversationMaxRetrieved: 1,
		Now:                      func() time.Time { return now },
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
			Conversation: &contracts.RuntimeConversation{
				Provider:       "test",
				Kind:           contextconversation.KindGroup,
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:   "msg_now",
					SpeakerID:   "user_1",
					SpeakerType: "user",
				},
			},
		},
		CreatedAt: now,
	}
	conversation := coordinator.conversationContext(context.Background(), envelope, agent, "run_1", "task_1", nil, nil, nil, nil, "old context?")
	if conversation == nil || len(conversation.Retrieved) != 1 || conversation.Retrieved[0].SourceID != "mem_1" {
		t.Fatalf("expected retrieved context to be limited to first item, got %#v", conversation)
	}
}

func TestCoordinatorRecordsConversationInputTaskEvent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	taskStore := taskrepo.NewInMemoryStore()
	taskService := taskruntime.NewService(taskStore, taskStore)
	task := taskrepo.NewTask("task_1", "tenant_1", "test-agent", "v1", "policy_default", "existing", "objective", now)
	if _, err := taskService.CreateTask(ctx, task, "user_1", "user"); err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{
		Tasks: taskService,
		Trace: trace.NewInMemoryRecorder(),
		Now:   func() time.Time { return now },
		Runs:  runrepo.NewInMemoryRepository(),
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    "trace_1",
		Caller:     contracts.AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: "tenant_1"},
		Context: contracts.RuntimeContext{
			TenantID: "tenant_1",
			UserID:   "user_1",
			Conversation: &contracts.RuntimeConversation{
				Provider:       "test",
				Kind:           contextconversation.KindGroup,
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:         "msg_1",
					ExternalMessageID: "msg_1",
					SpeakerID:         "user_1",
					SpeakerType:       "user",
				},
			},
		},
	}
	coordinator.recordConversationInput(ctx, envelope, "run_1", task.TaskID, "第二个问题呢？")
	events, err := taskService.Events(ctx, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "conversation.input" {
			found = true
			if event.Payload["input"] != "第二个问题呢？" || event.Payload["external_message_id"] != "msg_1" {
				t.Fatalf("unexpected conversation input payload: %#v", event.Payload)
			}
		}
	}
	if !found {
		t.Fatalf("expected conversation.input event, got %#v", events)
	}
}

type stubAddressingJudge struct {
	result contracts.AddressingAssessment
	calls  int
}

func (s *stubAddressingJudge) JudgeAddressing(context.Context, contracts.ConversationContext, contracts.AgentDefinition) (contracts.AddressingAssessment, error) {
	s.calls++
	return s.result, nil
}

type stubSufficiencyJudge struct {
	result contracts.ContextSufficiencyAssessment
	calls  int
}

func (s *stubSufficiencyJudge) JudgeSufficiency(context.Context, contracts.ConversationContext, string) (contracts.ContextSufficiencyAssessment, error) {
	s.calls++
	return s.result, nil
}

type stubContextRetriever struct {
	result []contracts.RetrievedContext
	calls  int
}

func (s *stubContextRetriever) Retrieve(context.Context, []contracts.ContextRetrievalQuery, contextconversation.RetrievalInput) ([]contracts.RetrievedContext, error) {
	s.calls++
	return s.result, nil
}
