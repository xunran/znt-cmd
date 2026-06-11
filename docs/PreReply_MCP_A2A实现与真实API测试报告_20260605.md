# PreReply / MCP / A2A 实现与真实 API 测试报告

日期：2026-06-05

结论：本轮 PreReply / IntakePolicy、MCP ToolProvider adapter、A2A external collaboration adapter 均已实现，并通过单元测试、全接口真实 HTTP E2E、真实模型 smoke 验证。

---

## 1. 实现摘要

### PreReply / IntakePolicy

新增 `internal/intake` 服务，提供：

1. Intake policy CRUD。
2. deterministic evaluate：`always / exact / prefix / contains / regex`。
3. `dispatch=external_channel` 决策返回。
4. policy upsert/delete audit。
5. evaluate trace：`intake.pre_reply_evaluated`。

HTTP API 已接入：

```text
POST   /v1/intake/policies
GET    /v1/intake/policies
GET    /v1/intake/policies/{policy_id}
PUT    /v1/intake/policies/{policy_id}
DELETE /v1/intake/policies/{policy_id}
POST   /v1/intake/evaluate
```

边界结论：

```text
CleanCore 只返回预回复决策。
具体话术发送仍由外层应用 / channel adapter 执行。
Runtime Kernel、PromptBuilder、ToolRuntime 均未承接 channel send。
```

### MCP ToolProvider Adapter

在 `internal/tool/catalog` 增加：

1. `ProviderTypeMCP = "mcp"`。
2. `ExecutorTypeMCP = "mcp"`。
3. MCP `tools/list` JSON-RPC catalog fetch。
4. MCP tool 到 `ToolManifest` 的映射。
5. `MCPExecutor` 使用 `tools/call` 执行。
6. MCP provider health check 通过 `tools/list`。
7. MCP `isError=true` 映射为 tool execution failure。

边界结论：

```text
MCP tool 仍通过 ToolCatalog 安装为普通 ToolManifest。
调用仍通过 ToolRuntime 的 schema / policy / approval / execution domain / trace 链路。
MCP 没有绕过 ToolRuntime。
```

### A2A External Collaboration Adapter

新增 `internal/bridge/a2a` HTTP adapter，实现 `contracts.CollaborationProvider`：

1. `GetTask` -> A2A JSON-RPC `tasks/get`。
2. `SendMessage` -> A2A JSON-RPC `message/send`。
3. `AttachArtifact` -> A2A `message/send` data part。
4. `GetParticipants` -> Agent Card。
5. `CheckAccess` -> remote task reachable check；本地 binding tenant/status gate 仍由 `array.Bridge` 执行。

配置新增：

```text
external_bridge_provider = array / a2a
CLEAN_CORE_EXTERNAL_BRIDGE_PROVIDER
```

边界结论：

```text
A2A 作为 external bridge adapter 接入。
没有替代内部 AgentHandoff / agent_tool / origin.agent.delegate。
```

---

## 2. 架构复查

| 检查项 | 结论 |
| --- | --- |
| PreReply 是否污染 Kernel | 否。`intake` 只在 `internal/intake`、`core` 初始化、`server` handler、contracts trace/audit 常量中出现。 |
| PreReply 是否发送渠道消息 | 否。evaluate 只返回 `pre_reply.dispatch=external_channel`。 |
| MCP 是否绕过 ToolRuntime | 否。MCP provider sync 后生成 `ToolManifest`，执行器由 registry 装入，再由 ToolRuntime 调用。 |
| MCP 是否复用治理 | 是。provider/group/status/tenant guard、schema、policy、trace 均复用现有链路。 |
| A2A 是否替代内部协作 | 否。A2A 只实现 `CollaborationProvider`，内部 handoff/agent_tool 保持原路径。 |
| 外部协议是否侵入内部契约 | 否。MCP 映射到 ToolManifest/ToolCall/ToolResult；A2A 映射到 ExternalTaskSummary/ParticipantSummary/writeback request。 |

---

## 3. 测试证据

### Go Test

命令：

```powershell
& 'D:\code2\znt\.tools\go-go1.26.3\go\bin\go.exe' test ./...
```

结果：

```text
PASS
所有 package 通过。
```

重点新增测试：

```text
internal/intake
internal/tool/catalog
internal/bridge/a2a
internal/app/config
internal/app/core
internal/server
```

