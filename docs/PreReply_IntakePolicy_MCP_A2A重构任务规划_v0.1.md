# PreReply / MCP / A2A 重构任务规划

版本：v0.1
日期：2026-06-05
依据：`docs/PreReply_IntakePolicy_MCP_A2A扩展设计_v0.1.md`
定位：把三项扩展设计拆成可实现、可验证、可回放的工程任务。

---

## 0. 当前结论

本轮要实现三条链路：

```text
P1. PreReply / IntakePolicy
    新增可选入口预回复策略服务和 HTTP API。

P2. MCP ToolProvider adapter
    扩展 ToolCatalog 支持 MCP provider、tools/list 同步和 tools/call 执行。

P3. A2A external collaboration adapter
    新增 A2A CollaborationProvider adapter，并接入 external bridge config。
```

实施边界：

```text
Runtime Kernel 不新增控制流插件。
ToolRuntime 仍是工具执行唯一治理入口。
外部应用负责发送 pre_reply，CleanCore 只返回可审计决策。
```

---

## 1. 任务清单

### T1. 文档设计与任务规划

目标：

```text
输出三项能力设计文档和任务规划文档，并自查是否覆盖目标。
```

交付：

```text
docs/PreReply_IntakePolicy_MCP_A2A扩展设计_v0.1.md
docs/PreReply_IntakePolicy_MCP_A2A重构任务规划_v0.1.md
```

验收：

```text
文档包含 PreReply、MCP、A2A 的边界、数据模型、API、治理、测试要求和自查表。
```

状态：已完成

### T2. PreReply / IntakePolicy 服务

目标：

```text
新增 internal/intake 服务，支持 policy CRUD 和 deterministic evaluate。
```

要改/新增：

```text
internal/intake/service.go
internal/intake/service_test.go
internal/contracts/governance.go
internal/app/core/core.go
```

实现点：

```text
1. IntakePolicy 支持 always/exact/prefix/contains/regex。
2. 多 policy 按 priority 降序、updated_at 降序选择首个 enabled match。
3. EvaluateResult 返回 matched、policy_id、reply_text、reply_kind、continue_to_run、dispatch=external_channel。
4. policy upsert/delete 写 Audit。
5. evaluate 写 TraceIntakePreReplyEvaluated。
6. Core 初始化 Intake service。
```

验收：

```text
go test ./internal/intake ./internal/app/core
```

状态：已完成

### T3. PreReply HTTP API 和 OpenAPI

目标：

```text
暴露 PreReply policy 和 evaluate API，并纳入 OpenAPI。
```

要改：

```text
internal/server/server.go
internal/server/server_test.go
docs/openapi.clean-core.v1.json
docs/openapi.yaml
scripts/e2e_clean_core_all_interfaces.ps1
```

实现点：

```text
1. POST /v1/intake/policies
2. GET /v1/intake/policies
3. GET /v1/intake/policies/{policy_id}
4. PUT /v1/intake/policies/{policy_id}
5. DELETE /v1/intake/policies/{policy_id}
6. POST /v1/intake/evaluate
```

验收：

```text
server tests 覆盖 CRUD/evaluate。
all-interface E2E 覆盖所有新增 operations。
```

状态：已完成

### T4. MCP ToolProvider adapter

目标：

```text
ToolCatalog 支持 provider_type=mcp，executor.type=mcp。
```

要改：

```text
internal/tool/catalog/catalog.go
internal/tool/catalog/catalog_test.go
```

实现点：

```text
1. ProviderTypeMCP / ExecutorTypeMCP。
2. validateProvider 允许 mcp。
3. CheckProviderHealth 对 mcp 使用 tools/list JSON-RPC。
4. SyncProviderCatalog 对 mcp 使用 tools/list 生成 manifest。
5. MCPExecutor 使用 tools/call 执行。
6. provider/group/status/tenant guard 仍生效。
7. MCP result.isError 映射为 ToolExecutionFailed。
```

验收：

```text
go test ./internal/tool/catalog ./internal/tool/runtime ./internal/tool/invoke
```

状态：已完成

### T5. A2A external collaboration adapter

目标：

```text
新增 internal/bridge/a2a HTTP adapter，实现 contracts.CollaborationProvider。
```

要改/新增：

