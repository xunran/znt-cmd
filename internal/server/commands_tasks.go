package server

import (
	"net/http"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	taskhandoff "znt/internal/task/handoff"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	toolruntime "znt/internal/tool/runtime"
	"znt/pkg/idgen"
)

func isPlanCommand(command contracts.TaskCommand) bool {
	switch command {
	case contracts.CmdCreatePlan, contracts.CmdUpdatePlan, contracts.CmdReplan, contracts.CmdCompleteStep, contracts.CmdFailStep:
		return true
	default:
		return false
	}
}

func isHandoffCommand(command contracts.TaskCommand) bool {
	switch command {
	case contracts.CmdCreateHandoff, contracts.CmdAcceptHandoff, contracts.CmdRejectHandoff, contracts.CmdCompleteHandoff, contracts.CmdFailHandoff:
		return true
	default:
		return false
	}
}

func planCommand(r *http.Request, appCore *core.Core, taskID contracts.TaskID, command contracts.TaskCommand, caller auth.CallerIdentity, payload map[string]any) (any, error) {
	task, err := appCore.TaskRepo.Get(r.Context(), taskID)
	if err != nil {
		return nil, err
	}
	if task.TenantID != caller.TenantID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil)
	}
	switch command {
	case contracts.CmdCreatePlan:
		objective, _ := payload["objective"].(string)
		plan, steps, event, err := appCore.Plans.CreatePlan(r.Context(), task, objective, parsePlanSteps(payload["steps"]), caller.CallerID, caller.CallerType)
		if err != nil {
			return nil, err
		}
		return map[string]any{"plan": plan, "steps": steps, "event": event}, nil
	case contracts.CmdReplan:
		objective, _ := payload["objective"].(string)
		reason, _ := payload["reason"].(string)
		plan, steps, event, err := appCore.Plans.Replan(r.Context(), task, objective, parsePlanSteps(payload["steps"]), caller.CallerID, caller.CallerType, reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{"plan": plan, "steps": steps, "event": event}, nil
	case contracts.CmdCompleteStep:
		stepID, _ := payload["step_id"].(string)
		if stepID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "complete_step requires step_id", nil)
		}
		step, event, err := appCore.Plans.CompleteStep(r.Context(), taskID, stepID, parseArtifactRefs(payload["result_refs"]), "", caller.CallerID, caller.CallerType)
		if err != nil {
			return nil, err
		}
		return map[string]any{"step": step, "event": event}, nil
	case contracts.CmdFailStep:
		stepID, _ := payload["step_id"].(string)
		reason, _ := payload["reason"].(string)
		if stepID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "fail_step requires step_id", nil)
		}
		step, event, err := appCore.Plans.FailStep(r.Context(), taskID, stepID, reason, caller.CallerID, caller.CallerType)
		if err != nil {
			return nil, err
		}
		return map[string]any{"step": step, "event": event}, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported plan command", map[string]any{"command": command})
	}
}

