# CleanCore 服务连接与 Adapter 开发任务文档 v0.1

## 0. 目标

以 CleanCore Go 后端为事实源，干净补齐“服务连接 -> ToolProvider / Adapter Provider -> ToolManifest -> Agent 绑定”的能力链路。

本任务不先迁就当前产品 `docs/openapi.yaml` 里的页面契约，而是先让 Go 模型、存储、路由、运行时语义成立。Go 实现稳定后，再反向修正 `docs/openapi.clean-core.v1.json` 和产品 `docs/openapi.yaml`。

最终目标：

```text
ServiceConnection
  管 endpoint / auth_ref / network_scope / health / resources
        |
        | 被引用
        v
ToolProvider
  外部来源：static_tool_host / agent_plugin_service / mcp
  内置 Adapter：http_api_adapter / database_adapter
        |
        | sync / generate
        v
ToolManifest
  Agent 最终绑定 tool_id
```

## 1. 当前代码基线

已落地：

- `internal/tool/catalog/catalog.go` 已有 `ToolProvider`、`ToolManifest`、`ToolGroup`、`ExecutorSpec`。
- `ToolProvider` 当前支持 `static_tool_host`、`agent_plugin_service`、`mcp`、`http_api_adapter`、`database_adapter`。
- `ToolExecutorSpec` schema 可表达 `static_tool_host`、`agent_plugin_service`、`mcp`、`agent_tool`、`http_api_adapter`、`database_adapter`；两类 managed adapter executor 都由 AdapterOperation 内部发布生成，不允许用户手工 upsert 公开 manifest executor。
- `internal/serviceconnection` 已有 `ServiceConnection` domain/service/memory 主干，HTTP 类连接测试会真实探测 `/healthz` 或 base URL。
- REST 已有：
- `GET/POST /v1/service-connections`
- `GET/PUT/PATCH/DELETE /v1/service-connections/{connection_id}`
  - `PUT` 是全量替换；body 中如携带 `connection_id` 必须和 path 一致。
  - `PATCH` 是局部合并，未提交字段保持原值；如 patch 认证字段，`auth_type` 和 `auth_ref` 必须一起提交。
- `POST /v1/service-connections/{connection_id}/test`
- `POST /v1/service-connections/{connection_id}/enable`
- `POST /v1/service-connections/{connection_id}/disable`
  - `GET /v1/service-connections/{connection_id}/resources`
  - `GET /v1/service-connections/{connection_id}/health-events`
  - `GET /v1/service-connections/{connection_id}/impact`
  - `GET /v1/service-connections/{connection_id}/usage`
  - `GET /v1/service-connections/templates`
  - `GET/POST /v1/tool-providers`
  - `GET/PUT/PATCH/DELETE /v1/tool-providers/{provider_id}`
  - `POST /v1/tool-providers/{provider_id}/health`
  - `POST /v1/tool-providers/{provider_id}/sync`
  - `GET/POST /v1/tool-providers/{provider_id}/operations`
  - `POST /v1/tool-providers/{provider_id}/operations/from-resource`
  - `GET/PATCH /v1/tool-providers/{provider_id}/operations/{operation_id}`
  - `POST /v1/tool-providers/{provider_id}/operations/{operation_id}/publish`
  - `POST /v1/tool-providers/{provider_id}/operations/{operation_id}/test`
  - `GET/POST /v1/tool-groups`
  - `GET/POST /v1/tool-manifests`
  - `GET/PUT/PATCH/DELETE /v1/tool-manifests/{tool_id}`
- Postgres 已有 `service_connections`、`service_connection_resources`、`tool_providers`、`tool_adapter_operations`、`tool_groups`、`tool_manifests`、`tool_manifest_versions`、`tool_runtime_registry_cache`。

主要缺口：

- `database_adapter` 已有 Provider/AdapterOperation/Manifest 字段建模；Postgres 数据库服务连接已支持 test 和 table/view 资源发现；只读 SQL 执行器已通过 AdapterOperation publish/sync 暴露为可调用工具，并支持基于已发现 `resource_id` 的资源绑定、`redact_columns` 列存在性校验和基础结果列脱敏；`from-resource` 可从已发现 table/view 生成 Database AdapterOperation 草稿。
- HTTP API Adapter 已有 Provider、AdapterOperation store/handler、publish/test/execute 主链路；AdapterOperation 已按 provider_type 校验字段边界：HTTP API Adapter 只接受 HTTP request/mapping 字段，Database Adapter 只接受查询模板/资源/脱敏字段；HTTP API ServiceConnection 已支持通过 `metadata.openapi_path` 从 `base_url` 下的 OpenAPI JSON 发现 `http_operation` 资源；`from-resource` 接口已可从已发现 `http_operation` 生成 HTTP AdapterOperation 草稿；HTTP Adapter 轻量映射已覆盖 `path_params/query/headers/body` 请求映射和 `body_path/output` 响应映射，完整表达式 DSL/JSONPath 引擎后续再评估。
- 非 Postgres 数据库、缓存、队列、存储类 ServiceConnection 只建模和存储，连接测试/资源发现仍明确返回未实现；cache/queue/storage 的 test 覆盖要求返回 unknown + not implemented，不能假装 healthy。
- 产品 `docs/openapi.yaml` 已按当前 CleanCore 主干收敛服务连接、ToolProvider 和 AdapterOperation 关键接口；页面级聚合接口不再压进 CleanCore 主契约。

