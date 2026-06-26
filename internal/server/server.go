package server

import (
	"context"
	"net/http"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	runtimedriver "znt/internal/runtime/driver"
)

func dispatchCommand(r *http.Request, appCore *core.Core, metrics *metricsState, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if !allowedCommand(envelope.Command, caller) {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "caller role is not allowed for command", map[string]any{"command": envelope.Command})
	}
	switch envelope.Command {
	case "agent.run":
		if contains(appCore.Config.DisabledAgentIDs, string(envelope.Target.AgentID)) {
			return nil, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent is disabled by release switch", map[string]any{"agent_id": envelope.Target.AgentID})
		}
		releaseAdmission := func() {}
		if appCore.Admission != nil {
			release, err := appCore.Admission.AcquireRun(caller.TenantID, envelope.Target.AgentID)
			if err != nil {
				return nil, err
			}
			releaseAdmission = release
		}
		runStarted := time.Now()
		observeRun := func(failed bool) {
			if metrics != nil {
				metrics.observeAgentRun(time.Since(runStarted), failed)
			}
		}
		route, err := resolveRunnableAgentTarget(r, appCore, caller.TenantID, envelope.Target, envelope.Context, envelope.TraceID, caller)
		if err != nil {
			releaseAdmission()
			observeRun(true)
			return nil, err
		}
		driver, err := driverForRoute(appCore, route)
		if err != nil {
			releaseAdmission()
			observeRun(true)
			return nil, err
		}
		envelope.Target.Version = route.ResolvedVersion
		if appCore.Config.EffectiveAgentRunExecutionMode() == "async" {
			preparer, ok := driver.(runtimedriver.PreparedDriver)
			if !ok {
				releaseAdmission()
				observeRun(true)
				return nil, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "runtime driver does not support async prepared execution", map[string]any{
					"carrier_kind":     driver.Kind(),
					"runtime_contract": driver.Contract(),
				})
			}
			prepared, err := preparer.PrepareRun(r.Context(), runtimedriver.StartRunRequest{Envelope: envelope})
			if err != nil {
				releaseAdmission()
				observeRun(true)
				return nil, err
			}
			_ = recordAgentRouteResolved(r, appCore, caller.TenantID, envelope.TraceID, envelope.Target.AgentID, prepared.Run.RunID, route)
			if route.Canary {
				_ = recordCanaryRoute(r, appCore, caller.TenantID, envelope.TraceID, caller, envelope.Target.AgentID, prepared.Run.RunID, route.Release)
			}
			go func(driver runtimedriver.PreparedDriver, prepared runtimedriver.PreparedRun, release func()) {
				defer release()
				_, _ = driver.ExecutePreparedRun(context.Background(), prepared)
			}(preparer, prepared, releaseAdmission)
			observeRun(false)
			return prepared.Result(), nil
		}
		defer releaseAdmission()
		preparer, ok := driver.(runtimedriver.PreparedDriver)
		if !ok {
			result, err := driver.StartRun(r.Context(), runtimedriver.StartRunRequest{Envelope: envelope})
			observeRun(err != nil)
			if err == nil && route.Canary {
				_ = recordCanaryRoute(r, appCore, caller.TenantID, envelope.TraceID, caller, envelope.Target.AgentID, result.RunID, route.Release)
			}
			return result, err
		}
		prepared, err := preparer.PrepareRun(r.Context(), runtimedriver.StartRunRequest{Envelope: envelope})
		if err != nil {
			observeRun(true)
			return nil, err
		}
		_ = recordAgentRouteResolved(r, appCore, caller.TenantID, envelope.TraceID, envelope.Target.AgentID, prepared.Run.RunID, route)
		result, err := preparer.ExecutePreparedRun(r.Context(), prepared)
		observeRun(err != nil)
		if err == nil {
			if route.Canary {
				_ = recordCanaryRoute(r, appCore, caller.TenantID, envelope.TraceID, caller, envelope.Target.AgentID, result.RunID, route.Release)
			}
		}
		return result, err
	case "task.start":
		return taskStart(r, appCore, envelope, caller)
	case "task.command":
		payload := envelope.Payload
		taskID, _ := payload["task_id"].(string)
		command, _ := payload["command"].(string)
		if taskID == "" || command == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "task.command requires task_id and command", nil)
		}
		if isPlanCommand(contracts.TaskCommand(command)) {
			return planCommand(r, appCore, contracts.TaskID(taskID), contracts.TaskCommand(command), caller, payload)
		}
		taskCommand := contracts.TaskCommand(command)
		task, err := appCore.TaskRepo.Get(r.Context(), contracts.TaskID(taskID))
		if err != nil {
			return nil, err
		}
		if !sameTenant(task.TenantID, caller.TenantID) {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match caller tenant", nil)
		}
		if taskCommand == contracts.CmdApproveAction {
			return approveTaskAction(r, appCore, task, caller, payload)
		}
		if taskCommand == contracts.CmdUpgradeAgentVersion {
			return upgradeTaskAgentVersion(r, appCore, task, caller, payload)
		}
		if isHandoffCommand(taskCommand) {
			return handoffCommand(r, appCore, envelope, taskCommand, caller, payload)
		}
		task, event, transition, err := appCore.TaskRuntime.ApplyCommand(r.Context(), taskcommandInput(taskID, command, caller, payload))
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": task, "event": event, "transition": transition}, nil
	case "tools.invoke":
		return externalToolInvoke(r, appCore, envelope, caller)
	case "artifact.read":
		return artifactRead(r, appCore, envelope, caller)
	case "artifact.delete":
		return artifactDelete(r, appCore, envelope, caller)
	case "origin.agent.delegate":
		if appCore.Config.DisableHandoff {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "handoff is disabled by release switch", nil)
		}
		return delegateAgent(r, appCore, envelope, caller)
	case "tool.provider.upsert":
		return toolProviderUpsert(r, appCore, envelope, caller)
	case "tool.provider.sync":
		return toolProviderSync(r, appCore, envelope, caller)
	case "tool.group.upsert":
		return toolGroupUpsert(r, appCore, envelope, caller)
	case "tool.group.list":
		return toolGroupList(r, appCore, envelope, caller)
	case "tool.manifest.upsert":
		return toolManifestUpsert(r, appCore, envelope, caller)
	case "tool.manifest.list":
		return toolManifestList(r, appCore, envelope, caller)
	case "runtime_hook.provider.upsert":
		return runtimeHookProviderUpsert(r, appCore, envelope, caller)
	case "runtime_hook.provider.list":
		return runtimeHookProviderList(r, appCore, envelope, caller)
	case "runtime_hook.binding.upsert":
		return runtimeHookBindingUpsert(r, appCore, envelope, caller)
	case "runtime_hook.binding.list":
		return runtimeHookBindingList(r, appCore, envelope, caller)
	case "runtime_hook.preview":
		return runtimeHookPreview(r, appCore, envelope, caller)
	case "prompt.preview":
		return promptPreview(r, appCore, envelope, caller)
	case "agent.plugin.sync":
		return agentPluginSync(r, appCore, envelope, caller)
	case "agent.package.draft.create":
		return packageDraftCreate(r, appCore, envelope, caller)
	case "agent.package.draft.patch_strategies":
		return packageDraftPatchStrategies(r, appCore, envelope, caller)
	case "agent.package.collaborator.replace":
		return packageCollaboratorReplace(r, appCore, envelope, caller)
	case "agent.package.collaborator.add", "agent.package.collaborator.update":
		return packageCollaboratorUpsert(r, appCore, envelope, caller)
	case "agent.package.collaborator.remove":
		return packageCollaboratorRemove(r, appCore, envelope, caller)
	case "agent.package.exported_tool.replace":
		return packageExportedToolReplace(r, appCore, envelope, caller)
	case "agent.package.exported_tool.add", "agent.package.exported_tool.update":
		return packageExportedToolUpsert(r, appCore, envelope, caller)
	case "agent.package.exported_tool.remove":
		return packageExportedToolRemove(r, appCore, envelope, caller)
	case "agent.package.skill.add", "agent.package.skill.update":
		return packageSkillUpsert(r, appCore, envelope, caller)
	case "agent.package.skill.remove":
		return packageSkillRemove(r, appCore, envelope, caller)
	case "agent.package.proposal.create":
		return packageProposalCreate(r, appCore, envelope, caller)
	case "agent.package.proposal.submit":
		return packageProposalSubmit(r, appCore, envelope, caller)
	case "agent.package.proposal.approve":
		return packageProposalApprove(r, appCore, envelope, caller)
	case "agent.package.proposal.reject":
		return packageProposalReject(r, appCore, envelope, caller)
	case "agent.package.proposal.publish":
		return packageProposalPublish(r, appCore, envelope, caller)
	case "agent.package.draft.validate":
		return packageDraftValidate(r, appCore, envelope, caller)
	case "agent.package.review":
		return packageReview(r, appCore, envelope, caller)
	case "agent.package.publish":
		return packagePublish(r, appCore, envelope, caller)
	case "agent.package.canary":
		return packageReleaseAction(r, appCore, envelope, caller, "canary")
	case "agent.package.stable":
		return packageReleaseAction(r, appCore, envelope, caller, "stable")
	case "agent.package.rollback":
		return packageReleaseAction(r, appCore, envelope, caller, "rollback")
	case "policy.draft.create":
		return policyDraftCreate(r, appCore, envelope, caller)
	case "policy.update":
		return policyDraftUpdate(r, appCore, envelope, caller)
	case "policy.draft.validate":
		return policyDraftValidate(r, appCore, envelope, caller)
	case "policy.review":
		return policyReview(r, appCore, envelope, caller)
	case "policy.publish":
		return policyPublish(r, appCore, envelope, caller)
	case "policy.canary":
		return policyReleaseAction(r, appCore, envelope, caller, "canary")
	case "policy.stable":
		return policyReleaseAction(r, appCore, envelope, caller, "stable")
	case "policy.rollback":
		return policyReleaseAction(r, appCore, envelope, caller, "rollback")
	case "permission.policy.upsert":
		return permissionPolicyUpsert(r, appCore, envelope, caller)
	case "approval.approve":
		return approvalResolve(r, appCore, envelope, caller, true)
	case "approval.reject":
		return approvalResolve(r, appCore, envelope, caller, false)
	case "eval.suite.create":
		return evalSuiteCreate(r, appCore, envelope, caller)
	case "eval.suite.add_case":
		return evalSuiteAddCase(r, appCore, envelope, caller)
	case "eval.suite.run":
		return evalSuiteRun(r, appCore, envelope, caller)
	case "eval.run":
		return evalRun(r, appCore, envelope, caller)
	case "external.delivery.replay":
		return externalDeliveryReplay(r, appCore, envelope, caller)
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported command", map[string]any{"command": envelope.Command})
	}
}

