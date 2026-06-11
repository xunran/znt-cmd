# CleanCore 动态工具注册与外部工具接入改造设计 v0.1

## 0. 当前实现状态

截至本轮整改，动态工具 MVP 已落地到 command/API 层和运行时门禁：

```text
ToolCatalogService
ToolProvider / ToolGroup / ToolManifest
tool.provider.upsert / tool.provider.sync
tool.group.upsert / tool.group.list
tool.manifest.upsert / tool.manifest.list
agent.package.tool_binding.update
Postgres: tool_providers / tool_groups / tool_manifests / tool_manifest_versions
ToolRuntime availability check
Registry.CardsForTenant 动态读取当前 runtime registry
```

当前实现采用 command envelope 暴露能力；REST 风格 `/v1/tool-providers`、`/v1/tool-manifests`、`/v1/agents/{id}/tool-bindings` 可以作为后续网关封装，不要求在 MVP 内单独落地。

## 1. 背景

CleanCore 的工具体系已经具备基础运行时能力：

- `registry.Tool = ToolDefinition + Executor + WhenToUse`
- `ToolRuntime.Invoke()` 负责 schema 校验、ToolPolicy、approval、execution domain、trace、result 保存
- Agent 通过 `tool_bindings` 和候选检索拿到 `ToolCard`

早期工具注册主要发生在服务启动时的 Go 代码装配中，例如 `RegisterBuiltinsWithArtifacts()`、`origin.agent.delegate`、`originext.Register()`。这对内核工具足够，但对业务开发者不够友好：

- 新增工具必须改 Go 代码并重启服务
- 动态外部工具没有标准 manifest、provider catalog 和 execution adapter
- 工具注册、Agent 绑定、模型可见、运行时可执行之间的生命周期没有产品化
- 没有 API 支持在线注册、禁用、更新、注入工具

本设计不考虑兼容旧数据。当前处于开发阶段，可以重构数据结构和 API。

## 2. 目标

本次改造目标：

1. 统一工具定义模型：所有工具都通过 `ToolManifest` 表达。
2. 支持内部 Go 工具：继续允许内核通过 Go 代码注册 executor。
3. 支持动态外部工具：开发者通过 API 注册工具定义，CleanCore 通过 provider adapter 调用外部实现。
4. 支持运行中注入工具：服务启动后可通过 API 增加、更新、禁用工具，不必重启。
5. 明确生命周期：工具注册不是每次任务注册，而是系统级或租户级能力注册；任务运行时只做候选检索和快照。
6. 复用现有 ToolRuntime：schema、policy、approval、trace、result 保存不绕过。

非目标：

- 不支持动态上传任意 Go 代码执行。
- 不允许模型直接访问外部工具 endpoint。
- 不把 MCP、worker、managed runtime 一次性全部做完；先打通 `static_tool_host` 和 `http_direct` 两种外部 provider。

## 3. 核心结论

工具生命周期应分成三层：

```text
工具定义生命周期:
ToolManifest 创建/更新/启用/禁用

运行时注册生命周期:
服务启动加载 manifests，或 API 热注册 manifests 到 RuntimeRegistry

任务调用生命周期:
每个 run/step 检索候选工具，模型输出 tool_call，ToolRuntime 执行
```

不要“每运行一次任务注册一次工具”。  
任务运行时只应该读取已注册工具，生成候选集和快照。

可以支持“运行后增加新工具”，但这是 **工具目录的热注入**：

```text
POST /v1/tool-manifests
  -> validate manifest
  -> persist manifest
  -> build executor adapter
  -> register into runtime registry
  -> refresh candidate tool cards
```

本项目运行起来后，外部开发者的理想接入方式应该是：

```text
开发者自己的服务实现业务逻辑
  +
开发者调用 CleanCore API 注册/更新 ToolManifest
  +
CleanCore Agent 无需改代码、无需重启
```

动态工具的实现不进入 CleanCore 进程。CleanCore 只保存工具声明、治理工具调用、通过 provider adapter 调用外部服务、记录结果。

HTTP 不是外部工具体系本身，只是一种传输方式。核心抽象应稳定在：

```text
ToolManifest
ToolCard
ToolCall
ToolResult
ToolRuntime governance
```

外部工具实现可以替换为：

```text
static_tool_host
http_direct
worker
managed
grpc
queue
mcp
```

生产推荐优先使用独立部署的 `static_tool_host`，即一个工具宿主服务提供 catalog、invoke、health 等标准接口。`http_direct` 适合开发期、低风险或少量轻量工具。

当前代码已引入 `ToolGroup` 和 group allow/deny，候选检索可以按工具组过滤并展开为 ToolCard。MVP 暂不要求模型显式先选择工具组；“先判断工具组、再展开工具”的逻辑先放在服务端候选检索和权限过滤里，减少 prompt 噪音和误调用。

## 4. 新架构

### 4.1 组件

```text
Developer / Admin
      |
      v
Tool / Provider Registry API
      |
      v
ToolCatalogService
      |
      +--> ToolCatalog Store
      |       stores ToolManifest / ToolGroup / version / status
      |
      +--> ToolProvider Store
      |       stores ExternalToolProvider / endpoint_ref / auth_ref / health
      |
      +--> ExecutorFactory
      |       builds go / static_tool_host / http_direct / worker / managed adapter
      |
      +--> RuntimeRegistry
      |       executable cache keyed by tenant scope + tool_id + version
      |
      +--> Audit / Trace
              records register / update / enable / disable / install result
```

运行时调用链：

```text
Agent run
  -> CandidateProvider reads ToolCatalog + RuntimeRegistry cards
  -> PromptBundle injects ToolCards
  -> Model returns tool_call
  -> Decision validator checks candidate set
  -> ToolRuntime.Invoke()
       -> ToolAvailabilityChecker.Check(tenant_id, tool_id, snapshot_version)
       -> registry.Get(tenant_id, tool_id, snapshot_version)
       -> input schema validation
       -> ToolPolicy
       -> approval check
       -> execution domain / executor
       -> output schema validation
       -> trace + result
```

关键不变量：

1. `ToolCatalogService` 是工具写入的唯一入口。API、启动恢复、内部 seed 注册都应通过它进入 catalog 和 runtime registry。
2. `ToolCatalog Store` 是工具定义、状态、版本、租户 scope 的 source of truth。
3. `RuntimeRegistry` 只是可执行缓存，不能成为唯一状态源。
4. `CandidateProvider` 每次候选召回必须读取 catalog/registry 的当前 enabled 状态，或使用带失效通知的缓存；不能再使用启动时 `tools.Cards()` 快照。
5. `ToolRuntime` 执行前必须再次检查 tool/group/status/scope。候选检索只负责“模型是否可见”，不能替代执行门禁。

### 4.2 新增模块建议

```text
internal/tool/catalog
  manifest.go
  provider.go
  store.go
  memory.go
  postgres.go

internal/tool/register
  service.go
  validator.go
  installer.go

internal/tool/executor
  http.go
  toolhost.go
  go.go
  factory.go

internal/execution/domain
  http.go

internal/tool/discovery
  provider.go
```

也可以沿用现有 `internal/discovery/tool`，但建议把“静态候选卡”升级为读取 runtime registry。

### 4.3 ToolCatalogService 职责

`ToolCatalogService` 负责把“开发者提交的工具声明”变成“运行时可见且可执行的工具”。

```go
type ToolCatalogService interface {
    UpsertManifest(ctx context.Context, req UpsertToolManifestRequest) (ToolManifest, error)
    UpsertProvider(ctx context.Context, req UpsertToolProviderRequest) (ExternalToolProvider, error)
    SyncProviderCatalog(ctx context.Context, tenantID contracts.TenantID, providerID string) ([]ToolManifest, error)
    EnableTool(ctx context.Context, tenantID contracts.TenantID, toolID string) error
    DisableTool(ctx context.Context, tenantID contracts.TenantID, toolID string, reason string) error
    UpsertGroup(ctx context.Context, req UpsertToolGroupRequest) (ToolGroup, error)
    DisableGroup(ctx context.Context, tenantID contracts.TenantID, groupID string, reason string) error
    InstallEnabledTools(ctx context.Context, tenantID contracts.TenantID) error
}
```

`UpsertManifest` 的顺序必须固定：

```text
1. validate manifest schema / scope / reserved prefix / executor/provider spec
2. validate group exists and is enabled if group_id is provided
3. validate provider exists and is enabled if provider_id is provided
4. persist manifest and version history in one DB transaction
5. if status=enabled, build executable adapter
6. upsert executable tool into RuntimeRegistry
7. invalidate CandidateProvider cache, if cache exists
8. write audit/trace event
```

如果 adapter 构建或 runtime 安装失败，manifest 不能进入 `enabled` 状态。可以保存为 `draft` 或 `disabled`，并返回安装失败原因。这样 API 不会出现“注册成功但运行不可调用”的假成功。

### 4.4 目标开发者接入模式

外部开发者不应该接触 CleanCore 内部 Go registry，也不应该为了加工具修改运行中的 agent 框架。

推荐开发者接入流程：

```text
1. 开发者独立部署工具实现服务
2. 开发者调用 CleanCore Provider API 注册 ExternalToolProvider
3. CleanCore 从 provider catalog 同步 ToolManifest，或开发者手工注册 ToolManifest
4. 开发者调用 Agent Tool Binding API 把工具授权给某个 Agent 或工具组
5. CleanCore 后续任务运行时自动检索、注入、调用该工具
```

生产推荐开发者维护一个静态工具宿主：

```text
https://developer-tool-host.example.com
  GET  /tools/catalog
  POST /tools/invoke
  GET  /health
```

CleanCore 只持有：

```text
tool_id
description
input_schema
output_schema
when_to_use
risk_level
visibility
http endpoint
auth_ref
tool_group_id
provider_id
```

这样动态工具升级时，开发者可以：

- 只改自己的工具服务实现，不动 CleanCore。
- 如果参数 schema 或说明变化，再调用 CleanCore API 更新 manifest。
- 如果只是内部逻辑变化，可以不更新 manifest。
- 如果 provider transport 从 `http_direct` 切换到 `static_tool_host`、worker 或 managed，只要 ToolManifest / ToolCall / ToolResult 契约不变，Agent 和 ToolRuntime 主流程不变。

## 5. ToolManifest 设计

所有工具，无论内部 Go 工具还是外部 provider 工具，都统一声明成 manifest。

