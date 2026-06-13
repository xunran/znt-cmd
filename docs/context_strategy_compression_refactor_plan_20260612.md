# 上下文策略与模型压缩重构开发计划

日期：2026-06-12

## 1. 背景与目标

本项目的核心用户是智能体创建者，不是最终聊天用户。因此，上下文加载、上下文压缩、压缩提示词、压缩模型和上下文预算都应该成为 Agent 创建与发布能力的一部分，而不是隐藏在服务端代码里的固定策略。

本次重构目标：

1. 将上下文加载策略从硬编码参数升级为 Agent 级可配置能力。
2. 将当前的字符串截断式压缩升级为可选的大模型上下文压缩。
3. 让压缩提示词可配置、可版本化、可预览、可评估。
4. 让 `prompt.preview` 和 run diagnostics 能说明上下文如何被加载、压缩、丢弃。
5. 本阶段不考虑前端，不考虑旧数据兼容，不保留旧表结构迁移兜底。

## 2. 当前代码事实

### 2.1 上下文组装链路

当前主链路在 `internal/runtime/kernel/coordinator.go`：

1. `step()` 收集上下文：
   - `toolSummaries(ctx, runID)`
   - `taskHistory(ctx, tenantID, taskID, runID, events)`
   - `memorySummaries(ctx, tenantID, agentID, userID)`
   - `artifactRefs(ctx, runID)`
   - `conversationContext(...)`
2. `WorkView.Build()` 将这些内容装入 `contracts.WorkView`。
3. `PromptBundle.Build()` 将 `WorkView` 渲染为 `PromptBundle.Context`。
4. `applyPromptPolicy()` 检查 prompt policy，并在超限时截断 `bundle.Context`。

关键文件：

- `internal/runtime/kernel/coordinator.go`
- `internal/runtime/kernel/conversation_context.go`
- `internal/context/workview/builder.go`
- `internal/context/promptbundle/builder.go`
- `internal/context/conversation/engine.go`
- `internal/contracts/view_prompt.go`
- `internal/contracts/policy.go`

### 2.2 上下文窗口限制的重构约束

此前代码里存在多处固定窗口，例如最近会话消息 20 条、会话检索默认 8 条、task history 最后保留 30 条，以及 `conversation_max_retrieved` 这种单点配置。

重构后的约束是：

- `BasicRetriever.Retrieve()` 不再自带默认 Top-K；`query.MaxResults=0` 表示不在 retriever 层限制。
- `BuildRetrievalQuery()` 不再写死 `MaxResults: 8`；需要限制时由 `BuildRetrievalQueryWithLimit(...)` 或上层 strategy 注入。
- `conversationContext(...)`、`storedConversationMessages(...)`、`normalizeRecentMessages(...)` 统一读取 `ContextStrategy.RecentMessageLimit`。
- task history、memory summary、artifact refs、tool results 都应有独立 limit，并且 `0` 表示该来源不限制。
- 服务级 config 只提供默认策略值，例如 `context_default_retrieval_max_results=8`，不作为不可覆盖的 runtime 硬编码。
- 旧的 `conversation_max_retrieved` 不再作为主配置入口，开发阶段直接替换为 `context_default_retrieval_max_results` 和 Agent 级 `strategies.context.retrieval_max_results`。

### 2.3 当前压缩能力

当前契约已经从旧 `contracts.ContextCompressionPolicy` 迁到：

- `AgentStrategies.Context.Compression`：表达创建者希望如何压缩上下文。
- `PolicySet.ContextGovernancePolicy`：表达平台允许的上下文预算、full debug、LLM compression 和压缩模型范围。
- `internal/context/compressor`：承载本地 truncate、LLM compression、`llm_then_truncate` fallback 和 `ContextCompressionReport`。

实际入口仍在 `applyPromptPolicy()` 附近，但语义应拆清：

1. `PromptPolicy` 只保留 hard prompt limit 和 blocked phrases。
2. `ContextStrategy.Compression` 决定是否触发 truncate/LLM compression。
3. `ContextGovernancePolicy` 限制创建者的压缩模型、base URL、预算和 full debug。
4. 压缩结果必须回写 `PromptBundle.ContextAssemblyReport.Compression`，并在 trace/diagnostics 中只记录 hash、计数和 source refs，不记录压缩前明文。

这让创建者可以表达“压缩时保留工具结果、保留未解决问题、保留 source refs、禁止生成新事实”等策略，同时仍受平台治理约束。

