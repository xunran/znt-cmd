# 原智能体 CleanCore 十三模块架构与代码审查报告 v0.1

日期：2026-05-30

## 1. 审查口径

本报告按《原智能体 CleanCore 全景开发设计文档 v1.2 Clean》定义的 13 个核心模块逐一审查：

```text
0. core-contracts
1. agent-definition
2. runtime-kernel
3. task-runtime
4. context-engine
5. capability-discovery
6. decision-engine
7. model-runtime
8. policy-engine
9. tool-runtime
10. execution-domain
11. memory-artifact
12. governance
```

每个模块均从以下角度审查：

- 是否遵循设计定位和模块边界。
- 当前代码实现是否覆盖文档职责。
- 功能逻辑是否正确。
- 代码是否干净、可维护、可继续演进。
- 测试覆盖是否足够支撑模块交付。

验证命令：

```powershell
.\.tools\go1.26.3\go\bin\gofmt.exe -l (rg --files -g '*.go')
.\.tools\go1.26.3\go\bin\go.exe vet ./...
.\.tools\go1.26.3\go\bin\go.exe test ./...
```

验证结果：

- `gofmt -l` 无输出，Go 代码格式干净。
- `go vet ./...` 通过。
- `go test ./...` 通过。

结论先行：

- 当前代码目录和模块命名整体贴合 13 模块设计。
- 当前实现更接近 Alpha 骨架，很多模块已有接口、内存实现和单元测试，但距离文档定义的生产级 CleanCore 闭环仍有明显差距。
- 主要问题不是“代码完全混乱”，而是“模块骨架正确，但关键边界和生产语义没有补齐”。
- 最需要优先处理的是租户隔离、持久化、强制 Trace/Audit、真实模型、审批恢复、AgentPackage 编译、Handoff 闭环和接口契约冻结。

## 2. 总览评分

评分说明：5 = 基本符合文档目标；3 = Alpha 可用但缺核心能力；1 = 只有占位或严重偏离。

| 模块 | 文档符合度 | 逻辑正确性 | 代码洁净度 | 当前判断 |
|---|---:|---:|---:|---|
| 0 core-contracts | 4 | 3 | 4 | 契约集中较好，但关键租户字段和 ExternalTaskBinding 缺失 |
| 1 agent-definition | 2 | 2 | 3 | 静态加载和发布状态机有骨架，真实编译器缺失 |
| 2 runtime-kernel | 3 | 2 | 2 | 主循环可跑，但 Trace、租户、审批恢复和错误收敛不足 |
| 3 task-runtime | 3 | 2 | 3 | 状态机和事件有基础，事务/审计/append-only 有缺口 |
| 4 context-engine | 3 | 2 | 4 | WorkView/PromptBundle 清晰，但压缩、注入防护和 Handoff 语义不足 |
| 5 capability-discovery | 2 | 3 | 3 | 静态候选召回可用，真实索引/策略过滤/目标发现缺失 |
| 6 decision-engine | 3 | 2 | 4 | JSON 解析和候选校验清晰，但 no_op/name-only tool 存在逻辑 bug |
| 7 model-runtime | 1 | 2 | 4 | Stub 清楚，真实模型客户端未实现 |
| 8 policy-engine | 2 | 2 | 3 | Tool/Handoff 局部策略可用，统一 PolicyEngine 缺失 |
| 9 tool-runtime | 3 | 3 | 4 | 执行链路较清楚，但租户、参数校验、Trace/Audit 不完整 |
| 10 execution-domain | 1 | 2 | 4 | 只有 local domain，未达到执行域设计 |
| 11 memory-artifact | 2 | 2 | 4 | Artifact 元数据可存，Memory 和权限/审计缺失 |
| 12 governance | 2 | 2 | 4 | Trace/Audit 内存可查，但强制事件矩阵、持久化、Replay/Metrics 缺失 |

## 3. 模块 0：core-contracts

对应代码：

- `internal/contracts`

### 3.1 文档定位

文档要求 core-contracts 提供稳定、小型、无业务逻辑的核心契约，包括 AgentEnvelope、RuntimeContext、Task、AgentRun、Decision、ToolCall、ToolResult、PolicySet、TraceEvent、AuditEvent、AgentHandoff、HandoffContextPackage、CollaborationContext、ExternalTaskBinding 等。

它不应该包含 AgentLoader、Task 状态机、Tool 执行、Policy 判断、Model 调用、Trace 存储等实现逻辑。

### 3.2 当前实现

当前 `internal/contracts` 已集中定义：

- ID 类型：`TenantID`、`TaskID`、`AgentRunID`、`ToolCallID`、`HandoffID` 等。
- 枚举：`DecisionType`、`TaskStatus`、`RunStatus`、`ToolResultStatus`、`RiskLevel`、`HandoffMode`、`ReleaseStatus` 等。
- 主结构：`AgentEnvelope`、`RuntimeContext`、`AgentDefinition`、`Task`、`TaskEvent`、`AgentRun`、`VersionSnapshot`、`Decision`、`ToolDefinition`、`ToolCall`、`ToolResult`、`PolicySet`、`TraceEvent`、`AuditEvent`、`AgentHandoff`、`HandoffContextPackage`、`Artifact`、`MemoryEvent` 等。
- 错误结构：`RuntimeError`、`ErrorCode`、API error response、Trace payload 转换。

整体看，契约集中在 `contracts` 包，没有明显把状态机、执行器、策略判断等实现逻辑塞进来，模块边界总体正确。

### 3.3 符合设计的部分

- 契约层相对稳定，业务模块基本都依赖 `contracts`，方向正确。
- 枚举有基础 `Validate` 方法，避免字符串完全失控。
- `RuntimeError` 同时承载 retryable/repairable 特征，符合文档中的统一错误口径。
- `VersionSnapshot` 已预留 Agent、Package、Policy、Tool、Skill、Model、Prompt hash 等字段。
- `CollaborationContext` 和 `CollaborationProvider` 相关接口已有基础定义。

### 3.4 逻辑问题

1. `AgentHandoff` 缺少 `TenantID`。

   migration 中 `agent_handoffs` 表包含 `tenant_id`，但契约结构没有该字段。这样 API 返回、内存存储、审计和租户校验无法直接基于 handoff 对象完成。

