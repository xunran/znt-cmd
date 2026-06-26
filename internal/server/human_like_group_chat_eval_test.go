package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"znt/internal/contracts"
)

type humanLikeEvalSuiteSpec struct {
	Cases []humanLikeEvalCaseSpec `json:"cases"`
}

type humanLikeEvalCaseSpec struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	EvalCase humanLikeEvalCase `json:"eval_case"`
	Custom   map[string]any    `json:"custom_assertions"`
}

type humanLikeEvalCase struct {
	Name                  string   `json:"name"`
	Category              string   `json:"category"`
	Input                 string   `json:"input"`
	MustCallTools         []string `json:"must_call_tools"`
	ShouldNotCallTools    []string `json:"should_not_call_tools"`
	FinalReplyContains    []string `json:"final_reply_contains"`
	FinalReplyNotContains []string `json:"final_reply_not_contains"`
}

func TestHumanLikeGroupChatEvalCoreRoutes(t *testing.T) {
	spec := loadHumanLikeEvalSuiteSpec(t)
	casesByID := map[string]humanLikeEvalCaseSpec{}
	for _, tc := range spec.Cases {
		casesByID[tc.ID] = tc
	}
	for _, id := range []string{
		"DLG-005",
		"DLG-009",
		"DLG-010",
		"DLG-014",
		"DLG-021",
		"DLG-024",
		"DLG-025",
		"DLG-028",
		"DLG-036",
	} {
		tc, ok := casesByID[id]
		if !ok {
			t.Fatalf("human-like eval case %s missing", id)
		}
		t.Run(id+"_"+tc.EvalCase.Category, func(t *testing.T) {
			expectTool := len(tc.EvalCase.MustCallTools) > 0
			reply := humanLikeScriptedFinalReply(tc, expectTool)
			responses := [][]byte{}
			if expectTool {
				responses = append(responses, scriptedReplyJSON("商家测额工具结果：当前可融资金额为 88,000 元。"))
			}
			responses = append(responses, scriptedReplyJSON(reply))
			appCore, handler := newMerchantLimitChatTestHarness(t, responses)
			create := doJSON(handler, http.MethodPost, "/v1/chat/conversations", map[string]any{
				"name":          "human-like-eval-" + id,
				"main_agent_id": "test-agent",
				"version":       "v1",
				"member_agents": []map[string]any{{"agent_id": "znt-merchant-limit", "name": "商家测额智能体"}},
			})
			if create.Code != http.StatusCreated {
				t.Fatalf("create conversation failed %d body %s", create.Code, create.Body.String())
			}
			var created struct {
				Conversation struct {
					ConversationID string `json:"conversation_id"`
				} `json:"conversation"`
			}
			if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			send := doJSON(handler, http.MethodPost, "/v1/chat/conversations/"+created.Conversation.ConversationID+"/messages", map[string]any{"text": tc.EvalCase.Input})
			if send.Code != http.StatusOK {
				t.Fatalf("send failed %d body %s", send.Code, send.Body.String())
			}
			var sent struct {
				TraceID  contracts.TraceID `json:"trace_id"`
				Messages []struct {
					Text string `json:"text"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(send.Body.Bytes(), &sent); err != nil {
				t.Fatal(err)
			}
			if len(sent.Messages) != 2 {
				t.Fatalf("expected user and main-agent messages, got %#v body %s", sent.Messages, send.Body.String())
			}
			for _, expected := range tc.EvalCase.FinalReplyContains {
				if !strings.Contains(sent.Messages[1].Text, expected) {
					t.Fatalf("reply missing %q: %s", expected, sent.Messages[1].Text)
				}
			}
			for _, forbidden := range append(defaultHumanLikeForbiddenPublicText(), tc.EvalCase.FinalReplyNotContains...) {
				if forbidden != "" && strings.Contains(sent.Messages[1].Text, forbidden) {
					t.Fatalf("reply leaked forbidden public text %q: %s", forbidden, sent.Messages[1].Text)
				}
			}
			events, err := appCore.Trace.ListByTrace(context.Background(), sent.TraceID)
			if err != nil {
				t.Fatal(err)
			}
			calledTool := hasTraceEvent(events, "agent_tool.invoked")
			if expectTool && !calledTool {
				t.Fatalf("expected agent_tool invocation for %s, got %#v", id, events)
			}
			if !expectTool && calledTool {
				t.Fatalf("did not expect agent_tool invocation for %s, got %#v", id, events)
			}
			if expectTool && !tracePayloadStringEquals(events, contracts.TraceDecisionCompleted, "source", "merchant_limit_forced_tool") {
				t.Fatalf("expected forced merchant-limit trace for %s, got %#v", id, events)
			}
		})
	}
}

func loadHumanLikeEvalSuiteSpec(t *testing.T) humanLikeEvalSuiteSpec {
	t.Helper()
	path := filepath.Join("..", "..", "eval", "human_like_group_chat_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec humanLikeEvalSuiteSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Cases) == 0 {
		t.Fatal("human-like eval suite has no cases")
	}
	return spec
}

func humanLikeScriptedFinalReply(tc humanLikeEvalCaseSpec, expectTool bool) string {
	switch tc.ID {
	case "DLG-024":
		return "不能直接修改、审批通过或放款；我可以帮你整理审核关注点。"
	case "DLG-025":
		return "不能提供接口原始返回、密钥或请求地址；我可以整理成业务摘要。"
	case "DLG-036":
		return "白话说，就是这单目前还不能直接按申请金额处理，要先核对额度和风险点。"
	}
	if expectTool {
		return "当前可融资金额为 88,000 元，申请金额需以最终审批为准。"
	}
	return "我可以帮你看请款单基础信息、可融资额度、授信、质押店铺和回款情况。"
}

func defaultHumanLikeForbiddenPublicText() []string {
	return []string{
		"capability_not_available",
		"no operation required",
		"no action required",
		"tool_not_found",
		"tool_execution_failed",
		"tool_policy_denied",
		"provider_agent_id",
		"trace_id",
		"run_id",
		"task_id",
		"stack trace",
		"token",
		"API Key",
		"agent_tool provider_id is required",
	}
}

func TestHumanLikeGroupChatEvalJSONCoverage(t *testing.T) {
	spec := loadHumanLikeEvalSuiteSpec(t)
	if len(spec.Cases) < 15 {
		t.Fatalf("expected at least 15 machine-readable eval cases, got %d", len(spec.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range spec.Cases {
		if strings.TrimSpace(tc.ID) == "" || strings.TrimSpace(tc.EvalCase.Name) == "" || strings.TrimSpace(tc.EvalCase.Input) == "" {
			t.Fatalf("case must include id, eval_case.name and eval_case.input: %#v", tc)
		}
		if seen[tc.ID] {
			t.Fatalf("duplicate case id %s", tc.ID)
		}
		seen[tc.ID] = true
	}
	for _, required := range []string{"DLG-001", "DLG-009", "DLG-021", "DLG-024", "DLG-028", "DLG-036"} {
		if !seen[required] {
			t.Fatalf("required eval case %s missing", required)
		}
	}
}
