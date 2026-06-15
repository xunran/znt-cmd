# AgentOps 控制面与 Agent 运行范式解耦开发任务计划

## 0. 背景与目标

当前 `znt-cmd` 已经具备明显的模块化边界，但还不是严格的 AgentOps 控制面与 Agent runtime 解耦架构。

真实代码中的现状：

- `internal/app/core/core.go` 将 `Runs`、`Trace`、`Audit`、`Approvals`、`Policies`、`ToolCatalog`、`ServiceConnections`、`RuntimeHooks`、`ToolRuntime`、`Coordinator` 和 `Packages` 装配在同一个 `Core`。
- `internal/runtime/kernel/coordinator.go` 是当前默认 Agent 执行器，负责加载 agent、创建 run、写 trace、组 prompt、调用模型、调用工具、推进 run 状态。
- `internal/agentdef/package` 已经有 `AgentPackageSource`、`AgentPluginSource`、`AgentAsset`、draft/publish/version/rollback 等控制面雏形。
- `internal/contracts/agent_source.go` 已经有 `AgentSourceKindPackage` 和 `AgentSourceKindPlugin`，说明 source kind 扩展缝已经存在。
- `internal/server/handlers_routes.go` 同时暴露 `/v1/commands`、`/v1/runs`、`/v1/traces`、`/v1/audit`、`/v1/tool-providers`、`/v1/service-connections`、`/v1/agents` 等控制面和运行时入口。

本计划目标：

1. 将 AgentOps 控制面与具体 Agent 执行范式逻辑解耦。
2. 将当前默认 `Coordinator` 明确封装为第一种内置 runtime / carrier。
3. 为未来便捷新增不同 Agent 载体预留稳定扩展点，例如 `agent_plugin_source`、`workflow_graph`、`external_runtime`。
4. 保证治理能力不打折：run、trace、audit、policy、tool、credential、approval 不被任何 carrier 绕过。
5. 第一阶段只做逻辑边界和架构重整，不强求拆成多个微服务。
6. 当前阶段允许 breaking change，优先保证控制面与 runtime 边界干净；不为旧的 agent/source/tool API 追加长期兼容层。

## 1. 核心原则

### 1.1 控制面管理稳定事实

控制面必须长期稳定管理：

- Agent / carrier 资产目录
- 版本、发布、回滚、canary、stable
- run 账本和状态
- trace / audit / evidence
- policy refs 与 policy decision
- tool catalog / tool binding / tool runtime gateway
- service connection / credential boundary
- policy 触发的 approval handshake
- runtime conformance 状态

### 1.2 Runtime 只负责执行或调度

具体 Agent 范式只负责执行或调度：

- native prompt/model/tool loop
- plugin source 编译后的 managed runtime
- workflow graph runtime
- external runtime callback / event reporting

Runtime 不应直接成为控制面事实源。

### 1.3 不变量优先于抽象命名

任何 carrier 都必须满足治理不变量：

- run 必须由控制面创建或登记。
- trace/audit 必须进入统一 recorder/logger。
- tool call 必须走 `ToolRuntime` 或受控 gateway。
- service connection secret 不能暴露给 runtime 任意使用。
- 高风险动作必须由控制面 policy 决定是否进入 approval handshake。
- policy refs、release status、version snapshot 必须可追溯。
- external runtime 不得直接写控制面事实源。

## 2. 目标架构

### 2.1 当前形态

```text
Core
  ├─ AgentPackage / AgentAsset
  ├─ Runs / Trace / Audit / Approval / Policy
  ├─ ToolCatalog / ToolRuntime / ServiceConnections
  └─ Coordinator
       └─ native agent execution
```

### 2.2 目标逻辑形态

```text
AgentOps Control Plane
  ├─ AgentCarrier / AgentAsset
  ├─ CarrierVersion / Release
  ├─ Run ledger
  ├─ Trace / Audit
  ├─ Policy / Approval
  ├─ ToolCatalog / ToolRuntime gateway
  ├─ ServiceConnections / credential boundary
  └─ Runtime conformance

Runtime Layer
  ├─ NativeRuntimeDriver       -> wraps current Coordinator
  ├─ AgentPluginSourceAdapter  -> plugin manifest/source compile
  ├─ WorkflowGraphDriver       -> future
  └─ ExternalRuntimeDriver     -> future
```