### 2.4 Runtime Hook 的关系

当前 runtime hook 已支持：

- `before_context_build`
- `after_candidate_retrieval`
- `before_model_call`
- `before_memory_write`

Hook 可以 `add_context_blocks`、`drop_context_refs`，但它不是内建上下文压缩策略。模型压缩应该成为核心服务内建能力，hook 可作为扩展点，而不是唯一方案。

### 2.5 AgentPlugin Service 与上下文的关系

项目理念上，智能体创建者可以通过 AgentPlugin Service 以插件方式开发智能体。结合当前代码，它和上下文策略的关系应该是：

- `agent_plugin_service` 目前是 `ToolProvider.provider_type`，通过 `ServiceConnection` 绑定外部服务。
- 插件服务可以通过 `/tools/catalog` 声明工具，通过 `/tools/invoke` 返回工具结果。
- 插件服务也可以通过 runtime hook，尤其是 `before_context_build`、`after_candidate_retrieval`、`before_model_call`，返回 `add_context_blocks`、`drop_context_refs`、`planner_hints`。
- 插件服务返回的上下文块必须进入 CleanCore 的 `ContextStrategy` 预算、来源标记、压缩和 diagnostics，而不能绕过统一上下文装配。
- 插件服务不能直接写 CleanCore Memory，也不能直接修改 Task / Run 状态。

因此，插件服务是“上下文贡献者”和“能力实现者”，不是上下文最终裁决者。最终加载多少、是否压缩、如何压缩、哪些块被丢弃，仍由 CleanCore 的 effective context strategy 决定。

## 3. 目标架构

### 3.1 新增核心概念

新增一等配置：`AgentStrategies.Context`。

建议放入 `contracts.AgentDefinition.Strategies`，作为 Agent 包的一部分：

```go
type AgentDefinition struct {
    // existing fields...
    Strategies AgentStrategies `json:"strategies,omitempty"`
}

type AgentStrategies struct {
    Context ContextStrategy `json:"context,omitempty"`
}
```

同时保留治理层限制在 `PolicySet` 中，用来约束创建者可用范围：

```go
type PolicySet struct {
    // existing fields...
    ContextGovernancePolicy ContextGovernancePolicy `json:"context_governance_policy,omitempty"`
}
```

Agent 的 `Strategies.Context` 表达“这个智能体希望如何使用上下文”。Policy 的 `ContextGovernancePolicy` 表达“平台允许这个智能体最多怎么用上下文”。

开发阶段不保留 `AgentDefinition.ContextStrategy` 顶层字段，避免 `Strategies.Context` 和 `ContextStrategy` 双事实源。

### 3.2 ContextStrategy 草案

```go
type ContextStrategy struct {
    Mode string `json:"mode,omitempty"` // auto, concise, balanced, long_context, full_debug

    RecentMessageLimit    int `json:"recent_message_limit,omitempty"`
    RetrievalMaxResults   int `json:"retrieval_max_results,omitempty"`
    TaskHistoryMaxItems   int `json:"task_history_max_items,omitempty"`
    MemoryMaxItems        int `json:"memory_max_items,omitempty"`
    ArtifactRefMaxItems   int `json:"artifact_ref_max_items,omitempty"`
    ToolResultMaxItems    int `json:"tool_result_max_items,omitempty"`
    ContextTokenBudget    int `json:"context_token_budget,omitempty"`

    EnabledSources []string `json:"enabled_sources,omitempty"`
    SourceBudgets  map[string]int `json:"source_budgets,omitempty"`

    Compression ContextCompressionStrategy `json:"compression,omitempty"`
}
```

约定：

- `0` 表示该层不限制。
- 空数组表示使用默认来源。
- `mode` 用来快速套用默认值，高级字段可以覆盖 mode 默认值。

建议内置来源 ID：

```text
conversation_recent
conversation_retrieval
task_history
memory_summary
artifact_refs
tool_results
runtime_hook_context
agent_plugin_context
```

其中 `runtime_hook_context` / `agent_plugin_context` 来自外部 Hook 或插件服务返回的上下文块。它们必须带 source ref、provider id、hook id 或 tool call id，且默认视为 untrusted，不允许覆盖 system/developer prompt。

### 3.3 ContextCompressionStrategy 草案

