# CleanCore Agent 开发者插件生命周期与接口指南 v0.1

## 1. 文档目标

本文面向 Agent 开发者、插件开发者、工具提供方和未来 Runtime Hook 提供方，回答四个问题：

```text
1. 创建一个 Agent 时，平台经历哪些生命周期？
2. Agent 运行时，每一次 run / step 发生什么？
3. 开发者可以通过哪些已暴露接口接入？
4. 未来 Runtime Hook 插件可以在哪些点影响数据，但不能控制什么？
```

本文区分两类状态：

```text
当前已实现：
  代码里已经存在，可通过现有 command / service / tool runtime 使用。

设计中：
  已在设计文档中收口，但还需要继续产品化或扩展。
```

关键代码锚点：

```text
internal/agentdef/package/service.go
internal/agentdef/package/compiler.go
internal/runtime/kernel/coordinator.go
internal/runtime/hook/service.go
internal/runtime/hook/store.go
internal/tool/registry/registry.go
internal/tool/runtime/runtime.go
internal/server/server.go
internal/app/core/core.go
```

相关设计文档：

```text
docs/Agent运行时Hook与Runtime扩展设计_v0.1.md
docs/Agent协作与Agent能力工具化整改设计_v0.1.md
docs/动态工具注册与HTTP工具改造设计_v0.1.md
```

## 2. 开发者角色

CleanCore 里常见的开发者角色有四类：

```text
Agent Package 开发者
  编写 AGENTS.md / prompt / tool_bindings / skills / metadata。
  目标是发布一个可运行 AgentDefinition。

Tool 插件开发者
  提供 ToolDefinition、input_schema、output_schema、executor 或外部 provider。
  目标是让 Agent 可以通过 ToolRuntime 安全调用能力。

Agent 协作/能力开发者
  声明 collaborator、exports.tools、tool_bindings。
  目标是让 Agent 可以委派任务，或把固定能力暴露成工具。

Runtime Hook 开发者
  提供观察 hook、数据 patch、策略 hint。
  目标是影响候选、上下文、排序、规划和记忆写入建议，但不接管主循环。
```

当前代码里 Agent Package、Tool、Agent 协作/能力工具化已经进入 MVP 可用状态；Runtime Hook 已落地为 `runtimehook.Service`，支持进程内 data hook、静态 Hook Host 调用、Agent 级 binding、Postgres/InMemory 持久化、trace/audit/runtime_hook_events。完整 RuntimeDriver 仍属于后续阶段。

## 3. Agent 创建生命周期

### 3.1 总览

当前代码中的 Agent 创建主链路是：

```text
AgentPackageSource
  -> Draft
  -> patch prompts / tool bindings / skills
  -> validate
  -> review 或 proposal approve
  -> publish
  -> AgentPackageVersion
  -> Compile 为 AgentDefinition
  -> AgentRegistry.Put()
  -> canary / eval / stable / rollback
```

不要直接修改运行中的内存 `AgentDefinition`。正式路径应该走 package draft / publish / release，这样 source hash、compiled hash、trace、audit、eval、canary/stable 才能闭环。

### 3.2 AgentPackageSource

当前已实现的源模型在 `internal/agentdef/package/service.go`：

```go
type AgentPackageSource struct {
    AgentsMD      string
    Prompt        string
    ToolBindings  contracts.AgentToolsConfig
    Collaborators []contracts.AgentCollaboratorRef
    Exports       contracts.AgentExports
    RuntimeHooks  contracts.AgentRuntimeHooks
    Metadata      map[string]any
}
```

字段语义：

```text
agents_md
  Agent 的说明、身份、边界和能力描述。

prompt
  Agent 的 identity prompt。为空时会回退到 agents_md。

tool_bindings
  当前 Agent 允许、暴露、拒绝的工具 ID。

collaborators
  当前 Agent 允许运行时委派给哪些 Agent。`origin.agent.delegate` 会强制校验目标必须在这里。

exports
  当前 Agent 对外导出的固定能力。`exports.tools` 会同步成 tenant-scoped ToolManifest，执行域为 `agent_tool`。

runtime_hooks
  当前 Agent 声明的运行时数据 Hook。Hook 只返回 patch / hints / intents，由 Kernel 统一校验和应用。

metadata
  name、description、system_prompt、developer_prompt、policy_set_id、
  runtime limits、skills / skill_definitions 等扩展字段。
```

