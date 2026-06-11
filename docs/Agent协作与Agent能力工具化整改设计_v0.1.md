# CleanCore Agent 协作与 Agent 能力工具化整改设计 v0.1

## 1. 背景

当前 CleanCore 已经有“Agent 通过工具形式委派给另一个 Agent”的雏形：

- `origin.agent.delegate` 在启动时注册为普通工具。
- 模型必须通过 `tool_call` 调用 `origin.agent.delegate`。
- `origin.agent.delegate` 的 executor 会创建 `AgentHandoff`、child task，并启动目标 Agent run。
- `AgentCapability` 已有存储和文本匹配能力，但它目前更像“能力检索卡”，不是严格工具接口。

这说明框架里已经有 Agent 协作基础，但还没有把下面两类需求拆清楚：

```text
A. 协调其他 Agent
   把任务交给另一个 Agent 处理，底层是 handoff / child task / child run。

B. 使用其他 Agent 暴露的能力接口
   把另一个 Agent 的固定能力当工具调用，底层是 ToolRuntime / ToolResult。
```

这两类不能混在一个“其他 Agent”配置里。否则会导致权限扩大、工具噪音、循环委派、上下文泄露和责任边界不清。

### 1.1 本文范围

本文只处理 Agent 相关的协作和工具化问题：

```text
1. 框架内 Agent 协作
   当前 Agent 把任务委派给另一个框架内 Agent。
   底层语义是 AgentHandoff、child task、child run。

2. 框架内 Agent 能力工具化
   某个内部 Agent 显式把稳定能力导出为工具。
   底层语义是 ToolRuntime、ToolManifest、ToolResult。

3. 第三方 Agent 接入
   第三方 Agent 不进入内部 handoff 生命周期。
   它作为 Tool Provider / ToolGroup 暴露 catalog 和 invoke。
```

本文不处理：

```text
1. HTTP 工具本身的通用接入协议。
   这部分归属 docs/动态工具注册与HTTP工具改造设计_v0.1.md。

2. 把 AgentCapability 全量自动转成工具。

3. 通过一个 other_agents 字段同时表达协作、授权、导出和外部接入。

4. 绕过 AgentPackage draft/publish/stable/canary 的运行时内存热改。
```

## 2. 当前代码现状

### 2.1 `origin.agent.delegate` 已是工具

`internal/app/core/core.go` 当前注册：

```text
tool_id: origin.agent.delegate
description: Delegate a task to another Clean Core agent through AgentHandoff.
risk_level: medium
visibility: protected
execution_profile: local
```

input schema 已包含：

```text
parent_task_id
to_agent_id
to_agent_version
capability_query
objective
reason
handoff_mode
trace_id
artifact_refs
memory_refs
expected_output
```

这证明 “Agent 协作可以被模型以 tool_call 形式触发” 已经存在。

### 2.2 delegate executor 做的是 handoff

`internal/tool/handoff/executor.go` 的执行链路是：

```text
ToolCall origin.agent.delegate
  -> 读取 parent task
  -> 加载 source agent
  -> resolve target agent
  -> Handoffs.Create()
  -> 创建 child task
  -> StartTaskRun()
  -> 返回 handoff / child_task / target_run
```

这不是普通函数调用，而是 Agent 协作生命周期。

### 2.3 目标 Agent 发现还不成熟

当前 `resolveTargetAgent()` 主要依赖 `to_agent_id`：

```text
如果传入 to_agent_id，则加载该 Agent。
如果未传入，当前代码默认 test-agent。
```

`capability_query` 只做粗粒度文本匹配：

```text
agent_id / name / description / prompts / skill definitions
```

这说明当前具备“委派工具”，但不具备成熟的“可协调 Agent 检索与选择”。

### 2.4 AgentCapability 不是工具接口

当前 `AgentCapability` 字段包括：

```text
capability_id
tenant_id
agent_id
version
name
description
tags
when_to_use
risk_level
```

它缺少工具必需的：

```text
input_schema
output_schema
operation
executor/provider
visibility
auth/scope
version hash
enabled status
```

因此不能把所有 AgentCapability 自动当工具启用。

## 3. 核心结论

Agent 相关扩展必须拆成两条独立主线：

```text
主线 A: Agent Collaborator
  用于框架内 Agent 协作。
  走 origin.agent.delegate / AgentHandoff。
  表现为“可委派给哪个 Agent”。

主线 B: Agent Exported Tool
  用于把某个 Agent 的固定能力接口暴露为工具。
  走 ToolRuntime / ToolManifest / ToolResult。
  表现为“可调用哪个能力接口”。
```

不要做：

```text
配置一个 other_agents 字段
  -> 自动允许 handoff
  -> 自动把对方所有能力变工具
  -> 自动授权当前 Agent 使用
```

这会把协作关系、工具授权、能力导出、策略边界混成一坨。

推荐做：

```text
调用方声明 collaborators
被调用方声明 exports.tools
调用方 tool_bindings 决定能用哪些 exported tools
policy 再做最终治理
```

三方职责必须固定下来：

