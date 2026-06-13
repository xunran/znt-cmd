package replay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"znt/internal/contracts"
)

type Report struct {
	TraceID             contracts.TraceID      `json:"trace_id"`
	TenantID            contracts.TenantID     `json:"tenant_id,omitempty"`
	Status              string                 `json:"status"`
	EventCount          int                    `json:"event_count"`
	FirstAt             *time.Time             `json:"first_at,omitempty"`
	LastAt              *time.Time             `json:"last_at,omitempty"`
	EventTypes          map[string]int         `json:"event_types"`
	RunIDs              []contracts.AgentRunID `json:"run_ids,omitempty"`
	TaskIDs             []contracts.TaskID     `json:"task_ids,omitempty"`
	PromptBundleHashes  []string               `json:"prompt_bundle_hashes,omitempty"`
	ContextStrategyHashes []string              `json:"context_strategy_hashes,omitempty"`
	PolicyVersions      []string               `json:"policy_versions,omitempty"`
	ToolResultCount     int                    `json:"tool_result_count"`
	HandoffCount        int                    `json:"handoff_count"`
	ContextCompressionCount int                 `json:"context_compression_count,omitempty"`
	ContextCompressionApplied int               `json:"context_compression_applied,omitempty"`
	ContextCompressionFailures int              `json:"context_compression_failures,omitempty"`
	RedactionViolations []string               `json:"redaction_violations,omitempty"`
	Problems            []string               `json:"problems,omitempty"`
}

func Build(events []contracts.TraceEvent) Report {
	report := Report{
		Status:     "ok",
		EventCount: len(events),
		EventTypes: map[string]int{},
	}
	if len(events) == 0 {
		report.Status = "empty"
		report.Problems = append(report.Problems, "trace has no events")
		return report
	}
	report.TraceID = events[0].TraceID
	report.TenantID = events[0].TenantID
	runIDs := map[contracts.AgentRunID]struct{}{}
	taskIDs := map[contracts.TaskID]struct{}{}
	promptHashes := map[string]struct{}{}
	contextStrategyHashes := map[string]struct{}{}
	policyVersions := map[string]struct{}{}
	for i, event := range events {
		if event.TraceID != report.TraceID {
			report.Problems = append(report.Problems, "trace contains multiple trace_id values")
		}
		if event.TenantID != report.TenantID {
			report.Problems = append(report.Problems, "trace contains multiple tenant_id values")
		}
		report.EventTypes[event.Type]++
		if event.RunID != "" {
			runIDs[event.RunID] = struct{}{}
		}
		if event.TaskID != "" {
			taskIDs[event.TaskID] = struct{}{}
		}
		if value, ok := event.Payload["prompt_bundle_hash"].(string); ok && value != "" {
			promptHashes[value] = struct{}{}
		}
		if value, ok := event.Payload["hash"].(string); ok && event.Type == contracts.TracePromptBundleBuilt && value != "" {
			promptHashes[value] = struct{}{}
		}
		if event.Type == contracts.TraceContextCollectionCompleted {
			if contextReport, ok := contextAssemblyReportFromAny(event.Payload["context_assembly_report"]); ok && contextReport.StrategyHash != "" {
				contextStrategyHashes[contextReport.StrategyHash] = struct{}{}
			}
		}
		if event.Type == contracts.TracePromptBundleBuilt {
			if contextReport, ok := contextAssemblyReportFromAny(event.Payload["context_assembly_report"]); ok && contextReport.StrategyHash != "" {
				contextStrategyHashes[contextReport.StrategyHash] = struct{}{}
			}
		}
		if event.Type == contracts.TraceStrategyResolved {
			if value, ok := event.Payload["strategy_hash"].(string); ok && value != "" {
				contextStrategyHashes[value] = struct{}{}
			}
		}
		if event.Type == contracts.TraceContextCompressionCompleted {
			report.ContextCompressionCount++
			if applied, _ := event.Payload["applied"].(bool); applied {
				report.ContextCompressionApplied++
			}
			if reason, _ := event.Payload["failure_reason"].(string); reason != "" {
				report.ContextCompressionFailures++
			}
		}
		if value, ok := event.Payload["policy_set_version"].(string); ok && value != "" {
			policyVersions[value] = struct{}{}
		}
		if event.Type == contracts.TraceToolCompleted || event.Type == contracts.TraceToolFailed || event.Type == contracts.TraceToolDenied || event.Type == contracts.TraceToolPendingApproval {
			report.ToolResultCount++
		}
		if event.Type == contracts.TraceHandoffCreated || event.Type == contracts.TraceHandoffCompleted {
			report.HandoffCount++
		}
		for _, path := range sensitivePayloadPaths(event.Payload, "payload") {
			violation := event.Type + " " + path
			report.RedactionViolations = append(report.RedactionViolations, violation)
			report.Problems = append(report.Problems, "sensitive trace payload: "+violation)
		}
		if i == 0 || event.CreatedAt.Before(*report.FirstAt) {
			at := event.CreatedAt
			report.FirstAt = &at
		}
		if i == 0 || event.CreatedAt.After(*report.LastAt) {
			at := event.CreatedAt
			report.LastAt = &at
		}
	}
	report.RunIDs = sortedRunIDs(runIDs)
	report.TaskIDs = sortedTaskIDs(taskIDs)
	report.PromptBundleHashes = sortedStrings(promptHashes)
	report.ContextStrategyHashes = sortedStrings(contextStrategyHashes)
	report.PolicyVersions = sortedStrings(policyVersions)
	if report.EventTypes[contracts.TraceRunCreated] == 0 {
		report.Problems = append(report.Problems, "missing run.created event")
	}
	for _, required := range []string{
		contracts.TraceInputReceived,
		contracts.TraceAgentLoaded,
		contracts.TraceCapabilityRetrieved,
		contracts.TracePromptBundleBuilt,
		contracts.TraceDecisionCreated,
		contracts.TraceDecisionValidated,
	} {
		if report.EventTypes[required] == 0 {
			report.Problems = append(report.Problems, "missing "+required+" event")
		}
	}
	if report.EventTypes[contracts.TraceTaskCreated] == 0 && report.EventTypes[contracts.TraceTaskLoaded] == 0 {
		report.Problems = append(report.Problems, "missing task.created or task.loaded event")
	}
	if report.EventTypes[contracts.TraceModelCalled] != report.EventTypes[contracts.TraceModelCompleted] {
		report.Problems = append(report.Problems, "model.called/model.completed count mismatch")
	}
	if report.EventTypes[contracts.TraceModelCalled] > 0 && len(report.PromptBundleHashes) == 0 {
		report.Problems = append(report.Problems, "missing prompt bundle hash evidence")
	}
	if report.EventTypes[contracts.TraceRunCreated] > 0 && len(report.PolicyVersions) == 0 {
		report.Problems = append(report.Problems, "missing policy version evidence")
	}
	if report.EventTypes[contracts.TraceResponseSent] == 0 &&
		report.EventTypes[contracts.TraceToolPendingApproval] == 0 &&
		report.EventTypes[contracts.TraceToolFailed] == 0 {
		report.Problems = append(report.Problems, "trace has no terminal response, pending approval, or tool failure marker")
	}
	if len(report.Problems) > 0 {
		report.Status = "incomplete"
	}
	return report
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
	if report.StrategyHash == "" && report.Mode == "" && len(report.Sources) == 0 {
		return contracts.ContextAssemblyReport{}, false
	}
	return report, true
}

