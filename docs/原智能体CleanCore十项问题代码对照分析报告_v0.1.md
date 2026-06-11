# 原智能体 CleanCore 十项问题代码对照分析报告 v0.1

日期：2026-05-30

## 1. 分析范围

本报告依据以下开发文档与当前代码实现进行对照：

- `docs/原智能体CleanCore开发任务拆解与交付计划_v1.2-alpha.md`
- `docs/原智能体CleanCore接口冻结与受控变更开发文档_v1.0-alpha.md`
- `docs/原智能体CleanCore工程实施规格文档_v1.0.md`
- `docs/原智能体全景架构详细设计文档_v0.8_Clean_Core_Managed_Execution.md`
- `docs/原智能体CleanCore全景开发设计文档_v1.2_Clean.md`

本报告重点核验此前归纳的十条问题是否真实存在、是否覆盖充分，并补充从代码对照中发现的额外问题。

验证命令：

```powershell
.\.tools\go1.26.3\go\bin\go.exe test ./...
```

验证结果：通过。当前工程具备可编译、可运行、可测试的 Alpha 骨架，但距离开发文档定义的生产级 CleanCore 闭环仍有明显差距。

## 2. 总体结论

此前十条问题全部成立。

需要强调的是：这些问题并不都代表当前代码“错误”，其中相当一部分是 Alpha 阶段有意采用的内存实现、stub 实现或骨架实现。但如果以开发文档中定义的 CleanCore 目标为准，它们确实都是未完成项或交付风险。

十条问题并不全面。代码对照后还发现了若干更靠近生产阻断的风险，尤其是租户隔离、审批恢复、强制 Trace/Audit 点、配置合并、幂等隔离和接口契约冻结等问题。这些问题应与原十条一并纳入后续开发计划。

当前状态可以概括为：

- Alpha 主链路：已具备。
- 内存态可运行闭环：已具备。
- 文档要求的持久化、治理、隔离、真实模型、真实 AgentPackage、外部协作和发布运维闭环：未完成。
- 生产可用性：尚未达到。

## 3. 十条问题逐项核验

### 3.1 问题一：Postgres 持久化实现缺失

结论：成立。

代码证据：

- `internal/app/core/core.go` 中核心依赖默认使用 `NewInMemoryRepository`、`NewInMemoryTaskRepository`、`NewInMemoryEventRepository`、`taskplan.NewInMemoryRepository`、`trace.NewInMemoryRecorder`、`audit.NewInMemoryLogger`、`artifact.NewInMemoryStore`、`toolrepo.NewInMemoryRepository` 等内存实现。
- `internal/app/config/config.go` 中虽然存在 `DatabaseURL` 配置，但核心装配未使用该配置切换到 Postgres repository。
- `internal/storage/migration` 目前具备 SQL schema 文本与 readiness 检查能力，但未看到真实数据库连接、迁移执行、事务化 repository 或回滚链路。

文档对照：

- 任务文档 B1.6、B1.7 要求 TraceRecorder、AuditLogger 与数据库表落地。
- 工程规格文档要求 PostgreSQL schema、Repository 与迁移版本具备可执行交付。

风险：

- 服务重启后任务、运行、工具调用、审计、Trace、Artifact、Package 发布状态全部丢失。
- 无法满足审计追溯、恢复、回放、幂等和多实例部署要求。
- Readiness 即使通过，也不能证明真实数据库链路可用。

建议：

- 增加 Postgres repository 实现并由配置装配。
- 将 migration runner 从 schema 文本检查升级为真实迁移执行。
- 为 task/run/tool/trace/audit/artifact/package/handoff/plan 增加数据库集成测试。

### 3.2 问题二：真实模型客户端缺失

结论：成立。

代码证据：

- `internal/model/client/client.go` 中 `StubModelClient` 默认返回固定 `ok` 回复。
- `OpenAICompatibleClient.Complete` 当前直接返回 `openai-compatible client skeleton is not configured for network calls yet`。
- `internal/app/core/core.go` 默认注入 `modelclient.StubModelClient{}`。

