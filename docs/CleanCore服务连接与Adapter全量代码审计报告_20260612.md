# CleanCore 服务连接与 Adapter 全量代码审计报告 2026-06-12

## 1. 审计结论

本次审计对象是当前 git 工作区中围绕服务连接、ToolProvider、AdapterOperation、ToolManifest、运行时、OpenAPI 和验证脚本的全量改动。

总体判断：当前代码主线基本满足《CleanCore 服务连接与 Adapter 开发任务文档 v0.1》的方向，核心模型是干净的：

```text
ServiceConnection
  管连接资产：base_url / auth_type / auth_ref / network_scope / health / resources
        |
        | 被外部 ToolProvider 或托管 AdapterOperation 引用
        v
ToolProvider / Managed Adapter Provider
        |
        | sync / generate / publish
        v
ToolManifest
        |
        | Agent 最终绑定 tool_id
        v
ToolRuntime
```

没有发现阻断级问题。审计发现的 4 个风险点已在本轮处理闭环：

- HTTP API Adapter 所有 invoked 后失败路径统一写入 `tool_provider.failed`。
- `DELETE /v1/service-connections/{connection_id}` 增加 impact 依赖拦截，避免删除后留下悬空 Provider/Operation/Manifest。
- ServiceConnection 因连接身份变化清空资源目录时，Postgres store 改为连接写入与资源替换同事务。
- Database Adapter 增加查询关系约束，`query_template` 的 `FROM/JOIN` 关系必须落在绑定的 `resource_id` 上。

## 2. 主要发现

### F1. 已修复：HTTP API Adapter 部分失败路径没有写入 provider failed trace

位置：

- `internal/tool/catalog/catalog.go:2895` 先记录 `tool_provider.invoked`
- `internal/tool/catalog/catalog.go:2904` 到 `2930` 请求映射、path 参数、query/header/body 映射失败时直接 `return err`
- `internal/tool/catalog/catalog.go:2933` 到 `2948` 只有真实 HTTP 请求失败时记录 `tool_provider.failed`
- `internal/tool/catalog/catalog.go:2949` 到 `2956` 响应解码或 response mapping 失败时直接 `return err`
- `internal/server/handlers_service_connections.go:713` 到 `742` 服务连接 usage 只统计 `tool_provider.failed` 作为 provider 失败

影响：

HTTP API Adapter 调用如果失败在“请求发出前”或“响应返回后映射阶段”，ToolRuntime 仍会记录总体 `tool.failed`，但服务连接 usage/trace 维度不会统计为 provider failure。这样会造成：

- `/v1/service-connections/{connection_id}/usage` 的 failures_total 偏低。
- 运维看服务连接调用质量时，只能看到 invoked，没有对应 failed/completed。
- 定位 schema/mapping 配置错误时，trace 证据链不完整。

处理：

- `HTTPAPIAdapterExecutor.Execute` 新增 `recordProviderFailure` helper。
- path 参数、query/header/body 映射、HTTP 请求、响应解码、response mapping 失败都会记录 `TraceToolProviderFailed`。
- 新增 `TestHTTPAPIAdapterRecordsProviderFailureForMappingErrors`，覆盖 path 参数缺失时的 provider failed trace。

### F2. 已修复：删除 ServiceConnection 时没有处理依赖的 Provider / Operation / Manifest

位置：

- `internal/server/handlers_service_connections.go:327` 到 `332` DELETE 直接调用 `ServiceConnections.Delete`
- `internal/serviceconnection/service.go:337` 到 `353` 只删除连接、资源、健康事件、轮换记录
- `internal/storage/postgres/service_connections.go:111` 到 `129` Postgres 删除事务只清理 `service_connection_*` 表
- `internal/tool/catalog/catalog.go:912` 到 `999` 已有 `ServiceConnectionImpact` 能算影响范围，但 DELETE 没用它做拦截或级联治理

影响：

管理员删除连接后，仍可能留下：

- `ToolProvider.service_connection_id` 指向不存在的连接。
- `AdapterOperation.service_connection_id` 指向不存在的连接。
- 已发布的 `ToolManifest` 仍处于 enabled，但运行时 availability 才发现连接不存在。

这不会绕过执行安全，因为运行时会通过 `providerConnectionRef` / `operationConnection` 拦住，但产品上会出现“目录里看起来可用，实际调用失败”的悬空状态。

处理：

- `DELETE /v1/service-connections/{connection_id}` 在删除前调用 `ServiceConnectionImpact`。
- 只要存在依赖的 `ToolProvider`、`AdapterOperation` 或 `ToolManifest`，接口返回策略错误，要求先解绑/禁用依赖。
- OpenAPI YAML/JSON 已补充删除接口说明和 400 响应。
- `TestServiceConnectionImpactIncludesProvidersOperationsAndTools` 增加依赖删除拦截断言。

### F3. 已修复：ServiceConnection 更新后清理资源目录不是同一事务语义

位置：

