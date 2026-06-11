# 原智能体 CleanCore 产品文档能力缺口分析 v0.1

日期：2026-05-31

## 1. 结论先行

不能简单说“这些细节都没做好”。当前代码已经实现了 Clean Core 的核心运行闭环：

- `agent.run` 主链路已经能创建 `AgentRun` / `Task`，构建 `WorkView` / `PromptBundle`，调用模型，校验 `Decision`，执行工具并继续循环。
- `ToolRuntime` 已经接入 ToolPolicy、ExecutionDomain、输入/输出 schema 校验、Trace 记录。
- `AgentPackage` 已有 draft / validate / publish / eval / canary / stable / rollback 的模块能力和部分 HTTP command。
- `PolicySet` 已有结构、默认策略、内存 Store、Postgres Store、运行时加载。
- `Agent-to-Agent Handoff` 已经通过内部工具 `origin.agent.delegate`、HandoffPolicy、HandoffContextPackage、ChildTask、下游 AgentRun、Trace/Audit 串起来。
- Postgres migration、OpenAPI、readiness、go/no-go、replay、metrics 等工程化能力已经有基础实现。

但是，如果严格按产品/架构文档验收，“可交给优化人员持续调优”和“可治理、可审计、可版本化策略运营”还没有完全闭环。主要缺口集中在：

1. 人类可写的 Prompt / Skill / ToolBinding 只有 `agent.package.publish` 快捷入口，没有完整 Draft / Patch / Review / Publish 外部 API。
2. PolicySet 有存储和运行时加载，但没有 `policy.update` / Policy Draft / Policy 发布 / 灰度 / 回滚 API。
3. `agent.package.publish` 当前会直接 create draft + validate + publish，和文档中的 Draft / Proposal / Eval / Review / Publish 分阶段流程不完全一致。
4. Trace / Audit 必写点有常量和部分落盘，但运行链路没有完整记录 `input.received`、`agent.loaded`、`task.created`、`task.loaded`、`capability.retrieved` 等事件。
5. Tool Audit 粒度与文档清单不完全一致，当前主要记录 `tool.policy_checked`，没有按文档拆出 `tool.policy_denied`、`tool.approval_required`、`tool.high_risk_invoked`。
6. Eval Suite 能跑基础断言并影响 stable 门禁，但还不是文档描述的完整评测套件、评测用例管理、失败原因/工具轨迹展示体系。
7. 外部协作有 HTTP Adapter 和 ExternalTaskBinding，但业务事件到外部协作系统的自动回写矩阵还没有完整接入运行链路。
8. OpenAPI 暴露面没有覆盖已存在的全部 command 语义和管理类能力。

## 2. 审计依据

### 产品/架构文档

- `docs/原智能体CleanCore全景开发设计文档_v1.2_Clean.md`
- `docs/原智能体CleanCore工程实施规格文档_v1.0.md`
- `docs/原智能体CleanCore接口冻结与受控变更开发文档_v1.0-alpha.md`
- `docs/openapi.clean-core.v1.json`

### 当前代码证据

- `internal/server/server.go`
- `internal/app/core/core.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/agentdef/package/service.go`
- `internal/agentdef/package/compiler.go`
- `internal/policy/engine/engine.go`
- `internal/policy/toolpolicy/evaluator.go`
- `internal/storage/postgres/postgres.go`
- `internal/governance/replay/report.go`
- `internal/governance/metrics/metrics.go`
- `internal/bridge/array/stub.go`
- `internal/bridge/array/http_adapter.go`
- `migrations/001_clean_core_base.sql`

## 3. 当前已经实现的关键能力

