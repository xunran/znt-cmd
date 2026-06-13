# 智能体创建者策略体系重构开发计划

日期：2026-06-12

## 1. 背景

当前 Clean Core 已经具备 Agent package、Prompt profile、Tool binding、Skill definition、Collaborator、Runtime hook、PolicySet、Release/Eval、Run/Trace/Diagnostics 等基础能力。

但从“智能体创建者”的视角看，很多可配置能力分散在不同位置：

- 一部分在 `AgentDefinition`：prompt、tools、skills、collaborators、runtime hooks、runtime limits。
- 一部分在 `PolicySet`：runtime、tool、approval、prompt、compression、handoff、release、memory、artifact。
- 一部分在独立服务：tone、intake、knowledge、memory scope、agent capability、runtime hook provider。
- 一部分来自外部创建载体：AgentPlugin Service / ToolHost Service 可以用插件方式声明工具、Hook、上下文补丁和智能体能力。
- 一部分仍然硬编码在 runtime kernel：上下文窗口、检索数量、压缩方式、候选排序细节。

这会导致两个问题：

1. 创建者无法清晰理解“这个智能体的行为到底由哪些策略决定”。
2. 服务端代码里 Agent 自身意图、平台治理约束、租户运营规则混在一起，后续会越来越难扩展。

本阶段处于开发期，不考虑旧数据兼容和旧 API 兼容。目标是重构核心服务模块，不涉及前端。

## 2. 重构目标

本次重构不做一个过度抽象的万能策略系统，而是建立一套清晰、可版本化、可预览、可诊断的创建者策略模型。

目标：

1. 明确哪些策略应该由智能体创建者配置。
2. 明确哪些策略属于平台治理，不应该由单个 Agent 任意突破。
3. 明确哪些策略属于租户/群组运营层，不应该塞进 Agent package。
4. 将 Agent 运行时策略纳入 Agent package 编译、发布、灰度、回滚、eval、prompt.preview。
5. 消除关键运行策略的硬编码。
6. 将原生 Agent package 和 AgentPlugin Service 统一到同一套编译、发布、策略解析、治理和诊断模型。
7. 保持实现简单，不引入复杂 DSL，不做前端。

## 3. 当前代码中的策略版图

### 3.1 AgentDefinition 已有创建者配置

文件：`internal/contracts/agent.go`

当前 `AgentDefinition` 包含：

- `IdentityPrompt`
- `SystemPrompt`
- `DeveloperPrompt`
- `Skills`
- `SkillDefinitions`
- `Tools`
- `Collaborators`
- `Exports`
- `RuntimeHooks`
- `PolicyRefs`
- `Runtime`

这些大部分都属于创建者配置。

### 3.2 PolicySet 已有治理策略

文件：`internal/contracts/policy.go`

当前 `PolicySet` 包含：

- `RuntimePolicy`
- `ToolPolicy`
- `ToolRepairPolicy`
- `ApprovalPolicy`
- `PromptPolicy`
- `ContextGovernancePolicy`
- `RecoveryPolicy`
- `TaskUpgradePolicy`
- `HandoffPolicy`
- `ReleasePolicy`
- `MemoryPolicy`
- `ArtifactPolicy`

其中有些是平台治理上限，有些其实是 Agent 行为偏好。现在它们放在一起，语义容易混。

### 3.3 Agent package source 已有输入

文件：`internal/agentdef/package/service.go`

当前 `AgentPackageSource` 包含：

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

当前很多字段经由 `Metadata` 编译进入 `AgentDefinition.Runtime`、prompt、skills。这适合早期开发，但长期看会让配置结构不稳定。

### 3.4 Runtime kernel 中的隐性策略

文件：`internal/runtime/kernel/coordinator.go`

当前 `step()` 中隐含了多类策略：

- capability/tool/skill candidate retrieval
- context collection
- conversation guard no-op
- prompt policy
- model retry/repair
- tool dispatch
- memory write hook
- task upgrade

其中一部分应该显式成为 Agent strategy 或 Policy guardrail。

### 3.5 独立服务中的策略

当前独立服务也承载了一些“策略”：

- `internal/intake`：入口匹配、预回复、是否继续运行。
- `internal/tone`：群组语气与是否回复。
- `internal/knowledge`：知识库可见性、搜索模式、搜索 limit。
- `internal/memoryscope`：记忆读写可见性。
- `internal/runtime/hook`：hook 绑定、失败策略、审批策略。
- `internal/agentcapability`：能力匹配 limit。

这些不一定都应该进入 Agent package。需要分清归属。

### 3.6 AgentPlugin Service / ToolHost 已有接入基础

当前代码中，插件式接入已经不是空白能力：

- `internal/tool/catalog/catalog.go` 已有规范 provider type：
  - `static_tool_host`
  - `agent_plugin_service`
  - `mcp`
  - `http_api_adapter`
  - `database_adapter`
- `agent_plugin_service` 会通过 `ToolProvider.ServiceConnectionID` 绑定 `ServiceConnection`；`/tools/catalog` 或 `/tools` 用于同步工具目录，`/.well-known/agent-plugin.json` 用于同步完整插件 manifest。
- `ExecutorTypeAgentPlugin` 和 `ExecutorTypeStaticToolHost` 当前共用 `ToolHostExecutor`，执行时调用外部服务 `/tools/invoke`，并记录 provider trace。
- `internal/serviceconnection/service.go` 管理 base URL、auth_ref、network_scope、timeout、retry、health。
- `internal/runtime/hook` 支持 `static_hook_host`，外部服务可以在 `before_context_build`、`after_candidate_retrieval`、`before_model_call`、`before_memory_write` 返回 patch。
- `internal/tool/agenttool/handler.go` 支持内部 Agent exported tool，执行域为 `agent_tool`，会回到 CleanCore 启动目标 Agent run。

因此 AgentPlugin Service 不应该只被理解成“外部工具来源”。对智能体创建者来说，它还应是和 `AgentPackageSource` 并列的“创建载体”：创建者可以用插件服务开发智能体能力，但运行、治理、发布、trace、memory、approval 仍由 CleanCore 统一收口。

## 4. 策略归属原则

### 4.1 Agent-owned