## 2. 设计原则

- **Go 优先**：先完成 Go domain、store、handler、runtime、test，再更新 OpenAPI。
- **不保留双入口**：当前决策是不保留“直接填写 URL”和“使用服务连接”两套公开路径。
- **ServiceConnection 不产工具**：服务连接只被 Provider 或 Adapter operation 引用，不直接生成 ToolManifest。
- **ToolHost/MCP 不放进服务连接类型**：ToolHost/MCP 是 ToolProvider；它们可以引用 HTTP API 服务连接，但本身不是服务连接。
- **Adapter 也是 Provider**：HTTP API、Database 这类平台托管工具化能力，用内置 ToolProvider 表达，不新增一条并列的“工具来源”体系。
- **Secret 不落库明文**：ServiceConnection 只保存 `auth_ref` 引用，不保存真实凭据，也不接收 `secret_ref/token_ref` 这类兼容别名；真实凭据解析后续接 secret provider。
- **健康字段只读**：ServiceConnection 的 `health_status/last_health_at/last_health_error` 和 ToolProvider 的 `health_status/last_health_check_at/last_health_error` 都是运行探测产生的观测事实，公开创建/更新 payload 不接受客户端提交；Go domain 普通 `Upsert` 也不保存调用方传入的健康观测字段，只能由连接 test / provider health 写入。
- **认证字段必须显式成对**：ServiceConnection 的认证状态由 `auth_type + auth_ref` 表达。无认证统一为 `auth_type=none` 且 `auth_ref` 为空；只要提交 `auth_ref` 就必须提交非 `none` 的 `auth_type`。Secret rotation 请求同样必须显式提交 `auth_type`，不从旧连接隐式推断。
- **开发期允许破坏式清理**：不为了兼容旧 `endpoint/auth_ref/http_direct.url` 牺牲模型。测试、OpenAPI、脚本和样例都改向新模型；旧形态直接删除或重写。核心身份字段如 `provider_type`、`connection_type`、AdapterOperation 的 `method/input_schema` 必须显式提交，不由后端猜默认。
- **旧字段只允许被拒绝**：如果旧字段必须短期留在代码搜索结果中，只能出现在历史说明或负向测试里。公开 handler、OpenAPI、e2e 正向路径和 seed/sample payload 不保留兼容解析。
- **不保留资源壳 payload**：公开写接口只接收 OpenAPI 定义的顶层字段，不兼容 `{"connection": {...}}`、`{"provider": {...}}`、`{"group": {...}}`、`{"manifest": {...}}`、`{"tool": {...}}`、`{"operation": {...}}` 这类资源壳结构。响应体仍可按资源名包裹，但请求体不做双形态解析。

## 3. 阶段拆分

### P0-1 新增 ServiceConnection 后端主干

新增包建议：

```text
internal/serviceconnection/
  service.go
  store.go
  memory.go
  health.go
  templates.go
```

核心模型：

```go
type ServiceConnection struct {
    TenantID          contracts.TenantID
    ConnectionID      string
    Name              string
    ConnectionType    string
    Environment       string
    Status            string
    Description       string
    BaseURL           string
    AuthType          string
    AuthRef           string
    NetworkScope      string
    TimeoutMS         int
    RetryMax          int
    HealthCheckEnabled bool
    Metadata          map[string]any
    LastHealthAt      *time.Time
    LastHealthError   string
    Version           string
}
```

首批枚举：

```text
connection_type:
  clean_core
  http_api
  database
  webhook
  oauth
  cache
  queue
  storage

status:
  draft
  enabled
  disabled
  unhealthy
  unknown
```

明确不放：

```text
toolhost
mcp
agent_plugin_service
http_direct
```

Store 接口：