| 能力 | 文档预期 | 当前实现证据 | 判断 |
| --- | --- | --- | --- |
| AgentRun 主循环 | AgentEnvelope -> AgentRun -> Task -> WorkView -> PromptBundle -> Decision -> Tool/Response | `internal/runtime/kernel/coordinator.go` 中 `HandleEnvelope` / `loop` / `step` | 已实现 |
| AgentDefinition / AgentPackage 编译 | Prompt / Skill / ToolBinding 进入 AgentPackage，编译为 AgentDefinition | `internal/agentdef/package/compiler.go`、`internal/agentdef/package/service.go` | 部分实现 |
| Tool Policy 必经链路 | 工具执行必须经过 Policy / Audit | `internal/tool/runtime/runtime.go` 调用 `toolpolicy.Evaluator` | 已实现，但 Audit 粒度不足 |
| ToolResult Validator | 工具结果不能直接信任，需校验 | `internal/tool/runtime/runtime.go` 校验 `OutputSchema` | 已实现 |
| PolicySet 结构与加载 | Policy 可配置、可版本化、可审计 | `internal/contracts/policy.go`、`internal/policy/engine/engine.go`、`internal/storage/postgres/postgres.go` | 部分实现 |
| 长任务显式升级 | 长 Task 不静默升级，需 TaskCommand | `server.go` 中 `upgradeTaskAgentVersion` | 已实现 |
| Agent-to-Agent Handoff | `origin.agent.delegate` -> HandoffPolicy -> ContextPackage -> ChildTask -> 下游 AgentRun | `internal/app/core/core.go`、`internal/tool/handoff/executor.go`、`internal/task/handoff/service.go` | 已实现 |
| Governance Replay / Metrics | Trace / Audit / Metrics / Replay | `internal/governance/replay`、`internal/governance/metrics`、`/v1/metrics/governance` | 部分实现 |
| Postgres Store / Migration | 核心表、PolicySet、Package、Task、Run 等持久化 | `migrations/001_clean_core_base.sql`、`internal/storage/postgres/postgres.go` | 已实现基础能力 |

## 4. 未实现或部分实现能力清单

