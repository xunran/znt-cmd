# origin-agentops 与新 znt 真实对齐分析

日期：2026-06-14  
范围：`D:\code2\znt\ploykit\modules\origin-agentops` 对当前 `D:\code2\znt\znt-cmd` 的 znt HTTP/OpenAPI/command/runtime 契约静态对齐分析。  
结论性质：基于真实代码、OpenAPI、znt server dispatch 和 origin-agentops 实际调用点；本次未启动 PloyKit 模块做端到端联调，因此不把“可编译/可运行”写成已验证事实。

## 修复标注（2026-06-14 续）

本节是按本文原“建议修复顺序”和硬缺口逐项实施后的真实状态标注。

| 项 | 状态 | 真实处理 |
|---|---|---|
| `x-roles` 缺失 | 已修 | `origin-agentops/lib/service-client.ts` 增加 role intent/自动推断：runtime command 走 `runtime_caller`，optimizer command 走 `optimizer`；关键调用显式传 role。 |
| Agent metadata 行为字段 | 已修 | Agent 创建/发布 payload 把 `strategies/skills/skill_definitions` 放到结构化顶层；metadata 增加 blocklist，禁止 `system_prompt/developer_prompt/max_tool_calls/skills/strategies` 等行为字段回流。 |
| `tools.invoke` smoke payload | 已修 | `actions/smoke-invoke.ts` 改为 `payload.arguments + target.agent_id/version`；UI 测试调用会选择绑定/暴露该工具的 agent，没有 agent 时明确报错。 |
| rollback payload | 已修 | `actions/rollback-agent.ts` 改为发送 `agent.package.rollback` 的 `package_version_id + reason`；发布/read model/release history 保存并回读 znt `package_version_id`。 |
| `skill.run` unsupported | 已修 | `actions/run-skill.ts` 不再发送 `skill.run`，改为 agent-scoped `agent.run`；本地审计 action 改为 `skill.agent_run`。 |
| approvals HTTP/API | 已修 | znt 新增通用 `/v1/approvals` GET/POST、`/v1/approvals/{id}` GET/PATCH，DELETE 明确 405；POST/PATCH 要求 `optimizer/admin`；origin-agentops 创建用 POST，解决审批用 `approval.approve/reject` command，删除仅本地工作台移除。 |
| ToolGroup 字段漂移 | 已修一层 | origin-agentops 不再把 `provider_id/risk/tool_ids/agent_ids/tags/owner` 作为 znt ToolGroup 顶层字段发送；这些仍作为 UI metadata/cache，并从 ToolManifest 反算成员。 |
| mock/test 漂移 | 已修 | smoke mock 增加 schema 断言：`tools.invoke` 禁止 `args` 且要求 `target.agent_id`；禁止 agent metadata 行为字段；禁止 ToolGroup UI 聚合字段顶层发送；禁止 `skill.run` 出现。 |
| OpenAPI clean-core | 已修 | `docs/openapi.clean-core.v1.json` 新增当前代码真实支持的 approvals 路径；未把 bulk/request-info 等未实现能力写入 clean-core。 |

验证结果：

- `npm run module:test -- modules/origin-agentops`：通过，doctor 0 warning，12/12 tests pass。
- `npm run typecheck`：通过。
- znt Docker 测试：通过。`docker build --no-cache --progress=plain -t znt-cmd-test .` 已在 Dockerfile 内执行 `go test ./...` 并完成 Linux build。
- znt Docker release 检查：通过。使用 `golang:1.22-bookworm` 容器执行 `gofmt -l ./cmd ./internal ./pkg` 空输出、`go vet ./...` 通过、`go test ./... -count=1` 通过（包含 `znt/internal/server`）。
- 备注：本机 Windows PATH 仍未找到 `go`/`gofmt`；本轮 znt 验证以 Docker 容器内 Go 1.22.12 为准。初次 Docker 格式检查发现 `internal/server/handlers_approvals.go` 需要 gofmt，已用容器内 `gofmt -w` 修复后重跑通过。

说明：下方原始分析章节保留为修复前基线与证据链；若与本节“修复标注”冲突，以本节状态为当前代码状态。

## 总结

origin-agentops 的 `tests/service-contract.json` 中登记的 43 个 method/path，在当前 `docs/openapi.clean-core.v1.json` 中 method/path 级别全部存在，机械比对结果为 `missing_count=0`。