```text
调用方 Agent
  - 声明 collaborators，决定可以把任务委派给谁。
  - 声明 tool_bindings，决定自己可以调用哪些工具或工具组。
  - 运行时受 policy / approval / release switch 约束。

被调用内部 Agent
  - 声明 exports.tools，决定自己愿意暴露哪些稳定能力接口。
  - 每个 exported tool 必须提供 schema、risk、visibility、operation、version。
  - 不因为被列为 collaborator 就自动暴露所有能力。

第三方 Agent / 第三方服务
  - 按 provider 协议暴露 catalog / invoke / health。
  - 对 CleanCore 表现为 ToolProvider + ToolGroup + Tools。
  - 不进入内部 AgentHandoff 生命周期。
```

## 4. 概念模型

### 4.1 AgentCollaborator

`AgentCollaborator` 表示“当前 Agent 可以把任务委派给哪个框架内 Agent”。

```go
type AgentCollaborator struct {
    TenantID      contracts.TenantID      `json:"tenant_id,omitempty"`
    AgentID       contracts.AgentID       `json:"agent_id"`
    Version       contracts.AgentVersion  `json:"version,omitempty"` // stable by default

    Name        string   `json:"name,omitempty"`
    Description string   `json:"description,omitempty"`
    WhenToUse   []string `json:"when_to_use,omitempty"`
    Tags        []string `json:"tags,omitempty"`

    AllowedHandoffModes []contracts.HandoffMode `json:"allowed_handoff_modes,omitempty"`
    DefaultHandoffMode  contracts.HandoffMode   `json:"default_handoff_mode,omitempty"`

    MaxContextTokens int      `json:"max_context_tokens,omitempty"`
    AllowedContextScopes []string `json:"allowed_context_scopes,omitempty"`
    DeniedContextScopes  []string `json:"denied_context_scopes,omitempty"`

    RequiresApproval bool `json:"requires_approval,omitempty"`
    Status           string `json:"status"` // enabled, disabled
}
```

它不是工具。它是 delegate 工具的候选目标。

### 4.2 CollaboratorCard

PromptBundle 中可以注入轻量卡片：

```go
type CollaboratorCard struct {
    AgentID     contracts.AgentID      `json:"agent_id"`
    Version     contracts.AgentVersion `json:"version,omitempty"`
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    WhenToUse   []string               `json:"when_to_use,omitempty"`
    Tags        []string               `json:"tags,omitempty"`
    RiskLevel   contracts.RiskLevel    `json:"risk_level,omitempty"`
}
```

模型看到：

```text
retrieved collaborator cards:
  crm-assistant: 处理 CRM 客户查询、客户跟进、商机协调。

retrieved tool cards:
  origin.agent.delegate
```

模型调用：

```json
{
  "type": "tool_call",
  "tool_calls": [
    {
      "tool_id": "origin.agent.delegate",
      "arguments": {
        "to_agent_id": "crm-assistant",
        "objective": "查询客户 C123 的跟进状态并给出下一步建议",
        "handoff_mode": "hybrid"
      }
    }
  ]
}
```

### 4.3 AgentExportedTool

`AgentExportedTool` 表示“某个 Agent 显式导出的固定能力接口”。

```go
type AgentExportedTool struct {
    TenantID contracts.TenantID `json:"tenant_id,omitempty"`

    ProviderAgentID contracts.AgentID      `json:"provider_agent_id"`
    ProviderVersion contracts.AgentVersion `json:"provider_version,omitempty"`

    ToolID      string `json:"tool_id"`
    Operation   string `json:"operation"`
    Name        string `json:"name"`
    Description string `json:"description"`
    WhenToUse   []string `json:"when_to_use,omitempty"`
    Tags        []string `json:"tags,omitempty"`

    InputSchema  map[string]any `json:"input_schema"`
    OutputSchema map[string]any `json:"output_schema"`

    RiskLevel  contracts.RiskLevel      `json:"risk_level"`
    Visibility contracts.ToolVisibility `json:"visibility"`

    GroupID string `json:"group_id,omitempty"`
    Status  string `json:"status"` // draft, enabled, disabled
    Version string `json:"version"`
}
```

它可以被编译/同步成 ToolManifest：

```json
{
  "tool_id": "crm-agent.history.query",
  "provider_id": "agent:crm-agent",
  "group_id": "crm-agent-tools",
  "executor": {
    "type": "agent_tool",
    "provider_id": "agent:crm-agent",
    "operation": "history.query"
  },
  "execution_profile": {
    "domain_id": "agent_tool"
  }
}
```

## 5. 两条运行链路

### 5.1 协调链路：AgentCollaborator -> origin.agent.delegate

适用场景：

```text
这件事交给 CRM Agent 处理。
让审批 Agent 看一下这个合同风险。
把这个复杂任务路由给售后 Agent。
```

链路：

```text
Agent run
  -> CandidateProvider 检索 collaborators
  -> CandidateProvider 检索 origin.agent.delegate
  -> PromptBundle 注入 CollaboratorCard + delegate ToolCard
  -> 模型调用 origin.agent.delegate
  -> ToolRuntime schema/policy/approval
  -> HandoffService.Create()
  -> child task / child run
  -> handoff trace/audit
```

治理重点：

