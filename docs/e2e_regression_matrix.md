# CleanCore E2E Regression Matrix

日期：2026-05-30

| 场景 | 覆盖点 | 自动化入口 |
|---|---|---|
| agent.run reply | envelope -> task -> run -> model -> decision -> task complete -> trace | `internal/server.TestCommandAgentRunAndQueries` |
| tools.invoke idempotency | exposed tool、ToolPolicy、ToolResult、幂等复用、tool trace | `internal/server.TestToolsInvokeRequiresExposedToolAndIsIdempotent` |
| task.start | 外部入口创建 CoreTask、tenant 绑定 | `internal/server.TestTaskStartCommandCreatesTask` |
| task plan | create_plan、plan snapshot、PlanStep 事实 | `internal/server.TestTaskCommandCreatePlanAndQuerySnapshot` |
| package release | publish、canary、eval gate、stable、rollback、默认版本切换 | `internal/server.TestPackageReleaseCommandsAndEvalRun` |
| handoff | delegate、HandoffContextPackage、ChildTask、Target AgentRun、result backflow | `internal/server.TestOriginAgentDelegateCommand` |
| handoff decision tool | model decision tool_call -> ToolRuntime -> origin.agent.delegate -> Handoff -> result backflow | `internal/server.TestOriginAgentDelegateDecisionToolCall` |
| task upgrade | task.command(upgrade_agent_version) -> Task agent version/policy update -> next AgentRun uses upgraded version | `internal/server.TestTaskCommandUpgradeAgentVersion` |
| governance replay/metrics | trace -> PromptBundle hash / Policy version evidence -> replay/metrics snapshot | `internal/governance/replay`, `internal/governance/metrics` |
| release switches | disabled agent/tool/handoff/external invoke | `internal/server.TestReleaseSwitchesDenyCommands` |
| readiness/go-no-go | readiness report、migration object check、release report | `internal/server.TestCommandAgentRunAndQueries` |
| auth role | tenant header、role deny | `internal/server.TestCommandRequiresTenant`, `internal/server.TestCommandRoleDenied` |
| runtime loop | tool loop、max tool calls、model retry、tool failure threshold | `internal/runtime/kernel` tests |

新增场景进入主干前必须补充本矩阵，并在对应包内增加自动化测试或记录无法自动化的原因。

## Online Kernel Capability Gates

| Gate | Coverage | Automated evidence |
|---|---|---|
| K-01 policy version pinning | run snapshot records `policy_version_id`; old run continues using pinned policy after a newer stable policy is published | `internal/runtime/kernel.TestCoordinatorPinsPolicyVersionAcrossRunSteps` |
| K-02 strategy draft editing | `agent.package.draft.patch_strategies`; strategy changes are reflected in prompt preview and compiled AgentDefinition | `internal/server.TestPackageDraftPatchStrategiesAffectsPromptPreview` |
| K-02b context strategy e2e | draft create/patch strategies drive prompt preview effective context, assembly report, and compression report | `scripts/e2e_context_strategy.ps1` |
| K-02c prompt preview hook effects | `prompt.preview` reports runtime hook effects through `hook_effects` without exposing hook-injected plaintext | `internal/runtime/kernel.TestRuntimeHooksMutatePromptPreview` |
| K-03 management approval flow | release action returns `approval_required`; forged `approved:true` cannot bypass; concrete `approval_id` must match tenant/resource/action | `internal/server.TestPolicyStableRequiresConcreteApprovalRequest` |
| K-04 canary routing/hits | default `agent.run` can route to canary release by canary percent and records `canary.routed` trace with `strategy_hash` and `manifest_hash` release evidence | `internal/server.TestPackageCanaryRoutesDefaultTrafficAndRecordsHit` |
| K-05 eval result evidence | eval result captures final reply, tool calls/results, artifact refs and emits `eval.case.completed`, `eval.case.failed`, and `eval.summary.created` trace | `internal/eval.TestRunnerPassesFinalReplyContains`, `internal/eval.TestRunnerToolAssertions`, `internal/eval.TestRunnerRecordsFailedCaseTrace`, `internal/server.TestEvalSuiteRunRecordsSummaryTrace` |
| K-05b eval strategy assertions | `strategy_assertions` can verify resolved strategy/context/compression evidence from trace, including `compression_mode` | `internal/eval.TestRunnerSupportsStrategyAssertions`, `internal/eval.TestRunnerSupportsCompressionModeStrategyAssertions` |
| K-05c compression model routing | `ContextCompressionStrategy.model_base_url/model_provider/model_name` reaches the compression model request and OpenAI-compatible client can use request-level base URL override | `internal/context/compressor.TestLocalCompressorIncludesPreserveAndForbidInModelPrompt`, `internal/model/client.TestOpenAICompatibleClientUsesRequestBaseURL` |
| K-05d run diagnostics strategy limits | run diagnostics classifies strategy limit failures with `strategy_limit_reason` | `internal/server.TestDiagnosticsSummaryClassifiesStrategyLimitFailures` |
| K-06 credential/data boundary | execution profile supports `credential_scope` and data boundary; local domain rejects widened boundary; ToolRuntime resolves credential handles before dispatch, rejects missing/out-of-boundary credentials, validates caller tenant boundary, and traces credential usage | `internal/execution/domain.TestLocalDomainRejectsCredentialScopeAndDataBoundary`, `internal/tool/runtime.TestInvokeUsesExecutionDomainProfileAndTracesMetadata`, `internal/tool/runtime.TestInvokeRejectsCredentialScopeWithoutResolver`, `internal/tool/runtime.TestInvokeRejectsCredentialOutsideTenantBoundary`, `internal/tool/runtime.TestInvokeRejectsDataBoundaryOutsideCallerTenant` |

## Clean Core Final Alignment Gates

| Gate | Coverage | Automated evidence |
|---|---|---|
| C-01 model streaming | model-runtime exposes stable `ModelStreamEvent`; runtime records `model.delta` and `decision.completed` trace events | `internal/model/client.TestStubModelClientStream`, `internal/runtime/kernel.TestCoordinatorReplyRun` |
| C-02 package proposals | `agent.package.proposal.*` supports create/submit/approve/reject/publish lifecycle and cannot publish before approval | `internal/agentdef/package.TestProposalLifecyclePublishesApprovedDraft`, `internal/server.TestPackageProposalCommandsExposeReviewFlow` |
| C-03 OpenAPI contract | command enum and schemas include P7/P8 commands, canary scope, approval resolve, execution credential/data boundary, model stream event, `RunDiagnosticsResponse`, `AgentPluginManifest`, strict strategy schemas with `additionalProperties=false`, and `hook_effects` | `docs/openapi.clean-core.v1.json`, `ModelStreamEvent` |