但源码真实调用不止 `tests/service-contract.json`。源码中额外存在 approvals HTTP API、`skill.run`、`tools.invoke` smoke、rollback command、trace evidence/replay mock 等路径或命令。按当前 znt 实现看，核心 Agent/Tool/ServiceConnection/Run 读写路径大部分已经接近对齐，但仍有若干会导致真实 live 调用失败的硬缺口：

- `origin-agentops/lib/service-client.ts` 默认只传 `x-caller-id`、`x-caller-type`、`x-tenant-id`，不传 `x-roles`；znt 未收到 `X-Roles` 时默认为 `runtime_caller`，而 `agent.package.*`、`tool.*` 命令、`approval.*` 等 command 要求 `optimizer/admin`。
- Agent 创建/发布仍把 `system_prompt`、`developer_prompt`、`max_tool_calls`、`skills`、`strategies` 等行为字段塞入 `metadata`；新 znt 的 `ValidateSourceMetadata` 明确拒绝这些字段，要求放到结构化 source 或 `strategies`。
- `actions/smoke-invoke.ts` 发送 `tools.invoke` payload `{ tool_id, args }` 且没有 `target`；znt 当前读取 `payload.arguments`，并加载 `target.agent_id` 对应 agent，且要求工具在该 agent 的 exposed tools 中。
- `actions/rollback-agent.ts` 发送 `{ agent_id, target_version, current_version }`；znt 的 `agent.package.rollback` 需要 `package_version_id`，并按 release policy 读取 `reason`。
- `actions/run-skill.ts` 发送 command `skill.run`；当前 znt `dispatchCommand` 没有 `skill.run` 分支，会返回 unsupported command。
- `api/approvals.ts`、`api/approval.ts`、`actions/resolve-approval.ts` 调用 `/v1/approvals`、`/v1/approvals/{approval_id}`；当前 znt OpenAPI/route 没有这些通用 HTTP approvals 路由，只提供 `approval.approve`/`approval.reject` command 和 `/v1/runtime-hook-approvals` 列表。

因此，不能说 origin-agentops 已经与新 znt 全面真实对齐；更准确的状态是：主干 HTTP 资源路径已大面积对齐，但 command payload、权限 header、Agent package metadata、审批 API、技能执行和部分 mock/test 契约仍需修正。

## 证据来源

- origin-agentops 代码：`D:\code2\znt\ploykit\modules\origin-agentops`
- origin-agentops 服务契约：`D:\code2\znt\ploykit\modules\origin-agentops\tests\service-contract.json`
- znt OpenAPI：`docs/openapi.clean-core.v1.json`
- znt command dispatch：`internal/server/server.go`
- znt auth：`internal/app/auth/auth.go`
- znt agent package metadata 校验：`internal/agentdef/package/compiler.go`
- znt tools.invoke 实现：`internal/tool/invoke/service.go`
- znt agent package release command：`internal/server/commands_agent_package.go`
- znt 实测背景：`docs/znt-real-path-full-coverage-test-execution-report-20260614.zh-CN.md`

## 契约覆盖现状

### service-contract.json 对 OpenAPI

机械比对：

- `tests/service-contract.json` endpoint 数量：43
- 当前 znt OpenAPI 缺失 method/path：0

这说明 contract 文件中列出的路径，如 `/readyz`、`/v1/commands`、`/v1/agents`、`/v1/tool-manifests`、`/v1/tool-providers`、`/v1/tool-groups`、`/v1/service-connections` 及子资源，在 OpenAPI 层面是存在的。

但这只是 path/method 覆盖，不等价于 payload、role、语义、状态流全部可跑通。

### 源码真实调用超出 service-contract.json

源码中实际还有这些未被 service-contract 完整表达的 znt 调用：

- `POST /v1/tool-providers/{provider_id}/health`
- `POST /v1/tool-providers/{provider_id}/sync`
- `GET /v1/runs` 及 run diagnostics/timeline/final-response/replay
- `GET /v1/audit`
- `GET /v1/traces/{trace_id}/diagnostics`
- agent 子资源：versions、collaborators、prompt-profile、tool-bindings、draft validate、version activate
- `POST /v1/approvals`
- `PATCH /v1/approvals/{approval_id}`
- `DELETE /v1/approvals/{approval_id}`
- commands：`agent.run`、`agent.package.draft.patch_strategies`、`agent.package.rollback`、`tools.invoke`、`skill.run`

