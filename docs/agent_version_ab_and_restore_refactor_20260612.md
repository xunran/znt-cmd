# Agent Version 轻量 A/B 与历史版本回退重构计划

日期：2026-06-12

## 1. 背景与目标

本计划基于当前代码中的 AgentPackage release 体系，不新增完整 `AgentExperiment` 平台，不做 Prompt/Skill 独立实验系统。

目标只收敛到两件事：

1. 用 `agent version` 做简单 A/B：
   - `stable` 版本作为 A 组。
   - `canary` 版本作为 B 组。
   - `canary_percent` 控制 B 组比例。
2. 能清楚、可靠地回退到任意旧的已发布 agent version：
   - 旧版本可查询。
   - 旧版本可显式运行。
   - 旧 stable 版本可重新激活为默认版本。
   - 当前默认路由尊重已激活版本，而不是总是抢到最新 stable。

非目标：

- 不新增 `agent_experiments` 表。
- 不支持多 variant 权重实验。
- 不做 winner / significance / dashboard。
- 不做 PromptProfileVersion 或 SkillVersion 的独立发布平台。
- 不兼容旧数据。本项目仍在开发阶段，可以直接调整 migration 和契约。

## 2. 当前代码事实

### 2.1 发布版本已经有留存

`PackageStore.SaveRelease` 会把 release 写入：

- `agent_package_versions.source_json`
- `agent_package_versions.compiled_json`
- `agent_definitions.definition_json`

因此历史版本的 source 和 compiled definition 已经具备持久化基础。

相关代码：

- `internal/storage/postgres/postgres.go:4614`
- `migrations/001_clean_core_base.sql` 中的 `agent_package_versions`、`agent_definitions`

### 2.2 当前 A/B 基础来自 canary

`resolveRunnableAgentTarget` 当前逻辑：

1. 如果请求显式传 `target.version`，直接校验该版本是否 runnable。
2. 如果未传版本：
   - 取 `AgentRegistry.DefaultVersionForTenant`。
   - 查 latest stable release。
   - 查 latest canary release。
   - stable 覆盖 default。
   - canary 命中时再覆盖 stable。

相关代码：

- `internal/server/agent_routing.go:21`
- `internal/server/agent_routing.go:101`
- `internal/server/agent_routing.go:122`

这个设计能做 stable vs canary 两组测试，但有两个问题：

1. 默认路由会优先 latest stable，导致手工激活旧 stable 后，默认流量仍可能被 latest stable 抢回。
2. 分桶 key 包含 `traceID`，如果每次请求 trace 不同，同一个用户可能在 A/B 之间跳组。

### 2.3 当前回退语义偏 rollback，不等于 restore old version

现有 `rollback` 是把某个 release 标记为 `rolled_back`，然后自动选择一个 fallback stable version：

- `internal/server/commands_agent_package.go:480`
- `fallbackStableVersion` 选择除当前版本外的最新 stable，默认 fallback 到 `v1`。

这适合“撤掉坏版本”，但不够表达“恢复到 N 个版本之前的指定版本”。

### 2.4 当前 runtime loader 依赖进程内 AgentRegistry

发布时 `releaseAndRegisterDraft` 会把 compiled definition 放进 `AgentRegistry`：

- `internal/server/commands_agent_package.go:51`

但服务启动时目前只初始化 `loader.TestAgentDefinition()`，没有看到从 `agent_definitions` 恢复所有已发布 definitions 到 `AgentRegistry` 的流程。

这会导致一个实际问题：

> DB 中老版本存在，但进程重启后 `AgentRegistry.Load` 未必能加载它。

如果要支持可靠历史回退，这个需要补齐。

## 3. 设计原则

1. 复用现有 release 状态机。
   `draft -> publish -> eval -> canary -> stable -> rollback` 不另起炉灶。

2. A/B 只支持两组。
   A = active/default stable 版本，B = 一个 canary 版本。

3. 激活版本是明确事实。
   `agent_assets.active_version/default_version` 应成为默认路由的第一优先级。

4. rollback 和 restore 分开。
   - rollback：标记坏版本不可运行。
   - restore/activate：把某个旧 stable 版本重新设为默认。

5. 记录路由事实，不建立复杂实验指标体系。
   先把每次路由解析写入 trace，后续统计可以基于 trace/run/canary hits 做。

## 4. 重构方案

### 4.1 默认路由改为尊重 active/default version

修改 `resolveRunnableAgentTarget`。

当前行为：

```text
defaultVersion = AgentRegistry default
latest stable 覆盖 defaultVersion
canary 命中后覆盖 stable
```

目标行为：

