# 原智能体 CleanCore 问题修复进度 v0.1

日期：2026-05-30

来源：

- `docs/原智能体CleanCore十项问题代码对照分析报告_v0.1.md`
- `docs/原智能体CleanCore十三模块架构与代码审查报告_v0.1.md`

## P0：安全边界与事实源

- [x] P0-01 普通 `task.command` 增加 task tenant 校验。
- [x] P0-02 Trace 查询和 TraceEvent 增加 tenant 维度，避免跨租户读取。
- [x] P0-03 ToolCall 增加 tenant 维度，ToolRepository 幂等按 tenant 隔离。
- [x] P0-04 runtime-kernel 内部工具调用传递 TenantID / TraceID。
- [x] P0-05 修复 ToolCall.PlanStepID 在无计划时写入 runtime step id 的语义污染。
- [x] P0-06 Task 状态更新与 TaskEvent 追加的事务一致性设计落地到 repository/service 接口。
- [x] P0-07 Replan 改为纯 append-only，移除 ReplaceEvent 语义。
- [x] P0-08 Artifact 创建写入 TenantID，并增加 artifact tenant guard。
- [x] P0-09 Handoff / tool trace / task timeline / task recovery / task plan 查询增加 tenant guard。
- [x] P0-10 AuditEvent 写入兜底 CreatedAt，并修复 package release/eval/rollback tenant 为空。

## P1：运行闭环正确性

- [x] P1-01 修复 name-only tool call：validator 或 runtime 规范化为 tool_id。
- [x] P1-02 明确并实现 DecisionTypeNoOp 的 runtime/task 状态语义。
- [x] P1-03 增加 model.called / model.completed / decision.created / decision.validated / response.sent trace。
- [x] P1-04 ToolRuntime 增加 tool.policy_checked / tool.invoked / denied / pending_approval trace。
- [x] P1-05 实现 pending approval action 持久化与 approve 后 resume。
- [x] P1-06 Tool arguments 按 InputSchema 校验。
- [x] P1-07 模型失败时同步推进 Task 到 failed。
- [x] P1-08 PlanStep 增加明确状态约束，避免 pending step 直接 complete。

## P2：模块能力补齐

- [x] P2-01 实现 OpenAI-compatible ModelClient。
- [x] P2-02 增加模型配置装配，禁止生产默认使用 StubModelClient。
- [x] P2-03 AgentPackage Compiler 从 server 下沉到 agent-definition 模块。
- [x] P2-04 AgentPackage Compiler 解析/校验真实 package source，并输出带路径错误。
- [x] P2-05 Capability Discovery 增加 Skill/Tool index、版本过滤和 policy filtering。
- [x] P2-06 统一 PolicyEngine，加载完整 PolicySet。
- [x] P2-07 HandoffPolicy 从 PolicySet/AgentPackage 加载，禁止 server 硬编码。
- [x] P2-08 Handoff 完成 target AgentRun 启动与 result backflow。
- [x] P2-09 增加 ExternalTaskBinding 持久模型和外部写回状态机。
- [x] P2-10 MemoryRepository / MemoryPolicy / Memory Audit。
- [x] P2-11 Artifact 内容存储抽象和 ContextPackageStore。
- [x] P2-12 ExecutionDomain 增加 worker/sandbox/managed profile 抽象。

## P3：持久化、契约与上线硬化

- [x] P3-01 Postgres repositories：task/run/tool/trace/audit/artifact/package/handoff/plan。
- [x] P3-02 真实 migration runner 和 database readiness。
- [x] P3-03 OpenAPI / contract snapshot / 兼容性测试。
- [x] P3-04 E2E regression matrix。
- [x] P3-05 Metrics 与 request/trace structured logging。
- [x] P3-06 Go/No-Go 报告接入真实测试、安全、migration、release 门禁。
- [x] P3-07 配置布尔合并语义修复。
- [x] P3-08 server 层业务逻辑下沉到模块 service。

## P4：二轮架构审查硬化