- `internal/serviceconnection/service.go:176` 到 `188` 先 `UpsertConnection`，再在 `clearResources` 时调用 `ReplaceResources(nil)`
- `internal/storage/postgres/service_connections.go:159` 起 `ReplaceResources` 使用单独事务

影响：

当连接的 base_url/auth_ref/metadata 变更导致资源目录需要清空时，如果连接写入成功但资源清空失败，会短暂留下“新连接配置 + 旧资源目录”的不一致状态。正常情况下概率不高，但这类目录资源会被 `from-resource` 和数据库 Adapter 校验使用，出错时会让后续工具生成判断变脏。

处理：

- `serviceconnection.Store` 增加 `UpsertConnectionAndReplaceResources`。
- Postgres `ServiceConnectionStore` 用同一事务完成连接 upsert 与资源目录 replace。
- Service 内存路径也在同一个锁保护区清空资源缓存。
- 新增 `TestServiceConnectionUpsertClearsResourcesAtomicallyWhenScopeChanges`，覆盖连接身份变化时调用组合写接口并清空资源目录。

### F4. 已修复并保留权限兜底要求：Database Adapter 的只读约束主要是语法级，仍需数据库权限兜底

位置：

- `internal/tool/catalog/catalog.go:1883` 到 `1899` 校验 `query_template`、`read_only`、`parameter_schema`、`resource_id`
- `internal/tool/catalog/catalog.go:1953` 到 `1977` 校验 `resource_id` 必须来自连接资源目录
- `internal/tool/catalog/catalog.go:3222` 到 `3245` 使用 `isReadOnlySQL` 进行基础只读判断
- `internal/tool/catalog/catalog.go:3031` 到 `3058` 执行时直接用服务连接 DSN 查询

影响：

当前实现已经能挡住明显写 SQL、分号、多语句、混入 HTTP 字段等问题，也要求绑定已发现资源。本轮进一步增加了关系约束：`query_template` 中 `FROM/JOIN` 引用到的关系必须等于绑定的 `resource_id` / 资源 SQL identifier。仍需数据库只读账号兜底，因为代码没有完整 SQL AST 解析，也无法证明数据库函数无副作用。

处理：

- `validateDatabaseOperationResource` 增加 `validateDatabaseQueryTargetsResource`。
- `databaseQueryRelationIdentifiers` 提取 `FROM/JOIN` 后的关系名，支持常见 quoted identifier、schema.table 和 CTE 场景。
- `query_template` 必须显式引用绑定资源，跨资源查询会返回 `CodeToolPolicyDenied`。
- `TestDatabaseAdapterOperationPublishesAndExecutesReadOnlyQuery` 增加跨资源查询拒绝断言。
- 仍建议真实企业数据库使用只读账号，并在后续引入更强 SQL parser / table allowlist policy。

## 3. 开发目标符合性审计

| 目标 | 审计结果 | 证据 |
| --- | --- | --- |
| 服务连接作为连接资产，不直接作为工具 | 通过 | `ServiceConnection` 只管理连接、健康、资源、密钥轮换；`ToolManifest` 仍由 Provider/Adapter 产生 |
| ToolProvider 只引用服务连接，不保存 endpoint/auth 旧字段 | 通过 | `ToolProvider` 只有 `service_connection_id`；handler 拒绝 `endpoint/endpoint_ref/auth_ref/secret_ref/token_ref` |
| managed adapter provider 不允许用户直接创建 | 通过 | handler 对 `http_api_adapter/database_adapter` public write 直接拒绝 |
| HTTP Direct 从公开执行器路径移除 | 通过 | `ExecutorSpec` 只有 `type/provider_id/operation`；`http_direct` 只保留负向测试和契约拒绝 |
| HTTP API 工具从服务连接 + AdapterOperation 生成 | 通过 | `managed_http_api_adapter`、`service_connection_id`、`method/path/mapping/schema` 主链路已实现 |
| 数据库查询工具从数据库连接 + AdapterOperation 生成 | 基本通过 | Postgres 资源发现、resource_id 绑定、只读 SQL、redact_columns 已实现；只读边界见 F4 |
| 服务连接资源目录 | 通过 | HTTP OpenAPI path 可发现 `http_operation`；Postgres 可发现 table/view |
| 服务连接 impact / usage | 基本通过 | impact 和 usage 接口已实现；usage 完整性见 F1 |
| 不为了兼容牺牲干净 | 通过 | 旧 resource envelope、旧 secret_ref/token_ref、旧 provider endpoint 字段均被拒绝 |
| OpenAPI 和契约脚本同步 | 通过 | `verify_contracts.ps1` 已覆盖旧字段缺失、路径、schema 要求 |

## 4. 代码契合度分析

### 4.1 Domain 层

`internal/serviceconnection` 的职责边界清楚：连接类型、认证引用、网络边界、健康检测、资源发现、密钥轮换都在这里；它没有承担工具注册职责。这个和设计文档里“服务连接不是工具来源”的结论一致。

当前连接类型包括 `clean_core/http_api/database/webhook/oauth/cache/queue/storage`。其中 `cache/queue/storage` 的 test 明确返回 unknown + not implemented，没有伪装成 healthy，这个处理是干净的。

