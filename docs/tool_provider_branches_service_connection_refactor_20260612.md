# ToolProvider 持久化分支对齐 ServiceConnection 新实现重构方案

日期：2026-06-12

本文针对远程两个改动线：

- `ee7128e33d307ff063921f296dfec0d12b4ffbfa`：`feat: persist tool groups and providers`
- `8c30093f712deee35f519d8ce83d58c13a456c7f`：`Persist tool provider secret refs`

目标不是把这两个提交原样合入，而是基于当前 CleanCore 新实现，判断哪些设计意图可以吸收，哪些代码必须改写，哪些应直接废弃。

## 1. 当前新实现的事实基线

当前代码已经把“连接资产”和“工具来源”拆开：

```text
ServiceConnection
  管 base_url / auth_type / auth_ref / network_scope / health / resources
        |
        | 被 Provider 或 AdapterOperation 引用
        v
ToolProvider
  外部工具来源：static_tool_host / agent_plugin_service / mcp
  托管工具化能力：http_api_adapter / database_adapter
        |
        | sync / publish
        v
ToolManifest
  Runtime 最终可调用工具
```

关键约束：

- `ToolProvider` 只保存 `service_connection_id`，不保存 `endpoint/auth_ref/secret_ref/token_ref`。
- `ServiceConnection` 是连接事实源，负责 `base_url/auth_type/auth_ref/network_scope/health/resources`。
- `AdapterOperation` 是 HTTP API / Database 工具化草稿事实源，发布后生成 `ToolManifest`。
- `ToolGroup` 保持轻模型，只表达 `group_id/name/description/status/version`，不保存 `provider_id/tool_ids/metadata` 快照。
- public write payload 不接受资源壳：`{"provider": {...}}`、`{"group": {...}}`、`{"manifest": {...}}`、`{"operation": {...}}` 等均应拒绝。
- `PUT` 是全量替换，`PATCH` 才是局部合并。
- 健康字段由测试/探活写入，公开 create/update 不接收 `health_status/last_health_*`。
- `scripts/verify_contracts.ps1` 已明确断言 ToolProvider schema 不含 `endpoint/auth_ref/secret_ref/token_ref`。

这些约束优先级高于旧分支中的兼容字段和默认分组逻辑。

## 2. 总体结论

`ee7128e` 的“Provider/Group/Manifest 持久化”方向可以保留，但实现必须按当前 ServiceConnection + AdapterOperation 模型重写。

`8c30093` 的“在 ToolProvider 上持久化 secret_ref”方向不应保留。密钥引用应统一进入 `ServiceConnection.auth_ref`，轮换进入 `service_connection_secret_rotations`，响应和审计只暴露 `auth_ref_set` 或 ref hash，不暴露真实引用值到不该出现的面。

两个旧提交都不应直接 cherry-pick 到当前主线。正确做法是拆成三个独立重构线：

1. ToolCatalog 持久化与 restore 对齐当前模型。
2. ServiceConnection 连接、认证、健康、资源发现闭环。
3. Agent package loader / skill 展示类旁路改动单独审查，不能混在 ToolProvider 持久化分支里。

## 3. `ee7128e` 改造方案

### 3.1 保留的意图

可以保留：

- ToolProvider、ToolGroup、ToolManifest 从内存恢复到 Postgres 的事实源思路。
- 启动时 `Restore` 后重新注册 runtime registry 的思路。
- provider sync 后将远端目录转为 `ToolManifest` 的主流程。
- 对 Provider/Group/Manifest resource API 增补测试的方向。
- 让 Postgres 中发布的 AgentDefinition 可被运行时加载的需求，但必须拆到 Agent package loader 分支。

### 3.2 必须删除或重写的内容

必须删除或重写：

- 不新增 `tool_groups.provider_id`。
- 不新增 `tool_groups.tool_ids_json`。
- 不新增 `tool_groups.metadata_json`。
- 不引入 `ProviderCatalogSyncResult` 作为默认返回契约。
- 不创建 `<provider>-default` 默认 group 并强行覆盖远端 tool `group_id`。
- 不把 `ToolGroup` 当 provider package 快照表。
- 不在同一个提交里改 `PackageStore.Load`、`ChainLoader`、`handlers_agent_skills.go`。
- 不修改已发布迁移 `001` 的 checksum。

### 3.3 ToolGroup 重构

旧分支把 group 改成：