文档对照：

- 任务文档 B2.6 要求 ModelClient、ModelErrorClassifier、模型调用 trace、重试与错误分类。
- 全景设计文档要求 AgentRun 能基于模型决策产生下一步行动，而不是固定 stub 输出。

风险：

- 不能验证真实提示词、模型返回格式、工具调用决策、错误分类和重试策略。
- Eval Gate 当前只能验证静态/脚本化行为，不能代表真实模型能力。
- Trace 中缺少真实模型 provider/model/request/response/error 证据。

建议：

- 实现可配置的 OpenAI-compatible HTTP client。
- 增加模型错误分类、超时、重试、响应裁剪与脱敏 trace。
- 保留 Stub/Scripted client 作为测试专用实现，生产装配不得默认使用 stub。

### 3.3 问题三：AgentPackage 编译器缺失

结论：成立。

代码证据：

- `internal/server/server.go` 的 package publish 路径调用 `compilePackageDefinition`。
- 当前 `compilePackageDefinition` 以 `agentloader.TestAgentDefinition()` 为基础构造定义，不解析真实 `AGENTS.md`、`agent.yaml`、`SKILL.md`、`skill.yaml`。
- `internal/agentdef/package` 已有 draft/publish/canary/stable/rollback 的骨架，但 package 内容编译仍是简化实现。

文档对照：

- 任务文档 B4.2 要求 AgentPackage 编译器解析 Agent 与 Skill 文件，输出 AgentDefinition、SkillCard、ToolCard、PolicyFragment、EvalSpec、ArtifactSchema 等，并返回带文件路径的错误。
- 接口冻结文档要求发布对象可版本化、可验证、可回滚。

风险：

- 发布 API 看似可用，但无法承载真实 Agent 包。
- Canary/stable/release switch 建立在测试定义上，无法作为真实发布治理依据。
- 编译错误不可定位到具体文件/字段，开发体验和发布安全都不足。

建议：

- 实现真实 PackageSource 解析与编译。
- 定义严格 schema 校验和跨文件引用校验。
- 将 eval gate 与编译产物绑定，禁止未通过编译和评测的包进入 stable。

### 3.4 问题四：真实能力发现缺失

结论：成立，且当前仅有静态 provider 的部分骨架。

代码证据：

- `internal/discovery/tool` 中已有静态能力发现实现。
- WorkView/PromptBundle 已有基础构建能力，但没有看到文档要求的 SkillCardIndex、ToolCardIndex、目标召回、策略过滤、候选集排序和 CandidateSet 产物闭环。
- Handoff 创建路径没有基于 capability discovery 选择目标 Agent，而是由请求直接提供目标。

文档对照：

- 任务文档 B4.3 要求 Skill/Tool 索引、objective-based skill recall、skill-to-tool expansion、policy filtering 和 WorkView CandidateSet。
- B5.4 要求 delegate 时基于 Capability Discovery 选择 target_agent。

风险：

- Agent 不能真正根据目标选择技能、工具和委托对象。
- prompt 中候选工具/技能不具备可解释的召回来源。
- 后续策略过滤、审计和 eval 难以判断“为什么选择了这个能力”。

建议：

- 建立 SkillCardIndex/ToolCardIndex。
- 在 WorkView 中输出候选能力集合、过滤原因和排序分数。
- Handoff 与 Tool Runtime 共用能力发现结果，避免直接信任输入目标。

### 3.5 问题五：ExecutionDomain 适配器缺失

结论：成立。

代码证据：

- `internal/execution/domain/domain.go` 目前只有 `LocalExecutionDomain` 与 `Resolver`。
- 未看到 Worker、Sandbox、Managed Execution、资源限额、隔离执行、外部任务执行域等实现。

文档对照：

- 全景架构文档强调 Clean Core + Managed Execution，要求执行域边界清晰。
- 工程规格文档要求任务、工具、外部协作在受控执行域中运行。

风险：