```go
type ToolManifest struct {
    ToolID      string `json:"tool_id"`
    Name        string `json:"name"`
    Description string `json:"description"`

    WhenToUse []string `json:"when_to_use,omitempty"`
    GroupID   string   `json:"group_id,omitempty"`
    ProviderID string  `json:"provider_id,omitempty"`
    Tags      []string `json:"tags,omitempty"`

    InputSchema  map[string]any `json:"input_schema"`
    OutputSchema map[string]any `json:"output_schema,omitempty"`

    RiskLevel  contracts.RiskLevel      `json:"risk_level"`
    Visibility contracts.ToolVisibility `json:"visibility"`

    Executor         ToolExecutorSpec `json:"executor"`
    ExecutionProfile map[string]any     `json:"execution_profile,omitempty"`

    Scope   ToolScope  `json:"scope"`
    Status  ToolStatus `json:"status"`
    Version string     `json:"version"`
}
```

### 5.1 Executor Spec

```go
type ToolExecutorSpec struct {
    Type string `json:"type"` // go, static_tool_host, http_direct, worker, managed

    GoRef string `json:"go_ref,omitempty"`

    ProviderID string `json:"provider_id,omitempty"`
    Operation  string `json:"operation,omitempty"`

    HTTPDirect *HTTPDirectExecutorSpec `json:"http_direct,omitempty"`

    WorkerRef      string `json:"worker_ref,omitempty"`
    ManagedRuntime string `json:"managed_runtime,omitempty"`

    TimeoutMS int `json:"timeout_ms,omitempty"`
}

type HTTPDirectExecutorSpec struct {
    URL       string            `json:"url"`
    Method    string            `json:"method"` // POST by default
    Headers   map[string]string `json:"headers,omitempty"`
    AuthRef   string            `json:"auth_ref,omitempty"`
    TimeoutMS int               `json:"timeout_ms,omitempty"`
}
```

`executor.type=static_tool_host` 表示工具由已注册的 `ExternalToolProvider` 执行，manifest 中只保存 `provider_id` 和 `operation`。这是生产推荐路径。

`executor.type=http_direct` 表示该工具直接配置一个 HTTP endpoint。它适合开发期、内网轻量工具或 PoC，不推荐作为大量生产工具的默认接入方式。

无论是 `static_tool_host` 还是 `http_direct`，都不应伪装成当前的 `local` execution domain。现有 local domain 不允许网络和 credential scope，因此外部工具必须满足以下二选一：

1. 新增对应 execution domain，例如 `tool_host` / `http`，由 domain 负责网络访问、超时、host allowlist、credential 注入、审计。
2. 或者把外部工具作为 `managed/worker` adapter 执行，由对应 execution domain 负责远程访问。

推荐开发阶段先做方案 1：

```json
{
  "executor": {
    "type": "http_direct",
    "http_direct": {
      "url": "https://crm.example.com/tools/customer.lookup",
      "method": "POST",
      "auth_ref": "tenant:crm-api-key",
      "timeout_ms": 5000
    }
  },
  "execution_profile": {
    "domain_id": "http",
    "network_policy": {
      "allow_network": true,
      "allowed_hosts": ["crm.example.com"]
    },
    "credential_scope": {
      "allowed_credential_refs": ["tenant:crm-api-key"]
    }
  }
}
```

`ToolManifest.executor` 描述“怎么调用工具实现”，`execution_profile` 描述“运行治理边界”。两者都要进入 manifest，但执行时以 `execution_profile.domain_id` 选择 domain，以 `executor` 构建具体 adapter。

### 5.2 Scope

```go
type ToolScope struct {
    TenantID contracts.TenantID `json:"tenant_id,omitempty"`
    Global   bool               `json:"global,omitempty"`
}
```

开发阶段可以先做：

- `global=true`：全局工具
- `tenant_id=xxx`：租户工具

候选检索时：租户工具 + 全局工具。

### 5.3 Status

```go
type ToolStatus string

const (
    ToolStatusDraft    ToolStatus = "draft"
    ToolStatusEnabled  ToolStatus = "enabled"
    ToolStatusDisabled ToolStatus = "disabled"
)
```

只有 `enabled` 工具会进入 runtime registry 和候选检索。

### 5.4 ToolGroup 设计

为了支持“先判断工具组，再从组中加载工具”的两层架构，新增 `ToolGroup`。

```go
type ToolGroup struct {
    GroupID     string              `json:"group_id"`
    TenantID    contracts.TenantID  `json:"tenant_id,omitempty"`
    Name        string              `json:"name"`
    Description string              `json:"description"`
    WhenToUse   []string            `json:"when_to_use,omitempty"`
    Tags        []string            `json:"tags,omitempty"`
    RiskLevel   contracts.RiskLevel `json:"risk_level"`
    Status      ToolStatus          `json:"status"`
    Version     string              `json:"version"`
}
```

示例：

```json
{
  "group_id": "crm",
  "name": "CRM 工具组",
  "description": "查询和更新客户、商机、联系人等 CRM 数据。",
  "when_to_use": ["客户资料", "商机查询", "联系人", "CRM"],
  "tags": ["crm", "customer", "sales"],
  "risk_level": "medium",
  "status": "enabled",
  "version": "v1"
}
```

工具绑定到组：

```json
{
  "tool_id": "crm.customer.lookup",
  "group_id": "crm"
}
```

两层召回流程：

```text
Step 1: Group retrieval
  输入 objective/context
  返回匹配的 ToolGroupCard 列表

Step 2: Tool retrieval inside selected groups
  只在命中的 group_id 中检索 ToolCard
  再叠加 Agent tool_bindings、Policy、visibility、risk 过滤

Step 3: Prompt injection
  PromptBundle 注入 retrieved tool groups 和 retrieved tool cards
```

开发阶段建议先做同步两层召回：一次 step 内先选组再选工具，最终仍只让模型看到少量 ToolCard。这样不用改 decision schema。

## 6. 内部 Go 工具

内部工具仍然由 Go 实现 executor，但也走 manifest 注册。

推荐形式：

```go
type GoToolProvider interface {
    Manifest() catalog.ToolManifest
    Executor() registry.Executor
}
```

注册例子：

```go
type EchoTool struct{}

func (EchoTool) Manifest() catalog.ToolManifest {
    return catalog.ToolManifest{
        ToolID:      "echo",
        Name:        "echo",
        Description: "Returns input arguments.",
        InputSchema: map[string]any{"type": "object"},
        OutputSchema: map[string]any{"type": "object"},
        RiskLevel:  contracts.RiskLow,
        Visibility: contracts.ToolExposed,
        Version:    "v1",
        Status:     catalog.ToolStatusEnabled,
        Executor: catalog.ToolExecutorSpec{
            Type:  "go",
            GoRef: "builtin.echo",
        },
        WhenToUse: []string{"smoke tests", "debugging"},
    }
}

func (EchoTool) Executor() registry.Executor {
    return registry.EchoExecutor{}
}
```

内部 Go 工具注册流程：

```go
toolRegistration.RegisterGoTool(ctx, EchoTool{})
```

`RegisterGoTool` 做三件事：

1. 校验 manifest。
2. 保存到 catalog。
3. 安装到 runtime registry。

内部工具也应该有 manifest，这样外部查看、prompt preview、tool card、policy 都统一。

## 7. 外部工具 Provider

外部工具 Provider 是 CleanCore 连接独立工具实现的稳定抽象。HTTP direct 只是 provider 的一种传输方式，不是核心模型。

推荐 provider 类型：

```text
static_tool_host
  独立部署的工具宿主服务，提供 catalog / invoke / health 标准接口。
  生产推荐。

http_direct
  每个工具直接配置 URL。
  适合开发期、低风险、少量轻量工具。

worker
  通过 worker queue / job runner 执行。
  适合长任务、异步、高可靠场景。

managed
  由 CleanCore 管理或受控运行时执行。
  适合需要强隔离和平台统一运维的工具。
```

### 7.1 ExternalToolProvider

```go
type ExternalToolProvider struct {
    ProviderID  string             `json:"provider_id"`
    TenantID    contracts.TenantID `json:"tenant_id,omitempty"`
    Type        string             `json:"type"` // static_tool_host, http_direct, worker, managed
    Name        string             `json:"name"`
    Description string             `json:"description,omitempty"`

    Endpoint ToolProviderEndpoint `json:"endpoint,omitempty"`
    AuthRef  string               `json:"auth_ref,omitempty"`

    AllowedToolPrefixes []string `json:"allowed_tool_prefixes,omitempty"`
    AllowedHosts        []string `json:"allowed_hosts,omitempty"`

    Status  ToolStatus `json:"status"`
    Version string     `json:"version"`
}

type ToolProviderEndpoint struct {
    BaseURL string `json:"base_url,omitempty"`
    CatalogPath string `json:"catalog_path,omitempty"` // default /tools/catalog
    InvokePath  string `json:"invoke_path,omitempty"`  // default /tools/invoke
    HealthPath  string `json:"health_path,omitempty"`  // default /health
    TimeoutMS   int    `json:"timeout_ms,omitempty"`
}
```

Provider 级别负责：

1. 服务地址、鉴权、host allowlist。
2. provider health check。
3. provider 下工具前缀约束。
4. catalog 同步。
5. provider 级启用/禁用。

ToolManifest 级别负责：

1. 单个工具的 schema、说明、risk、visibility。
2. group、tags、when_to_use。
3. version、status、manifest_hash。

### 7.2 Static ToolHost 协议

Static ToolHost 是生产推荐的外部工具接入方式。开发者独立部署一个服务，CleanCore 只和这个宿主服务通信。

ToolHost 标准接口：

```http
GET /tools/catalog
POST /tools/invoke
GET /healthz
```

当前代码中的 ToolProvider 已支持 provider 级治理参数：

```json
{
  "provider_id": "crm-tools",
  "provider_type": "static_tool_host",
  "endpoint": "https://crm-tools.internal",
  "auth_ref": "cred/crm-tools",
  "timeout_ms": 1500,
  "retry_max": 1
}
```

CleanCore 会在 catalog / healthz / invoke 请求上携带 `X-Origin-Provider-Auth-Ref`，该值是凭据引用，不是明文 secret；真实 secret resolver / KMS / mTLS 仍属于后续企业凭据闭环。`timeout_ms` 和 `retry_max` 会应用到 catalog、healthz 和 invoke。