func handoffCommand(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, command contracts.TaskCommand, caller auth.CallerIdentity, payload map[string]any) (any, error) {
	switch command {
	case contracts.CmdCreateHandoff:
		parentTaskID := payloadString(payload, "task_id")
		toAgentID := payloadString(payload, "to_agent_id")
		objective := payloadString(payload, "objective")
		if parentTaskID == "" || toAgentID == "" || objective == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "create_handoff requires task_id, to_agent_id, and objective", nil)
		}
		parent, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(parentTaskID))
		if err != nil {
			return nil, err
		}
		if !sameTenant(parent.TenantID, caller.TenantID) {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil)
		}
		if err := appCore.EnsureAgentRunnable(r.Context(), caller.TenantID, parent.AgentID); err != nil {
			return nil, err
		}
		targetVersion := contracts.AgentVersion(payloadString(payload, "to_agent_version"))
		target, err := appCore.Agents.Load(r.Context(), caller.TenantID, contracts.AgentID(toAgentID), targetVersion)
		if err != nil {
			return nil, err
		}
		if err := ensureRunnableAgentVersion(appCore, caller.TenantID, contracts.AgentTarget{AgentID: target.AgentID, Version: target.Version}); err != nil {
			return nil, err
		}
		policy := appCore.LoadPolicySet(r.Context(), caller.TenantID, parent.PolicySetID)
		mode := contracts.HandoffMode(payloadString(payload, "handoff_mode"))
		result, err := appCore.Handoffs.Create(r.Context(), taskhandoff.CreateInput{
			TenantID:       caller.TenantID,
			TraceID:        envelope.TraceID,
			ParentTaskID:   parent.TaskID,
			SourceRunID:    contracts.AgentRunID(payloadString(payload, "source_run_id")),
			FromAgentID:    parent.AgentID,
			ToAgentID:      target.AgentID,
			ToAgentVersion: target.Version,
			ToPolicySetID:  target.PolicyRefs.PolicySetID,
			TargetTenantID: target.TenantID,
			Objective:      objective,
			Reason:         payloadString(payload, "reason"),
			Mode:           mode,
			ArtifactRefs:   parseArtifactRefs(payload["artifact_refs"]),
			MemoryRefs:     parseMemoryRefs(payload["memory_refs"]),
			ExpectedOutput: parseExpectedOutput(payload["expected_output"]),
			Policy:         policy.HandoffPolicy,
			ToAgentExists:  true,
			ActorID:        caller.CallerID,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	case contracts.CmdAcceptHandoff:
		return transitionHandoffCommand(r, appCore, caller, payload, contracts.HandoffAccepted, envelope.TraceID)
	case contracts.CmdRejectHandoff:
		return transitionHandoffCommand(r, appCore, caller, payload, contracts.HandoffRejected, envelope.TraceID)
	case contracts.CmdCompleteHandoff:
		handoffID := contracts.HandoffID(payloadString(payload, "handoff_id"))
		if handoffID == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "complete_handoff requires handoff_id", nil)
		}
		current, ok := appCore.Handoffs.Get(handoffID)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "handoff not found", map[string]any{"handoff_id": handoffID})
		}
		if !sameTenant(current.TenantID, caller.TenantID) {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "handoff tenant does not match caller tenant", nil)
		}
		output := parseMetadata(payload["output"])
		if output == nil {
			output = map[string]any{"reason": payloadString(payload, "reason")}
		}
		handoff, err := appCore.Handoffs.Complete(r.Context(), handoffID, caller.CallerID, envelope.TraceID, output)
		if err != nil {
			return nil, err
		}
		return map[string]any{"handoff": handoff}, nil
	case contracts.CmdFailHandoff:
		return transitionHandoffCommand(r, appCore, caller, payload, contracts.HandoffFailed, envelope.TraceID)
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported handoff command", map[string]any{"command": command})
	}
}

func transitionHandoffCommand(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, payload map[string]any, status contracts.HandoffStatus, traceID contracts.TraceID) (any, error) {
	handoffID := contracts.HandoffID(payloadString(payload, "handoff_id"))
	if handoffID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "handoff transition requires handoff_id", nil)
	}
	current, ok := appCore.Handoffs.Get(handoffID)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "handoff not found", map[string]any{"handoff_id": handoffID})
	}
	if !sameTenant(current.TenantID, caller.TenantID) {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "handoff tenant does not match caller tenant", nil)
	}
	handoff, err := appCore.Handoffs.Transition(r.Context(), handoffID, status, caller.CallerID, caller.CallerType, traceID, payloadString(payload, "reason"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"handoff": handoff}, nil
}