- `origin.agent.delegate` 必须在当前 Agent 的 tool bindings / policy 中允许。
- `to_agent_id` 必须来自 retrieved collaborator cards，不能任意编造。
- `handoff_mode` 必须在 collaborator 允许范围内。
- context package 按 collaborator 的 context policy 裁剪。
- 必须有 max handoff depth 和 cycle guard。
- Agent disabled/deleted 时不能作为 collaborator 被召回或执行。

### 5.2 工具链路：AgentExportedTool -> ToolRuntime

适用场景：

```text
调用 CRM Agent 的查询历史记录接口。
调用风控 Agent 的额度评分接口。
调用审批 Agent 的合同条款风险扫描接口。
```

链路：

```text
Agent run
  -> CandidateProvider 检索 ToolManifest
  -> PromptBundle 注入 ToolCard
  -> 模型调用 crm-agent.history.query
  -> ToolRuntime schema/policy/approval
  -> AgentToolExecutionDomain
  -> 调用目标 Agent 暴露的 operation
  -> 返回 ToolResult
```

治理重点：

- 目标 Agent 必须显式导出该 tool。
- 调用方 Agent 必须通过 tool_bindings 允许该 tool 或 tool group。
- policy 可以继续按 tool_id / group_id / risk_level 拦截。
- 不创建 child task，不进入 handoff 生命周期。
- 目标 Agent 不应该自由启动完整自主 run，除非该 exported tool 的语义明确如此。
- input/output schema 是硬边界。

## 6. 配置模型

### 6.1 调用方 Agent 配置 collaborators

AgentPackageSource 或 AgentPluginSource 建议新增：

```json
{
  "collaborators": [
    {
      "agent_id": "crm-assistant",
      "version": "stable",
      "name": "CRM Assistant",
      "description": "处理 CRM 客户资料、跟进记录和商机协调。",
      "when_to_use": ["CRM 客户查询", "客户跟进", "商机协调"],
      "allowed_handoff_modes": ["summary", "hybrid"],
      "default_handoff_mode": "hybrid",
      "max_context_tokens": 3000,
      "requires_approval": false,
      "status": "enabled"
    }
  ]
}
```

这只表示“可协调”。它不会自动授权对方 exported tools。

Agent 创建或编辑时可以直接带上这段配置。推荐把它作为 AgentPackageSource / AgentPluginSource 的一等字段，而不是放进 prompt 文本里：

```json
{
  "agent_id": "origin-coordinator",
  "collaborators": [
    {
      "agent_id": "crm-assistant",
      "version": "stable",
      "when_to_use": ["CRM 客户查询", "客户跟进", "商机协调"],
      "allowed_handoff_modes": ["summary", "hybrid"],
      "max_context_tokens": 3000,
      "requires_approval": false,
      "status": "enabled"
    }
  ]
}
```

字段名建议使用 `allowed_handoff_modes`，不要只叫 `handoff_modes`，这样能表达“这是调用方允许的委派模式约束”，不是目标 Agent 自己支持能力的完整列表。

创建/编辑 API 可以提供快捷体验，但落库和生效建议遵循：

```text
Agent create/edit request
  -> 写入 package draft 的 collaborators
  -> validate target agent/version/status/policy
  -> publish/canary/stable
  -> 编译 AgentDefinition
  -> upsert agent_collaborators 查询表
  -> invalidate collaborator candidate cache
```

运行时协作检索可以直接查 `agent_collaborators`：

```text
tenant_id + caller_agent_id + status=enabled
  -> 过滤 target agent enabled/current version
  -> 按 objective / when_to_use / tags 排序
  -> 生成 CollaboratorCard
  -> 如有命中，注入 origin.agent.delegate ToolCard
```

但这张表只解决“能委派给谁”。它不能自动变成“能调用对方哪些工具”。

### 6.2 被调用方 Agent 声明 exports

目标 Agent 自己声明可导出的能力接口：

```json
{
  "exports": {
    "tools": [
      {
        "tool_id": "crm-agent.history.query",
        "operation": "history.query",
        "name": "查询客户历史记录",
        "description": "按客户 ID 查询 CRM 历史跟进记录。",
        "when_to_use": ["客户历史记录", "CRM 跟进记录", "查询客户历史"],
        "input_schema": {
          "type": "object",
          "required": ["customer_id"],
          "properties": {
            "customer_id": { "type": "string" }
          }
        },
        "output_schema": {
          "type": "object",
          "required": ["records"],
          "properties": {
            "records": { "type": "array" }
          }
        },
        "risk_level": "low",
        "visibility": "protected",
        "group_id": "crm-agent-tools",
        "status": "enabled",
        "version": "v1"
      }
    ]
  }
}
```

这只表示“我愿意暴露什么”。它不会自动让所有调用方可用。

### 6.3 调用方通过 tool_bindings 授权使用 exported tools

调用方必须显式绑定：

```json
{
  "tool_bindings": {
    "allowed_tool_ids": ["crm-agent.history.query"],
    "allowed_tool_group_ids": ["crm-agent-tools"],
    "denied_tool_ids": [],
    "denied_tool_group_ids": []
  }
}
```

最终关系：