2. `TraceEvent` 缺少 `TenantID`。

   当前 trace 查询以 `trace_id` 直接取事件，若没有 tenant 字段或关联校验，HTTP 查询层很难做严格租户隔离。

3. `ToolCall` 缺少 `TenantID`。

   migration 的 `tool_calls` 有 `tenant_id`，tool repository 的 `GetResultByIdempotencyKey(ctx, tenantID, key)` 也带 tenant 参数，但契约和内存实现没有真正保存 tenant。契约、数据库和 repository 语义不一致。

4. `ExternalTaskBinding` 没有真正建模。

   文档强调外部任务和 CoreTask 必须通过 ExternalTaskBinding 映射。当前只有 `ExternalTaskRef`、`ExternalTaskSummary` 和 `CollaborationProvider`，缺少绑定结构与状态。

5. `DecisionTypeNoOp` 有枚举，但运行语义不完整。

   validator 允许 no_op 类型通过，但 runtime-kernel 没有显式处理 no_op，会落入 default 分支，导致 run 结束但 task 未必正确完成。

6. `PolicyDecision.Decision` 是自由字符串。

   当前使用 `"allowed"`、`"denied"`、`"approval_required"` 等字符串，缺少枚举约束。策略是安全边界，建议冻结为强类型。

### 3.5 代码洁净度

代码总体干净，结构定义直观，没有过度实现。主要问题是契约字段与后续模块已经出现不一致，尤其是 tenant 维度、ExternalTaskBinding 和 no_op 语义。

### 3.6 建议

- 补齐 `TenantID`：至少覆盖 `AgentHandoff`、`TraceEvent`、`ToolCall`，并同步 repository、API 和 migration。
- 增加 `ExternalTaskBinding` 契约。
- 将 `PolicyDecision.Decision` 提升为枚举。
- 明确 `DecisionTypeNoOp` 的语义：要么实现完整状态转换，要么从冻结接口中移除。

## 4. 模块 1：agent-definition

对应代码：

- `internal/agentdef/loader`
- `internal/agentdef/package`
- `internal/server/server.go` 中 `compilePackageDefinition`

### 4.1 文档定位

文档要求 agent-definition 负责智能体定义、AgentPackage 解析、编译、版本发布、加载、Draft/Proposal/Publish、Eval Suite、Canary、Stable 和 Rollback。

它应该解析：

```text
AGENTS.md
agent.yaml
SKILL.md
skill.yaml
tool-bindings.yaml
memory-policy.yaml
permission-policy.yaml
evals.yaml
release.yaml
metadata.yaml
```

最终输出可运行的 Validated AgentDefinition。

### 4.2 当前实现

当前包含两块：

- `StaticLoader`：内存加载 `AgentDefinition`，支持默认版本。
- `agentpackage.Service`：内存 Draft、Validate、Publish、Canary、Stable、Rollback、EvalResult 状态管理。

HTTP 层 `packagePublish` 会：

1. 从 payload 取 `agent_id`、`version`、`prompt`、`agents_md`、`tool_bindings`。
2. 创建 draft。
3. 调用 `ValidateDraft`。
4. 调用 `PublishDraft`。
5. 在 server 层调用 `compilePackageDefinition` 生成 `AgentDefinition` 并塞回 `StaticLoader`。

### 4.3 符合设计的部分

- Draft/Validate/Publish/Canary/Stable/Rollback 的最小状态机已经存在。
- Stable 前需要 `MarkEvalResult(... passed=true ...)`，这点符合 Eval Gate 的方向。
- 发布和回滚会写部分 AuditEvent。
- `StaticLoader` 的接口简单，便于未来替换为持久化 loader。

### 4.4 逻辑问题

1. 没有真实 AgentPackage 编译器。

   `compilePackageDefinition` 位于 `server.go`，并且基于 `agentloader.TestAgentDefinition()` 修改字段。它没有解析 `AGENTS.md`、`agent.yaml`、`SKILL.md`、`skill.yaml`，也没有输出 `SkillDefinition`、`ToolBinding` 校验结果、Policy 引用校验、EvalSpec 等。

2. agent-definition 职责泄漏到 server 层。

   编译 AgentDefinition 是 agent-definition 模块的核心职责，却由 `internal/server/server.go` 完成。server 变成业务编译器，会使接口层和定义层耦合。

3. `StaticLoader.Load` 忽略 tenant。

   方法签名接收 `tenantID`，但实现完全不用。默认版本也是全局按 agentID 存储，不按 tenant 隔离。

4. package 发布状态没有持久化。

   `agentpackage.Service` 全部使用内存 map。服务重启后 draft、release、eval 结果、stable/canary 状态全部丢失。

5. ValidateDraft 只是改状态。

   当前没有 schema 校验、文件路径错误、工具绑定合法性、权限策略引用、Memory 策略、Eval 配置检查。

6. 审计 tenant 不完整。

   `MarkEvalResult`、`markRelease`、`Rollback` 中部分 auditEvent 传入空 tenantID，后续按租户审计会查不到或混入全局数据。

7. `PublishDraft` 不要求 eval 通过。

   当前是 publish 后再 canary/stable。文档允许发布流程分阶段，但需要明确 published 是否代表可运行。如果 published 会被 loader 使用，就必须有更强门禁。

### 4.5 代码洁净度

`loader` 和 `package` 包内部代码可读性较好，锁使用简单。但 agent-definition 的核心编译逻辑放到了 server 层，这是明显架构味道。`agentpackage.Service` 当前既是 service 又是 repository，后续上 Postgres 时需要拆分。

### 4.6 建议

- 新增 `Compiler`：输入 `AgentPackageSource`，输出 `CompiledAgentPackage` 与 `AgentDefinition`。
- 将 `compilePackageDefinition` 从 server 移到 agent-definition 模块。
- Loader 和 PackageService 按 tenant 维度隔离。
- Draft/Release/EvalResult 持久化。
- AuditEvent 必须携带 tenant、trace、actor 和 packageVersionID。

## 5. 模块 2：runtime-kernel

对应代码：

- `internal/runtime/kernel`
- `internal/runtime/run`

### 5.1 文档定位

runtime-kernel 是运行主循环，只做编排，不做具体能力。它应该：

```text
接收 AgentEnvelope
加载 AgentDefinition
创建/恢复 AgentRun
构建 WorkView
生成 PromptBundle
调用 ModelRuntime
解析并验证 Decision
分发到 ToolRuntime / TaskRuntime / Handoff
记录 Trace
处理 waiting_input / waiting_approval / repair / resume
```