```go
type ToolGroup struct {
    ProviderID string
    ToolIDs []string
    Metadata map[string]any
}
```

当前模型应保持：

```go
type ToolGroup struct {
    TenantID contracts.TenantID
    GroupID string
    Name string
    Description string
    Status string
    Version string
}
```

工具和 group 的关系由 `ToolManifest.GroupID` 表达。需要查询“某 group 下有哪些工具”时，从 manifests 反查，不维护 `tool_ids_json` 双写快照。

原因：

- 避免 `ToolManifest.GroupID` 和 `ToolGroup.ToolIDs` 漂移。
- 避免 provider sync 半失败后 group 快照不准。
- 保持 group 是治理边界，不是目录同步结果包。

### 3.4 Provider sync 重构

旧分支的问题：

```go
groupID := providerDefaultGroupID(provider.ProviderID)
for _, item := range catalog.Tools {
    item.GroupID = groupID
    ...
}
```

新实现应保留远端 catalog 的 `group_id`：

```go
manifest := ToolManifest{
    GroupID: item.GroupID,
    ...
}
```

如果远端没有 `group_id`，可以保持为空；是否展示“未分组”由 UI 或查询层处理。不要后端猜测默认组并写入事实源。

如果产品确实需要“Provider 默认分组”，应做成显式操作：

- 创建普通 `ToolGroup`：`POST /v1/tool-groups`
- 同步时只对缺省 `group_id` 的工具使用显式配置的 default group
- default group 配置不应藏在 provider sync 里

### 3.5 Store 和迁移重构

旧 `002_tool_group_provider_package.sql` 不应原样保留。

当前目标 schema 应是：

- `tool_providers.service_connection_id`
- `tool_adapter_operations.service_connection_id`
- `tool_groups` 只保留 group 基础字段
- `tool_manifests.group_id`
- `service_connections.auth_ref`
- `service_connection_secret_rotations`

如果目标分支已经包含旧 `002`，处理方式：

1. 如果尚未发布到任何环境：直接改写或删除旧 `002`。
2. 如果已经发布：新增迁移移除旧字段或停止使用旧字段，但不要修改已发布迁移文件和 checksum。
3. 给 SQL 固定换行策略，避免 Windows checkout 造成 checksum 不一致。

建议新增 `.gitattributes`：

```text
*.sql text eol=lf
*.json text eol=lf
*.ps1 text eol=crlf
```

同时迁移 checksum 脚本继续以文件字节为准，避免隐式文本转换。

### 3.6 Agent loader 旁路改动

`ee7128e` 里新增 `ChainLoader`、`PackageStore.Load`、`resolveRunnableAgentVersion`，这个需求合理，但不能混在 ToolProvider 分支。

建议拆成独立分支：

```text
feat/postgres-agent-definition-loader
```

该分支需要单独回答：

- Postgres package release 是不是运行时 AgentDefinition 的权威来源？
- 当 tenant 版本不存在时，是否允许 fallback 到 tenant_id='' 的静态定义？
- 空 version 时选择 active/default/stable/canary 的优先级是否和 `ensureRunnableAgentVersion` 一致？
- 如果 release 已 disabled，loader 是否仍可加载？加载和可运行检查边界如何划分？

验收测试应覆盖：

- 无数据库时仍用 static loader。
- 有数据库时优先加载 package release。
- tenant 隔离。
- active/default/stable/canary 解析。
- not found 只在 `CodeAgentVersionNotFound` 时 fallback。

### 3.7 Agent skills 展示改动

`handlers_agent_skills.go` 的 active projection 合并逻辑也应拆出。它和工具目录持久化无直接关系。

如果保留，应补测试：

- base definition 有 skill，active projection 覆盖同 `skill_id/version`。
- active projection 新增 skill。
- base agent 找不到时是否允许只返回 active projections。
- `err != nil || !found` 时吞掉错误的行为要谨慎。建议只在 `CodeAgentVersionNotFound` 时降级，其他错误应返回。

## 4. `8c30093` 改造方案

### 4.1 不保留 ToolProvider.SecretRef

旧分支新增：

```go
ToolProvider.SecretRef string `json:"secret_ref,omitempty"`
```

当前新实现中必须删除或拒绝：