### 2.3 关键抽象

建议引入或明确化以下概念：

```text
AgentCarrier
  carrier_id / agent_id
  tenant_id
  display_name
  carrier_kind
  source_kind
  runtime_contract
  status
  current_version
  policy_refs
  capabilities
  conformance_status
  metadata
```

初始 carrier kind：

- `native_agent`：当前 `AgentDefinition + Coordinator`。
- `agent_plugin_source`：AgentPlugin Service 声明 source，CleanCore 编译治理并由 managed runtime 执行。
- `workflow_graph`：未来 workflow/graph 范式。
- `external_runtime`：未来用户自带 Agent runtime。

初始 runtime contract：

- `managed`：平台负责执行和强治理。
- `connected`：外部 runtime 执行，但工具、凭证、审批必须受控。
- `observed`：外部 runtime 只上报 run/trace/audit，控制面提供观测，不承诺强治理。

实施策略：

- 第一阶段只实现并产品化 `managed`。
- `connected` 和 `observed` 先作为内部架构分级和后续 contract 草案，不在第一阶段做成用户必须选择的产品入口。
- 文档中保留三种等级，是为了避免未来 external runtime 接入时重新推翻控制面边界；不是要求第一版同时落地三套运行模式。

独立部署与接入关系：

- `agent_plugin_source` 可以部署在独立服务器上，但它主要是外部能力声明源；CleanCore 拉取 manifest、编译为平台 carrier，并由 managed runtime 执行。
- `external_runtime` 才表示用户自带 Agent runtime。它可以独立部署，也可以不接入 AgentOps 控制面而作为普通外部 agent 服务运行。
- 当 `external_runtime` 接入控制面时，按 `connected` 或 `observed` contract 接入。`connected` 追求“外部独立执行 + 工具/凭证/审批/trace 受控”；`observed` 只提供观测，不承诺强治理。
- 文档中的治理不变量只约束“接入 AgentOps 控制面后的行为”。未接入平台的外部 agent 可以独立运行，但不属于平台治理范围。

### 2.4 Approval 语义

审批不是默认运行流程，也不是每次 agent run 都需要管理员处理。

审批是控制面 policy 对高风险动作触发的例外门禁：

```text
Policy Engine
  -> allow      自动放行
  -> deny       自动拒绝
  -> approval   暂停并进入人工审批
```

应触发审批的典型场景：

- 发布 agent 新版本、切 stable、rollback。
- 修改高权限 tool binding。
- 新增或修改 service connection、secret rotation。
- 执行高风险工具，例如删除数据、生产写入、转账、发外部邮件、生产变更。
- 跨 workspace / 跨权限域读取敏感数据。
- 绑定高风险 runtime hook。
- 外部 runtime 请求使用受控 credential。
- policy 判断不确定，需要人工复核。

不应触发审批的典型场景：

- 普通 run。
- 低风险查询工具。
- 已授权的内部只读能力。
- 常规 trace/audit 上报。

职责边界：

- Runtime 只能报告动作意图和上下文，不能自己决定审批结果。
- 控制面负责 policy decision、approval request、审批人选择、证据展示、audit 记录和恢复/终止动作。
- 遇到 `approval_required` 时，managed runtime 暂停 run；connected runtime 必须进入 pause/resume handshake；observed runtime 不承诺强制审批。

## 3. 阶段计划

## Phase 0：边界审计与基线锁定

目标：不改业务行为，先形成真实代码边界地图和治理不变量测试清单。

### 任务 0.1 包边界分类

审计以下代码并分类：