```text
collaborators
  控制能委派给谁。

exports.tools
  控制目标 Agent 暴露什么工具。

tool_bindings
  控制当前 Agent 能调用哪些工具。

policy
  最终治理，仍可 deny/approval。
```

## 7. API 设计

### 7.1 Collaborator API

```http
GET /v1/agents/{agent_id}/collaborators
PUT /v1/agents/{agent_id}/collaborators/{target_agent_id}
DELETE /v1/agents/{agent_id}/collaborators/{target_agent_id}
POST /v1/agents/{agent_id}/collaborators/{target_agent_id}/enable
POST /v1/agents/{agent_id}/collaborators/{target_agent_id}/disable
```

`PUT` 示例：

```json
{
  "version": "stable",
  "when_to_use": ["CRM 客户查询", "客户跟进"],
  "allowed_handoff_modes": ["summary", "hybrid"],
  "default_handoff_mode": "hybrid",
  "max_context_tokens": 3000,
  "requires_approval": false,
  "status": "enabled"
}
```

语义：

- 修改 draft，不直接改 stable。
- `activate=true` 可以一键 validate/publish/stable，但不能绕过 AgentPackage 发布流。
- target agent 必须存在且 status enabled。

### 7.2 Agent Exported Tools API

```http
GET /v1/agents/{agent_id}/exports/tools
PUT /v1/agents/{agent_id}/exports/tools/{tool_id}
DELETE /v1/agents/{agent_id}/exports/tools/{tool_id}
POST /v1/agents/{agent_id}/exports/tools/{tool_id}/enable
POST /v1/agents/{agent_id}/exports/tools/{tool_id}/disable
POST /v1/agents/{agent_id}/exports/tools/sync
```

`sync` 语义：

```text
1. 读取目标 Agent exports.tools
2. validate tool_id / schema / risk / visibility
3. upsert ToolManifest
4. upsert ToolGroup if needed
5. install enabled tools into RuntimeRegistry
6. invalidate CandidateProvider cache
```

### 7.3 Agent Tool Binding API

沿用工具绑定：

```http
GET /v1/agents/{agent_id}/tool-bindings
PUT /v1/agents/{agent_id}/tool-bindings
```

但需要支持 group 字段：

```json
{
  "allowed_tool_group_ids": ["crm-agent-tools"],
  "allowed_tool_ids": ["crm-agent.history.query"],
  "denied_tool_ids": [],
  "denied_tool_group_ids": []
}
```

## 8. CandidateProvider 改造

### 8.1 新候选类型

当前 CandidateSet 有 capabilities、skills、tools。建议新增 collaborators：

```go
type CandidateSet struct {
    Capabilities []contracts.CapabilityCard
    Skills       []contracts.SkillCard
    Tools        []contracts.ToolCard
    Collaborators []CollaboratorCard
}
```

检索顺序：

```text
1. 按 objective 检索 collaborators。
2. 如果 collaborators 非空，确保 origin.agent.delegate 参与 tool candidate。
3. 按 objective 检索 skill/tool/capability。
4. 对 exported tools 正常走 ToolManifest/ToolGroup 检索。
5. PromptBundle 注入 collaborator cards 和 tool cards。
```

### 8.2 delegate 工具可见性

`origin.agent.delegate` 不应该仅靠 objective 文本命中。建议规则：

```text
如果当前 Agent 有 enabled collaborators，且 policy 允许 handoff：
  origin.agent.delegate 可以进入候选工具。

如果没有 collaborators：
  origin.agent.delegate 不进入候选工具，除非显式 allowed_tool_ids 指定。
```

这样可以减少误委派。

### 8.3 to_agent_id 校验

模型调用 `origin.agent.delegate` 时：

```text
to_agent_id 必须在本 step retrieved collaborator cards 中。
```

如果没有：

```json
{
  "status": "denied",
  "error": {
    "code": "tool_policy_denied",
    "message": "target agent is not in retrieved collaborators"
  }
}
```

这比当前 executor 里默认 `test-agent` 更安全。

## 9. Runtime 改造

### 9.1 `origin.agent.delegate` executor

需要整改：

1. 移除 `toAgentID == "" -> test-agent` 默认逻辑。
2. `to_agent_id` 必须显式给出，或由上游 per-agent wrapper 固定注入。
3. 校验 target agent 是否在 caller 的 collaborators 中。
4. 校验 target agent status enabled。
5. 校验 handoff mode 是否允许。
6. 校验 max handoff depth。
7. 校验 cycle guard：同一 trace/run 链路中不允许 A -> B -> A 无界循环。
8. `capability_query` 不再作为唯一发现机制，只作为二次校验。

### 9.2 AgentToolExecutionDomain

新增 `agent_tool` execution domain：

```go
type AgentToolExecutionDomain struct {
    Agents loader.Loader
    ExportedTools AgentExportedToolStore
    Runtime AgentOperationRuntime
}
```

执行链路：

```text
ToolRuntime.Invoke()
  -> availability check
  -> schema validation
  -> policy / approval
  -> AgentToolExecutionDomain.Execute()
  -> resolve provider agent
  -> resolve exported operation
  -> execute operation
  -> output schema validation
  -> ToolResult
```