当前 `tool_bindings` 结构：

```json
{
  "allowed_tool_ids": ["artifact.create"],
  "exposed_tool_ids": [],
  "denied_tool_ids": ["origin.agent.delegate"]
}
```

当前 runtime limit metadata 示例：

```json
{
  "name": "CRM Assistant",
  "description": "处理 CRM 客户资料和跟进记录。",
  "system_prompt": "Return decisions as JSON.",
  "developer_prompt": "只处理 CRM 范围内任务。",
  "policy_set_id": "policy_default",
  "max_steps": 4,
  "max_tool_calls": 2,
  "max_duration_seconds": 60,
  "max_prompt_tokens": 4000,
  "max_model_retries": 1,
  "max_repair_attempts": 1,
  "max_consecutive_tool_failures": 2
}
```

### 3.3 编译为 AgentDefinition

`Compile()` 会把 `AgentPackageSource` 编译成 `contracts.AgentDefinition`：

```text
AgentID / Version
Name / Description
IdentityPrompt / SystemPrompt / DeveloperPrompt
Tools
Collaborators
Exports
RuntimeHooks
SkillDefinitions
PolicyRefs
RuntimeLimits
ContractVersion
```

编译时会校验：

```text
agent_id 非空
version 非空
agents_md 或 prompt 至少一个非空
tool_bindings 中 allowed / denied 不冲突
collaborators 中 agent_id 非空且不重复
exports.tools 中 tool_id / name / description / input_schema 合法且不重复
runtime_hooks 中 phase / provider_type / failure_policy 合法
runtime limits 合法
skill risk_level 合法
skill resources 必填字段完整
```

### 3.4 Draft / Patch / Validate / Publish

当前已暴露的 package command 在 `internal/server/server.go`：

```text
agent.package.draft.create
agent.package.draft.patch_prompt
agent.package.draft.patch_developer_prompt
agent.package.draft.patch_system_prompt
agent.package.draft.patch_agents_md
agent.package.tool_binding.update
agent.package.collaborator.add / update / replace / remove
agent.package.exported_tool.add / update / replace / remove
agent.package.runtime_hooks.update
agent.package.skill.add
agent.package.skill.update
agent.package.skill.remove
agent.package.draft.validate
agent.package.review
agent.package.publish
```

典型流程：

```text
1. agent.package.draft.create
2. patch prompt / developer prompt / system prompt / AGENTS.md
3. agent.package.tool_binding.update
4. agent.package.skill.add 或 update
5. agent.package.draft.validate
6. agent.package.review
7. agent.package.publish
```

`agent.package.publish` 发布成功后，会：

```text
1. 创建 AgentPackageVersion。
2. 记录 source_hash。
3. 编译 AgentDefinition。
4. 记录 compiled_hash。
5. 保存 agent_package_versions / agent_definitions。
6. 调用 AgentRegistry.Put(compiled)。
7. 将 exports.tools 同步到 ToolCatalog / ToolRegistry。
```

### 3.5 Proposal / Eval / Canary / Stable

当前也支持 proposal 和 release 管理：

```text
agent.package.proposal.create
agent.package.proposal.submit
agent.package.proposal.approve
agent.package.proposal.reject
agent.package.proposal.publish
agent.package.canary
agent.package.stable
agent.package.rollback
eval.run
```

注意：

```text
stable 之前需要 eval 通过。
canary 可以按 percent / scope 路由默认流量。
rollback 应带 reason。
```

运行 `agent.run` 时，server 会先解析目标版本。如果命中 canary，会记录 canary hit。

### 3.6 通过 AgentFactory 创建草稿

