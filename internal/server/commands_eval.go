package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/internal/eval"
	"znt/pkg/idgen"
)

func evalRun(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	input, _ := envelope.Payload["input"].(string)
	if input == "" {
		input = "eval"
	}
	contains := stringSlice(envelope.Payload["final_reply_contains"])
	status := contracts.RunStatus("")
	if raw, _ := envelope.Payload["should_end_status"].(string); raw != "" {
		status = contracts.RunStatus(raw)
	}
	packageVersionID, _ := envelope.Payload["package_version_id"].(string)
	policyVersionID, _ := envelope.Payload["policy_version_id"].(string)
	var evalRelease *contracts.AgentPackageVersion
	if packageVersionID != "" {
		release, err := ensurePackageReleaseTenant(appCore, contracts.PackageVersionID(packageVersionID), caller.TenantID)
		if err != nil {
			return nil, err
		}
		evalRelease = &release
	}
	target := envelope.Target
	if evalRelease != nil {
		if target.AgentID == "" {
			target.AgentID = evalRelease.AgentID
		}
		if target.Version == "" {
			target.Version = evalRelease.Version
		}
		if target.AgentID != evalRelease.AgentID || target.Version != evalRelease.Version {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval target must match package_version_id release", map[string]any{
				"package_version_id": packageVersionID,
				"release_agent_id":   evalRelease.AgentID,
				"release_version":    evalRelease.Version,
				"target_agent_id":    target.AgentID,
				"target_version":     target.Version,
			})
		}
	} else if target.AgentID == "" {
		if !strings.EqualFold(strings.TrimSpace(appCore.Config.Env), "test") {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval target is required when package_version_id is not provided", map[string]any{
				"command": "eval.run",
			})
		}
		target.AgentID = "test-agent"
		target.Version = "v1"
	}
	result := appCore.EvalRunner.Run(r.Context(), eval.Case{
		Name:                  fmt.Sprintf("eval_%s", envelope.EnvelopeID),
		SuiteID:               contracts.EvalSuiteID(payloadString(envelope.Payload, "suite_id")),
		Category:              payloadString(envelope.Payload, "category"),
		Critical:              payloadBool(envelope.Payload, "critical"),
		Safety:                payloadBool(envelope.Payload, "safety"),
		Input:                 input,
		Target:                target,
		Context:               envelope.Context,
		MustCallTools:         stringSlice(envelope.Payload["must_call_tools"]),
		ShouldNotCallTools:    stringSlice(envelope.Payload["should_not_call_tools"]),
		FinalReplyContains:    contains,
		FinalReplyNotContains: stringSlice(envelope.Payload["final_reply_not_contains"]),
		MaxToolCalls:          payloadInt(envelope.Payload, "max_tool_calls"),
		ShouldEndStatus:       status,
		StrategyAssertions:    evalStrategyAssertionsFromPayload(envelope.Payload["strategy_assertions"]),
		CustomAssertions:      evalCustomAssertionsFromPayload(envelope.Payload["custom_assertions"]),
	})
	suiteResult, err := appCore.Evals.SaveResult(r.Context(), eval.SuiteResult{
		EvalRunID:      result.EvalRunID,
		SuiteID:        result.SuiteID,
		TenantID:       caller.TenantID,
		Passed:         result.Passed,
		PassRate:       boolRate(result.Passed),
		ToolMisuseRate: singleResultToolMisuseRate(result),
		Failures:       result.Failures,
		Results:        []eval.Result{result},
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	resultEnvelope := envelope
	resultEnvelope.TraceID = result.TraceID
	recordEvalSummaryTrace(r, appCore, resultEnvelope, caller, suiteResult)
	if packageVersionID != "" {
		reason := "passed"
		if !result.Passed {
			reason = strings.Join(result.Failures, "; ")
		}
		if _, err := appCore.Packages.MarkEvalResult(r.Context(), contracts.PackageVersionID(packageVersionID), result.Passed, caller.CallerID, reason); err != nil {
			return nil, err
		}
	}
	if policyVersionID != "" {
		reason := "passed"
		if !result.Passed {
			reason = strings.Join(result.Failures, "; ")
		}
		if _, err := appCore.PolicyManager.MarkEvalResult(r.Context(), contracts.PolicyVersionID(policyVersionID), result.Passed, caller.CallerID, reason); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func evalSuiteCreate(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	name := payloadString(envelope.Payload, "name")
	if name == "" {
		name = "eval suite"
	}
	gates := parseEvalGates(envelope.Payload["gates"])
	if payloadBool(envelope.Payload, "require_critical_pass") {
		gates.RequireCriticalPass = true
	}
	if payloadBool(envelope.Payload, "require_safety_pass") {
		gates.RequireSafetyPass = true
	}
	if value := payloadFloat(envelope.Payload, "min_pass_rate"); value > 0 {
		gates.MinPassRate = value
	}
	if value := payloadFloat(envelope.Payload, "max_tool_misuse_rate"); value > 0 {
		gates.MaxToolMisuseRate = value
	}
	return appCore.Evals.CreateSuite(r.Context(), caller.TenantID, name, gates, caller.CallerID)
}

func evalSuiteAddCase(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	suiteID := contracts.EvalSuiteID(payloadString(envelope.Payload, "suite_id"))
	if suiteID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval.suite.add_case requires suite_id", nil)
	}
	current, ok, err := appCore.Evals.GetSuite(r.Context(), suiteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval suite not found", map[string]any{"suite_id": suiteID})
	}
	if current.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "eval suite tenant does not match caller tenant", nil)
	}
	tc, err := evalCaseFromPayload(envelope, envelope.Payload)
	if err != nil {
		return nil, err
	}
	suite, ok, err := appCore.Evals.AddCase(r.Context(), suiteID, tc)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval suite not found", map[string]any{"suite_id": suiteID})
	}
	return suite, nil
}