```go
type Store interface {
    UpsertConnection(ctx context.Context, connection ServiceConnection) error
    GetConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, bool, error)
    ListConnections(ctx context.Context, tenantID contracts.TenantID, filter ListFilter) ([]ServiceConnection, error)
    DeleteConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) error
    UpsertResource(ctx context.Context, resource ServiceConnectionResource) error
    ReplaceResources(ctx context.Context, tenantID contracts.TenantID, connectionID string, resources []ServiceConnectionResource) error
    ListResources(ctx context.Context, tenantID contracts.TenantID, connectionID string) ([]ServiceConnectionResource, error)
}
```

新增 REST：

```text
GET    /v1/service-connections
POST   /v1/service-connections
GET    /v1/service-connections/{connection_id}
PATCH  /v1/service-connections/{connection_id}
DELETE /v1/service-connections/{connection_id}
POST   /v1/service-connections/{connection_id}/test
POST   /v1/service-connections/{connection_id}/enable
POST   /v1/service-connections/{connection_id}/disable
GET    /v1/service-connections/{connection_id}/resources
GET    /v1/service-connections/{connection_id}/health-events
GET    /v1/service-connections/{connection_id}/impact
GET    /v1/service-connections/{connection_id}/usage
GET    /v1/service-connections/templates
```

P0 里 `test` 先支持：

- `http_api/webhook/oauth/clean_core`：请求 `BaseURL` 或 `/healthz`。
- `database/cache/queue/storage`：先返回 `unsupported` 或 `not_implemented`，不要假装成功。

Postgres migration：

```sql
CREATE TABLE service_connections (
  tenant_id text NOT NULL,
  connection_id text NOT NULL,
  connection_type text NOT NULL,
  name text NOT NULL,
  environment text NOT NULL DEFAULT '',
  status text NOT NULL,
  description text,
  base_url text,
  auth_type text,
  auth_ref text,
  network_scope text,
  timeout_ms integer NOT NULL DEFAULT 0,
  retry_max integer NOT NULL DEFAULT 0,
  health_check_enabled boolean NOT NULL DEFAULT true,
  last_health_at timestamptz,
  last_health_error text,
  metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  version text NOT NULL DEFAULT 'v1',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, connection_id)
);

CREATE TABLE service_connection_resources (
  tenant_id text NOT NULL,
  connection_id text NOT NULL,
  resource_id text NOT NULL,
  resource_type text NOT NULL,
  name text NOT NULL,
  schema_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  discovered_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, connection_id, resource_id)
);
```

验收：

- server test 覆盖 ServiceConnection CRUD、enable/disable、test、resources。
- migration readiness 增加 `service_connections` 表检查。
- 返回体不泄露任何 secret 明文。

### P0-2 ToolProvider 引用 ServiceConnection

模型变更：

```go
type ToolProvider struct {
    ...
    ServiceConnectionID string `json:"service_connection_id"`
}
```

Store/schema 改造：

```sql
ALTER TABLE tool_providers
  ADD COLUMN service_connection_id text;

ALTER TABLE tool_providers
  DROP COLUMN endpoint,
  DROP COLUMN auth_ref;

CREATE INDEX idx_tool_providers_tenant_connection
  ON tool_providers (tenant_id, service_connection_id);
```

如果开发库可以重建，优先直接修改 `migrations/001_clean_core_base.sql` 中的 `tool_providers` 表结构，而不是写兼容式增量迁移。

解析规则：

```text
provider.service_connection_id
  -> ServiceConnection
  -> endpoint/auth/network/timeout/retry
```

开发期直接移除 `ToolProvider.endpoint` / `ToolProvider.auth_ref` 的权威语义。Provider 不再保存 endpoint 和认证引用，只保存 `service_connection_id`。

需要改的位置：

- `parseToolProviderPayload`
- `normalizeProvider`
- `validateProvider`
- `probeProviderHealth`
- `fetchCatalog`
- `ToolHostExecutor`
- `MCPExecutor`
- Postgres `UpsertProvider/ListProviders/scanToolProvider`
- OpenAPI clean-core ToolProvider schema

校验规则：

- `static_tool_host`、`agent_plugin_service`、`mcp` 必须有 `service_connection_id`。
- `http_api_adapter`、`database_adapter` 这类托管 Provider 必须没有 Provider 级 `service_connection_id`；连接引用只在 operation 上声明。
- Provider 引用的 ServiceConnection 必须存在、同租户、状态可用。
- `endpoint/auth_ref` 不再接受为 Provider 创建/更新字段。
- `health_status`、`last_health_check_at`、`last_health_error` 是健康检查产生的只读观测事实，不接受为 Provider 创建/更新字段；普通 `UpsertProvider` 会忽略调用方提交的健康观测，连接身份不变时保留既有观测，`service_connection_id/provider_type` 变化时重置为 `unknown`。