| 编号 | 能力 | 文档依据 | 当前代码证据 | 实现状态 | 分类 | 优先级 |
| --- | --- | --- | --- | --- | --- | --- |
| G-01 | 优化人员可创建/修改 AgentPackage Draft | 全景文档 2.5：提示词、Skill、ToolBinding 必须进入 Draft / Proposal / Publish；24.5：优化人员可以创建 AgentPackage Draft、修改 AGENTS.md / SKILL.md | `agentdef/package.Service` 有 `CreateDraft` / `PatchPrompt` / `UpdateToolBinding`，但 `server.go` 只暴露 `agent.package.publish` | 部分实现 | B 文档要求但部分实现 | P1 |
| G-02 | AgentPackage 分阶段发布流程 | 全景文档 24.5：Draft -> Eval -> Review -> Publish / Canary -> Rollback；接口文档要求受控变更 | `packagePublish` 在一个 command 内 create draft + validate + publish；未暴露 draft_id 驱动的 publish/review API | 实现偏离 | C 文档要求但实现偏离 | P1 |
| G-03 | Policy Draft / policy.update / Policy 发布灰度回滚 | 全景文档 2.5：Policy 优化必须 Eval；AgentPackage / Policy 发布必须支持版本、灰度、回滚；管理类工具策略提到 `policy.update` | `PolicySet`、`PolicyStore.Put/Get` 已有；`server.go` 没有 `policy.update`；OpenAPI 没有 `/v1/policies` | 部分实现 | B 文档要求但部分实现 | P0 |
| G-04 | 优化人员可改 Policy 不改 Go 代码 | 全景文档 24.5：优化人员可以调整 ToolBinding 和 Policy Draft；25 章：优化人员可以改 Prompt / Skill / Policy / Eval，不应改 Runtime Kernel | 运行时可从 `PolicyStore` 加载，但缺少外部受控写入入口；目前需代码或数据库直接写 `policy_sets` | 部分实现 | B 文档要求但部分实现 | P0 |
| G-05 | 管理类工具必须经过 Policy / Audit | 全景文档 2.5：所有管理类工具和外部工具调用必须经过 Policy / Audit；13.7 管理类工具策略 | `agent.package.publish` 做角色校验并写 package audit，但没有统一 ToolRuntime/PolicyEngine 管理类工具调用链；`policy.update` 未实现 | 部分实现 | B 文档要求但部分实现 | P1 |
| G-06 | Trace 必写点完整落盘 | 工程规格 19.3：`input.received`、`agent.loaded`、`task.created`、`task.loaded`、`capability.retrieved` 等必须 Trace | `contracts/governance.go` 有常量；`coordinator.go` 主要记录 `run.created`、`workview.built`、`promptbundle.built`、`model.*`、`decision.*`、`tool.*`、`handoff.*` | 部分实现 | B 文档要求但部分实现 | P1 |
| G-07 | Audit 必写点按清单记录 | 工程规格 19.4：`tool.policy_denied`、`tool.approval_required`、`tool.high_risk_invoked` 等必须 Audit | `toolpolicy/evaluator.go` 统一写 `tool.policy_checked`；常量中定义了细分 action 但运行时未使用 | 实现偏离 | C 文档要求但实现偏离 | P1 |
| G-08 | Policy 版本化、回滚、审计链 | 接口冻结文档 L4：策略配置必须版本化、可回滚、写 Audit、关键变更运行 Eval | `policy_sets` 表只按 `(tenant_id, policy_set_id)` upsert，`version` 字段会覆盖；没有 policy release history / rollback 命令 | 部分实现 | B 文档要求但部分实现 | P0 |
| G-09 | Eval Suite 完整规格 | 工程规格 28：EvalCase、EvalResult、发布门槛；全景文档要求 Prompt/Skill/Policy 优化必须 Eval | `internal/eval/runner.go` 支持输入、最终回复包含、工具调用断言；缺少 eval suite 管理、critical/safety 分类、通过率门槛、结果查询 API | 部分实现 | B 文档要求但部分实现 | P1 |
| G-10 | Stable 发布门槛深度 | 工程规格 28.3：critical eval 全通过、普通 eval 通过率、安全 eval 全通过、工具误调用率阈值 | `MarkStable` 要求有一次 passed eval；`EvaluateReleaseAction` 支持部分 ReleasePolicy，但未实现 suite 级阈值 | 部分实现 | B 文档要求但部分实现 | P1 |
| G-11 | Handoff 状态机命令完整性 | 工程规格 11.3/TaskCommand：create/accept/reject/complete/fail handoff | `contracts` 有 Handoff command/status；`task/handoff.Service` 支持 create/complete/failed path；`server.go` 未暴露 `create_handoff` / `accept_handoff` / `reject_handoff` 等 command | 部分实现 | B 文档要求但部分实现 | P2 |
| G-12 | 外部协作事件回写矩阵 | 工程规格 29：reply/waiting_input/waiting_approval/artifact.created/handoff.created/run.failed 映射到外部系统 | `Bridge` 有 `SendMessage` / `AttachArtifact` / HTTP adapter；运行链路主要只在 `task.start` 绑定 ExternalTaskBinding，未见自动按事件矩阵回写 | 部分实现 | B 文档要求但部分实现 | P2 |
| G-13 | 外部输入适配层 | 全景文档 4.1/21.10：@mention、slash command、channel routing 在 AgentEnvelope 前完成 | 文档明确说 Clean Core 不解析原始渠道格式，只处理标准化 AgentEnvelope；当前代码符合 Clean Core 边界 | 不属于缺口 | H 可选/外部适配 | P3 |
| G-14 | OpenAPI 对管理能力的契约覆盖 | 工程规格 21：Command/API 应明确；接口冻结要求契约快照 | OpenAPI 只有 `/v1/commands` 泛型入口和查询接口，没有列出 `agent.package.*`、`eval.run`、policy 管理命令的具体 request/response schema | 部分实现 | B 文档要求但部分实现 | P2 |
| G-15 | CapabilityDiscovery 深层能力 | 全景文档 10：三层发现、目标 Agent 发现、Skill 渐进加载、Policy filtering | `discovery/tool/static.go` 实现静态关键词/allowed/denied/version 过滤；Agent-to-Agent 目标选择在 handoff executor 中局部完成；没有独立 Agent capability index/API | 部分实现 | B 文档要求但部分实现 | P2 |
| G-16 | Metrics 深度 | 全景文档 governance 包括 Metrics；产品目标强调观察 Trace / Metrics | `governance/metrics` 从 trace 统计 model/tool/handoff/prompt 数量；缺少耗时、审批等待、handoff 耗时、失败率趋势等运营指标 | 部分实现 | B 文档要求但部分实现 | P2 |
| G-17 | Artifact / Memory 策略完整性 | 全景文档 memory-artifact：MemoryPolicy、权限校验、Artifact 引用、下游读取再次校验 | Memory/Artifact Store 已有 tenant guard 和 memory.write audit；没有独立 MemoryPolicy / Artifact read audit / artifact.delete | 部分实现 | B 文档要求但部分实现 | P2 |
| G-18 | Policy 对自动升级的策略路径 | 全景文档 4.3：长 Task 升级可通过显式 TaskCommand 或 TaskUpgradePolicy | 显式 `upgrade_agent_version` 已实现；未见 TaskUpgradePolicy 自动升级策略 | 部分实现 | B 文档要求但部分实现 | P3 |