```text
internal/bridge/a2a/http_adapter.go
internal/bridge/a2a/http_adapter_test.go
internal/app/config/config.go
internal/app/config/config_test.go
internal/app/core/core.go
config.example.json
```

实现点：

```text
1. Config 增加 external_bridge_provider=array/a2a。
2. Core 根据 provider 选择 array.HTTPAdapter 或 a2a.HTTPAdapter。
3. A2A GetTask -> JSON-RPC tasks/get。
4. A2A SendMessage -> JSON-RPC message/send。
5. A2A AttachArtifact -> message/send data part。
6. A2A GetParticipants -> Agent Card。
7. A2A CheckAccess -> 远程 task 可达性 + 本地 binding gate。
```

验收：

```text
go test ./internal/bridge/a2a ./internal/bridge/array ./internal/app/config ./internal/app/core
```

状态：已完成

### T6. 全接口真实 API E2E

目标：

```text
每个 OpenAPI operation 都有真实 HTTP 调用和真实数据。
新增 PreReply/MCP/A2A 场景必须进入 all-interface E2E。
```

要改：

```text
scripts/e2e_clean_core_all_interfaces.ps1
```

实现点：

```text
1. host mock 增加 MCP JSON-RPC tools/list/tools/call。
2. host mock 增加 A2A JSON-RPC tasks/get/message/send 和 Agent Card。
3. 脚本调用 PreReply policy CRUD/evaluate。
4. 脚本创建 MCP provider、health、sync，并通过 tools.invoke 调用 MCP tool。
5. 脚本以 provider=a2a 建立 external task binding 并读取外部任务。
6. 报告断言 missing operations = 0。
```

验收：

```text
powershell -File scripts/e2e_clean_core_all_interfaces.ps1 -ReportDir tmp/clean-core-all-interfaces-YYYYMMDD-prereply-mcp-a2a
all-interfaces-report.json status = passed
missing operations = 0
```

状态：已完成

### T7. 真实模型 smoke

目标：

```text
如果 .env 已配置真实模型 key/base_url，运行真实模型 smoke；
若失败，要记录失败原因而不是静默跳过。
```

要用：

```text
scripts/e2e_deepseek_smoke.ps1
.env
```

验收：

```text
deepseek-smoke-report.json status = passed
eval_pass_rate = 1
eval_tool_misuse_rate = 0
```

状态：已完成

### T8. 架构复查和最终报告

目标：

```text
依据代码、设计文档、任务规划、测试结果复查三项扩展是否符合 CleanCore 解耦边界。
```

交付：

```text
docs/PreReply_MCP_A2A实现与真实API测试报告_20260605.md
```

报告内容：

```text
1. 设计目标逐项结论。
2. 代码改动清单。
3. 架构边界复查。
4. go test ./... 结果。
5. all-interface E2E 结果。
6. 每个路由 operation coverage 结果。
7. 真实模型 smoke 结果。
8. 剩余风险和后续建议。
```

状态：已完成

---

## 2. 文档自查

| 检查项 | 结论 |
| --- | --- |
| 是否覆盖用户指定三项 | 已覆盖：PreReply/IntakePolicy、MCP ToolProvider、A2A external collaboration。 |
| 是否明确框架内外边界 | 已覆盖：PreReply 只决策不发送；MCP/A2A 只作为 adapter。 |
| 是否包含代码任务 | 已覆盖 T2-T5。 |
| 是否包含真实 API 测试 | 已覆盖 T6。 |
| 是否包含真实模型测试 | 已覆盖 T7。 |
| 是否包含最终报告 | 已覆盖 T8。 |
| 是否保留 Runtime Kernel 边界 | 已覆盖。 |

---

## 3. 任务状态跟踪

| 任务 | 状态 |
| --- | --- |
| T1 文档设计与任务规划 | 已完成 |
| T2 PreReply / IntakePolicy 服务 | 已完成 |
| T3 PreReply HTTP API 和 OpenAPI | 已完成 |
| T4 MCP ToolProvider adapter | 已完成 |
| T5 A2A external collaboration adapter | 已完成 |
| T6 全接口真实 API E2E | 已完成 |
| T7 真实模型 smoke | 已完成 |
| T8 架构复查和最终报告 | 已完成 |
