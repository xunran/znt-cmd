package handoff

import (
	"context"
	"fmt"
	"strings"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	policyengine "znt/internal/policy/engine"
	taskhandoff "znt/internal/task/handoff"
	taskrepo "znt/internal/task/repository"
	"znt/pkg/idgen"
)

type RunResult struct {
	RunID  contracts.AgentRunID     `json:"run_id"`
	TaskID contracts.TaskID         `json:"task_id"`
	Status contracts.RunStatus      `json:"status"`
	Reply  *contracts.DecisionReply `json:"reply,omitempty"`
}

type Executor struct {
	Agents              loader.Loader
	Tasks               taskrepo.TaskRepository
	Handoffs            *taskhandoff.Service
	StartTaskRun        func(ctx context.Context, envelope contracts.AgentEnvelope, task contracts.Task) (RunResult, error)
	Policies            policyengine.Store
	TargetReleaseLookup func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentPackageVersion, bool, error)
	Now                 func() time.Time
}

func (e Executor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	parentTaskID := stringValue(call.Arguments, "parent_task_id")
	toAgentID := stringValue(call.Arguments, "to_agent_id")
	objective := stringValue(call.Arguments, "objective")
	if parentTaskID == "" {
		parentTaskID = string(call.TaskID)
	}
	if objective == "" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "origin.agent.delegate requires objective", nil)
	}
	if strings.TrimSpace(toAgentID) == "" {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "origin.agent.delegate requires to_agent_id", nil)
	}
	parent, err := e.Tasks.Get(ctx, contracts.TaskID(parentTaskID))
	if err != nil {
		return nil, nil, err
	}
	if parent.TenantID != call.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "parent task tenant does not match caller tenant", nil)
	}
	sourceAgent, err := e.Agents.Load(ctx, call.TenantID, parent.AgentID, parent.AgentVersion)
	if err != nil {
		return nil, nil, err
	}
	targetAgentID := contracts.AgentID(strings.TrimSpace(toAgentID))
	collaborator, ok := collaboratorFor(sourceAgent, targetAgentID)
	if !ok {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "target agent is not in source agent collaborators", map[string]any{"to_agent_id": strings.TrimSpace(toAgentID)})
	}
	retrieved, ok := call.Arguments["_retrieved_collaborators"]
	if !ok {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "origin.agent.delegate requires current step retrieved collaborators", map[string]any{"to_agent_id": strings.TrimSpace(toAgentID)})
	}
	if !retrievedCollaborator(retrieved, targetAgentID) {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "target agent was not retrieved as a collaborator in this step", map[string]any{"to_agent_id": strings.TrimSpace(toAgentID)})
	}
	targetAgent, matched, err := e.resolveTargetAgent(ctx, call.TenantID, toAgentID, contracts.AgentVersion(stringValue(call.Arguments, "to_agent_version")), stringValue(call.Arguments, "capability_query"), objective)
	if err != nil {
		return nil, nil, err
	}
	if err := e.validateTargetRunnable(ctx, call.TenantID, targetAgent.AgentID, targetAgent.Version); err != nil {
		return nil, nil, err
	}
	policySet := e.policySet(ctx, call.TenantID, sourceAgent.PolicyRefs.PolicySetID)
	policy := policySet.HandoffPolicy
	mode := contracts.HandoffMode(stringValue(call.Arguments, "handoff_mode"))
	if mode == "" {
		mode = collaborator.DefaultHandoffMode
	}
	if err := validateCollaboratorHandoffMode(collaborator, mode); err != nil {
		return nil, nil, err
	}
	if collaborator.MaxContextTokens > 0 && (policy.MaxContextTokens == 0 || collaborator.MaxContextTokens < policy.MaxContextTokens) {
		policy.MaxContextTokens = collaborator.MaxContextTokens
	}
	if collaborator.RequiresApproval {
		policy.RequireApprovalForCrossAgent = true
	}
	if err := e.validateHandoffChain(ctx, parent, targetAgent.AgentID, sourceAgent.Runtime.MaxHandoffDepth); err != nil {
		return nil, nil, err
	}
	result, err := e.Handoffs.Create(ctx, taskhandoff.CreateInput{
		TenantID:          call.TenantID,
		TraceID:           contracts.TraceID(stringValue(call.Arguments, "trace_id")),
		ParentTaskID:      contracts.TaskID(parentTaskID),
		SourceRunID:       call.RunID,
		FromAgentID:       sourceAgent.AgentID,
		ToAgentID:         targetAgent.AgentID,
		ToAgentVersion:    targetAgent.Version,
		ToPolicySetID:     targetAgent.PolicyRefs.PolicySetID,
		TargetTenantID:    targetAgent.TenantID,
		Objective:         objective,
		Reason:            stringValue(call.Arguments, "reason"),
		Mode:              mode,
		ArtifactRefs:      artifactRefs(call.Arguments["artifact_refs"]),
		MemoryRefs:        memoryRefs(call.Arguments["memory_refs"]),
		ExpectedOutput:    expectedOutput(call.Arguments["expected_output"]),
		Policy:            policy,
		ToAgentExists:     true,
		CapabilityMatched: matched,
		CapabilityChecked: true,
		ActorID:           string(sourceAgent.AgentID),
	})
	if err != nil || result.ChildTask == nil {
		return map[string]any{"decision": result.Decision, "handoff": result.Handoff}, nil, err
	}
	if e.StartTaskRun == nil {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeModelError, "handoff task runner is not configured", nil)
	}
	targetRun, err := e.StartTaskRun(ctx, contracts.AgentEnvelope{
		EnvelopeID: idgen.New("env"),
		TraceID:    contracts.TraceID(stringValue(call.Arguments, "trace_id")),
		Target:     contracts.AgentTarget{AgentID: result.ChildTask.AgentID, Version: result.ChildTask.AgentVersion},
		Caller:     contracts.AgentCaller{CallerID: string(sourceAgent.AgentID), CallerType: "agent", TenantID: call.TenantID},
		Command:    "agent.run",
		Payload:    map[string]any{"input": result.ChildTask.Objective},
		Context: contracts.RuntimeContext{
			TenantID: call.TenantID,
			TaskID:   result.ChildTask.TaskID,
		},
		CreatedAt: e.now(),
	}, *result.ChildTask)
	if err != nil {
		return nil, nil, err
	}
	if targetRun.Status == contracts.RunCompleted {
		completed, err := e.Handoffs.Complete(ctx, result.Handoff.HandoffID, string(sourceAgent.AgentID), contracts.TraceID(stringValue(call.Arguments, "trace_id")), map[string]any{"run_id": targetRun.RunID, "reply": targetRun.Reply})
		if err != nil {
			return nil, nil, err
		}
		result.Handoff = completed
	}
	return map[string]any{
		"handoff":    result.Handoff,
		"package":    result.Package,
		"decision":   result.Decision,
		"child_task": result.ChildTask,
		"target_run": targetRun,
	}, nil, nil
}