验收：

- ToolProvider 使用 `service_connection_id` 能 health、sync、invoke。
- 旧 endpoint/auth_ref Provider 测试删除或重写为 ServiceConnection 模型。
- 公开 ToolProvider 写接口拒绝客户端提交健康观测字段。
- ToolProvider domain 普通 upsert 不接受健康观测写入，健康状态只能由 `CheckProviderHealth` 写入。
- unhealthy ServiceConnection 应阻断依赖它的 Provider health/sync。

### P1-1 新增 HTTP API Adapter Provider

新增 Provider type：

```go
const ProviderTypeHTTPAPIAdapter = "http_api_adapter"
const ExecutorTypeHTTPAPIAdapter = "http_api_adapter"
const ManagedHTTPAPIAdapterID = "managed_http_api_adapter"
```

新增 operation 模型：

```go
type AdapterOperation struct {
    TenantID            contracts.TenantID
    ProviderID          string
    OperationID         string
    ToolID              string
    Name                string
    Description         string
    ServiceConnectionID string
    Method              string
    Path                string
    Headers             map[string]string
    InputSchema         map[string]any
    OutputSchema        map[string]any
    RequestMapping      map[string]any
    ResponseMapping     map[string]any
    RiskLevel           contracts.RiskLevel
    Visibility          contracts.ToolVisibility
    Status              string
    Version             string
}
```

新增表：

```sql
CREATE TABLE tool_adapter_operations (
  tenant_id text NOT NULL,
  provider_id text NOT NULL,
  operation_id text NOT NULL,
  tool_id text NOT NULL,
  service_connection_id text NOT NULL,
  name text NOT NULL,
  description text NOT NULL,
  method text NOT NULL,
  path text NOT NULL,
  headers_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  input_schema_json jsonb NOT NULL,
  output_schema_json jsonb,
  request_mapping_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  response_mapping_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  resource_id text,
  query_template text,
  parameter_schema_json jsonb,
  max_rows integer NOT NULL DEFAULT 0,
  read_only boolean NOT NULL DEFAULT false,
  risk_level text NOT NULL,
  visibility text NOT NULL,
  status text NOT NULL,
  version text NOT NULL DEFAULT 'v1',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, provider_id, operation_id)
);
```

新增 REST：

```text
GET  /v1/tool-providers/{provider_id}/operations
POST /v1/tool-providers/{provider_id}/operations
POST /v1/tool-providers/{provider_id}/operations/from-resource
GET  /v1/tool-providers/{provider_id}/operations/{operation_id}
PUT  /v1/tool-providers/{provider_id}/operations/{operation_id}
POST /v1/tool-providers/{provider_id}/operations/{operation_id}/publish
POST /v1/tool-providers/{provider_id}/operations/{operation_id}/test
```

HTTP API Adapter Provider 是 CleanCore 内置托管 Provider，不通过公开 `/v1/tool-providers` 创建。创建 HTTP API 工具时使用固定 `provider_id=managed_http_api_adapter` 创建 AdapterOperation；后端在首次 operation upsert 时自动确保该租户的内置 Provider 存在。公开 ToolProvider upsert 只用于 `static_tool_host`、`agent_plugin_service`、`mcp` 这类外部工具来源。`GET /v1/tool-providers` 默认也只返回外部工具来源；需要治理或排障时，显式传 `include_managed=true` 才返回 `managed_http_api_adapter`、`managed_database_adapter` 这类内置 Provider 事实。

`publish` 生成或更新：

```json
{
  "tool_id": "ticket.search",
  "executor": {
    "type": "http_api_adapter",
    "provider_id": "managed_http_api_adapter",
    "operation": "ticket.search"
  }
}
```

运行时：