func evalSuiteRun(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	suiteID := contracts.EvalSuiteID(payloadString(envelope.Payload, "suite_id"))
	if suiteID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval.suite.run requires suite_id", nil)
	}
	suite, ok, err := appCore.Evals.GetSuite(r.Context(), suiteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "eval suite not found", map[string]any{"suite_id": suiteID})
	}
	if suite.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "eval suite tenant does not match caller tenant", nil)
	}
	results := make([]eval.Result, 0, len(suite.Cases))
	for _, tc := range suite.Cases {
		if tc.Context.TenantID == "" {
			tc.Context.TenantID = caller.TenantID
		}
		if tc.Target.AgentID == "" {
			tc.Target = envelope.Target
		}
		results = append(results, appCore.EvalRunner.Run(r.Context(), tc))
	}
	result, err := appCore.Evals.SaveResult(r.Context(), eval.BuildSuiteResult(suite, results, time.Now().UTC()))
	if err != nil {
		return nil, err
	}
	recordEvalSummaryTrace(r, appCore, envelope, caller, result)
	if packageVersionID := payloadString(envelope.Payload, "package_version_id"); packageVersionID != "" {
		reason := "passed"
		if !result.Passed {
			reason = strings.Join(result.Failures, "; ")
		}
		if _, err := appCore.Packages.MarkEvalResult(r.Context(), contracts.PackageVersionID(packageVersionID), result.Passed, caller.CallerID, reason); err != nil {
			return nil, err
		}
	}
	if policyVersionID := payloadString(envelope.Payload, "policy_version_id"); policyVersionID != "" {
		reason := "passed"
		if !result.Passed {
			reason = strings.Join(result.Failures, "; ")
		}
		if _, err := appCore.PolicyManager.MarkEvalResult(r.Context(), contracts.PolicyVersionID(policyVersionID), result.Passed, caller.CallerID, reason); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func recordEvalSummaryTrace(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity, result eval.SuiteResult) {
	if appCore.Trace == nil || envelope.TraceID == "" {
		return
	}
	_ = appCore.Trace.Record(r.Context(), contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  caller.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		Type:      contracts.TraceEvalSummaryCreated,
		CreatedAt: time.Now().UTC(),
		Payload: map[string]any{
			"eval_run_id":           result.EvalRunID,
			"suite_id":              result.SuiteID,
			"passed":                result.Passed,
			"case_count":            len(result.Results),
			"pass_rate":             result.PassRate,
			"tool_misuse_rate":      result.ToolMisuseRate,
			"failure_count":         len(result.Failures),
			"package_version_id":    payloadString(envelope.Payload, "package_version_id"),
			"policy_version_id":     payloadString(envelope.Payload, "policy_version_id"),
			"linked_case_trace_ids": evalCaseTraceIDs(result.Results),
		},
	})
}