## 5. 重点问题说明

### 5.1 Prompt / Skill / ToolBinding 不是完全没做，而是缺完整人类工作台入口

文档要求：

- 提示词、Skill、ToolBinding 可以编辑，但必须进入 AgentPackage Draft / Proposal / Publish 流程。
- 优化人员可以创建 AgentPackage Draft，修改 AGENTS.md / SKILL.md，调整 ToolBinding，运行 Eval Suite，通过审核后发布。

当前代码：

- `AgentPackageSource` 已支持 `agents_md`、`prompt`、`tool_bindings`、`metadata`。
- `Service` 已支持 `CreateDraft`、`PatchPrompt`、`UpdateToolBinding`、`ValidateDraft`、`PublishDraft`。
- `server.go` 对外只暴露 `agent.package.publish`，并且在一个命令里完成 draft -> validate -> publish。

缺口：

- 没有对外 `agent.package.draft.create`。
- 没有对外 `agent.package.draft.patch_prompt`。
- 没有对外 `agent.package.tool_binding.update`。
- 没有对外 `agent.package.review`。
- `agent.package.publish` 没有按文档示例使用已有 `draft_id`，而是直接从 payload 编译发布。

判断：

这是“模块能力有了，但人类可运营流程没有完整开放”的部分实现问题。

### 5.2 PolicySet 有运行能力，但没有产品化策略管理能力

文档要求：

- Policy 是策略层，可配置、可版本化、可审计。
- Policy 优化必须通过 Eval Suite。
- AgentPackage / Policy 发布必须支持版本、灰度、回滚。
- 管理类工具中提到 `policy.update`，管理员调用需要审批 + Audit。

当前代码：

- `contracts.PolicySet` 已包含 RuntimePolicy、ToolPolicy、ToolRepairPolicy、ApprovalPolicy、PromptPolicy、CompressionPolicy、RecoveryPolicy、HandoffPolicy、ReleasePolicy。
- `DefaultPolicySet` 已给出默认策略。
- `PolicyStore` 支持 `Get` / `Put`。
- Runtime 会按 `policy_set_id` 加载策略。

缺口：

- 没有 `policy.update` command。
- 没有 Policy Draft。
- 没有 Policy Validate / Eval / Review / Publish。
- 没有 Policy Canary / Stable / Rollback。
- Postgres `policy_sets` 是 upsert 当前版本，不保留完整版本历史。
- 没有策略变更 Audit 的实际调用路径。

判断：

这是当前最关键的产品能力缺口。现在策略“能被代码加载”，但还不能让优化人员/管理员按文档流程安全运营。

### 5.3 Trace / Audit 是核心事实的一部分，但必写点没有完全闭环

文档要求：

- 工程规格明确列了必须 Trace 的事件。
- 工程规格明确列了必须 Audit 的事件。
- 全景文档强调 Trace/Audit 不是附加功能，而是核心运行事实的一部分。