当前已有 `origin.agent.create_draft` 工具和 `internal/agentfactory/service.go`。

它适合在群聊或协作场景里根据目标创建专业 Agent 草稿：

```text
输入：tenant_id / group_id / requested_by / objective / 可选 agent_id / name
输出：AgentDraftRequest + AgentPackage draft
状态：draft_created
```

它只创建 draft，不发布、不绕过权限、不自动 stable。

## 4. 运行时生命周期

### 4.1 AgentEnvelope

所有运行请求进入 CleanCore 时都包装为 `AgentEnvelope`：

```json
{
  "trace_id": "trace_1",
  "target": {
    "agent_id": "origin-coordinator",
    "version": "stable"
  },
  "caller": {
    "caller_id": "user_1",
    "caller_type": "user",
    "tenant_id": "tenant_1"
  },
  "command": "agent.run",
  "payload": {
    "input": "帮我整理这段客户跟进记录"
  },
  "context": {
    "tenant_id": "tenant_1",
    "user_id": "user_1",
    "conversation": {
      "provider": "web",
      "kind": "thread",
      "conversation_id": "conv_1",
      "thread_id": "thread_1",
      "current_message": {
        "message_id": "msg_1",
        "speaker_id": "user_1",
        "speaker_type": "user"
      }
    },
    "timezone": "Asia/Hong_Kong"
  }
}
```

`agent.run` 是当前主运行命令。server 会先做 caller role 检查、disabled agent 检查、版本路由，然后进入 `Coordinator.HandleEnvelope()`。

### 4.2 Run 启动

`Coordinator.HandleEnvelope()` 当前启动流程：

```text
1. 校验 command == agent.run。
2. Load AgentDefinition。
3. 记录 agent.loaded trace。
4. 创建 Task。
5. 创建 AgentRun。
6. 写入 VersionSnapshot。
7. 记录 run.created trace。
8. Task 依次进入 accepted / planning / running。
9. 记录 conversation input。
10. Run 标记 running。
11. 进入 loop。
```

VersionSnapshot 会记录：

```text
contract_version
agent_definition version
agent_package version
policy_set / policy_version
skill_definitions
tool_definitions
model_provider / model_name
prompt_bundle_hash
```

这保证了 trace / audit / replay / eval 可以知道当时用的是哪个 Agent、哪个 policy、哪些工具和技能。

### 4.3 Step 主循环

每个 step 当前代码顺序在 `Coordinator.step()`：

```text
1. Runs.IncrementStep()
2. TaskRepo.Get()
3. Tasks.Events()
4. policySetForRun()
5. maybeUpgradeByPolicy()
6. Tools.Candidates()
7. planContext()
8. toolSummaries()
9. memorySummaries()
10. artifactRefs()
11. conversationContext()
12. WorkView.Build()
13. conversation route guard
14. Prompts.Build()
15. applyPromptPolicy()
16. pinPromptBundleHash()
17. modelDecision()
18. dispatch()
```

循环限制：

```text
max_steps
max_duration_seconds
max_prompt_tokens
max_tool_calls
max_model_retries
max_repair_attempts
max_consecutive_tool_failures
```

### 4.4 CandidateProvider

当前候选召回来自 `internal/discovery/tool/static.go`：

```text
CandidateSet
  capabilities
  skills
  skill_instructions
  tools
```

当前过滤逻辑：

```text
skills:
  来自 AgentDefinition.Skills / SkillDefinitions / 默认 skills。
  按 objective 与 name / description / tags / when_to_use 粗匹配排序。

tools:
  来自 ToolRegistry.Cards()。
  private 工具不进入候选。
  agent denied / policy denied 会过滤。
  如果 agent allowed_tool_ids 非空，只允许这些工具。
  如果 policy allowed_tool_ids 非空，只允许这些工具。
  再按 objective / when_to_use 排序。
```

开发者影响候选的当前方式：