- `buildExecutor` 支持 `ExecutorTypeHTTPAPIAdapter`。
- 执行时根据 `provider_id + operation` 找到 AdapterOperation。
- 根据 `service_connection_id` 解析 base_url/auth/network。
- 可通过 `from-resource` 从 ServiceConnectionResource 生成 AdapterOperation 草稿：`managed_http_api_adapter` 接受 `http_operation`，method/path/schema/request_mapping 由资源目录派生；`managed_database_adapter` 接受 table/view，生成只读查询草稿、默认 output_schema 和可编辑 query_template。
- 拼接 `method + path`。
- 当前基础映射：未配置 mapping 时，非 GET/DELETE 直接将 `ToolCall.Arguments` 作为 JSON body，GET/DELETE 转 query。
- 当前轻量 request mapping：`request_mapping.path_params` 用于替换 OpenAPI 风格 `{id}` 路径参数，`request_mapping.query` 用于 URL query，`request_mapping.headers` 用于动态请求头，`request_mapping.body` 用于 JSON body；值均来自 ToolCall arguments 的 dot-path，body/query 允许嵌套对象映射，path_params/headers 为目标名到 dot-path 的对象映射。
- 当前轻量 response mapping：`response_mapping.body_path/output` 已支持 dot-path 字符串或嵌套对象映射；暂不引入完整 DSL/JSONPath 引擎。
- 当前基础输出：对象响应直接作为工具输出，`{"output": {...}}` 自动拆出 `output`；配置 `response_mapping` 时可先用 `body_path` 选择响应片段，再用 `output` 映射为 ToolManifest 的输出对象。
- output 继续走 `ToolRuntime` 的 schema 校验。
- HTTP API Adapter operation 不接受 `resource_id/query_template/parameter_schema/max_rows/redact_columns/read_only` 等数据库字段，避免一条 operation 同时承担两种适配器语义。

验收：

- 能创建 HTTP API 服务连接。
- 能创建 HTTP API Adapter operation。
- publish 后生成 ToolManifest。
- Agent 绑定该 `tool_id` 后能调用。
- 不需要在 ToolManifest 里保存原始 URL。

### P1-2 删除 http_direct 公开能力

开发阶段不保留 `http_direct` 兼容分支。`http_direct` 当前暴露的是脏模型：URL、认证、网络边界散落在每个工具上。目标是直接从代码和契约中移除公开 `http_direct` 能力，用 `http_api_adapter` 取代。

改造要求：

1. 删除 `ExecutorTypeHTTPDirect` 的公开创建路径。
2. `parseToolExecutorSpec` 不再解析 `url/method/headers` 作为 manifest executor 字段。
3. `validateManifest` 不再接受 `executor.type=http_direct`。
4. `buildExecutor` 删除 `HTTPExecutor` 分支。
5. `HTTPExecutor` 类型删除，或只在新 `http_api_adapter` executor 内部以私有 helper 形式重写。
6. `ToolExecutorSpec` 只保留 Provider 路径需要的字段：`type/provider_id/operation`。
7. 所有 `http_direct` 正向运行测试、server 测试、e2e 脚本改成 HTTP API Adapter 路径；只保留拒绝旧 executor 的负向测试。
8. `disable_http_direct` 配置删除；不再需要开关控制已经不存在的能力。

验收：

- 新建 HTTP API 工具只走 ServiceConnection + AdapterOperation。
- 代码搜索 `http_direct` 只允许出现在历史说明文档和负向拒绝测试里，不应出现在 Go runtime、OpenAPI、e2e 脚本的可执行路径里。
- OpenAPI 不再暴露 `executor.url`。

### P1-3 ToolProvider 页面聚合接口不进入 CleanCore 主契约

早期产品 OpenAPI 里曾出现几个页面聚合接口：

```text
GET  /v1/tool-providers/{provider_id}/tools
GET  /v1/tool-providers/{provider_id}/details
POST /v1/tool-providers/{provider_id}/repair
```

当前目标态不再把这些页面级聚合接口压进 CleanCore 主契约。CleanCore 提供可组合的事实接口：

- `GET /v1/tool-providers/{provider_id}` 返回 Provider。
- `GET /v1/tool-manifests?provider_id=...` 返回该 Provider 产生的工具。
- `POST /v1/tool-providers/{provider_id}/health` 执行健康检查。
- `POST /v1/tool-providers/{provider_id}/sync` 同步/发布工具目录。
- `GET /v1/tool-provider-governance` 和 `GET /v1/service-connections/{connection_id}/impact` 提供治理矩阵和影响范围。

前端需要的详情页、工具页、修复建议页由 BFF/前端聚合这些基础接口生成；CleanCore 不提供 `details/tools/repair` 这类页面专用接口，也不保留 alias。

同时补 list filter：

```text
GET /v1/tool-providers?q=&provider_type=&status=&health_status=&include_managed=&page_size=&cursor=
GET /v1/tool-manifests?q=&provider_id=&executor_type=&status=&risk_level=&visibility=&page_size=&cursor=
GET /v1/service-connections?q=&connection_type=&status=&health_status=&environment=&page_size=&cursor=
```

## 4. P2 后续能力

### P2-1 Database Adapter Provider

新增：

```text
ProviderTypeDatabaseAdapter = "database_adapter"
ExecutorTypeDatabaseAdapter = "database_adapter" // AdapterOperation 内部发布使用；公开 manifest upsert 不接受手工创建
```

能力：