```text
如果请求显式传 version：
  校验并使用该 version

否则：
  baseVersion = agent_assets.active_version
  如果为空，使用 agent_assets.default_version
  如果仍为空，使用 AgentRegistry.DefaultVersionForTenant
  如果仍为空，使用 latest stable

  如果存在可用 canary 且命中 canary_percent：
    使用 canary version
  否则：
    使用 baseVersion
```

关键点：

- latest stable 只能作为 fallback，不能覆盖已激活的旧 stable。
- 这样用户执行 `POST /v1/agents/{agent_id}/versions/{old_version}/activate` 后，默认流量会真的回到旧版本。

建议新增 helper：

```go
func activeAgentVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) (contracts.AgentVersion, bool, error)
```

### 4.2 稳定 A/B 分桶

修改 `shouldRouteCanary` 的 hash key。

当前 key：

```text
tenant_id | agent_id | package_version_id | caller_id | trace_id
```

目标 key：

```text
tenant_id | agent_id | package_version_id | assignment_key
```

`assignment_key` 优先级：

1. `envelope.Context.UserID`
2. `caller.CallerID`
3. `envelope.Context.SessionID`
4. `envelope.Context.Collaboration.ExternalThreadID`
5. `traceID`

需要把 `shouldRouteCanary` 签名扩展为可拿到 `RuntimeContext`：

```go
func shouldRouteCanary(release contracts.AgentPackageVersion, caller auth.CallerIdentity, runtimeContext contracts.RuntimeContext, traceID contracts.TraceID, agentID contracts.AgentID) bool
```

这样同一个用户/调用方在实验期间会稳定落在 A 或 B。

### 4.3 补路由 trace 事件

当前只在 canary 命中时记录 `canary.routed`。这会导致 A 组缺少 denominator。

建议新增统一 trace event：

```text
agent.route.resolved
```

payload：

```json
{
  "agent_id": "xxx",
  "requested_version": "",
  "resolved_version": "v2",
  "release_status": "stable|canary|none",
  "package_version_id": "pkg_xxx",
  "route_reason": "explicit|active_default|latest_stable_fallback|canary_percent",
  "canary_percent": 10,
  "assignment_key_hash": "..."
}
```

注意：

- 不记录原始 user_id/caller_id/session_id。
- 只记录 hash，避免 trace 中暴露可识别身份。
- `recordCanaryRoute` 可以保留，用于兼容当前 canary hit 查询；但 trace 应同时覆盖 stable 和 canary。

### 4.4 明确 canary 选择规则

当前是 latest canary。为了保持简单，第一版仍只支持一个 B 组：

```text
latest canary release = B 组
```

但需要补一条约束：

- 当存在多个 canary release 时，只有 latest canary 参与默认路由。
- 旧 canary 可以显式传 version 运行，但不会自动接默认流量。

如需更强约束，可以在 `MarkCanaryWithRule` 时把同一 agent 的其他 canary 自动降为 `evaluated`，但第一版不建议做，避免改状态机过多。

### 4.5 新增 restore/activate old stable 的明确入口

现有 `POST /v1/agents/{agent_id}/versions/{version}/activate` 已经可以激活 stable 版本。

建议保留这个入口，增加一个语义更清楚的别名：

```text
POST /v1/agents/{agent_id}/versions/{version}/restore
```

行为与 activate 一致：

1. 目标 release 必须存在。
2. 目标 release 必须是 `stable`。
3. 调用 `EnsureAgentAssetVersionForTenant` 更新 active/default。
4. 调用 `AgentRegistry.SetDefaultForTenant`。
5. 写 audit/trace：
   - `agent.version.restored`
   - from_version
   - to_version
   - package_version_id
   - actor
   - reason

不建议允许 restore 到 `rolled_back`。如果确实要恢复 rolled_back 版本，应先显式重新标记 stable，这属于高风险治理动作。

### 4.6 启动时恢复已发布 agent definitions

新增 package definition restore 能力。

在 Postgres `PackageStore` 增加方法：

```go
ListAgentDefinitions(ctx context.Context) ([]contracts.AgentDefinition, error)
```

查询：

```sql
SELECT definition_json
FROM agent_definitions
ORDER BY created_at ASC
```

在 `core.New` 中，当 `pg != nil` 且 `packageStore` 可提供 definitions 时：

1. 读取所有 persisted agent definitions。
2. 逐个 `agents.Put(definition)`。
3. 再根据 `agent_assets.active_version/default_version` 设置 tenant default。

可以新增：

```go
type DefinitionRestoreStore interface {
    ListAgentDefinitions(ctx context.Context) ([]contracts.AgentDefinition, error)
    ListAgentAssets(ctx context.Context) ([]agentpackage.AgentAsset, error)
}
```