Catalog 响应：

```json
{
  "provider_id": "crm-tools",
  "provider_version": "v1",
  "tools": [
    {
      "tool_id": "crm.customer.lookup",
      "name": "crm.customer.lookup",
      "description": "查询 CRM 客户资料。",
      "group_id": "crm",
      "tags": ["crm", "customer"],
      "when_to_use": ["查询客户资料", "客户 ID 查询", "CRM 客户画像"],
      "risk_level": "low",
      "visibility": "protected",
      "input_schema": {
        "type": "object",
        "required": ["customer_id"],
        "properties": {
          "customer_id": { "type": "string" }
        }
      },
      "output_schema": {
        "type": "object",
        "required": ["customer"],
        "properties": {
          "customer": { "type": "object" }
        }
      },
      "operation": "customer.lookup",
      "version": "v1"
    }
  ]
}
```

CleanCore 同步 catalog 后生成或更新 ToolManifest：

```json
{
  "tool_id": "crm.customer.lookup",
  "provider_id": "crm-tools",
  "executor": {
    "type": "static_tool_host",
    "provider_id": "crm-tools",
    "operation": "customer.lookup"
  },
  "execution_profile": {
    "domain_id": "tool_host"
  }
}
```

Invoke 请求：

```json
{
  "tool_call_id": "toolcall_xxx",
  "tenant_id": "tenant_1",
  "trace_id": "trace_xxx",
  "run_id": "run_xxx",
  "task_id": "task_xxx",
  "tool_id": "crm.customer.lookup",
  "operation": "customer.lookup",
  "group_id": "crm",
  "arguments": {
    "customer_id": "C123"
  },
  "idempotency_key": "toolcall:abc",
  "schema_version": "v1"
}
```

Invoke 响应：

```json
{
  "output": {
    "customer": {
      "id": "C123",
      "name": "张三",
      "level": "VIP"
    }
  },
  "artifact_refs": []
}
```

失败响应：

```json
{
  "error": {
    "code": "customer_not_found",
    "message": "customer not found",
    "details": {
      "customer_id": "C123"
    },
    "retryable": false
  }
}
```

ToolHost 接入优势：

1. CleanCore 不暴露任意 URL，只信任注册过的 provider。
2. provider 可以一次暴露多个工具，catalog/version/health 统一管理。
3. `auth_ref`、timeout、retry 已可在 provider 级治理；mTLS、真实 token 解析、allowlist、限流继续作为企业级增强。
4. 禁用 provider 可以一次性禁用其下工具。
5. 将来从 HTTP 换成 gRPC、queue、worker，只替换 provider adapter，不影响 Agent。
6. Agent 和 ToolRuntime 仍只面对 ToolManifest / ToolCall / ToolResult。

### 7.3 HTTP Direct 工具

HTTP Direct 是轻量外部工具接入方式。它直接在 ToolManifest 中配置 URL，不需要先注册 ToolHost provider。当前实现已在 ToolManifest 注册阶段强制 `executor.type=http_direct` 只能使用 `risk_level=low`，并在 HTTP execution domain 执行阶段要求显式 `allow_network=true` 和 `allowed_hosts` 校验；`disable_http_direct` / `CLEAN_CORE_DISABLE_HTTP_DIRECT` 可在生产环境关闭 direct HTTP 注册与 restore；生产上如果工具数量较多、需要统一鉴权/health/catalog/version 管理，应优先使用 `static_tool_host`。

#### 7.3.1 Manifest 示例

```json
{
  "tool_id": "crm.customer.lookup",
  "name": "crm.customer.lookup",
  "description": "查询 CRM 客户资料。",
  "group_id": "crm",
  "tags": ["crm", "customer"],
  "when_to_use": ["查询客户资料", "客户 ID 查询", "CRM 客户画像"],
  "risk_level": "low",
  "visibility": "protected",
  "input_schema": {
    "type": "object",
    "required": ["customer_id"],
    "properties": {
      "customer_id": { "type": "string" }
    }
  },
  "output_schema": {
    "type": "object",
    "required": ["customer"],
    "properties": {
      "customer": { "type": "object" }
    }
  },
  "executor": {
    "type": "http_direct",
    "http_direct": {
      "url": "https://tools.example.com/crm/customer.lookup",
      "method": "POST",
      "auth_ref": "cred/crm-tools",
      "timeout_ms": 8000
    }
  },
  "scope": {
    "tenant_id": "tenant_1"
  },
  "status": "enabled",
  "version": "v1"
}
```

#### 7.3.2 HTTP Direct 请求协议

CleanCore 调外部工具：

```http
POST /crm/customer.lookup
Authorization: Bearer <resolved-auth-ref>
Content-Type: application/json
```

请求体：

```json
{
  "tool_call_id": "toolcall_xxx",
  "tenant_id": "tenant_1",
  "trace_id": "trace_xxx",
  "run_id": "run_xxx",
  "task_id": "task_xxx",
  "tool_id": "crm.customer.lookup",
  "name": "crm.customer.lookup",
  "group_id": "crm",
  "arguments": {
    "customer_id": "C123"
  },
  "idempotency_key": "toolcall:abc"
}
```

响应体：

```json
{
  "output": {
    "customer": {
      "id": "C123",
      "name": "张三",
      "level": "VIP"
    }
  },
  "artifact_refs": []
}
```

失败响应建议：

```json
{
  "error": {
    "code": "external_tool_failed",
    "message": "customer not found",
    "details": {
      "customer_id": "C123"
    }
  }
}
```

HTTP adapter 应把外部失败转换成 `ToolResultFailed`，不要 panic，也不要绕过 ToolRuntime。

#### 7.3.3 HTTP ExecutionDomain 与 Adapter

```go
type HTTPExecutionDomain struct {
    Client         *http.Client
    CredentialRepo CredentialResolver
    HostPolicy     HostAllowlist
}

type HTTPDirectAdapter struct {
    Manifest catalog.ToolManifest
}

func (d HTTPExecutionDomain) Execute(ctx context.Context, req domain.ExecutionRequest) (domain.ExecutionResult, error) {
    // 1. build standard request body
    // 2. resolve auth_ref
    // 3. enforce allowed hosts / timeout / size limits
    // 4. send HTTP request
    // 5. parse response
    // 6. return output / artifact refs / execution metadata
}
```

注意：

- input schema 校验仍由 `ToolRuntime` 做。
- output schema 校验也仍由 `ToolRuntime` 做。
- HTTP execution domain 负责网络访问、credential 注入、host allowlist、timeout、size limit、审计元数据。
- HTTP adapter 只保存 manifest 中的 endpoint/protocol 信息，不应该自己绕过 execution domain 发请求。
- HTTP Direct 工具必须通过 `http` / `managed` / `worker` execution domain 执行，不能通过 `local` domain 偷偷发网络请求。
- `auth_ref` 只允许引用 credential store 中的凭据名，真实 secret 不能进入 manifest、PromptBundle、ToolCard、trace 明文。
- 外部 HTTP Direct 失败统一转换为 `ToolResultFailed`。错误码优先使用现有 `TOOL_EXECUTION_FAILED`，外部服务自己的错误码放入 `error.details.external_code`；如果后续需要新增 `EXTERNAL_TOOL_FAILED`，必须同步扩展 `contracts.ErrorCode` 校验。

#### 7.3.4 HTTP Direct 工具实现边界

外部开发者自己的服务需要保证：

1. endpoint 可访问。
2. 按 CleanCore 标准请求/响应协议处理。
3. 对 `idempotency_key` 做幂等，至少对写操作要支持幂等。
4. 不返回超过限制的大字段；大内容应返回 artifact 或摘要。
5. 失败时返回结构化 error。

CleanCore 保证：

1. 只把 schema 校验通过的参数发给外部服务。
2. 只在 Agent/Policy 允许时调用。
3. 对高风险工具进入 approval。
4. 调用结果写入 trace/audit/tool_results。
5. 输出不符合 output schema 时标记工具失败。

### 7.4 Provider Adapter 选择原则

Agent、PromptBundle、DecisionValidator、ToolRuntime 主流程不应该关心底层工具部署形态。不同 provider 只影响 `ExecutorFactory` 和 `ExecutionDomain`。

```text
ToolManifest.executor.type=go
  -> LocalExecutionDomain + Go executor

ToolManifest.executor.type=static_tool_host
  -> ToolHostExecutionDomain + ToolHost adapter

ToolManifest.executor.type=http_direct
  -> HTTPExecutionDomain + HTTPDirect adapter

ToolManifest.executor.type=worker
  -> WorkerExecutionDomain + Worker adapter

ToolManifest.executor.type=managed
  -> ManagedExecutionDomain + Managed adapter
```

切换 provider 的要求：

1. `tool_id` 可以不变，但 schema 变化必须生成新 version。
2. ToolCall / ToolResult 协议必须保持兼容。
3. risk、visibility、policy、approval 不因 provider 切换而降低。
4. run snapshot 中记录的 tool version、manifest hash、provider id 要能解释当时实际调用路径。
5. `static_tool_host` 和 `http_direct` 都要遵守相同的 timeout、size limit、idempotency、credential redaction 规则。

## 8. 运行时注入

### 8.1 是否支持运行后增加新工具

目标设计：支持。

流程：

```text
POST /v1/tool-manifests
  -> validate
  -> persist
  -> create executor
  -> register in RuntimeRegistry
  -> refresh card index
```

注册完成后：

- 新任务可以看到新工具。
- 下一轮 step 候选检索可以看到新工具。
- 已经构建完成的当前 PromptBundle 不自动变更。

### 8.2 是否每次任务注册一次

不应该。

正确生命周期：

```text
服务启动:
  加载 enabled manifests，注册到 RuntimeRegistry

API 注册/更新:
  热安装到 RuntimeRegistry

任务运行:
  每个 step 从 ToolCatalog + RuntimeRegistry 检索候选 ToolCard
  不做工具注册
```

### 8.3 运行中任务如何处理工具变化

建议规则：

- run 创建时记录 `tool_snapshot_hash`。
- step 构建 PromptBundle 时记录本 step 的候选 tool card 列表。
- model 只能调用本 step candidate set 中的工具。
- 如果 step 过程中工具被禁用，ToolRuntime 执行前仍要检查 status/disabled set，禁用则拒绝。

这样避免“模型没看到工具却突然能调用”或者“工具刚被禁用但旧 prompt 还在调用”的混乱。