`execute operation` 第一版可以有两种实现：

```text
1. internal handler
   目标 Agent 注册本地 operation handler。

2. controlled mini-run
   目标 Agent 用固定 prompt/skill 处理该 operation，但必须限制 max_steps=1 或固定工具范围。
```

不建议第一版让 exported tool 启动完整自由 Agent run。那会和 handoff 语义混淆。

### 9.3 Per-Agent Delegate Wrapper 可选

为了减少模型填错 `to_agent_id`，可以自动生成 wrapper tool：

```text
agent.crm-assistant.delegate
```

它的 executor 只是包装：

```text
origin.agent.delegate(to_agent_id="crm-assistant", ...)
```

但底层仍然走 AgentHandoff，不是普通工具执行。

是否生成 wrapper 由 collaborator 配置决定：

```json
{
  "agent_id": "crm-assistant",
  "generate_delegate_tool": true,
  "delegate_tool_id": "agent.crm-assistant.delegate"
}
```

MVP 可以先不做 wrapper，只做 collaborator cards + `origin.agent.delegate`。

## 10. DB 草案

### 10.1 Agent Collaborators

```sql
CREATE TABLE agent_collaborators (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  target_agent_id TEXT NOT NULL,
  target_agent_version TEXT NOT NULL DEFAULT 'stable',
  collaborator_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  collaborator_hash TEXT NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, agent_id, target_agent_id, version)
);

CREATE INDEX idx_agent_collaborators_agent_status
  ON agent_collaborators (tenant_id, agent_id, status);
```

查询语义：

```sql
SELECT collaborator_json
FROM agent_collaborators
WHERE tenant_id = $1
  AND agent_id = $2
  AND status = 'enabled';
```

实际执行前还要二次校验：

```text
target_agent_id 存在
target_agent_version 可解析到 stable/current
target agent status enabled
caller policy 允许 handoff
release switch 未禁用 handoff 或 target agent
```

因此 `agent_collaborators` 是协作候选的查询表，也是 delegate executor 的强制校验来源。

### 10.2 Agent Exported Tools

```sql
CREATE TABLE agent_exported_tools (
  tenant_id TEXT NOT NULL,
  provider_agent_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  export_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  export_hash TEXT NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, provider_agent_id, tool_id, version)
);

CREATE INDEX idx_agent_exported_tools_provider_status
  ON agent_exported_tools (tenant_id, provider_agent_id, status);
```

### 10.3 与 tool_manifests 的关系

`agent_exported_tools` 是源配置，`tool_manifests` 是运行时工具目录。

同步后：

```text
agent_exported_tools
  -> ToolManifest {
       provider_id = "agent:" + provider_agent_id
       executor.type = "agent_tool"
       executor.operation = operation
     }
  -> RuntimeRegistry
```

## 11. 安全与治理

必须遵守：

1. Collaborator 不等于 ToolBinding。
2. ExportedTool 不等于自动授权。
3. `origin.agent.delegate` 的 target 必须来自 retrieved collaborator 或专属 wrapper。
4. Agent disabled/deleted 时，collaborator 和 exported tools 都不可用。
5. Exported tool 必须有 input/output schema。
6. 高风险 exported tool 仍走 approval。
7. Handoff 必须限制 max depth。
8. Handoff 必须做 cycle guard。
9. Context package 必须按 target collaborator policy 裁剪。
10. Exported tool 不应默认读取父 Agent 全量上下文。
11. ToolCard 不暴露内部 prompt、credential、不可见 memory。
12. AgentCapability 不能直接自动启用成工具。

## 12. 推荐落地路线

### 阶段一：修正现有 delegate 安全边界

1. 移除 `test-agent` 默认目标。
2. `origin.agent.delegate` 要求明确 `to_agent_id`。
3. 增加 collaborator store/interface。
4. executor 校验 target 是否在 collaborators 中。
5. CandidateProvider 注入 CollaboratorCard。

### 阶段二：产品化 collaborators

1. AgentPackageSource / AgentPluginSource 增加 collaborators。
2. API 支持 collaborators CRUD。
3. PromptBundle 注入 retrieved collaborator cards。
4. prompt 约束模型只能委派给 retrieved collaborators。
5. trace/audit 记录 collaborator match。

### 阶段三：Agent exported tools

1. AgentPackageSource / AgentPluginSource 增加 exports.tools。
2. 新增 AgentExportedTool store/API。
3. `exports/tools/sync` 生成 ToolManifest。
4. 新增 `agent_tool` execution domain。
5. ToolRuntime availability check 增加 provider agent status。

### 阶段四：可选 per-agent delegate wrapper

1. 根据 collaborator 生成 `agent.<agent_id>.delegate`。
2. wrapper 固定 `to_agent_id`。
3. wrapper 仍走 `origin.agent.delegate` 和 AgentHandoff。
4. 用 wrapper 降低模型填错参数概率。

## 13. 最小验收标准

MVP 至少满足：