其中 health/sync/run/audit/trace diagnostics 与当前 znt OpenAPI 基本对齐；approvals HTTP、`skill.run`、rollback payload、`tools.invoke` payload 不对齐。

## 传输与认证

origin-agentops 的 `module.ts` 定义了 `originApi` signed-http service requirement，并允许 `x-roles` header；但 `lib/service-client.ts` 默认实际只注入：

- `x-caller-id`
- `x-caller-type`
- `x-tenant-id`

znt `internal/app/auth/auth.go` 在没有 `X-Roles` 时默认角色是 `runtime_caller`。znt `internal/server/server.go` 的 `allowedCommand` 明确区分：

- `agent.run`、`tools.invoke` 等 runtime command：`runtime_caller/admin`
- `agent.package.*`、`tool.provider.*`、`tool.group.*`、`tool.manifest.*`、`approval.approve/reject` 等 optimizer command：`optimizer/admin`

影响：

- `agent.run` 在角色上可通过。
- `agent.package.draft.patch_strategies`、`agent.package.rollback`、tool command 类、approval command 类如果走 `/v1/commands`，默认会被 znt 拒绝。
- 当前大量 HTTP resource handlers 没有显式检查 `optimizer`，只依赖 auth + tenant；这不代表 product 语义上不需要角色，只是当前实现尚未在 HTTP handlers 加 role gate。

建议：

- origin-agentops 的 `invokeOriginApi` 支持 per-call role intent，例如 runtime 调用传 `runtime_caller`，发布/回滚/工具目录/服务连接/审批管理传 `optimizer`。
- 或由 PloyKit service connector 注入固定 `X-Roles: optimizer,runtime_caller`，但这会把所有调用提权，审计边界较粗。
- 长期建议 znt HTTP resource writes 也统一 role gate，避免 command 和 REST 权限语义不一致。

## Agent 管理与发布

### 已对齐部分

origin-agentops 已经开始使用 znt 新的 structured strategies：

- `lib/agent-strategies.ts` 生成 `strategies.prompt/model/context/tools/runtime/skills`
- `lib/agent-publish.ts` 使用 `agent.package.draft.patch_strategies`
- znt `contracts.AgentStrategies` 支持 prompt/model/context/tools/skills/collaboration/memory/knowledge/runtime/repair/output
- znt `RuntimeStrategy` 包含 `execution_mode`，所以 origin-agentops 默认 `interactive` 在 JSON schema 字段层面能解析；是否符合产品枚举语义需要后续运行策略验证

`agent.run` 主路径也基本对齐：`lib/run-origin.ts` 会发 command `agent.run`，包含 `target.agent_id/version` 和 `payload.input`。payload 里额外带 `objective`、`agent_id`，当前 znt 不依赖这些字段，风险较低。

agent 详情页读取的子资源路径也在 OpenAPI 中存在：

- `GET /v1/agents/{agent_id}/versions`
- `GET /v1/agents/{agent_id}/collaborators`
- `GET /v1/agents/{agent_id}/prompt-profile`
- `GET /v1/agents/{agent_id}/tool-bindings`
- `POST /v1/agents/{agent_id}/drafts/{draft_id}/validate`
- `POST /v1/agents/{agent_id}/versions/{version}/activate`

### 硬缺口：metadata 行为字段被 znt 拒绝

znt `internal/agentdef/package/compiler.go` 的 `ValidateSourceMetadata` 拒绝这些 metadata key：

- `identity_prompt`
- `system_prompt`
- `developer_prompt`
- `skills`
- `skill_definitions`
- `tool_bindings`
- `collaborators`
- `exports`
- `runtime_hooks`
- `strategies`
- `runtime`
- `max_steps`
- `max_tool_calls`
- 以及多种 runtime limit 字段

origin-agentops 仍有两条真实路径会触发：

- `api/agents.ts` 调用 `POST /v1/agents` 时，`metadata` 内包含 `system_prompt` 和 `strategies`。
- `lib/agent-publish.ts` 的 `agentDraftCreatePayload` 在 `metadata` 内包含 `system_prompt`、`developer_prompt`、`max_tool_calls`、`skills`。