```go
type ContextCompressionStrategy struct {
    Enabled bool `json:"enabled"`

    TriggerRatio int `json:"trigger_ratio,omitempty"` // 80 表示达到预算 80% 时触发
    TargetTokens int `json:"target_tokens,omitempty"`

    Mode string `json:"mode,omitempty"` // none, truncate, llm, llm_then_truncate

    ModelProvider string `json:"model_provider,omitempty"`
    ModelBaseURL  string `json:"model_base_url,omitempty"`
    ModelName     string `json:"model_name,omitempty"`
    MaxTokens     int    `json:"max_tokens,omitempty"`
    Temperature   *float64 `json:"temperature,omitempty"`

    PromptProfileID string `json:"prompt_profile_id,omitempty"`
    InlinePrompt    *CompressionPromptProfile `json:"inline_prompt,omitempty"`

    Preserve []string `json:"preserve,omitempty"`
    Forbid   []string `json:"forbid,omitempty"`

    WriteSummaryToMemory bool `json:"write_summary_to_memory,omitempty"`
}
```

### 3.4 CompressionPromptProfile 草案

压缩提示词需要作为可版本化资源。

```go
type CompressionPromptProfile struct {
    ProfileID string `json:"profile_id"`
    Version   string `json:"version,omitempty"`
    Name      string `json:"name,omitempty"`

    SystemPrompt    string `json:"system_prompt"`
    DeveloperPrompt string `json:"developer_prompt,omitempty"`

    OutputSchema map[string]any `json:"output_schema,omitempty"`

    Preserve []string `json:"preserve,omitempty"`
    Forbid   []string `json:"forbid,omitempty"`
}
```

建议默认内置一个 `context.compression.factual_v1`：

- 只做事实保真摘要。
- 不新增事实。
- 保留来源引用。
- 明确标注未解决问题。
- 把用户文本、历史消息、工具输出都视为不可信上下文。
- 输出结构化 JSON，方便验证。

### 3.5 AgentPlugin Service 上下文边界

插件服务参与上下文时，只允许走两条受控路径：

```text
Tool result path
  AgentPlugin Service /tools/invoke
  -> ToolResult
  -> toolSummaries / context collector
  -> WorkView
  -> PromptBundle
```

```text
Runtime hook path
  AgentPlugin Service 或 HookHost /runtime-hooks/invoke
  -> add_context_blocks / drop_context_refs / planner_hints
  -> context collector / prompt bundle patch
  -> compression
```

不要新增“插件服务直接返回最终 PromptBundle”的路径。原因：

- CleanCore 必须统一做 token budget、source budget、policy guardrails。
- CleanCore 必须统一做 prompt injection 标记和压缩审计。
- CleanCore 必须统一记录 trace、diagnostics、artifact refs。
- 插件服务可能接触私有系统和凭证，不能让其输出绕过脱敏、审批和运行策略。

如果插件服务希望强烈影响上下文排序，应通过 `planner_hints` 或 `ToolRankAdjustment` 表达偏好，再由 CleanCore 应用。

## 4. 数据契约重构

### 4.1 contracts

新增文件：

- `internal/contracts/context_strategy.go`

新增类型：

- `ContextStrategy`
- `ContextSourceBudget`
- `ContextCompressionStrategy`
- `CompressionPromptProfile`
- `ContextAssemblyReport`
- `ContextSourceReport`
- `ContextCompressionReport`
- `CompressedContext`

建议 `ContextAssemblyReport` 结构：

```go
type ContextAssemblyReport struct {
    StrategyHash string `json:"strategy_hash,omitempty"`
    Mode         string `json:"mode,omitempty"`

    Sources []ContextSourceReport `json:"sources,omitempty"`

    TokenBudget       int `json:"token_budget,omitempty"`
    EstimatedTokensIn int `json:"estimated_tokens_in,omitempty"`
    EstimatedTokensOut int `json:"estimated_tokens_out,omitempty"`

    Compression *ContextCompressionReport `json:"compression,omitempty"`
}

type ContextSourceReport struct {
    SourceType     string `json:"source_type"`
    SourceRef      string `json:"source_ref,omitempty"`
    ProviderID     string `json:"provider_id,omitempty"`
    HookID         string `json:"hook_id,omitempty"`
    ToolCallID     string `json:"tool_call_id,omitempty"`
    TrustLevel     string `json:"trust_level,omitempty"` // trusted, untrusted_external_context
    CandidateCount int    `json:"candidate_count"`
    SelectedCount  int    `json:"selected_count"`
    DroppedCount   int    `json:"dropped_count"`
    Limit          int    `json:"limit,omitempty"`
    Reason         string `json:"reason,omitempty"`
}

type ContextCompressionReport struct {
    Applied         bool   `json:"applied"`
    Mode            string `json:"mode,omitempty"`
    ModelProvider   string `json:"model_provider,omitempty"`
    ModelName       string `json:"model_name,omitempty"`
    PromptProfileID string `json:"prompt_profile_id,omitempty"`
    InputTokens     int    `json:"input_tokens,omitempty"`
    OutputTokens    int    `json:"output_tokens,omitempty"`
    SummaryHash     string `json:"summary_hash,omitempty"`
    FailureReason   string `json:"failure_reason,omitempty"`
}
```

