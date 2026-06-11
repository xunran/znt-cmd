# PreReply / IntakePolicy、MCP ToolProvider、A2A 外部协作扩展设计

版本：v0.1
日期：2026-06-05
定位：在不污染 Runtime Kernel 的前提下，为 CleanCore 增加入口预回复、MCP 工具接入和 A2A 外部协作 adapter。

---

## 0. 总结

本设计覆盖三项能力：

```text
1. PreReply / IntakePolicy
   框架提供可选的入口预回复匹配和返回接口；
   具体话术发送仍由外层应用或渠道 adapter 完成。

2. MCP ToolProvider adapter
   MCP 作为 ToolProvider / ExecutorAdapter 接入 ToolCatalog；
   MCP tools/list 生成 ToolManifest，MCP tools/call 仍走 ToolRuntime 治理链路。

3. A2A external collaboration adapter
   A2A 作为 CollaborationProvider 实现接入现有 ExternalTaskBinding / Bridge；
   不替代内部 AgentHandoff，也不污染 Runtime Kernel。
```

共同原则：

```text
Runtime Kernel 不接收新的控制流插件。
外部协议只通过 adapter 进入系统。
所有副作用仍进入 policy / trace / audit / repository。
```

---

## 1. PreReply / IntakePolicy

### 1.1 目标

支持这种产品体验：

```text
用户发送一句话
  -> 框架按规则匹配可选预回复
  -> 外层应用立即把固定话术发给用户
  -> 外层应用继续调用 agent.run / task.start
  -> Runtime Kernel 再让模型思考并进入正常流程
```

### 1.2 不做什么

```text
1. 不让大模型生成“收到，我先看一下”这类固定话术。
2. 不让 RuntimeHook 发送用户可见消息。
3. 不让 PromptBuilder 或 ToolRuntime 负责入口体验。
4. 不在 Kernel 内启动异步后台流程。
```

### 1.3 模块边界

新增模块：

```text
internal/intake
  Policy
  Rule
  PreReply
  EvaluateRequest
  EvaluateResult
  Store
  Service
```

新增 HTTP API：

```text
POST   /v1/intake/policies
GET    /v1/intake/policies
GET    /v1/intake/policies/{policy_id}
PUT    /v1/intake/policies/{policy_id}
DELETE /v1/intake/policies/{policy_id}
POST   /v1/intake/evaluate
```

### 1.4 数据模型

```text
IntakePolicy
  tenant_id
  policy_id
  name
  status: draft / enabled / disabled
  priority
  agent_id
  agent_version
  channel
  match_type: always / exact / prefix / contains / regex
  pattern
  reply_text
  reply_kind: status_update / acknowledgement / policy_notice
  continue_to_run
  version
  created_at
  updated_at
```

匹配结果：

```text
PreReplyDecision
  matched
  policy_id
  reply_text
  reply_kind
  continue_to_run
  dispatch: external_channel
```

说明：

```text
dispatch = external_channel 表示 CleanCore 只返回话术和决策，
不直接向微信、WebSocket、Slack、A2A、Array 等渠道发送消息。
```

### 1.5 Trace / Audit

新增事件：

```text
TraceIntakePreReplyEvaluated = intake.pre_reply_evaluated
AuditIntakePolicyUpserted = intake.policy.upserted
AuditIntakePolicyDeleted = intake.policy.deleted
```

审计原则：

```text
1. policy upsert/delete 写 Audit。
2. evaluate 可写 Trace，不写用户可见消息。
3. trace payload 记录 policy_id、matched、continue_to_run、channel、agent_id。
4. 不记录 secret，不记录渠道 token。
```

---

## 2. MCP ToolProvider Adapter

### 2.1 目标

让 MCP server 的工具进入 CleanCore 统一工具治理链路：

```text
MCP endpoint
  -> tools/list
  -> ToolManifest
  -> RuntimeRegistry cache
  -> ToolRuntime.Invoke
  -> MCP tools/call
  -> ToolResult / Trace / Audit
```

### 2.2 协议映射

基于 MCP Streamable HTTP / JSON-RPC 形态：

```text
tools/list
  -> provider catalog

tools/call
  -> executor invoke
```

映射规则：

```text
MCP tool.name              -> ToolManifest.Executor.Operation
provider_id + "." + name   -> ToolManifest.ToolID
tool.description           -> ToolManifest.Description
tool.inputSchema           -> ToolManifest.InputSchema
tool.outputSchema          -> ToolManifest.OutputSchema, 如果存在
annotations/destructive/openWorld -> risk hint
```

### 2.3 模块边界

修改模块：