由智能体创建者配置，并随 Agent 版本发布、灰度、回滚。

典型内容：

- Prompt 策略
- 上下文策略
- 压缩策略
- 模型偏好
- 工具使用策略
- 技能策略
- 协作/交接策略
- 运行策略
- 修复策略
- 记忆使用偏好
- 知识检索偏好
- 输出策略
- runtime hook 绑定
- AgentPlugin Service 绑定与能力选择

### 4.2 Policy-owned

由平台或租户管理员配置，作为治理上限。Agent 创建者可以表达偏好，但不能突破这些约束。

典型内容：

- 最大运行步数上限
- 最大工具调用数上限
- 最大 prompt token 上限
- 可用/禁用工具范围
- 高风险工具审批
- 是否允许 LLM 压缩
- 是否允许跨 Agent handoff
- 是否允许写 memory
- 是否允许 artifact 删除
- 发布审批和灰度规则

### 4.3 Tenant/group-owned

属于租户、群组或渠道运营配置，不随单个 Agent 版本发布。

典型内容：

- intake policy
- tone policy
- group permission
- memory scope
- knowledge base visibility
- cross-group share
- external bridge binding

Agent 可以引用或请求使用这些资源，但不应该把它们内联进 Agent package。

### 4.4 AgentPlugin Service-owned

由插件服务或企业执行面负责，CleanCore 只保存引用、声明、能力目录和治理状态。

典型内容：

- 外部服务实现代码。
- 私有凭证、本地密钥、KMS/HSM 访问。
- 企业系统连接细节。
- 本地模型、本地数据处理、私有检索实现。
- `/tools/catalog`、`/tools/invoke`、`/runtime-hooks/invoke` 的服务端实现。
- 插件服务自身的部署、健康检查、版本发布。

边界：

- AgentPlugin Service 可以声明能力，但不能直接写 CleanCore Task / Run / Memory。
- AgentPlugin Service 可以返回工具结果、Hook patch、planner hints，但不能绕过 CleanCore Policy / Approval。
- AgentPlugin Service 可以有自己的内部实现策略，但进入 CleanCore 后必须被编译成 `AgentDefinition`、`ToolManifest`、`RuntimeHookBinding` 等核心契约。

## 5. 建议的策略分层模型

### 5.1 不做万能 Strategy DSL

不建议做：

```json
{
  "strategies": [
    {"type": "...", "rules": [{"when": "...", "then": "..."}]}
  ]
}
```

原因：

- 当前服务端运行链路已经明确，不需要 DSL。
- DSL 会增加验证、审计、调试成本。
- 创建者真正需要的是结构化配置和预览结果。

建议做明确结构体：

```go
type AgentDefinition struct {
    // existing fields...
    Strategies AgentStrategies `json:"strategies,omitempty"`
}

type AgentStrategies struct {
    Prompt        PromptStrategy        `json:"prompt,omitempty"`
    Model         ModelStrategy         `json:"model,omitempty"`
    Context       ContextStrategy       `json:"context,omitempty"`
    Tools         ToolUseStrategy       `json:"tools,omitempty"`
    Skills        SkillUseStrategy      `json:"skills,omitempty"`
    Collaboration CollaborationStrategy `json:"collaboration,omitempty"`
    Memory        MemoryUseStrategy     `json:"memory,omitempty"`
    Knowledge     KnowledgeUseStrategy  `json:"knowledge,omitempty"`
    Runtime       RuntimeStrategy       `json:"runtime,omitempty"`
    Repair        RepairStrategy        `json:"repair,omitempty"`
    Output        OutputStrategy        `json:"output,omitempty"`
}
```

注意：`RuntimeHooks` 可以先保留原字段，不急着塞进 `Strategies`。它是扩展机制，不是普通策略。

### 5.2 PolicySet 作为 guardrails

建议将 `PolicySet` 语义调整为治理上限，而不是 Agent 偏好。

```go
type PolicySet struct {
    PolicySetID PolicySetID `json:"policy_set_id"`
    TenantID    TenantID    `json:"tenant_id"`
    Version     string      `json:"version"`

    RuntimeGuardrails       RuntimeGuardrails       `json:"runtime_guardrails"`
    ModelGuardrails         ModelGuardrails         `json:"model_guardrails,omitempty"`
    ContextGuardrails       ContextGuardrails       `json:"context_guardrails,omitempty"`
    ToolGuardrails          ToolGuardrails          `json:"tool_guardrails"`
    CollaborationGuardrails CollaborationGuardrails `json:"collaboration_guardrails,omitempty"`
    MemoryGuardrails        MemoryGuardrails        `json:"memory_guardrails,omitempty"`
    ArtifactGuardrails      ArtifactGuardrails      `json:"artifact_guardrails,omitempty"`
    ReleasePolicy           ReleasePolicy           `json:"release_policy"`
}
```

开发阶段不考虑兼容，直接替换旧字段，避免 PolicySet 新旧语义双轨。

## 6. 创建者可配置策略清单

### 6.1 PromptStrategy

已有基础：

- `IdentityPrompt`
- `SystemPrompt`
- `DeveloperPrompt`
- `PromptPolicy.BlockedPhrases`
- `PromptPolicy.MaxPromptTokens`

建议目标：

```go
type PromptStrategy struct {
    IdentityPrompt  string `json:"identity_prompt,omitempty"`
    SystemPrompt    string `json:"system_prompt,omitempty"`
    DeveloperPrompt string `json:"developer_prompt,omitempty"`

    TemplateMode string `json:"template_mode,omitempty"` // strict_json_decision, conversational, tool_planner
    BlockedPhrases []string `json:"blocked_phrases,omitempty"`
    MaxPromptTokens int `json:"max_prompt_tokens,omitempty"`
}
```

重构建议：

- 保留 `AgentDefinition.IdentityPrompt/SystemPrompt/DeveloperPrompt` 作为编译后的平铺字段，运行时读取方便。
- Agent package source 中新增 `strategies.prompt`，编译时同步写入平铺字段。
- `BlockedPhrases` 更偏治理，可以放在 guardrail；如果放在 Agent 侧，只能更严格，不能覆盖平台禁词。