| 路径 | 当前角色 | 目标分类 |
| --- | --- | --- |
| `internal/app/core/core.go` | 总装配，控制面与 runtime 混合 | composition root |
| `internal/runtime/kernel` | 当前 native agent 执行器 | managed runtime driver |
| `internal/runtime/run` | run repository | control-plane |
| `internal/governance/trace` | trace recorder | control-plane |
| `internal/governance/audit` | audit logger | control-plane |
| `internal/governance/approval` | approval service | control-plane |
| `internal/policy/engine` | policy engine / manager | control-plane |
| `internal/agentdef/package` | agent source/draft/release | control-plane + native source adapter |
| `internal/agentdef/plugin` | plugin manifest/source | source adapter |
| `internal/tool/catalog` | tool provider/manifest/group | control-plane |
| `internal/tool/runtime` | governed tool execution | control-plane gateway / runtime-adjacent |
| `internal/serviceconnection` | connection assets | control-plane |
| `internal/runtime/hook` | runtime hook provider/manifests | control-plane extension + runtime hook adapter |
| `internal/server` | API adapter | mixed API surface |

产出：

- `docs/agent-control-plane-runtime-boundary-audit.zh-CN.md`
- 标明所有 mixed 包的拆分建议。

### 任务 0.2 API 面分类

审计 `internal/server/handlers_routes.go` 中所有 `/v1/*` 路由，分为：

- 通用 control-plane API
- native-agent 专属 API
- runtime 操作 API
- legacy command API（待收敛或迁移）

重点检查：

- `/v1/commands`
- `/v1/runs`
- `/v1/traces`
- `/v1/audit`
- `/v1/agents`
- `/v1/agents/{id}/prompt-profile`
- `/v1/agents/{id}/tool-bindings`
- `/v1/agents/{id}/skills`
- `/v1/tool-providers`
- `/v1/service-connections`
- `/v1/runtime-hook-*`

产出：

- API 分类表。
- 哪些 API 不应假设所有 carrier 都有 prompt/profile/tool-binding。

### 任务 0.3 治理不变量测试清单

整理当前已有测试覆盖：

- `internal/server/server_test.go`
- `internal/runtime/kernel/coordinator_test.go`
- `internal/tool/runtime/runtime_test.go`
- `internal/agentdef/package/service_test.go`
- `internal/agentdef/plugin/manifest_test.go`

补充缺口测试计划：

- run 必须写入 source kind / carrier kind。
- run 状态变更必须写 trace。
- tool call 必须经过 policy。
- policy-triggered approval required 必须暂停 run。
- service connection secret 不进入 trace/audit 明文。
- external/provider 调用必须有 provider/service_connection 证据。

验收：

- 产出测试矩阵，但 Phase 0 可不立即实现全部测试。

## Phase 1：明确 native agent carrier

目标：当前默认 Agent 行为不变，但语义上从“唯一 agent 模型”变成“第一种 carrier”。

### 任务 1.1 扩展 contracts

文件：

- `internal/contracts/agent_source.go`
- `internal/contracts/agent.go`
- `internal/contracts/run.go`

建议：

```go
type AgentCarrierKind string

const (
    AgentCarrierKindNativeAgent       AgentCarrierKind = "native_agent"
    AgentCarrierKindAgentPluginSource AgentCarrierKind = "agent_plugin_source"
    AgentCarrierKindWorkflowGraph     AgentCarrierKind = "workflow_graph"
    AgentCarrierKindExternalRuntime   AgentCarrierKind = "external_runtime"
)

type RuntimeContractKind string

const (
    RuntimeContractManaged   RuntimeContractKind = "managed"
    RuntimeContractConnected RuntimeContractKind = "connected"
    RuntimeContractObserved  RuntimeContractKind = "observed"
)
```

将以下结构增加新边界字段：

- `AgentPackageVersion`
- `AgentRun`
- `RunStep` 或 run snapshot
- `AgentAsset` 或后续 `AgentCarrier`

字段建议：

- `carrier_kind`
- `runtime_contract`
- `source_kind`
- `source_provider_id`
- `manifest_hash`
- `conformance_status`

注意：

- 当前阶段允许修改数据结构与 API schema，不承诺对旧开发数据长期兼容。
- 如果本地开发数据阻碍边界重整，可以通过重建 seed / 重跑 migration 处理。
- 对测试和本地联调保留最小迁移说明即可，不为了历史字段继续扩展兼容分支。

### 任务 1.2 将 AgentAsset 语义升级为 carrier view

文件：

- `internal/agentdef/package/service.go`
- `internal/storage/postgres` 中 package/agent asset store
- migrations

做法：