func approveTaskAction(r *http.Request, appCore *core.Core, task contracts.Task, caller auth.CallerIdentity, payload map[string]any) (any, error) {
	updated, event, transition, err := appCore.TaskRuntime.ApplyCommand(r.Context(), taskcommandInput(string(task.TaskID), string(contracts.CmdApproveAction), caller, payload))
	if err != nil {
		return nil, err
	}
	call, _, ok, err := latestPendingApprovalTool(r, appCore, task.TaskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{"task": updated, "event": event, "transition": transition, "resumed": false}, nil
	}
	run, err := appCore.Runs.Get(r.Context(), call.RunID)
	if err != nil {
		return nil, err
	}
	agent, err := appCore.Agents.Load(r.Context(), caller.TenantID, run.AgentID, run.AgentVersion)
	if err != nil {
		return nil, err
	}
	result, err := appCore.ToolRuntime.Invoke(r.Context(), toolruntime.InvokeRequest{
		TenantID:        caller.TenantID,
		TraceID:         run.TraceID,
		ActorID:         caller.CallerID,
		ActorType:       caller.CallerType,
		Agent:           agent,
		PolicySet:       appCore.LoadPolicySet(r.Context(), caller.TenantID, agent.PolicyRefs.PolicySetID),
		Call:            call,
		ApprovalGranted: true,
	})
	if err != nil {
		return nil, err
	}
	if err := appCore.ToolRepo.SaveResult(r.Context(), result); err != nil {
		return nil, err
	}
	if _, _, _, err := appCore.TaskRuntime.ApplyCommand(r.Context(), taskruntime.CommandInput{
		TaskID:    task.TaskID,
		Command:   contracts.CmdToolWaiting,
		ActorID:   caller.CallerID,
		ActorType: caller.CallerType,
		RunID:     call.RunID,
		StepID:    call.PlanStepID,
	}); err != nil {
		return nil, err
	}
	if _, _, _, err := appCore.TaskRuntime.ApplyCommand(r.Context(), taskruntime.CommandInput{
		TaskID:    task.TaskID,
		Command:   contracts.CmdToolCompleted,
		ActorID:   caller.CallerID,
		ActorType: caller.CallerType,
		RunID:     call.RunID,
		StepID:    call.PlanStepID,
		Payload:   map[string]any{"tool_result_id": result.ToolResultID, "status": result.Status},
	}); err != nil {
		return nil, err
	}
	resume, err := appCore.Coordinator.ResumeRun(r.Context(), contracts.AgentEnvelope{
		EnvelopeID: idgen.New("env"),
		TraceID:    run.TraceID,
		Target:     contracts.AgentTarget{AgentID: run.AgentID, Version: run.AgentVersion},
		Caller:     contracts.AgentCaller{CallerID: caller.CallerID, CallerType: caller.CallerType, DisplayName: caller.DisplayName, TenantID: caller.TenantID},
		Command:    "agent.run",
		Payload:    map[string]any{},
		Context: contracts.RuntimeContext{
			TenantID: caller.TenantID,
			TaskID:   task.TaskID,
		},
		CreatedAt: time.Now().UTC(),
	}, call.RunID, task.TaskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"task": updated, "event": event, "transition": transition, "tool_result": result, "run": resume, "resumed": true}, nil
}