### 6.2 ModelStrategy

当前模型配置主要来自服务级 config：

- `model_provider`
- `model_base_url`
- `model_name`
- `model_max_tokens`
- `model_temperature`
- `model_thinking`
- `model_reasoning_effort`

这对创建者不够。不同 Agent 可能需要不同模型。

建议：

```go
type ModelStrategy struct {
    Provider string `json:"provider,omitempty"`
    Model    string `json:"model,omitempty"`

    MaxOutputTokens int `json:"max_output_tokens,omitempty"`
    Temperature *float64 `json:"temperature,omitempty"`
    Thinking string `json:"thinking,omitempty"`
    ReasoningEffort string `json:"reasoning_effort,omitempty"`

    TimeoutMS int `json:"timeout_ms,omitempty"`
    Streaming bool `json:"streaming,omitempty"`
}
```

治理：

```go
type ModelGuardrails struct {
    AllowedProviders []string `json:"allowed_providers,omitempty"`
    AllowedModels    []string `json:"allowed_models,omitempty"`
    MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
    MaxTimeoutMS     int      `json:"max_timeout_ms,omitempty"`
}
```

代码改造点：

- `internal/app/core/modelClientFromConfig` 保留作为默认模型。
- `Coordinator` 在每次 run 时解析 effective model strategy。
- `completeModel/streamModel` 使用 effective model client。
- `VersionSnapshot` 记录实际 provider/model。

不过不要一开始支持每个 Agent 自带 base_url/api_key。开发阶段建议：

- Agent 只能选择 provider/model alias。
- 真实 base URL 和 API key 仍由服务端配置或 service connection 管理。

### 6.3 ContextStrategy

已在 `docs/context_strategy_compression_refactor_plan_20260612.md` 单独设计。

这里作为 AgentStrategies 的一部分。

核心字段：

- recent message limit
- retrieval max results
- task history max items
- memory max items
- artifact ref max items
- tool result max items
- source budgets
- token budget
- compression

治理：

- `ContextGovernancePolicy` 需要能限制 context token budget、recent/retrieval/task history、memory、artifact refs、tool results 和 LLM compression 范围。
- Agent 可以选择更少或关闭某类来源，但不能用 `0 = unlimited` 突破 policy 上限。

代码改造点：

- `internal/runtime/kernel/conversation_context.go`
- `internal/context/conversation/engine.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/context/promptbundle/builder.go`

### 6.4 ToolUseStrategy

当前已有：

- `AgentToolsConfig.AllowedToolIDs`
- `AllowedToolGroupIDs`
- `DeniedToolIDs`
- `DeniedToolGroupIDs`
- `ExposedToolIDs`
- `PolicySet.ToolPolicy`
- capability discovery 中按 objective + allowed/denied 过滤工具。

建议将 Agent 侧工具偏好升级为：

```go
type ToolUseStrategy struct {
    AllowedToolIDs []string `json:"allowed_tool_ids,omitempty"`
    AllowedToolGroupIDs []string `json:"allowed_tool_group_ids,omitempty"`
    DeniedToolIDs []string `json:"denied_tool_ids,omitempty"`
    DeniedToolGroupIDs []string `json:"denied_tool_group_ids,omitempty"`
    ExposedToolIDs []string `json:"exposed_tool_ids,omitempty"`

    PreferredToolIDs []string `json:"preferred_tool_ids,omitempty"`
    ToolChoiceMode string `json:"tool_choice_mode,omitempty"` // auto, conservative, tool_first, no_tools

    MaxToolCalls int `json:"max_tool_calls,omitempty"`
    RequireApprovalAtRiskLevel contracts.RiskLevel `json:"require_approval_at_risk_level,omitempty"`
}
```

治理：

- 平台 policy 仍可 deny tools。
- Agent 可以更严格，不能更宽。
- `MaxToolCalls` 取 Agent 与 policy 更小值。

代码改造点：

- `internal/discovery/tool/static.go`
- `internal/policy/toolpolicy/evaluator.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/tool/runtime/runtime.go`
- `internal/agentdef/package/compiler.go`

不过不要在第一阶段做复杂 tool planner。先做：

- allowed/denied/preferred
- tool_choice_mode
- max_tool_calls

### 6.5 SkillUseStrategy

当前已有：

- `AgentDefinition.Skills`
- `SkillDefinitions`
- `SkillInstruction`
- `AllowedTools`
- `RecommendedTools`
- `RecommendedMemoryReads/Writes`
- `RecommendedHandoffs`
- `CompletionCriteria`
- `OutputSchema`

这些已经像策略了，但都混在 skill definition 里。

建议保持 SkillDefinition 结构，不新增复杂策略，只新增 Agent 层选择策略：

```go
type SkillUseStrategy struct {
    EnabledSkillIDs []string `json:"enabled_skill_ids,omitempty"`
    DisabledSkillIDs []string `json:"disabled_skill_ids,omitempty"`
    SelectionMode string `json:"selection_mode,omitempty"` // auto, explicit_only, all_enabled
    MaxSelectedSkills int `json:"max_selected_skills,omitempty"`
}
```

代码改造点：

- `internal/discovery/tool/static.go` 的 `skillCandidates`
- `internal/context/promptbundle/builder.go` 注入 skill instructions

不过当前 SkillDefinition 已经足够，建议低优先级。

### 6.6 CollaborationStrategy

当前已有：

- `AgentCollaboratorRef`
- `AllowedHandoffModes`
- `DefaultHandoffMode`
- `MaxContextTokens`
- `RequiresApproval`
- `Status`
- `PolicySet.HandoffPolicy`
- `origin.agent.delegate`

建议创建者可配置：