### 全接口真实 HTTP E2E

命令：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/e2e_clean_core_all_interfaces.ps1 -ReportDir tmp/clean-core-all-interfaces-20260605-prereply-mcp-a2a
```

报告：

```text
tmp/clean-core-all-interfaces-20260605-prereply-mcp-a2a/all-interfaces-report.json
```

结果：

```text
status = passed
operation_count = 154
call_count = 187
agent_id = all-agent-20260605083354283
trace_id = trace_all_interfaces_20260605083354283
```

新增能力真实 API 覆盖：

| 能力 | 路由/动作 | 状态 |
| --- | --- | --- |
| PreReply create | `POST /v1/intake/policies` | 201 |
| PreReply list | `GET /v1/intake/policies` | 200 |
| PreReply get | `GET /v1/intake/policies/{policy_id}` | 200 |
| PreReply update | `PUT /v1/intake/policies/{policy_id}` | 200 |
| PreReply evaluate | `POST /v1/intake/evaluate` | 200 |
| PreReply delete | `DELETE /v1/intake/policies/{policy_id}` | 200 |
| MCP health | `POST /v1/tool-providers/all-mcp-.../health` | 200 |
| MCP sync | `POST /v1/tool-providers/all-mcp-.../sync` | 200 |
| MCP invoke | `POST /v1/commands` with `tools.invoke` | 200 |
| A2A external lookup | `GET /v1/external-tasks/a2a/a2a-ext-...` | 200 |

业务断言：

```text
PreReply evaluate matched=true，dispatch=external_channel。
MCP tools/list 安装 all-mcp-*.sum，tools.invoke 返回 structuredContent.total=12。
A2A external task lookup 返回 remote status=working。
OpenAPI operation coverage missing = 0。
```

### 真实模型 Smoke

命令：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1 -ReportDir tmp/deepseek-smoke-20260605-prereply-mcp-a2a
```

报告：

```text
tmp/deepseek-smoke-20260605-prereply-mcp-a2a/deepseek-smoke-report.json
tmp/deepseek-smoke-20260605-prereply-mcp-a2a/diagnostics.json
```

结果：

```text
status = passed
model_base_url = https://api.deepseek.com
model_name = deepseek-v4-flash
eval_run_id = eval_cacfbdf0fb1e2140c75bd65de3ef91b0
eval_pass_rate = 1
eval_tool_misuse_rate = 0
failure_count = 0
run_id = run_5735a705d680cb793eae101bde245336
trace_id = trace_deepseek_smoke_20260605083754024
go_no_go = go
```

---

## 4. 文档与 OpenAPI

已更新：

```text
docs/PreReply_IntakePolicy_MCP_A2A扩展设计_v0.1.md
docs/PreReply_IntakePolicy_MCP_A2A重构任务规划_v0.1.md
docs/openapi.clean-core.v1.json
docs/openapi.yaml
scripts/e2e_clean_core_all_interfaces.ps1
config.example.json
```

OpenAPI 更新：

```text
新增 /v1/intake/* 六个 operation。
ToolProvider.provider_type 增加 mcp。
ToolExecutorSpec.type 增加 mcp。
```

---

## 5. 剩余风险

1. IntakePolicy 当前默认使用 in-memory store；接口已有 `Store` 抽象，生产持久化可继续补 Postgres store/migration。
2. MCP 本轮实现 HTTP JSON-RPC `tools/list` / `tools/call`，未实现 stdio transport、OAuth/session negotiation、resources/prompts。
3. A2A 本轮实现 `tasks/get`、`message/send`、Agent Card 的最小 CollaborationProvider 映射；完整 A2A streaming、push notification、复杂 artifact lifecycle 可后续扩展。
4. E2E 使用真实 HTTP、真实业务数据和真实模型；MCP/A2A 远端为本地 mock 协议服务器，用于稳定验证 adapter 行为。

---

## 6. 最终结论

本轮目标已完成：

```text
PreReply / IntakePolicy：完成
MCP ToolProvider adapter：完成
A2A external collaboration adapter：完成
设计文档和任务规划：完成
文档自查：完成
代码实现：完成
架构复查：完成
全接口真实 API E2E：通过
真实模型 smoke：通过
最终报告：完成
```