### 5.2 当前实现

`Coordinator.HandleEnvelope` 已实现基础主链路：

1. 校验 command 必须是 `agent.run`。
2. 加载 agent。
3. 创建 task。
4. 创建 run。
5. 推进 task 到 accepted/planning/running。
6. 进入 loop。
7. 每步构建 candidate set、WorkView、PromptBundle。
8. 调用模型。
9. parse/validate decision。
10. reply/ask/tool_call 分发。
11. tool_call 支持多轮继续直到 reply。

`run.InMemoryRepository` 支持 run 生命周期、step/tool call 计数、waiting 状态和失败状态。

### 5.3 符合设计的部分

- 主循环方向正确，确实是由 runtime-kernel 编排其他模块。
- 已支持多轮 tool loop。
- 已支持 MaxSteps、MaxToolCalls、MaxDuration、MaxPromptTokens、MaxModelRetries、MaxConsecutiveToolFailures。
- 已对接 WorkView、PromptBundle、Decision parser/validator、ToolRuntime、TaskRuntime、TaskPlan。
- 单测覆盖 reply、tool loop、max tool calls、model retry、consecutive tool failures。

### 5.4 逻辑问题

1. `agent.run` 总是创建新 task。

   即使 `envelope.Context.TaskID` 已存在，当前也不会加载已有 task 或恢复 run，而是新建一个 task。这与长任务恢复、外部任务绑定、task.command 驱动的设计不完全一致。

2. 关键 Trace 缺失。

   当前记录了 `run.created`、`workview.built`、`promptbundle.built` 和工具完成/失败。缺少：

   ```text
   input.received
   agent.loaded
   capability.retrieved
   model.called
   model.completed
   decision.created
   decision.validated
   response.sent
   ```

3. 内部工具调用丢失 tenant 和 trace。

   `dispatchToolCalls` 调用 ToolRuntime 时传入：

   ```go
   TenantID: "",
   TraceID:  "",
   ```

   这会导致 ToolPolicy audit 和 ToolRuntime trace 缺失租户和链路 ID。

4. VersionSnapshot 硬编码模型信息。

   创建 run 时写入 `ModelProvider: "stub"`、`ModelName: "stub-decision"`，没有根据真实模型响应或配置更新，也没有写入 package version、prompt hash、tool version、skill version 的真实快照。

5. ToolCall 的 `PlanStepID` 可能被写成 runtime step ID。

   当前先执行：

   ```go
   call.PlanStepID = stepID
   if ok {
       call.PlanStepID = currentPlanStep.StepID
   }
   ```

   如果没有 active plan，`plan_step_id` 会被填入类似 runtime step 的 ID。这会污染 ToolCall 语义，`plan_step_id` 应只指向 PlanStep。

6. 模型失败只标记 run failed，不稳定标记 task failed。

   `HandleEnvelope` 捕获 loop 错误后会 `Runs.MarkFailed`，但 task 可能仍停留在 running/waiting 状态。

7. waiting_approval 不能恢复原工具调用。

   ToolRuntime 可以返回 pending approval，TaskRuntime 可以 approve_action，但 runtime-kernel 没有持久化 pending action，也没有审批通过后从原 ToolCall 继续执行的机制。

8. `DecisionTypeNoOp` 未显式处理。

   no_op 会落到 dispatch default，run completed，但 task 没有对应完成/等待状态，语义不完整。

9. repair loop 缺失。

   文档要求 ToolRepairPolicy、Decision repair、模型错误分类等能力。当前只重试模型错误，没有 repair prompt 或结构化修复。

### 5.5 代码洁净度

`Coordinator` 单文件超过 20KB，已经承担 task 创建、run 创建、主循环、工具分发、计划推进、错误归一、tool summary 等多项职责。它仍然可读，但后续继续加审批恢复、handoff、repair、streaming 会变得过重。

### 5.6 建议

- 将 `RunLifecycle`、`DecisionLoop`、`ToolDispatch`、`PlanProgress` 拆出内部小组件。
- 内部工具调用必须传入 tenant/trace。
- 修复 `PlanStepID` 语义污染。
- 增加 mandatory trace event。
- 模型失败、工具失败、no_op、waiting_approval 都要同步更新 task 状态。
- 实现 pending action 持久化和 approval resume。

## 6. 模块 3：task-runtime

对应代码：

- `internal/task/runtime`
- `internal/task/state`
- `internal/task/repository`
- `internal/task/plan`
- `internal/task/recovery`
- `internal/task/handoff`

### 6.1 文档定位

task-runtime 是长任务事实源，负责 Task、TaskEvent、TaskPlan、PlanStep、AgentHandoff、父子任务和恢复。它应该坚持事件追加原则，不把 TaskEvent、ToolResult、AuditEvent 等历史事实原地改写。

### 6.2 当前实现

当前已有：

- Task 状态机。
- Task 创建与 command 应用。
- TaskEvent append。
- 内存 TaskRepository/EventRepository。
- TaskPlan/PlanStep/PlanEvent service。
- Recovery checker。
- Handoff service，创建 context package 和 child task。

### 6.3 符合设计的部分

- Task 状态转换集中在 `state.Apply`，比散落 if 更干净。
- Task 更新使用 optimistic version，有并发冲突意识。
- TaskEvent 会记录 from/to status、command、runID、stepID。
- Plan service 已支持 create/replan/current step/complete/fail/snapshot。
- Recovery checker 可基于事件粗略检查 Task 状态一致性。

### 6.4 逻辑问题

1. `ApplyCommand` 没有 tenant guard。

   TaskRuntime 只按 taskID 取任务，不校验调用者租户。server 的 planCommand 有校验，但普通 task.command 没有。这个问题属于 P0 安全边界。

2. Task 状态更新和事件追加不是事务。

   `ApplyCommand` 先更新 task，再 append event。如果 event append 失败，会产生 task 状态变化但事实事件缺失，违反“事件记录事实”的设计。Postgres 实现必须放入同一事务。

3. `Transition.Audit` 没有被使用。

   状态机标记了 pause/resume/approval/cancel 等需要 audit 的转换，但 TaskRuntime 没有写 AuditEvent。