```go
type CollaborationStrategy struct {
    Collaborators []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`

    DelegationMode string `json:"delegation_mode,omitempty"` // disabled, explicit, auto
    MaxHandoffDepth int `json:"max_handoff_depth,omitempty"`
    MaxChildTasks int `json:"max_child_tasks,omitempty"`
    DefaultHandoffMode contracts.HandoffMode `json:"default_handoff_mode,omitempty"`
    MaxContextTokens int `json:"max_context_tokens,omitempty"`
}
```

治理：

- 是否允许跨 Agent handoff。
- 是否允许 full context。
- 是否需要审批。
- 最大 handoff depth。

代码改造点：

- `internal/tool/handoff/executor.go`
- `internal/task/handoff/service.go`
- `internal/context/handoffpkg/builder.go`
- `internal/runtime/kernel/coordinator.go`

短期建议：

- 保留 `AgentDefinition.Collaborators`。
- 新增 `Strategies.Collaboration` 只表达全局 delegation mode 和 limits。
- 不重复存 collaborators。

### 6.7 MemoryUseStrategy

当前已有：

- `MemoryPolicy.AllowWrite/AllowRead/Scopes`
- `MemoryScope`
- runtime hook 的 `MemoryWriteIntent`
- `memorySummaries()` 自动注入 memory summary

缺口：

- 创建者不能表达这个 Agent 什么时候读 memory、读多少、写什么类型 memory、是否自动写。

建议：

```go
type MemoryUseStrategy struct {
    ReadEnabled bool `json:"read_enabled"`
    WriteEnabled bool `json:"write_enabled"`

    ReadScopes []string `json:"read_scopes,omitempty"`
    WriteScopes []string `json:"write_scopes,omitempty"`

    MaxMemoryItems int `json:"max_memory_items,omitempty"`
    AutoWriteMode string `json:"auto_write_mode,omitempty"` // disabled, explicit_intent, post_run_summary

    WritePromptProfileID string `json:"write_prompt_profile_id,omitempty"`
}
```

治理：

- Policy 决定是否允许读写。
- MemoryScope 决定具体 memory 可见性。

代码改造点：

- `internal/runtime/kernel/coordinator.go`
- `internal/asset/artifact/memory.go`
- `internal/storage/postgres/postgres.go` memory store
- `internal/runtime/hook` memory write hook

短期建议：

- 先只做 read enabled、write enabled、max items、scopes。
- 自动写 memory 先继续通过 hook，不内建复杂策略。

### 6.8 KnowledgeUseStrategy

当前知识检索通过 tool/service 存在：

- `KnowledgeBase`
- `KnowledgeDocument`
- `Knowledge.SearchInput`
- search mode: bm25/embedding/hybrid
- limit 最大 10

但当前主 PromptBundle 组装没有统一将 knowledge retrieval 作为核心上下文源。

建议：

```go
type KnowledgeUseStrategy struct {
    Enabled bool `json:"enabled"`
    KnowledgeBaseIDs []contracts.KnowledgeBaseID `json:"knowledge_base_ids,omitempty"`
    SearchMode string `json:"search_mode,omitempty"` // bm25, embedding, hybrid
    MaxResults int `json:"max_results,omitempty"`
    AllowCrossGroup bool `json:"allow_cross_group,omitempty"`
    InjectMode string `json:"inject_mode,omitempty"` // retrieved_context, tool_only
}
```

治理：

- group permission 决定是否可 search。
- cross group share 决定跨组知识是否可见。

短期建议：

- 不急着把 knowledge 自动注入主上下文。
- 先作为 tool 能力，策略只决定 candidate 和 tool instruction。
- 后续再纳入 ContextStrategy 的 sources。

### 6.9 RuntimeStrategy

当前已有：

- `RuntimeLimits`
- `RuntimePolicy`

建议把 Agent 创建者偏好放入：

```go
type RuntimeStrategy struct {
    MaxSteps int `json:"max_steps,omitempty"`
    MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
    MaxModelRetries int `json:"max_model_retries,omitempty"`
    MaxConsecutiveToolFailures int `json:"max_consecutive_tool_failures,omitempty"`
    ExecutionMode string `json:"execution_mode,omitempty"` // sync, async
}
```

治理：

- policy guardrails 设置最大上限。

代码改造点：

- `internal/runtime/kernel/coordinator.go`
- `internal/runtime/admission/limiter.go`
- `internal/app/core/core.go`

短期建议：

- 继续编译到 `AgentDefinition.Runtime` 平铺字段。
- `Strategies.Runtime` 是 source-of-truth，`Runtime` 是 compiled/effective runtime。

### 6.10 RepairStrategy

当前已有：

- `ToolRepairPolicy`
- `repairPrompt()`
- `repairAttemptLimit()`
- model retries
- validation repair

建议：

```go
type RepairStrategy struct {
    Enabled bool `json:"enabled"`
    MaxRepairAttempts int `json:"max_repair_attempts,omitempty"`
    RepairableErrorCodes []string `json:"repairable_error_codes,omitempty"`
    RequestModelRepairOnFail bool `json:"request_model_repair_on_fail"`
    StopOnDenied bool `json:"stop_on_denied"`
}
```

治理：

- policy 可降低 repair attempts。
- denied/critical 风险必须由 policy 控制。

代码改造点：

- `internal/runtime/kernel/coordinator.go`
- `internal/policy/engine`

短期建议：

- 将现有 `ToolRepairPolicy` 拆成 Agent strategy + policy guardrail。

### 6.11 OutputStrategy

当前输出主要由 `PromptBundle.OutputSchema` 和 hardcoded decision contract 决定：

- `reply`
- `ask_clarification`
- `tool_call`
- `unsupported`
- `error`
- `no_op`

SkillDefinition 也有 `OutputSchema`。

建议：

```go
type OutputStrategy struct {
    ResponseMode string `json:"response_mode,omitempty"` // decision_json, final_answer, artifact_first
    PreferredReplyStyle string `json:"preferred_reply_style,omitempty"`
    OutputSchema map[string]any `json:"output_schema,omitempty"`
    ArtifactMode string `json:"artifact_mode,omitempty"` // disabled, optional, required_for_long_output
}
```

短期建议：

- 不改变核心 Decision JSON 协议。
- 只允许配置 artifact preference 和 reply style hint。
- `OutputSchema` 先继续来自 skill definition，不做复杂合并。

### 6.12 HookStrategy

当前 `RuntimeHooks` 已经是创建者配置：

- phase
- provider
- timeout
- failure policy
- approval policy
- config

建议不再新增 HookStrategy，保留当前模型。

只做两点清理：

1. 将 `RuntimeHooks` 归入 Agent package source 的结构化字段。
2. 在 `prompt.preview` 和 diagnostics 展示 hook 是否改变了 context/candidates/prompt。

## 7. 不应放入 Agent package 的策略

### 7.1 IntakePolicy

`internal/intake` 是入口/渠道层策略。

它决定：

- 哪类消息先回固定文本。
- 是否继续进入 agent.run。
- 按渠道、agent_id、version 匹配。

这不应放入 Agent package，因为它是接入层运营规则。

保留独立命令：

- intake policy upsert/list/delete

后续可允许 Agent package 引用 intake policy id，但不内联。

### 7.2 TonePolicy

`internal/tone` 是群组/租户风格策略。

它决定：

- 默认语气。
- 高风险动作语气。
- 人对人讨论时 silent。

它应该是 group-owned，不随 Agent 版本发布。

Agent 的 `OutputStrategy.PreferredReplyStyle` 可以作为 hint，但最终 tone policy 可以覆盖。

### 7.3 Permission / MemoryScope / Knowledge Visibility

这些属于治理和数据边界。

Agent 可以声明希望读哪些 memory scope、knowledge base，但实际允许与否由：

- group permission
- memory scope
- knowledge base visibility
- cross group share

决定。

### 7.4 ReleasePolicy

Release/Canary/Stable/Rollback 是发布治理，不是 Agent 行为策略。

它应继续属于 PolicySet 或发布服务，不进入 AgentStrategies。

## 8. 新 Agent source 结构

建议先引入“创建载体”概念，但不要把它做成复杂抽象。

核心枚举：

```go
type AgentSourceKind string