1. 当前 Agent 可配置 enabled collaborators。
2. PromptBundle 能看到 retrieved collaborator cards。
3. 没有 collaborator 时，`origin.agent.delegate` 不应被误召回。
4. 模型只能委派给 retrieved collaborator。
5. `origin.agent.delegate` 不再默认 `test-agent`。
6. Handoff 仍产生 child task / child run / trace / audit。
7. 目标 Agent 可声明一个 exported tool。
8. exported tool 能同步为 ToolManifest。
9. 当前 Agent 必须通过 tool_bindings 才能调用 exported tool。
10. exported tool 调用走 ToolRuntime schema/policy/approval/result。

## 14. 和外部工具 Provider 文档的关系

`docs/动态工具注册与HTTP工具改造设计_v0.1.md` 解决的是：

```text
外部 provider / ToolHost / http_direct / worker / managed
```

本文解决的是：

```text
框架内 Agent 协作
框架内 Agent 能力工具化
```

两者统一到 ToolManifest 的位置是：

```text
External ToolHost exported tool
  -> ToolManifest executor.type=static_tool_host

Internal Agent exported tool
  -> ToolManifest executor.type=agent_tool

Internal Agent collaborator
  -> CollaboratorCard + origin.agent.delegate
```

不要把 collaborator 直接塞进 ToolManifest，也不要把 exported tool 做成 handoff。

## 15. 第三方 Agent 作为 Tool Provider

第三方 Agent 的推荐接入方式是：

```text
第三方 Agent = Tool Provider / Tool Group
第三方 Agent 的每个能力接口 = Tool
```

CleanCore 不需要判断第三方本质是不是 LLM、Agent、工作流引擎或普通服务。只要它能稳定提供工具目录和调用接口，就按外部工具 provider 治理。

### 15.1 Provider 形态

建议在外部工具文档的 provider 模型上增加一种 provider kind：

```json
{
  "provider_id": "third-party-agent:risk-agent",
  "provider_type": "static_tool_host",
  "provider_kind": "third_party_agent",
  "name": "Risk Agent",
  "description": "第三方风控 Agent，提供额度评分和合同风险扫描。",
  "endpoint": "https://risk-agent.example.com",
  "status": "enabled"
}
```

`provider_type` 表示 transport / adapter，`provider_kind` 表示业务来源。两者不要混淆：

```text
provider_type
  static_tool_host / http_direct / worker / managed

provider_kind
  service / third_party_agent / internal_agent_export / builtin
```

### 15.2 Catalog 协议

第三方 Agent 至少需要暴露：

```http
GET /tools/catalog
POST /tools/invoke
GET /health
```

catalog 返回工具组和工具：

```json
{
  "provider_id": "third-party-agent:risk-agent",
  "groups": [
    {
      "group_id": "risk-agent-tools",
      "name": "Risk Agent Tools",
      "description": "风控 Agent 暴露的稳定工具能力。"
    }
  ],
  "tools": [
    {
      "tool_id": "risk-agent.credit.score",
      "group_id": "risk-agent-tools",
      "operation": "credit.score",
      "name": "额度评分",
      "description": "根据客户资料和订单信息计算授信评分。",
      "input_schema": {
        "type": "object",
        "required": ["customer_id"],
        "properties": {
          "customer_id": { "type": "string" }
        }
      },
      "output_schema": {
        "type": "object",
        "required": ["score"],
        "properties": {
          "score": { "type": "number" },
          "reason": { "type": "string" }
        }
      },
      "risk_level": "medium",
      "visibility": "protected",
      "version": "v1"
    }
  ]
}
```

### 15.3 与内部 Agent 的区别

```text
内部 collaborator
  -> 走 origin.agent.delegate
  -> 创建 AgentHandoff / child task / child run
  -> 目标是任务协作

内部 exported tool
  -> 走 ToolRuntime
  -> executor.type = agent_tool
  -> 目标是固定能力接口

第三方 Agent provider
  -> 走 ToolRuntime
  -> executor.type = static_tool_host / http_direct / worker
  -> 目标是外部稳定工具接口
```

第三方 Agent 不应该出现在内部 `agent_collaborators` 表里，除非未来明确支持跨系统 handoff 协议。当前整改阶段只把它接入工具体系。

## 16. 发布、同步与启动恢复

### 16.1 不允许的正式路径

以下方式只能用于开发期调试，不能作为生产正式能力：

```text
直接修改运行中内存 AgentDefinition
直接向 RuntimeRegistry 塞 ToolCard 而不落 catalog
只更新 DB 不刷新 CandidateProvider / RuntimeRegistry
只更新 CandidateProvider 但 ToolRuntime 无法执行
```

这些做法都会造成“模型看到了但运行不了”或“注册成功但模型看不到”。

### 16.2 Collaborator 发布路径

`collaborators` 属于 AgentPackage / AgentPlugin 的声明性配置，推荐路径：

```text
编辑 draft
  -> validate collaborator target/status/policy
  -> publish package version
  -> canary/stable
  -> AgentRegistry.Put(compiled definition)
  -> CandidateProvider cache invalidate
```

运行时不能只改当前内存里的 AgentDefinition。即使 API 提供快捷入口，也应在内部生成 package draft 并走发布流。

### 16.3 Exported Tool 同步路径

`exports.tools` 是源声明，`ToolManifest` 是运行时目录。推荐路径：