func boolRate(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

func singleResultToolMisuseRate(result eval.Result) float64 {
	if result.ToolCallsTotal <= 0 {
		return 0
	}
	return float64(result.ToolMisuse) / float64(result.ToolCallsTotal)
}

func evalCaseTraceIDs(results []eval.Result) []string {
	out := make([]string, 0, len(results))
	for _, result := range results {
		if result.TraceID != "" {
			out = append(out, string(result.TraceID))
		}
	}
	return out
}

func evalCaseFromPayload(envelope contracts.AgentEnvelope, payload map[string]any) (eval.Case, error) {
	status := contracts.RunStatus("")
	if raw := payloadString(payload, "should_end_status"); raw != "" {
		status = contracts.RunStatus(raw)
	}
	target := envelope.Target
	if raw, ok := payload["target"].(map[string]any); ok {
		if agentID, _ := raw["agent_id"].(string); agentID != "" {
			target.AgentID = contracts.AgentID(agentID)
		}
		if version, _ := raw["version"].(string); version != "" {
			target.Version = contracts.AgentVersion(version)
		}
	}
	runtimeContext, err := runtimeContextFromEvalPayload(envelope.Context, payload)
	if err != nil {
		return eval.Case{}, err
	}
	return eval.Case{
		Name:                  payloadString(payload, "name"),
		SuiteID:               contracts.EvalSuiteID(payloadString(payload, "suite_id")),
		Category:              payloadString(payload, "category"),
		Critical:              payloadBool(payload, "critical"),
		Safety:                payloadBool(payload, "safety"),
		Input:                 payloadString(payload, "input"),
		Target:                target,
		Context:               runtimeContext,
		MustCallTools:         stringSlice(payload["must_call_tools"]),
		ShouldNotCallTools:    stringSlice(payload["should_not_call_tools"]),
		FinalReplyContains:    stringSlice(payload["final_reply_contains"]),
		FinalReplyNotContains: stringSlice(payload["final_reply_not_contains"]),
		MaxToolCalls:          payloadInt(payload, "max_tool_calls"),
		ShouldEndStatus:       status,
		StrategyAssertions:    evalStrategyAssertionsFromPayload(payload["strategy_assertions"]),
		CustomAssertions:      evalCustomAssertionsFromPayload(payload["custom_assertions"]),
	}, nil
}

func evalCustomAssertionsFromPayload(value any) eval.CustomAssertions {
	raw, ok := value.(map[string]any)
	if !ok {
		return eval.CustomAssertions{}
	}
	return eval.CustomAssertions{
		ExpectExpectedToolMissing: payloadBool(raw, "expect_expected_tool_missing"),
	}
}

func evalStrategyAssertionsFromPayload(value any) eval.StrategyAssertions {
	raw, ok := value.(map[string]any)
	if !ok {
		return eval.StrategyAssertions{}
	}
	return eval.StrategyAssertions{
		StrategyHash:       payloadString(raw, "strategy_hash"),
		ContextMode:        payloadString(raw, "context_mode"),
		ContextSources:     stringSlice(raw["context_sources"]),
		CompressionApplied: optionalBool(raw["compression_applied"]),
		CompressionMode:    payloadString(raw, "compression_mode"),
	}
}

func optionalBool(value any) *bool {
	switch typed := value.(type) {
	case bool:
		return &typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			parsed := true
			return &parsed
		case "false", "0", "no":
			parsed := false
			return &parsed
		}
		return nil
	default:
		return nil
	}
}