```text
internal/tool/catalog
  ProviderTypeMCP = "mcp"
  ExecutorTypeMCP = "mcp"
  MCP catalog fetch
  MCPExecutor
```

不修改：

```text
ToolRuntime 主流程
DecisionValidator
Runtime Kernel
```

### 2.4 治理要求

```text
1. MCP provider 必须 enabled 且健康才能安装 runtime cache。
2. MCP tool 仍必须通过 ToolRuntime 的 schema / policy / approval / execution domain / trace。
3. MCP endpoint 只能通过 ExecutionProfile 的 http domain 访问。
4. MCP tool call 的 content / structuredContent 映射到 ToolResult.Output。
5. 如果 MCP result.isError = true，返回 ToolResult failed。
```

### 2.5 非目标

```text
1. 本轮不实现 stdio MCP transport。
2. 本轮不实现完整 OAuth / session negotiation。
3. 本轮不让模型直接访问 MCP endpoint。
4. 本轮不把 MCP resource/prompt 暴露成 PromptBundle 事实源。
```

---

## 3. A2A External Collaboration Adapter

### 3.1 目标

让 CleanCore 能把外部 A2A Agent / task 作为外部协作系统绑定：

```text
ExternalTaskBinding(provider = "a2a")
  -> CollaborationProvider
  -> A2A JSON-RPC
  -> message/send, tasks/get, agent card
```

### 3.2 与内部协作的边界

```text
Internal Handoff != External A2A
Agent exported tool != A2A remote agent
Agent card != internal AgentDefinition
```

内部 Agent 之间仍走：

```text
origin.agent.delegate -> AgentHandoff
```

外部 A2A 只负责：

```text
1. 获取外部任务摘要。
2. 获取远程 Agent card / participant summary。
3. 写回消息。
4. 写回 artifact ref。
5. 做可选 access check。
```

### 3.3 模块边界

新增模块：

```text
internal/bridge/a2a
  HTTPAdapter implements contracts.CollaborationProvider
```

修改模块：

```text
internal/app/config
  external_bridge_provider: array / a2a
  external_bridge_base_url
  external_bridge_token

internal/app/core
  根据 external_bridge_provider 选择 array.HTTPAdapter 或 a2a.HTTPAdapter
```

### 3.4 A2A 映射

```text
GetTask
  -> JSON-RPC tasks/get

SendMessage
  -> JSON-RPC message/send

AttachArtifact
  -> JSON-RPC message/send，携带 artifact ref data part

GetParticipants
  -> Agent Card

CheckAccess
  -> 本地 binding tenant/status gate + 远程 task 可达性
```

### 3.5 Trace / Audit

沿用现有外部协作治理：

```text
TraceExternalWritebackOK
TraceExternalWritebackFailed
AuditExternalWritebackFailed
```

新增 adapter 不新增新的事实源；ExternalTaskBinding 仍是 CleanCore 内部绑定事实。

---

## 4. API 和测试要求

真实 API 必须覆盖：

```text
1. PreReply policy create/list/get/update/delete/evaluate。
2. MCP provider create/health/sync/manifest/tools.invoke/trace。
3. A2A external task binding、external task get、writeback trace。
4. 全 OpenAPI operation coverage missing = 0。
```

真实业务逻辑测试必须证明：

```text
1. 预回复只返回决策，不直接发送渠道消息。
2. MCP tools/list 生成 ToolManifest，tools/call 返回 ToolResult。
3. MCP 工具调用记录 tool_version / execution_profile。
4. A2A adapter 可读取外部任务并写回消息。
5. 所有新增能力不绕过 ToolRuntime / ExternalBridge / Trace / Audit。
```

---

## 5. 参考

```text
MCP specification: https://modelcontextprotocol.io/specification
MCP tools: https://modelcontextprotocol.io/specification/2025-06-18/server/tools
A2A specification v0.3.0: https://a2a-protocol.org/v0.3.0/specification/
```

---

## 6. 设计自查

| 检查项 | 结论 |
| --- | --- |
| 是否污染 Runtime Kernel | 否。PreReply 在 intake，MCP 在 ToolCatalog adapter，A2A 在 external bridge adapter。 |
| 是否绕过 ToolRuntime | 否。MCP tools/call 由 MCPExecutor 作为 ToolRuntime executor 执行。 |
| 是否让外部协议改内部契约 | 否。MCP/A2A 都映射到现有 ToolManifest/ToolCall/ToolResult/ExternalTaskBinding。 |
| 是否满足“外层应用发送话术” | 是。PreReply 只返回 dispatch=external_channel。 |
| 是否能做真实 API 测试 | 是。新增 routes 和已有 all-interface E2E 均可覆盖。 |