znt 当前错误语义是：`metadata.<field>: agent behavior fields must use structured source fields or strategies`。

影响：

- 新建 agent 时如果 `POST /v1/agents` 同时创建 draft，会失败。
- 编辑/发布 agent 时 `POST /v1/agents/{agent_id}/drafts` 会失败。
- 即使随后有 `agent.package.draft.patch_strategies`，也到不了那一步。

建议：

- `originCreatePayload` 增加顶层 `strategies`，不要把 `strategies/system_prompt` 放进 metadata。
- draft create payload 顶层保留 `prompt`、`tool_bindings`、`strategies`、`skills`；metadata 只保留 `name/description/governance_summary/tags/source` 等非行为字段。
- 如果要保存 UI-only 原始值，使用不会被 znt 解释为行为字段的命名，例如 `ui_prompt_summary`，但不要保存 `system_prompt/developer_prompt/max_tool_calls/strategies/skills`。

## Runtime / Run / Trace

### 已对齐部分

origin-agentops live runtime gateway 使用：

- `GET /v1/runs`
- `POST /v1/commands` + `agent.run`
- `GET /v1/runs/{run_id}/diagnostics`
- `GET /v1/runs/{run_id}/timeline`
- `GET /v1/runs/{run_id}/final-response`
- `POST /v1/runs/{run_id}/replay`
- `GET /v1/audit`
- `GET /v1/traces/{trace_id}/diagnostics`

这些当前 znt OpenAPI 都有对应 method/path，其中 `/v1/runs/{run_id}/replay` 同时支持 GET/POST。origin-agentops 的 trace evidence 在 live gateway 中实际复用 diagnostics，不直接请求不存在的 `/v1/traces/{trace_id}/evidence`。

### 限制与漂移

- znt 有 `GET /v1/traces/{trace_id}/replay`，没有 `POST /v1/traces/{trace_id}/replay`；origin-agentops live gateway 未调用该 POST，但 mock fixtures 中出现了 trace replay/evidence 字符串。
- znt 没有 `/v1/traces/{trace_id}/evidence`；origin-agentops mock fixture 仍暴露 evidence 链接，live gateway 已降级为 diagnostics。
- trace list 当前通过 `GET /v1/audit` 组合出来，不是 znt 原生 trace list。它能服务审计视图，但会漏掉“有 trace 但无 audit 搜索结果”的情况。
- `markTraceAnomaly` live gateway 直接返回 `ORIGIN_TRACE_ANOMALY_UNSUPPORTED`，没有 znt 对应写接口。

建议：

- mock runtime fixture 不要再宣传 znt 不存在的 `POST /v1/traces/{trace_id}/replay` 和 `/evidence` 为真实 API。
- 如产品需要 trace evidence 独立视图，在 znt 增加正式 endpoint；否则 UI 文案应明确 evidence 来自 diagnostics。

## Tool / ToolProvider / ToolGroup

### 已对齐部分

origin-agentops 对工具目录的主要 HTTP 路径与 znt 当前 handler/OpenAPI 对齐：

- `/v1/tool-manifests`
- `/v1/tool-manifests/{tool_id}`
- `/v1/tool-providers`
- `/v1/tool-providers/{provider_id}`
- `/v1/tool-providers/{provider_id}/operations`
- `/v1/tool-providers/{provider_id}/operations/{operation_id}`
- `/v1/tool-providers/{provider_id}/operations/{operation_id}/test`
- `/v1/tool-providers/{provider_id}/operations/{operation_id}/publish`
- `/v1/tool-providers/{provider_id}/operations/from-resource`
- `/v1/tool-providers/{provider_id}/health`
- `/v1/tool-providers/{provider_id}/sync`
- `/v1/tool-groups`
- `/v1/tool-groups/{group_id}`

`api/tool-providers.ts` 创建 ToolHost 时先建 service connection，再建 provider，再 health/sync；这与 znt service connection + tool catalog 设计一致。

### 语义差异：ToolGroup 字段只部分落地

origin-agentops 的 ToolGroup 本地模型包含：

- `provider_id`
- `risk`
- `tool_ids`
- `agent_ids`
- `tags`
- `owner`
- `metadata`

znt 当前 `toolcatalog.ToolGroup` 只包含：

- `tenant_id`
- `group_id`
- `name`
- `description`
- `status`
- `version`