- 所有执行都退化为本地内存逻辑，无法表达生产环境中的 worker/sandbox/tenant execution。
- 工具运行、外部协作和模型调用没有统一的资源隔离与失败隔离。
- 后续多实例、限流、排队和恢复难以落地。

建议：

- 定义 ExecutionDomain 接口族：local、worker、sandbox、external。
- 为工具调用和 agent run 增加 domain selection 与资源预算。
- 将执行域信息写入 RunSnapshot、Trace 和 Audit。

### 3.6 问题六：PolicyEngine 深层能力缺失

结论：成立，当前只有局部策略检查。

代码证据：

- `internal/policy/toolpolicy/evaluator.go` 已实现工具 allow/deny、visibility、risk threshold 和 approval required。
- `internal/policy/handoff/evaluator.go` 已实现 handoff 模式、artifact read、敏感 artifact approval 的基础规则。
- 未看到统一 PolicyEngine、策略版本合并、Prompt/Repair/Release/RateLimit/Permission 等策略族。
- 内部 runtime 调用工具时，部分请求中 TenantID/TraceID 为空，导致策略审计上下文不足。

文档对照：

- 开发文档要求 ToolPolicy、HandoffPolicy、Package Release Policy、Governance Policy 与 Trace/Audit 联动。
- 架构文档强调 Policy 是 Core 内强约束，而不是零散 if 判断。

风险：

- 策略能力分散，难以证明每条敏感路径都被治理。
- 策略版本无法完整进入 VersionSnapshot，后续回放和责任追踪不足。
- 高风险审批、发布审批和委托审批无法共享一致的状态机。

建议：

- 抽象统一 PolicyEngine 和 PolicyDecision。
- 所有工具、委托、发布、外部写回、模型调用前后都产生可追踪 policy decision。
- 将 policy_version、decision_id、approval_id 写入 Trace/Audit/RunSnapshot。

### 3.7 问题七：外部协作真实集成缺失

结论：成立，当前只有 ArrayBridge stub。

代码证据：

- `internal/bridge/array/stub.go` 是内存 stub，实现 access、message、artifact 的简化记录。
- `internal/server/server.go` 已暴露 external task lookup 与 external tool invoke 等端点，但没有真实第三方系统适配。
- 未看到真实 ExternalTaskBinding 的持久化、消息同步、附件到 ArtifactRef、外部状态回写和重试补偿。

文档对照：

- 任务文档 B5.5 要求 ExternalTaskBinding、ArrayBridge stub、task.message + @agent 到 AgentEnvelope、附件转 ArtifactRef，以及 reply/waiting_input/artifact.created 外部写回。

风险：

- 外部协作只能在单进程内模拟，无法恢复、重放或跨服务协调。
- 外部消息与内部任务/Artifact 的绑定关系不可持久追踪。
- 写回失败、重复写回、外部任务状态变化都缺少处理。

建议：

- 将 ExternalTaskBinding 持久化。
- 明确外部消息入站、内部任务创建、Agent 运行、结果写回的状态机。
- 为每个外部 provider 定义 adapter contract、幂等键和重试策略。

### 3.8 问题八：Handoff 完整闭环缺失

结论：成立，当前实现了创建上下文包和子任务，但没有完整执行闭环。

代码证据：

- `internal/task/handoff/service.go` 可创建 `HandoffContextPackage` 与 child task。
- 当前只有 `CreateHandoff` 与 `CompleteHandoff` 级别的简化状态，未看到 target AgentRun 自动启动、结果回流、失败恢复、取消、拒绝、超时和 trace/audit 完整记录。
- `internal/server/server.go` 的 delegate 路径使用固定 HandoffPolicy，目标由请求传入，不经能力发现。

文档对照：

- 任务文档 B5.4 要求 origin.agent.delegate 完成目标选择、策略校验、HandoffContextPackage、ChildTask、目标 AgentRun、结果回流。
- 全景架构文档要求 AgentHandoff 进入 Policy、Trace、Audit 管控。

风险：