const (
    AgentSourceKindPackage AgentSourceKind = "package"
    AgentSourceKindPlugin  AgentSourceKind = "plugin_service"
)
```

### 8.1 AgentPackageSource

原生包仍是最直接的创建方式，适合完全由 CleanCore 管理 prompt、策略、协作、导出工具和 Hook 绑定的 Agent。

```go
type AgentPackageSource struct {
    AgentsMD string `json:"agents_md"`
    Prompt   string `json:"prompt"`

    Strategies AgentStrategies `json:"strategies,omitempty"`

    ToolBindings  contracts.AgentToolsConfig `json:"tool_bindings,omitempty"`
    Skills        []contracts.SkillDefinition `json:"skills,omitempty"`
    Collaborators []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
    Exports       contracts.AgentExports `json:"exports,omitempty"`
    RuntimeHooks  contracts.AgentRuntimeHooks `json:"runtime_hooks,omitempty"`

    Metadata map[string]any `json:"metadata,omitempty"`
}
```

开发阶段可直接移除 Metadata 中承载策略的用法，只保留少量描述性 metadata。

推荐：

- `name`
- `description`
- `policy_set_id`

其他运行策略都从 `strategies` 读取。

### 8.2 AgentPluginSource

插件式创建方式用于“实现和私有资源在外部 AgentPlugin Service，运行和治理在 CleanCore”的场景。

建议新增轻量结构，不要一开始做复杂插件包管理器：

```go
type AgentPluginSource struct {
    ProviderID          string `json:"provider_id"`
    ManifestVersion     string `json:"manifest_version,omitempty"`

    AgentsMD string `json:"agents_md,omitempty"`
    Prompt   string `json:"prompt,omitempty"`

    Strategies AgentStrategies `json:"strategies,omitempty"`

    ToolBindings  contracts.AgentToolsConfig `json:"tool_bindings,omitempty"`
    Skills        []contracts.SkillDefinition `json:"skills,omitempty"`
    Collaborators []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
    Exports       contracts.AgentExports `json:"exports,omitempty"`
    RuntimeHooks  contracts.AgentRuntimeHooks `json:"runtime_hooks,omitempty"`

    Metadata map[string]any `json:"metadata,omitempty"`
}
```

字段来源：

- `provider_id` 对应 `ToolProvider.ProviderID`，`provider_type` 必须是 `agent_plugin_service`。
- 插件服务连接只从 `ToolProvider.ServiceConnectionID` 解析，不在 `AgentPluginSource` 再存一份，避免双事实源。
- `strategies` 是创建者策略，和原生包共用同一套结构。
- `tool_bindings` 表示这个 Agent 可以调用哪些插件工具或其他工具。
- `runtime_hooks` 表示这个 Agent 绑定哪些外部 Hook，不等于把运行时控制权交给插件服务。

### 8.3 编译后的统一形态

无论来源是 `AgentPackageSource` 还是 `AgentPluginSource`，运行时都只认：

```text
AgentDefinition
PolicySet
ToolManifest
RuntimeHookBinding
ServiceConnection
EffectiveRunConfig
```

这样做可以避免两套 runtime：

- 原生包和插件式 Agent 共用 `Coordinator`。
- 原生包和插件式 Agent 共用 policy、approval、trace、diagnostics。
- 插件服务只扩展能力面，不替代 CleanCore 的 task/run/memory/release/eval 主链路。

## 9. 编译模型

### 9.1 Source 和 compiled definition 的关系

建议区分：

- `AgentPackageSource`：创建者输入。
- `AgentPluginSource`：插件式创建者输入。
- `AgentDefinition`：运行时编译产物。
- `EffectiveRunConfig`：每次 run 解析 policy guardrails 后的实际配置。

新增：

```go
type EffectiveRunConfig struct {
    Prompt PromptStrategy
    Model ModelStrategy
    Context ContextStrategy
    Tools ToolUseStrategy
    Skills SkillUseStrategy
    Collaboration CollaborationStrategy
    Memory MemoryUseStrategy
    Knowledge KnowledgeUseStrategy
    Runtime RuntimeStrategy
    Repair RepairStrategy
    Output OutputStrategy

    Policy contracts.PolicySet
}
```

不要把 `EffectiveRunConfig` 存库为主数据。它是 run-time 派生结果，可在 trace 中记录 hash 和摘要。

### 9.2 编译流程

原生包编译流程：

1. 读取 source prompt/agents_md。
2. 读取 `source.Strategies`。
3. 校验结构化策略。
4. 写入 `AgentDefinition.Strategies`。
5. 为运行时便利，继续填充平铺字段：
   - prompt fields
   - tools
   - collaborators
   - runtime limits
   - skills
   - exports
   - hooks

这样能减少 runtime 改造面积。

插件式 Agent 编译流程：

1. 校验 `provider_id` 对应的 `ToolProvider.provider_type == agent_plugin_service`。
2. 从 `ToolProvider.ServiceConnectionID` 解析 `ServiceConnection`，并校验连接存在、状态可用、base URL 可用。
3. 拉取或读取 `AgentPluginManifest`，解析插件声明的 agent metadata、tools、hooks、exports、collaborators。
4. 将插件 manifest 与 `AgentPluginSource.Strategies` 合并，创建 `AgentDefinition`。
5. 将插件工具能力同步为 `ToolManifest`，executor type 使用 `agent_plugin_service`。
6. 将插件 Hook 能力同步为 `RuntimeHookProvider` / `RuntimeHookBinding`。Runtime hook provider 需要支持 `service_connection_id`，不要只复制一个裸 endpoint。
7. 生成 source hash、manifest hash、compiled hash，进入发布、eval、canary、stable 流程。

插件 manifest 只提供声明，最终能否进入运行时仍取决于 CleanCore 编译校验和 policy guardrails。

新增 manifest 契约建议：

```go
type AgentPluginManifest struct {
    ManifestVersion string `json:"manifest_version,omitempty"`
    ProviderID      string `json:"provider_id,omitempty"`

    Agent AgentPluginAgentManifest `json:"agent"`
    Tools []AgentPluginToolManifest `json:"tools,omitempty"`
    Hooks []runtimehook.HookManifest `json:"hooks,omitempty"`

    Collaborators []contracts.AgentCollaboratorRef `json:"collaborators,omitempty"`
    Exports       contracts.AgentExports `json:"exports,omitempty"`
    Strategies    AgentStrategies `json:"strategies,omitempty"`
}