因此 `/v1/tool-groups` 路径是对齐的，但 origin-agentops 发送的 `tool_ids/agent_ids/risk/tags/metadata/provider_id` 不会成为 znt ToolGroup 权威字段。工具和 group 的真实关系在 znt 主要通过 `ToolManifest.group_id`、adapter operation `group_id` 等字段表达。

影响：

- UI 本地表能展示 group 包含哪些 tools/agents，但 znt 的 group resource 不会保存这些聚合字段。
- 如果 origin-agentops 期望通过 `PUT /v1/tool-groups/{group_id}` 更新成员列表，znt 不会按这个模型生效。

建议：

- ToolGroup 成员关系以 znt manifest/operation 为权威，从 znt manifest 列表反算 group tool_ids。
- `agent_ids` 如果是 UI 辅助关系，应留在本地或另建 znt 绑定 API，不要假设 ToolGroup resource 会保存。

### 硬缺口：tools.invoke smoke payload

origin-agentops：

```json
{
  "command": "tools.invoke",
  "payload": {
    "tool_id": "...",
    "args": { "ping": "origin-agentops" }
  }
}
```

znt 当前实现读取：

- `payload.tool_id` 或 `payload.tool_name`
- `payload.arguments`
- `envelope.target.agent_id/version`

并且 znt 会加载 target agent，校验 tool 是否在 agent exposed tools 中。

影响：

- 当前 smoke invoke 即使 tool 存在，也会把参数当空对象。
- 没有 target agent 时，大概率无法通过 agent load 或 exposed tool 校验。

建议：

- smoke invoke 改为 `{ payload: { tool_id, arguments }, target: { agent_id, version } }`。
- 如果产品确实需要“无 agent 直接 smoke tool”，znt 应新增显式 tool catalog test/invoke endpoint 或 command，不能复用当前 agent-scoped `tools.invoke`。

## Service Connections

### 已对齐部分

origin-agentops service connection 路径与 znt 对齐：

- `POST /v1/service-connections`
- `PATCH /v1/service-connections/{connection_id}`
- `DELETE /v1/service-connections/{connection_id}`
- `POST /v1/service-connections/{connection_id}/test`
- `GET /v1/service-connections/{connection_id}/resources`
- `GET /v1/service-connections/{connection_id}/health-events`
- `GET /v1/service-connections/{connection_id}/usage`
- `GET /v1/service-connections/{connection_id}/impact`
- `GET/POST /v1/service-connections/{connection_id}/secret-rotations`

payload 字段也大体对齐：`connection_id/name/connection_type/environment/status/description/base_url/auth_type/auth_ref/network_scope/timeout_ms/retry_max/health_check_enabled/metadata/version`。

### 风险

- origin-agentops 本地 `service_connections` 表会在 live znt 成功后写本地状态；如果 znt 后续 health/usage/impact 改变，本地表可能漂移。
- 本地 UI 使用中文/展示型 connection type，再通过 `originConnectionType` 映射到 znt backend type；这条映射是合理的，但新增类型时必须双边同步。
- `availability_pct` 在 origin-agentops 本地表像是千分制/展示字段，znt 真实 usage/health 不一定使用相同语义。

建议：

- 对 live 模式，列表页尽量以 znt `GET /v1/service-connections` 为权威源，本地表只存 UI 草稿和最近交互缓存。
- 增加后台 reconciliation 或“从 znt 重新同步”入口。

## Approvals

这是当前不对齐最明显的区域之一。

origin-agentops 调用：

- `POST /v1/approvals`
- `PATCH /v1/approvals/{approval_id}`
- `DELETE /v1/approvals/{approval_id}`

当前 znt：

- 没有通用 `/v1/approvals` HTTP 路由。
- 有 command enum/dispatch：`approval.approve`、`approval.reject`。
- 有 `GET /v1/runtime-hook-approvals`，只用于 runtime hook approval 列表。
- tool invoke/release/runtime hook 内部会通过 approval service 产生 approval request。

影响：

- origin-agentops 创建、更新、删除 approval 的 live 调用会 404 或不被路由。
- origin-agentops 当前 `tests/smoke.test.ts` mock 里直接返回 `/v1/approvals` 成功，会掩盖真实 znt 缺口。

建议：