- [x] P4-01 AgentDefinition / StaticLoader 增加 tenant-aware 装载与默认版本隔离，同时保留全局 fallback。
- [x] P4-02 TaskRuntime 使用 StateMachine 的 Transition.Audit 写入状态转换审计。
- [x] P4-03 AgentRun VersionSnapshot 写入配置模型 provider/name、Skill/Tool 版本快照，移除固定 stub 快照。
- [x] P4-04 HTTP handler 增加 panic recovery，避免 handler panic 直接击穿服务进程。
- [x] P4-05 ExternalTaskBinding 从 ArrayBridge 内存状态扩展到可插拔 store，并接入 Postgres。
- [x] P4-06 PolicyDecision 从裸字符串提升为强类型枚举。
- [x] P4-07 WorkView 注入 MemorySummary 和 ArtifactRef 来源，补齐运行上下文视图。
- [x] P4-08 ContextEngine 对 prompt source block 做边界转义，并执行 PromptPolicy / CompressionPolicy。
- [x] P4-09 HandoffContextPackage 按 handoff mode 控制上下文可见 scope。
- [x] P4-10 Handoff 增加 policy_checked / context_packaged / created / completed Trace，并保持 Audit 闭环。
- [x] P4-11 Decision validator 支持规范化输出，并增加可修复 schema error 的 decision repair loop。
- [x] P4-12 TaskPlan 增加前置步骤约束，并在全部步骤完成后追加 `plan.completed` 事件。
- [x] P4-13 Release 运行门禁硬化：published 包进入 canary/stable 前不可被 runtime 精确版本运行，release/eval 操作增加 package tenant guard。
- [x] P4-14 外部 `tools.invoke` 默认幂等键加入 tenant/task/trace/tool/request hash，避免同 trace 不同参数误复用结果。
- [x] P4-15 Governance 增加 trace replay 读模型与 `/v1/traces/{trace_id}/replay` 查询入口。
- [x] P4-16 Eval gate 与 package release 绑定：`eval.run` 指定 package_version_id 时必须评测同一 agent/version。
- [x] P4-17 `agent.run` 支持复用 RuntimeContext.TaskID 指向的已有 task，并增加租户校验，避免总是新建 task。
- [x] P4-18 AgentPackage skill definition / instruction 从 metadata 编译进入 AgentDefinition，并经 Capability Discovery、WorkView、PromptBundle 注入真实技能指令。
- [x] P4-19 AgentPackage release 状态转换硬化，限制 canary/stable/rollback 的合法前置状态，避免 rolled_back/stable 后被随意切换。
- [x] P4-20 Eval pass 状态转换纳入 AgentPackage release 状态机，避免 rolled_back release 被 eval.run 重新复活为 evaluated。
- [x] P4-21 Tool registry 拒绝重复 tool_id 注册，避免静默覆盖既有工具定义与执行器。
- [x] P4-22 HandoffContextPackage 执行 MaxContextTokens 预算，超额时压缩 summary 并裁剪 artifact refs。
- [x] P4-23 ModelRuntime 增加 provider 错误分类，并让 runtime-kernel 只重试 retryable 模型错误。
- [x] P4-24 External task lookup 接入 tenant-aware access decision，已绑定外部任务禁止跨租户读取。
- [x] P4-25 Prompt source block 标签名白名单清洗，防止 metadata/skill_id 注入伪造上下文边界。
- [x] P4-26 External writeback failure 记录要求存在 ExternalTaskBinding，避免缺失绑定时静默吞掉失败。
- [x] P4-27 AgentPackage compiler 校验 skill metadata 的 risk_level 与重复 skill definition，坏包在编译阶段失败。
- [x] P4-28 AgentPackage compiler 校验 runtime limits，拒绝无效运行预算进入发布链路。
- [x] P4-29 Capability Discovery 按 skill_id@version 绑定 SkillInstruction，避免同名不同版本技能注入错配。
- [x] P4-30 WorkView/PromptBundle 注入高风险候选的 RiskMark，模型决策前可见 skill/tool 风险提示。
- [x] P4-31 Release 运行门禁覆盖租户默认版本，`agent.run` 省略 version 时也会校验解析后的 release 状态。
- [x] P4-32 Artifact in-memory store 强制 tenant_id 写入与读取 tenant guard，和 Postgres 行为对齐。
- [x] P4-33 Memory store 强制 tenant_id 写入，内存与 Postgres 均防止 MemoryEvent 事实源出现空租户记录。
- [x] P4-34 PolicySet 缺失兜底改为完整 DefaultPolicySet，避免运行时拿空策略绕过默认策略族。
- [x] P4-35 HandoffContextPackage full/reference/hybrid scope 细化执行 AllowArtifactRead/AllowMemoryRead/AllowTaskEventRead。
- [x] P4-36 HandoffService.Create 增加 parent task tenant guard，模块直接调用也不能跨租户创建 handoff。
- [x] P4-37 ToolRepository 空 idempotency key 不参与幂等索引，内存与 Postgres 均避免未显式给 key 的调用互相误判重。
- [x] P4-38 SQL migration schema 收紧 tool_calls/trace_events tenant_id 为 NOT NULL，并补 tenant 维度查询索引。
- [x] P4-39 AgentPackage compiler 解析并校验 SkillResourceRef，技能资源引用不再在编译产物中丢失。
- [x] P4-40 Release lookup 按发布时间/创建时间选择同 agent/version 的最新 release，避免旧状态干扰运行门禁。
- [x] P4-41 AgentPackage publish 拒绝同 tenant/agent/version 重复发布，版本不可变冲突前置失败。
- [x] P4-42 ReleasePolicy 接入 rollback 动作，默认要求回滚原因并进入发布命令门禁。