```text
1. 在 AgentPackageSource.tool_bindings 里配置 allowed / denied。
2. 给工具写好 name / description / when_to_use。
3. 给 skill 写好 name / description / tags / when_to_use。
4. 配置 policy 的 tool policy。
```

未来 Runtime Hook 会在 `after_candidate_retrieval` 允许返回排序建议，但不能添加未授权工具。

### 4.5 WorkView

`WorkView` 是模型调用前的结构化工作视图，包含：

```text
run_id / task_id
agent summary
user_input
conversation_context
task_summary
plan_summary / current_plan_step
handoff_context
memory_summaries
artifact_refs
tool_result_summaries
candidate_capabilities
candidate_skills
candidate_skill_instructions
candidate_tools
constraints
risk_marks
```

开发者当前不能直接改 WorkView。可以间接影响：

```text
AgentDefinition
tool / skill candidates
memory / artifact / tool result 数据
conversation context
policy constraints
```

未来 `before_context_build` Hook 会允许对上下文块、上下文引用、排序、压缩和 planner hint 提建议。

### 4.6 PromptBundle

`PromptBundle` 是模型实际看到的包：

```text
system
developer
task
context
skill_instructions
tool_cards
tool_definitions
output_schema
constraints
hash
```

PromptBundle 构建后会执行 `applyPromptPolicy()`：

```text
blocked phrase 检查
max prompt tokens 检查
compression policy
重新计算 hash
```

开发者当前影响 PromptBundle 的方式：

```text
system_prompt / developer_prompt / prompt / agents_md
skill instruction / constraints / output_requirements
tool cards / tool definitions
policy prompt settings
```

未来 `before_model_call` Hook 可以追加 visible constraints、非敏感 context section、planner hints、format hints，但仍要经过 prompt policy。

### 4.7 Model Decision

模型必须返回合法 `Decision`：

```text
reply
tool_call
ask_clarification
no_op
unsupported
error
```

`modelDecision()` 会：

```text
调用模型
记录 model.called / model.completed / model.delta trace
解析 Decision
校验 schema 和工具候选
失败时按 max_repair_attempts 注入 repair prompt 重试
```

未来第一阶段只建议开放 `on_model_decision` 只读观察，不开放可写 `after_model_decision`。

### 4.8 Dispatch

Decision dispatch 结果：

```text
reply
  Task complete，Run completed，同步回复。

ask_clarification
  Task ask_clarification，Run waiting_input。

no_op
  Task complete，Run completed。

unsupported
  Task complete，返回 refusal。

error
  Task fail，Run failed。

tool_call
  进入 dispatchToolCalls，然后通常继续下一 step。
```

### 4.9 Tool 调用生命周期

Tool call 当前由 `ToolRuntime.Invoke()` 统一治理：

```text
1. 通过 Registry.Get(tool_id) 找工具。
2. 校验 input_schema。
3. 运行 tool policy。
4. 如果需要 approval，返回 pending_approval。
5. 解析 execution_profile。
6. 记录 tool.invoked trace。
7. 执行 executor / execution domain。
8. 校验 output_schema。
9. 记录 tool.completed / tool.failed trace。
10. 返回 ToolResult。
```

Coordinator 还会在调用前后做：

```text
tool_call_id 补齐
tenant_id / run_id / task_id 补齐
idempotency_key 计算
max_tool_calls 检查
ToolRepo.SaveCall()
ToolRepo.SaveResult()
task event: tool_waiting / tool_completed
artifact sync
pending approval 时 Run waiting_approval
```

如果工具需要人工审批：

```text
1. ToolRuntime 返回 pending_approval。
2. Run 标记 waiting_approval。
3. 外部调用 task.command / approve_action 或 approval.approve。
4. server 以 ApprovalGranted=true 重新 Invoke。
5. 保存 ToolResult。
6. ResumeRun 继续 loop。
```

## 5. 当前已暴露接口

### 5.1 Command 接口

当前服务通过 envelope command 分发。Runtime caller 可用：

```text
agent.run
task.start
task.command
tools.invoke
artifact.read
origin.agent.delegate
```