- 委托只完成“建子任务”，没有完成“被委托 Agent 执行并回传结果”。
- 父子任务状态不一致时缺少恢复机制。
- 委托链路不可完整审计。

建议：

- 增加 Handoff state machine：requested、approved、child_created、running、completed、failed、cancelled、reconciled。
- 创建 child task 后自动启动目标 AgentRun，或明确进入等待调度队列。
- 完成 result backflow，并将父子 trace 串联。

### 3.9 问题九：完整 E2E 回归矩阵缺失

结论：成立。

代码证据：

- 当前包级单测覆盖了 contracts、runtime、server、eval、policy 等局部能力。
- 未看到覆盖文档 B5/B6 端到端场景的完整测试矩阵，例如真实 package 发布、canary/stable 切换、handoff 子任务执行、外部协作写回、审批恢复、租户隔离、迁移升级回滚等。

文档对照：

- 任务文档 B5.6、B6.8 明确要求 E2E Regression Matrix。
- B6.11 Go/No-Go 要求以回归矩阵作为上线判据。

风险：

- 单元测试通过不能证明主业务链路完整。
- 后续补齐 Postgres、真实模型、外部协作时容易出现跨模块回归。
- Go/No-Go 缺乏可量化证据。

建议：

- 建立 `tests/e2e` 或等价目录。
- 覆盖 task start -> model -> tool -> artifact -> trace/audit -> recovery。
- 覆盖 package publish -> eval gate -> canary -> stable -> rollback。
- 覆盖 tenant isolation、approval resume、handoff、external collaboration、migration readiness。

### 3.10 问题十：部署和运维硬化缺失

结论：成立。

代码证据：

- 已有基本 server、config、readiness、release endpoints。
- 未看到 Dockerfile/compose/helm/systemd、生产配置模板、metrics、structured logs with trace_id、CI pipeline、migration job、secret 管理、备份恢复策略。
- `internal/app/config/config.go` 的配置合并仍存在布尔值覆盖限制，说明生产配置层还未硬化。

文档对照：

- 任务文档 B6.9 要求 deployment unit。
- B6.10、B6.11 要求 release/rollback 与 Go/No-Go。
- 工程规格文档要求可部署、可观测、可验证。

风险：

- 无法稳定部署到目标环境。
- 运行异常难以定位和回放。
- 发布状态、迁移状态和 readiness 状态不能支撑上线决策。

建议：

- 增加部署单元、环境配置模板、健康检查说明。
- 增加 metrics、request/trace structured logging、panic recovery。
- 将 Go/No-Go 报告绑定 E2E、migration、security、release switch 状态。

## 4. 额外发现的问题

原十条问题方向正确，但仍不全面。以下问题在代码对照中同样需要纳入开发计划。

### 4.1 租户隔离不完整

结论：新增高优先级问题。

代码证据：

- 多个查询端点按 ID 查询 trace、task recovery、timeline、tool trace、handoff trace、external task 时，缺少对返回资源 `TenantID` 与调用方租户的统一校验。
- `task.command` 的 plan 分支有 task tenant 校验，但非 plan 命令路径直接调用 `TaskRuntime.ApplyCommand`。
- 内存 repository 多数以 ID 为主键，缺少 tenant-scoped key。

风险：

- 多租户场景下存在越权读取或操作其他租户资源的风险。
- 审计记录即使存在，也可能记录的是越权后的事实。

建议：

- 所有 resource read/write 必须统一走 tenant guard。
- Repository 接口建议显式包含 tenant 维度，例如 `GetTask(ctx, tenantID, taskID)`。
- 增加跨租户访问拒绝的 E2E 测试。

### 4.2 审批恢复链路不完整

结论：新增高优先级问题。

代码证据：

- 高风险工具策略可以返回 approval required。
- `TaskRuntime.ApplyCommand` 支持 `approve_action`/`reject_action` 这类状态命令。
- 但当前没有看到“审批通过后恢复原 pending ToolCall 并继续执行”的完整 runtime loop。

风险：

- 任务可以进入等待审批状态，但审批后不一定能恢复到原始执行点。
- 高风险工具调用的幂等、结果写入和 trace 串联会断裂。