- Go model 不加 `SecretRef`
- handler 不解析 `secret_ref`
- OpenAPI 不暴露 `secret_ref`
- Postgres 不新增 `tool_providers.secret_ref`
- e2e 正向路径不提交 `secret_ref`
- 只在负向测试中断言 `secret_ref` 被拒绝

原因：

- Provider 不再持有认证事实。
- `secret_ref` 和 `auth_ref` 双字段会制造语义分叉。
- 旧提交里 `SecretRef` 没有进入执行路径，只是落库并返回，属于无闭环字段。

### 4.2 用 ServiceConnection Secret Rotation 替代

如果旧分支想表达“持久化密钥引用并可轮换”，应改为：

```http
POST /v1/service-connections/{connection_id}/secret-rotations
```

请求：

```json
{
  "auth_type": "bearer",
  "auth_ref": "secret://tenant_1/crm/new-token",
  "reason": "scheduled rotation"
}
```

落库：

- `service_connections.auth_type`
- `service_connections.auth_ref`
- `service_connection_secret_rotations.previous_auth_ref_hash`
- `service_connection_secret_rotations.new_auth_ref_hash`

响应可以返回 `auth_ref` 在 connection 上的引用值，但审计、trace、governance、replay 中不得泄露；rotation history 只返回 hash。

### 4.3 PATCH 合并逻辑保留，但只用于 PATCH

`8c30093` 修了一个真实问题：PATCH 只提交 `secret_ref` 时不应清空 endpoint/health 等字段。

当前新实现已经用正确边界处理：

- `PUT /v1/tool-providers/{provider_id}`：全量替换。
- `PATCH /v1/tool-providers/{provider_id}`：`mergeToolProviderPatch` 局部合并。
- PATCH 不接受 `endpoint/auth_ref/secret_ref/health_status/last_health_*`。

旧分支中的 merge 思路可以保留，但目标字段只限：

- `provider_type`
- `name`
- `description`
- `service_connection_id`
- `status`
- `version`

### 4.4 Provider health 重构

旧分支基于 `provider.Endpoint/AuthRef` 做健康检查。

新实现必须通过连接解析：

```go
connection, err := s.providerConnection(ctx, provider)
```

健康检查使用：

- `connection.BaseURL`
- `connection.AuthRef`
- `connection.TimeoutMS`
- `connection.RetryMax`
- `connection.NetworkScope`

Provider 自身只记录 health 观测结果，不保存连接配置。

## 5. 文件级改造清单

### 5.1 `internal/tool/catalog/catalog.go`

应保留/对齐：

- `ToolProvider.ServiceConnectionID`
- `AdapterOperation`
- `SetServiceConnections`
- `providerConnection`
- `operationConnection`
- `connectionHeaders`
- managed adapter provider 逻辑
- `manifestForAdapterOperation`
- `SyncProviderCatalog` 对 managed adapter 从 operations 发布 manifests

应删除/避免：

- `ToolProvider.Endpoint`
- `ToolProvider.AuthRef`
- `ToolProvider.SecretRef`
- `ToolGroup.ProviderID`
- `ToolGroup.ToolIDs`
- `ToolGroup.Metadata`
- `ProviderCatalogSyncResult`
- `providerDefaultGroupID`
- sync 时覆盖 `item.GroupID`

### 5.2 `internal/server/handlers_tool_catalog.go`

应保留/对齐：

- `rejectResourceEnvelopePayload(payload, "provider")`
- `rejectUnsupportedToolProviderPayloadFields`
- managed adapter provider public write 拒绝
- `PUT` 和 `PATCH` 分离
- `mergeToolProviderPatch`
- `/operations`
- `/operations/from-resource`
- operation publish/test

应删除/避免：

- 兼容 `{"provider": {...}}`
- 解析 `endpoint/auth_ref/secret_ref/token_ref`
- create/update 接受健康字段
- PUT 走 PATCH merge 逻辑

### 5.3 `internal/serviceconnection/service.go`

这是认证和连接事实源。需要确保：

- `auth_type/auth_ref` 成对校验。
- `auth_type=none` 时 `auth_ref` 必须为空。
- `auth_ref` 不接受真实明文 secret。
- `secret_ref/token_ref` 只作为负向兼容拒绝字段。
- health/test 可发现 resources。
- secret rotation 只记录 ref hash。

### 5.4 `internal/storage/postgres/postgres.go`

ToolCatalog store 应只持久化当前模型字段：