### 4.2 AgentDefinition

修改：

- `internal/contracts/agent.go`

新增总策略字段：

```go
Strategies AgentStrategies `json:"strategies,omitempty"`
```

其中上下文策略位于：

```go
Strategies.Context ContextStrategy `json:"context,omitempty"`
```

不要再新增顶层 `context_strategy` 字段。

### 4.3 PolicySet

修改：

- `internal/contracts/policy.go`

旧 `CompressionPolicy ContextCompressionPolicy` 不再保留；开发阶段直接使用：

```go
ContextGovernancePolicy ContextGovernancePolicy `json:"context_governance_policy,omitempty"`
```

治理策略示例：

```go
type ContextGovernancePolicy struct {
    MaxContextTokenBudget int `json:"max_context_token_budget,omitempty"`
    MaxRecentMessageLimit int `json:"max_recent_message_limit,omitempty"`
    MaxRetrievalResults   int `json:"max_retrieval_results,omitempty"`
    MaxTaskHistoryItems   int `json:"max_task_history_items,omitempty"`
    MaxMemoryItems        int `json:"max_memory_items,omitempty"`
    MaxArtifactRefItems   int `json:"max_artifact_ref_items,omitempty"`
    MaxToolResultItems    int `json:"max_tool_result_items,omitempty"`

    AllowFullDebugMode bool `json:"allow_full_debug_mode,omitempty"`
    AllowLLMCompression bool `json:"allow_llm_compression"`
    AllowedCompressionModels []string `json:"allowed_compression_models,omitempty"`
}
```

### 4.4 PromptBundle / WorkView

修改：

- `internal/contracts/view_prompt.go`

建议：

```go
type WorkView struct {
    // existing fields...
    ContextAssemblyReport *ContextAssemblyReport `json:"context_assembly_report,omitempty"`
}

type PromptBundle struct {
    // existing fields...
    ContextAssemblyReport *ContextAssemblyReport `json:"context_assembly_report,omitempty"`
}
```

`PromptBundle.Hash` 应包含压缩后的 context 和压缩策略 hash，但不需要包含压缩前明文。

## 5. 核心服务模块拆分

### 5.1 新增 context strategy 包

新增目录：

- `internal/context/strategy`

职责：

1. 合并 Agent strategy、Policy governance、服务默认值。
2. 套用 mode 默认配置。
3. 校验创建者配置。
4. 输出 `EffectiveContextStrategy`。

建议文件：

- `resolver.go`
- `defaults.go`
- `validator.go`
- `budget.go`

核心接口：

```go
type Resolver struct{}

func (Resolver) Resolve(agent contracts.AgentDefinition, policy contracts.PolicySet, cfg Defaults) (contracts.ContextStrategy, error)
```

### 5.2 新增 context collector

新增或重构：

- `internal/context/workview`
- 或新增 `internal/context/collector`

职责：

1. 替代 `Coordinator.step()` 中分散的上下文收集逻辑。
2. 根据 `ContextStrategy` 控制每类来源的数量和预算。
3. 生成 `ContextAssemblyReport`。
4. 接收 runtime hook / AgentPlugin Service 贡献的上下文块，并按统一来源预算处理。

建议接口：

```go
type Collector struct {
    Runs runrepo.Repository
    Tasks *taskruntime.Service
    TaskRepo taskrepo.TaskRepository
    ToolRepo toolrepo.Repository
    Memory artifact.MemoryStore
    ConversationStore conversation.Store
}

func (c Collector) Collect(ctx context.Context, input CollectInput) (CollectResult, error)
```

`CollectResult` 包含：

- `TaskHistory`
- `Memory`
- `Artifacts`
- `ToolResults`
- `Conversation`
- `RuntimeHookContext`
- `AgentPluginContext`
- `Report`