type AgentPluginAgentManifest struct {
    AgentID     contracts.AgentID      `json:"agent_id"`
    Version     contracts.AgentVersion `json:"version,omitempty"`
    Name        string                 `json:"name,omitempty"`
    Description string                 `json:"description,omitempty"`
    AgentsMD    string                 `json:"agents_md,omitempty"`
    Prompt      string                 `json:"prompt,omitempty"`
}

type AgentPluginToolManifest struct {
    ToolID       string                   `json:"tool_id"`
    Operation    string                   `json:"operation,omitempty"`
    GroupID      string                   `json:"group_id,omitempty"`
    Name         string                   `json:"name"`
    Description  string                   `json:"description"`
    WhenToUse    []string                 `json:"when_to_use,omitempty"`
    InputSchema  map[string]any           `json:"input_schema"`
    OutputSchema map[string]any           `json:"output_schema,omitempty"`
    RiskLevel    contracts.RiskLevel      `json:"risk_level,omitempty"`
    Visibility   contracts.ToolVisibility `json:"visibility,omitempty"`
    Version      string                   `json:"version,omitempty"`
}
```

不要复用 `toolcatalog.decodeProviderCatalog()` 解析完整插件 manifest。当前 tool catalog decoder 只保证能取到 tools，用它来承载 agent metadata / hooks / collaborators 会让 schema 语义变虚。

### 9.3 运行时解析流程

在 `Coordinator.step()` 开始处：

```text
policySet := load policy
effective := strategy.Resolve(activeDefinition, policySet, serviceDefaults)
candidates := retrieve candidates using effective.Tools/effective.Skills
context := collect context using effective.Context
prompt := build prompt using effective.Prompt/effective.Output
prompt := compress using effective.Context.Compression
decision := model call using effective.Model
dispatch using effective.Tools/effective.Collaboration/effective.Memory
```

新增包：

- `internal/agentdef/strategy`

职责：

- resolve
- validate
- apply guardrails
- produce report

不要让 `Coordinator` 自己到处合并字段。

### 9.4 AgentPlugin Service 同步流程

推荐把插件服务同步拆成两个动作，不要和运行时临时调用混在一起：

```text
agent.plugin.sync
  -> 读取 ToolProvider(provider_type=agent_plugin_service)
  -> 从 ToolProvider.ServiceConnectionID 读取 ServiceConnection
  -> GET /.well-known/agent-plugin.json
  -> validate AgentPluginManifest
  -> upsert ToolProvider / ToolManifest / RuntimeHookProvider facts
  -> upsert AgentPluginSource draft 或更新 draft 可用能力快照
  -> 如果没有完整 manifest，再退化为 /tools/catalog 仅同步 ToolManifest，不生成 AgentPluginSource
```

```text
agent.package.publish
  -> compile AgentPluginSource
  -> publish AgentPackageVersion / AgentDefinition
  -> run eval / canary / stable
```

运行时调用仍走现有治理路径：

```text
model decision
  -> tool runtime policy / approval
  -> ToolHostExecutor
  -> AgentPlugin Service /tools/invoke
  -> result/artifact refs
  -> CleanCore trace / diagnostics / memory policy