func upgradeTaskAgentVersion(r *http.Request, appCore *core.Core, task contracts.Task, caller auth.CallerIdentity, payload map[string]any) (any, error) {
	targetVersion := contracts.AgentVersion(payloadString(payload, "agent_version"))
	if targetVersion == "" {
		targetVersion = contracts.AgentVersion(payloadString(payload, "to_agent_version"))
	}
	if targetVersion == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "upgrade_agent_version requires agent_version", nil)
	}
	targetAgentID := task.AgentID
	if raw := payloadString(payload, "agent_id"); raw != "" && contracts.AgentID(raw) != task.AgentID {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "upgrade_agent_version cannot switch task agent_id", map[string]any{"task_agent_id": task.AgentID, "requested_agent_id": raw})
	}
	if err := ensureRunnableAgentVersion(appCore, caller.TenantID, contracts.AgentTarget{AgentID: targetAgentID, Version: targetVersion}); err != nil {
		return nil, err
	}
	definition, err := appCore.Agents.Load(r.Context(), caller.TenantID, targetAgentID, targetVersion)
	if err != nil {
		return nil, err
	}
	updated := task
	updated.AgentVersion = definition.Version
	updated.PolicySetID = definition.PolicyRefs.PolicySetID
	updated.UpdatedAt = time.Now().UTC()
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, definition.PolicyRefs.PolicySetID)
	policyVersionID := ""
	if version, _, ok, err := appCore.PolicyManager.CurrentVersion(r.Context(), caller.TenantID, definition.PolicyRefs.PolicySetID); err == nil && ok {
		policyVersionID = string(version.PolicyVersionID)
	}
	event := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    task.TaskID,
		TenantID:  task.TenantID,
		Type:      "task.agent_version_upgraded",
		ActorID:   caller.CallerID,
		ActorType: caller.CallerType,
		Payload: map[string]any{
			"from_agent_version":   task.AgentVersion,
			"to_agent_version":     definition.Version,
			"from_policy_set_id":   task.PolicySetID,
			"to_policy_set_id":     definition.PolicyRefs.PolicySetID,
			"to_policy_version":    policySet.Version,
			"to_policy_version_id": policyVersionID,
			"command":              contracts.CmdUpgradeAgentVersion,
		},
		CreatedAt: updated.UpdatedAt,
	}
	if atomicRepo, ok := appCore.TaskRepo.(taskrepo.AtomicRepository); ok {
		if err := atomicRepo.UpdateWithVersionAndAppendEvent(r.Context(), updated, task.Version, event); err != nil {
			return nil, err
		}
	} else {
		if err := appCore.TaskRepo.UpdateWithVersion(r.Context(), updated, task.Version); err != nil {
			return nil, err
		}
		if err := appCore.TaskEvents.Append(r.Context(), event); err != nil {
			return nil, err
		}
	}
	if appCore.Audit != nil {
		_ = appCore.Audit.Log(r.Context(), contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     task.TenantID,
			ActorID:      caller.CallerID,
			ActorType:    caller.CallerType,
			Action:       "task.agent_version_upgraded",
			ResourceType: "task",
			ResourceID:   string(task.TaskID),
			Decision:     "allowed",
			Reason:       string(contracts.CmdUpgradeAgentVersion),
			TaskID:       task.TaskID,
			CreatedAt:    updated.UpdatedAt,
		})
	}
	reloaded, err := appCore.TaskRepo.Get(r.Context(), task.TaskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"task": reloaded, "event": event}, nil
}

func latestPendingApprovalTool(r *http.Request, appCore *core.Core, taskID contracts.TaskID) (contracts.ToolCall, contracts.ToolResult, bool, error) {
	events, err := appCore.TaskEvents.ListByTask(r.Context(), taskID)
	if err != nil {
		return contracts.ToolCall{}, contracts.ToolResult{}, false, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "task.approval_required" || events[i].RunID == "" {
			continue
		}
		results, err := appCore.ToolRepo.ListResultsByRun(r.Context(), events[i].RunID)
		if err != nil {
			return contracts.ToolCall{}, contracts.ToolResult{}, false, err
		}
		for j := len(results) - 1; j >= 0; j-- {
			if results[j].Status != contracts.ToolResultPendingApproval {
				continue
			}
			call, ok, err := appCore.ToolRepo.GetCall(r.Context(), results[j].ToolCallID)
			if err != nil {
				return contracts.ToolCall{}, contracts.ToolResult{}, false, err
			}
			if ok && call.TaskID == taskID {
				return call, results[j], true, nil
			}
		}
	}
	return contracts.ToolCall{}, contracts.ToolResult{}, false, nil
}