4. Plan 的 `CreatedBy` 写错。

   `CreatePlan` 中 `CreatedBy: actorType`，应更可能是 `actorID`。当前会把 `"user"`、`"agent"` 这类类型写成创建者。

5. Replan 违反 append-only。

   `Replan` 先创建新 plan 和 `plan.created` event，然后把 event 改成 `plan.replanned` 并 `ReplaceEvent`。这不是纯追加历史，和文档“事件追加原则”冲突。

6. PlanStep 状态约束不够。

   `CompleteStep` 可以直接完成 pending step；`StartStep` 没有限制上一个步骤必须完成；`FailStep` 也没有严格判断当前 step 状态。这会使计划执行顺序和状态机失真。

7. Plan completed 没有事件。

   `completePlanIfDone` 会更新 plan status 为 completed，但没有 append `plan.completed` 事件。

8. Recovery 只检查 TaskStatus。

   当前 recovery 不校验 AgentRun、ToolCall、ToolResult、PlanStep、Handoff、Audit/Trace 的一致性。

9. Handoff 子任务创建后没有目标 AgentRun。

   HandoffService 会创建 child task，并将 handoff 置为 running，但没有启动目标 agent run，也没有结果回流。

### 6.5 代码洁净度

TaskRuntime、StateMachine、Repository 分层清楚。Plan service 当前把 service 和 in-memory repository 放在一个大文件里，随着持久化实现增加，建议拆分。

HandoffService 依赖 task runtime、handoff policy 和 context builder，方向可以接受，但缺 trace/audit 注入，后续扩展会受限。

### 6.6 建议

- TaskRepository 接口增加 tenant 维度，或 TaskRuntime command input 增加 caller tenant 并统一校验。
- Task 更新和 TaskEvent append 进入事务。
- 删除 `ReplaceEvent` 模式，replan 改为纯追加事件。
- PlanStep 增加明确状态机。
- 使用 `Transition.Audit` 写 AuditEvent。
- Handoff 应纳入 task-runtime 的状态机，但 trace/audit 由 governance 强制记录。

## 7. 模块 4：context-engine

对应代码：

- `internal/context/workview`
- `internal/context/promptbundle`
- `internal/context/handoffpkg`

### 7.1 文档定位

context-engine 负责构建 WorkView、PromptBundle 和 HandoffContextPackage。它不保存事实，不做工具执行，不做模型调用。它需要处理上下文裁剪、注入防护、Handoff 上下文模式和 Skill 渐进加载。

### 7.2 当前实现

当前包含：

- `workview.Builder`：从 run/task/events/agent/candidates/tool results/plan 构建 WorkView。
- `promptbundle.Builder`：把 agent prompt、task objective、context、skill/tool candidates 渲染为 PromptBundle。
- `handoffpkg.Builder`：构建 HandoffContextPackage，计算 hash。

### 7.3 符合设计的部分

- WorkView 和 PromptBundle 分层清楚，没有把 PromptBundle 当事实源。
- PromptBundle 会生成 hash，便于未来写入 VersionSnapshot/Trace。
- PromptBundle 使用 source block 标注 system/developer/task/context 来源，有基础注入防护意识。
- HandoffContextPackage 有 hash、mode、allowed scopes 等基本字段。

### 7.4 逻辑问题

1. 注入防护过浅。

   `sourceBlock` 使用类似 XML 的标签包裹内容，但没有转义用户输入。如果用户输入包含闭合标签，可以破坏上下文边界。当前只添加了一句 constraint，不足以满足文档“注入防护基本要求”。

2. 没有 ContextCompressionPolicy。

   文档要求 context compression 和 max context items/tokens。当前 WorkView 不压缩，PromptBundle 只构建后在 kernel 里粗略估算 token。

3. SkillInstruction 不是来自真实 SKILL.md。

   当前根据 SkillCard 的 `WhenToUse` 拼接字符串，没有加载 `SkillInstruction.Content`。

4. HandoffContextPackage 没有真正体现 mode 差异。

   不管 full_context、summary_only、reference_only、hybrid，builder 基本都输出 summary + artifact refs，没有根据模式决定可见范围。

5. HandoffPolicy 的 max context tokens 没有执行。

   `HandoffContextPackage` 可能无限增长，当前没有 token 预算检查。

6. WorkView 的 memory/handoff/artifact 来源未打通。

   WorkView 支持 MemorySummaries、ArtifactRefs、HandoffContext，但 runtime-kernel 实际构建时基本没有加载这些上下文。

### 7.5 代码洁净度

三个 builder 都比较小，职责清晰，代码干净。问题主要是能力还处于简化层，未实现文档要求的压缩、注入、Skill 渐进加载和 Handoff 上下文策略。

### 7.6 建议

- `sourceBlock` 改为不可逃逸的结构化渲染，至少转义同名闭合标签。
- 引入 ContextPolicy/CompressionPolicy。
- PromptBundle 使用真实 SkillInstruction。
- HandoffPackage 根据 mode 明确 include/exclude scope。
- 将 PromptBundle hash 写入 RunSnapshot/Trace。

## 8. 模块 5：capability-discovery

对应代码：

- `internal/discovery/tool`

### 8.1 文档定位

capability-discovery 负责能力召回，包括 SkillCardIndex、ToolCardIndex、CapabilityCard、目标 Agent 发现、Skill 渐进加载、policy filtering 和 CandidateSet。

### 8.2 当前实现

当前实现为 `StaticCandidateProvider`：

- 静态默认 capabilities。
- 静态默认 skills。
- 从 tool registry 导入 ToolCard。
- 基于 objective 做简单关键词匹配排序。
- 根据 agent allowed/denied tool IDs 做基础过滤。

### 8.3 符合设计的部分

- CandidateSet 的概念已经存在，并被 WorkView 使用。
- 支持技能、工具、能力三类候选。
- 能按 objective 做基本排序。
- 能按 agent tools allowed/denied 过滤工具。

### 8.4 逻辑问题

1. 没有真实索引。

   当前没有 SkillCardIndex、ToolCardIndex，也没有按 AgentPackage 版本构建索引。

2. 没有 Policy filtering。

   ToolPolicy 的 denied/private/high risk 等结果没有在 candidate set 阶段过滤或打标。模型可能看到不应暴露的工具候选。