func sensitivePayloadPaths(value any, path string) []string {
	switch typed := value.(type) {
	case map[string]any:
		out := make([]string, 0)
		for key, child := range typed {
			keyPath := path + "." + key
			normalized := normalizeSensitiveKey(key)
			if allowedReferenceKey(normalized) {
				continue
			}
			if sensitiveKey(normalized) {
				out = append(out, keyPath)
				continue
			}
			out = append(out, sensitivePayloadPaths(child, keyPath)...)
		}
		sort.Strings(out)
		return out
	case []any:
		out := make([]string, 0)
		for i, child := range typed {
			out = append(out, sensitivePayloadPaths(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return out
	case string:
		if looksLikeSensitiveValue(typed) {
			return []string{path}
		}
	}
	return nil
}

func normalizeSensitiveKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	key = strings.ReplaceAll(key, ".", "")
	return key
}

func allowedReferenceKey(key string) bool {
	switch key {
	case "credentialref", "secretref", "ciphertextref", "artifactref", "artifactrefs", "promptbundlehash", "idempotencykey":
		return true
	default:
		return false
	}
}

func sensitiveKey(key string) bool {
	switch key {
	case "apikey", "authorization", "password", "passwd", "secret", "clientsecret", "accesstoken", "refreshtoken", "sessiontoken", "privatekey":
		return true
	default:
		return false
	}
}

func looksLikeSensitiveValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if len(trimmed) >= 12 && (strings.HasPrefix(trimmed, "sk-") || strings.HasPrefix(trimmed, "sk_")) {
		return true
	}
	if strings.HasPrefix(lower, "bearer ") && len(trimmed) > len("bearer ")+8 {
		return true
	}
	if strings.Contains(lower, "-----begin private key-----") {
		return true
	}
	for _, marker := range []string{"api_key=", "apikey=", "password=", "access_token=", "refresh_token="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sortedRunIDs(values map[contracts.AgentRunID]struct{}) []contracts.AgentRunID {
	out := make([]contracts.AgentRunID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedTaskIDs(values map[contracts.TaskID]struct{}) []contracts.TaskID {
	out := make([]contracts.TaskID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