- 若 znt 产品要支持通用 approval console：新增 `GET/POST /v1/approvals`、`GET/PATCH/DELETE /v1/approvals/{approval_id}`，并明确状态机与 role。
- 若不新增 HTTP API：origin-agentops 改为消费 znt 已有 approval request 来源，并用 `/v1/commands` 的 `approval.approve`/`approval.reject` 解决 approval；删除“手工 POST 创建 approval”的假路径。

## Rollback

origin-agentops rollback command：

```json
{
  "command": "agent.package.rollback",
  "payload": {
    "agent_id": "...",
    "target_version": "...",
    "current_version": "..."
  }
}
```

znt rollback command 当前要求：

- `payload.package_version_id`
- 可选/按 policy 需要 `payload.reason`

影响：

- 当前 rollback live 调用会直接失败：`package release command requires package_version_id`。
- origin-agentops 的 release history 是本地表快照，不一定能反查 znt package_version_id。

建议：

- origin-agentops 在发布后保存 znt 返回的 `package_version_id` 到本地 release history/governance summary。
- rollback UI 选择的目标应映射到“要 rollback 的当前 package version id”，并提供 reason。
- 如果产品语义是“把 agent 默认版本切回某 stable version”，znt 需要另一个明确 command/API；不要把 release rollback 与 default activation 混用。

## Skills

origin-agentops 有 agent skills HTTP 子资源：

- `GET/POST /v1/agents/{agent_id}/skills`
- `PUT/DELETE /v1/agents/{agent_id}/skills/{skill_id}`

这些路径已在 service contract 与 znt OpenAPI 中覆盖。

需要注意：znt 的 skill 子资源是 agent package 内的结构化 `SkillDefinition`/`SkillDefinitionProjection`，包含 `card`、`instruction`、`resources`、`recommended_tools`、`allowed_tools`、`output_schema` 等；origin-agentops 的本地 `skills` 表是扁平 UI 模型。`lib/skill-openapi.ts` 已做了转换，这是正确方向，但仍有两个真实限制：

- skill 归属需要明确 owner agent/draft/version；没有 owner agent 的“全局 skill”不是当前 znt 的主模型。
- skill metadata 不能绕回 agent package metadata；发布时仍要走结构化 `skills`/`skill_definitions`，不要放进 agent source metadata。

但 `actions/run-skill.ts` 调用：

```json
{
  "command": "skill.run",
  "payload": {
    "skill_id": "...",
    "sample": {}
  }
}
```

当前 znt `dispatchCommand` 没有 `skill.run` case，`allowedCommand` default 会允许未知 command 进入 dispatch，但 dispatch default 会返回 `unsupported command`。

建议：

- 如果 skill 是 agent package 的静态能力，origin-agentops 不应声称可直接 run skill，改为 agent run 或本地 mock。
- 如果需要 skill sandbox/test，znt 新增 `skill.run` command 或 `POST /v1/agents/{agent_id}/skills/{skill_id}/run`，并定义 payload、target、role、审计。

## Local Tables 与 znt 权威状态

origin-agentops 不是纯 znt API client，它有大量 PloyKit 本地表：

- agents
- tool_providers
- tool_manifests
- tool_groups
- service_connections
- skills
- runs
- approvals
- audit_events
- usage_buckets

当前许多 API 会“先/后调用 znt，再写本地表”。这能让 UI 响应快，也能支持 mock/demo，但 live 模式有漂移风险：

- znt 创建成功、本地写失败：UI 看不到真实 znt 资源。
- znt 后台状态变化、本地表不更新：health、usage、release、approval 状态过期。
- 本地 mock 流程成功，但真实 znt 不支持相同 endpoint/command：测试产生假阳性。

建议：

- 明确每张表的权威源：`source_of_truth = znt | local | cache`。
- live 模式下关键列表优先从 znt 拉取，本地只缓存展示补充字段。
- 每个写操作保存 znt 返回的 ID：`draft_id`、`package_version_id`、`run_id`、`trace_id`、`connection_id`、`provider_id`。
- smoke tests 增加 znt OpenAPI/command schema 校验，不允许 mock 自创 endpoint。

## Mock/Test 漂移

origin-agentops 的 `tests/smoke.test.ts` mock 明确支持：