- 短期不一定新增表，可先在 `AgentAsset` 增加 carrier 字段。
- 明确 `AgentAsset` 是控制面资产视图，不等于 runtime 内部 `AgentDefinition`。
- `ListAgentAssets` 返回 carrier metadata。

验收：

- `GET /v1/agents` 返回新的 carrier 信息。
- 旧测试可按新语义更新；不要求维持旧字段的长期兼容。

### 任务 1.3 Release 写入 carrier 信息

文件：

- `internal/agentdef/package/service.go`
- `internal/agentdef/package/compiler.go`

在 `PublishDraft` 生成 `AgentPackageVersion` 时写入：

- `SourceKind`
- `SourceProviderID`
- `ManifestVersion`
- `ManifestHash`
- `CarrierKind`
- `RuntimeContract`

验收：

- `TestPackageDraftValidatePublishWritesAudit` 扩展断言 carrier 信息。
- plugin source release 也写入 `agent_plugin_source`。

## Phase 2：拆出 RuntimeDriver 边界

目标：把 `Coordinator` 包装成 native runtime driver，控制面通过 driver 调用执行，不直接绑定 `Coordinator`。

### 任务 2.1 定义 runtime driver interface

新增包建议：

```text
internal/runtime/driver
  driver.go
  native.go
  conformance.go
```

接口草案：

```go
type Driver interface {
    Kind() contracts.AgentCarrierKind
    Contract() contracts.RuntimeContractKind
    StartRun(ctx context.Context, req StartRunRequest) (StartRunResult, error)
    ResumeRun(ctx context.Context, req ResumeRunRequest) (RunResult, error)
    CancelRun(ctx context.Context, req CancelRunRequest) (RunResult, error)
    Preview(ctx context.Context, req PreviewRequest) (PreviewResult, error)
    ValidateSource(ctx context.Context, req ValidateSourceRequest) (ValidateSourceResult, error)
}
```

第一阶段只实现：

```go
type NativeDriver struct {
    Coordinator kernel.Coordinator
}
```

### 任务 2.2 包装 Coordinator

文件：

- `internal/runtime/kernel/coordinator.go`
- 新增 `internal/runtime/driver/native.go`
- `internal/app/core/core.go`

要求：

- `NativeDriver.StartRun` 内部调用当前 `Coordinator.HandleEnvelope`。
- 行为完全不变。
- `Core` 中新增 `RuntimeDrivers` registry。
- 当前 `/v1/commands agent.run` 仍能走 native driver。

验收：

- 现有 run 相关测试全部通过。
- 新增测试验证 native driver 与直接 Coordinator 结果一致。

### 任务 2.3 识别 Coordinator 内控制面职责

分析并标注 `Coordinator` 中哪些职责未来应上移：

- run id / trace id 创建
- `Runs.Create`
- run status update
- policy pinning / version snapshot
- trace event 写入
- tool invocation gateway
- approval pause/resume

第一阶段不全部迁移，只加 TODO/文档，不要大拆。

产出：

- `docs/native-runtime-driver-extraction-notes.zh-CN.md`

## Phase 3：建立 carrier registry 与 dispatch

目标：控制面根据 carrier kind / runtime contract 选择 runtime driver。

### 任务 3.1 RuntimeDriver registry

新增：

```text
internal/runtime/driver/registry.go
```

功能：

- Register(driver)
- Get(kind, contract)
- Default native driver

`Core` 增加：

```go
RuntimeDrivers *driver.Registry
```

### 任务 3.2 run command dispatch

文件：

- `internal/server/commands*.go`
- `internal/app/core/core.go`

当前 `agent.run` 应：

1. 根据 agent/carrier asset 查 `carrier_kind`。
2. 查 runtime driver。
3. 通过 driver start run。

重构要求：

- 所有新建 carrier 必须显式写入 carrier kind / runtime contract。
- 开发期旧 seed 或旧测试数据可通过重建处理，不要求空字段长期自动 fallback。
- `AgentEnvelope` 可保留为 native/managed runtime 的输入格式，但不得作为所有 future carrier 的唯一运行协议。

验收：

- `agent.run` 旧测试通过。
- 新增测试：未知 carrier kind 返回明确错误，不落到 native runtime。

### 任务 3.3 run record 增加 carrier snapshot

文件：

