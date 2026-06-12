package contracts

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnumValidationRejectsUnknown(t *testing.T) {
	if err := TaskStatus("mystery").Validate(); err == nil {
		t.Fatal("expected invalid task status to fail")
	}
	if err := DecisionTypeReply.Validate(); err != nil {
		t.Fatalf("expected valid decision type: %v", err)
	}
}

func TestAgentEnvelopeJSONRoundTrip(t *testing.T) {
	in := AgentEnvelope{
		EnvelopeID: "env_1",
		TraceID:    TraceID("trace_1"),
		Target:     AgentTarget{AgentID: AgentID("agent_1"), Version: AgentVersion("v1")},
		Caller:     AgentCaller{CallerID: "user_1", CallerType: "user", TenantID: TenantID("tenant_1")},
		Command:    "agent.run",
		Payload:    map[string]any{"input": "hello"},
		Context: RuntimeContext{
			TenantID: TenantID("tenant_1"),
			UserID:   UserID("user_1"),
			Conversation: &RuntimeConversation{
				Provider:       "array",
				Kind:           "thread",
				ConversationID: "conv_1",
				ThreadID:       "thread_1",
				CurrentMessage: &RuntimeMessage{
					MessageID:   "msg_1",
					SpeakerID:   "user_1",
					SpeakerType: "user",
				},
			},
		},
		CreatedAt: time.Unix(100, 0).UTC(),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out AgentEnvelope
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Target.AgentID != in.Target.AgentID || out.Context.Conversation.ThreadID != "thread_1" {
		t.Fatalf("unexpected round trip: %#v", out)
	}
}

func TestRuntimeErrorTraitsAndPayload(t *testing.T) {
	err := NewRuntimeError(CodeTaskConflict, "version mismatch", map[string]any{"task_id": "task_1"})
	if !err.IsRetryable() {
		t.Fatal("task conflict must be retryable")
	}
	if err.IsRepairable() {
		t.Fatal("task conflict should not be repairable by model")
	}
	payload := err.ToTracePayload()
	if payload["code"] != CodeTaskConflict {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
