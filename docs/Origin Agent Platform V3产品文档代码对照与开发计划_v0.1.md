# Origin Agent Platform V3 产品文档代码对照与开发计划 v0.1

日期：2026-06-02

对照来源：`docs/Origin Agent Platform Product Doc V3.docx`

对照范围：当前仓库 CleanCore 后端代码、迁移、OpenAPI、E2E 脚本与既有设计文档。本文只判断“代码是否已经能支撑 V3 产品文档要求”，不把 V3 明确放到后续路线图的高级能力强行列为 MVP 缺口。

## 1. 总结

当前代码已经不是早期原型。CleanCore 的运行内核、Task / Run 生命周期、PromptBundle、Decision 校验、ToolRuntime、Policy / Approval、Trace / Audit / Replay、AgentPackage 发布、Eval、Handoff、动态工具注册、协作能力包基础服务都已经有较完整骨架。

但如果按 V3 产品文档验收，仍然不能说“已满足 V3 MVP”。主要差距不在模型调用或单次运行链路，而在以下六类：

1. 资源化产品 API 仍不完整，但 Agent 资产主干已落地：`/v1/agents` 已支持创建、列表、详情、PATCH、DELETE 和 OpenAPI schema；Agent package drafts 已支持创建、列表、详情、validate、review、publish；Agent package version 已支持列表、详情和 stable version activate；RuntimeHooks、PromptProfile、Skill、ToolBinding、Collaborator、ExportedTool 已有 REST wrapper，且子资源 GET 已支持 `draft_id` 读取未发布 draft 视图；PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 已有 draft/release 投影表，管理 GET 路径已优先投影表直读；PromptProfile、Skill、ToolBinding、Collaborator 与 ExportedTool 已具备 standalone CRUD 和 runtime loader overlay；PromptProfile / ToolBinding / Skill / Collaborator / ExportedTool 已补子资源 versions/activate 入口，并已补 PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 治理查询视图。
2. Tool Catalog 已具备持久化事实源和资源 API：`ToolProvider` / `ToolGroup` / `ToolManifest` / `ToolManifestVersion` 已有表、Store、restore、执行前 availability check、`/v1/tool-providers`、`/v1/tool-groups`、`/v1/tool-manifests`；基础 provider health 已接入 `/v1/tool-providers/{provider_id}/health`、持久化健康字段和 unhealthy 执行门禁；ToolHost provider 已支持 `auth_ref` 引用头、`timeout_ms` 和 `retry_max` 并覆盖 catalog / health / invoke；`/v1/tool-provider-governance` 已提供当前 provider health/status、manifest 分布、blocked reasons 和可选 trace evidence 的只读矩阵；仍缺 mTLS、真实凭据解析、健康调度、历史趋势和配额等更完整指标矩阵。
3. Hook 已从内核接口推进到 MVP 管理服务：已有 Hook Provider、HookManifest、Binding、Preview、事件持久化、Trace/Audit、`/v1/runtime-hook-providers`、`/v1/runtime-hook-manifests`、`/v1/runtime-hook-events`、`/v1/runtime-hook-governance`、`/v1/runtime-hook-approvals` 和 `/v1/agents/{agent_id}/runtime-hooks`；HookManifest 已具备版本快照表、`/versions` 查询和 enabled version activate；HookManifest / Binding 可通过 `requires_approval` 或 `approval_policy` 要求绑定审批，REST 与 command upsert 会生成 ApprovalRequest，审批后带 `approval_id` 重试才落库；Runtime Hook approval management 已有只读列表视图，可按 status/trace/resource/agent/hook/phase 过滤；基础 provider health 已接入 `/v1/runtime-hook-providers/{provider_id}/health` 和 unhealthy invoke guard，且可按 `trace_id` 写入 health evidence 并进入 governance metrics；Static HookHost provider catalog 已接入 `/v1/runtime-hook-providers/{provider_id}/catalog` 和 `/catalog/sync`，会读取并校验远端 `GET /runtime-hooks/catalog`，可同步为独立 HookManifest，但不自动安装 binding 或推断租户授权；Hook config/patch 已有敏感字段和明文 secret lint；runtime hook governance 已提供 provider_matrix；仍缺更深历史趋势/健康矩阵和更完整的指标/配额。
4. Agent 协作安全边界已完成一轮运行时收紧：Collaborator / ExportedTool 已建模，`origin.agent.delegate` 已强制校验当前 step retrieved collaborators，并接入 depth / cycle / target release runnable guard；Agent Asset disabled/deleted 状态已接入 run、handoff、agent_tool 关键入口。
5. 企业数据本地化还是架构预留：ExecutionProfile、CredentialScope、DataBoundary 已从元数据推进到 ToolRuntime 基础执行门禁，声明 credential scope 会先解析 credential handle，缺 resolver、跨 tenant credential、越界 data scope/tenant 会被拒绝；但密文引用托管、脱敏策略、KMS/HSM/mTLS、本地模型执行协议等深水区仍未闭环。
6. V3 数据模型仍有缺口：`agent_assets`、`tool_providers`、`tool_manifests`、`tool_groups`、`runtime_hook_*`、`runtime_hook_manifest_versions`、`agent_prompt_profiles`、`agent_skill_definitions`、`agent_tool_bindings`、`agent_collaborators`、`agent_exported_tools` 已补入迁移；API draft/release 基础读视图已补，PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 的管理 GET 已优先投影表直读，PromptProfile 已复用 `agent_prompt_profiles` 的 `source_kind=profile` 独立行作为 active standalone profile 并接入 runtime loader，Skill 已复用 `agent_skill_definitions` 的 `source_kind=skill` 独立行作为 active standalone skill 并接入 runtime loader，ToolBinding 已复用 `agent_tool_bindings` 的 `source_kind=tool_binding` 独立行作为 active standalone binding 并接入 runtime loader，Collaborator 已复用 `agent_collaborators` 的 `source_kind=collaborator` 独立行作为 active standalone collaborator 并接入 runtime loader，ExportedTool 已复用 `agent_exported_tools` 的 `source_kind=exported_tool` 独立行作为 active standalone exported tool 并接入 runtime loader，Agent 主要子资源已具备 stable package version 边界下的 versions/activate 语义，HookManifest 已具备独立版本快照和 activate 语义；PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 治理查询 API 已补，可返回 standalone active、draft 与 release 来源的只读治理视图。

结论：

```text
CleanCore 内核：基本可用
V3 MVP 产品面：部分完成
V3 企业生产闭环：仍需一轮产品化、持久化、安全边界和治理补齐
```

## 2. V3 MVP 要求拆解

V3 文档的 MVP 必须进入项可以归纳为 15 个能力组：