## 9. API 设计

### 9.1 注册外部工具 Provider

```http
POST /v1/tool-providers
PUT /v1/tool-providers/{provider_id}
POST /v1/tool-providers/{provider_id}/sync
GET /v1/tool-providers
GET /v1/tool-providers/{provider_id}
POST /v1/tool-providers/{provider_id}/enable
POST /v1/tool-providers/{provider_id}/disable
```

注册 `static_tool_host`：

```json
{
  "provider_id": "crm-tools",
  "type": "static_tool_host",
  "name": "CRM Tools",
  "endpoint": {
    "base_url": "https://crm-tools.internal",
    "catalog_path": "/tools/catalog",
    "invoke_path": "/tools/invoke",
    "health_path": "/health",
    "timeout_ms": 5000
  },
  "auth_ref": "tenant:crm-tools-token",
  "allowed_tool_prefixes": ["crm."],
  "allowed_hosts": ["crm-tools.internal"],
  "status": "enabled",
  "version": "v1"
}
```

`sync` 语义：

```text
1. health check provider
2. fetch catalog
3. validate tool_id prefix / schema / risk / visibility
4. upsert ToolManifest versions
5. install enabled tools into RuntimeRegistry
6. invalidate CandidateProvider cache
```

### 9.2 注册工具

```http
POST /v1/tool-manifests
```

请求：

```json
{
  "tool_id": "crm.customer.lookup",
  "name": "crm.customer.lookup",
  "description": "查询 CRM 客户资料。",
  "group_id": "crm",
  "tags": ["crm", "customer"],
  "when_to_use": ["查询客户资料"],
  "risk_level": "low",
  "visibility": "protected",
  "input_schema": {
    "type": "object",
    "required": ["customer_id"],
    "properties": {
      "customer_id": { "type": "string" }
    }
  },
  "output_schema": {
    "type": "object"
  },
  "executor": {
    "type": "http_direct",
    "http_direct": {
      "url": "https://tools.example.com/crm/customer.lookup",
      "method": "POST",
      "auth_ref": "cred/crm-tools",
      "timeout_ms": 8000
    }
  },
  "scope": {
    "tenant_id": "tenant_1"
  },
  "status": "enabled",
  "version": "v1"
}
```

响应：

```json
{
  "manifest": {},
  "registered": true,
  "tool_card": {
    "tool_id": "crm.customer.lookup",
    "name": "crm.customer.lookup",
    "description": "查询 CRM 客户资料。",
    "when_to_use": ["查询客户资料"],
    "risk_level": "low",
    "visibility": "protected",
    "version": "v1"
  }
}
```

### 9.3 更新工具

```http
PUT /v1/tool-manifests/{tool_id}
```

开发阶段可以不考虑兼容旧数据，但不建议原地覆盖运行时语义。schema、endpoint、risk、visibility 变化应生成新 version，并更新 `current_version` 指针。

规则：

- 如果工具正在被某个 run 调用，不阻塞更新。
- 新 step 使用新 manifest。
- 已保存的 tool call/result 保持原记录。
- 更新后重新 install 到 RuntimeRegistry。

### 9.4 启用/禁用工具

```http
POST /v1/tool-manifests/{tool_id}/enable
POST /v1/tool-manifests/{tool_id}/disable
```

禁用规则：

- 从候选检索中移除。
- RuntimeRegistry 可保留但标记 disabled，或直接 unregister。
- ToolRuntime 执行前必须拒绝 disabled 工具。

建议实现 `Unregister(toolID)` 或 `SetEnabled(toolID, false)`。

### 9.5 查询工具

```http
GET /v1/tool-manifests
GET /v1/tool-manifests/{tool_id}
GET /v1/tool-providers
GET /v1/tool-providers/{provider_id}
GET /v1/tools/cards
GET /v1/tools/{tool_id}/definition
```

### 9.6 绑定工具到 Agent

```http
PUT /v1/agents/{agent_id}/tool-bindings
```

请求：

```json
{
  "allowed_tool_ids": ["crm.customer.lookup"],
  "exposed_tool_ids": [],
  "denied_tool_ids": []
}
```

也可以继续通过 AgentPackage 的 `tool_bindings` 管理。开发期建议两种都支持，但最终以 AgentPackage 编译结果为准。

### 9.7 注册工具组

```http
POST /v1/tool-groups
```

请求：

```json
{
  "group_id": "crm",
  "name": "CRM 工具组",
  "description": "查询和更新客户、商机、联系人等 CRM 数据。",
  "when_to_use": ["客户资料", "商机查询", "联系人", "CRM"],
  "tags": ["crm", "customer", "sales"],
  "risk_level": "medium",
  "status": "enabled",
  "version": "v1"
}
```

### 9.8 更新工具组

```http
PUT /v1/tool-groups/{group_id}
POST /v1/tool-groups/{group_id}/enable
POST /v1/tool-groups/{group_id}/disable
GET /v1/tool-groups
GET /v1/tool-groups/{group_id}/tools
```

禁用工具组时：

- 新候选检索不再返回该组。
- 组内工具默认不可检索。
- ToolRuntime 执行时如果工具所属组已禁用，应拒绝调用。

### 9.9 工具注册一站式 API

为了让外部开发者更省事，可以提供一个 upsert API：

```http
PUT /v1/tools/{tool_id}
```

语义：

- tool 不存在则创建。
- tool 存在则更新 manifest。
- 如果 `status=enabled`，立即热安装。
- 如果带 `bind_to_agents`，不能直接改内存中的 AgentDefinition；必须创建或更新 AgentPackage draft，然后通过一键发布流生效。

一站式 API 可以为了体验把多步流程封装起来，但内部顺序仍应是：

```text
1. ToolCatalogService.UpsertManifest
2. 如果需要绑定 Agent，创建/更新 AgentPackage draft 的 tool_bindings
3. validate draft
4. publish draft，生成新的 agent package version 和 compiled agent_definition
5. 根据参数进入 canary 或 stable
6. AgentRegistry.Put(compiled)
7. SetDefaultForTenant(agent_id, version)，仅 stable 时设置默认版本
```

禁止把“直接修改运行中内存 AgentDefinition”作为正式路径。开发阶段也建议遵守这个顺序，只是可以把 validate/publish/stable 做成一个 API 内部的快捷事务。

## 10. Registry 改造

当前 registry 只有 `Register/Get/Card/Cards`。建议改成：

```go
type ToolKey struct {
    TenantID contracts.TenantID // empty string means global
    ToolID   string
    Version  string
}

type Registry interface {
    Register(key ToolKey, tool Tool) error
    Upsert(key ToolKey, tool Tool) error
    Unregister(key ToolKey) error
    Get(tenantID contracts.TenantID, toolID string, version string) (Tool, bool)
    Definition(tenantID contracts.TenantID, toolID string, version string) (contracts.ToolDefinition, bool)
    Card(tenantID contracts.TenantID, toolID string, version string) (contracts.ToolCard, bool)
    Cards(filter CardFilter) []contracts.ToolCard
}
```

开发阶段不考虑兼容，建议直接支持 tenant-aware、version-aware 的 `Upsert` 和 `Unregister`。查找顺序建议是：

```text
1. tenant_id + tool_id + version
2. global + tool_id + version
3. 如果 version 为空，使用 catalog 当前 enabled version
```

这样才能支持：

- 租户级工具覆盖 global 工具。
- 运行中 run 按快照继续调用旧工具版本。
- 新 run 使用当前 enabled 版本。
- 工具被禁用时通过 catalog 状态即时拒绝，而不是依赖 registry 是否删除。

还需要保留 manifest：

```go
type Tool struct {
    Definition contracts.ToolDefinition
    Executor   registry.Executor
    WhenToUse  []string
    Manifest   catalog.ToolManifest
    Enabled    bool
}
```

或者不改原 `Tool`，在 catalog/store 里保存 manifest，registry 只保存可执行对象。无论采用哪种实现，`ToolRuntime` 都不能只信 registry；执行前还要用 catalog 做 availability check。

## 11. CandidateProvider 改造

当前实现仍保留 `StaticCandidateProvider` 这个名字，但 Core 装配时已经传入 `Registry: tools`，`Candidates()` 每次通过 `CardsForTenant(tenant_id)` 读取当前 runtime registry。也就是说，ToolCatalog 热注册/禁用后，新 run 和新 step 会看到更新后的工具卡，不再依赖启动时的一次性 `tools.Cards()` 快照。

后续如果需要更清晰的命名和更强的 catalog 查询能力，可以把它拆成显式的动态 provider：

```go
type RegistryCandidateProvider struct {
    Registry registry.Registry
    Catalog  catalog.Store
}
```

当前执行门禁已经由 `ToolRuntime.Availability` 回查 ToolCatalog；后续的 `RegistryCandidateProvider` 可以进一步把 catalog 状态、provider health、group tags/when_to_use 都纳入候选召回。`Registry` 只负责拿到可渲染、可执行的工具定义。

每次 `Candidates()`：

1. 从 catalog 读取 enabled tool groups。
2. 读取 enabled/current providers，过滤 disabled/unhealthy provider。
3. 按 objective + group `when_to_use/tags/description` 召回工具组。
4. 从命中的 group 中读取 enabled tool cards。
5. 叠加无 group 的全局工具候选。
6. 按 tenant/global scope 过滤。
7. 按 provider scope / provider status 过滤。
8. 按 Agent `allowed_tool_ids`、`denied_tool_ids` 过滤。
9. 按 Policy `allowed_tool_ids`、`denied_tool_ids` 过滤。
10. 跳过 `private`。
11. 按 objective + tool `when_to_use/tags/description` 打分排序。

这样热注册后新任务自然能看到新工具；当前 MVP 已通过 `CardsForTenant` 达到这个效果，并已对 allowed group 与 objective 命中的 `group_id` 做候选排序 boost。

如果 Agent 配置了 `allowed_tool_group_ids`，则只允许这些组参与召回。建议扩展 Agent tools config：

```go
type AgentToolsConfig struct {
    AllowedToolIDs      []string `json:"allowed_tool_ids"`
    ExposedToolIDs      []string `json:"exposed_tool_ids,omitempty"`
    DeniedToolIDs       []string `json:"denied_tool_ids,omitempty"`
    AllowedToolGroupIDs []string `json:"allowed_tool_group_ids,omitempty"`
    DeniedToolGroupIDs  []string `json:"denied_tool_group_ids,omitempty"`
}
```