插件或 Hook 注入的上下文块不要直接拼接进 prompt。先进入 collector 的候选集合，再根据 `EnabledSources`、`SourceBudgets`、`ContextTokenBudget` 和 policy guardrails 选择。

### 5.3 改造 conversation context

此前 `conversation_context.go` 直接写死 20 和 `ConversationMaxRetrieved`。重构后这两个语义必须全部收口到 `ContextStrategy` 和服务默认策略里。

改造目标：

1. `conversationContext(...)` 增加 strategy 参数。
2. `storedConversationMessages(...)` 使用 `strategy.RecentMessageLimit`。
3. `buildConversationContext(...)` 使用 `strategy.RecentMessageLimit`。
4. `BuildRetrievalQuery(...)` 不再写死 `MaxResults: 8`，由 strategy 注入。
5. `BasicRetriever.Retrieve()` 接受默认 limit 或从 query 中读取。

建议改签名：

```go
func (c Coordinator) conversationContext(
    ctx context.Context,
    envelope contracts.AgentEnvelope,
    definition contracts.AgentDefinition,
    strategy contracts.ContextStrategy,
    runID contracts.AgentRunID,
    taskID contracts.TaskID,
    events []contracts.TaskEvent,
    memories []contracts.MemorySummary,
    artifacts []contracts.ArtifactRef,
    tools []contracts.ToolResultSummary,
    userInput string,
) *contracts.ConversationContext
```

### 5.4 改造 taskHistory / memory / artifact / tool summaries

当前：

- `taskHistory()` 内部固定最多 30。
- `memorySummaries()` 返回全部。
- `artifactRefs()` 返回当前 run 全部 refs。
- `toolSummaries()` 返回当前 run 全部 summaries。

改造：

```go
func (c Coordinator) taskHistory(..., limit int) []contracts.RetrievedContext
func (c Coordinator) memorySummaries(..., limit int) []contracts.MemorySummary
func (c Coordinator) artifactRefs(..., limit int) []contracts.ArtifactRef
func (c Coordinator) toolSummaries(..., limit int) []contracts.ToolResultSummary
```

约定 `limit=0` 表示不限制。

Postgres 层建议同步支持 limit，避免先查全量再截断：

- `MemoryStore.ListMemory(..., limit int)`
- `ToolRepository.ListResultsByRun(..., limit int)` 可作为新增方法，或在 collector 层截断。

开发阶段不考虑兼容，可以直接改接口。

## 6. 模型压缩设计

### 6.1 新增 compressor 包

新增目录：

- `internal/context/compressor`

职责：

1. 判断是否触发压缩。
2. 构建压缩 PromptBundle。
3. 调用压缩模型。
4. 验证压缩结果 JSON。
5. 回写压缩后的 `bundle.Context`。
6. 生成 `ContextCompressionReport`。
7. 模型压缩失败时按策略 fallback。
8. 保留 conversation/tool/hook/plugin 等 source refs，且把外部插件返回内容作为 untrusted context。

建议接口：

```go
type Compressor interface {
    Compress(ctx context.Context, request Request) (Result, error)
}

type Request struct {
    Agent contracts.AgentDefinition
    Policy contracts.PolicySet
    Strategy contracts.ContextStrategy
    PromptBundle contracts.PromptBundle
    WorkView contracts.WorkView
}

type Result struct {
    PromptBundle contracts.PromptBundle
    Report contracts.ContextCompressionReport
}
```

### 6.2 压缩触发点

当前 `applyPromptPolicy()` 同时做 blocked phrase、token limit、截断压缩。

建议拆分：

1. `applyPromptSafetyPolicy()`
2. `applyContextCompression()`
3. `applyPromptLimitPolicy()`

执行顺序：

```text
PromptBundle.Build
-> before_model_call runtime hook
-> blocked phrase check
-> context compression trigger
-> hard token limit final guard
-> rehash
-> model call
```

说明：

- compression 应在 `before_model_call` hook 后执行，因为 hook 可能新增上下文。
- hard token limit final guard 必须保留，避免压缩失败导致模型上下文过大。
- 如果 `compression.mode=llm_then_truncate`，模型压缩失败后走安全截断。
- 如果 `compression.mode=llm` 且失败策略为 reject，则返回 runtime error。

### 6.3 压缩模型选择

建议复用 `internal/model/client` 的 `ModelClient` 接口。

新增：

- `CompressionModelFactory`