- 数据库服务连接 test：Postgres 已支持；其他 driver 明确拒绝。
- 资源发现：Postgres schema/table/view 已支持，资源刷新采用 replace 语义；`database_adapter` 启用、test、publish 和运行时安装都会校验 `resource_id` 必须命中已发现 table/view。
- 只读查询模板。
- 参数化查询。
- 行数限制、超时、基础结果列脱敏；`redact_columns` 必须命中已发现列；AdapterOperation upsert/test/publish 生命周期审计已进入 audit，更复杂的策略脱敏和运行审计增强后续补。
- publish 查询模板为 ToolManifest；公开 manifest upsert 仍拒绝手工 `executor.type=database_adapter`，只允许 AdapterOperation 内部发布。HTTP API Adapter 同样只允许通过 AdapterOperation publish/sync 生成 `executor.type=http_api_adapter`。
- Database Adapter operation 不接受 `method/path/headers/request_mapping/response_mapping` 等 HTTP 字段，避免数据库查询工具混入 HTTP 请求语义。

初版不做：

- LLM 任意 SQL。
- 写操作。
- 跨库 join。

### P2-2 更完整的连接治理

后续补：

- health history：已进入 `service_connection_health_events` 和 `/health-events`。
- trace-backed usage aggregation：已进入 `/usage`，支持 `trace_id` 精确聚合、无 `trace_id` 的最近 trace limit 聚合，以及 `from/to` RFC3339Nano 时间窗口；`limit` 只在未指定 `trace_id` 时用于最近事件聚合。
- dependency impact view：已进入 `/impact`。
- secret rotation workflow：已进入 `/secret-rotations`，接收显式 `auth_type + auth_ref`，只保存 auth_ref 指纹和 auth_type，不保存旧/新 ref 明文。
- auth contract：`auth_type` 已收敛为 `none/api_key/bearer/basic/oauth2/signed_request/mtls`。创建/更新连接和 secret rotation 均校验 `auth_type/auth_ref` 成对关系，`none` 不允许携带 `auth_ref`。
- network allowlist enforcement：`network_scope` 已作为连接级 host allowlist，下发到 ToolHost/MCP/HTTP API Adapter 生成的 HTTP execution profile；未配置时默认限制为 `base_url` host；HTTP 类连接显式配置 `network_scope` 时必须包含 `base_url` host。
- mTLS / signed request。

## 5. OpenAPI 更新顺序

### 5.1 先更新 clean-core OpenAPI

Go 完成并测试通过后，更新 `docs/openapi.clean-core.v1.json`：

- 增加 `/v1/service-connections` 路径。
- 增加 ServiceConnection schemas。
- ToolProvider 增加 `service_connection_id`。
- ToolProvider provider_type 增加 `http_api_adapter`、`database_adapter`。
- ToolExecutorSpec 增加 `http_api_adapter`、`database_adapter`；两类 managed adapter executor 只由 AdapterOperation 发布生成，公开 manifest upsert 不接受手工创建。
- Adapter operation schemas 和 endpoints。
- ServiceConnection 增加 `/secret-rotations`，轮转请求接收显式 `auth_type + auth_ref`，响应历史只暴露 ref hash 和 auth_type。
- 移除 `http_direct`、`executor.url`、`executor.method`、`executor.headers`。

### 5.2 再修产品 OpenAPI

更新 `docs/openapi.yaml`：

- `ServiceConnection.connection_type` 移除 `toolhost`。
- `ToolProvider.provider_type` 移除 `http`、`http-direct`。
- `ToolProvider.provider_type` 只接受规范枚举，不在后端做产品文案别名：
  - `static_tool_host`
  - `agent_plugin_service`
  - `mcp`
  - `http_api_adapter`
  - `database_adapter`
- 产品页面可显示“ToolHost / AgentPlugin Service”，但提交到 CleanCore 时必须使用规范 provider_type；后端接收 `static_tool_host`、`agent_plugin_service`、`mcp`、`http_api_adapter`、`database_adapter`，不接收 `toolhost`、`agent-plugin-service`、`http`、`http-direct` 这类文案别名。
- 产品 `/v1/tool-providers/{id}/health-check` 统一改为 CleanCore 的 `/health`，不补 alias。
- 产品工具目录契约直接使用 `/v1/tool-manifests`，不要再引入 `/v1/tools` 作为目录/管理面；产品 OpenAPI 中旧 `/v1/tools` 管理路径和旧 `Tool*` schema 已移除。
- HTTP Direct 从公开创建入口移除。

## 6. 测试计划

单元测试：