### 4.2 Catalog 层

`internal/tool/catalog` 当前分层比较合理：

- 外部工具来源：`static_tool_host`、`agent_plugin_service`、`mcp`
- 托管 Adapter Provider：`managed_http_api_adapter`、`managed_database_adapter`
- 最终目录对象：`ToolManifest`

`ToolProvider` 是“能同步/执行一批工具的主体”，`ServiceConnection` 是“被引用的连接资产”，`ToolManifest` 是 Agent 真正绑定的工具单元。这个模型可以解释前端上“工具来源、工具目录、服务连接”的差异。

### 4.3 Runtime 层

`internal/tool/runtime/runtime.go` 在执行前做 availability、input schema、policy、domain/profile、credential/data boundary；执行后校验 output schema。服务连接网络边界能进入 ExecutionProfile，外部 Provider 和 HTTP Adapter 可以带 network scope。数据库 Adapter 没有 HTTP 网络边界，但受连接 DSN、只读 SQL 和数据库账号权限约束。

### 4.4 Server 层

REST 基础接口覆盖完整：

- `/v1/service-connections`
- `/v1/service-connections/{id}`
- `/test`、`/enable`、`/disable`、`/resources`、`/health-events`、`/impact`、`/usage`、`/secret-rotations`
- `/v1/tool-providers`
- `/v1/tool-providers/{id}/sync`
- `/v1/tool-providers/{id}/health`
- `/v1/tool-providers/{id}/operations`
- `/operations/from-resource`
- `/operations/{operation_id}/test`
- `/operations/{operation_id}/publish`
- `/v1/tool-manifests`

目前不缺主干接口。更应该补的是依赖治理语义，而不是继续新增并列入口。

## 5. OpenAPI 与脚本审计

OpenAPI 已经按新模型收口：

- ToolProvider schema 不暴露 `endpoint/auth_ref/secret_ref/token_ref`。
- ToolExecutorSpec 不暴露 `url/method/headers` 这类 HTTP Direct 字段。
- AdapterOperation 明确要求 `service_connection_id`，并区分 HTTP API 和 Database 字段。
- `AdapterOperationFromResourceRequest` 要求 `service_connection_id/resource_id`。
- 服务连接写请求不接受健康观测字段，健康字段由 test/health 流程写入。

`scripts/verify_contracts.ps1` 已经把这些作为契约断言，能防止后续误把旧模型加回来。

风险点：`docs/openapi.clean-core.v1.json` 体量大，手动维护成本高。当前校验能兜住关键字段，但长期建议生成化或至少加更细的 schema diff 检查。

## 6. 测试与验证结果

已执行：

```powershell
& "$env:USERPROFILE\sdk\go1.23.3\bin\go.exe" test ./... -count=1
```

结果：通过。

```powershell
.\scripts\verify_contracts.ps1
```

结果：通过，输出 `Contract verification passed`。

```powershell
[System.Management.Automation.PSParser]::Tokenize((Get-Content scripts\verify_contracts.ps1 -Raw), [ref]$errors)
[System.Management.Automation.PSParser]::Tokenize((Get-Content scripts\e2e_clean_core_all_interfaces.ps1 -Raw), [ref]$errors)
```

结果：两个脚本解析通过。

```powershell
Get-Content docs/openapi.clean-core.v1.json -Raw | ConvertFrom-Json
```

结果：JSON 解析通过，未检测到 BOM。

```powershell
git diff --check
```

结果：通过。命令输出只有 Git 的 LF/CRLF 提示，不是 diff-check 失败。

未执行：

- 未启动真实服务跑全量 HTTP e2e。
- 未连接真实 Postgres 实例跑 live migration/readiness。
- 未对真实外部 ToolHost/MCP/HTTP API 服务做联调。

## 7. 处理结果

1. F1 已处理：HTTP API Adapter 所有 invoked 后错误路径统一记录 `tool_provider.failed`。
2. F2 已处理：服务连接删除增加 impact 拦截；有 Provider、AdapterOperation 或 ToolManifest 依赖时拒绝删除。
3. F3 已处理：服务连接身份变化导致资源目录清空时，Postgres store 使用连接写入和资源替换同事务。
4. F4 已处理到当前阶段：Database Adapter 增加 `FROM/JOIN` 关系绑定校验；真实企业数据库仍必须使用只读账号兜底。
5. 已补测试：HTTP adapter 失败 trace、服务连接依赖删除拦截、资源清理组合写接口、数据库跨资源查询拒绝、SQL relation 提取边界。

## 8. 最终判断

这批改动没有偏离整体设计，反而把之前容易混淆的 HTTP Direct、ToolHost、服务连接、工具目录关系收束到了更干净的模型里。

当前代码可以作为下一步产品 OpenAPI/前端页面调整的后端基础。进入更大范围联调前，剩余重点不是主干接口缺口，而是真实外部服务联调、真实 Postgres 权限验证，以及后续是否引入更强 SQL parser / table allowlist policy。