逻辑：

1. 如果 `ContextCompressionStrategy` 指定 `model_provider/model_name/base_url`，构造单独压缩模型 client。
2. 否则复用主模型 client。
3. Policy governance 校验是否允许该模型。

注意：

- `model_max_tokens` 是输出上限，不是上下文预算。
- 压缩模型的 `max_tokens` 应来自 `compression.max_tokens` 或 `compression.target_tokens`。

### 6.4 压缩输出结构

建议要求模型返回 JSON：

```json
{
  "summary": "...",
  "preserved_facts": [],
  "open_questions": [],
  "tool_results": [],
  "source_refs": [],
  "dropped": [
    {"source_ref": "message:xxx", "reason": "low_relevance"}
  ],
  "warnings": []
}
```

渲染回 `PromptBundle.Context` 时使用明确边界：

```text
<compressed context>
...
</compressed context>

<compression metadata>
strategy_hash=...
prompt_profile_id=...
source_refs=...
</compression metadata>
```

不要把压缩前原文存入 trace。

## 7. Prompt Profile 管理

### 7.1 存储

开发阶段可先内置默认 profile，并支持 Agent 包内 inline prompt。

后续可新增表：

- `compression_prompt_profiles`

字段：

- `profile_id`
- `tenant_id`
- `version`
- `name`
- `system_prompt`
- `developer_prompt`
- `output_schema_json`
- `status`
- `created_by`
- `created_at`

本阶段如果要快速落地，可以先不做独立表，只支持：

1. 内置 profile。
2. `AgentDefinition.Strategies.Context.Compression.InlinePrompt`。

### 7.2 Agent package 编译

修改：

- `internal/agentdef/package/compiler.go`
- `internal/agentdef/package/service.go`

在 package source 中支持：

```yaml
strategies:
  context:
    mode: long_context
    recent_message_limit: 40
    retrieval_max_results: 16
    context_token_budget: 12000
    compression:
      enabled: true
      mode: llm_then_truncate
      target_tokens: 4000
      prompt_profile_id: context.compression.factual_v1
```

编译时写入 `AgentDefinition.Strategies.Context`。

## 8. API 与命令改造

本阶段不做前端，但核心服务 API 需要具备能力。

### 8.1 prompt.preview

修改：

- `internal/server/commands_prompt.go`
- `Coordinator.PreviewPromptBundle`

返回增加：

```json
{
  "effective_strategies": {
    "context": {}
  },
  "context_assembly_report": {},
  "compression_report": {}
}
```

`prompt.preview` 应支持 draft 中的 `strategies.context`。

### 8.2 agent package draft

使用统一策略 patch 命令：

- `agent.package.draft.patch_strategies`

payload：

```json
{
  "draft_id": "xxx",
  "strategies": {
    "context": {}
  }
}
```

也可以直接扩展 `agent.package.draft.create`，允许创建时传入 `strategies.context`。

### 8.3 diagnostics

修改：

- `internal/server/handlers_runs.go`

Run diagnostics 增加：

```json
{
  "context": {
    "strategy_hashes": [],
    "assembly": {},
    "compression": {},
    "external_sources": [
      {
        "source_type": "agent_plugin_context",
        "provider_id": "crm-plugin",
        "hook_id": "crm-context-ranker",
        "tool_call_id": "",
        "selected_count": 2,
        "dropped_count": 1
      }
    ],
    "warnings": []
  }
}
```

trace 中只记录：

- strategy hash
- compression applied
- model provider/name
- input/output token estimate
- summary hash
- dropped counts

不记录完整 PromptBundle 明文。

## 9. Trace 与审计事件

新增 trace event 类型：

修改：

- `internal/contracts/governance.go`

建议新增：

```text
context.strategy.resolved
context.collection.completed
context.compression.requested
context.compression.completed
context.compression.failed
context.compression.fallback_applied
context.external_source.selected
context.external_source.dropped
```

payload 只放摘要：

```json
{
  "strategy_hash": "sha256...",
  "mode": "long_context",
  "source_counts": {},
  "external_sources": [
    {
      "source_type": "agent_plugin_context",
      "provider_id": "crm-plugin",
      "hook_id": "crm-context-ranker",
      "selected_count": 2,
      "dropped_count": 1
    }
  ],
  "compression_mode": "llm_then_truncate",
  "model_provider": "openai-compatible",
  "model_name": "deepseek-v4-flash",
  "input_tokens": 12000,
  "output_tokens": 3800,
  "summary_hash": "sha256..."
}
```