Optimizer / Admin 可用：

```text
prompt.preview
agent.package.draft.create
agent.package.draft.patch_prompt
agent.package.draft.patch_developer_prompt
agent.package.draft.patch_system_prompt
agent.package.draft.patch_agents_md
agent.package.tool_binding.update
agent.package.skill.add
agent.package.skill.update
agent.package.skill.remove
agent.package.proposal.create
agent.package.proposal.submit
agent.package.proposal.approve
agent.package.proposal.reject
agent.package.proposal.publish
agent.package.draft.validate
agent.package.review
agent.package.publish
agent.package.canary
agent.package.stable
agent.package.rollback
policy.*
approval.*
eval.*
artifact.delete
runtime_hook.provider.upsert
runtime_hook.provider.list
runtime_hook.binding.upsert
runtime_hook.binding.list
runtime_hook.preview
```

### 5.2 Prompt Preview

`prompt.preview` 可用于开发期检查：

```text
AgentDefinition
PolicySet
WorkView
PromptBundle
prompt_bundle_hash
token_estimate
model_provider / model_name
```

它可以预览已发布 Agent，也可以传 `draft_id` 预览未发布 draft。

### 5.3 Tool Registry 接口

当前 Go 内部工具插件接口：

```go
type Executor interface {
    Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error)
}

type Tool struct {
    Definition contracts.ToolDefinition
    Executor   Executor
    WhenToUse  []string
}
```

ToolDefinition 必填：

```text
tool_id
name
description
input_schema
output_schema
risk_level
visibility
execution_profile
version
```

当前内置工具：

```text
echo
artifact.create
origin.agent.delegate
origin.permission.check
origin.identity.resolve_member
origin.skill.propose_update
origin.knowledge.create
origin.knowledge.ingest
origin.knowledge.search
origin.cross_group.search
origin.memory.share
origin.agent.progress_query
origin.agent.capability_match
origin.agent.create_draft
origin.tone.decide
```

### 5.4 外部 Tool Provider

外部 HTTP 工具 provider 属于动态工具注册设计范围。开发者应按工具文档提供：

```text
catalog
invoke
health
```

并最终进入 ToolManifest / ToolRuntime，而不是绕过 ToolRuntime 直接执行。

## 6. Runtime Hook 插件接口

### 6.1 当前状态

当前代码已落地 Runtime Hook MVP，核心接口在：

```text
internal/runtime/hook/hooks.go
internal/runtime/hook/service.go
internal/runtime/hook/store.go
internal/runtime/kernel/coordinator.go
internal/storage/postgres/postgres.go
internal/server/server.go
```

已支持：

```text
Observer:
  on_run_started
  on_context_built
  on_model_decision
  on_tool_result
  on_run_finished

Transformer:
  after_candidate_retrieval
  before_context_build
  before_model_call
  before_memory_write
```

已支持两种接入形态：

```text
go
  进程内/config-driven patch，适合 MVP、测试和内置策略。

static_hook_host
  外部 Hook Host，RuntimeHookService 会 POST /runtime-hooks/invoke。
```

Hook 可以来自 AgentPackageSource.runtime_hooks，也可以来自 `agent_runtime_hook_bindings`。Core 启动时会把 `RuntimeHookService` 装配到 `Coordinator.RuntimeHooks`；Postgres 可持久化 provider / binding / event，未配置数据库时使用 InMemoryStore。

### 6.2 Hook 生命周期

当前与后续规划的 Hook 阶段：

```text
Phase 1：只读观察 Hook
  on_run_started
  on_context_built
  on_model_decision
  on_tool_result
  on_run_finished

Phase 2：数据变换 Hook
  before_context_build
  after_candidate_retrieval
  before_model_call
  before_memory_write

Phase 3：策略型 Hook
  MemoryPolicy
  PlannerPolicy
  ToolRankingPolicy
  StopConditionPolicy

Phase 4：完整 RuntimeDriver
  暂不进入 MVP。
```

MVP 结论：