当前代码：

- Trace 事件常量完整。
- Runtime 记录了多个关键事件。
- ToolPolicy、Package、Task audited transition、Handoff、Memory 有 Audit。

缺口：

- `input.received`、`agent.loaded`、`task.created`、`task.loaded`、`capability.retrieved` 等常量存在但运行路径没有系统性记录。
- Tool Audit 使用 `tool.policy_checked`，但文档列的是 `tool.policy_denied`、`tool.approval_required`、`tool.high_risk_invoked` 等结果型事件。
- 外部工具调用没有明确写 `external_tool_call` audit。
- policy.update、credential.used、artifact.delete 等常量没有实际业务入口或调用路径。

判断：

治理能力不是没有，但与文档要求的“审计证据矩阵”还没有完全一致。

### 5.4 Eval / Release 已有最小闭环，但不是完整 Eval Suite 产品能力

文档要求：

- Prompt / Skill / ToolBinding / Policy 优化必须通过 Eval Suite。
- stable 发布前需要满足关键 eval、安全 eval、通过率、工具误调用率等门槛。

当前代码：

- `eval.run` 可执行一次 eval case。
- `MarkEvalResult` 会记录 eval 结果。
- `MarkStable` 要求 package version 有 passed eval。
- `ReleasePolicy` 支持 canary/stable/rollback 的部分门禁。

缺口：

- 没有 Eval Suite 定义和持久化管理 API。
- 没有 critical/safety/normal 分类。
- 没有通过率统计。
- 没有工具误调用率阈值。
- 没有评测结果查询/展示 API。
- Policy 变更没有接入 Eval。

判断：

这是“最小可运行 eval”到“产品化 Eval Suite”的差距。

### 5.5 外部协作接入有 Adapter，但事件映射没有完整自动化

文档要求：

- Clean Core 不做完整 Group/TaskPool，但要通过 CollaborationContext / ExternalTaskBinding 接入外部协作系统。
- 工程规格要求 reply、waiting_input、waiting_approval、artifact.created、handoff.created、run.failed 等映射到外部任务。

当前代码：

- `ExternalTaskBinding` 已有。
- `ArrayBridge` 有 BindingStore、HTTPAdapter、SendMessage、AttachArtifact、CheckAccess。
- `task.start` 可绑定外部任务。

缺口：

- runtime/task/handoff/tool 事件没有统一自动调用 ArrayBridge 做外部回写。
- 没有完整事件映射器。
- 没有按文档矩阵对 reply/waiting_input/waiting_approval/artifact.created/handoff.created/run.failed 全覆盖。

判断：

外部协作基础设施已存在，但产品文档要求的事件同步闭环还不完整。

## 6. 不应判定为缺口的内容

| 内容 | 原因 |
| --- | --- |
| @mention / slash command 原始解析 | 文档明确说这是 AgentEnvelope 生成前的外围适配层，不进入 Clean Core。当前 Core 只接收标准化 AgentEnvelope，符合边界。 |
| 完整 Group / TaskPool / 消息通知系统 | 文档明确说不进入 Clean Core。 |
| MCP / LangChain / LangGraph | 文档明确说第一阶段不强加这些协议。 |
| 大而全的可视化后台 | 文档没有要求完整后台页面。当前缺的是 API/command/治理流程，不是必须有 UI。 |

## 7. 修复优先级

### P0：影响“产品文档核心承诺”的缺口

1. 增加受控 `policy.update` / Policy Draft / Policy Publish 流程。
2. PolicySet 改为真正版本化，保留历史版本，支持 rollback。
3. Policy 变更接入 Audit 和 Eval 门禁。

### P1：影响主要产品能力闭环

1. 拆出 AgentPackage Draft API：create / patch prompt / update tool binding / validate / review / publish。
2. 调整 `agent.package.publish`，支持从已有 `draft_id` 发布，而不是只支持“一步发布”。
3. 补齐 Trace 必写点：input.received、agent.loaded、task.created、task.loaded、capability.retrieved。
4. 调整 Tool Audit 粒度：policy_denied、approval_required、high_risk_invoked、external_tool_call。
5. 扩展 Eval Suite：suite/case 管理、分类、通过率、失败详情、结果查询。