func runtimeContextFromEvalPayload(base contracts.RuntimeContext, payload map[string]any) (contracts.RuntimeContext, error) {
	raw, ok := payload["context"].(map[string]any)
	if !ok {
		return base, nil
	}
	if _, ok := raw["session_id"]; ok {
		return base, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "session_id is removed; use context.conversation.conversation_id", nil)
	}
	if _, ok := raw["collaboration"]; ok {
		return base, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "collaboration is removed; use context.conversation and context.external_task", nil)
	}
	if tenantID := payloadString(raw, "tenant_id"); tenantID != "" {
		base.TenantID = contracts.TenantID(tenantID)
	}
	if userID := payloadString(raw, "user_id"); userID != "" {
		base.UserID = contracts.UserID(userID)
	}
	if taskID := payloadString(raw, "task_id"); taskID != "" {
		base.TaskID = contracts.TaskID(taskID)
	}
	if locale := payloadString(raw, "locale"); locale != "" {
		base.Locale = locale
	}
	if timezone := payloadString(raw, "timezone"); timezone != "" {
		base.Timezone = timezone
	}
	if conversationRaw, ok := raw["conversation"].(map[string]any); ok {
		conversation := runtimeConversationFromPayload(conversationRaw)
		base.Conversation = &conversation
	}
	if externalTaskRaw, ok := raw["external_task"].(map[string]any); ok {
		base.ExternalTask = &contracts.ExternalTaskRef{
			Provider:       payloadString(externalTaskRaw, "provider"),
			ExternalTaskID: contracts.ExternalTaskID(payloadString(externalTaskRaw, "external_task_id")),
		}
	}
	return base, nil
}

func runtimeConversationFromPayload(raw map[string]any) contracts.RuntimeConversation {
	conversation := contracts.RuntimeConversation{
		Provider:       payloadString(raw, "provider"),
		Kind:           payloadString(raw, "kind"),
		ConversationID: payloadString(raw, "conversation_id"),
		ThreadID:       payloadString(raw, "thread_id"),
		ExternalRefs:   stringMap(raw["external_refs"]),
		RecentMessages: conversationMessagesFromPayload(raw["recent_messages"]),
		Participants:   conversationParticipantsFromPayload(raw["participants"]),
	}
	if currentRaw, ok := raw["current_message"].(map[string]any); ok {
		message := runtimeMessageFromPayload(currentRaw)
		conversation.CurrentMessage = &message
	}
	return conversation
}

func runtimeMessageFromPayload(raw map[string]any) contracts.RuntimeMessage {
	return contracts.RuntimeMessage{
		MessageID:         payloadString(raw, "message_id"),
		ExternalMessageID: payloadString(raw, "external_message_id"),
		SpeakerID:         payloadString(raw, "speaker_id"),
		SpeakerType:       payloadString(raw, "speaker_type"),
		SpeakerName:       payloadString(raw, "speaker_name"),
		ReplyToMessageID:  payloadString(raw, "reply_to_message_id"),
		ThreadID:          payloadString(raw, "thread_id"),
		Mentions:          stringSlice(raw["mentions"]),
		Text:              payloadString(raw, "text"),
	}
}

func stringMap(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if text, ok := value.(string); ok && text != "" {
			out[key] = text
		}
	}
	return out
}

func conversationMessagesFromPayload(value any) []contracts.ConversationMessage {
	rows, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contracts.ConversationMessage, 0, len(rows))
	for _, rowValue := range rows {
		row, ok := rowValue.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contracts.ConversationMessage{
			MessageID:         payloadString(row, "message_id"),
			ExternalMessageID: payloadString(row, "external_message_id"),
			SpeakerID:         payloadString(row, "speaker_id"),
			SpeakerType:       payloadString(row, "speaker_type"),
			SpeakerName:       payloadString(row, "speaker_name"),
			Text:              payloadString(row, "text"),
			ReplyToMessageID:  payloadString(row, "reply_to_message_id"),
			ThreadID:          payloadString(row, "thread_id"),
			Mentions:          stringSlice(row["mentions"]),
		})
	}
	return out
}

func conversationParticipantsFromPayload(value any) []contracts.ConversationParticipant {
	rows, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]contracts.ConversationParticipant, 0, len(rows))
	for _, rowValue := range rows {
		row, ok := rowValue.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, contracts.ConversationParticipant{
			ID:   payloadString(row, "id"),
			Type: payloadString(row, "type"),
			Name: payloadString(row, "name"),
			Role: payloadString(row, "role"),
		})
	}
	return out
}

func parseEvalGates(value any) eval.Gates {
	raw, ok := value.(map[string]any)
	if !ok {
		return eval.Gates{RequireCriticalPass: true, RequireSafetyPass: true, MinPassRate: 1}
	}
	return eval.Gates{
		RequireCriticalPass: boolFromAny(raw["require_critical_pass"]),
		RequireSafetyPass:   boolFromAny(raw["require_safety_pass"]),
		MinPassRate:         floatFromAny(raw["min_pass_rate"]),
		MaxToolMisuseRate:   floatFromAny(raw["max_tool_misuse_rate"]),
	}
}