## P5：生产级能力块收口

- [x] P5-01 ExecutionDomain 从 local-only/stub 升级为统一 ExecutionRequest/ExecutionResult，支持 JSON/shorthand execution profile、worker/sandbox/managed adapter 边界、资源/网络策略元数据与 ToolRuntime trace 记录。
- [x] P5-02 External Collaboration 从 ArrayBridge 本地 stub 扩展为 binding/tenant/status 状态层 + HTTP CollaborationProvider adapter，支持真实第三方任务读取、访问判定、消息/Artifact 写回与 writeback_failed 状态记录。
- [x] P5-03 Release/Canary Policy 增加 canary 百分比、发布时间窗口、stable 审批、canary-before-stable 与 rollback reason 统一门禁，server release 命令进入 PolicyEngine 判定。
- [x] P5-04 Migration readiness 从 SQL 文本对象检查升级为 live DB schema 校验，覆盖表、tenant NOT NULL 列、关键索引、schema migration 版本与 checksum，并接入 readiness/migration status。
- [x] P5-05 Deployment/Ops 交付 Docker/compose/Helm/CI/备份恢复/Secret 管理 runbook，并修复 TaskPlan 事件列表顺序稳定性，保证全量回归可重复通过。

## P6：产品文档能力缺口闭环

- [x] P6-01 AgentPackage Draft 外部命令补齐：create / patch prompt / update tool binding / validate / review / publish，`agent.package.publish` 支持 `draft_id`。
- [x] P6-02 Policy 管理闭环补齐：Policy Draft / policy.update / validate / review / publish / canary / stable / rollback，Postgres 保留 `policy_versions` 历史并支持 rollback restore。
- [x] P6-03 管理命令租户边界前置：Policy draft validate/review/publish/update、Eval suite add case、handoff complete 均先校验 tenant 再变更。
- [x] P6-04 Trace/Audit 必写矩阵补齐：input.received、agent.loaded、task.created/task.loaded、capability.retrieved、tool.policy_denied、tool.approval_required、tool.high_risk_invoked、external_tool_call、artifact.read/delete。
- [x] P6-05 Eval Suite 产品化：suite/case/result、critical/safety/pass_rate/tool_misuse_rate gate、package/policy eval 标记、Postgres 持久化、结果查询接口。
- [x] P6-06 Handoff 命令与外部协作事件矩阵补齐：create/accept/reject/complete/fail handoff，reply/waiting_input/waiting_approval/artifact.created/handoff.created/run.failed 写回。
- [x] P6-07 Artifact/Memory 策略补齐：MemoryPolicy scope 校验、ArtifactPolicy read/delete 校验、artifact.delete 必填 reason 并写 Audit。
- [x] P6-08 Capability/Metrics/OpenAPI 补齐：Agent capability index API、governance metrics 深化、OpenAPI command enum 与 package/policy/eval/artifact schema、Eval 查询路径。
- [x] P6-09 TaskUpgradePolicy 策略路径落地：运行时按 policy target version 自动加载升级后 AgentDefinition，并记录升级 trace。
- [x] P6-10 文档状态标记：`docs/原智能体CleanCore产品文档能力缺口分析_v0.1.md` 追加 v0.2 解决进度表，G-01 ～ G-18 均有当前状态和代码证据。