如果不想扩 store 接口太多，也可以在 Postgres 层加一个专用 restore 方法。

验收目标：

- 服务重启后，历史 version 仍可 `agent.run target.version=old`。
- 服务重启后，已 restore 的旧 active/default 仍生效。

### 4.7 rollback 行为收敛

当前 rollback 会自动 fallback 到另一个 stable。这个保留，但要修两个边界：

1. fallback 不应该固定默认 `v1`，如果没有其他 stable，应返回明确错误或清空 default。
2. rollback 后要同步 `agent_assets.active_version/default_version`，不能只改 `AgentRegistry`。

建议：

```text
rollback 当前 active version:
  找到 previous stable
  更新 agent_assets active/default
  更新 AgentRegistry default

rollback 非 active version:
  只标记 rolled_back
  不切默认版本
```

这样 rollback 与 restore 的职责更清楚。

## 5. API 与命令调整

### 5.1 保留现有

```text
GET  /v1/agents/{agent_id}/versions
GET  /v1/agents/{agent_id}/versions/{version}
POST /v1/agents/{agent_id}/versions/{version}/activate
POST command agent.package.canary
POST command agent.package.stable
POST command agent.package.rollback
```

### 5.2 新增

```text
POST /v1/agents/{agent_id}/versions/{version}/restore
```

请求：

```json
{
  "reason": "restore previous stable after online regression"
}
```

响应复用 activate：

```json
{
  "agent": {},
  "version": {}
}
```

### 5.3 可选新增查询

为了便于前端展示历史版本，可以在 version view 里补充：

```json
{
  "active": true,
  "default": true,
  "runnable": true,
  "route_role": "control|candidate|inactive"
}
```

`route_role` 规则：

- `control`：当前 active/default stable。
- `candidate`：latest canary。
- `inactive`：其他历史版本。

## 6. 数据模型调整

不新增实验表。

建议调整/确认现有表：

### 6.1 `agent_assets`

继续作为 active/default 事实源：

```text
active_version
default_version
```

要求：

- activate/restore/stable/rollback 都必须同步更新。
- 默认路由优先读取它。

### 6.2 `agent_package_canary_hits`

保留，用于 B 组命中记录。

不建议扩成完整 experiment hits 表。

### 6.3 Trace event

新增 trace type：

```go
TraceAgentRouteResolved = "agent.route.resolved"
TraceAgentVersionRestored = "agent.version.restored"
```

如果当前 trace type 不要求枚举，也可以直接写字符串；但建议放入 `contracts/governance.go`，保持集中管理。

### 6.4 运行日志与诊断视图

补齐面向运维和提示词优化师的只读运行日志视图，不新增表，聚合现有事实源：

- `agent_runs`
- `trace_events`
- `task_events`
- `tool_calls` / `tool_results`
- `audit_events`
- `runtime_hook_events`

服务端入口：

```text
GET /v1/runs
GET /v1/runs/{run_id}
GET /v1/runs/{run_id}/timeline
GET /v1/runs/{run_id}/diagnostics
GET /v1/runs/{run_id}/final-response
GET /v1/traces/{trace_id}/diagnostics
```

运维视角重点看：

- run 状态、耗时、错误码、错误信息。
- trace/task/tool/audit/runtime hook 时间线。
- tool 调用、tool 失败、approval 等阻塞点。
- `agent.route.resolved` 中的路由原因、命中版本、canary_percent。

提示词优化师视角重点看：

- `prompt_bundle_hash`、policy version、agent/package version。
- `model.called` / `model.completed` 的模型、tokens、失败信息。
- `decision.created` / `decision.validated` / `decision.repair_requested` 的决策类型与修复次数。
- 运行日志不存完整 PromptBundle 明文；需要复盘完整提示词时，用 `prompt.preview` 按同 agent/version/input 重建预览，避免 trace 中沉淀敏感上下文。

## 7. 代码改造任务拆解

### T1. 路由优先级重构

文件：

- `internal/server/agent_routing.go`

内容：

- 增加 active/default version resolver。
- latest stable 只做 fallback。
- 返回 `route_reason`、`release_status`、`assignment_key_hash` 等路由元数据。

测试：

- 激活旧 stable 后，默认 `agent.run` 使用旧版本。
- 未激活任何版本时，默认走 latest stable。
- 显式传 version 时，不进入 canary。

### T2. 稳定 canary 分桶

文件：

- `internal/server/agent_routing.go`

内容：

- `shouldRouteCanary` 使用 runtime context 生成 assignment key。
- hash 不再默认依赖 traceID。

测试：

- 同一 user_id 多次请求稳定落同组。
- 不同 user_id 可按比例分散。
- 缺少 user_id 时 fallback 到 caller/session/trace。