- `serviceconnection.Service` normalize/validate。
- ServiceConnection memory store。
- HTTP health test。
- ToolProvider service_connection_id resolution。
- HTTP API Adapter operation publish。
- HTTP API Adapter executor 基础 request/response mapping，覆盖 path_params/query/headers/body 和 body_path/output；完整表达式 DSL/JSONPath 引擎后续再评估。
- AdapterOperation from-resource：HTTP API 可从 `http_operation` 资源生成草稿并自动带出 path/query/header/body 映射；Database 可从 table/view 资源生成只读查询草稿、默认 output_schema 和可编辑 query_template。
- HTTP API ServiceConnection test 可通过 `metadata.openapi_path=/openapi.json` 发现接口资源，且拒绝绝对 URL 形式的 `openapi_path`。

Server tests：

- ServiceConnection CRUD。
- ServiceConnection PATCH 保留未提交字段，并拒绝 path/body `connection_id` 不一致或只改半组认证字段。
- ServiceConnection test/resources。
- ToolProvider 创建时引用 service_connection_id。
- ToolProvider PATCH 保留未提交字段，并拒绝 path/body `provider_id` 不一致。
- ToolProvider health/sync 使用连接解析出的 endpoint/auth。
- ToolGroup PATCH 保留未提交字段，并拒绝 path/body `group_id` 不一致。
- Adapter operation publish 生成 ToolManifest。
- AdapterOperation PATCH 保留未提交字段，并拒绝 path/body `provider_id` 或 `operation_id` 不一致。
- ToolManifest PATCH 保留未提交字段，并拒绝 path/body `tool_id` 不一致。
- ToolManifest 调用走 Adapter executor。
- ServiceConnection、ToolProvider、ToolGroup、ToolManifest、AdapterOperation 的 REST/command 写入口拒绝资源壳 payload，只接受顶层字段。

Postgres/migration tests：

- `service_connections`、`service_connection_resources` readiness。
- `tool_providers.service_connection_id` migration。
- `tool_adapter_operations` migration。
- restore 后 provider/manifest/operation 可用。

E2E：

```text
创建 HTTP API 服务连接
  -> 在固定 provider_id=managed_http_api_adapter 下创建 operation
  -> publish ToolManifest
  -> Agent 绑定 tool_id
  -> 运行调用
  -> trace/audit 可见 provider_id、operation、connection_id
```

## 7. 破坏式清理策略

开发阶段不做旧模型兼容。为了模型干净，以下内容直接删除或重写：

- `ToolProvider.endpoint/auth_ref` 作为公开 Provider 字段。
- `ExecutorTypeHTTPDirect`。
- `HTTPExecutor`。
- `executor.url/method/headers`。
- `disable_http_direct` 配置。
- 使用 `http_direct` 的测试、脚本、OpenAPI schema。

替代模型：

- 外部 ToolHost/MCP Provider 必须引用 ServiceConnection。
- HTTP API 工具通过 `http_api_adapter` 管。
- 数据库查询工具通过 `database_adapter` 或外部 ToolHost/MCP 管。
- Agent 永远只绑定 ToolManifest。

## 8. 任务清单

P0：

- [x] 新增 `internal/serviceconnection` domain/service/store/memory。
- [x] 新增 Postgres 表和 store。
- [x] 新增 `/v1/service-connections` handlers/routes。
- [x] Core 注入 `ServiceConnections`。
- [x] ServiceConnection templates。
- [x] ToolProvider 增加 `service_connection_id`。
- [x] ToolProvider health/sync/invoke 支持 connection resolution。
- [x] ToolProvider REST PATCH 改为局部合并，避免清空 `service_connection_id/provider_type`。
- [x] 测试覆盖 P0 主链路。

P1：

- [x] 新增 `http_api_adapter` provider/executor。
- [x] `http_api_adapter` / `database_adapter` 作为固定内置 managed provider，不通过公开 ToolProvider 创建页创建；HTTP/DB 工具只创建 AdapterOperation。
- [x] 新增 adapter operation store/table。
- [x] 新增 provider operations REST。
- [x] operation publish 生成 ToolManifest。
- [x] Adapter executor 支持 HTTP 调用和基础 request/response mapping。
- [x] HTTP API Adapter 支持轻量 dot-path request/response mapping。
- [x] HTTP API Adapter 支持 OpenAPI 风格 path_params 和动态 header 映射，并要求参数化 path 显式声明 path_params。
- [x] AdapterOperation REST PATCH 改为局部合并，避免清空 `method/path/schema/service_connection_id`。
- [x] 删除 `http_direct` executor 和 `HTTPExecutor` 公开运行路径。
- [x] 删除 `executor.url/method/headers` 公开 schema。
- [x] 重写现有 `http_direct` 运行测试为 ServiceConnection + Provider 路径；保留拒绝旧 executor 的负向测试。
- [x] 更新 `docs/openapi.clean-core.v1.json`。
- [x] 更新产品 `docs/openapi.yaml`。
- [x] ToolGroup/ToolManifest REST PATCH 改为局部合并，避免局部编辑清空治理字段、schema 或 executor。
- [x] ServiceConnection、ToolProvider、ToolGroup、ToolManifest、AdapterOperation 写入口拒绝资源壳 payload，不保留 wrapper 兼容解析。
- [x] ServiceConnection/ToolProvider 普通 domain upsert 不保存调用方提交的健康观测字段；连接/provider 健康范围变化时重置观测。