## 10. 配置策略

服务级 config 只保留默认值和平台兜底，不作为 Agent 主要配置入口。

修改：

- `internal/app/config/config.go`
- `config.example.json`
- `local.deepseek.env.example.ps1`

新增默认配置：

```json
{
  "context_default_mode": "balanced",
  "context_default_recent_message_limit": 20,
  "context_default_retrieval_max_results": 8,
  "context_default_task_history_max_items": 30,
  "context_default_token_budget": 4000,
  "context_compression_default_enabled": true,
  "context_compression_default_mode": "truncate"
}
```

保留环境变量只做部署兜底，不作为智能体创建者主要入口。

旧的 `conversation_max_retrieved` 在开发阶段直接替换为新配置，不保留旧入口兼容。

## 11. 实施步骤

### Phase 1：契约与默认策略

1. 新增 `internal/contracts/context_strategy.go`。
2. 修改 `AgentDefinition`，增加 `Strategies.Context`。
3. 修改 `PolicySet`，替换或扩展上下文治理策略。
4. 新增 `internal/context/strategy`，实现默认模式和策略解析。
5. 更新 `DefaultPolicySet()`。
6. 将 `runtime_hook_context` / `agent_plugin_context` 纳入内置 source type。
7. 更新 contract tests。

验收：

- `go test ./internal/contracts ./internal/policy/engine ./internal/context/strategy`
- 默认策略与当前行为大致一致：20 recent、8 retrieval、30 task history。

### Phase 2：移除硬编码窗口

1. 改造 `conversation_context.go`，所有 20/8/ConversationMaxRetrieved 改由 strategy 控制。
2. 改造 `context/conversation/engine.go`，`BuildRetrievalQuery` 支持传入 max results。
3. 改造 `taskHistory/memorySummaries/artifactRefs/toolSummaries`，支持 limit。
4. 增加 `ContextAssemblyReport`。
5. 将 runtime hook / AgentPlugin Service 注入的 context blocks 先进入 collector 候选集合，再按 strategy 选择。
6. 更新 coordinator tests。

验收：

- 设置 `recent_message_limit=1` 时 prompt 只出现 1 条 recent message。
- 设置 `retrieval_max_results=1` 时 retrieved context 只出现 1 条。
- 设置 `task_history_max_items=0` 时不限制 task history。
- 设置 `enabled_sources` 可关闭 memory/tool/artifact 等来源。
- 设置 `enabled_sources` 可关闭 plugin/hook 外部上下文来源。

### Phase 3：模型压缩器

1. 新增 `internal/context/compressor`。
2. 实现 `truncate` compressor。
3. 实现 `llm` compressor，复用 `modelclient.ModelClient`。
4. 内置默认 compression prompt profile。
5. 修改 `Coordinator`，在 `before_model_call` hook 后执行 compressor。
6. `applyPromptPolicy()` 拆分为 safety check、compression、hard limit guard。
7. 增加 trace events。
8. 压缩 prompt 默认把 hook/plugin/tool 输出标记为 untrusted，并要求保留 source refs。

验收：

- 超过 token budget 时触发压缩。
- `mode=truncate` 走本地截断。
- `mode=llm_then_truncate` 模型失败后走 fallback。
- 压缩后 PromptBundle hash 改变。
- trace 不包含压缩前明文。
- plugin/hook source refs 在压缩输出和 report 中可追溯。

### Phase 4：Agent package 与命令

1. 扩展 package source，支持 `strategies.context`。
2. 修改 compiler，将 `strategies.context` 编译进 `AgentDefinition.Strategies.Context`。
3. 使用 `agent.package.draft.patch_strategies` 更新上下文策略。
4. 更新 package validate，校验 compression prompt、模型、预算。
5. plugin_service source kind 发布时可携带 context strategy。
6. 更新 server tests。

验收：

- draft create 可带 `strategies.context`。
- patch 后 prompt.preview 使用新 strategy。
- 发布后 run 使用版本快照中的 context strategy。

### Phase 5：预览与诊断

1. `prompt.preview` 返回 `context_assembly_report` 和 `compression_report`。
2. run diagnostics 聚合 context trace events。
3. replay report 支持展示 context strategy hash、压缩状态、token 变化。
4. 更新 docs/openapi。

验收：