### P2：影响可观测性、外部集成和完整治理

1. 外部协作事件映射器：reply、waiting_input、waiting_approval、artifact.created、handoff.created、run.failed。
2. Handoff command 状态机外部入口：accept/reject/fail/cancel 等。
3. OpenAPI 补齐管理类命令 schema 和 policy 管理 schema。
4. Metrics 增加耗时、等待、失败率等运营指标。
5. MemoryPolicy / Artifact read audit / artifact.delete 入口。

### P3：可后续增强

1. TaskUpgradePolicy 自动升级策略。
2. CapabilityDiscovery 独立 Agent capability index/API。
3. 更完整的协作平台适配器矩阵。

## 8. 建议后续任务拆解

### T1：实现 Policy 管理闭环

分类：B 文档要求但部分实现

优先级：P0

依据：全景文档要求 Policy 可配置、可版本化、可审计；Policy 发布支持版本、灰度、回滚。

涉及文件：

- `internal/contracts/policy.go`
- `internal/policy/engine`
- `internal/storage/postgres/postgres.go`
- `migrations/001_clean_core_base.sql`
- `internal/server/server.go`
- `docs/openapi.clean-core.v1.json`

任务说明：

- 增加 PolicyDraft / PolicyRelease 模型。
- 增加 `policy.update` 或分阶段 `policy.draft.create` / `policy.draft.validate` / `policy.publish` / `policy.rollback` command。
- 所有策略变更写 Audit。
- 运行时按指定 `policy_set_id + version` 或当前 stable 版本加载。

验收标准：

- 优化人员无需改 Go 代码即可创建策略草稿。
- 策略发布后新 run 使用新版本。
- 旧 run 的 `VersionSnapshot` 保留旧策略版本。
- rollback 后新 run 使用回滚后的策略版本。
- 所有策略变更可在 `/v1/audit` 查到。

### T2：拆分 AgentPackage Draft 外部 API

分类：C 文档要求但实现偏离

优先级：P1

依据：文档要求 Prompt / Skill / ToolBinding 进入 Draft / Proposal / Publish，优化人员可以修改 AGENTS.md / SKILL.md。

涉及文件：

- `internal/agentdef/package/service.go`
- `internal/server/server.go`
- `docs/openapi.clean-core.v1.json`

任务说明：

- 增加 draft create / patch prompt / update tool binding / validate / review / publish commands。
- `agent.package.publish` 支持 `draft_id`。
- 保留当前“一步发布”可作为测试便利命令，但不作为主流程。

验收标准：

- 可以先创建 draft，再修改 prompt，再 validate，再 publish。
- 未 publish 的 draft 不影响当前 run。
- publish 后新 run 可加载新版本。
- 全流程写 Audit。

### T3：补齐 Trace / Audit 证据矩阵

分类：B/C 文档要求但部分实现/实现偏离

优先级：P1

依据：工程规格 19.3 / 19.4 必须 Trace / Audit 清单。

涉及文件：

- `internal/runtime/kernel/coordinator.go`
- `internal/tool/runtime/runtime.go`
- `internal/policy/toolpolicy/evaluator.go`
- `internal/server/server.go`
- `internal/governance/replay/report.go`
- `internal/governance/metrics/metrics.go`

任务说明：

- 补写 `input.received`、`agent.loaded`、`task.created`、`task.loaded`、`capability.retrieved`。
- Tool audit 按结果写 `tool.policy_denied`、`tool.approval_required`、`tool.high_risk_invoked`。
- 外部 tools.invoke 写 `external_tool_call`。
- Replay 报告检查缺失必写点。

验收标准：

- 一次普通 run 的 trace 能覆盖文档必写点。
- 一次被拒绝工具调用能查到 `tool.policy_denied` audit。
- 一次高风险审批能查到 `tool.approval_required` audit。