P2：

- [x] 新增 `database_adapter` Provider/Operation/Manifest 字段建模。
- [x] 查询模板工具化的定义、存储和治理主链路；通过 AdapterOperation publish/sync 发布。
- [x] `database_adapter` 真实只读 SQL 执行器。
- [x] Database Adapter 支持已发现 `resource_id` 绑定、`redact_columns` 列校验和基础结果列脱敏。
- [x] AdapterOperation upsert/test/publish 写入结构化 audit，resource_type 区分为 `tool_adapter_operation`。
- [x] Postgres 数据库资源发现。
- [x] ToolManifest、AdapterOperation、database parameter_schema 的 JSON Schema 基础合法性校验。
- [x] MCP executor 输出归一化：`structuredContent` 优先作为业务对象，缺失时保留 `{content: ...}` 包装。
- [x] 连接健康历史和使用统计：新增 `service_connection_health_events`、`GET /v1/service-connections/{connection_id}/health-events`、`GET /v1/service-connections/{connection_id}/usage`。
- [x] Provider/Connection impact view。
- [x] Secret rotation workflow：新增 `GET/POST /v1/service-connections/{connection_id}/secret-rotations`，轮转请求接收显式 `auth_type + auth_ref`，轮转后连接健康状态回到 `unknown`，历史只保存 ref hash 和 auth_type。
- [x] ServiceConnection `auth_type` 枚举化，并强制 `auth_type/auth_ref` 成对；无认证统一为 `auth_type=none`。
- [x] ServiceConnection `network_scope` 下发到 HTTP execution profile 并由 runtime 网络 allowlist 强制执行。
- [x] 数据库 ServiceConnection 的连接身份变化时清空已发现资源目录，Postgres store 使用连接 upsert + resources replace 同事务语义，避免旧资源 schema 被新连接继续复用。
- [x] HTTP API ServiceConnection 支持 `metadata.openapi_path` OpenAPI JSON 资源发现，写入 `http_operation` 资源目录；连接身份或 metadata 变化时清空旧目录，test 后 replace 刷新。
- [x] HTTP API / Database Adapter 支持从已发现资源生成 AdapterOperation 草稿，服务连接资源目录和工具目录之间有明确转换接口。
- [x] Database Adapter 的 `query_template` 校验 `FROM/JOIN` 关系必须命中绑定 `resource_id`，跨资源查询会被拒绝。
- [x] cache/queue/storage 连接类型只建模不假健康，连接 test 明确返回 unknown + not implemented 并写健康事件。

## 9. 需要确认但不阻塞 P0 的问题

1. Secret provider 最终由 Host 提供还是 CleanCore 内置轻量实现？
2. MCP 远程地址是否也强制用 ServiceConnection？本任务建议强制。
3. `docs/openapi.clean-core.v1.json` 是否手写维护，还是后续引入生成流程？

## 10. 当前验证记录

截至当前实现，P0/P1/P2 主链路已通过以下验证：

- `go test ./... -count=1`
- `.\scripts\verify_contracts.ps1`
- PowerShell 解析检查：`scripts\verify_contracts.ps1`、`scripts\e2e_clean_core_all_interfaces.ps1`
- `docs/openapi.clean-core.v1.json` JSON 解析和 BOM 检查
- `git diff --check`

这些验证覆盖 Go domain/store/handler/runtime、OpenAPI 契约、脚本语法和格式问题。未纳入当前完成口径的是文档中明确标记的后续增强：更复杂策略脱敏、运行审计细化、完整 JSONPath/DSL、mTLS/signed request 真实签名执行，以及 OpenAPI 自动生成流程。

## 11. 推荐落地顺序

```text
1. ServiceConnection Go 主干
2. ToolProvider.service_connection_id
3. HTTP API Adapter Provider
4. 删除 http_direct 公开能力
5. clean-core OpenAPI
6. 产品 openapi.yaml
7. 前端页面联调
8. Database Adapter 和治理增强
```

这条顺序能避免再出现“产品页面先长出来，但 Go 语义没定”的问题。
