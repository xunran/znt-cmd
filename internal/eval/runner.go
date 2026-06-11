package eval

import (
	"context"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/internal/runtime/kernel"
	"znt/pkg/idgen"
)

type Case struct {
	Name                  string                   `json:"name"`
	SuiteID               contracts.EvalSuiteID    `json:"suite_id,omitempty"`
	Category              string                   `json:"category,omitempty"`
	Critical              bool                     `json:"critical,omitempty"`
	Safety                bool                     `json:"safety,omitempty"`
	Input                 string                   `json:"input"`
	Target                contracts.AgentTarget    `json:"target"`
	Context               contracts.RuntimeContext `json:"context"`
	MustCallTools         []string                 `json:"must_call_tools,omitempty"`
	ShouldNotCallTools    []string                 `json:"should_not_call_tools,omitempty"`
	FinalReplyContains    []string                 `json:"final_reply_contains,omitempty"`
	FinalReplyNotContains []string                 `json:"final_reply_not_contains,omitempty"`
	MaxToolCalls          int                      `json:"max_tool_calls,omitempty"`
	ShouldEndStatus       contracts.RunStatus      `json:"should_end_status,omitempty"`
}

type Result struct {
	EvalRunID      contracts.EvalRunID     `json:"eval_run_id"`
	SuiteID        contracts.EvalSuiteID   `json:"suite_id,omitempty"`
	CaseName       string                  `json:"case_name"`
	Category       string                  `json:"category,omitempty"`
	Critical       bool                    `json:"critical,omitempty"`
	Safety         bool                    `json:"safety,omitempty"`
	Passed         bool                    `json:"passed"`
	Failures       []string                `json:"failures,omitempty"`
	RunID          contracts.AgentRunID    `json:"run_id"`
	TraceID        contracts.TraceID       `json:"trace_id"`
	FinalReply     string                  `json:"final_reply,omitempty"`
	ToolCalls      []contracts.ToolCall    `json:"tool_calls,omitempty"`
	ToolResults    []contracts.ToolResult  `json:"tool_results,omitempty"`
	ArtifactRefs   []contracts.ArtifactRef `json:"artifact_refs,omitempty"`
	ToolCallsTotal int                     `json:"tool_calls_total"`
	ToolMisuse     int                     `json:"tool_misuse_total"`
	EndStatus      contracts.RunStatus     `json:"end_status,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
}

type Runner struct {
	Coordinator kernel.Coordinator
	Now         func() time.Time
}

func NewRunner(coordinator kernel.Coordinator) Runner {
	return Runner{Coordinator: coordinator, Now: func() time.Time { return time.Now().UTC() }}
}

func (r Runner) Run(ctx context.Context, tc Case) Result {
	traceID := contracts.TraceID(idgen.New("trace"))
	evalRunID := contracts.EvalRunID(idgen.New("eval"))
	r.trace(ctx, traceID, tc.Context.TenantID, contracts.TraceEvalRunStarted, map[string]any{
		"eval_run_id": evalRunID,
		"suite_id":    tc.SuiteID,
		"case_name":   tc.Name,
		"category":    tc.Category,
	})
	result, err := r.Coordinator.HandleEnvelope(ctx, contracts.AgentEnvelope{
		EnvelopeID: idgen.New("env"),
		TraceID:    traceID,
		Target:     tc.Target,
		Caller:     evalCaller(tc.Context),
		Command:    "agent.run",
		Payload:    map[string]any{"input": tc.Input},
		Context:    tc.Context,
		CreatedAt:  r.Now(),
	})
	failures := make([]string, 0)
	if err != nil {
		failures = append(failures, err.Error())
	}
	if tc.ShouldEndStatus != "" && result.Status != tc.ShouldEndStatus {
		failures = append(failures, "unexpected end status")
	}
	reply := ""
	if result.Reply != nil {
		reply = result.Reply.Text
	}
	if len(tc.FinalReplyContains) > 0 {
		for _, expected := range tc.FinalReplyContains {
			if !strings.Contains(reply, expected) {
				failures = append(failures, "final reply missing: "+expected)
			}
		}
	}
	for _, forbidden := range tc.FinalReplyNotContains {
		if forbidden != "" && strings.Contains(reply, forbidden) {
			failures = append(failures, "final reply contained forbidden text: "+forbidden)
		}
	}
	toolCallsTotal := 0
	toolMisuse := 0
	toolCalls := []contracts.ToolCall(nil)
	toolResults := []contracts.ToolResult(nil)
	artifactRefs := []contracts.ArtifactRef(nil)
	if r.Coordinator.ToolRepo != nil && result.RunID != "" {
		results, err := r.Coordinator.ToolRepo.ListResultsByRun(ctx, result.RunID)
		if err != nil {
			failures = append(failures, err.Error())
		} else {
			calledNames := r.calledToolNames(ctx, result.RunID)
			toolResults = results
			toolCallsTotal = len(toolResults)
			for _, required := range tc.MustCallTools {
				if !calledNames[required] {
					failures = append(failures, "required tool not called: "+required)
				}
			}
			for _, denied := range tc.ShouldNotCallTools {
				if calledNames[denied] {
					failures = append(failures, "forbidden tool called: "+denied)
					toolMisuse++
				}
			}
			if tc.MaxToolCalls > 0 && len(toolResults) > tc.MaxToolCalls {
				failures = append(failures, "max tool calls exceeded")
			}
		}
		calls, err := r.Coordinator.ToolRepo.ListCallsByRun(ctx, result.RunID)
		if err != nil {
			failures = append(failures, err.Error())
		} else {
			toolCalls = calls
		}
		for _, toolResult := range toolResults {
			artifactRefs = append(artifactRefs, toolResult.ArtifactRefs...)
		}
	}
	out := Result{
		EvalRunID:      evalRunID,
		SuiteID:        tc.SuiteID,
		CaseName:       tc.Name,
		Category:       tc.Category,
		Critical:       tc.Critical,
		Safety:         tc.Safety,
		Passed:         len(failures) == 0,
		Failures:       failures,
		RunID:          result.RunID,
		TraceID:        traceID,
		FinalReply:     reply,
		ToolCalls:      toolCalls,
		ToolResults:    toolResults,
		ArtifactRefs:   artifactRefs,
		ToolCallsTotal: toolCallsTotal,
		ToolMisuse:     toolMisuse,
		EndStatus:      result.Status,
		CreatedAt:      r.Now(),
	}
	eventType := contracts.TraceEvalCaseCompleted
	if !out.Passed {
		eventType = contracts.TraceEvalCaseFailed
	}
	r.trace(ctx, traceID, tc.Context.TenantID, eventType, map[string]any{
		"eval_run_id":        out.EvalRunID,
		"suite_id":           out.SuiteID,
		"case_name":          out.CaseName,
		"passed":             out.Passed,
		"failures":           out.Failures,
		"run_id":             out.RunID,
		"tool_calls_total":   out.ToolCallsTotal,
		"tool_misuse_total":  out.ToolMisuse,
		"artifact_ref_count": len(out.ArtifactRefs),
	})
	return out
}

func evalCaller(runtimeContext contracts.RuntimeContext) contracts.AgentCaller {
	caller := contracts.AgentCaller{CallerID: "eval", CallerType: "system", TenantID: runtimeContext.TenantID}
	if runtimeContext.Collaboration != nil {
		if runtimeContext.Collaboration.CallerID != "" {
			caller.CallerID = runtimeContext.Collaboration.CallerID
		}
		if runtimeContext.Collaboration.CallerType != "" {
			caller.CallerType = runtimeContext.Collaboration.CallerType
		}
	}
	if caller.CallerID == "" && runtimeContext.UserID != "" {
		caller.CallerID = string(runtimeContext.UserID)
		caller.CallerType = "user"
	}
	return caller
}

func (r Runner) calledToolNames(ctx context.Context, runID contracts.AgentRunID) map[string]bool {
	out := map[string]bool{}
	if r.Coordinator.ToolRepo == nil {
		return out
	}
	calls, err := r.Coordinator.ToolRepo.ListCallsByRun(ctx, runID)
	if err != nil {
		return out
	}
	for _, call := range calls {
		out[call.ToolID] = true
		out[call.Name] = true
	}
	return out
}

func (r Runner) trace(ctx context.Context, traceID contracts.TraceID, tenantID contracts.TenantID, eventType string, payload map[string]any) {
	if r.Coordinator.Trace == nil || traceID == "" {
		return
	}
	_ = r.Coordinator.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  tenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		Type:      eventType,
		Payload:   payload,
		CreatedAt: r.Now(),
	})
}