3. Skill version 没有生效。

   `AgentDefinition.Skills` 有 `SkillDefinitionRef{SkillID, Version}`，但 StaticCandidateProvider 只按 skillID 过滤，不校验 version。

4. 没有目标 Agent discovery。

   文档中 Handoff 需要基于 capability-discovery 发现 target agent。当前 delegate 由请求直接给 `to_agent_id`。

5. 包名 `internal/discovery/tool` 偏窄。

   该模块实际处理 capability/skill/tool，包名叫 tool 容易让边界变窄。

### 8.5 代码洁净度

实现简单，测试覆盖了基本过滤和排序。代码本身干净，但它是静态候选 provider，不是文档定义的完整 discovery 模块。

### 8.6 建议

- 重命名或新增 `internal/discovery/capability`。
- 建立 SkillCardIndex 和 ToolCardIndex。
- CandidateSet 中加入 filter reason、score、source package version。
- 接入 PolicyEngine，在进入 PromptBundle 前剔除不可见工具。
- 为 Handoff 增加 target agent discovery。

## 9. 模块 6：decision-engine

对应代码：

- `internal/decision/parser`
- `internal/decision/validator`

### 9.1 文档定位

decision-engine 负责解析和约束模型输出。它不决定工具是否允许执行，也不执行业务逻辑。它必须保证模型输出是可理解、可验证、可修复的 Decision。

### 9.2 当前实现

当前包含：

- `parser.Parse`：JSON unmarshal 到 `contracts.Decision`。
- `validator.Validate`：校验 decision type、reply text、ask question、tool call 非空、tool 是否在 candidate set、unsupported reason、error 内容等。

### 9.3 符合设计的部分

- parser 和 validator 分离。
- validator 不做 ToolPolicy 判断，只判断候选集内工具，这个边界正确。
- 对 reply、ask、tool_call、unsupported、error 做了基础结构校验。

### 9.4 逻辑问题

1. name-only tool call 会通过校验但执行失败。

   Validator 允许模型只填 `name`，只要 name 在候选工具里即可。但 runtime-kernel 后续调用 ToolRuntime 时使用 `call.ToolID` 查 registry。若 `ToolID` 为空、`Name` 为 `"echo"`，校验通过，执行时 `tool not found`。

2. no_op 语义不完整。

   `DecisionTypeNoOp` 通过枚举校验，但 validator 没有明确分支，runtime-kernel 也没有明确处理，可能导致 run completed、task 状态不一致。

3. 没有 JSON Schema 级别校验。

   当前只靠 Go struct 和手写校验，未校验 arguments schema、reply kind、confidence 范围、required 字段组合等。

4. 没有 DecisionID 生成或要求。

   Decision 结构有 DecisionID，但 parser/validator/kernel 不生成也不记录，Trace 难以关联一次模型决策。

5. 没有 repair output。

   文档要求 repair prompt 和可修复输出。当前 schema error 直接失败。

### 9.5 代码洁净度

代码很小、可读性好，边界清楚。主要是缺少规范化步骤。建议 validator 输出 normalized decision，而不是只返回 error。

### 9.6 建议

- Validator 返回 `DecisionValidationResult{Decision, Warnings}`，并把 name-only tool call 规范化为 tool_id。
- 明确 no_op 状态语义。
- 引入 JSON Schema 校验。
- 生成/要求 DecisionID，并写入 Trace。
- 对 schema error 接入 repair loop。

## 10. 模块 7：model-runtime

对应代码：

- `internal/model/client`

### 10.1 文档定位

model-runtime 负责隔离模型供应商，提供统一 ModelClient、模型错误分类、超时、重试、fallback、模型调用 Trace。

### 10.2 当前实现

当前包含：

- `ModelClient` interface。
- `StubModelClient`：默认返回固定 reply。
- `ScriptedModelClient`：测试用脚本响应。
- `OpenAICompatibleClient`：结构存在，但 `Complete` 直接返回 skeleton error。

### 10.3 符合设计的部分

- ModelClient 抽象已经存在。
- Stub/Scripted 对测试很有用。
- ModelRequest 中包含 PromptBundle 和 Timeout。
- Runtime-kernel 已通过 interface 调用 model。

### 10.4 逻辑问题

1. 真实模型客户端未实现。

   `OpenAICompatibleClient.Complete` 当前只是 skeleton，不发 HTTP 请求。

2. 没有错误分类器。

   文档要求 ModelErrorClassifier。当前模型错误统一包装为 `MODEL_ERROR` 或 `MODEL_TIMEOUT`，没有区分限流、认证失败、上下文过长、供应商 5xx、格式错误等。

3. 没有模型调用 Trace。

   `model.called` 和 `model.completed` 常量存在，但 runtime 没有记录。

4. 没有配置装配。

   core.New 默认注入 `StubModelClient{}`，config 中也没有 model base_url/api_key/model 字段。

5. Scripted timeout 语义有限。

   ScriptedModelClient 只检查 ctx 是否已 done，不会基于 request.Timeout 创建 timeout context。作为测试客户端问题不大，但不应代表真实超时语义。

### 10.5 代码洁净度

Stub 代码干净，测试友好。问题是生产能力缺失，而不是局部代码脏。

### 10.6 建议

- 实现 OpenAI-compatible HTTP client。
- 增加 model config。
- 增加错误分类、重试退避、超时和响应大小限制。
- 模型请求/响应写 Trace，敏感内容按策略脱敏。
- core.New 根据环境禁止默认生产使用 StubModelClient。

## 11. 模块 8：policy-engine

对应代码：

- `internal/policy/toolpolicy`
- `internal/policy/handoff`
- `internal/contracts/policy.go`

### 11.1 文档定位

policy-engine 承载动态策略，包括 RuntimePolicy、ToolPolicy、ApprovalPolicy、PromptPolicy、ContextCompressionPolicy、ToolRepairPolicy、HandoffPolicy、TaskRecoveryPolicy、ReleasePolicy、CanaryPolicy 等。Policy 是不可绕过的安全边界。

### 11.2 当前实现

当前实现了两个 evaluator：

- ToolPolicy evaluator：allowed/denied、agent allowed/denied、private visibility、高风险审批、写 audit。
- Handoff evaluator：full_context 禁止、artifact read 禁止、敏感 artifact 需审批。

### 11.3 符合设计的部分