| 编号 | V3 MVP 能力 | 当前判断 |
| --- | --- | --- |
| M01 | Agent 基础 CRUD | 基本完成（AgentAsset + `/v1/agents` 资源 API） |
| M02 | PromptProfile API | 基本完成（standalone CRUD、runtime overlay、versions/activate、治理视图已有） |
| M03 | Skill API | 基本完成（standalone CRUD、runtime overlay、V3 字段、versions/activate、治理视图已有） |
| M04 | ToolManifest / ToolGroup / ToolProvider | 基本完成（command + REST 资源形态） |
| M05 | Static ToolHost / AgentPlugin Service 接入 | 部分完成 |
| M06 | HTTP Direct 低风险工具接入 | 基本完成 |
| M07 | ToolRuntime 可用性检查 | 基本完成 |
| M08 | ToolBinding 支持 tool group | 基本完成 |
| M09 | AgentCollaborator 基础模型 | 基本完成（模型、standalone CRUD、runtime overlay、versions/activate、治理视图已有） |
| M10 | `origin.agent.delegate` 安全整改 | 基本完成（retrieved/depth/cycle/target release/AgentAsset guard 已接入） |
| M11 | AgentExportedTool 同步为 ToolManifest | 基本完成（standalone CRUD、runtime overlay、ToolManifest sync、agent_tool 执行闭环、治理视图已有） |
| M12 | Data Hook 最小能力：`after_candidate_retrieval`、`before_model_call` | 基本完成 |
| M13 | Trace / Audit 覆盖工具、handoff、hook、prompt、decision | 部分完成（hook 事件已补） |
| M14 | 企业数据本地化：控制面只见元数据，明文在 AgentPlugin Service | 未闭环 |
| M15 | Release / Canary / Rollback 基础能力 | 基本完成 |

## 3. 当前已具备的代码基础

| 能力 | 代码证据 | 说明 |
| --- | --- | --- |
| Task / Run 生命周期 | `internal/runtime/kernel/coordinator.go`、`internal/runtime/run/repository.go`、`internal/task/runtime/service.go` | `HandleEnvelope`、`StartTaskRun`、`loop`、`step` 已串起任务、运行、模型、工具、终态。 |
| WorkView / PromptBundle | `internal/context/workview`、`internal/context/promptbundle` | 已有上下文投影、工具卡、协作者卡、prompt hash。 |
| Decision 解析和校验 | `internal/decision/parser`、`internal/decision/validator` | 模型输出不能直接执行，先 Parse / Normalize。 |
| ToolRuntime 治理链 | `internal/tool/runtime/runtime.go` | 输入 schema、ToolPolicy、Approval、ExecutionDomain、输出 schema、Trace 已在链路中。 |
| 动态工具目录事实源 | `internal/tool/catalog/catalog.go`、`internal/storage/postgres/postgres.go`、`migrations/001_clean_core_base.sql`、`internal/server/server.go` | 支持 `tool.provider.upsert`、`tool.group.upsert`、`tool.provider.sync`、`tool.manifest.upsert/list`，并持久化 provider/group/manifest/version/runtime cache。 |
| AgentPackage 发布流 | `internal/agentdef/package/service.go`、`internal/agentdef/package/compiler.go` | Draft、Proposal、Validate、Publish、Canary、Stable、Rollback 已有服务和命令。 |
| Policy 发布流 | `internal/policy/engine`、`internal/storage/postgres/postgres.go`、`internal/server/server.go` | Policy Draft / Version / Canary / Stable / Rollback 已有基础实现。 |
| Eval Suite | `internal/eval`、`internal/server/server.go`、`migrations/001_clean_core_base.sql` | Suite、Case、Result、Gate 已有基础能力，并影响 stable。 |
| Handoff | `internal/tool/handoff/executor.go`、`internal/task/handoff/service.go` | `origin.agent.delegate` 走 ToolRuntime 后创建 Handoff、Child Task、Child Run。 |
| Agent Asset 资源 API | `internal/server/server.go`、`internal/agentdef/package/service.go`、`internal/storage/postgres/postgres.go`、`docs/openapi.clean-core.v1.json` | `/v1/agents` 已支持 Create/List/Get/Patch/Delete；`/v1/agents/{agent_id}/drafts` 支持 draft lifecycle；`/v1/agents/{agent_id}/versions` 支持版本查询和 stable activate；`agent_assets` 持久化状态、owner、active/default version；禁用/删除状态进入 run / handoff / agent_tool 门禁。 |
| Agent ExportedTool | `internal/agentdef/package/service.go`、`internal/agentdef/loader/loader.go`、`internal/tool/agenttool/handler.go` | ExportedTool 可同步为 `executor.type = agent_tool` 的 ToolManifest，standalone active 行可覆盖 runtime loader。 |
| Runtime Hook MVP 服务 | `internal/runtime/hook/hooks.go`、`internal/runtime/hook/service.go`、`internal/runtime/kernel/coordinator.go`、`internal/storage/postgres/postgres.go` | 有 Hook Provider / Manifest / ManifestVersion / Binding / Preview / Event Store，已接入候选排序、上下文、模型调用前和 before_memory_write。 |
| 群聊上下文 | `internal/runtime/kernel/conversation_context.go`、`internal/context/conversation` | Addressee Judge、Sufficiency Judge、ContextRetriever 已接入。 |
| 协作能力包基础工具 | `internal/tool/originext/executors.go` | identity、permission、skill update、knowledge、cross-group、memory share、progress、agent factory、tone 等受控工具已注册。 |
| Trace / Audit / Replay | `internal/contracts/governance.go`、`internal/governance/*` | 常量、记录器、回放、指标和审计查询已有基础能力。 |
| Postgres 基础持久化 | `migrations/001_clean_core_base.sql`、`internal/storage/postgres/postgres.go` | 覆盖 package、policy、eval、task、run、handoff、tool call/result、artifact、memory、trace、audit、协作能力包表，并新增 Agent 子资源投影表。 |

## 4. 关键缺口矩阵

