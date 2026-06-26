package eval

import (
	"context"
	"encoding/json"
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
	StrategyAssertions    StrategyAssertions       `json:"strategy_assertions,omitempty"`
	CustomAssertions      CustomAssertions         `json:"custom_assertions,omitempty"`
}

type StrategyAssertions struct {
	StrategyHash       string   `json:"strategy_hash,omitempty"`
	ContextMode        string   `json:"context_mode,omitempty"`
	ContextSources     []string `json:"context_sources,omitempty"`
	CompressionApplied *bool    `json:"compression_applied,omitempty"`
	CompressionMode    string   `json:"compression_mode,omitempty"`
}

type StrategyEvidence struct {
	StrategyHash       string   `json:"strategy_hash,omitempty"`
	ContextMode        string   `json:"context_mode,omitempty"`
	ContextSources     []string `json:"context_sources,omitempty"`
	CompressionApplied *bool    `json:"compression_applied,omitempty"`
	CompressionMode    string   `json:"compression_mode,omitempty"`
}

type CustomAssertions struct {
	ExpectExpectedToolMissing bool `json:"expect_expected_tool_missing,omitempty"`
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
	Strategy       StrategyEvidence        `json:"strategy,omitempty"`
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
	strategyEvidence := r.strategyEvidence(ctx, traceID)
	failures = append(failures, strategyAssertionFailures(tc.StrategyAssertions, strategyEvidence)...)
	failures = append(failures, r.customAssertionFailures(ctx, traceID, tc.CustomAssertions)...)
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
		Strategy:       strategyEvidence,
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
		"strategy":           out.Strategy,
	})
	return out
}

func (r Runner) customAssertionFailures(ctx context.Context, traceID contracts.TraceID, assertions CustomAssertions) []string {
	if !assertions.ExpectExpectedToolMissing {
		return nil
	}
	if r.Coordinator.Trace == nil || traceID == "" {
		return []string{"expected_tool_missing trace missing"}
	}
	events, err := r.Coordinator.Trace.ListByTrace(ctx, traceID)
	if err != nil {
		return []string{err.Error()}
	}
	for _, event := range events {
		if event.Type == "expected_tool_missing" {
			return nil
		}
	}
	return []string{"expected_tool_missing trace missing"}
}

func (r Runner) strategyEvidence(ctx context.Context, traceID contracts.TraceID) StrategyEvidence {
	out := StrategyEvidence{}
	if r.Coordinator.Trace == nil || traceID == "" {
		return out
	}
	events, err := r.Coordinator.Trace.ListByTrace(ctx, traceID)
	if err != nil {
		return out
	}
	for _, event := range events {
		switch event.Type {
		case contracts.TraceStrategyResolved:
			if value, ok := event.Payload["strategy_hash"].(string); ok && value != "" {
				out.StrategyHash = value
			}
			if value, ok := event.Payload["context_mode"].(string); ok && value != "" {
				out.ContextMode = value
			}
			out.ContextSources = uniqueStrings(append(out.ContextSources, stringsFromAny(event.Payload["context_sources"])...))
		case contracts.TraceContextCompressionCompleted:
			applied, ok := event.Payload["applied"].(bool)
			if ok {
				out.CompressionApplied = &applied
			}
			if value, ok := event.Payload["mode"].(string); ok && value != "" {
				out.CompressionMode = value
			}
		case contracts.TracePromptBundleBuilt:
			report, ok := contextAssemblyReportFromAny(event.Payload["context_assembly_report"])
			if !ok {
				continue
			}
			if report.StrategyHash != "" {
				out.StrategyHash = report.StrategyHash
			}
			if report.Mode != "" {
				out.ContextMode = report.Mode
			}
			for _, source := range report.Sources {
				if source.SourceType != "" {
					out.ContextSources = uniqueStrings(append(out.ContextSources, source.SourceType))
				}
			}
			if report.Compression != nil {
				applied := report.Compression.Applied
				out.CompressionApplied = &applied
				out.CompressionMode = report.Compression.Mode
			}
		}
	}
	return out
}

func strategyAssertionFailures(assertions StrategyAssertions, evidence StrategyEvidence) []string {
	failures := make([]string, 0)
	if assertions.StrategyHash != "" && evidence.StrategyHash != assertions.StrategyHash {
		failures = append(failures, "strategy hash mismatch")
	}
	if assertions.ContextMode != "" && evidence.ContextMode != assertions.ContextMode {
		failures = append(failures, "context mode mismatch")
	}
	for _, source := range assertions.ContextSources {
		if source != "" && !containsString(evidence.ContextSources, source) {
			failures = append(failures, "context source missing: "+source)
		}
	}
	if assertions.CompressionApplied != nil {
		if evidence.CompressionApplied == nil || *evidence.CompressionApplied != *assertions.CompressionApplied {
			failures = append(failures, "compression applied mismatch")
		}
	}
	if assertions.CompressionMode != "" && evidence.CompressionMode != assertions.CompressionMode {
		failures = append(failures, "compression mode mismatch")
	}
	return failures
}

func evalCaller(runtimeContext contracts.RuntimeContext) contracts.AgentCaller {
	caller := contracts.AgentCaller{CallerID: "eval", CallerType: "system", TenantID: runtimeContext.TenantID}
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

func contextAssemblyReportFromAny(value any) (contracts.ContextAssemblyReport, bool) {
	if value == nil {
		return contracts.ContextAssemblyReport{}, false
	}
	if report, ok := value.(contracts.ContextAssemblyReport); ok {
		return report, true
	}
	if report, ok := value.(*contracts.ContextAssemblyReport); ok && report != nil {
		return *report, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return contracts.ContextAssemblyReport{}, false
	}
	var report contracts.ContextAssemblyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return contracts.ContextAssemblyReport{}, false
	}
	if report.StrategyHash == "" && report.Mode == "" && len(report.Sources) == 0 && report.Compression == nil {
		return contracts.ContextAssemblyReport{}, false
	}
	return report, true
}

func stringsFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