- ToolRuntime 调用工具前会经过 ToolPolicy。
- ToolPolicy 不直接执行工具，边界正确。
- ToolPolicy 会写 AuditEvent。
- Handoff 有独立 evaluator，而不是硬编码在 HandoffService 内。

### 11.4 逻辑问题

1. 没有统一 PolicyEngine。

   当前是两个分散 evaluator，没有统一 `PolicyDecision` 生命周期、policy version、decision id、audit/trace matrix。

2. 实际运行没有加载完整 PolicySet。

   runtime-kernel 内部工具调用传入 `contracts.PolicySet{PolicySetID: ...}`，大部分策略字段为空。也就是说文档中的 RuntimePolicy、PromptPolicy、CompressionPolicy、ApprovalPolicy 实际不生效。

3. HandoffPolicy 在 server 层硬编码。

   `handoffCreateInput` 使用：

   ```go
   HandoffPolicy{
       DefaultMode: contracts.HandoffHybrid,
       AllowArtifactRead: true,
   }
   ```

   没有从 AgentDefinition 或 PolicySet 加载真实策略。

4. ApprovalPolicy 未使用。

   ToolPolicy 自己判断 approval_required，TaskRuntime 有 approve/reject 状态，但没有统一审批策略和恢复机制。

5. PromptPolicy、CompressionPolicy、RecoveryPolicy 未执行。

   契约存在，但 context-engine/runtime-kernel 没有使用。

6. ReleasePolicy/CanaryPolicy 缺失。

   package stable 只要求 evalPass，没有发布窗口、审批、灰度、回滚策略。

7. Handoff policy 不写 audit。

   ToolPolicy 写 audit，HandoffPolicy 没有写 `handoff.policy_checked`。

### 11.5 代码洁净度

两个 evaluator 都很小，规则清楚。问题是策略体系尚未成型，并且实际运行传入的 PolicySet 过空，导致“看起来有策略，实际只用了局部默认规则”。

### 11.6 建议

- 新增统一 `PolicyEngine`，封装 tool/handoff/release/prompt/recovery。
- PolicySet 持久化并按 tenant/agent/package version 加载。
- 所有 PolicyDecision 写 trace + audit。
- HandoffPolicy 从 PolicySet 或 AgentPackage 读取，禁止 server 硬编码。
- 把 ApprovalPolicy 与 pending action/resume 打通。

## 12. 模块 9：tool-runtime

对应代码：

- `internal/tool/registry`
- `internal/tool/runtime`
- `internal/tool/repository`

### 12.1 文档定位

tool-runtime 负责工具注册、工具定义、工具调用、参数校验、Policy 检查、ExecutionDomain 选择、ToolResult、工具失败 repair，以及内部工具和外部 tools.invoke 边界。

### 12.2 当前实现

当前已有：

- InMemoryRegistry。
- 内置工具 `echo` 和 `artifact.create`。
- ToolRuntime.Invoke：查 registry -> ToolPolicy -> approval/deny -> execution domain -> executor -> ToolResult。
- Tool repository：ToolCall/ToolResult 内存保存和 idempotency map。
- server `tools.invoke` 外部入口，要求 tool 在 agent exposed list 内。

### 12.3 符合设计的部分

- 工具执行前经过 ToolPolicy。
- ToolRuntime 不直接暴露 HTTP，外部入口在 server 中并做 exposed tool 检查。
- ToolResult 状态包含 succeeded/failed/denied/pending_approval。
- ExecutionDomain resolver 已接入 ToolRuntime。
- artifact.create 返回 ArtifactRef，符合 Artifact 原则方向。

### 12.4 逻辑问题

1. 参数 schema 未校验。

   ToolDefinition 有 InputSchema，但 ToolRuntime 没有验证 arguments。`artifact.create` 只手写检查 content。

2. tool.policy_checked 没有 Trace。

   ToolPolicy 写 Audit，但 Trace 中没有 `tool.policy_checked`。ToolRuntime 也没有 `tool.invoked`，只记录 completed/failed。

3. denied/pending approval 缺少 trace。

   policy denied 返回 ToolResultDenied，但不记录 tool.failed/tool.denied trace；pending approval 也不记录 trace。

4. 内部工具调用上下文缺失。

   runtime-kernel 传入空 TenantID/TraceID，导致 Audit 和 Trace 质量不足。

5. idempotency 没有 tenant scope。

   `GetResultByIdempotencyKey(ctx, tenantID, key)` 接口带 tenant，但内存实现忽略 tenant。跨租户相同 idempotency key 可能冲突。

6. ArtifactCreate 未写 tenant。

   Artifact 契约有 TenantID，但 ToolCall 没有 TenantID，ArtifactCreateExecutor 创建的 Artifact TenantID 为空。

7. Registry 注册重复工具会覆盖。

   对接口冻结阶段来说，重复注册是否允许需要明确。当前无审计、无版本冲突检测。

### 12.5 代码洁净度

tool-runtime 代码相对干净、职责集中，是当前实现质量较好的模块之一。需要补的是策略上下文、schema 校验、trace/audit 完整性和持久化。

### 12.6 建议

- 使用 `pkg/jsonschema` 或标准 schema 校验 ToolCall arguments。
- ToolRuntime 记录 `tool.policy_checked`、`tool.invoked`、`tool.completed`、`tool.failed`、`tool.pending_approval`。
- ToolCall 增加 TenantID。
- 幂等键至少包含 tenant/run/task/tool/request hash。
- Registry 增加 duplicate/version policy。

## 13. 模块 10：execution-domain

对应代码：

- `internal/execution/domain`

### 13.1 文档定位

execution-domain 控制执行边界，负责根据 ToolDefinition、RuntimeProfile、Policy 选择 local/worker/sandbox/managed runtime，并处理资源限制、网络策略、托管 Agent runtime。

### 13.2 当前实现

当前包含：

- `ExecutionProfile` 结构。
- `ExecutionDomain` interface。
- `LocalExecutionDomain`。
- `Resolver`，按 profile 字符串找 domain。

ToolRuntime 默认注册 local domain。

### 13.3 符合设计的部分

- ExecutionDomain 抽象存在。
- ToolRuntime 没有直接调用 executor，而是经过 domain。
- unknown profile 会返回 `EXECUTION_DOMAIN_UNAVAILABLE`。

### 13.4 逻辑问题