| 编号 | V3 要求 | 当前代码现状 | 缺口 | 优先级 |
| --- | --- | --- | --- | --- |
| G01 | `/v1/agents` CRUD | 已有 AgentAsset 状态层、`agent_assets` 表、`POST/GET/PATCH/DELETE /v1/agents`、OpenAPI schema；`/v1/agents/{agent_id}/drafts` 已支持 create/list/get/validate/review/publish；`/v1/agents/{agent_id}/versions`、`/versions/{version}` 和 `/versions/{version}/activate` 已支持 package version 查询与 stable version 激活；发布稳定版本时会回写 active/default version；Agent 子资源治理查询视图已补 | 更细的治理指标仍可演进 | Done |
| G02 | PromptProfile 独立 API | `agent.package.draft.patch_prompt`、`patch_system_prompt`、`patch_developer_prompt`、`prompt.preview` 以及 `/v1/agents/{agent_id}/prompt-profile` / `/preview` 已可操作 draft；GET 支持 `draft_id` 读取未发布 draft 视图，且在 Postgres Store 下优先从 `agent_prompt_profiles` 投影表直读；新增 `/prompt-profile/versions` 可列出 PromptProfile 版本视图，`/prompt-profile/activate` 复用 stable package version 边界，非 stable 版本会拒绝激活并在成功后回写 Agent active/default version；`POST/PUT/PATCH/DELETE /prompt-profile` 不带 `draft_id` 时已支持 standalone PromptProfile CRUD，独立 `source_kind=profile` 行会覆盖 active runtime loader，prompt preview / run 装载路径已优先叠加 active PromptProfile；`/prompt-profile/governance` 已输出 standalone active、draft、release 来源治理视图 | 基础治理视图已补；更细指标仍可演进 | Done |
| G03 | Skill API | `agent.package.skill.add/update/remove` 和 `/v1/agents/{agent_id}/skills` / `/skills/{skill_id}` 已支持 package draft wrapper；列表/单体 GET 支持 `draft_id` 读取未发布 draft 视图，且在 Postgres Store 下优先从 `agent_skill_definitions` 投影表直读；不带 `draft_id` 的 `POST/PUT/PATCH/DELETE /skills` 已支持 standalone Skill CRUD，独立 `source_kind=skill` 行会覆盖 active runtime loader 并同步 `SkillDefinitions` / `Skills` refs；`/skills/{skill_id}/versions` 可列出包含目标 skill 的版本视图，`/skills/{skill_id}/activate` 会先确认目标 stable package version 内存在该 skill，再回写 Agent active/default version；recommended_tools、allowed_tools、memory/handoff 建议、completion_criteria、output_schema 已进入 contract/compile，recommended/allowed tools 已影响候选工具召回；`/skills/governance` 与 `/skills/{skill_id}/governance` 已输出来源治理视图 | 基础治理视图已补；更细指标仍可演进 | Done |
| G04 | ToolBinding 支持 tool group | `AgentToolsConfig` / `ToolPolicy` 已支持 allowed / denied group ids，`/v1/agents/{agent_id}/tool-bindings` 已可操作 draft，GET 支持 `draft_id` 读取未发布 draft 视图，且在 Postgres Store 下优先从 `agent_tool_bindings` 投影表直读；不带 `draft_id` 的 `POST/PUT/PATCH/DELETE /tool-bindings` 已支持 standalone ToolBinding CRUD，独立 `source_kind=tool_binding` 行会覆盖 active runtime loader；`/tool-bindings/versions` 可列出 ToolBinding 版本视图，`/tool-bindings/activate` 复用 stable package version 边界，非 stable 版本会拒绝激活并在成功后回写 Agent active/default version；release status 变更会同步投影状态；CandidateProvider 已对 allowed group 和 objective 命中的 group_id 做排序 boost；`/tool-bindings/governance` 已输出来源治理视图和授权计数 | 基础治理视图已补；更细指标仍可演进 | Done |
| G05 | ToolManifest 是长期事实源 | 已有 `tool_providers`、`tool_groups`、`tool_manifests`、`tool_manifest_versions`、`tool_runtime_registry_cache`、Store、restore、REST 资源 API、provider health 字段、`/health` 探测和 `/v1/tool-provider-governance` 当前治理矩阵 | health 调度、历史指标、配额和更深可观测视图仍可增强 | Done |
| G06 | ToolGroup | 已有 ToolGroup 模型、command API、REST API、启停、候选过滤、group-aware ranking 和执行前 availability check | 可视化管理和更复杂 group 策略仍可增强 | Done |
| G07 | AgentPlugin Service ToolHost 协议 | 支持 `GET /tools/catalog`、`POST /tools/invoke` 和 provider `/healthz` 探测，invoke payload 已带 `trace_id`、`run_id`、`task_id`、`idempotency_key`；ToolProvider 已支持 `auth_ref` 引用头、`timeout_ms` 和 `retry_max`，并持久化 / OpenAPI 对齐；ToolHost invoke 已写入 `tool_provider.invoked/completed/failed` trace，provider health 检查可按 `trace_id` 写入 `tool_provider.health_checked`，governance metrics 已统计 provider latency / failure / health failure；`/v1/tool-provider-governance` 可按 provider/tool/status/health/executor 过滤当前矩阵并附加 trace-scoped evidence | 缺 mTLS、真实 secret resolver、健康调度、重试历史趋势和配额治理 | P0 |
| G08 | HTTP Direct 只做低风险开发期路径 | 已有 `ExecutorTypeHTTPDirect`；HTTP execution domain 已要求 `network_policy.allow_network=true` 并校验 `allowed_hosts`；`ToolManifest` 注册时已强制 `http_direct` 只能用于 `risk_level=low` 工具；新增 `disable_http_direct` / `CLEAN_CORE_DISABLE_HTTP_DIRECT` release switch，可拒绝新注册并阻止 restore 旧 direct 工具 | 生产默认策略和更细租户级 allowlist 仍可演进 | Done |
| G09 | ToolRuntime 执行前完整门禁 | 已有 input/output schema、policy、approval、execution domain、disabled tool switch、provider/group/tool availability check，并拒绝 unhealthy provider 下的 stale tools；HTTP execution domain 已强制 `allow_network=true` 并校验 `allowed_hosts`；`credential_scope` 已接入 resolver 预检和 resolved credential handle 传递，缺 resolver、跨 tenant credential、越界 data scope/tenant 会拒绝并审计 | 更细的 secret backend/KMS、凭据轮换、细粒度 scope 目录和治理视图仍待补 | Done |
| G10 | AgentCollaborator 产品模型 | `AgentCollaboratorRef` 已支持 handoff mode、max_context_tokens、requires_approval，`/v1/agents/{agent_id}/collaborators` 和 `/{collaborator_agent_id}` 已作为 draft REST wrapper 接入，列表/单体 GET 支持 `draft_id` 读取未发布 draft 视图，且在 Postgres Store 下优先从 `agent_collaborators` 投影表直读；集合 `POST` 和单体 `POST/PUT/PATCH/DELETE` 不带 `draft_id` 时已支持 standalone Collaborator upsert/delete，独立 `source_kind=collaborator` 行会覆盖 active runtime loader；`/collaborators/{collaborator_agent_id}/versions` 可列出包含目标 collaborator 的版本视图，`/activate` 会先确认目标 stable package version 内存在该 collaborator，再回写 Agent active/default version；`/collaborators/governance` 与 `/collaborators/{collaborator_agent_id}/governance` 已输出来源治理视图和 approval 信号 | 基础治理视图已补；更细指标仍可演进 | Done |
| G11 | `origin.agent.delegate` 必须来自当前 step retrieved collaborators | Agent run 工具调用会注入当前 step retrieved collaborators，executor 强制要求该内部标记并校验目标在本 step 已召回；command 兼容入口也会先按当前目标/目标文本重新召回协作者；已增加 depth/cycle guard、collaborator status guard、target release canary/stable guard、AgentAsset disabled/deleted guard，并让 executor 级 Handoff 拒绝写入 `tool.policy_denied` Audit | Handoff 管理视图和更细指标仍可增强；核心安全边界已闭环 | Done |
| G12 | AgentExportedTool 固定能力调用 | ExportedTool 可同步成 `agent_tool` manifest；`/v1/agents/{agent_id}/exported-tools` 和 `/{tool_id}` 已支持 package draft wrapper，列表/单体 GET 支持 `draft_id` 读取未发布 draft 视图，且在 Postgres Store 下优先从 `agent_exported_tools` 投影表直读；集合 `POST` 和单体 `POST/PUT/PATCH/DELETE` 不带 `draft_id` 时已支持 standalone ExportedTool upsert/delete，独立 `source_kind=exported_tool` 行会覆盖 active runtime loader，并同步启用/禁用对应 `agent_tool` ToolManifest；`/exported-tools/{tool_id}/versions` 可列出包含目标 exported tool 的版本视图，`/activate` 会先确认目标 stable package version 内存在该 exported tool，再回写 Agent active/default version；`/exported-tools/governance` 与 `/exported-tools/{tool_id}/governance` 已输出来源治理视图、risk/visibility/operation 信号；`AgentToolHandler` 会校验 provider agent / exported tool 状态，注入 provider agent run 回调，执行时创建 provider agent `agent.run`，并把 run/task/status/reply/artifacts 写回 ToolResult 输出；high/critical exported tool 在 `tools.invoke` 入口会生成 ApprovalRequest，审批后带 `approval_id` 重试可执行 provider agent run；已覆盖 `agent_tool.completed/failed` Trace | 基础管理/治理视图已补；更细的异步 child run、固定 operation handler 和结果投影协议仍可演进 | Done |
| G13 | Data Hook MVP | RuntimeHookService 已支持 provider、HookManifest、HookManifestVersion、binding、preview、持久化事件、before_memory_write，并暴露 runtime hook provider / manifest / manifest versions / agent binding REST API；provider health、unhealthy invoke guard、disabled HookManifest invoke guard 和 static binding 必须引用 enabled HookManifest 的基础授权门禁已接入；Static HookHost catalog 可通过 `/v1/runtime-hook-providers/{provider_id}/catalog` 读取校验，并通过 `/catalog/sync` 同步到 `runtime_hook_manifests` 与 `runtime_hook_manifest_versions`，且不会自动写入 agent binding；HookManifest activate 只允许 enabled version，并把版本快照复制回当前 manifest；HookManifest / Binding 的 `requires_approval` 与 `approval_policy` 已接入 REST 与 command 绑定 upsert，审批前不落库、审批后 consume `approval_id` 才落库；`/v1/runtime-hook-approvals` 已提供审批只读管理视图；`/v1/runtime-hook-governance` 已可按过滤条件输出 summary、分桶、provider_matrix 和时间窗口 trend | 更深 provider 健康/配额矩阵仍待补 | P0 |
| G14 | Hook 不能绕过治理 | 已有 Patch 校验、候选工具权限校验、失败策略、trace/audit applied/failed，governance metrics 已统计 hook invoke/applied/denied/failed/timeout、hook latency 和 runtime hook provider health checks/failures/latency；Hook provider/binding config 与 Hook patch 已增加敏感字段/明文 secret lint；Hook patch 已有 context/memory/planner 数量和文本大小配额；Static HookHost timeout 会写 `runtime_hook.timeout` 并带 provider/latency 证据；runtime hook governance summary 已支持 provider/hook/phase/status 分桶、provider_matrix 和 from/to/interval 趋势窗口 | 更细的 provider 健康/配额矩阵仍待补 | Done |
| G15 | RuntimeHooks 属于 Agent Asset Profile | AgentDefinition / package compiler / package update command 已支持 RuntimeHooks，`/v1/agents/{agent_id}/runtime-hooks` 已可管理绑定和 preview | 独立管理 UI 待补 | P0 |
| G16 | 企业数据本地化与加密执行 | `ExecutionProfile` 有 CredentialScope / DataBoundary 元数据；ToolRuntime 已做基础 credential handle 解析门禁、tenant/data scope 边界校验，并记录 credential 使用 Trace/Audit | 缺密文引用托管模型、脱敏摘要、KMS/HSM/mTLS、本地解密执行、控制面明文禁止策略和更完整企业级测试 | P1 |
| G17 | Trace / Audit 覆盖 hook | 已有 `runtime_hook.invoked` / `runtime_hook.applied` / `runtime_hook.failed` / `runtime_hook.denied` / `runtime_hook.timeout` / `runtime_hook.provider_health_checked` 常量、runtime_hook_events、metrics 计数/latency 汇总；`/v1/runtime-hook-events` 可按 trace/status/hook/provider/phase 查询事件；`/v1/runtime-hook-governance` 可按 trace/provider/hook/phase/status/from/to/interval 输出 summary、分桶、provider_matrix 和趋势；provider health REST 可按 `trace_id` 写入健康证据；`/v1/tool-provider-governance` 可把 `tool_provider.*` trace evidence 投影到 provider/tool 矩阵 | 更完整 provider health / quota 深层历史矩阵仍待补 | Done |
| G18 | Trace / Audit 默认不记录敏感明文 | PromptBundle 记录 hash；credential 使用只记 ref；Runtime Hook config/patch 已有 secret lint，防止 hook 把明文 secret 写入事件、上下文或记忆；Replay report 已递归扫描 trace payload，发现明显 secret key / 裸 secret 值会标记 `redaction_violations` | 仍缺全局 data classification、强制 ciphertext_ref 模型和 KMS/HSM 级策略 | P1 |
| G19 | ConversationContext 只在 group/thread/collaboration channel 默认启用 | `buildConversationContext` 默认只在显式 `collaboration` 上下文中启用；direct conversation 需通过 `conversation_direct_enabled` 显式开启；群聊 / thread / collaboration 仍默认走接话判断和历史召回；PromptBundle retrieved context 已统一带 `input_boundary=untrusted`，metrics 已统计 addressee / sufficiency / retrieval 事件 | 更细的协作系统适配器和治理看板仍可演进 | Done |
| G20 | KnowledgeBase Pack | 已有 knowledge service、工具、表、permission/cross group | 缺真实 indexer、embedding/hybrid、文件/url/api/db connector、异步 ingestion 状态 | P2 |
| G21 | CrossGroupSearch 默认禁止并审计 | 已有 permission + crossgroup service + audit/trace | 需要补产品 API、共享策略管理、脱敏策略、授权可视化 | P2 |
| G22 | Collaboration Capability Pack | 工具和基础表已具备 | 缺外部协作系统适配器矩阵、群成员同步 API、频道/线程管理、通知回写配置 | P2 |
| G23 | API 总览中的资源 API | OpenAPI 已包含 `/v1/agents`、`/v1/agents/{agent_id}`、`/v1/agents/{agent_id}/drafts`、`/drafts/{draft_id}`、`/drafts/{draft_id}/validate`、`/review`、`/publish`、`/versions`、`/versions/{version}`、`/versions/{version}/activate`、`/prompt-profile`、`/prompt-profile/versions`、`/prompt-profile/activate`、`/tool-bindings`、`/tool-bindings/versions`、`/tool-bindings/activate`、`/skills`、`/skills/{skill_id}`、`/skills/{skill_id}/versions`、`/skills/{skill_id}/activate`、`/runtime-hooks`、`/collaborators`、`/collaborators/{collaborator_agent_id}`、`/collaborators/{collaborator_agent_id}/versions`、`/collaborators/{collaborator_agent_id}/activate`、`/exported-tools`、`/exported-tools/{tool_id}`、`/exported-tools/{tool_id}/versions`、`/exported-tools/{tool_id}/activate`、Agent 子资源 governance 端点、`/v1/tool-providers`、`/v1/tool-provider-governance`、`/v1/tool-groups`、`/v1/tool-manifests`、`/v1/runtime-hook-providers`、`/v1/runtime-hook-providers/{provider_id}/catalog`、`/v1/runtime-hook-providers/{provider_id}/catalog/sync`、`/v1/runtime-hook-manifests`、`/v1/runtime-hook-manifests/{hook_id}/versions`、`/v1/runtime-hook-manifests/{hook_id}/versions/{version}`、`/v1/runtime-hook-manifests/{hook_id}/versions/{version}/activate`、`/v1/runtime-hook-events`、`/v1/runtime-hook-governance`、`/v1/tasks/start`、`/v1/commands` 和查询端点 | 更深的子资源独立生命周期仍可继续拆分 | Done |
| G24 | 数据模型总览 | migration 覆盖大量内核表，并已包含 `agent_assets`、tool catalog、runtime hook 表、`runtime_hook_manifest_versions` 和 Agent 子资源投影表；PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 的管理 GET 已可从投影表直读；PromptProfile standalone CRUD 已复用 `agent_prompt_profiles` 的 `source_kind=profile` 独立行并接入 runtime loader overlay；Skill standalone CRUD 已复用 `agent_skill_definitions` 的 `source_kind=skill` 独立行并接入 runtime loader overlay；ToolBinding standalone CRUD 已复用 `agent_tool_bindings` 的 `source_kind=tool_binding` 独立行并接入 runtime loader overlay；Collaborator standalone CRUD 已复用 `agent_collaborators` 的 `source_kind=collaborator` 独立行并接入 runtime loader overlay；ExportedTool standalone CRUD 已复用 `agent_exported_tools` 的 `source_kind=exported_tool` 独立行并接入 runtime loader overlay；Agent 子资源治理查询已复用 active standalone、draft 和 release 投影事实源；Tool provider governance 已复用当前 tool catalog 事实源和可选 trace evidence 输出只读矩阵；HookManifest / Binding 的审批信号与 `approval_policy` 已可随 JSONB 快照持久化；runtime_hook_events 已承载治理 summary、provider_matrix 和时间窗口 trend 的事实源；ApprovalRequest 已可作为 runtime hook approval management view 的事实源 | provider health / quota 深层历史、调度和配额矩阵待补 | P0 |
| G25 | 完整 RuntimeDriver | 文档明确 MVP 不承诺 | 当前没有完整 RuntimeDriver 是正确选择，不列入 MVP 缺口 | P3 |