```text
编辑 provider Agent exports.tools
  -> validate schema/risk/visibility/tool_id/operation
  -> publish provider Agent package
  -> sync exports.tools
  -> upsert tool_groups
  -> upsert tool_manifests
  -> ToolCatalogService.InstallEnabledTools()
  -> CandidateProvider cache invalidate
```

`ToolCatalogService` 是工具注册、更新、启停、同步、启动恢复的唯一入口。内部 Agent exported tool 也应通过它进入工具目录，不能在 `agent_tool` executor 里临时拼 ToolCard。

### 16.4 启动恢复顺序

推荐启动恢复顺序：

```text
1. load config / release switches
2. load stable/canary AgentPackage definitions
3. restore AgentRegistry
4. load tool_providers / tool_groups / tool_manifests
5. load agent_exported_tools current versions
6. rebuild internal agent_tool manifests if needed
7. ToolCatalogService.InstallEnabledTools()
8. rebuild CandidateProvider indexes
9. run provider health checks asynchronously
10. mark unavailable providers/tools as not invokable
```

`origin.agent.delegate` 作为 builtin internal tool 可以在 Go tool provider 启动时注册，但它是否进入某个 Agent 的候选集，仍由 collaborators + tool binding + policy 决定。

## 17. 代码整改清单

### 17.1 Contracts

建议新增或扩展：

```text
contracts.AgentCollaborator
contracts.CollaboratorCard
contracts.AgentExportedTool
contracts.AgentExportedToolBinding
contracts.ToolGroup
contracts.ToolManifest
contracts.ToolProvider
contracts.ToolExecutorSpec
contracts.CandidateSet.Collaborators
```

注意：

```text
AgentCapability 继续作为“能力检索卡”。
AgentCollaborator 表示“可委派目标”。
AgentExportedTool 表示“被导出的固定工具接口”。
ToolManifest 表示“运行时可检索、可执行的工具目录项”。
```

### 17.2 Loader / Package

AgentPackageSource 建议增加：

```yaml
collaborators:
  - agent_id: crm-assistant
    version: stable
    when_to_use:
      - CRM 客户查询
    allowed_handoff_modes:
      - summary
      - hybrid
    default_handoff_mode: hybrid
    max_context_tokens: 3000
    status: enabled

exports:
  tools:
    - tool_id: crm-agent.history.query
      operation: history.query
      name: 查询客户历史记录
      input_schema: {}
      output_schema: {}
      risk_level: low
      visibility: protected
      group_id: crm-agent-tools
      status: enabled
      version: v1
```

loader validate 阶段至少检查：

```text
collaborator agent_id 格式
handoff_mode 合法
max_context_tokens 上限
exported tool_id 全局命名规范
operation 非空
input_schema/output_schema 是合法 JSON Schema
risk_level/visibility 合法
tool_id 与 builtin/external tool 不冲突
```

### 17.3 Discovery / PromptBundle

`internal/discovery/tool` 当前主要返回 tools/capabilities/skills。需要拆出 collaborator discovery：

```text
CollaboratorStore.ListEnabled(caller_agent_id)
  -> rank by objective/query
  -> CollaboratorCard[]
  -> PromptBundle 注入 retrieved collaborator cards
```

Prompt 约束：

```text
模型只能把 origin.agent.delegate 的 to_agent_id 设置为 retrieved collaborator cards 中的 agent_id。
如果没有合适 collaborator，不要调用 origin.agent.delegate。
如果需要固定能力接口，优先调用对应 exported tool，而不是 handoff。
```

### 17.4 Tool Runtime

`ToolRuntime.Invoke()` 前必须做强制可用性检查：

```text
tool status enabled
provider status enabled/healthy enough
agent status enabled
tool binding allowed
policy allowed or approval granted
not disabled by release switch
```

对 `agent_tool` executor 额外检查：

```text
provider_agent_id exists
provider_agent status enabled
exported tool status enabled
operation version matches manifest
input/output schema validation
```

### 17.5 Handoff Executor

`internal/tool/handoff/executor.go` 是第一优先级整改点：

```text
移除 toAgentID 为空时默认 test-agent
要求 to_agent_id 显式存在
校验 target 在 caller enabled collaborators 中
校验 target tenant/status/version
校验 handoff mode
校验 max depth
校验 cycle guard
把 collaborator_id / target_agent_id / decision reason 写 trace/audit
```

### 17.6 Server API

新增 API 可以分三组：

```text
Collaborator API
  管理 caller Agent 可以委派给谁。

Agent Export API
  管理 provider Agent 暴露什么工具。

Tool Binding API
  管理 caller Agent 可以调用哪些工具/工具组。
```

这些 API 可以提供快捷编辑体验，但底层必须写入 draft 或 catalog，不应只改运行时对象。

## 18. 兼容与迁移策略

### 18.1 对现有 `origin.agent.delegate` 的兼容

短期兼容策略：

```text
已有 agent package 中 allowed_tool_ids 包含 origin.agent.delegate 的，继续允许工具存在。
如果没有配置 collaborators，delegate 不再自动召回。
如果模型显式调用 delegate 但 target 不在 collaborators，返回 policy denied。
```