1. 只有 local domain。

   worker、sandbox、managed runtime 都没有实现。

2. `ExecutionProfile` 没有真正使用。

   ToolDefinition 只有 `ExecutionProfile string`，Resolver 把它当 domain ID。文档中的 runtime profile、network policy、worker ref、resource limits 没有进入执行。

3. 没有资源限制。

   没有 CPU/memory/time/network policy，也没有 sandbox 隔离。

4. execution-domain 依赖 tool registry.Executor。

   当前 domain interface 直接接收 `registry.Executor`，使 execution-domain 与 tool registry 发生耦合。文档上 execution-domain 更像通用执行边界，建议用更中性的 execution request。

5. ManagedAgentAdapter 缺失。

   文档预留外部托管 Agent Runtime 作为执行域，目前没有。

### 13.5 代码洁净度

代码很小、清楚，但只是占位能力。作为 Alpha local stub 可以接受，作为文档目标远远不足。

### 13.6 建议

- 定义 `ExecutionRequest`/`ExecutionResult`，减少对 tool registry 的直接依赖。
- 实现 sandbox/worker profile stub，再逐步接真实 worker。
- 资源预算和网络策略进入 ExecutionProfile。
- 将 domain selection 写入 Trace 和 ToolResult metadata。

## 14. 模块 11：memory-artifact

对应代码：

- `internal/asset/artifact`
- `internal/contracts/artifact_memory.go`
- `internal/tool/registry/builtin.go` 中 `artifact.create`

### 14.1 文档定位

memory-artifact 负责 MemoryEvent、MemorySummary、Artifact、ArtifactRef、HandoffContextPackage 存储/引用。记忆写入必须经过 MemoryPolicy 和 Audit，Artifact 是跨模块传递标准引用。

### 14.2 当前实现

当前有：

- Artifact/ArtifactRef/MemoryEvent/MemorySummary 契约。
- InMemory Artifact Store。
- artifact.create 工具可创建 Artifact 元数据和 ArtifactRef。

没有 Memory store。

### 14.3 符合设计的部分

- ArtifactRef 作为跨模块引用已经存在。
- artifact.create 不直接把大内容塞进 Prompt，而返回 ArtifactRef，方向正确。
- Artifact store 接口简单，便于未来替换。

### 14.4 逻辑问题

1. Memory 基本未实现。

   有 MemoryEvent 契约，但没有 MemoryRepository、MemoryPolicy、Memory read/write、MemorySummary builder。

2. Artifact 只有元数据，没有内容读写。

   artifact.create 接收 content，但 store 只保存 metadata，`StorageURI` 是 `memory://...`，没有按 URI 读取内容的能力。

3. Artifact tenant 为空。

   Artifact 契约有 TenantID，但 artifact.create 无法从 ToolCall 获取 tenant，导致创建的 Artifact TenantID 为空。

4. Artifact store 没有 tenant guard。

   `GetArtifact(ctx, artifactID)` 只按 ID 查询，没有 tenant 校验。

5. HandoffContextPackage 没有由 memory-artifact 管理。

   文档要求 memory-artifact 保存或引用 HandoffContextPackage。当前由 HandoffService 内存 map 保存。

6. 记忆/Artifact 相关 audit 缺失。

   Audit 常量有 `memory.write`、`artifact.delete`，但没有实际 Memory 写入，也没有 Artifact 创建/读取/删除审计。

### 14.5 代码洁净度

Artifact store 代码干净，但模块覆盖面不足。当前更像 artifact metadata stub，不是完整 memory-artifact。

### 14.6 建议

- 增加 MemoryRepository 和 MemoryPolicy。
- Artifact 增加内容存储抽象和 tenant-scoped read。
- ToolCall 或 InvokeRequest 必须携带 tenant，让 ArtifactCreate 写入 TenantID。
- HandoffContextPackage 迁移到 memory-artifact 或统一 ContextPackageStore。
- Memory/Artifact 写入和敏感读取写 Audit。

## 15. 模块 12：governance

对应代码：

- `internal/governance/trace`
- `internal/governance/audit`
- `internal/readiness`
- `internal/release`
- `internal/storage/migration`
- `migrations/001_clean_core_base.sql`

### 15.1 文档定位

governance 负责 Trace、Audit、Metrics、Replay。它回答：

```text
这次运行发生了什么？
谁做了什么？
为什么允许或拒绝？
用的是哪个 AgentPackage / Policy / Model / Tool 版本？
能否回放和审计？
```

### 15.2 当前实现

当前已有：

- InMemoryTraceRecorder。
- InMemoryAuditLogger。
- Trace/Audit 契约与常量。
- `/v1/traces/{id}`、`/v1/audit`、tool trace、handoff trace 等查询入口。
- readiness report。
- release go/no-go report。
- migration 文件加载、checksum、required schema object 检查。

### 15.3 符合设计的部分

- Trace 和 Audit 分开建模。
- Audit 搜索支持 tenant/action/resource 过滤。
- ToolPolicy 会写 audit。
- AgentPackage publish/rollback 会写部分 audit。
- migration SQL 中包含核心事实表。
- readiness/go-no-go 已有接口形态。

### 15.4 逻辑问题

1. Trace 缺少 tenant 维度和租户隔离。

   TraceEvent 契约没有 TenantID，HTTP `/v1/traces/{id}` 直接按 trace_id 返回所有事件。跨租户只要猜到 trace_id 就可能读取。

2. 强制 Trace/Audit 点不完整。

   文档要求模型调用、决策创建/校验、工具策略、工具执行、Handoff 创建/接受/拒绝/完成/失败、AgentPackage 发布/回滚等关键步骤都有 Trace/Audit。当前只覆盖部分。

3. Handoff audit 缺失。

   HandoffService 只写 TaskEvent，没有写 AuditEvent，也没有写 TraceEvent。

4. Package audit tenant 不完整。

   agentpackage 的部分 release/eval/rollback audit 使用空 tenantID。

5. AuditEvent CreatedAt 不统一兜底。

   AuditLogger 只是 append，若调用方忘记设置 CreatedAt，会写入零值时间。ToolPolicy 当前就没有设置 CreatedAt。

6. 没有 Metrics。

   文档中 governance 包括 Metrics，目前没有模型失败率、工具耗时、审批等待、handoff 耗时等指标。