## 5. 建议开发顺序

### P0：补齐 V3 MVP 主干

目标：让 V3 文档里“必须进入 MVP”的控制面能力能被 API、存储、治理证据完整支撑。

#### T1. Agent 资产资源化 API

状态：已完成主干。代码证据包括 `handleAgentCreate/List/Get/Patch/Delete`、`handleAgentDrafts`、`handleAgentVersions`、`AgentAsset` Service/Store、`agent_assets` migration、OpenAPI `AgentResource*` / `AgentPackageDraft*` / `AgentVersion*` schema，以及 server 回归 `TestAgentResourceAPIDisableBlocksRunHandoffAndAgentTool` / `TestAgentResourceAPIDisableBlocksSourceHandoffEntrypoints` / `TestAgentDraftLifecycleResourceAPIs` / `TestAgentVersionResourceAPIActivatesStableVersion`。

涉及文件：

- `internal/contracts/agent.go`
- `internal/agentdef/package`
- `internal/server/server.go`
- `internal/storage/postgres/postgres.go`
- `migrations/*.sql`
- `docs/openapi.clean-core.v1.json`

任务：

1. 增加资源化 API：`POST /v1/agents`、`GET /v1/agents`、`GET/PATCH/DELETE /v1/agents/{agent_id}`、`/v1/agents/{agent_id}/drafts`、`/drafts/{draft_id}/validate|review|publish`、`GET /v1/agents/{agent_id}/versions`、`GET /v1/agents/{agent_id}/versions/{version}`、`POST /v1/agents/{agent_id}/versions/{version}/activate`。
2. 在现有 AgentPackage 之上封装 Agent Asset Profile，不破坏已有 `/v1/commands`。
3. 明确 `status`、`owner`、`active_version`、`default_version`、禁用/删除语义。
4. OpenAPI 补齐 request/response schema。