- `internal/contracts/run.go`
- `internal/runtime/run`
- Postgres migration

run 创建时记录：

- `carrier_kind`
- `runtime_contract`
- `source_kind`
- `source_provider_id`
- `carrier_version`
- `manifest_hash`

验收：

- `/v1/runs` 和 `/v1/runs/{id}` 返回 carrier snapshot。
- 老 run 默认展示 native/package。

## Phase 4：SourceAdapter 边界

目标：source 解析/校验/编译与 runtime driver 分离。

### 任务 4.1 定义 SourceAdapter

新增包建议：

```text
internal/agentdef/source
  adapter.go
  native_package.go
  plugin_service.go
  registry.go
```

接口草案：

```go
type Adapter interface {
    Kind() contracts.AgentSourceKind
    Normalize(ctx context.Context, req NormalizeRequest) (NormalizedSource, error)
    Validate(ctx context.Context, source NormalizedSource) error
    Compile(ctx context.Context, source NormalizedSource) (CompiledCarrier, error)
}
```

### 任务 4.2 迁移现有 package compile

文件：

- `internal/agentdef/package/compiler.go`

短期做 wrapper：

- `PackageSourceAdapter` 调用现有 `Compile`。
- 不改变 `Compile` 行为。

验收：

- compiler tests 不变。

### 任务 4.3 迁移 plugin manifest source

文件：

- `internal/agentdef/plugin/manifest.go`
- `internal/agentdef/package/compiler.go`

短期做 wrapper：

- `PluginServiceSourceAdapter` 调用 `BuildSource` + `CompilePlugin`。
- 明确 plugin source 仍是 `managed` contract，不是 external runtime。

验收：

- `manifest_test.go` 增加 source adapter 测试。
- plugin manifest 不允许携带 direct credential / base URL 作为 source fact。

## Phase 5：AgentPlugin Service 作为第二种 carrier source

目标：把 AgentPlugin Service 从“工具来源”提升为“外部能力声明源”，但不让它绕过控制面。

### 任务 5.1 对齐 provider/service connection 模型

文件：

- `internal/tool/catalog`
- `internal/serviceconnection`
- `internal/agentdef/plugin`

规则：

- `AgentPluginSource.provider_id` 指向 `ToolProvider`。
- `ToolProvider.provider_type` 必须是 `agent_plugin_service`。
- 连接事实来自 `ToolProvider.ServiceConnectionID`。
- `AgentPluginSource` 不保存 base_url / secret_ref。

验收：

- provider 类型不匹配时 compile 失败。
- service connection 不健康时 manifest sync 失败。

### 任务 5.2 manifest sync flow

实现或补齐流程：

```text
ToolProvider(agent_plugin_service)
  -> ServiceConnection
  -> GET /.well-known/agent-plugin.json
  -> Validate AgentPluginManifest
  -> Build AgentPluginSource
  -> Sync ToolManifest
  -> Create/Update Draft
```

相关文件：

- `internal/agentdef/plugin/manifest.go`
- `internal/tool/catalog`
- `internal/server/handlers_tool_catalog.go`
- `internal/server/commands_agent_package.go` 或新增 handler

验收：

- manifest hash 进入 release。
- tools 同步为 `ToolManifest`，executor type 为 `agent_plugin_service`。
- hooks 同步为 runtime hook binding 或 manifest。
- AgentPlugin Service 不能直接写 run/task/memory。

### 任务 5.3 release report 加 manifest evidence

文件：

- `internal/release/report.go`

新增：

- source kind
- carrier kind
- provider id
- service connection id
- manifest version
- manifest hash
- compiled hash
- strategy hash

验收：

- native agent release report 保留现有等价信息，并补充 carrier/source/runtime evidence。
- plugin source release report 可追溯 manifest。

## Phase 6：Workflow / External runtime 预留，不立即做深

目标：建立扩展缝，避免当前代码继续焊死 native agent。

### 任务 6.1 增加未实现 carrier kind 的明确错误

当 carrier kind 为：

- `workflow_graph`
- `external_runtime`

但 driver 未注册时，返回明确错误：

```text
AGENT_RUNTIME_DRIVER_UNAVAILABLE
```

不要静默 fallback 到 native driver。