### T4：扩展 Eval Suite

分类：B 文档要求但部分实现

优先级：P1

依据：文档要求 Eval Suite 支持发布门槛、失败原因、工具轨迹、最终输出。

涉及文件：

- `internal/eval/runner.go`
- `internal/agentdef/package/service.go`
- `internal/server/server.go`
- `migrations/001_clean_core_base.sql`
- `docs/openapi.clean-core.v1.json`

任务说明：

- 增加 EvalSuite / EvalCase 持久化。
- 增加 critical/safety/normal 分类。
- 增加通过率、工具误调用率门槛。
- 增加 eval result 查询接口。
- 将 Policy 变更也接入 Eval。

验收标准：

- stable 发布必须满足配置的 suite 门槛。
- eval 失败能返回失败原因、trace_id、run_id、工具调用摘要。

### T5：外部协作事件映射器

分类：B 文档要求但部分实现

优先级：P2

依据：工程规格 29 要求 Clean Core 回写外部协作系统。

涉及文件：

- `internal/bridge/array`
- `internal/runtime/kernel/coordinator.go`
- `internal/task/runtime/service.go`
- `internal/tool/runtime/runtime.go`
- `internal/task/handoff/service.go`

任务说明：

- 建立 TaskEvent/TraceEvent -> CollaborationProvider 的映射器。
- 覆盖 reply、waiting_input、waiting_approval、artifact.created、handoff.created、run.failed。
- 写回失败时更新 ExternalTaskBinding 状态。

验收标准：

- 有绑定外部任务时，关键状态会调用 SendMessage / AttachArtifact。
- 写回失败会进入 `writeback_failed`，可查询 last_error。

## 9. 最终判断

当前 CleanCore 不是“细节都没做好”，而是已经完成了运行内核和多数模块基础能力；真正还没完全实现的是产品文档中面向“优化人员持续调优”和“策略治理运营”的上层闭环。

换句话说：

```text
核心 Agent Runtime：基本完成
工具治理 / Handoff / 持久化：基本完成
Prompt 人类编辑：模块有，外部流程不完整
Policy 人类编辑：结构和存储有，产品化流程缺失
Trace / Audit：有基础，必写矩阵未完全对齐
Eval / Release：有最小闭环，未达到完整 Suite 级产品能力
外部协作：有 adapter 和 binding，事件回写矩阵未完整接入
```

## 10. 解决进度 v0.2（2026-05-31）

本节记录按上方解决方案逐项修复后的状态。原审计内容保留，用于追溯问题来源；以本节为当前解决进度准。