验收：

- 可以不走命令总线创建/查询/禁用 Agent。
- 可以不走命令总线创建、查询、校验、review、发布 AgentPackage draft。
- 可以不走命令总线查询 package version，并把 stable version 激活为 Agent 默认版本。
- 禁用 Agent 后新 run、handoff、agent_tool 调用都被拒绝。
- Agent 资产变更写 Audit。

#### T2. PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 独立资源

状态：已完成 P0 主干。`/v1/agents/{agent_id}/prompt-profile`、`/prompt-profile/preview`、`/prompt-profile/versions`、`/prompt-profile/activate`、`/prompt-profile/governance`、`/tool-bindings`、`/tool-bindings/versions`、`/tool-bindings/activate`、`/tool-bindings/governance`、`/skills`、`/skills/governance`、`/skills/{skill_id}`、`/skills/{skill_id}/governance`、`/skills/{skill_id}/versions`、`/skills/{skill_id}/activate`、`/collaborators`、`/collaborators/governance`、`/collaborators/{collaborator_agent_id}`、`/collaborators/{collaborator_agent_id}/governance`、`/collaborators/{collaborator_agent_id}/versions`、`/collaborators/{collaborator_agent_id}/activate`、`/exported-tools`、`/exported-tools/governance`、`/exported-tools/{tool_id}`、`/exported-tools/{tool_id}/governance`、`/exported-tools/{tool_id}/versions`、`/exported-tools/{tool_id}/activate` 和 OpenAPI schema 已补齐；PromptProfile 带 `draft_id` 时仍作为 AgentPackage draft wrapper，不带 `draft_id` 的 `POST/PUT/PATCH/DELETE /prompt-profile` 已支持 standalone CRUD，并复用 `agent_prompt_profiles` 的 `source_kind=profile` 独立行；Skill 带 `draft_id` 时仍作为 AgentPackage draft wrapper，不带 `draft_id` 的 `POST/PUT/PATCH/DELETE /skills` 已支持 standalone CRUD，并复用 `agent_skill_definitions` 的 `source_kind=skill` 独立行；ToolBinding 带 `draft_id` 时仍作为 AgentPackage draft wrapper，不带 `draft_id` 的 `POST/PUT/PATCH/DELETE /tool-bindings` 已支持 standalone CRUD，并复用 `agent_tool_bindings` 的 `source_kind=tool_binding` 独立行；Collaborator 带 `draft_id` 时仍作为 AgentPackage draft wrapper，不带 `draft_id` 的集合 `POST` 和单体 `POST/PUT/PATCH/DELETE` 已支持 standalone CRUD，并复用 `agent_collaborators` 的 `source_kind=collaborator` 独立行；ExportedTool 带 `draft_id` 时仍作为 AgentPackage draft wrapper，不带 `draft_id` 的集合 `POST` 和单体 `POST/PUT/PATCH/DELETE` 已支持 standalone CRUD，并复用 `agent_exported_tools` 的 `source_kind=exported_tool` 独立行，同时同步启用/禁用 `agent_tool` ToolManifest；runtime loader 已在装载 AgentDefinition 后叠加 active PromptProfile、active standalone Skill、active ToolBinding、active standalone Collaborator 和 active standalone ExportedTool，prompt preview / run / candidate retrieval / collaborator retrieval / agent_tool provider lookup 读路径可脱离 package source 使用独立 profile / skill / tool binding / collaborator / exported tool。这些子资源 GET 已支持 `draft_id` 读取未发布 draft 视图，并可继续读取已发布版本视图；ToolBinding versions/activate 已有子资源入口，激活时只允许 stable package version，并回写 Agent active/default version；Skill / Collaborator / ExportedTool activate 会先确认目标版本内存在对应子资源，避免 missing 子资源误切默认版本；Skill V3 字段已进入 contract/compile，recommended/allowed tools 会影响候选工具召回；Agent package draft lifecycle 与 stable version activate 已有资源入口；`agent_prompt_profiles` / `agent_skill_definitions` / `agent_tool_bindings` / `agent_collaborators` / `agent_exported_tools` 投影表已随 draft/release 保存同步，管理 GET 路径在 Postgres Store 下已优先投影表直读；治理视图可汇总 standalone active、draft 和 release 来源，并输出 status、active/default、runnable、risk、approval、visibility 和授权/推荐计数等基础治理信号。