该结构影响面：

1. `contracts.AgentToolsConfig` 增加 group allow/deny 字段。
2. `agentdef/package/compiler.go` 的 `validateToolBindings` 要校验 group id 非空、allow/deny 不冲突。
3. `server.go` 的 `parseToolsConfig` / `parseToolsPayload` 要解析 group 字段。
4. `policy.ToolPolicy` 也建议增加 `allowed_tool_group_ids` / `denied_tool_group_ids`，否则策略层不能整体禁用工具组。
5. `toolpolicy.Evaluator` 执行时要同时检查 tool id 和 group id。
6. `ToolCard` 或新增 `ToolGroupCard` 要携带 `group_id/tags`，否则 PromptBundle 和快照无法解释“为什么这个工具被召回”。
7. `tools.invoke` 的 `ExposedToolIDs` 仍然是外部直接调用白名单；如果要暴露整组工具，需要新增 `ExposedToolGroupIDs`，不能默认把 allowed group 全部暴露给外部调用。

MVP 可以不改模型 decision schema：服务端先召回工具组，再展开组内工具，最终仍只把少量 ToolCard 给模型。此时“先判断工具组”发生在 CandidateProvider 内部，不是模型显式输出 group choice。若后续希望模型先选择工具组，需要新增 hidden retrieval step 或扩展 decision schema。

## 12. ToolRuntime 改造点

当前 `ToolRuntime.Invoke()` 已经很好，但动态工具后必须补齐执行门禁：

1. 执行前检查 tool 是否 enabled。
2. 执行前检查 tool 所属 group 是否 enabled。
3. 执行前检查 tenant/global scope 是否允许当前租户使用。
4. 如果 run snapshot 中记录了 tool version，按该 version 取 registry executable；如果没有记录，才使用当前 enabled version。
5. 禁用状态是安全开关，应立即生效；即使 run snapshot 记录了旧版本，也必须拒绝执行 disabled tool/group。
6. 对 HTTP execution domain / adapter 增加统一错误转换。

可新增：

```go
type ToolAvailabilityChecker interface {
    Check(ctx context.Context, tenantID contracts.TenantID, toolID string, version string) (ToolAvailability, error)
}

type ToolAvailability struct {
    Allowed bool
    Reason  string

    TenantID contracts.TenantID
    ToolID   string
    Version  string
    GroupID  string
    ProviderID string

    ToolStatus     ToolStatus
    GroupStatus    ToolStatus
    ProviderStatus ToolStatus
}
```

如果 tool、group 或 provider disabled：

```json
{
  "status": "denied",
  "error": {
    "code": "tool_policy_denied",
    "message": "tool is disabled"
  }
}
```

## 13. 安全与治理

动态工具必须遵守这些规则：

1. `tool_id` 在同一 `tenant_id` scope 内唯一，不允许覆盖内核保留前缀，除非管理员模式。
2. `input_schema` 必须是 object schema。
3. `output_schema` 必须是 object schema。
4. `risk_level=high/critical` 默认需要 approval。
5. provider endpoint / HTTP URL 必须符合 allowlist 或租户配置。
6. `auth_ref` 不能把真实 secret 写进 manifest。
7. 模型只能看到 ToolCard，看不到 provider endpoint、HTTP secret。
8. 模型不能直接访问 provider endpoint / HTTP URL，只能输出 `tool_call`。
9. credential 解析必须走 credential store，并记录 `credential.used` 审计事件；trace 中只记录 credential ref，不记录 secret。
10. 外部工具需要 request/response 大小限制、超时、重试策略和幂等键传递。
11. ToolCard 不应暴露 endpoint、header、auth_ref 等执行细节。

建议保留前缀：

```text
system.*
origin.*
artifact.*
policy.*
```

业务工具推荐：

```text
crm.customer.lookup
order.refund.create
finance.invoice.query
```

## 14. 数据表草案

开发期可直接新增或重建：

```sql
CREATE TABLE tool_providers (
  tenant_id TEXT NOT NULL DEFAULT '',
  provider_id TEXT NOT NULL,
  provider_type TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  provider_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  provider_hash TEXT NOT NULL,
  current_version BOOLEAN NOT NULL DEFAULT TRUE,
  last_health_status TEXT,
  last_health_checked_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, provider_id, version)
);

CREATE UNIQUE INDEX uniq_tool_providers_current
  ON tool_providers (tenant_id, provider_id)
  WHERE current_version = TRUE;

CREATE INDEX idx_tool_providers_tenant_status
  ON tool_providers (tenant_id, status);

CREATE TABLE tool_manifests (
  tenant_id TEXT NOT NULL DEFAULT '',
  tool_id TEXT NOT NULL,
  group_id TEXT,
  provider_id TEXT,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  manifest_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  manifest_hash TEXT NOT NULL,
  current_version BOOLEAN NOT NULL DEFAULT TRUE,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, tool_id, version)
);

CREATE INDEX idx_tool_manifests_tenant_status
  ON tool_manifests (tenant_id, status);

CREATE INDEX idx_tool_manifests_group
  ON tool_manifests (tenant_id, group_id, status);

CREATE INDEX idx_tool_manifests_provider
  ON tool_manifests (tenant_id, provider_id, status);

CREATE UNIQUE INDEX uniq_tool_manifests_current
  ON tool_manifests (tenant_id, tool_id)
  WHERE current_version = TRUE;
```

工具组：

```sql
CREATE TABLE tool_groups (
  tenant_id TEXT NOT NULL DEFAULT '',
  group_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  group_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  group_hash TEXT NOT NULL,
  current_version BOOLEAN NOT NULL DEFAULT TRUE,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, group_id, version)
);

CREATE INDEX idx_tool_groups_tenant_status
  ON tool_groups (tenant_id, status);

CREATE UNIQUE INDEX uniq_tool_groups_current
  ON tool_groups (tenant_id, group_id)
  WHERE current_version = TRUE;
```

如果需要版本历史：

```sql
CREATE TABLE tool_manifest_versions (
  tool_version_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT '',
  tool_id TEXT NOT NULL,
  version TEXT NOT NULL,
  manifest_json JSONB NOT NULL,
  manifest_hash TEXT NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, tool_id, version)
);
```

说明：

- `tenant_id=''` 表示 global scope，避免 PostgreSQL 主键列不能为 null 的问题。
- `tool_providers` 保存 provider 配置和健康状态；ToolManifest 通过 `provider_id` 关联 provider。
- 禁用 provider 时，其下工具默认不可检索、不可执行；ToolRuntime availability check 必须同时检查 provider status。
- 同一个 `tenant_id + tool_id` 可以有多个 version，但只能有一个 `current_version=true` 的当前指针；是否可用由 `status` 决定。
- 如果开发期不需要保留多版本可执行体，可以仍然只启用当前版本；但 run snapshot 里记录到的历史版本如果要继续可执行，就必须保留对应 registry executable 或能够按版本重建。
- 工具组也按 version 存储，方便以后回放“某个 run 当时看到的是哪个组定义”。

## 15. 推荐落地步骤

### 第一阶段：内部结构统一（MVP 已落地）

1. 新增 `ToolManifest`。
2. 新增 catalog memory/Postgres store。
3. 内置工具仍通过 Go 注册进入 runtime registry。
4. ToolCatalogService 负责 manifest/provider/group 写入、恢复和 runtime registry 安装。
5. Core 启动时恢复持久化 manifest，并把 ToolCatalog 作为 ToolRuntime availability gate。

### 第二阶段：动态候选检索（MVP 已落地）

1. `StaticCandidateProvider` 通过 `Registry.CardsForTenant()` 动态读取 runtime registry。
2. 增加 `ToolGroup` 和 group catalog。
3. Agent/Policy 支持 allowed/denied tool group。
4. Prompt preview 和 agent run 使用同一候选入口。
5. 显式 `RegistryCandidateProvider` 命名和更复杂的 ToolGroup 策略可后续演进；基础 group-aware ranking 已落地。

### 第三阶段：外部工具 Provider（MVP 已落地）

1. 实现 `ToolProvider` / `static_tool_host` catalog 同步。
2. 通过 ToolManifest.executor 安装 `static_tool_host` / `http_direct` / `agent_tool` 等 adapter。
3. 保留 `http_direct` 作为轻量接入。
4. 通过 `tool.manifest.upsert` / `tool.provider.sync` 完成动态注册。
5. 增加 provider、schema、policy、trace 测试。

### 第四阶段：Agent 绑定 API（command 形态已落地）

1. 增加 `agent.package.tool_binding.update`。
2. prompt preview 显示候选工具。
3. e2e 验证：注册 ToolHost provider -> 同步工具 -> 绑定 Agent -> 模型 tool_call -> 外部工具被调用。

### 第五阶段：提示词在线更新（后续体验封装）

当前系统已有 AgentPackage draft/publish/canary/stable 流程，也已有：

```text
agent.package.draft.patch_prompt
agent.package.draft.patch_developer_prompt
agent.package.draft.patch_system_prompt
agent.package.draft.patch_agents_md
prompt.preview
agent.package.publish
agent.package.canary
agent.package.stable
```

但这套流程偏发布管理，不是最轻量的“运行后便捷更新当前 Agent 提示词”。建议新增 PromptProfile API：

```http
PUT /v1/agents/{agent_id}/prompt-profile
GET /v1/agents/{agent_id}/prompt-profile
POST /v1/agents/{agent_id}/prompt-profile/preview
POST /v1/agents/{agent_id}/prompt-profile/activate
```

`PromptProfile` 是 AgentPackage draft 的体验封装，不应绕过发布体系。`activate` 可以一键完成多步，但内部必须生成新的 package version、compiled definition 和 release 状态。

提示词生命周期建议：

```text
PromptProfile draft/update
  -> prompt.preview
  -> activate
  -> validate draft
  -> publish draft
  -> canary 或 stable
  -> AgentRegistry.Put(compiled)
  -> stable 时 SetDefaultForTenant(agent_id, version)
  -> 新 run 使用新 prompt
  -> 已运行 run 保持 version snapshot，不被热修改
```

原则：

- 不要修改正在执行中的 PromptBundle。
- 新 prompt 默认只影响新 run。
- 如果需要强制让运行中任务用新 prompt，应显式 `task.command=upgrade_agent_version` 或 resume 时升级。
- 不允许原地修改已经 stable 的 `agent_id + version`。如果提示词变了，就生成新 version 或 revision。