| 编号 | 当前状态 | 解决证据 | 备注 |
| --- | --- | --- | --- |
| G-01 | 已解决 | `internal/server/server.go` 已暴露 `agent.package.draft.create`、`agent.package.draft.patch_prompt`、`agent.package.tool_binding.update`、`agent.package.draft.validate`、`agent.package.review`；`internal/agentdef/package/service.go` 已提供 tenant-safe draft 操作 | 人类可写 Prompt / ToolBinding 已进入 Draft 流程 |
| G-02 | 已解决 | `agent.package.publish` 已支持 `draft_id` 发布；`PackageService` 保留 validate/review/publish/canary/stable/rollback 分阶段状态 | 一步发布保留为兼容/测试便利入口 |
| G-03 | 已解决 | `policy.draft.create`、`policy.update`、`policy.draft.validate`、`policy.review`、`policy.publish`、`policy.canary`、`policy.stable`、`policy.rollback` 已接入 `server.go`；`PolicyManager` 已提供 Draft/Version 管理 | Policy 管理入口已补齐 |
| G-04 | 已解决 | `PolicyManager` + `PolicyStore` 支持外部命令写入 Policy Draft/Version；运行时继续通过 `LoadPolicySet` 加载 stable 策略 | 优化人员不需要改 Go 代码即可变更 Policy |
| G-05 | 已解决 | 管理类 package/policy/artifact 命令均有角色校验、租户校验和 Audit；policy release 也接入 `EvaluateReleaseAction` | 管理类命令仍走 `/v1/commands` 统一入口 |
| G-06 | 已解决 | `coordinator.go` 已记录 `input.received`、`agent.loaded`、`task.created`、`task.loaded`、`capability.retrieved`；`replay/report.go` 已校验必写点 | existing task 场景允许 `task.loaded` 替代 `task.created` |
| G-07 | 已解决 | `toolpolicy/evaluator.go` 已写 `tool.policy_denied`、`tool.approval_required`、`tool.high_risk_invoked`；`tool/invoke/service.go` 已写 external tool audit | 保留 `tool.policy_checked` 作为兼容事件 |
| G-08 | 已解决 | migration 增加 `policy_drafts`、`policy_versions`；`PolicyStore.SaveVersion` 保留版本历史并在 stable 时更新当前 `policy_sets`；`Rollback` 支持 fallback restore | readiness 已纳入 policy draft/version 检查 |
| G-09 | 已解决 | `eval.Store` 支持 Suite/Case/Result；`eval.suite.create`、`eval.suite.add_case`、`eval.suite.run` 已接入；Postgres 增加 `eval_suites`、`eval_suite_results` | Eval result 可通过 `/v1/evals/results/{eval_run_id}` 查询 |
| G-10 | 已解决 | Suite gate 支持 critical/safety/pass_rate/tool_misuse_rate；`eval.suite.run` 可标记 package/policy eval result；stable 仍要求通过 eval 后才能发布 | release policy 的 canary/stable/rollback 门禁已对 package 与 policy 生效 |
| G-11 | 已解决 | `task.command` 已支持 `create_handoff`、`accept_handoff`、`reject_handoff`、`complete_handoff`、`fail_handoff`；tenant 校验已前置 | cancel 未作为文档明确必需命令单独实现 |
| G-12 | 已解决 | `bridge/array/sync.go` 增加事件映射；runtime/handoff 已回写 reply、waiting_input、waiting_approval、artifact.created、handoff.created、run.failed | 依赖已有 ExternalTaskBinding active 绑定 |
| G-13 | 不处理，非 Clean Core 缺口 | 文档明确 @mention/slash/channel routing 位于 AgentEnvelope 前的外部适配层 | 保持原边界判断 |
| G-14 | 已解决 | `docs/openapi.clean-core.v1.json` 已补齐 command enum、package/policy/eval/artifact payload/result schema、`/v1/agents/capabilities`、`/v1/evals/suites/{suite_id}`、`/v1/evals/results/{eval_run_id}` | `/v1/commands` 仍是管理命令统一入口 |
| G-15 | 已解决 | `internal/discovery/agent/index.go` 和 `/v1/agents/capabilities` 已提供 Agent capability index | 深层 policy filtering 可后续增强，但独立 API 已有 |
| G-16 | 已解决 | `governance/metrics` 已增加 duration、input/agent/task/capability、tool approval wait、handoff completed/failure rate 等指标 | 趋势聚合未做，因为文档未要求独立时序库 |
| G-17 | 已解决 | `MemoryPolicy` / `ArtifactPolicy` 已进入 `PolicySet`；Memory write 支持 scope policy；Artifact read/delete 有 tenant guard、policy guard、audit；`artifact.read`/`artifact.delete` 命令已接入 | delete 需要 reason |
| G-18 | 已解决 | `TaskUpgradePolicy` 已进入 `PolicySet`；`coordinator.go` 会按策略加载目标版本并记录升级 trace | 显式 `upgrade_agent_version` 仍是主控路径 |

### 当前剩余说明

- 未新增 UI 后台，因为文档要求的是 API/command 与治理流程，不是必须有可视化后台。
- 未实现 @mention/slash 原始解析，因为文档将其定义为 Clean Core 外部适配层。
- 已使用工作区临时 Go 工具链 `go1.26.3 windows/amd64` 执行 `gofmt`、`go vet ./...` 与 `go test ./...`，均通过。