```text
做 Data Hook，不做 control-flow plugin。
```

### 6.3 Data Hook 返回值

Hook 只返回 patch / hints / intents：

```json
{
  "add_context_blocks": [],
  "drop_context_refs": [],
  "tool_rank_adjustments": [],
  "memory_write_intents": [],
  "planner_hints": []
}
```

Kernel 统一校验后应用：

```text
schema validation
tenant validation
permission validation
policy validation
token budget validation
tool availability validation
memory scope validation
trace / audit recording
```

实际应用点：

```text
after_candidate_retrieval
  调整候选工具排序或丢弃候选工具。只能影响已经授权并检索出的工具。

before_context_build
  追加 context block、请求移除非安全关键 context ref、追加 planner hint。

before_model_call
  在 PromptBundle 前追加受控上下文和可见约束，并重新计算 prompt hash。

before_memory_write
  返回 memory_write_intents；Kernel 通过 MemoryService + MemoryPolicy 写入，Hook 不直接写 memory。
```

### 6.4 Hook 不能做什么

Hook 不能：

```text
控制主循环
执行工具
写 memory / task / run / trace
修改 policy decision
绕过 approval
添加未授权 tool
移除安全必需上下文
覆盖 system / developer prompt
直接改模型 decision
```

Hook 可以：

```text
影响候选排序
追加受控上下文块
请求移除非必需上下文 ref
追加可见约束
给 planner hint
给 memory write intent
记录观察事件
```

### 6.5 Hook Provider 与 Binding

外部 Hook Host 需要提供：

```http
GET /runtime-hooks/catalog
POST /runtime-hooks/invoke
GET /health
```

invoke 请求包含：

```text
hook_id
hook_version
phase
tenant_id
agent_id
run_id
task_id
step_id
trace_id
package_version_id
policy_set_id
objective
payload
limits
```

invoke 响应包含：

```text
status
reason
patch
hints
diagnostics
```

当前服务端已暴露：

```text
runtime_hook.provider.upsert
runtime_hook.provider.list
runtime_hook.binding.upsert
runtime_hook.binding.list
runtime_hook.preview
agent.package.runtime_hooks.update
```

AgentPackageSource 推荐字段：

```json
{
  "runtime_hooks": {
    "mode": "data_hooks",
    "hooks": [
      {
        "hook_id": "crm-context-ranker",
        "phase": "after_candidate_retrieval",
        "provider_type": "static_hook_host",
        "version": "v1",
        "enabled": true,
        "timeout_ms": 300,
        "failure_policy": "ignore"
      }
    ]
  }
}
```

`provider_type` 当前支持：

```text
go
  从 provider.config + binding.config 读取 patch，适合内置策略和快速验证。

static_hook_host
  根据 provider.endpoint 调用 /runtime-hooks/invoke。
```

`failure_policy` 当前支持：

```text
ignore
  Hook 失败时记录 trace/audit/event，主 run 继续。

reject
  Hook 失败时中断当前 run。
```

## 7. Agent 协作与能力工具化接口

### 7.1 当前状态

当前已有 `origin.agent.delegate` 工具，底层会创建：

```text
AgentHandoff
child task
child run
handoff context package
trace / audit
```

当前 `origin.agent.delegate` 已强制要求显式 `to_agent_id`，并且目标必须出现在源 Agent 的 `collaborators` 中；没有 fallback 到 `test-agent`。开发者应把可委派对象写入 package draft 的 `collaborators`，再通过 `agent.package.collaborator.*` 命令维护。

### 7.2 协作配置

Agent 协作拆成三条线：

```text
collaborators
  当前 Agent 可以委派给谁。

exports.tools
  当前 Agent 愿意把哪些固定能力导出为工具。

tool_bindings
  当前 Agent 被允许调用哪些工具或工具组。
```

边界：

```text
被列为 collaborator 不等于自动获得对方 exported tools。
声明 exports.tools 不等于所有调用方自动可用。
最终仍由 tool_bindings + policy + approval 治理。
```