涉及文件：

- `internal/contracts/agent.go`
- `internal/agentdef/package/compiler.go`
- `internal/agentdef/package/service.go`
- `internal/discovery/tool/static.go`
- `internal/context/promptbundle`

任务：

1. PromptProfile 从 package metadata 中抽象出来，支持 draft、preview、activate。
2. SkillDefinition 补齐 V3 字段：recommended_tools、allowed_tools、recommended_memory_reads、recommended_memory_writes、recommended_handoffs、completion_criteria、output_schema。
3. ToolBinding 增加 allowed / denied group ids，并保持 allowed 不等于 exposed。
4. CandidateProvider 支持 ToolGroup 两层召回。
5. Collaborator 从 package collaborators 抽象出 active standalone 行，并在 runtime loader 中覆盖 AgentDefinition.Collaborators。
6. ExportedTool 从 package exports 抽象出 active standalone 行，并在 runtime loader 中覆盖 AgentDefinition.Exports.Tools。
7. PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 提供 governance 只读视图，汇总 standalone active、draft、release 来源。

验收：

- Prompt 修改默认只影响新 run，旧 run 的 PromptBundle hash 不变。
- Skill 可以声明推荐工具和允许工具，并影响候选排序。
- ToolGroup 授权能召回组内工具，组 disabled 后旧 snapshot 中工具也会在执行前拒绝。
- 子资源 governance 端点能返回 standalone active、draft、release 三类来源，并携带风险、审批、可运行与授权计数等治理信号。

#### T3. Tool Catalog 持久化与 ToolGroup

状态：已完成主干。provider/group/manifest/version/runtime cache 已持久化，启动 restore、provider/group disable unregister、执行前 availability deny stale tools 已有测试；`/v1/tool-providers`、`/v1/tool-groups`、`/v1/tool-manifests`、`/v1/tool-providers/{provider_id}/health`、`/v1/tool-provider-governance` 和 OpenAPI schema 已补齐。health_status / last_health_check_at / last_health_error 已持久化，unhealthy provider 会卸载静态 ToolHost 工具并拒绝旧快照执行；Tool provider governance 可输出当前 provider health/status、manifest/group 分布、blocked reasons、missing provider 计数、risk 计数，并可按 `trace_id` 附加 `tool_provider.*` invoke / health evidence；CandidateProvider 已对 allowed group 和 objective 命中的 group_id 做排序 boost；ToolHost provider 已支持 `auth_ref` 引用头、`timeout_ms` 和 `retry_max`，并覆盖 catalog / health / invoke；HTTP execution domain 已强制 network_policy.allow_network 并校验 allowed_hosts，`http_direct` manifest 已强制仅允许 low risk，且 `disable_http_direct` / `CLEAN_CORE_DISABLE_HTTP_DIRECT` 可关闭 direct HTTP 注册与 restore；更完整 ToolHost mTLS、真实凭据解析、健康调度、历史趋势和配额指标仍待补。

涉及文件：

- `internal/tool/catalog/catalog.go`
- `internal/tool/registry`
- `internal/tool/runtime/runtime.go`
- `internal/storage/postgres/postgres.go`
- `migrations/*.sql`

任务：

1. 增加 `tool_providers`、`tool_groups`、`tool_manifests`、`tool_manifest_versions`、`tool_runtime_registry_cache`。
2. ToolCatalog Service 接入 Store，启动时恢复 enabled manifests。
3. `UpsertProvider(status=disabled)` 时同步禁用或运行时拒绝 provider 下工具。
4. ToolRuntime 执行前加入 `ToolAvailabilityChecker`：tool/group/provider/status/tenant/agent binding/policy/approval/risk/schema/credential/network。
5. ToolHost invoke payload 对齐 V3：`trace_id`、`run_id`、`task_id`、`idempotency_key`。

验收：

- 服务重启后热注册工具仍存在。
- provider disabled 后，即使 registry 中仍有旧工具，执行也会被拒绝。
- ToolHost 收到完整治理上下文。

#### T4. Runtime Hook 产品化最小闭环

状态：已完成 MVP 主干。Hook Provider / HookManifest / HookManifestVersion / Binding / Preview / Event Store、before_memory_write、patch 校验、trace/audit、package-declared runtime_hooks_hash、`/v1/runtime-hook-providers`、`/v1/runtime-hook-providers/{provider_id}/health`、`/v1/runtime-hook-providers/{provider_id}/catalog`、`/v1/runtime-hook-providers/{provider_id}/catalog/sync`、`/v1/runtime-hook-manifests`、`/v1/runtime-hook-manifests/{hook_id}/versions`、`/versions/{version}`、`/versions/{version}/activate`、`/v1/runtime-hook-events`、`/v1/runtime-hook-governance`、`/v1/runtime-hook-approvals`、`/v1/agents/{agent_id}/runtime-hooks` 和 OpenAPI schema 已接入；unhealthy provider invoke guard、disabled HookManifest invoke guard 与 static binding manifest 授权门禁已接入；Hook config/patch 敏感字段 lint、patch 配额、Static HookHost timeout trace、provider health trace-scoped metrics、Static HookHost catalog 读取/字段校验、HookManifest 持久化资源 API、enabled version activate、requires_approval / approval_policy 绑定审批门禁、runtime hook approval list view 和 runtime hook governance summary/trend/provider_matrix 已接入；更深 provider health / quota 矩阵仍待补。

涉及文件：

- `internal/runtime/hook/hooks.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/contracts/agent.go`
- `internal/server/server.go`
- `internal/storage/postgres/postgres.go`

任务：

1. 增加 HookProvider、HookManifest、AgentRuntimeHookBinding、HookEvent 模型和表。
2. 增加 API：`/v1/runtime-hook-providers`、`/v1/runtime-hook-providers/{provider_id}/catalog`、`/v1/runtime-hook-providers/{provider_id}/catalog/sync`、`/v1/runtime-hook-manifests`、`/v1/runtime-hook-manifests/{hook_id}/versions`、`/versions/{version}/activate`、`/v1/runtime-hook-events`、`/v1/runtime-hook-governance`、`/v1/runtime-hook-approvals`、`/v1/agents/{agent_id}/runtime-hooks`、`/preview`。
3. AgentDefinition 增加 RuntimeHooks 引用，compiler 和 package flow 支持。
4. 接入 `before_memory_write`。
5. 每次 Hook invoke / applied / denied / failed 写 Trace 和 Audit。
6. 增加 Patch 校验：不能扩大权限，不能引入未授权工具，不能写状态。

验收：