### 任务 6.2 external runtime contract 草案

新增文档：

- `docs/external-agent-runtime-contract-draft.zh-CN.md`

内容包括：

- external runtime 可独立部署、独立运行，不接入控制面时不属于平台治理范围。
- external runtime 后续接入控制面时可能支持的等级：
  - `managed`：一般不适用于外部 runtime，除非平台托管该 runtime。
  - `connected`：外部执行，但 run、tool、credential、approval、trace/audit 接入控制面。
  - `observed`：外部执行，只上报 run/trace/audit，平台只提供观测。
- run create / ack
- status event
- trace event
- approval request / resume
- tool invocation gateway
- credential delegation
- conformance tests

不要急着实现完整外部 runtime；第一阶段不产品化 `connected` / `observed`。

验收：

- contract 草案必须明确哪些能力在 `observed` 模式不可承诺，例如强制工具策略、强制审批、凭证隔离。
- contract 草案必须明确 `connected` 模式的最小强治理要求：工具调用通过 gateway、凭证通过 delegated credential、approval 支持 pause/resume、trace/audit 标准化。
- contract 草案不得让第一阶段实现范围膨胀；第一阶段只要求 native `managed` driver 可运行、可治理、可测试。

## Phase 7：Conformance tests

目标：新增 carrier 必须通过统一治理验收，防止架构漏水。

### 任务 7.1 新增 conformance 测试包

建议：

```text
internal/runtime/conformance
  suite.go
  native_test.go
  plugin_source_test.go
```

测试项：

1. can register carrier
2. can publish version
3. can create run
4. run includes carrier snapshot
5. emits trace events
6. writes audit where required
7. tool call goes through ToolRuntime
8. policy denial blocks tool call
9. policy-triggered approval required pauses run
10. secret is not exposed in trace/audit
11. old run snapshot remains reproducible
12. unsupported runtime contract fails closed

### 任务 7.2 native driver conformance

当前 native runtime 必须先通过 conformance。

验收：

- native driver 成为 reference runtime。
- 后续 carrier 以 native conformance 为基线。

## Phase 8：API 与 OpenAPI 对齐

目标：对外 API 不再暗示所有 Agent 都是 native prompt agent。

### 任务 8.1 OpenAPI schema 更新

文件：

- `docs/openapi.clean-core.v1.json`
- server response structs

新增字段：

- `carrier_kind`
- `runtime_contract`
- `source_kind`
- `conformance_status`
- `manifest_hash`

### 任务 8.2 API 分层命名

建议：

- `/v1/agents` 作为现有主列表入口暂保留，但返回新的 carrier view。
- native 专属能力用 `/v1/agents/{id}/prompt-profile` 等现有路径，但 response 中标记 unsupported reason。
- 未来可增加 `/v1/agent-carriers`，但不在第一阶段强推。

验收：

- PloyKit 读取 `/v1/agents` 不需要知道 runtime 内部细节。
- 对 unsupported native-only API 返回明确错误，而不是空数据假成功。

## Phase 9：PloyKit / Origin AgentOps 对接调整

目标：前端控制面按 carrier 思维展示，不把 UI 焊死到 native agent。

相关仓库：

- `D:\code2\znt\ploykit\modules\origin-agentops`

任务：

1. Agent 列表展示 `carrier_kind` / `runtime_contract`。
2. Agent 详情页按能力显示 tabs：
   - native prompt profile
   - tools
   - runtime
   - trace
   - release
3. 对非 native carrier，不显示或禁用 native-only 编辑入口。
4. AgentPlugin Service 创建页明确是 `agent_plugin_source`，不是 external runtime。
5. run / trace 页面展示 source kind、provider id、manifest hash。

验收：

- 前端不需要判断内部 `Coordinator`。
- 外部 carrier 出现时不会进入错误的 native 编辑流程。

## 4. 兼容与迁移策略

### 4.1 本阶段允许 breaking change

当前项目仍处在架构重整与产品化联调阶段，本计划不承诺外部 API / 数据结构向后兼容。

优先级：

1. 控制面与 runtime 边界干净。
2. 治理不变量完整可测。
3. carrier/source/runtime contract 可扩展。
4. 当前测试、seed、PloyKit 联调可恢复。
5. 旧字段、旧表结构、旧 command 兼容层不作为长期目标。