```

不要让 AgentPlugin Service 在 `/tools/invoke` 返回“请直接更新 run 状态”之类的控制指令。它只能返回工具输出和 artifact refs；状态推进由 `Coordinator` 决定。

`RuntimeHookProvider` 需要和 `ToolProvider` 一样支持 `service_connection_id`：

```go
type Provider struct {
    ProviderID          string
    ProviderType        ProviderType
    ServiceConnectionID string
    Endpoint            string // 可选，仅用于 go/static 快速验证或无连接场景
}
```

执行 `static_hook_host` 时优先使用 `ServiceConnection` 的 base URL、auth_ref、timeout、retry、network_scope；这样插件 Hook、工具调用、健康检查都走同一套服务连接治理。

## 10. 需要新增的核心包

### 10.1 `internal/agentdef/strategy`

职责：

- 默认值。
- Agent strategy + Policy guardrails 合并。
- 生成 `EffectiveRunConfig`。
- 生成 strategy hash。

文件：

- `types.go`
- `defaults.go`
- `resolver.go`
- `validator.go`
- `report.go`

### 10.2 `internal/context/collector`

职责：

- 根据 `EffectiveRunConfig.Context` 收集上下文。
- 输出 `WorkView` 输入和 assembly report。

替换 Coordinator 中分散的：

- `toolSummaries`
- `taskHistory`
- `memorySummaries`
- `artifactRefs`
- `conversationContext`

不要求一次全部搬完，可以先将 limit 参数下沉。

### 10.3 `internal/context/compressor`

职责：

- local truncate
- LLM compression
- compression report

详见 `docs/context_strategy_compression_refactor_plan_20260612.md`。

### 10.4 `internal/model/router`

可选，低优先级。

如果做 Agent-level model strategy，建议新增 model router。

职责：

- 根据 provider/model alias 构造 model client。
- 受 ModelGuardrails 限制。
- 记录实际模型。

短期可先不建包，先在 core/coordinator 内部做小函数。

### 10.5 `internal/agentdef/plugin`

职责：

- 读取 `AgentPluginSource`。
- 通过 `ToolProvider.ProviderID` 找到 `ToolProvider`，再通过 `ToolProvider.ServiceConnectionID` 拉取插件 manifest。
- 校验 `provider_type == agent_plugin_service`。
- 新增并解析 `AgentPluginManifest`，不要复用 tool catalog decoder 承载完整插件语义。
- 将插件 manifest 归一化为 Agent source 能力快照。
- 输出可交给 compiler 的 `AgentPluginSource` 等价结构。
- 生成 manifest hash 和同步 report。

不要把工具执行逻辑放进这个包。工具执行继续由 `internal/tool/catalog` 的 `ToolHostExecutor` 负责，内部 Agent exported tool 继续由 `internal/tool/agenttool` 负责。

同时需要改造 `internal/runtime/hook`：

- `runtimehook.Provider` 增加 `ServiceConnectionID`。
- `static_hook_host` 可以通过 service connection 调用 `/runtime-hooks/invoke`。
- provider health/catalog 优先使用 service connection。
- provider endpoint 只保留为开发期快速验证字段，不作为 AgentPlugin Service 的主连接事实。

## 11. PolicySet 重构建议

当前 PolicySet 字段过多，但还能工作。开发阶段建议直接重命名并收敛语义：

### 11.1 RuntimeGuardrails

替代：

- `RuntimePolicy`

```go
type RuntimeGuardrails struct {
    MaxSteps int `json:"max_steps,omitempty"`
    MaxToolCalls int `json:"max_tool_calls,omitempty"`
    MaxModelRetries int `json:"max_model_retries,omitempty"`
    MaxRepairAttempts int `json:"max_repair_attempts,omitempty"`
    MaxConsecutiveToolFailures int `json:"max_consecutive_tool_failures,omitempty"`
    MaxDurationSeconds int `json:"max_duration_seconds,omitempty"`
}
```

### 11.2 ToolGuardrails

替代：

- `ToolPolicy`

保留 allowed/denied/approval risk。

### 11.3 PromptGuardrails

替代：

- `PromptPolicy`
- `ContextGovernancePolicy` 中的压缩治理字段

拆成：

- prompt hard limit
- blocked phrases
- compression allowed models

### 11.4 CollaborationGuardrails

替代：

- `HandoffPolicy`

保留：

- allow full context
- max context tokens
- require approval
- allow parent task query
- allow artifact/memory/task event read

### 11.5 Memory/ArtifactGuardrails

替代：

- `MemoryPolicy`
- `ArtifactPolicy`

语义保持。

### 11.6 ReleasePolicy 保持

发布治理已经独立，保留。

## 12. Server 命令调整

### 12.1 Agent package commands

当前已有：

- draft create
- patch prompt
- patch system/developer prompt
- tool binding update
- runtime hooks update
- collaborator add/update/remove
- exported tool add/update/remove
- skill add/update/remove
- validate/review/publish/canary/stable/rollback

建议新增一个统一 patch：

```text
agent.package.draft.patch_strategies
```

payload：

```json
{
  "draft_id": "draft_xxx",
  "strategies": {
    "model": {},
    "context": {},
    "tools": {},
    "runtime": {}
  }
}
```

不要为每个策略都加一个命令，避免命令数量爆炸。

开发阶段不做旧 API 兼容，重构后建议直接删除或替换这些细粒度命令：

- `agent.package.tool_binding.update`
- `agent.package.runtime_hooks.update`
- prompt patch 系列

调用方统一改到 `agent.package.draft.patch_strategies` 或 draft create 的结构化 source payload。

插件式创建只建议新增同步命令：

```text
agent.plugin.sync
```

Agent draft / publish 继续复用 package 主链路，只在 payload 增加：

```json
{
  "source_kind": "plugin_service",
  "plugin": {
    "provider_id": "crm-plugin"
  }
}
```

不新增 `agent.plugin.publish`、`agent.plugin.draft.patch_strategies` 等第二套 release API，避免发布、eval、canary、rollback 双轨。

### 12.2 Policy commands

现有 policy draft create/update/validate/review/publish 可以继续。

只需要更新 schema：

- `policyFromPayload`
- validation
- OpenAPI

### 12.3 Preview commands

`prompt.preview` 应变成策略验证入口之一。

返回增加：

- effective strategies
- guardrail adjustments
- context assembly report
- compression report
- selected model
- selected tools/skills/collaborators

不要只返回 PromptBundle。

## 13. Trace 和 Diagnostics

新增 trace event：

```text
strategy.resolved
strategy.guardrail_applied
context.collection.completed
context.compression.completed
model.strategy.selected
tool.strategy.applied
collaboration.strategy.applied
memory.strategy.applied
agent.plugin.synced
agent.plugin.manifest_compiled
```

payload 原则：

- 记录 hash、数量、结果。
- 不记录完整 prompt 明文。
- 不记录完整上下文明文。
- 不记录密钥。

run diagnostics 增加：

```json
{
  "strategy": {
    "source_kind": "plugin_service",
    "provider_id": "crm-plugin",
    "service_connection_id": "crm-plugin-connection",
    "manifest_hash": "",
    "strategy_hash": "",
    "policy_hash": "",
    "effective": {},
    "guardrail_adjustments": []
  }
}
```

这里的 `service_connection_id` 是从 `ToolProvider` 解析出的运行事实，不是 `AgentPluginSource` 自己保存的字段。

## 14. 实施计划

### Phase 1：策略契约整理

1. 新增 `contracts.AgentStrategies` 和各策略结构。
2. 修改 `AgentDefinition` 增加 `Strategies`。
3. 修改 `AgentPackageSource` 增加 `Strategies`。
4. 新增轻量 `AgentPluginSource` 和 `AgentSourceKind`。
5. 将 compiler 中 metadata runtime/prompt/skills 的读取直接迁移到结构化字段。
6. 保留 compiled 平铺字段仅作为 runtime 内部读取便利，不作为旧 API 或旧数据兼容入口。

验收：

- package compile 可以从 `strategies` 编译 AgentDefinition。
- plugin source 可以校验 provider/service connection 并生成同形 AgentDefinition。
- 旧 metadata 逻辑可直接删除或仅保留 name/description/policy_set_id。

### Phase 2：AgentPlugin manifest sync

1. 新增 `internal/agentdef/plugin`。
2. 复用 `ToolProvider.provider_type=agent_plugin_service` 和 `ServiceConnection`。
3. 从 `/.well-known/agent-plugin.json` 读取完整 `AgentPluginManifest`；`/tools/catalog` 只作为工具目录 fallback。
4. 同步插件工具到 `ToolManifest`。
5. 改造 runtime hook provider 支持 `service_connection_id`，同步插件 Hook 声明到 runtime hook provider/binding 候选。
6. 记录 `agent.plugin.synced` trace/audit。

验收：

- 插件服务可以通过已有 service connection 接入。
- 只有完整 `AgentPluginManifest` 可以生成/更新 `AgentPluginSource`；`/tools/catalog` fallback 只同步工具。
- provider unhealthy / connection disabled 时不能发布可运行 Agent。
- manifest hash 可进入 release report。

### Phase 3：Effective strategy resolver

1. 新增 `internal/agentdef/strategy`。
2. 实现 default values。
3. 实现 Agent strategy + Policy guardrails 合并。
4. 输出 `EffectiveRunConfig`。
5. 在 `Coordinator.step()` 开始处解析 effective config。

验收：

- policy 上限能压低 Agent 配置。
- strategy hash 可稳定生成。
- trace 记录 `strategy.resolved`。

### Phase 4：运行链路接入

按优先级接入：

1. RuntimeStrategy：max steps/tool calls/retries。
2. ToolUseStrategy：allowed/denied/preferred/tool_choice_mode。
3. ContextStrategy：移除 20/8/30 硬编码。
4. ModelStrategy：选择模型 alias。
5. RepairStrategy：修复次数和失败策略。
6. CollaborationStrategy：handoff limits。
7. MemoryUseStrategy：memory read/write enable 和 max items。

验收：

- 每个策略至少有一个单元测试证明生效。
- prompt.preview 与实际 run 使用同一 resolver。

### Phase 5：Context + compression 完整落地

按已有文档实施：

- `docs/context_strategy_compression_refactor_plan_20260612.md`

验收：

- LLM compression 可配置。
- 压缩失败可 fallback。
- diagnostics 可解释压缩。

### Phase 6：命令和 OpenAPI 清理

1. 新增 `agent.package.draft.patch_strategies`。
2. 更新 draft create 支持 strategies。
3. 更新 draft create 支持 `source_kind=plugin_service`。
4. 新增 `agent.plugin.sync`。
5. 更新 policy schema。
6. 更新 `prompt.preview` 返回 effective strategies。
7. 更新 OpenAPI。

验收：

- 服务端测试覆盖 draft create/patch/preview/publish/run。

### Phase 7：Eval 与诊断

1. eval case 增加 strategy assertions。
2. run diagnostics 展示 effective strategy。
3. run diagnostics 展示 source kind、provider id、service connection id、manifest hash。
4. release report 包含 strategy hash 和 manifest hash。

验收：

- 同一个 Agent 不同 strategy 版本可以通过 eval 区分效果。
- run 失败时能看出是否由于策略限制导致。
- 插件服务版本变化可以通过 manifest hash 追溯。

## 15. 不做事项

本次不做：

1. 前端配置页面。
2. 通用 DSL 策略引擎。
3. 复杂模型路由市场。
4. 多租户 UI 权限模型。
5. 旧数据迁移或旧 API 兼容适配。
6. 自动从自然语言生成策略。
7. 压缩摘要长期记忆自动写入。
8. 向量检索重构。
9. AgentPlugin Service 部署平台或插件市场。
10. 允许插件服务直接写 CleanCore Task / Run / Memory。
11. 让插件服务绕过 ToolRuntime / Policy / Approval。

## 16. 推荐优先级

最高优先级：

1. AgentStrategies 契约。
2. Effective strategy resolver。
3. Agent source kind + AgentPluginSource 轻量契约。
4. ContextStrategy 和 CompressionStrategy。
5. ToolUseStrategy。
6. RuntimeStrategy。
7. prompt.preview 策略报告。

中优先级：

1. ModelStrategy。
2. RepairStrategy。
3. CollaborationStrategy。
4. MemoryUseStrategy。
5. AgentPlugin manifest sync 与 release report。

低优先级：

1. SkillUseStrategy。
2. KnowledgeUseStrategy 自动注入。
3. OutputStrategy 深度定制。

## 17. 最终完成标准

完成后应满足：

1. 智能体创建者可以在 Agent package 中配置主要 Agent 行为策略。
2. PolicySet 明确作为 guardrails，而不是混合承载 Agent 偏好。
3. runtime kernel 不再散落大量硬编码行为策略。
4. `prompt.preview` 能解释 effective strategy、上下文、工具、模型、压缩。
5. run diagnostics 能追踪策略如何影响本次运行。
6. Agent release/eval/canary/rollback 都包含策略版本和 hash。
7. 原生包 Agent 和插件式 Agent 共用同一套 runtime、policy、approval、trace、diagnostics。
8. AgentPlugin Service 只能作为受控能力来源，不能直接写 Task / Run / Memory，不能绕过 Policy / Approval。
9. 核心服务模块可测试，不依赖前端。