func (e Executor) resolveTargetAgent(ctx context.Context, tenantID contracts.TenantID, toAgentID string, version contracts.AgentVersion, capabilityQuery string, objective string) (contracts.AgentDefinition, bool, error) {
	toAgentID = strings.TrimSpace(toAgentID)
	if toAgentID == "" {
		return contracts.AgentDefinition{}, false, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "origin.agent.delegate requires to_agent_id", nil)
	}
	agent, err := e.Agents.Load(ctx, tenantID, contracts.AgentID(toAgentID), version)
	if err != nil {
		return contracts.AgentDefinition{}, false, err
	}
	query := strings.TrimSpace(capabilityQuery)
	if query == "" {
		return agent, true, nil
	}
	return agent, matchesAgentCapability(agent, query), nil
}

func matchesAgentCapability(agent contracts.AgentDefinition, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		string(agent.AgentID),
		agent.Name,
		agent.Description,
		agent.IdentityPrompt,
		agent.SystemPrompt,
		agent.DeveloperPrompt,
	}, " "))
	for _, skill := range agent.SkillDefinitions {
		haystack += " " + strings.ToLower(strings.Join(append([]string{skill.Card.SkillID, skill.Card.Name, skill.Card.Description}, skill.Card.Tags...), " "))
	}
	for _, token := range strings.Fields(query) {
		token = strings.Trim(token, ".,;:!?()[]{}\"'")
		if len(token) < 3 {
			continue
		}
		if strings.Contains(haystack, token) {
			return true
		}
	}
	return false
}

func collaboratorFor(source contracts.AgentDefinition, target contracts.AgentID) (contracts.AgentCollaboratorRef, bool) {
	for _, collaborator := range source.Collaborators {
		if collaborator.AgentID == target && collaboratorEnabled(collaborator.Status) {
			return collaborator, true
		}
	}
	return contracts.AgentCollaboratorRef{}, false
}

func collaboratorEnabled(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "enabled", "active":
		return true
	default:
		return false
	}
}

func validateCollaboratorHandoffMode(collaborator contracts.AgentCollaboratorRef, mode contracts.HandoffMode) error {
	if mode == "" || len(collaborator.AllowedHandoffModes) == 0 {
		return nil
	}
	for _, allowed := range collaborator.AllowedHandoffModes {
		if allowed == mode {
			return nil
		}
	}
	return contracts.NewRuntimeError(contracts.CodeHandoffDenied, "handoff mode is not allowed for collaborator", map[string]any{
		"to_agent_id":   collaborator.AgentID,
		"handoff_mode":  mode,
		"allowed_modes": collaborator.AllowedHandoffModes,
	})
}