- `/v1/approvals`
- `/v1/approvals/{id}`
- `/v1/traces` POST/PATCH/DELETE
- `skill.run`
- `tools.invoke`
- `agent.package.rollback`

但当前 znt 不支持其中一部分，或 payload 不一致。尤其：

- `/v1/approvals` 是 mock-only，不是当前 znt API。
- `/v1/traces/{trace_id}/evidence` 是 mock fixture/link，不是当前 znt API。
- `skill.run` 是 mock 成功路径，不是当前 znt command。
- `tools.invoke` mock 未校验 znt 所需的 `arguments` 和 `target`。
- rollback mock 未校验 `package_version_id`。

建议：

- smoke mock 从 znt OpenAPI + command allowlist 生成或校验。
- 对每个 `invokeOriginApi` 调用加 contract test：method/path 存在、payload 禁止字段、required fields、command target。
- 将 mock-only 能力用 `mockRuntime` 命名，不要标成 `originApi` 成功路径。

## 分域对齐矩阵

| 域 | 当前状态 | 真实结论 |
|---|---|---|
| service-contract path/method | 已覆盖 | 43/43 OpenAPI 存在 |
| transport auth | 部分对齐 | tenant/caller 对齐；roles 默认缺失 |
| `agent.run` | 基本对齐 | target/input 对齐；payload 额外字段低风险 |
| agent create/draft/publish | 未完全对齐 | metadata 行为字段会被 znt 拒绝 |
| strategies | 部分对齐 | prompt/model/context/tools/runtime/skills 对齐；未覆盖 memory/knowledge/repair/output 等高级策略 |
| run diagnostics/replay/final-response | 基本对齐 | live gateway 使用的路径存在 |
| trace audit | 部分对齐 | diagnostics/audit 对齐；evidence/anomaly/trace list 不完整 |
| tools catalog | 基本对齐 | REST 路径和主要 payload 对齐 |
| tool groups | 部分对齐 | path 对齐；znt group 模型不保存 `tool_ids/agent_ids/risk/tags/metadata` |
| `tools.invoke` smoke | 未对齐 | `args` 应为 `arguments`，且缺 target |
| service connections | 基本对齐 | REST 路径和主要 payload 对齐；本地表有漂移风险 |
| approvals | 未对齐 | origin-agentops 调用通用 HTTP approvals；znt 当前没有 |
| rollback | 未对齐 | payload 需要 `package_version_id`，不是 `target_version/current_version` |
| skills CRUD | 部分对齐 | agent skills 子资源存在；模型是 agent-scoped SkillDefinition，不是全局扁平 skill |
| `skill.run` | 未对齐 | znt dispatch 没有该 command |
| mock tests | 有漂移 | mock 支持了 znt 不存在或 payload 不同的能力 |

## 建议修复顺序

1. 修 origin-agentops payload：移除 agent metadata 行为字段，`tools.invoke` 改 `arguments + target`，rollback 改 `package_version_id + reason`。
2. 修 role header：在 `invokeOriginApi` 增加 role intent，所有 optimizer command 明确传 `x-roles: optimizer` 或 `optimizer,runtime_caller`。
3. 决策 approvals：要么 znt 增加通用 approvals HTTP API，要么 origin-agentops 改用 `approval.approve/reject` command 并删除 mock-only 创建接口。
4. 决策 skill.run：要么 znt 实现 skill run command/API，要么 origin-agentops UI 去掉 live skill run。
5. 清理 mock/test：mock 必须对齐 znt OpenAPI 和 command schema，不能继续把不存在的 `/v1/approvals`、trace evidence、`skill.run` 当真实成功。
6. 建立 live reconciliation：origin-agentops 本地表与 znt 权威数据定期/按需同步，尤其 release history、package_version_id、approval、run 状态。

## 不应声称已完成的事项

- 不能声称 origin-agentops 已与新 znt 全面端到端跑通；本次没有启动 PloyKit 模块和真实 znt 服务做联调。
- 不能声称 approvals、skill.run、rollback、tools.invoke smoke 已真实可用；代码层面已发现不对齐。
- 不能把 `tests/smoke.test.ts` mock 成功等同于 znt 成功；mock 当前覆盖了 znt 不存在的接口。
- 不能把 OpenAPI path 存在等同于 payload/role/状态机对齐；当前几个关键失败点都不是 path 缺失，而是 payload/role/语义不一致。