- `tool_providers.service_connection_id`
- `tool_adapter_operations.*`
- `tool_groups` 基础字段
- `tool_manifests.executor_json`

不应再 SELECT/INSERT：

- `tool_providers.endpoint`
- `tool_providers.auth_ref`
- `tool_providers.secret_ref`
- `tool_groups.provider_id`
- `tool_groups.tool_ids_json`
- `tool_groups.metadata_json`

### 5.5 `internal/storage/postgres/service_connections.go`

ServiceConnection store 是连接字段的唯一持久化位置：

- `base_url`
- `auth_type`
- `auth_ref`
- `network_scope`
- `timeout_ms`
- `retry_max`
- `health_status`
- `resources`
- `secret_rotations`

### 5.6 `docs/openapi.yaml` 和 `docs/openapi.clean-core.v1.json`

必须与 `scripts/verify_contracts.ps1` 对齐：

- ToolProvider schema 不含 `endpoint/auth_ref/secret_ref/token_ref`。
- ToolProvider upsert 只允许外部工具来源 provider type。
- managed adapter provider 只能通过 AdapterOperation 使用。
- ServiceConnection schema 描述 auth pair 和 plain secret 禁止。
- AdapterOperation schema 明确 HTTP-only 和 Database-only 字段边界。

## 6. 数据迁移策略

### 6.1 开发期未发布环境

如果旧分支未进入真实环境：

- 删除旧 `002_tool_group_provider_package.sql`。
- 删除旧 `003_tool_provider_secret_ref.sql`。
- 直接采用当前 `001_clean_core_base.sql` 中的 ServiceConnection + AdapterOperation schema。
- 重新生成 checksum，固定换行。

### 6.2 已有旧 provider 数据的环境

如果已有旧字段：

```text
tool_providers.endpoint
tool_providers.auth_ref
tool_providers.secret_ref
```

迁移步骤：

1. 为每条 provider 创建一条 `service_connections`。
2. `connection_id` 建议可预测：`conn_<provider_id>` 或 `provider_<provider_id>_connection`。
3. `base_url = old.endpoint`。
4. `auth_ref = old.auth_ref`。
5. `auth_type` 不允许猜。如果旧数据没有 auth type，应由迁移配置或人工映射给出。
6. `tool_providers.service_connection_id = new connection_id`。
7. 删除或停止读取旧 provider 认证字段。
8. `secret_ref` 不自动迁移到 provider。若它实际是认证引用，人工确认后写入 `service_connections.auth_ref` 并记录 rotation hash。

### 6.3 Group 数据

旧 `tool_groups.provider_id/tool_ids_json/metadata_json` 不迁移为事实字段。

如果 UI 需要展示，可以通过查询生成：

```text
group -> manifests where manifest.group_id = group.group_id
provider -> manifests where manifest.executor.provider_id = provider.provider_id
```

不要把派生视图回写进 group 表。

## 7. 测试改造清单

### 7.1 必补单元测试

ToolCatalog：

- Provider create/update 不接受 `endpoint/auth_ref/secret_ref/token_ref`。
- PATCH 只合并 `service_connection_id/status/name/description/version`。
- PUT 全量替换，缺失必填字段时报错。
- SyncProviderCatalog 保留远端 `group_id`。
- Provider health 通过 ServiceConnection 解析 base_url/auth_ref。
- ServiceConnection disabled 时 provider health/sync/invoke 拒绝。
- Managed adapter sync 从 enabled operations 生成 manifests。
- AdapterOperation publish 后 manifest executor 正确。

ServiceConnection：

- `auth_type/auth_ref` 必须成对。
- `auth_type=none` 不允许 `auth_ref`。
- `secret_ref/token_ref` 被拒绝。
- rotation 只保存 hash。
- HTTP API resource discovery 生成 `http_operation`。
- Database resource discovery 生成 table/view。

Storage：

- Restore 后 provider、operation、group、manifest 全部回到内存。
- 旧 schema 缺字段时不要静默返回空事实源。要么明确报 schema not ready，要么提供受控 legacy fallback。
- checksum 在 Windows/Linux checkout 下稳定。

### 7.2 必补接口测试