## 16. 最小 MVP 验收标准

MVP 应满足：

1. 服务启动时内置工具进入 runtime registry。
2. `tool.provider.upsert` 可以注册一个 `static_tool_host` provider。
3. `tool.provider.sync` 可以同步 provider catalog 到 ToolManifest。
4. `tool.manifest.upsert` 仍可注册一个 `http_direct` 工具，用于开发期和轻量场景。
5. 不重启服务，`tool.manifest.list` / prompt preview / agent run 能看到新工具。
6. Agent 绑定该工具后，prompt preview 能看到 retrieved tool card。
7. 模型输出 `tool_call` 后，ToolRuntime 通过 provider adapter 调用外部工具。
8. input/output schema 校验仍然生效。
9. 禁用工具或 provider 后，新任务不可检索，旧 step 调用会被拒绝。
10. `tool.group.upsert` 可以注册工具组。
11. 候选检索先命中工具组，再返回组内工具。
12. 提示词在线更新继续走 AgentPackage draft/publish/canary/stable；PromptProfile 是后续体验封装。

## 17. 最终开发者体验

内部开发者新增 Go 工具：

```go
registration.RegisterGoTool(ctx, EchoTool{})
```

业务开发者新增外部工具宿主：

```text
tool.provider.upsert
tool.provider.sync
```

少量轻量工具也可以直接注册 `http_direct` manifest：

```text
tool.manifest.upsert
```

然后绑定到 Agent：

```text
agent.package.tool_binding.update
```

任务运行时无需注册工具。工具作为租户级或全局能力长期存在，模型只在每次 step 中看到当前允许使用的候选工具。

外部业务开发者最终只需要两类接口：

```text
工具接入:
tool.provider.upsert / tool.provider.sync / tool.manifest.upsert

工具分组:
tool.group.upsert / tool.group.list
```

提示词优化者最终只需要：

```text
PUT /v1/agents/{agent_id}/prompt-profile
POST /v1/agents/{agent_id}/prompt-profile/preview
POST /v1/agents/{agent_id}/prompt-profile/activate
```

内部 Go 工具开发者才需要接触 `RegisterGoTool()`。

## 18. Agent 插件化与 DB/API 化设计

### 18.1 设计判断

Agent 开发者可以被视为一种“插件开发者”。CleanCore 主进程是稳定的 Agent Runtime / Hosting Framework，Agent 开发者不改主框架，只通过 API 提交和维护这个 Agent 的插件资产：

```text
AgentPlugin =
  prompt profile
  skill definitions
  tool bindings
  external tool manifests
  policy refs / policy drafts
  model settings
  capability metadata
  eval suites
  release state
```

运行时框架负责：

```text
task/run lifecycle
context retrieval
prompt bundle build
tool candidate retrieval
tool runtime invoke
handoff
policy / approval
trace / audit
eval
release routing
```

开发者负责：

```text
定义 Agent 做什么
写提示词
定义 skill
注册外部工具 provider / manifest
维护自己的工具服务
配置策略边界
提供 eval 用例
发布/回滚自己的 Agent
```

这个方向是合理的。它把 CleanCore 从“单个 Agent 项目”升级成“多 Agent 托管平台”。

### 18.2 当前代码现状

当前代码已经有插件化雏形，但还没有完整产品化。

已有能力：

1. **AgentPackage 源模型**

   `internal/agentdef/package/service.go` 中已有：

   ```go
   type AgentPackageSource struct {
       AgentsMD     string
       Prompt       string
       ToolBindings contracts.AgentToolsConfig
       Metadata     map[string]any
   }
   ```

   其中 `Metadata` 已承载：

   ```text
   system_prompt
   developer_prompt
   policy_set_id
   runtime limits
   skill_definitions
   ```

2. **AgentPackage 生命周期**

   已有 draft / proposal / validate / publish / canary / stable / rollback：

   ```text
   agent.package.draft.create
   agent.package.draft.patch_prompt
   agent.package.draft.patch_developer_prompt
   agent.package.draft.patch_system_prompt
   agent.package.draft.patch_agents_md
   agent.package.tool_binding.update
   agent.package.skill.add
   agent.package.skill.update
   agent.package.skill.remove
   agent.package.publish
   agent.package.canary
   agent.package.stable
   agent.package.rollback
   prompt.preview
   ```

3. **运行时 Agent Registry**

   `internal/agentdef/loader/loader.go` 中的 `StaticLoader` 提供：

   ```text
   Put(definition)
   Load(tenant_id, agent_id, version)
   SetDefaultForTenant(agent_id, version)
   ListByTenant(tenant_id)
   ```

   发布 AgentPackage 后，server 会执行：

   ```go
   appCore.AgentRegistry.Put(compiled)
   ```

4. **数据库已有核心表**

   `migrations/001_clean_core_base.sql` 已有：

   ```text
   agent_package_drafts
   agent_package_versions
   agent_package_proposals
   agent_package_eval_results
   agent_package_canary_hits
   agent_definitions
   policy_sets
   policy_drafts
   policy_versions
   agent_capabilities
   eval_suites
   eval_suite_results
   ```

5. **发布态路由**

   `resolveRunnableAgentTarget()` 已支持：

   ```text
   明确 version -> 检查版本是否 runnable
   未指定 version -> stable / canary / default 路由
   canary 命中记录 canary hit
   ```

当前不足：

1. 文件夹 AgentPackage 仍像“默认开发入口”，DB/API 不是唯一主路径。
2. 缺少一组面向 Agent 开发者的直观 CRUD API。
3. Agent 的 prompt / skill / tool / policy / model settings 分散在 package 命令和 metadata 中。
4. 工具注册还没有外部 provider、ToolHost catalog、HTTP Direct manifest。
5. Agent list/detail/status/update/delete 还不完整。
6. AgentCapability 和 AgentPackage 发布流没有完全闭环。
7. 运行时 registry 启动时没有从 DB 系统性恢复所有 stable/canary AgentDefinition。

### 18.3 目标抽象：AgentPlugin

建议引入产品概念 `AgentPlugin`，它不是替代 `AgentDefinition`，而是 Agent 开发者看到的高层对象。

```go
type AgentPlugin struct {
    TenantID    contracts.TenantID      `json:"tenant_id"`
    AgentID     contracts.AgentID       `json:"agent_id"`
    Name        string                  `json:"name"`
    Description string                  `json:"description"`
    OwnerID     string                  `json:"owner_id"`
    Status      AgentStatus             `json:"status"`
    ActiveVersion contracts.AgentVersion `json:"active_version,omitempty"`
    CreatedAt   time.Time              `json:"created_at"`
    UpdatedAt   time.Time              `json:"updated_at"`
}
```

`AgentStatus` 是顶层 Agent 状态，和 package version 的 `ReleaseStatus` 不同：

```text
draft
enabled
disabled
deleted
```

`AgentPluginVersion` 对应一个可发布版本：

```go
type AgentPluginVersion struct {
    PackageVersionID contracts.PackageVersionID `json:"package_version_id"`
    TenantID         contracts.TenantID         `json:"tenant_id"`
    AgentID          contracts.AgentID          `json:"agent_id"`
    Version          contracts.AgentVersion     `json:"version"`
    Status           contracts.ReleaseStatus    `json:"status"`
    Source           AgentPluginSource          `json:"source"`
    Compiled         contracts.AgentDefinition  `json:"compiled"`
    SourceHash       string                     `json:"source_hash"`
    CompiledHash     string                     `json:"compiled_hash"`
    CreatedBy        string                     `json:"created_by"`
    CreatedAt        time.Time                  `json:"created_at"`
    PublishedAt      *time.Time                 `json:"published_at,omitempty"`
}
```

`AgentPluginSource` 可以先复用 `AgentPackageSource`，但建议逐步结构化：

```go
type AgentPluginSource struct {
    PromptProfile PromptProfile              `json:"prompt_profile"`
    Skills        []SkillDraftInput          `json:"skills,omitempty"`
    ToolBindings  contracts.AgentToolsConfig `json:"tool_bindings"`
    PolicySetID   contracts.PolicySetID      `json:"policy_set_id,omitempty"`
    Runtime       RuntimeConfig              `json:"runtime,omitempty"`
    Model         ModelConfig                `json:"model,omitempty"`
    Capabilities  []CapabilityDraftInput     `json:"capabilities,omitempty"`
    Metadata      map[string]any             `json:"metadata,omitempty"`
}
```

这些类型不是全新的运行时概念，第一版可以这样映射：

```text
PromptProfile -> AgentPackageSource.Prompt + Metadata.system_prompt + Metadata.developer_prompt
RuntimeConfig  -> AgentPackageSource.Metadata.max_steps / max_tool_calls / max_duration_seconds ...
ModelConfig    -> 暂存 Metadata.model_*，真正按 Agent 选择模型需要改 Coordinator/ModelProvider
Capabilities   -> 复用现有 AgentCapability service/store
Skills         -> AgentPackageSource.Metadata.skill_definitions
```

其中 `ModelConfig` 是二期能力。当前 Coordinator 使用全局 `ModelProvider/ModelName`，如果要做到 per-agent model，需要把 model config 放入 `AgentDefinition` 和 `VersionSnapshot`，并让 `completeModel()` 按 agent/run snapshot 选择 provider。

编译时转换为现有 `contracts.AgentDefinition`：

```text
AgentPluginSource
  -> AgentPackageSource
  -> agentpackage.Compile()
  -> contracts.AgentDefinition
  -> AgentRegistry.Put()
```

开发阶段可以不马上大改 compiler，先把 API payload 映射到现有 `AgentPackageSource`。

### 18.4 Agent 开发者 API

建议提供资源化 API，而不是只依赖 `/v1/commands`。

#### 18.4.1 Agent CRUD

```http
POST /v1/agents
GET /v1/agents
GET /v1/agents/{agent_id}
PATCH /v1/agents/{agent_id}
DELETE /v1/agents/{agent_id}
```

`POST /v1/agents`：

```json
{
  "agent_id": "crm-assistant",
  "name": "CRM Assistant",
  "description": "处理 CRM 查询、客户资料总结和商机跟进。",
  "initial_version": "v1",
  "prompt_profile": {
    "system_prompt": "Return decisions as JSON.",
    "developer_prompt": "Use retrieved tools only.",
    "identity_prompt": "你是 CRM 协作助手。"
  },
  "tool_bindings": {
    "allowed_tool_group_ids": ["crm"],
    "allowed_tool_ids": ["crm.customer.lookup"]
  },
  "policy_set_id": "policy_default"
}
```