允许：

- 调整 OpenAPI schema。
- 重命名 source/carrier/runtime 字段。
- 重做开发期 migration。
- 删除 UI-only 或后端忽略的旧字段。
- 将旧 `/v1/commands` 中不适合 command envelope 的能力迁到更清晰的 REST / driver 入口。

不允许：

- 为了省迁移继续让 `AgentDefinition` 承担所有未来 carrier 的公共模型。
- 为了兼容旧字段让 runtime 绕过 run/trace/audit/policy/tool/approval。
- 为了保留旧 API 让 unsupported carrier 返回空数据假成功。

### 4.2 新字段建议

可能涉及表：

- agent assets / packages
- agent package versions
- drafts
- runs
- run steps
- tool providers
- trace event payload 可不迁移，只新增后续事件字段

原则：

- 不修改已有 id 语义。
- 不让 `AgentDefinition` 承担所有 carrier 的公共模型。
- external runtime 专属字段放 metadata 或独立 contract 表，不污染 native source。
- 如果旧开发数据无法无损迁移，优先提供 seed 重建和一次性迁移脚本，不引入长期兼容分支。

## 5. 测试矩阵

| 范围 | 测试 |
| --- | --- |
| contracts | enum validate、显式 carrier/runtime contract、JSON roundtrip |
| package service | draft/publish/release 写 carrier 信息 |
| compiler | native/package/plugin source 行为不变 |
| runtime driver | native driver 包装 Coordinator 行为一致 |
| run repo | run carrier snapshot list/get/filter |
| server | `/v1/commands agent.run` dispatch driver |
| tool runtime | policy/approval/trace 不被 driver 绕过 |
| service connection | secret redaction |
| release report | carrier/source/runtime evidence |
| conformance | native driver baseline |
| PloyKit smoke | `/dashboard/origin-agentops` pages still live |

## 6. 风险与防护

### 风险 1：过早抽象导致改动过大

防护：

- Phase 1/2 只包装，不重写 Coordinator。
- 保持现有 API 和测试通过。

### 风险 2：carrier 概念和 AgentAsset 重叠

防护：

- 先把 `AgentAsset` 作为 carrier view 扩展，不急着新表。
- 文档明确 `AgentDefinition` 是 native compiled model，不是所有 carrier 的公共模型。

### 风险 3：外部 runtime 绕过治理

防护：

- external runtime 初期只设计 contract，不急着开放执行。
- conformance tests 先行。
- connected 模式必须走 ToolRuntime / credential delegation / approval handshake。

### 风险 4：PloyKit UI 误导用户

防护：

- carrier kind 驱动页面能力。
- native-only tab 对非 native carrier 明确显示 unsupported。

## 7. 推荐实施顺序

1. Phase 0：边界审计与不变量清单。
2. Phase 1：native agent carrier 字段与 release/run snapshot。
3. Phase 2：NativeRuntimeDriver 包装 Coordinator。
4. Phase 3：driver registry 和 run dispatch。
5. Phase 7：native conformance tests。
6. Phase 4/5：AgentPlugin Service 作为 source adapter / carrier source。
7. Phase 8/9：OpenAPI 与 PloyKit UI 对齐。
8. Phase 6：external runtime contract 文档，后续再实现。

## 8. 完成定义

本计划第一阶段完成时，应满足：

- 当前默认 agent 行为不变。
- 所有 run/release 可以看到 carrier/source/runtime contract 信息。
- `Coordinator` 被包装为 native runtime driver。
- 控制面可以根据 carrier kind dispatch runtime。
- 未实现 carrier 不会 fallback 到 native runtime。
- AgentPlugin Service 明确是 source carrier，不是无限制 external runtime。
- External runtime 明确可以独立部署、独立运行；只有接入控制面后才进入 managed/connected/observed contract 约束。
- 文档和代码不再为旧 agent/source/tool 字段追加长期兼容层；必要兼容只服务测试、seed 和联调恢复。
- 治理不变量有测试覆盖。
- 新增一种 carrier 的工作量被限制在 source adapter / runtime driver / conformance tests / UI 能力声明内。