func allowedCommand(command string, caller auth.CallerIdentity) bool {
	switch command {
	case "agent.run", "task.start", "task.command", "tools.invoke", "artifact.read", "origin.agent.delegate":
		return caller.HasRole(auth.RoleRuntimeCaller) || caller.HasRole(auth.RoleAdmin)
	case "prompt.preview", "agent.plugin.sync", "agent.package.draft.create", "agent.package.draft.patch_strategies", "agent.package.collaborator.add", "agent.package.collaborator.update", "agent.package.collaborator.replace", "agent.package.collaborator.remove", "agent.package.exported_tool.add", "agent.package.exported_tool.update", "agent.package.exported_tool.replace", "agent.package.exported_tool.remove", "agent.package.skill.add", "agent.package.skill.update", "agent.package.skill.remove", "agent.package.proposal.create", "agent.package.proposal.submit", "agent.package.proposal.approve", "agent.package.proposal.reject", "agent.package.proposal.publish", "agent.package.draft.validate", "agent.package.review", "agent.package.publish", "agent.package.canary", "agent.package.stable", "agent.package.rollback", "permission.policy.upsert", "tool.provider.upsert", "tool.provider.sync", "tool.group.upsert", "tool.group.list", "tool.manifest.upsert", "tool.manifest.list", "runtime_hook.provider.upsert", "runtime_hook.provider.list", "runtime_hook.binding.upsert", "runtime_hook.binding.list", "runtime_hook.preview", "policy.draft.create", "policy.update", "policy.draft.validate", "policy.review", "policy.publish", "policy.canary", "policy.stable", "policy.rollback", "approval.approve", "approval.reject", "eval.suite.create", "eval.suite.add_case", "eval.suite.run", "eval.run", "external.delivery.replay", "artifact.delete":
		return caller.HasRole(auth.RoleOptimizer) || caller.HasRole(auth.RoleAdmin)
	default:
		return true
	}
}