7. Replay 未实现。

   Task recovery 只是状态检查，不是完整 RunTrace/TaskTrace/ToolTrace replay。

8. Migration readiness 只是文本检查。

   `MissingRequiredObjects` 检查 SQL 文本是否包含对象名，没有连接真实数据库，也没有检查当前 schema version。

9. Go/No-Go 过浅。

   当前 go/no-go 只看 readiness 和 migration 文件文本，不包含 E2E、安全、租户隔离、真实 DB、真实模型、外部协作等门禁。

### 15.5 代码洁净度

Trace/Audit 内存实现很简洁。Readiness/Release 也容易理解。但 governance 的实现深度不足，且 query API 的安全边界需要优先修。

### 15.6 建议

- TraceEvent 增加 TenantID，所有 trace 查询按 tenant guard。
- AuditLogger 在 Log 时补 CreatedAt 和 audit id，或强制调用方提供。
- 制定 mandatory trace/audit matrix，并用测试断言。
- 实现 PostgresTraceRecorder/PostgresAuditLogger。
- 增加 metrics 和 replay。
- Go/No-Go 接入 E2E、migration DB status、security checks。

## 16. 跨模块架构审查

### 16.1 模块边界整体判断

当前目录基本围绕 13 模块拆分，方向是正确的：

```text
contracts        -> core-contracts
agentdef         -> agent-definition
runtime/kernel   -> runtime-kernel
task             -> task-runtime
context          -> context-engine
discovery        -> capability-discovery
decision         -> decision-engine
model            -> model-runtime
policy           -> policy-engine
tool             -> tool-runtime
execution        -> execution-domain
asset/artifact   -> memory-artifact 的 artifact 子集
governance       -> governance
```

但有三类边界问题：

1. server 层承担过多业务职责。

   `server.go` 里包含 AgentPackage 编译、HandoffPolicy 构造、tools.invoke 执行编排、release fallback 等逻辑。HTTP 层应更多做协议适配和 auth，业务逻辑应回到对应模块 service。

2. runtime-kernel 对具体实现依赖偏多。

   Coordinator 字段多为接口或轻量结构，但构造器直接默认注入 static discovery、prompt builder、validator 等具体实现。Alpha 可接受，后续应通过 app/core 统一装配接口。

3. 租户边界没有成为 repository/service 的默认接口。

   多数 Get/List 以 ID 为主，不带 tenant。HTTP 层也不是所有路径都补校验。多租户系统里，tenant guard 不能依赖调用方自觉。

### 16.2 最关键的逻辑 bug

以下问题不是“未完成”，而是当前逻辑已有错误或高风险：

1. `Decision` 只填 `name` 不填 `tool_id` 时，validator 会通过，但 ToolRuntime 查找失败。
2. `DecisionTypeNoOp` 允许进入系统，但 runtime 没有明确状态语义。
3. runtime-kernel 内部 tool invoke 传空 TenantID/TraceID。
4. ToolCall 的 `PlanStepID` 在无计划时会写入 runtime step ID。
5. 普通 `task.command` 缺少 task tenant 校验。
6. Task 状态更新和 TaskEvent append 非事务，可能事实丢失。
7. Replan 使用 ReplaceEvent，违反 append-only。
8. Artifact.create 创建 Artifact 时 TenantID 为空。
9. Trace 查询没有 tenant guard。
10. AgentPackage release/eval 部分 audit tenant 为空。

### 16.3 代码干净度总体判断

干净的部分：

- Go 格式、vet、单测均通过。
- 大多数包职责清楚，没有循环依赖。
- InMemory repository 基本都有锁保护。
- 合同层、validator、model stub、tool runtime、trace/audit 内存实现可读性好。

需要整理的部分：

- `internal/server/server.go` 太重，混入业务编译和治理逻辑。
- `internal/runtime/kernel/coordinator.go` 太重，后续会成为复杂度集中点。
- `internal/task/plan/service.go` 同时放 service 和 repository，后续建议拆。
- `agent-definition` 的 compile 逻辑不应在 server。
- tenant guard、trace/audit、policy decision 这些横切能力没有统一中间层或 helper，导致遗漏。

## 17. 建议整改顺序

### P0：先修安全和事实源

1. 给 Task/Run/Trace/ToolCall/Handoff/Artifact 相关查询和 repository 接口补 tenant guard。
2. 修复普通 `task.command` 越权风险。
3. 修复 runtime-kernel 内部工具调用 TenantID/TraceID 为空。
4. 修复 `PlanStepID` 语义污染。
5. Task 更新和 TaskEvent append 进入事务设计。
6. Replan 改成纯 append-only。
7. Trace 查询按 tenant 隔离。

### P1：补完整运行闭环

1. 实现 OpenAI-compatible ModelClient。
2. 增加 model.called/model.completed/decision.created/decision.validated trace。
3. 实现 approval pending action 和 resume。
4. 明确 no_op 和 name-only tool call 规范化。
5. Tool argument schema 校验。
6. Tool denied/pending approval trace。

### P2：补文档核心能力

1. AgentPackage Compiler。
2. PolicySet repository 和统一 PolicyEngine。
3. Capability index 和 policy filtering。
4. Handoff target discovery、target AgentRun、result backflow。
5. Memory store、Artifact content store、ContextPackageStore。
6. Postgres repository 和 migration runner。

### P3：发布和治理硬化

1. OpenAPI/Contract snapshot。
2. E2E 回归矩阵。
3. Metrics 和 structured logging。
4. Go/No-Go 门禁接入真实测试结果。
5. server 层业务逻辑下沉到模块 service。

## 18. 最终判断

当前代码架构“方向正确，但实现层级偏 Alpha”。

13 个模块大多已经有对应目录和基础类型，主运行链路也能跑通，说明工程骨架不是乱的。但文档中强调的 CleanCore 关键能力：可治理、可审计、可恢复、可版本化、可多租户隔离、可真实模型运行、可受控 Handoff、可外部协作映射，目前还没有完整落地。

如果按生产交付标准判断，当前最不应继续堆新功能，而应先把横切安全边界和事实源补牢：

```text
tenant guard
Postgres persistence
mandatory Trace/Audit
PolicyDecision lifecycle
approval resume
real ModelRuntime
AgentPackage compiler
E2E regression matrix
```

这些补齐后，现有 13 模块骨架才会真正接近开发文档定义的 CleanCore。