## P7：可上线内核七项缺口闭环

- [x] P7-01 K-01 AgentRun / PolicySet 版本钉住：`VersionSnapshot` 增加 `policy_version_id`，runtime 按 run snapshot 加载历史策略版本，长任务升级时显式刷新 snapshot。
- [x] P7-02 K-02 AGENTS.md / SKILL.md Draft 编辑：新增 `patch_agents_md` 与 skill add/update/remove draft 命令，技能进入 metadata -> compiler -> SkillDefinition 链路。
- [x] P7-03 K-03 管理类审批闭环：新增 ApprovalRequest 服务与 `approval.approve/reject` 命令，release approval 必须匹配租户、资源、动作，不能用 `approved:true` 伪造绕过。
- [x] P7-04 K-04 Canary 流量与命中记录：package canary 支持 percent/scope，默认 `agent.run` 可按稳定哈希路由到 canary，并写 `canary.routed` Trace 与 canary hit 记录。
- [x] P7-05 K-05 Eval 结果展示与治理事件：Eval Result 保存 final reply、tool calls/results、artifact refs，并写 `eval.run.started`、`eval.case.completed`、`eval.case.failed`、`eval.summary.created` Trace。
- [x] P7-06 K-06 ExecutionDomain 凭证/数据边界：ExecutionProfile 增加 `credential_scope` 与 `data_boundary`，local domain 拒绝扩大边界，ToolRuntime 记录 `credential.used` Trace。
- [x] P7-07 K-07 Go/No-Go 上线门禁覆盖：E2E matrix 增加 K-01 ～ K-06 专项证据，Go/No-Go 增加对应 capability gates，缺失时 no-go。

## P8：Clean Core 最终对齐缺口闭环

- [x] P8-01 ModelRuntime streaming 合同补齐：`ModelClient.Stream`、`ModelStreamEvent`、`model.delta` 与 `decision.completed` Trace 落地。
- [x] P8-02 AgentPackage Proposal 闭环补齐：create / submit / approve / reject / publish 生命周期、tenant guard、Audit、Postgres `agent_package_proposals` 持久化落地。
- [x] P8-03 OpenAPI 契约同步：补齐 P7/P8 命令枚举、Proposal/Skill/Approval/ExecutionProfile/ModelStreamEvent schema、canary_scope 字段。
- [x] P8-04 External writeback 治理可观测性补齐：外部协作写回成功/失败写入 `external.writeback_succeeded` / `external.writeback_failed` Trace；失败写入 `external.writeback_failed` Audit；Governance metrics 增加 external writeback 总量、失败量和失败率；外部 binding 保持 `writeback_failed` 状态与 `last_error`。

## 最新验证