为了降低破坏性，可以加一段临时迁移：

```text
开发/测试环境：
  可以用 config 开关允许 legacy delegate fallback。

生产环境：
  默认关闭 legacy fallback。
  必须显式配置 collaborators。
```

但 `to_agent_id == "" -> test-agent` 这种默认目标应尽快删除，因为它会制造隐式越权和错误路由。

### 18.2 对 AgentCapability 的迁移

已有 `AgentCapability` 数据不删除，迁移为两种用途：

```text
1. 继续作为 Agent 发现/匹配的文本索引。
2. 作为创建 collaborator 或 exported tool 的辅助素材。
```

不能自动做：

```text
AgentCapability -> enabled ToolManifest
AgentCapability -> allowed tool binding
AgentCapability -> enabled collaborator
```

可以提供半自动建议：

```text
根据 AgentCapability 生成 draft collaborator 建议。
根据 AgentCapability 生成 draft exported tool 模板。
由用户/管理员补齐 schema、risk、visibility、approval 后发布。
```

### 18.3 对旧工具候选逻辑的迁移

现有 `StaticCandidateProvider` 里基于 `tools.Cards()` 的静态快照，需要逐步替换：

```text
第一步：保留静态工具卡，但增加 collaborators candidate。
第二步：改为 Registry/Catalog backed provider。
第三步：ToolGroup 参与召回。
第四步：provider health/status 影响候选和执行。
```

## 19. 风险场景与防护矩阵

| 风险 | 触发方式 | 防护 |
| --- | --- | --- |
| 循环委派 | A 委派 B，B 又委派 A | max depth + trace cycle guard |
| 权限穿透 | A 通过 B 使用自己无权工具 | child run 使用 B 的 policy，同时限制 context package |
| 工具爆炸 | 自动把所有 Agent 能力转工具 | 只同步 exports.tools |
| Schema 漂移 | exported tool 参数变化 | version/hash + publish/sync + schema validation |
| Provider 不可用 | 第三方 Agent 下线 | health check + availability check |
| 上下文泄露 | handoff 传递全量上下文 | collaborator context policy + max_context_tokens |
| 错误目标 | 模型编造 to_agent_id | 必须来自 retrieved collaborator cards |
| 影子注册 | DB 有工具但 Runtime 没 executor | ToolCatalogService 统一 install + 启动恢复校验 |
| 越权调用 | 绑定了 group 后调用高风险工具 | policy 按 tool_id/risk/approval 二次拦截 |
| 审计缺失 | wrapper tool 隐藏真实目标 | trace/audit 同时记录 wrapper tool 和 resolved target |

## 20. 分阶段验收用例

### 20.1 Collaborator 验收

```text
given 当前 Agent 配置 crm-assistant collaborator
when 用户要求查询客户跟进
then CandidateProvider 返回 crm-assistant CollaboratorCard
and origin.agent.delegate 进入候选工具
and 模型调用 delegate 时 to_agent_id=crm-assistant
and handoff 创建 child task / child run
and trace/audit 记录 collaborator match 与 handoff created
```

反向用例：

```text
given 当前 Agent 没有任何 enabled collaborators
when 用户要求“让别的 Agent 处理”
then origin.agent.delegate 不应自动进入候选工具
and 如果模型仍调用 delegate，应返回 policy denied
```

### 20.2 Exported Tool 验收

```text
given crm-agent 声明 exports.tools: crm-agent.history.query
and caller Agent 绑定 crm-agent-tools
when 用户要求查询客户历史记录
then CandidateProvider 返回 crm-agent.history.query ToolCard
and ToolRuntime 通过 schema/policy/approval
and agent_tool executor 调用 history.query operation
and 返回 ToolResult
and 不创建 handoff / child task
```

反向用例：

```text
given caller Agent 只配置 crm-agent collaborator
and 未绑定 crm-agent-tools
when 用户要求查询客户历史记录
then crm-agent.history.query 不应进入候选工具
and 不能因为 collaborator 自动获得工具权限
```

### 20.3 第三方 Agent Provider 验收

```text
given 注册 third_party_agent provider risk-agent
and sync catalog 得到 risk-agent.credit.score
and caller Agent 绑定 risk-agent-tools
when 用户要求额度评分
then ToolRuntime 调用 provider /tools/invoke
and provider health/status 影响可用性
and 不创建内部 AgentHandoff
```

## 21. 最终目标状态

整改完成后，CleanCore 里 Agent 相关扩展应呈现为三条清晰路径：

```text
1. 内部协作
   collaborators
     -> CollaboratorCard
     -> origin.agent.delegate
     -> AgentHandoff / child task / child run

2. 内部能力工具化
   exports.tools
     -> ToolManifest executor.type=agent_tool
     -> ToolRuntime
     -> ToolResult

3. 第三方 Agent 接入
   tool provider provider_kind=third_party_agent
     -> ToolGroup / ToolManifest
     -> ToolRuntime
     -> external invoke
```

只要这三条路径不混用，后续再做自动发现、per-agent delegate wrapper、自动生成 tool draft、跨系统 handoff，都能在清晰边界上演进。