func taskStart(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	agentID := envelope.Target.AgentID
	if raw, _ := envelope.Payload["agent_id"].(string); raw != "" {
		agentID = contracts.AgentID(raw)
	}
	version := envelope.Target.Version
	if raw, _ := envelope.Payload["agent_version"].(string); raw != "" {
		version = contracts.AgentVersion(raw)
	}
	if contains(appCore.Config.DisabledAgentIDs, string(agentID)) {
		return nil, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent is disabled by release switch", map[string]any{"agent_id": agentID})
	}
	if err := ensureRunnableAgentVersion(appCore, caller.TenantID, contracts.AgentTarget{AgentID: agentID, Version: version}); err != nil {
		return nil, err
	}
	definition, err := appCore.Agents.Load(r.Context(), caller.TenantID, agentID, version)
	if err != nil {
		return nil, err
	}
	title, _ := envelope.Payload["title"].(string)
	if title == "" {
		title = "task.start"
	}
	objective, _ := envelope.Payload["objective"].(string)
	if objective == "" {
		objective, _ = envelope.Payload["input"].(string)
	}
	if objective == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "task.start requires objective or input", nil)
	}
	task := taskrepo.NewTask(contracts.TaskID(idgen.New("task")), caller.TenantID, definition.AgentID, definition.Version, definition.PolicyRefs.PolicySetID, title, objective, time.Now().UTC())
	created, err := appCore.TaskRuntime.CreateTask(r.Context(), task, caller.CallerID, caller.CallerType)
	if err != nil {
		return nil, err
	}
	var binding *contracts.ExternalTaskBinding
	if envelope.Context.ExternalTask != nil && envelope.Context.ExternalTask.ExternalTaskID != "" {
		bound, err := appCore.ArrayBridge.BindTask(r.Context(), contracts.ExternalTaskBinding{
			Provider:       envelope.Context.ExternalTask.Provider,
			ExternalTaskID: envelope.Context.ExternalTask.ExternalTaskID,
			CoreTaskID:     created.TaskID,
			TenantID:       caller.TenantID,
			SyncMode:       "two_way",
		})
		if err != nil {
			return nil, err
		}
		binding = &bound
	}
	return map[string]any{"task": created, "external_binding": binding}, nil
}