- Agent 可绑定一个 Data Hook，并在 preview 中看到候选或 prompt patch。
- Hook 异常不会中断主 run，只记录降级事件。
- Hook 不能让 Agent 使用未授权工具。

#### T5. Handoff 安全整改

状态：已完成核心安全闭环。`origin.agent.delegate` 现在必须带当前 step retrieved collaborators 内部标记，目标必须来自本 step 召回；命令兼容入口会重新召回协作者；depth、cycle、collaborator status、target release、AgentAsset disabled/deleted 和 source disabled guard 已接入。

涉及文件：

- `internal/discovery/tool/static.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/tool/handoff/executor.go`
- `internal/task/handoff/service.go`

任务：

1. `origin.agent.delegate` 校验 `to_agent_id` 来自当前 step retrieved collaborators，而不只是静态 collaborators。
2. AgentCollaboratorRef 补齐 V3 字段：allowed_handoff_modes、default_handoff_mode、max_context_tokens、requires_approval。
3. 实现 max handoff depth 和 cycle guard。
4. 目标 Agent disabled / deleted / non-stable 时拒绝；当前已覆盖 collaborator disabled/deleted、package release non-canary/non-stable、Agent Asset disabled/deleted，并补充了禁用 source Agent 不能继续发起新 handoff 的入口门禁。
5. HandoffContextPackage 按 collaborator max_context_tokens 裁剪。

验收：

- 模型指定未召回 collaborator 时被 Decision/ToolRuntime 拒绝。
- A -> B -> A 循环被拒绝。
- 超过 handoff depth 被拒绝并写 Trace/Audit。

#### T6. OpenAPI 与命令总线对齐

状态：部分完成。OpenAPI 已补齐 Agent Asset、Agent package drafts、Agent package versions / activate、PromptProfile、Skill、ToolBinding、Collaborator、ExportedTool、Agent 子资源 governance、Tool Catalog、Tool Provider Governance、Runtime Hook Provider、Runtime Hook Provider Catalog、Runtime Hook Manifest、Runtime Hook Manifest versions / activate、Agent RuntimeHook Binding、Runtime Hook Governance 和 `/v1/tasks/start` 等资源路径；`scripts/verify_contracts.ps1` 已把这些路径纳入必备契约检查。当前 PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool 已在不带 `draft_id` 时支持 standalone active 子资源写入，带 `draft_id` 时保持 AgentPackage draft wrapper，且 governance 端点已能汇总 standalone active、draft 与 release 来源。

涉及文件：

- `docs/openapi.clean-core.v1.json`
- `internal/server/server.go`
- `scripts/verify_contracts.ps1`

任务：

1. OpenAPI 增加 V3 资源 API。
2. `/v1/commands` 保持兼容，但标注为低层命令入口。
3. contract verifier 同时检查 command enum 和资源路径 schema。

验收：

- V3 API 总览中的 MVP 路径在 OpenAPI 中都有定义。
- 新资源 API 与命令入口产生一致的审计和状态变化。

### P1：补齐企业治理和执行面安全

#### T7. 企业数据本地化基线

任务：

1. 增加 `DataRef` / `CiphertextRef` / `RedactedSummary` 模型。
2. Trace / Audit 引入统一 redaction policy。
3. ToolHost 支持加密输入引用、结果摘要、密文输出引用。
4. 支持 provider 级 mTLS / auth 配置。
5. CredentialScope 基础链路已从“记录元数据”升级为“运行时凭据解析与最小授权”：ToolRuntime 会在 dispatch 前解析 credential handle，缺 resolver、跨 tenant credential、越界 data scope/tenant 会拒绝；后续继续补 secret backend/KMS/轮换/治理视图。

验收：

- 高敏工具调用 trace 中不能出现明文输入/输出。
- AgentPlugin Service 可以只收到 ciphertext_ref 并返回 artifact_ref / ciphertext_ref。
- credential 使用可审计，未授权 credential_ref 被拒绝。

#### T8. AgentExportedTool 真执行语义

状态：已完成基础闭环。`agent_tool` execution domain 已通过 `AgentToolHandler` 接入 provider agent runner：调用方执行 exported tool 时不再回显 provider/operation/arguments，而是在校验 provider Agent 可运行、exported tool enabled 后，构造 provider Agent 的 `agent.run` envelope，返回 provider run 的 `run_id` / `task_id` / `status` / `reply` / `artifact_refs`，并记录 `agent_tool.invoked` / `agent_tool.completed` / `agent_tool.failed` Trace；ExportedTool standalone CRUD 已接入 `agent_exported_tools` active projection 和 runtime loader overlay，不带 `draft_id` 的 upsert/delete 会同步启用/禁用对应 `agent_tool` ToolManifest；`TestAgentToolInvokeRunsProviderAgent` 覆盖了 `tools.invoke` 到 provider agent run 的闭环，`TestHighRiskAgentToolInvokeRequiresApproval` 覆盖 high-risk exported tool 首次调用生成 ApprovalRequest、审批后带 `approval_id` 重试执行 provider agent run，`TestExportedToolStandaloneCRUDOverlaysRuntimeLoaderAndManifest` 覆盖 standalone ExportedTool CRUD、runtime overlay 和 ToolManifest 同步。

任务：

1. 明确 agent_tool execution domain：同步调用、异步 child run、或固定 operation handler。
2. `AgentToolExecutor` 不再只 echo provider/operation/arguments，而是按协议执行 provider agent 的 exported operation。
3. agent_tool 调用结果写 ToolResult、Trace、Audit，并遵循 provider agent policy。

验收：

- Caller Agent 调用 exported tool 后，provider Agent 确实产出结果或 child run。
- 高风险 exported tool 首次调用会进入 ApprovalRequest，审批后带 `approval_id` 重试才执行。

#### T9. Context Engineering 边界修正

状态：已完成基础闭环。普通 API `agent.run` 默认不再构建 direct `ConversationContext`，只有显式 `collaboration` 或配置 `conversation_direct_enabled=true` 才进入 direct conversation；群聊 / thread / collaboration 仍走接话判断、上下文充分性判断和历史召回。PromptBundle 中 retrieved context 统一标记 `input_boundary=untrusted`，governance metrics 已增加 addressee / sufficiency / retrieval 请求、完成、失败和 merge 计数。

任务：

1. 普通 API run 默认不启用 ConversationContext Mode，除非显式传 `collaboration` 或配置允许 direct conversation。
2. RetrievedContext 在 PromptBundle 中统一标记 untrusted。
3. Addressee / Sufficiency / Retriever 事件补齐 metrics。

验收：

- 普通 `/agent.run` 不会因为 direct conversation guard 误 no-op。
- 群聊消息仍能走接话判断和历史召回。

#### T10. Trace / Audit 敏感数据与 Hook 证据矩阵