## 8. 开发者接入清单

### 8.1 开发一个普通 Agent

```text
1. 编写 agents_md / prompt。
2. 配置 system_prompt / developer_prompt / policy_set_id。
3. 配置 runtime limits。
4. 配置 tool_bindings。
5. 添加 skill_definitions。
6. 用 prompt.preview 检查 WorkView / PromptBundle。
7. validate。
8. review 或 proposal approve。
9. publish。
10. eval。
11. canary。
12. stable。
```

### 8.2 开发一个工具插件

```text
1. 定义 tool_id / name / description / when_to_use。
2. 定义 input_schema / output_schema。
3. 定义 risk_level / visibility。
4. 选择 execution_profile。
5. 实现 executor 或外部 provider invoke。
6. 注册到 ToolRegistry / ToolCatalog。
7. 在 Agent tool_bindings 里授权。
8. 确认 policy / approval 行为。
9. 用 agent.run 或 tools.invoke 测试。
10. 检查 trace / audit / ToolResult。
```

### 8.3 开发一个 Runtime Hook 插件

当前推荐先开发 Data Hook，不开发自定义 RuntimeDriver：

```text
1. 选择 hook phase：after_candidate_retrieval / before_context_build / before_model_call / before_memory_write。
2. 选择 provider_type：go 或 static_hook_host。
3. 定义 Patch：context blocks、drop refs、tool rank adjustments、planner hints、memory write intents。
4. 用 runtime_hook.provider.upsert 注册 provider。
5. 用 agent.package.runtime_hooks.update 或 runtime_hook.binding.upsert 绑定到 Agent。
6. 用 runtime_hook.preview 检查 patch。
7. 用 agent.run、trace、audit、runtime_hook_events 检查真实运行。
8. 用 eval 验证稳定性，再进入 canary / stable。
```

进程内测试也可以直接实现 `runtimehook.Observer` / `runtimehook.Transformer`，或用 `go` provider 的 config patch 做快速验证。

## 9. 常见边界问题

### 9.1 我能不能在插件里直接调用工具？

Runtime Hook 不能直接调用工具。工具调用必须走：

```text
model decision -> ToolCall -> ToolRuntime.Invoke() -> policy / approval / trace / audit
```

如果你的插件本身是一个工具 provider，那么它只在 ToolRuntime 调用它时执行。

### 9.2 我能不能在 Hook 里写 memory？

不能。Hook 只能返回 `memory_write_intents`，由 Kernel 和 MemoryPolicy 校验后决定是否写入。

### 9.3 我能不能修改 system prompt？

Agent Package 开发者可以通过 draft / patch / publish 修改 system prompt。

Runtime Hook 不能覆盖 system / developer prompt，只能追加可见、受控的辅助上下文或约束，并且要经过 prompt policy。

### 9.4 我能不能绕过 package 发布流热更新 Agent？

不应这样做。正式路径必须走：

```text
draft -> validate -> review/proposal -> publish -> eval -> canary/stable
```

直接改运行时内存会破坏 hash、trace、audit、replay、eval 和 release 边界。

### 9.5 Agent 能不能自动把所有能力暴露成工具？

不应该。`AgentCapability` 当前更像能力检索卡，不是严格工具接口。固定能力要暴露为工具，需要显式 `exports.tools`、schema、risk、visibility、version 和 tool binding。

## 10. 最小开发者心智模型

一句话：

```text
AgentPackage 决定 Agent 是谁。
tool_bindings + policy 决定 Agent 能用什么。
Coordinator 决定 run 怎么推进。
ToolRuntime 决定工具能不能安全执行。
Runtime Hook 只能影响数据和建议，不能接管控制流。
Agent collaborator 决定可以委派给谁。
Agent exports.tools 决定哪些固定能力可以被同步成工具。
```

如果开发者只记住一条边界：

```text
所有会改变状态的动作，都必须回到 Kernel / ToolRuntime / MemoryService / TaskRuntime / PackageService，
不能由插件私自落状态。
```