建议：

- 持久化 PendingAction/PendingToolCall。
- 审批通过后由 runtime recovery 恢复执行。
- 审批、恢复、工具执行结果必须共享同一个 trace/run/task 上下文。

### 4.3 强制 Trace/Audit 点不完整

结论：新增高优先级问题。

代码证据：

- Runtime 已记录部分 run/workview/prompt/tool 事件。
- 模型调用前后、decision 创建/验证、handoff 创建/执行/回流、package 发布切换、approval resume 等关键点未形成统一强制事件。
- 内部工具调用存在 TenantID/TraceID 为空的情况，影响策略审计质量。

风险：

- 关键决策无法完整回放。
- 审计链路不能证明“谁、在什么策略版本下、为什么允许执行”。

建议：

- 制定 mandatory trace/audit event matrix。
- 所有 core decision 都必须有 trace event、audit event 或明确的豁免说明。
- 在测试中断言关键事件存在。

### 4.4 配置合并存在布尔值覆盖问题

结论：新增中优先级问题。

代码证据：

- `internal/app/config/config.go` 中 `merge` 对布尔值采用 `override || base`。
- 这会导致文件配置无法将默认 true 的字段覆盖为 false，也无法将 base 中 true 的布尔开关清除。
- 环境变量路径可以显式设置，但文件配置语义不完整。

风险：

- 生产配置与预期不一致，例如 readiness、disable 开关无法按配置文件准确关闭。
- 排障困难，因为读取到的最终配置可能不是文件声明语义。

建议：

- 将布尔配置改为 pointer 或显式 `Set` 标记。
- 增加配置合并单测，覆盖 true -> false、false -> true、未设置三种情况。

### 4.5 接口冻结与契约快照不足

结论：新增中高优先级问题。

代码证据：

- 已有 contracts 包和 HTTP endpoints。
- 未看到 OpenAPI/JSON Schema 输出、版本化 contract snapshot、兼容性测试或受控变更检查。

文档对照：

- 接口冻结与受控变更文档要求接口冻结和变更受控。

风险：

- API 行为可能在后续开发中无意破坏。
- 前后端、外部系统或 SDK 难以锁定稳定契约。

建议：

- 生成 OpenAPI 或等价接口契约。
- 将 contract snapshot 纳入测试。
- 接口变更必须显式记录兼容性等级和迁移说明。

### 4.6 幂等键和租户作用域不足

结论：新增中高优先级问题。

代码证据：

- 外部工具调用默认 idempotency key 为 `traceID:toolID`。
- 工具 repository 的幂等结果查询未体现 tenant scope。
- 如果 traceID 缺失或复用，存在跨请求碰撞风险。

风险：

- 可能错误复用其他调用结果。
- 多租户或重试场景下产生难以定位的数据污染。

建议：

- 幂等键至少包含 tenantID、taskID/runID、traceID、toolID 和 request hash。
- Repository 层用 tenant-scoped unique key。
- 增加重复请求与跨租户幂等测试。

### 4.7 Migration readiness 过浅

结论：新增中优先级问题。

代码证据：

- migration readiness 当前主要检查 schema 文本对象是否存在。
- 未连接真实数据库检查当前迁移版本、缺失对象、索引、约束和回滚状态。

风险：

- readiness 通过不能代表数据库真的处于可运行状态。

建议：

- 增加 `schema_migrations` 表和迁移版本检查。
- readiness 连接数据库执行只读校验。
- E2E 覆盖空库初始化、旧版本升级和失败回滚。

### 4.8 Go/No-Go 报告缺少真实门禁信号

结论：新增中优先级问题。

代码证据：

- readiness/release endpoints 已有基本结构。
- 但 Go/No-Go 尚未绑定真实 E2E、租户隔离、安全、迁移、发布回滚和外部协作状态。

风险：

- 上线判定可能只反映“进程还活着”，不能反映“系统可安全发布”。

建议：