状态：部分完成。Replay report 已能汇总 prompt hash、policy version、tool/handoff/terminal 证据，并新增 trace payload redaction 扫描；发现 `api_key`、`authorization`、裸 `sk-*` / Bearer / private key 等明显敏感信息时会输出 `redaction_violations` 并把 report 标为 incomplete。Governance metrics 已有 hook latency、approval wait、handoff latency、conversation judge/retrieval 计数，以及 ToolHost provider invoke latency / failure / health check failure、RuntimeHook provider health check failure 计数。Runtime Hook 事件持久化已补 provider/latency 证据，`/v1/runtime-hook-events` 支持 provider 过滤，`/v1/runtime-hook-governance` 可按 trace/provider/hook/phase/status/from/to/interval 输出总量、失败率、latency、provider/hook/phase/status 分桶、provider_matrix 和时间窗口趋势；`/v1/tool-provider-governance` 已可把当前 ToolProvider / ToolManifest 状态和 trace-scoped `tool_provider.*` evidence 汇总为 provider/tool 矩阵；provider health / quota 深层历史矩阵、全局 data classification 和 ciphertext_ref 强约束仍待补。

任务：

1. 扩展 replay report，检查 hook、tool provider、prompt hash、decision、handoff、安全拒绝事件。
2. Audit 增加敏感数据策略校验字段。
3. Governance metrics 增加 hook latency、approval wait、handoff latency，以及 ToolHost provider invoke/health、RuntimeHook provider health trace 的 latency 和失败率统计；Runtime Hook governance summary 增加 provider/hook/phase/status 过滤、分桶、provider_matrix 与 from/to/interval 趋势窗口；Tool Provider Governance 增加当前 provider/tool 矩阵与可选 trace evidence；provider health / quota 深层历史矩阵仍需继续补。

验收：

- 一次完整 run 能通过 V3 Trace 必写点检查。
- 敏感字段出现在 audit/trace payload 时测试失败。

### P2：协作能力包产品化

#### T11. Knowledge / CrossGroup 产品化

状态：已完成 MVP 主干。`/v1/knowledge-bases`、`/v1/knowledge-bases/{knowledge_base_id}`、`/documents`、`/index-jobs`、`/v1/knowledge-search`、`/v1/cross-group-share-policies` 和 `/v1/cross-groups/search` 已接入；KnowledgeBase 已补 `search_mode`、`document_count`、`last_indexed_at`，KnowledgeDocument 已补 `index_status` / `indexed_at`，并新增 `knowledge_ingestion_jobs` 作为 ingestion / index status 查询事实源；检索层已抽象为 `SearchAdapter`，默认本地 BM25，并支持 `embedding` / `hybrid` 语义入口和结果标记，后续可替换真实向量/混合检索后端；CrossGroup 已新增共享策略资源、显式 share policy 双门禁、脱敏策略（summary_only / mask_emails / mask_numbers / strict）和可选 ApprovalRequest 流程。默认仅有 cross-group search permission 仍不能跨群查，必须存在 enabled share policy；授权后跨群搜索只返回脱敏后的 `KnowledgeSearchResult`。

任务：

1. KnowledgeBase 管理 API。
2. ingestion job / index status。
3. embedding / hybrid 检索适配层。
4. CrossGroup 共享策略 API、脱敏策略、审批流。

验收：

- 默认不能跨群查。
- 授权后只能返回允许共享的脱敏内容。

#### T12. 外部协作适配矩阵

任务：

1. 群成员同步、频道/线程绑定、消息回写配置 API。
2. Array / Slack / Feishu / 企业微信等适配器抽象。
3. reply、waiting_input、waiting_approval、artifact、handoff、run.failed 的回写矩阵配置化。

验收：

- 每类 TaskEvent / TraceEvent 都能映射为外部协作事件。
- 回写失败进入 `external.writeback_failed` 并可查询。

### P3：高级运行时路线图

这些能力是 V3 文档明确后置的方向，不建议抢在 P0/P1 之前做：

1. 完整 RuntimeDriver。
2. Strategy Hook 全量开放。
3. 多 worker / queue / managed execution domain。
4. parallel planning / tree planning。
5. managed MCP / third-party agent provider 深度集成。

## 6. 不应现在做的事

1. 不要先做完整自定义 RuntimeDriver。当前 Hook 层还没有管理面和审计闭环，过早开放控制流会打穿治理边界。
2. 不要把群聊能力写死进 Coordinator 主循环。V3 已明确它是 Capability Pack。
3. 不要把 HTTP Direct 当生产主路径。生产主路径应该是 AgentPlugin Service / Static ToolHost。
4. 不要让 AgentCapability 自动变 ToolManifest。能力发现和工具授权必须分开。
5. 不要让 AgentPlugin Service 写平台 Task / Run / Memory 状态。它只能返回 ToolResult / artifact refs / metadata。

## 7. 推荐里程碑

| 里程碑 | 目标 | 包含任务 | 完成标准 |
| --- | --- | --- | --- |
| Milestone 1 | V3 MVP API 与资产主干 | T1、T2、T6 | Agent / Prompt / Skill / ToolBinding / Collaborator / Release 可通过资源 API 操作，OpenAPI 对齐 |
| Milestone 2 | Tool Catalog 事实源 | T3 | provider / group / manifest 持久化，重启不丢，执行前门禁完整 |
| Milestone 3 | Hook MVP 闭环 | T4 | Data Hook 可注册、绑定、预览、审计，且不能绕过治理 |
| Milestone 4 | Handoff 安全闭环 | T5 | delegate 只能委派当前 step 已召回 collaborator，depth/cycle/collaborator disabled/target release/AgentAsset guard 生效 |
| Milestone 5 | 企业执行面安全基线 | T7、T8、T10 | ToolHost 协议、数据边界、敏感信息治理、agent_tool 真执行闭环 |
| Milestone 6 | 协作能力包产品化 | T9、T11、T12 | 群聊上下文边界清晰，Knowledge/CrossGroup/外部协作可配置可验收 |

## 8. 验收测试建议

新增或扩展以下测试和脚本：

| 测试 | 覆盖目标 |
| --- | --- |
| `go test ./internal/tool/catalog` | ToolProvider / ToolManifest / ToolGroup 持久化和启停一致性 |
| `go test ./internal/runtime/hook` | Patch 校验、合并、拒绝越权 |
| `go test ./internal/runtime/kernel` | hook 调用、before_memory_write、conversation mode gating、retrieved collaborator 校验 |
| `go test ./internal/tool/handoff` | depth / cycle guard、disabled target、mode 限制 |
| `go test ./internal/tool/agenttool` | exported tool 真实执行语义 |
| `scripts/e2e_plugin_runtime_smoke.ps1` | AgentPlugin Service、HTTP Direct、agent_tool、Hook 组合烟测 |
| `scripts/verify_contracts.ps1` | command enum、OpenAPI、资源 API schema 对齐 |
| 新增 `scripts/e2e_v3_mvp_smoke.ps1` | V3 MVP 主链路：创建 Agent -> PromptProfile -> Skill -> ToolProvider -> ToolGroup -> Hook -> Run -> Trace/Audit |

## 9. 最终判断

如果按“内核能跑”判断，当前代码已经完成了 V3 的大量底层能力。

如果按“V3 产品文档可交付”判断，下一步不应该继续堆更多高级 runtime，而应该优先把以下主干补齐：

```text
AgentPlugin Service 协议完整化
Tool/Hook provider health 调度、历史指标与配额治理
Runtime Hook provider health / quota 深层治理矩阵
企业数据边界基线
OpenAPI / 测试 / 审计证据矩阵
```

完成这些之后，V3 MVP 才能从“CleanCore 内核已具备”升级为“平台产品能力可验收”。