- `POST /v1/tool-providers` 提交 `auth_ref` 返回 400。
- `POST /v1/tool-providers` 提交 `provider` 资源壳返回 400。
- `PATCH /v1/tool-providers/{id}` 提交 `health_status` 返回 400。
- `POST /v1/service-connections` 提交 `secret_ref` 返回 400。
- `PATCH /v1/service-connections/{id}` 只提交 `auth_ref` 返回 400。
- `POST /v1/service-connections/{id}/secret-rotations` 缺 `auth_type` 返回 400。
- `POST /v1/tool-providers/managed_http_api_adapter/operations/from-resource` 可从 HTTP resource 生成 operation。
- `POST /v1/tool-providers/managed_database_adapter/operations/from-resource` 可从 table/view 生成 operation。

### 7.3 E2E 验收

建议保留并强化：

```powershell
scripts/verify_contracts.ps1
scripts/check_migration_checksums.ps1 -ValidateOnly
scripts/e2e_clean_core_all_interfaces.ps1
scripts/e2e_clean_core_mmr_service.ps1
scripts/e2e_plugin_runtime_smoke.ps1
```

重点断言：

- `auth_ref` 不进入 trace/audit/replay/governance 明文。
- provider governance 只暴露 `auth_ref_set` 这类布尔信号。
- ToolCard/PromptBundle 不暴露 connection base_url/auth_ref。
- AdapterOperation test/publish/invoke 全链路真实可用。

## 8. 分支拆分建议

### 分支 A：`refactor/tool-provider-service-connection`

范围：

- ToolProvider 模型收敛到 `service_connection_id`。
- 删除 Provider endpoint/auth/secret。
- Handler/OpenAPI/verify_contracts 对齐。
- Provider health/sync/invoke 通过 ServiceConnection。

不包含：

- Agent loader。
- Skill projection 展示合并。
- docker-compose 模型配置增强。

### 分支 B：`feat/adapter-operation-persistence`

范围：

- `tool_adapter_operations` store。
- HTTP API AdapterOperation。
- Database AdapterOperation。
- from-resource。
- publish/test/sync。

如果当前主线已实现，则此分支只做补测试和小修。

### 分支 C：`feat/service-connection-secret-rotation`

范围：

- `service_connection_secret_rotations`。
- auth pair validation。
- rotation history。
- trace/audit 泄露检查。

明确不做：

- ToolProvider.SecretRef。

### 分支 D：`feat/postgres-agent-definition-loader`

范围：

- Postgres package release loader。
- Chain loader 或等价 loader 组合。
- Runtime load 与 runnable check 的边界测试。

这是从 `ee7128e` 抽出的旁路能力，不和工具目录分支混合。

## 9. 验收标准

代码层：

- `ToolProvider` Go struct 没有 `Endpoint/AuthRef/SecretRef`。
- `ToolGroup` Go struct 没有 `ProviderID/ToolIDs/Metadata`。
- `ToolProvider` public payload 拒绝旧字段。
- `ServiceConnection` 是唯一认证引用事实源。
- `AdapterOperation` 是 HTTP/API 和 database 工具草稿事实源。
- `SyncProviderCatalog` 不覆盖远端 `group_id`。

数据层：

- migration checksum 稳定。
- 不修改已发布 migration。
- 不静默吞掉 schema mismatch 导致 restore 空数据。

契约层：

- `scripts/verify_contracts.ps1` 通过。
- OpenAPI 中 ToolProvider 不出现 `endpoint/auth_ref/secret_ref/token_ref`。
- ServiceConnection rotation request required `auth_ref/auth_type`。

测试层：

- `go test ./...` 通过。
- migration checksum 校验通过。
- CleanCore all interfaces e2e 通过。
- MMR service e2e 中 auth_ref 泄露检查通过。

## 10. 推荐落地顺序

1. 固定迁移 checksum 和换行策略。
2. 把旧 `8c30093` 的 `secret_ref` 线完全替换为 ServiceConnection rotation。
3. 把旧 `ee7128e` 的 group/provider persistence 改写到当前 schema。
4. 确认 provider sync 不再生成默认 group。
5. 补齐 Store restore 和 adapter operation persistence 测试。
6. 更新 OpenAPI 和 `verify_contracts`。
7. 再单独处理 Agent loader / skills projection 两个旁路改动。

最终原则：旧分支里的能力可以吸收，但字段模型不能倒退。CleanCore 当前主线应坚持 ServiceConnection 作为连接和认证事实源，ToolProvider 作为工具来源身份，AdapterOperation 作为托管工具化草稿，ToolManifest 作为 runtime 可调用事实。