- 定义上线门禁信号：unit、e2e、migration、security、release、observability。
- Go/No-Go 报告必须输出 pass/fail/blocker。

### 4.9 可观测性不足

结论：新增中优先级问题。

代码证据：

- 已有 trace recorder 与 audit logger 的内存实现。
- 未看到 metrics、请求级 trace_id 日志串联、结构化错误码统计、慢请求/慢工具调用统计。

风险：

- 生产问题难以定位。
- 无法监控模型失败率、工具失败率、审批阻塞时间、handoff 耗时等核心指标。

建议：

- 增加 metrics endpoint 或 exporter。
- HTTP middleware 注入 request_id/trace_id/tenant_id 到日志。
- 对模型、工具、handoff、external bridge 增加 latency/error metrics。

### 4.10 VersionSnapshot 内容不完整

结论：新增中优先级问题。

代码证据：

- Runtime 中已有 `VersionSnapshot` 概念。
- 模型 provider/name 存在硬编码 stub 倾向，policy/package/model/tool/schema 版本未形成完整真实快照。

风险：

- 后续无法准确回放一次 AgentRun。
- Canary/rollback 后难以解释某次运行到底使用了哪个包、策略、模型和工具版本。

建议：

- 每次 run 固化 package_version、agent_version、model_provider/model_name/model_config_version、policy_version、tool_registry_version、schema_version。
- 将 VersionSnapshot 写入 RunSnapshot 与 Trace。

## 5. 优先级建议

### P0：生产阻断项

这些问题会直接影响安全、数据可靠性或主链路闭环，建议优先解决：

- 租户隔离不完整。
- Postgres 持久化 repository 缺失。
- 真实模型客户端缺失。
- 审批恢复链路不完整。
- 强制 Trace/Audit 点不完整。
- Auth/Role/Policy 在敏感操作上的一致性不足。
- Migration 真实执行与数据库 readiness 缺失。

### P1：核心能力完整性

这些问题决定 CleanCore 是否达到文档定义的功能闭环：

- AgentPackage 编译器。
- Capability Discovery。
- Handoff 完整闭环。
- External Collaboration 真实绑定与写回。
- E2E 回归矩阵。
- VersionSnapshot 完整化。

### P2：发布与运维硬化

这些问题决定是否具备稳定交付和长期维护能力：

- 部署单元与生产配置模板。
- OpenAPI/契约快照。
- Metrics 与结构化日志。
- Go/No-Go 报告门禁。
- 配置合并语义修复。

## 6. 建议实施顺序

建议按以下顺序推进，避免后续返工：

1. 先修安全边界：租户隔离、auth role 校验、敏感 endpoint 统一 guard。
2. 补持久化底座：Postgres repository、migration runner、database readiness。
3. 补强治理证据：mandatory trace/audit matrix、VersionSnapshot 完整化。
4. 打通真实运行：OpenAI-compatible model client、模型错误分类、approval resume。
5. 完成 AgentPackage：真实编译、schema 校验、eval gate 与发布绑定。
6. 完成能力选择：Skill/Tool index、CandidateSet、policy filtering。
7. 完成协作闭环：handoff child run/result backflow、external task binding/writeback。
8. 建立 E2E 矩阵：将上述主链路纳入自动化回归。
9. 完成部署运维：Docker/compose 或目标部署单元、metrics、日志、Go/No-Go。

## 7. 最终判断

此前十条问题是真实存在的，并且基本覆盖了当前 Alpha 实现与开发文档目标之间的主要差距。

但十条问题还不够全面。若以“能否进入生产级 CleanCore 闭环”为标准，必须额外纳入租户隔离、审批恢复、Trace/Audit 强制点、接口契约冻结、配置语义、幂等作用域、真实 migration readiness、Go/No-Go 门禁和可观测性问题。

当前项目适合作为下一阶段开发的 Alpha 基线，不适合作为生产交付版本。下一阶段应先补 P0 阻断项，再继续补齐 AgentPackage、Capability Discovery、Handoff、External Collaboration 与 E2E 矩阵。