- 2026-05-30：完成 P0-01 ～ P0-10；`go test ./...` 通过。
- 2026-05-30：完成 P1-01 ～ P1-08；`go test ./...` 通过。
- 2026-05-30：完成 P2-01 ～ P2-04；`go test ./...` 通过。
- 2026-05-30：完成 P2-05 ～ P2-08；`go test ./...` 通过。
- 2026-05-30：完成 P2-09 ～ P2-12；`go test ./...` 通过。
- 2026-05-30：完成 P3-01 ～ P3-08；Postgres repository、真实 migration executor/database readiness、OpenAPI snapshot、E2E matrix、metrics/logging、Go/No-Go gates、配置布尔语义、server 业务下沉均已落地；`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P4-01 ～ P4-07；tenant-aware loader、状态转换审计、VersionSnapshot、panic recovery、ExternalTaskBinding Postgres store、PolicyDecision 强类型、WorkView 上下文补齐均已落地；`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P4-08 ～ P4-13；prompt 注入防护/压缩策略、handoff context scope、handoff trace/audit、decision repair、plan 完成事件、release 运行门禁与租户校验均已落地；`gofmt -l` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P4-14 ～ P4-15；外部工具幂等键请求指纹和 trace replay 治理视图均已落地；`gofmt -l` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P4-16；eval gate 与 package_version_id 对应 release 的 agent/version 强绑定；`gofmt -l` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P4-17；`agent.run` 带已有 task_id 时启动该 task 而非新建 task；`go test ./internal/runtime/kernel` 通过。
- 2026-05-30：完成 P4-18；AgentPackage 技能定义/指令进入 discovery 与 prompt bundle；`go test ./internal/agentdef/package ./internal/discovery/tool ./internal/context/promptbundle ./internal/runtime/kernel` 通过。
- 2026-05-30：完成 P4-19；AgentPackage release canary/stable/rollback 状态转换收紧；`go test ./internal/agentdef/package ./internal/server` 通过。
- 2026-05-30：完成 P4-20；eval pass 复用 release 状态机校验，rollback 后再次 eval 不能恢复运行态；`go test ./internal/agentdef/package` 通过。
- 2026-05-30：完成 P4-21；Tool registry duplicate tool_id 防覆盖保护落地；`go test ./internal/tool/registry` 通过。
- 2026-05-30：完成 P4-22；HandoffContextPackage max_context_tokens 由说明性约束变为实际预算裁剪；`go test ./internal/context/handoffpkg` 通过。
- 2026-05-30：完成 P4-23；OpenAI-compatible client 区分认证、限流、超时、上下文过长、5xx 等 provider 错误，runtime-kernel 尊重 Retryable；`go test ./internal/model/client ./internal/runtime/kernel` 通过。
- 2026-05-30：完成 P4-24；ExternalTaskBinding 被 ArrayBridge access check 用于租户隔离，外部任务查询入口先做访问判定；`go test ./internal/bridge/array ./internal/server` 通过。
- 2026-05-30：完成 P4-25；PromptBundle sourceBlock 对 source label 和 content 同时做边界防护；`go test ./internal/context/promptbundle` 通过。
- 2026-05-30：完成 P4-26；ArrayBridge writeback_failed 对 missing binding 返回 not found；`go test ./internal/bridge/array` 通过。
- 2026-05-30：完成 P4-27；AgentPackage skill metadata 增加 schema 级校验与路径化错误；`go test ./internal/agentdef/package` 通过。
- 2026-05-30：完成 P4-28；AgentPackage runtime limits 增加编译期边界校验；`go test ./internal/agentdef/package` 通过。
- 2026-05-30：完成 P4-29；SkillInstruction discovery 从 skill_id key 收紧为 skill_id@version key；`go test ./internal/discovery/tool` 通过。
- 2026-05-30：完成 P4-30；Capability candidate high/critical 风险标记进入 WorkView 与 PromptBundle；`go test ./internal/context/workview ./internal/context/promptbundle` 通过。
- 2026-05-30：完成 P4-31；runtime release gate 覆盖 tenant default version，rolled_back 默认版本不可绕过；`go test ./internal/server` 通过。
- 2026-05-30：完成 P4-32；Artifact memory store 空租户写入/读取与跨租户读取均被拒绝；`go test ./internal/asset/artifact ./internal/tool/runtime` 通过。
- 2026-05-30：完成 P4-33；Memory write 缺 tenant_id 直接失败并保持跨租户读取拒绝；`go test ./internal/asset/artifact ./internal/storage/postgres` 通过。
- 2026-05-30：完成 P4-34；runtime-kernel、external tools.invoke 与 Core LoadPolicySet 缺失策略时使用完整默认策略；`go test ./internal/policy/engine ./internal/runtime/kernel ./internal/tool/invoke ./internal/app/core` 通过。
- 2026-05-30：完成 P4-35；Handoff context package 所有 mode 都执行细粒度上下文读取开关；`go test ./internal/context/handoffpkg ./internal/task/handoff` 通过。
- 2026-05-30：完成 P4-36；HandoffService 在创建前校验 parent task tenant；`go test ./internal/task/handoff ./internal/server` 通过。
- 2026-05-30：完成 P4-37；ToolRepository empty idempotency key 跳过 dedupe，Postgres 使用 tool_call_id 作为一次性存储 key；`go test ./internal/tool/repository ./internal/tool/invoke ./internal/runtime/kernel ./internal/storage/postgres` 通过。
- 2026-05-30：完成 P4-38；base migration 与 readiness required objects 同步 tenant schema/index 硬化；`go test ./internal/storage/migration ./internal/readiness ./internal/release` 通过。
- 2026-05-30：完成 P4-39；AgentPackage skill resources metadata 编译进入 SkillDefinition.Resources；`go test ./internal/agentdef/package ./internal/discovery/tool` 通过。
- 2026-05-30：完成 P4-40；releaseForAgentVersion 选择最新 release 状态；`go test ./internal/server` 通过。
- 2026-05-30：完成 P4-41；PublishDraft 增加同 tenant/agent/version 冲突检测；`go test ./internal/agentdef/package ./internal/server` 通过。
- 2026-05-30：完成 P4-42；PolicySet 增加 ReleasePolicy，server rollback 执行 require_rollback_reason；`go test ./internal/policy/engine ./internal/server ./internal/contracts` 通过。
- 2026-05-30：完成二轮追加硬化 P4-20 ～ P4-42 全量回归；`gofmt -l cmd internal pkg` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-30：完成 P5-01；ExecutionDomain profile/adapters 与 ToolRuntime execution metadata trace 落地；`go test ./internal/execution/domain ./internal/tool/runtime` 通过。
- 2026-05-30：完成 P5-02；ArrayBridge HTTP adapter、配置装配与失败写回状态闭环落地；`go test ./internal/bridge/array ./internal/app/config ./internal/app/core ./internal/contracts` 通过。
- 2026-05-30：完成 P5-03；ReleasePolicy 窗口/比例/审批/回滚原因门禁落地；`go test ./internal/policy/engine ./internal/server ./internal/contracts` 通过。
- 2026-05-30：完成 P5-04；live DB schema readiness 与 migration status 校验落地；`go test ./internal/storage/migration ./internal/readiness ./cmd/clean-core-server` 通过。
- 2026-05-30：完成 P5-05；Dockerfile、docker-compose、GitHub Actions、Helm chart、ops runbook 与 TaskPlan 事件顺序稳定性落地；`gofmt -l cmd internal pkg` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 2026-05-31：完成 P6-01 ～ P6-10；使用工作区临时 Go 工具链 `go1.26.3 windows/amd64` 执行 `gofmt`、`go vet ./...` 与 `go test ./...`，均通过。
- 2026-05-31：完成 P7-01 ～ P7-07；使用本机 Go 工具链 `go1.23.3 windows/amd64` 执行 `gofmt`、`go vet ./...` 与 `go test ./...`，均通过。
- 2026-05-31：完成 P8-01 ～ P8-03；本机 PATH 暂无 Go 工具链，改用 Docker 执行格式化/测试验证。
- 2026-05-31：完成 P8-04；使用本机 Go toolchain `go1.24.0 windows/amd64` 执行 `gofmt`、`go vet ./...` 与 `go test ./...`，均通过。