func delegateAgent(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	parentTaskID, _ := envelope.Payload["parent_task_id"].(string)
	objective, _ := envelope.Payload["objective"].(string)
	if parentTaskID == "" {
		parentTaskID = string(envelope.Context.TaskID)
	}
	if parentTaskID == "" || objective == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "origin.agent.delegate requires parent_task_id and objective", nil)
	}
	if strings.TrimSpace(payloadString(envelope.Payload, "to_agent_id")) == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "origin.agent.delegate requires to_agent_id", nil)
	}
	parent, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(parentTaskID))
	if err != nil {
		return nil, err
	}
	if !sameTenant(parent.TenantID, caller.TenantID) {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "parent task tenant does not match caller tenant", nil)
	}
	if err := appCore.EnsureAgentRunnable(r.Context(), caller.TenantID, envelope.Target.AgentID); err != nil {
		return nil, err
	}
	agent, err := appCore.Agents.Load(r.Context(), caller.TenantID, envelope.Target.AgentID, envelope.Target.Version)
	if err != nil {
		return nil, err
	}
	policySet := appCore.LoadPolicySet(r.Context(), caller.TenantID, agent.PolicyRefs.PolicySetID)
	arguments := map[string]any{}
	for key, value := range envelope.Payload {
		arguments[key] = value
	}
	arguments["parent_task_id"] = parentTaskID
	arguments["objective"] = objective
	arguments["trace_id"] = string(envelope.TraceID)
	if appCore.Coordinator.Tools != nil {
		candidates, err := appCore.Coordinator.Tools.Candidates(r.Context(), agent, policySet, objective)
		if err != nil {
			return nil, err
		}
		retrievedCollaborators := make([]string, 0, len(candidates.Collaborators))
		for _, collaborator := range candidates.Collaborators {
			if collaborator.AgentID != "" {
				retrievedCollaborators = append(retrievedCollaborators, string(collaborator.AgentID))
			}
		}
		arguments["_retrieved_collaborators"] = retrievedCollaborators
	}
	envelope.Payload["arguments"] = arguments
	call := contracts.ToolCall{
		ToolCallID:     contracts.ToolCallID(idgen.New("toolcall")),
		TenantID:       caller.TenantID,
		ToolID:         "origin.agent.delegate",
		Name:           "origin.agent.delegate",
		Arguments:      arguments,
		TraceID:        envelope.TraceID,
		RunID:          contracts.AgentRunID(payloadString(envelope.Payload, "source_run_id")),
		TaskID:         contracts.TaskID(parentTaskID),
		IdempotencyKey: idempotencyFromRequest(r, envelope, "origin.agent.delegate"),
		CreatedAt:      time.Now().UTC(),
	}
	saved, duplicate, err := appCore.ToolRepo.SaveCall(r.Context(), call)
	if err != nil {
		return nil, err
	}
	if duplicate {
		if existing, ok, err := appCore.ToolRepo.GetResultByCall(r.Context(), saved.ToolCallID); err != nil {
			return nil, err
		} else if ok {
			return existing.Output, nil
		}
	}
	result, err := appCore.ToolRuntime.Invoke(r.Context(), toolruntime.InvokeRequest{
		TenantID:  caller.TenantID,
		TraceID:   envelope.TraceID,
		ActorID:   caller.CallerID,
		ActorType: caller.CallerType,
		Agent:     agent,
		PolicySet: policySet,
		Call:      saved,
	})
	if err != nil {
		return nil, err
	}
	if err := appCore.ToolRepo.SaveResult(r.Context(), result); err != nil {
		return nil, err
	}
	if result.Status != contracts.ToolResultSucceeded {
		return result, nil
	}
	return result.Output, nil
}

func handoffCreateInput(caller auth.CallerIdentity, envelope contracts.AgentEnvelope, parentTaskID string, toAgentID string, objective string, reason string, mode contracts.HandoffMode, policy contracts.HandoffPolicy, targetAgent contracts.AgentDefinition) taskhandoff.CreateInput {
	return taskhandoff.CreateInput{
		TenantID:       caller.TenantID,
		TraceID:        envelope.TraceID,
		ParentTaskID:   contracts.TaskID(parentTaskID),
		SourceRunID:    contracts.AgentRunID(payloadString(envelope.Payload, "source_run_id")),
		FromAgentID:    envelope.Target.AgentID,
		ToAgentID:      contracts.AgentID(toAgentID),
		ToAgentVersion: targetAgent.Version,
		ToPolicySetID:  targetAgent.PolicyRefs.PolicySetID,
		TargetTenantID: targetAgent.TenantID,
		Objective:      objective,
		Reason:         reason,
		Mode:           mode,
		ArtifactRefs:   parseArtifactRefs(envelope.Payload["artifact_refs"]),
		MemoryRefs:     parseMemoryRefs(envelope.Payload["memory_refs"]),
		ExpectedOutput: parseExpectedOutput(envelope.Payload["expected_output"]),
		Policy:         policy,
		ToAgentExists:  true,
		ActorID:        caller.CallerID,
	}
}

func taskcommandInput(taskID string, command string, caller auth.CallerIdentity, payload map[string]any) taskruntime.CommandInput {
	cleanPayload := map[string]any{}
	for key, value := range payload {
		if key == "task_id" || key == "command" {
			continue
		}
		cleanPayload[key] = value
	}
	return taskruntime.CommandInput{
		TaskID:    contracts.TaskID(taskID),
		Command:   contracts.TaskCommand(command),
		ActorID:   caller.CallerID,
		ActorType: caller.CallerType,
		Payload:   cleanPayload,
	}
}