### T3. 路由 trace

文件：

- `internal/server/agent_routing.go`
- `internal/server/server.go`
- `internal/contracts/governance.go`

内容：

- 增加 `recordAgentRouteResolved`。
- stable/canary 都记录。
- canary 命中继续记录 `canary.routed` 和 canary hit。

测试：

- stable 默认路由产生 `agent.route.resolved`。
- canary 路由同时产生 `agent.route.resolved` 和 `canary.routed`。

### T4. Restore endpoint

文件：

- `internal/server/handlers_agent_subresource_versions.go`

内容：

- 支持 `/versions/{version}/restore`。
- 复用 `activateStableAgentVersion`，额外写 audit/trace reason。

测试：

- restore stable 旧版本成功。
- restore non-stable 失败。
- restore missing version 失败。
- restore 后默认 run 使用该旧版本。

### T5. 启动恢复 persisted agent definitions

文件：

- `internal/storage/postgres/postgres.go`
- `internal/app/core/core.go`
- 可能新增 `internal/agentdef/loader/postgres_restore.go` 或 package store 接口文件

内容：

- 从 `agent_definitions` 读取所有 compiled definitions。
- 启动时 Put 到 `AgentRegistry`。
- 从 `agent_assets` 恢复 default version。

测试：

- 发布 v2、restore v1、重启 core 后默认仍是 v1。
- 重启后显式运行 v2 成功。

### T6. Rollback 同步 agent_assets

文件：

- `internal/server/commands_agent_package.go`
- `internal/agentdef/package/service.go`

内容：

- rollback active version 时同步 asset active/default。
- 无 fallback stable 时返回明确错误。
- rollback 非 active version 不切 default。

测试：

- rollback 当前 active v3 后 fallback 到 v2。
- rollback 非 active v2 时 active v3 不变。
- 没有其他 stable 时 rollback 给出清晰错误。

## 8. 验收场景

### 8.1 简单 A/B

1. 发布 `v1`。
2. eval pass。
3. stable `v1`。
4. 发布 `v2`。
5. eval pass。
6. canary `v2`，`canary_percent=10`。
7. 不传 version 执行多次 `agent.run`。
8. 期望：
   - 大部分 route 到 `v1`。
   - 小部分 route 到 `v2`。
   - 同一 assignment key 稳定落同组。
   - trace 中能看到 A/B 两组路由事实。

### 8.2 回退到 N 个版本之前

1. 已有 stable：`v1`、`v2`、`v3`。
2. 当前 active/default 是 `v3`。
3. 调用：

```text
POST /v1/agents/{agent_id}/versions/v1/restore
```

4. 不传 version 执行 `agent.run`。
5. 期望默认运行 `v1`，而不是 latest stable `v3`。

### 8.3 重启后仍可回退和运行历史版本

1. 发布 `v1`、`v2`。
2. restore 到 `v1`。
3. 重启服务。
4. 不传 version 执行 `agent.run`。
5. 显式传 `v2` 执行 `agent.run`。
6. 期望：
   - 默认仍是 `v1`。
   - 显式 `v2` 可运行。

## 9. 不做项与原因

### 不做 `AgentExperiment`

原因：

- 当前需求只需要两个版本验证。
- canary 已经覆盖 B 组分流。
- 新增 experiment 会引入 variant、assignment、metrics、winner 等概念，当前阶段收益不够。

### 不做多 canary 权重

原因：

- 会破坏 latest canary 的简单模型。
- 会引入多个候选版本的指标归因问题。
- 第一版 two-arm A/B 足够。

### 不做 Prompt/Skill 独立版本实验

原因：

- 当前 package version 已经能完整表达 prompt/skill/tool binding 的组合变化。
- 独立实验会绕开 release/eval/stable/rollback 治理链路。
- 等 agent version 级别实验跑顺后再考虑。

## 10. 推荐实施顺序

1. T1 路由优先级重构。
2. T2 稳定 canary 分桶。
3. T3 路由 trace。
4. T4 restore endpoint。
5. T5 启动恢复 persisted definitions。
6. T6 rollback 同步修正。

其中 T1、T4、T5 是“能回退旧版本”的核心；T2、T3 是“能像样做 A/B”的核心。

## 11. 最终形态

最终用户心智应保持简单：

```text
发布一个新 agent version。
通过 eval 后设为 canary。
用 canary_percent 做小流量 A/B。
效果好就 stable/activate。
效果不好就 rollback。
需要回到旧版本就 restore old stable version。
```

系统内部仍然只有一条主线：

```text
AgentPackage release + AgentAsset active/default + Runtime route trace
```

这样既能满足简单 A/B 和历史回退，又不会把当前项目拖进过度设计。