func (e Executor) validateHandoffChain(ctx context.Context, parent contracts.Task, target contracts.AgentID, maxDepth int) error {
	if e.Tasks == nil {
		return nil
	}
	depth := 0
	seenTasks := map[contracts.TaskID]struct{}{}
	current := parent
	for {
		if _, seen := seenTasks[current.TaskID]; seen {
			return contracts.NewRuntimeError(contracts.CodeHandoffDenied, "handoff task chain cycle detected", map[string]any{"task_id": current.TaskID})
		}
		seenTasks[current.TaskID] = struct{}{}
		if current.AgentID == target {
			return contracts.NewRuntimeError(contracts.CodeHandoffDenied, "handoff cycle detected", map[string]any{"to_agent_id": target, "task_id": current.TaskID})
		}
		if current.ParentTaskID == nil {
			break
		}
		depth++
		next, err := e.Tasks.Get(ctx, *current.ParentTaskID)
		if err != nil {
			return err
		}
		current = next
	}
	if maxDepth > 0 && depth+1 > maxDepth {
		return contracts.NewRuntimeError(contracts.CodeHandoffDenied, "max handoff depth exceeded", map[string]any{"max_handoff_depth": maxDepth, "handoff_depth": depth + 1})
	}
	return nil
}

func (e Executor) validateTargetRunnable(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) error {
	if e.TargetReleaseLookup == nil {
		return nil
	}
	release, ok, err := e.TargetReleaseLookup(ctx, tenantID, agentID, version)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if isRunnableAgentReleaseStatus(release.Status) {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeHandoffDenied, "target agent package version is not runnable for handoff", map[string]any{
		"agent_id":           agentID,
		"agent_version":      version,
		"package_version_id": release.PackageVersionID,
		"release_status":     release.Status,
	})
}

func isRunnableAgentReleaseStatus(status contracts.ReleaseStatus) bool {
	switch status {
	case contracts.ReleasePublished, contracts.ReleaseEvaluated, contracts.ReleaseCanary, contracts.ReleaseStable:
		return true
	default:
		return false
	}
}

func retrievedCollaborator(value any, target contracts.AgentID) bool {
	if target == "" {
		return false
	}
	switch raw := value.(type) {
	case []string:
		for _, id := range raw {
			if contracts.AgentID(strings.TrimSpace(id)) == target {
				return true
			}
		}
	case []any:
		for _, item := range raw {
			if id, ok := item.(string); ok && contracts.AgentID(strings.TrimSpace(id)) == target {
				return true
			}
		}
	case map[string]any:
		if id, ok := raw["agent_id"].(string); ok && contracts.AgentID(strings.TrimSpace(id)) == target {
			return true
		}
	}
	return false
}

func (e Executor) policySet(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	if e.Policies == nil {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	policy, ok, err := e.Policies.Get(ctx, tenantID, policySetID)
	if err != nil || !ok {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	return policy
}

func (e Executor) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func artifactRefs(value any) []contracts.ArtifactRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.ArtifactRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refs = append(refs, contracts.ArtifactRef{
			ArtifactID: contracts.ArtifactID(stringValue(row, "artifact_id")),
			Type:       stringValue(row, "type"),
			URI:        stringValue(row, "uri"),
			Summary:    stringValue(row, "summary"),
			Hash:       stringValue(row, "hash"),
		})
	}
	return refs
}

func memoryRefs(value any) []contracts.MemoryID {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.MemoryID, 0, len(raw))
	for _, item := range raw {
		if id, ok := item.(string); ok && id != "" {
			refs = append(refs, contracts.MemoryID(id))
		}
	}
	return refs
}

func expectedOutput(value any) contracts.ExpectedOutput {
	row, ok := value.(map[string]any)
	if !ok {
		return contracts.ExpectedOutput{}
	}
	rawRequirements, _ := row["requirements"].([]any)
	requirements := make([]string, 0, len(rawRequirements))
	for _, item := range rawRequirements {
		if value, ok := item.(string); ok {
			requirements = append(requirements, value)
		}
	}
	return contracts.ExpectedOutput{
		Format:       stringValue(row, "format"),
		Requirements: requirements,
	}
}

func ValidateTargetCapability(agent contracts.AgentDefinition, query string) error {
	if matchesAgentCapability(agent, query) {
		return nil
	}
	return fmt.Errorf("target agent %s does not match capability query", agent.AgentID)
}