返回：

```json
{
  "agent": {
    "agent_id": "crm-assistant",
    "status": "draft",
    "active_version": ""
  },
  "draft": {
    "draft_id": "draft_xxx",
    "version": "v1"
  }
}
```

`GET /v1/agents` 应返回开发者最关心的状态：

```json
{
  "agents": [
    {
      "agent_id": "crm-assistant",
      "name": "CRM Assistant",
      "description": "处理 CRM 查询、客户资料总结和商机跟进。",
      "status": "enabled",
      "active_version": "v1",
      "latest_draft_id": "draft_xxx",
      "tool_count": 4,
      "skill_count": 2,
      "updated_at": "2026-06-01T00:00:00Z"
    }
  ]
}
```

删除建议做软删除：

```text
DELETE /v1/agents/{agent_id}
  -> status=deleted
  -> 不删除历史 run / trace / package versions
  -> 新 run 不可路由
```

#### 18.4.2 Draft 与版本

```http
POST /v1/agents/{agent_id}/drafts
GET /v1/agents/{agent_id}/drafts
GET /v1/agents/{agent_id}/drafts/{draft_id}
PATCH /v1/agents/{agent_id}/drafts/{draft_id}
POST /v1/agents/{agent_id}/drafts/{draft_id}/validate
POST /v1/agents/{agent_id}/drafts/{draft_id}/publish
```

`PATCH draft` 支持部分更新：

```json
{
  "prompt_profile": {
    "developer_prompt": "只调用 retrieved tool cards 中出现的工具。"
  },
  "tool_bindings": {
    "allowed_tool_group_ids": ["crm", "invoice"]
  },
  "runtime": {
    "max_steps": 4,
    "max_tool_calls": 8
  }
}
```

映射到现有命令：

```text
PATCH prompt_profile.identity_prompt -> PatchPromptForTenant
PATCH prompt_profile.developer_prompt -> PatchDeveloperPromptForTenant
PATCH prompt_profile.system_prompt -> PatchSystemPromptForTenant
PATCH tool_bindings -> UpdateToolBindingForTenant
PATCH skills -> UpsertSkillForTenant / RemoveSkillForTenant
```

#### 18.4.3 发布态

```http
GET /v1/agents/{agent_id}/versions
GET /v1/agents/{agent_id}/versions/{version}
POST /v1/agents/{agent_id}/versions/{version}/canary
POST /v1/agents/{agent_id}/versions/{version}/stable
POST /v1/agents/{agent_id}/versions/{version}/rollback
```

开发期也可以直接使用 `package_version_id`：

```http
POST /v1/agent-package-versions/{package_version_id}/stable
```

但面向 Agent 开发者，`agent_id + version` 更直观。

#### 18.4.4 Prompt Profile

提示词建议作为独立子资源：

```http
GET /v1/agents/{agent_id}/prompt-profile
PUT /v1/agents/{agent_id}/prompt-profile
POST /v1/agents/{agent_id}/prompt-profile/preview
POST /v1/agents/{agent_id}/prompt-profile/activate
```

`PUT` 默认修改 draft，不直接影响 stable：

```json
{
  "version": "v2",
  "system_prompt": "Return decisions as JSON.",
  "developer_prompt": "必须遵守工具边界。",
  "identity_prompt": "你是 CRM 协作助手。"
}
```

`activate` 是快捷发布流，不是内存热改：

```text
1. validate draft
2. publish draft，生成 package_version_id
3. compile AgentDefinition，写入 agent_definitions
4. canary 100 或 stable
5. AgentRegistry.Put(compiled)
6. stable 时 SetDefaultForTenant(agent_id, version)
7. 更新 agents.active_version / latest_draft_id
```

上线后可以增加 gate，但不改变核心发布语义：

```text
preview -> eval -> publish -> canary -> stable
```

#### 18.4.5 Skills

```http
GET /v1/agents/{agent_id}/skills
PUT /v1/agents/{agent_id}/skills/{skill_id}
DELETE /v1/agents/{agent_id}/skills/{skill_id}
```

请求：

```json
{
  "version": "v1",
  "name": "客户跟进摘要",
  "description": "根据 CRM 记录生成客户跟进摘要。",
  "instruction": "输出客户现状、风险、下一步动作。",
  "tags": ["crm", "summary"],
  "when_to_use": ["客户跟进", "客户摘要"],
  "risk_level": "low"
}
```

底层仍映射到 `AgentPackageSource.Metadata.skill_definitions`。

#### 18.4.6 Tool Bindings

```http
GET /v1/agents/{agent_id}/tool-bindings
PUT /v1/agents/{agent_id}/tool-bindings
```

建议扩展为：

```json
{
  "allowed_tool_group_ids": ["crm"],
  "denied_tool_group_ids": [],
  "allowed_tool_ids": ["crm.customer.lookup"],
  "denied_tool_ids": ["crm.customer.delete"],
  "exposed_tool_ids": []
}
```

这让 Agent 开发者可以不关心每个具体工具，只绑定工具组。

绑定更新也不能直接修改 stable AgentDefinition。`PUT /v1/agents/{agent_id}/tool-bindings` 的语义应是：

```text
1. 找到或创建该 agent 的 draft
2. 更新 draft.Source.ToolBindings
3. validate tool/group id 是否存在、是否属于当前 tenant/global scope
4. preview 或 validate
5. 由 publish/stable/activate 生效到新版本
```

如果需要“保存并立即生效”，API 可以提供 `activate=true` 参数，但内部仍然走一键发布流。

#### 18.4.7 Policy

现有 policy draft/publish 已经比较完整。Agent API 可以只引用 policy：

```http
PUT /v1/agents/{agent_id}/policy-ref
```

```json
{
  "policy_set_id": "policy_crm_assistant"
}
```

如果要一站式，也可以支持：

```http
PUT /v1/agents/{agent_id}/policy
```

它内部创建/更新 policy draft，然后发布 policy version。

#### 18.4.8 Capability

Agent 列表、路由和委派需要可检索能力。建议把 capabilities 纳入 AgentPlugin：

```http
GET /v1/agents/{agent_id}/capabilities
PUT /v1/agents/{agent_id}/capabilities/{capability_id}
DELETE /v1/agents/{agent_id}/capabilities/{capability_id}
```

请求：

```json
{
  "name": "CRM 客户资料查询",
  "description": "查询客户基础资料、等级、最近跟进记录。",
  "tags": ["crm", "customer"],
  "when_to_use": ["客户资料", "查客户", "客户跟进"],
  "risk_level": "low"
}
```

现有 `agentcapability.Service.Upsert()` 和 `agent_capabilities` 表可以复用。

#### 18.4.9 Eval

```http
GET /v1/agents/{agent_id}/eval-suites
POST /v1/agents/{agent_id}/eval-suites
POST /v1/agents/{agent_id}/eval-suites/{suite_id}/cases
POST /v1/agents/{agent_id}/eval-suites/{suite_id}/run
```

发布 stable 前建议要求 eval 通过。当前 package stable 已经有 eval gate 雏形，可继续使用。

### 18.5 DB 作为运行时 Source of Truth

当前文件夹式 AgentPackage 适合内置包和本地开发，但运行时应该逐步以 DB 为准。

建议最终关系：

```text
文件夹 AgentPackage
  -> import
  -> DB draft/version
  -> compile
  -> agent_definitions
  -> runtime AgentRegistry

DB AgentPlugin
  -> export
  -> 文件夹 AgentPackage
```

职责划分：

```text
文件夹:
  seed / template / local dev / code review backup

DB:
  runtime source of truth / multi-tenant state / API CRUD / release status

AgentRegistry:
  in-memory executable cache
```

服务启动时必须做恢复：

```text
1. load built-in file packages as seed, if configured
2. load DB agents whose status is enabled
3. load stable/canary package versions for enabled agents
4. load matching agent_definitions
5. AgentRegistry.Put(compiled definitions)
6. SetDefaultForTenant(agent_id, stable version)
7. load enabled/current tool_providers
8. load enabled tool_groups
9. load enabled/current tool_manifests
10. ToolCatalogService.InstallEnabledTools()
```

当前 `PackageStore` 已把 compiled definition 写入 `agent_definitions`，但 `StaticLoader` 启动时只初始化了 `TestAgentDefinition()`。需要新增 DB-backed loader 或启动恢复器。

恢复原则：

- 不建议无脑加载所有历史版本。默认加载 enabled agent 的 stable/canary/current runnable definitions，历史版本按需懒加载或由 run resume 按 snapshot 加载。
- 如果是多租户部署，可以按租户分批恢复，或在首次请求某 tenant 时懒加载该 tenant 的 runnable definitions。
- DB 是 source of truth；内存 `AgentRegistry` 和 `RuntimeRegistry` 是缓存。启动后缓存缺失时，应能从 DB 恢复，而不是要求重启或重新注册。
- 恢复工具和恢复 Agent 要使用同一套 service 方法，避免启动路径与 API 路径行为不同。

### 18.6 Loader 改造

当前：

```text
loader.StaticLoader
  definitions map
  defaults map
```

建议：

```go
type AgentDefinitionStore interface {
    GetDefinition(ctx context.Context, tenantID TenantID, agentID AgentID, version AgentVersion) (AgentDefinition, bool, error)
    ListRunnableDefinitions(ctx context.Context, tenantID TenantID) ([]AgentDefinition, error)
    ListRunnableVersions(ctx context.Context, tenantID TenantID, agentID AgentID) ([]AgentPackageVersion, error)
    GetAgentStatus(ctx context.Context, tenantID TenantID, agentID AgentID) (AgentStatus, bool, error)
}
```

新增：

```go
type HybridLoader struct {
    Memory *loader.StaticLoader
    Store  AgentDefinitionStore
}
```

加载顺序：

```text
1. 路由前先检查 agents.status，disabled/deleted agent 不可启动新 run。
2. 如果指定 version，先 memory，再 DB。
3. 如果未指定 version，用 release routing 解析 stable/canary/default。
4. DB 找到后回填 memory cache。
```