- prompt preview 能看到每类上下文候选数、选中数、丢弃数。
- diagnostics 能看到压缩是否发生、用的模型、summary hash。
- diagnostics 能看到外部 source 的 provider_id、hook_id、tool_call_id 摘要。
- 默认不返回完整 PromptBundle 明文之外的新敏感内容。

### Phase 6：评测与回归

新增 eval case 类型：

1. 长会话追问：验证压缩后仍保留最新用户意图。
2. 工具重场景：验证工具结果不会被压缩丢失。
3. 多人群聊：验证 speaker/reply_to/thread 关系保留。
4. Prompt injection：验证压缩器不执行历史消息中的指令。
5. 源引用保真：验证 compressed context 包含 source refs。
6. 插件上下文：验证 AgentPlugin Service/Hook 注入的 context blocks 受 source budget 控制。

脚本：

- `scripts/e2e_deepseek_smoke.ps1`
- `scripts/e2e_eval_suite.ps1`
- 新增 `scripts/e2e_context_strategy.ps1`

## 12. 测试计划

### 单元测试

新增：

- `internal/context/strategy/resolver_test.go`
- `internal/context/compressor/compressor_test.go`
- `internal/context/compressor/prompt_profile_test.go`

更新：

- `internal/runtime/kernel/conversation_context_test.go`
- `internal/runtime/kernel/coordinator_test.go`
- `internal/context/conversation/engine_test.go`
- `internal/context/promptbundle/builder_test.go`
- `internal/app/config/config_test.go`
- `internal/server/server_test.go`

### 关键断言

1. `0 = unlimited` 在所有 limit 字段语义一致。
2. Policy governance 能限制 Agent context strategy。
3. LLM compression 输出非法 JSON 时进入 fallback 或失败。
4. 压缩器不会把 system/developer prompt 明文写入 trace。
5. prompt preview 与实际 run 的 context strategy 一致。
6. PromptBundle hash 包含压缩后 context。
7. AgentPlugin Service/Hook 注入上下文默认标记为 untrusted。
8. 关闭 `agent_plugin_context` source 后，插件注入块不会进入 PromptBundle。

## 13. 风险与取舍

### 风险 1：模型压缩引入二次幻觉

缓解：

- 压缩 prompt 强制事实保真。
- 输出结构化 JSON。
- 保留 source refs。
- eval 覆盖关键事实保留。
- 对高风险 Agent 可使用 `truncate` 或 `strict_factual`。

### 风险 2：压缩模型成本和延迟增加

缓解：

- 仅达到 trigger ratio 后触发。
- 支持小模型压缩。
- 支持按 Agent 关闭。
- 支持缓存压缩摘要 hash。

### 风险 3：创建者配置过度复杂

缓解：

- 提供 mode 预设。
- 高级字段可选。
- prompt.preview 给出清晰报告。

### 风险 4：上下文过多带来注入风险

缓解：

- 所有历史消息、工具输出、artifact summary 标记为 untrusted。
- AgentPlugin Service / HookHost 返回的 context blocks 标记为 untrusted。
- 压缩 prompt 明确禁止执行上下文指令。
- Policy governance 可限制 full debug。

## 14. 建议落地优先级

优先做：

1. `ContextStrategy` 契约。
2. 去硬编码窗口。
3. `prompt.preview` 展示 assembly report。
4. 本地 truncate compressor 抽象化。
5. LLM compressor。
6. Agent package 支持 context strategy。
7. AgentPlugin Service / Hook 上下文进入统一 collector。
8. diagnostics 与 eval。

不要一开始就做：

- 前端配置页面。
- 独立 compression prompt profile 管理后台。
- 复杂向量检索。
- 压缩摘要长期记忆自动写入。
- 插件服务直接返回最终 PromptBundle。

## 15. 完成标准

本次重构完成后，应满足：

1. 智能体创建者可以在 Agent 包中配置上下文策略。
2. 服务端不再有不可配置的 20/8/30 上下文窗口。
3. 上下文超预算时可选择本地截断或模型压缩。
4. 模型压缩的模型、提示词、目标 token、保留项可配置。
5. `prompt.preview` 可以解释上下文来源、数量、压缩状态。
6. run diagnostics 可以追踪上下文策略与压缩结果，但不泄露完整敏感明文。
7. AgentPlugin Service / HookHost 注入的上下文受同一套 source budget、token budget、压缩和审计规则约束。
8. 单元测试和 e2e 覆盖长上下文、工具结果、群聊接话、插件上下文、压缩失败 fallback。