`resolveRunnableAgentTarget` 需要接入 `agents` 顶层状态。否则 `/v1/agents/{agent_id}` 软删除后，新 run、handoff、delegate 仍可能通过旧 `AgentRegistry` 启动。

也可以更激进：去掉 `StaticLoader` 的主路径，让它只是 cache。

### 18.7 运行时快照规则

插件化之后，热更新会变多，必须保护正在运行的任务。

规则：

1. Run 创建时固定：

   ```text
   agent_id
   agent_version
   package_version_id
   agent_definition_hash
   prompt_bundle_hash
   policy_version_id
   tool_snapshot_hash
   skill_snapshot_hash
   model_config_hash
   ```

2. 新 run 使用最新 stable/canary/default。
3. 已运行 run 默认不被 prompt/tool/skill/policy 热更新影响。
4. 如果要升级运行中任务，必须显式调用：

   ```text
   task.command = upgrade_agent_version
   ```

5. 当前 step 的 PromptBundle 生成后，不再改变。
6. 工具如果在 step 后被禁用，ToolRuntime 执行前仍要拒绝。

当前代码已有 `VersionSnapshot.ToolDefinitions map[string]string`，可以先记录 `tool_id -> version`。如果引入 tenant/global override 和 group，需要扩展 snapshot：

```text
tool_snapshot:
  tool_id
  tool_version
  tenant_id/global
  group_id
  manifest_hash
  group_hash
```

`prompt_bundle_hash` 只证明模型看到的上下文没有变化；工具实际执行时还需要 availability check，避免已禁用工具继续执行。

建议区分两种变化：

- manifest 更新、schema 更新、endpoint 更新：新 version，新 run 使用新版本，老 run 按 snapshot 使用旧版本。
- disable/delete/security revoke：安全开关即时生效，老 run 也不能继续执行被禁用工具。

### 18.8 Agent 插件边界和限制

插件化 Agent 不是无限自由。限制包括：

1. **不能自定义主 runtime loop**

   Agent 开发者不能随意替换：

   ```text
   Coordinator loop
   decision schema
   ToolRuntime
   policy engine
   task state machine
   ```

   除非未来提供高级 hook。

2. **不能上传任意代码到 CleanCore 执行**

   动态外部工具只能通过 static_tool_host / HTTP Direct / worker / managed adapter。

3. **工具必须 schema 化**

   所有工具必须有 input/output schema、risk level、visibility、auth_ref。

4. **提示词热更新不影响已生成 PromptBundle**

   这是为了可审计和可复现。

5. **大量工具必须通过工具组召回**

   否则 prompt 过大、模型误调用率上升。

6. **外部工具可用性成为系统稳定性的一部分**

   需要 timeout、retry、circuit breaker、idempotency、health check。

7. **策略必须独立治理**

   开发者不能通过 prompt 绕过 policy。

### 18.9 插件开发者体验

理想流程：

```text
1. 创建 Agent
   POST /v1/agents

2. 注册工具组
   PUT /v1/tool-groups/crm

3. 注册外部工具宿主
   PUT /v1/tool-providers/crm-tools

4. 同步工具 catalog
   POST /v1/tool-providers/crm-tools/sync

5. 给 Agent 绑定工具组
   PUT /v1/agents/crm-assistant/tool-bindings

6. 配置提示词
   PUT /v1/agents/crm-assistant/prompt-profile

7. 配置 skills
   PUT /v1/agents/crm-assistant/skills/customer-summary

8. 配置策略
   PUT /v1/agents/crm-assistant/policy-ref

9. 预览 PromptBundle
   POST /v1/agents/crm-assistant/prompt-profile/preview

10. 跑 eval
   POST /v1/agents/crm-assistant/eval-suites/suite_1/run

11. 发布
   POST /v1/agents/crm-assistant/drafts/draft_1/publish

12. 灰度
   POST /v1/agents/crm-assistant/versions/v1/canary

13. 稳定
   POST /v1/agents/crm-assistant/versions/v1/stable
```

开发者不需要：

```text
改 CleanCore Go 代码
重启 CleanCore
理解 internal/runtime/kernel
手工编辑 agent_packages 文件夹
```

### 18.10 与现有命令的映射

开发期可以先不重写底层服务，只增加 REST wrapper。

| 新 API | 现有能力 |
|---|---|
| `POST /v1/agents` | `agent.package.draft.create` |
| `PUT /v1/agents/{id}/prompt-profile` | `agent.package.draft.patch_*` |
| `PUT /v1/agents/{id}/tool-bindings` | `agent.package.tool_binding.update` |
| `PUT /v1/agents/{id}/skills/{skill_id}` | `agent.package.skill.add/update` |
| `POST /v1/agents/{id}/drafts/{draft_id}/validate` | `agent.package.draft.validate` |
| `POST /v1/agents/{id}/drafts/{draft_id}/publish` | `agent.package.publish` |
| `POST /v1/agents/{id}/versions/{version}/canary` | `agent.package.canary` |
| `POST /v1/agents/{id}/versions/{version}/stable` | `agent.package.stable` |
| `POST /v1/agents/{id}/versions/{version}/rollback` | `agent.package.rollback` |
| `POST /v1/agents/{id}/prompt-profile/preview` | `prompt.preview` |

这样可以快速达成产品体验，同时最大程度复用现有代码。

### 18.11 需要新增/调整的代码模块

建议新增：

```text
internal/agentplugin/
  service.go
  api_models.go
  mapper.go
  store.go
```

职责：

```text
AgentPluginService
  CreateAgent
  ListAgents
  GetAgent
  UpdateAgent
  DisableAgent
  CreateDraft
  PatchPromptProfile
  PatchToolBindings
  UpsertSkill
  Publish
  ActivateStable
```

`mapper.go` 负责：

```text
AgentPluginSource -> AgentPackageSource
AgentPackageSource -> AgentPluginSource
Draft -> AgentPlugin view
Release -> AgentPluginVersion view
```

server 层新增 REST route：

```text
/v1/agents
/v1/agents/{agent_id}
/v1/agents/{agent_id}/drafts
/v1/agents/{agent_id}/versions
/v1/agents/{agent_id}/prompt-profile
/v1/agents/{agent_id}/skills
/v1/agents/{agent_id}/tool-bindings
/v1/agents/{agent_id}/capabilities
/v1/agents/{agent_id}/eval-suites
```

也可以继续挂在 `/v1/commands`，但从开发者体验看，REST resource 更清晰。

### 18.12 DB 调整建议

当前已有 package/version/draft 表，缺的是 Agent 顶层实体和默认版本状态。

新增：

```sql
CREATE TABLE agents (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  status TEXT NOT NULL,
  active_version TEXT,
  latest_draft_id TEXT,
  owner_id TEXT,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, agent_id)
);
```

`status` 建议枚举：

```text
draft
enabled
disabled
deleted
```

语义：

- `enabled`：允许创建新 run、handoff/delegate、外部 invoke。
- `disabled`：不允许创建新 run；已运行 run 是否继续由运行策略决定，但默认不升级、不重新路由。
- `deleted`：软删除，列表默认隐藏，保留审计和历史 run 可回放需要的数据。
- `active_version` 只能指向 stable release。canary 仍由 package version release 状态和 canary rule 决定。

可选新增：

```sql
CREATE TABLE agent_prompt_profiles (
  profile_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  profile_json JSONB NOT NULL,
  profile_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
```

开发期可以不单独建 `agent_prompt_profiles`，继续放在 `agent_package_drafts.source_json` 中。  
但如果提示词会频繁独立更新，单表会更好查。

### 18.13 文件夹 AgentPackage 的定位

保留文件夹方式，但降级为：

```text
seed package
local development format
import/export format
fallback bootstrap package
```

新增导入导出：

```http
POST /v1/agent-packages/import
GET /v1/agents/{agent_id}/export
```

导入流程：

```text
read package.yaml / prompt.md / developer.md / system.md / skills
  -> AgentPackageSource
  -> DB draft
  -> validate
  -> publish if requested
```

导出流程：

```text
DB AgentPluginVersion
  -> agent_packages/{agent_id}/package.yaml
  -> prompt.md
  -> developer.md
  -> system.md
  -> skills
```

这样可以同时满足：

```text
平台在线管理
Git 审查
本地开发
备份迁移
```

### 18.14 开发阶段落地路线

#### A. 先补体验 API，不大改底层

1. 新增 `/v1/agents` list/detail。
2. 新增 REST wrapper，内部调用现有 package service。
3. 新增 `agents` 表记录顶层状态。
4. 发布 stable 时更新 `agents.active_version`。
5. `resolveRunnableAgentTarget` 接入 `agents.status`。
6. 保持 `AgentPackageSource` 不变。

#### B. 补启动恢复

1. `PackageStore` 增加 `ListRunnableDefinitions` / `ListRunnableReleases`。
2. Core 启动时加载 DB stable/canary definitions。
3. 回填 `AgentRegistry.Put()`。
4. 设置 tenant default version。

#### C. 引入外部工具 Provider

1. `ToolManifest` / `ToolGroup` / `ExternalToolProvider`。
2. `ToolHostExecutionDomain` / `ToolHostAdapter`。
3. `HTTPExecutionDomain` / `HTTPDirectAdapter` 作为轻量接入。
4. Agent tool bindings 支持 group。
5. CandidateProvider 改为 registry/catalog 动态读取。
6. ToolRuntime 接入 availability check。
7. ToolCatalogService 作为 API、provider sync 和启动恢复的唯一安装入口。

#### D. PromptProfile 快捷流

1. `PUT prompt-profile` 修改 draft。
2. `preview` 复用现有 prompt.preview。
3. `activate` 一键 validate/publish/stable，但不绕过 package release 表和 agent_definitions。

#### E. 完整插件治理

1. Eval gate。
2. Approval gate。
3. Audit/trace 完整记录。
4. Import/export。
5. Agent marketplace / catalog。

### 18.15 最小可行版本

MVP 可以定义为：

1. 通过 API 创建 Agent draft。
2. 通过 API 更新 prompt、skill、tool bindings。
3. 通过 API 注册 ToolHost provider，同步工具并绑定给 Agent。
4. `prompt.preview` 能看到新 prompt 和工具。
5. 发布后不重启即可运行新 Agent。
6. 服务重启后能从 DB 恢复 stable Agent。
7. `GET /v1/agents` 能看到 Agent 列表、状态、版本、工具数、skill 数。

这就是第一版“Agent 插件开发平台”。
