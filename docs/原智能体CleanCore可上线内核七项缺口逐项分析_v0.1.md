# 原智能体 CleanCore 可上线内核七项缺口逐项分析 v0.1

日期：2026-05-31

依据：

- `docs/原智能体CleanCore全景开发设计文档_v1.2_Clean.md`
- `docs/原智能体CleanCore问题修复进度_v0.1.md`
- `docs/原智能体CleanCore产品文档能力缺口分析_v0.1.md`
- 当前代码目录：`internal/`、`cmd/`、`pkg/`、`docs/openapi.clean-core.v1.json`

## 1. 目标和边界

本文件只围绕“可上线的 CleanCore 内核产品”审查 7 项剩余能力：

1. AgentRun / PolicySet 版本钉住是否真正执行。
2. AGENTS.md / SKILL.md 人类可编辑 Draft 能力是否完整。
3. 管理类工具审批闭环是否达到产品文档要求。
4. Canary 是否具备真实灰度流量与命中记录能力。
5. Eval 结果是否能支撑优化人员和发布门禁。
6. ExecutionDomain 是否具备凭证范围和数据边界控制。
7. 上线门禁是否覆盖上述能力，避免回归。

说明：

- `docs/原智能体CleanCore问题修复进度_v0.1.md` 中 G-01 到 G-18 已标记为解决。
- 本文不是否定此前修复，而是按“可上线内核产品”目标继续细化验收颗粒度。
- UI 后台、@mention/slash 原始解析、Group/TaskPool/消息通知不纳入本文缺口，因为全景文档未要求它们进入 Clean Core 内核。

## 2. 总体结论

当前 CleanCore 已具备运行内核、任务状态、工具调用、策略基础、Eval Suite 基础、Handoff、Trace/Audit、Postgres migration、OpenAPI 和 Go/No-Go 基础能力。

但按全景设计文档 v1.2 的上线目标，仍存在 6 个产品能力缺口和 1 个上线验收缺口。它们主要集中在：

- 运行中版本钉住的强约束还不够硬。
- 人类可编辑 AgentPackage 的资源颗粒度不完整。
- 管理类变更审批没有形成统一 waiting_approval 闭环。
- Canary 还是发布状态，不是真正灰度流量路由。
- Eval 结果能判断通过/失败，但不能完整展示最终输出和工具轨迹。
- ExecutionDomain 缺少凭证范围和数据边界实体。
- Go/No-Go 门禁没有显式覆盖上述 6 项。

## 3. 七项总览

| 编号 | 项目 | 当前状态 | 缺口类型 | 上线影响 | 优先级 | 分析状态 |
| --- | --- | --- | --- | --- | --- | --- |
| K-01 | AgentRun / PolicySet 版本钉住 | 部分实现 | 文档要求但部分实现 | 运行可回放、可审计可信度不足 | P0 | 已完成分析 |
| K-02 | AGENTS.md / SKILL.md Draft 编辑 | 部分实现 | 文档要求但部分实现 | 优化人员持续调优闭环不完整 | P1 | 已完成分析 |
| K-03 | 管理类工具审批闭环 | 部分实现 | 文档要求但部分实现 | 高风险变更可能绕过标准 approval 流程 | P0 | 已完成分析 |
| K-04 | Canary 流量与命中记录 | 部分实现 | 文档要求但部分实现 | 灰度发布不可审计、不可复盘 | P1 | 已完成分析 |
| K-05 | Eval 结果展示与治理事件 | 部分实现 | 文档要求但部分实现 | Eval 可用但不够支撑优化交接和发布复盘 | P1 | 已完成分析 |
| K-06 | ExecutionDomain 凭证/数据边界 | 部分实现 | 文档要求但部分实现 | 工具执行域安全边界不足 | P1 | 已完成分析 |
| K-07 | Go/No-Go 上线门禁覆盖 | 部分实现 | 工程风险，不一定违反单项功能文档 | 修复后缺少防回退机制 | P1 | 已完成分析 |

## 4. K-01：AgentRun / PolicySet 版本钉住

### 分析结论

部分实现。代码已经记录 `PolicySetVersion`，但运行中仍按 `policy_set_id` 读取当前策略，未强制按 run snapshot 中的策略版本执行。

### 文档预期

全景文档 4.3 要求 AgentRun 必须记录并默认钉住：

- AgentDefinition version
- AgentPackage version
- PolicySet version
- ToolDefinition version
- SkillDefinition version
- PromptBundle hash
- Model provider / model version

同节还要求：

- 一个 AgentRun 内不静默切换 AgentDefinition。
- 一个 AgentRun 内不静默切换 PolicySet。
- 长 Task 是否升级到新版本，需要显式 TaskCommand 或 TaskUpgradePolicy。

### 代码证据

已实现部分：

- `internal/contracts/run.go`：`VersionSnapshot` 已包含 `PolicySetVersion`。
- `internal/runtime/kernel/coordinator.go`：`versionSnapshot()` 会写入 `PolicySetVersion`。
- `internal/policy/engine/engine.go` 与 `internal/storage/postgres/postgres.go`：已有 `PolicyVersion` 存储和查询能力。

缺口证据：

- `internal/runtime/kernel/coordinator.go` 的 `step()` 每轮通过 `definition.PolicyRefs.PolicySetID` 调用 `policySet()`。
- `internal/app/core/core.go` 的 `LoadPolicySet()` 通过 `tenantID + policySetID` 加载当前策略。
- 当前运行路径未看到根据 `run.VersionSnapshot.PolicySetVersion` 或 `PolicyVersionID` 加载历史策略版本的逻辑。

### 缺失内容

1. `VersionSnapshot` 缺少可直接定位策略历史版本的 `policy_version_id`。
2. runtime 没有 `LoadPolicySetVersion(policy_set_id, version)` 或 `LoadPolicyVersion(policy_version_id)` 路径。
3. Resume / 长任务继续执行时未强制使用 run 创建时钉住的策略版本。
4. 现有测试验证了 snapshot 记录字段，但未验证“stable policy 变更后旧 run 行为不变”。

### 上线风险

如果一个长任务运行期间管理员发布新的 stable PolicySet，旧 run 可能在下一轮读取新策略，导致：

- 回放结果与原执行环境不一致。
- 安全策略变更难以归因。
- 审计上虽然记录了旧 `policy_set_version`，但实际执行可能使用新策略。

### 建议修复

1. 在 `VersionSnapshot` 中增加 `PolicyVersionID`，或确保 `PolicySetID + PolicySetVersion` 可唯一查历史版本。
2. 为 `PolicyManager` / `PolicyStore` 增加按版本加载接口。
3. runtime 在 `AgentRun` 创建后，每轮优先使用 snapshot 中钉住的策略版本。
4. 只有显式 `upgrade_agent_version` 或 `TaskUpgradePolicy` 允许变更后，才更新 task/run 的版本引用并写 Trace/Audit。

### 验收标准

- 创建 run 后发布新 Policy stable，旧 run resume 后仍使用旧 policy version。
- 新 run 使用新 stable policy version。
- Trace / RunSnapshot 同时记录实际使用的 `policy_version_id` 和 `policy_set_version`。
- 测试覆盖 `agent.run`、`task resume`、`TaskUpgradePolicy` 三种路径。

## 5. K-02：AGENTS.md / SKILL.md Draft 编辑

### 分析结论

部分实现。AgentPackage 创建时可带 `agents_md` 和 skill metadata，但产品文档要求的 `patch_agents_md`、`add_skill`、`update_skill`、`remove_skill` 等 Draft 管理能力没有完整暴露。

### 文档预期

全景文档 6.3 / 6.4 / 6.9 / 24.5 要求：

- `AGENTS.md / SKILL.md` 是人类友好的作者格式。
- AgentDefinition 模块负责编译 `AGENTS.md` 和 `SKILL.md`。
- 管理工具包括 `agent.package.draft.patch_agents_md`、`agent.package.draft.add_skill`、`agent.package.draft.update_skill`、`agent.package.draft.remove_skill`。
- 优化人员可以修改 `AGENTS.md / SKILL.md`。

### 代码证据

已实现部分：

- `internal/agentdef/package/service.go`：`AgentPackageSource` 包含 `AgentsMD`、`Prompt`、`ToolBindings`、`Metadata`。
- `internal/agentdef/package/compiler.go`：可从 `AgentsMD` / `Prompt` 和 `metadata.skill_definitions` 编译 AgentDefinition。
- `internal/server/server.go`：已暴露 `agent.package.draft.create`、`agent.package.draft.patch_prompt`、`agent.package.tool_binding.update`、`agent.package.draft.validate`、`agent.package.review`、`agent.package.publish`。

缺口证据：

- `internal/server/server.go` 没有 `agent.package.draft.patch_agents_md`。
- `internal/server/server.go` 没有 `agent.package.draft.add_skill`、`update_skill`、`remove_skill`。
- `docs/openapi.clean-core.v1.json` 命令枚举中没有上述 skill/agents_md 管理命令。
- `internal/agentdef/package/service.go` 没有独立 PatchAgentsMD / UpsertSkill / RemoveSkill 服务方法。

### 缺失内容

1. 不能在 Draft 创建后单独修改 `AGENTS.md`。
2. 不能用产品文档定义的命令新增、更新、删除 `SKILL.md`。
3. Skill 当前更像 metadata 结构输入，而不是文档中描述的人类可写 `SKILL.md` 生命周期。
4. OpenAPI 没有对应 payload/result schema。

### 上线风险

优化人员虽然可以创建 Draft 和修改 prompt，但无法按文档承诺持续维护完整 AgentPackage 资产。上线后会出现：

- Skill 调优仍依赖开发人员或直接改 metadata。
- AGENTS.md / SKILL.md 与产品文档中的“人类作者入口”不一致。
- 管理工具颗粒度不足，难以做 diff、review、approval、audit。

### 建议修复

1. 扩展 `AgentPackageSource`，显式表达 `Skills` 或 `SkillSources`，保留 SKILL.md 原文。
2. 增加 Draft 服务方法：`PatchAgentsMD`、`AddSkill`、`UpdateSkill`、`RemoveSkill`。
3. 增加 server command 和 OpenAPI schema。
4. Validate 阶段输出路径化错误，例如 `skills/report/SKILL.md`。
5. Publish 后确保新 run 加载新 SkillDefinition，当前 run 不受影响。

### 验收标准

- 优化人员可创建 Draft。
- 优化人员可单独修改 `AGENTS.md`。
- 优化人员可新增、更新、删除 `SKILL.md`。
- Validate 能发现坏 skill，并指明路径。
- Publish 后新 run 可见新 skill，旧 run 不静默改变。

## 6. K-03：管理类工具审批闭环

### 分析结论

部分实现。代码有角色校验、review、Audit 和 release policy 判断，但管理类变更没有进入统一 `waiting_approval` / pending approval action 闭环。

### 文档预期

全景文档 6.9 / 13.7 / 21.4 / 24.4 要求：

- 管理类工具默认只能修改 Draft。
- 发布必须经过权限校验和审计。
- 高风险修改必须进入 `waiting_approval`。
- `publish / rollback / policy.update` 默认需要审批。
- 高风险管理工具必须进入 approval 或被拒绝。

### 代码证据

已实现部分：

- `internal/server/server.go` 的 `allowedCommand()` 对管理命令要求 optimizer/admin role。
- `packageReview()`、`policyReview()` 存在 review 状态。
- `policyReleaseAction()` 和 `packageReleaseAction()` 调用 `EvaluateReleaseAction()`。
- `internal/policy/engine/release.go` 可返回 `approval_required`。
- `internal/agentdef/package/service.go` 和 `internal/policy/engine/engine.go` 会写发布、回滚、policy 审计。

缺口证据：

- `packageReleaseAction()` / `policyReleaseAction()` 通过 payload 中 `approved` 或非空 `approval_id` 直接视为批准。
- 未看到对 `approval_id` 的实体校验、审批人校验、过期校验或绑定资源校验。
- `approveTaskAction()` 只处理工具调用产生的 pending approval tool result。
- 管理类命令没有创建 pending approval action，也没有让对应 task/run 进入 `waiting_approval`。

### 缺失内容

1. 管理类命令缺少统一 ApprovalRequest / ApprovalAction 事实对象。
2. `approval_id` 只是输入字段，当前材料未证明它对应真实审批记录。
3. `policy.update`、publish、rollback、large canary 等高风险操作未形成 waiting/resume 闭环。
4. 管理命令的 approval trace/audit 证据链不完整。

### 上线风险

如果上线为多人协作产品，当前审批链容易出现：

- 只要调用者传入 `approved=true` 或任意 `approval_id` 就绕过审批门槛。
- 审批人、审批范围、审批对象无法追溯。
- 高风险管理操作无法进入统一等待态，外部协作系统也难以提示和恢复。

### 建议修复

1. 增加管理类 ApprovalRequest 实体，至少包含 resource type/id、action、risk、requester、approver、status、expires_at。
2. `EvaluateReleaseAction()` 返回 approval_required 时，server 不应只返回错误，应创建 approval request 并返回 pending 状态。
3. `approval_id` 必须校验存在、状态 approved、租户一致、资源一致、动作一致。
4. 对 package/policy/artifact 管理命令统一写 `approval.requested`、`approval.resolved` Trace/Audit。
5. 与外部协作 writeback 打通 waiting_approval。

### 验收标准

- 未审批的 stable / rollback / large canary / policy.update 返回 pending approval，而不是直接执行。
- 伪造或跨租户 `approval_id` 被拒绝。
- 审批通过后相同资源和动作可继续执行。
- 审批拒绝后资源状态不变。
- Trace/Audit 可查完整申请、审批、执行链路。

## 7. K-04：Canary 流量与命中记录

### 分析结论

部分实现。代码支持 canary 状态和 canary_percent 门禁，但没有真实流量路由、流量范围记录和命中 runId 记录。

### 文档预期

全景文档 6.11 / 13.8 / 24.4 要求：

- AgentPackage 和 Policy 必须支持发布、灰度、回滚。
- canary 必须记录流量范围和命中 runId。
- canary 只能命中特定租户、用户组或流量比例。
- AgentRun 必须记录命中的 AgentPackage / Policy 版本。
- 长 Task 默认不静默跟随 canary。

### 代码证据

已实现部分：

- `internal/contracts/policy.go`：`ReleasePolicy` 有 canary percent 相关字段。
- `internal/policy/engine/release.go`：`EvaluateReleaseAction()` 校验 canary percent。
- `internal/server/server.go`：`packageReleaseAction()` 和 `policyReleaseAction()` 支持 canary。
- `internal/server/server.go`：`ensureRunnableAgentVersion()` 允许 canary/stable release 被运行。
- `internal/runtime/kernel/coordinator.go`：RunSnapshot 记录 AgentPackage 和 PolicySetVersion。

缺口证据：

- 未看到 canary scope / traffic rule / hit table。
- `agent.run` 默认版本解析仍主要使用 stable/default 版本；canary 需要显式指定版本才可运行。
- 未看到按租户、用户组、百分比对新 run 自动选择 canary version 的 resolver。
- 未看到每次命中 canary 时记录 `run_id`、命中原因、流量规则。

### 缺失内容

1. 缺少 `CanaryRule` 或 `ReleaseTrafficPolicy` 持久模型。
2. 缺少按 tenant/user/group/percent 的 canary routing。
3. 缺少 canary hit 记录，包括 run_id、package_version_id、policy_version_id、rule_id。
4. 缺少 canary 命中 Trace/Audit。
5. 缺少“旧 run / 长 Task 不跟随 canary”的回归测试。

### 上线风险

当前 canary 更像“允许运行的发布状态”，不是产品意义上的灰度发布。上线后会导致：

- 无法知道哪些 run 命中了 canary。
- 无法按用户组或百分比灰度。
- 灰度效果无法回收、评估和回滚。
- 出问题时难以定位受影响任务。

### 建议修复

1. 为 package/policy release 增加 canary rule：tenant scope、user/group scope、percent、start/end、created_by。
2. 新 run 创建前增加 release resolver，基于 caller/context 做稳定哈希路由。
3. RunSnapshot 增加 release_status、canary_rule_id、policy_version_id。
4. Trace 增加 `release.canary_hit` 或在 `run.created` payload 中明确记录命中详情。
5. 提供 canary hit 查询接口，支持按 release/run/tenant 查询。

### 验收标准

- canary 10% 时，同一 caller/task 上下文命中结果稳定。
- 指定用户组 canary 时，组外用户不命中。
- 每个命中的 run 都能查到 package/policy version 和 canary_rule_id。
- rollback 后新 run 不再命中已回滚版本。
- 旧 run / 长 Task 不静默切换到 canary。

## 8. K-05：Eval 结果展示与治理事件

### 分析结论

部分实现。Eval Suite 已能创建、加 case、运行、按 gate 判断通过并持久化结果，但结果展示还不足以完整支撑优化人员调优和发布复盘。

### 文档预期

全景文档 6.10 / 24.5 要求：

- EvalCase 覆盖输入样例、期望输出结构、必须调用工具、禁止调用工具、期望状态、Artifact、安全边界、不应泄漏信息。
- Eval 结果应写入治理数据：`eval.run.started`、`eval.case.completed`、`eval.case.failed`、`eval.summary.created`。
- Eval 结果能展示失败原因、工具调用轨迹、最终输出。

### 代码证据

已实现部分：

- `internal/eval/runner.go`：`Case` 支持 input、target、context、must/should-not tools、final reply contains、max tool calls、end status。
- `internal/eval/runner.go`：`Result` 包含 failures、run_id、trace_id、tool call total、tool misuse。
- `internal/eval/store.go`：`SuiteResult` 包含 pass_rate、tool_misuse_rate、failures、results。
- `internal/server/server.go`：支持 `eval.suite.create`、`eval.suite.add_case`、`eval.suite.run`、`eval.run`。
- `docs/openapi.clean-core.v1.json`：已有 Eval suite/result 查询 schema。

缺口证据：

- `EvalResult` 未保存最终回复文本或最终输出结构。
- `EvalResult` 未直接保存 tool call refs / tool result refs，只能通过 `trace_id` 或 `run_id` 间接查询。
- `internal/contracts/governance.go` 未定义 eval.* Trace 常量。
- 当前材料未看到 `eval.run.started`、`eval.case.completed`、`eval.case.failed`、`eval.summary.created` 写入 Trace/Audit。

### 缺失内容

1. Eval 结果缺少最终输出字段。
2. Eval 结果缺少工具轨迹摘要或引用列表。
3. Eval 治理事件未按文档命名写入。
4. EvalCase 对 expected artifact / expected output schema / leak check 仍不完整。
5. Eval 结果查询还不能直接给优化人员展示完整失败上下文。

### 上线风险

Eval 能作为发布 gate 使用，但调优体验不足：

- 优化人员只能看到失败原因片段，不能直接看到最终输出。
- 工具误调用需要跳转 Trace 才能复盘。
- 发布失败的治理证据不够标准化。
- 后续做 Eval 报表或回归对比会缺字段。

### 建议修复

1. `EvalResult` 增加 `final_reply`、`final_output`、`tool_call_refs`、`artifact_refs`、`expected_mismatches`。
2. Eval runner 在每个 case 开始/结束/失败时写 eval.* Trace。
3. Suite result 保存 summary，并写 `eval.summary.created`。
4. 增强 EvalCase：expected artifact、output schema、forbidden leak patterns。
5. OpenAPI 补齐 Eval result 展示字段。

### 验收标准

- Eval result API 直接返回失败原因、最终输出、工具调用轨迹引用。
- Trace 中能查到 eval.run.started、eval.case.completed/failed、eval.summary.created。
- Suite gate 失败时 release stable 被拒绝，并能看到具体 case 失败证据。
- Eval 结果可被优化人员用于修改 Prompt/SKILL/Policy，而无需查数据库。

## 9. K-06：ExecutionDomain 凭证范围和数据边界

### 分析结论

部分实现。ExecutionDomain 已有统一 profile、worker/sandbox/managed adapter、ResourceLimits 和 NetworkPolicy 元数据，但文档要求的 CredentialScope 和 DataBoundary 没有实体和执行路径。

### 文档预期

全景文档 15.2 / 15.3 要求 ExecutionDomain 核心对象包括：

- RuntimeProfile
- ExecutionDomain
- ExecutionDomainResolver
- WorkerAdapter
- SandboxAdapter
- ManagedAgentAdapter
- NetworkPolicy
- ResourceLimit
- CredentialScope
- DataBoundary

同节要求 ExecutionDomain 负责：

- 控制网络策略。
- 控制资源限制。
- 控制凭证范围。
- 控制数据边界。

全景文档 17.4 还将“凭证使用”列为 Audit 关注点。

### 代码证据

已实现部分：

- `internal/execution/domain/domain.go`：`ExecutionProfile`、`ResourceLimits`、`NetworkPolicy` 已存在。
- `internal/execution/domain/domain.go`：已有 Local / Worker / Sandbox / Managed execution domain 抽象。
- `internal/tool/runtime/runtime.go`：执行工具时解析 execution profile，并在 trace 中记录 execution metadata。
- `internal/contracts/governance.go`：定义了 `AuditCredentialUsed = "credential.used"`。

缺口证据：

- 未看到 `CredentialScope` 结构体或字段。
- 未看到 `DataBoundary` 结构体或字段。
- `AuditCredentialUsed` 只有常量，当前材料未看到实际写入路径。
- `ResourceLimits` 多数作为 profile metadata 传递，未看到本地执行强制限制。
- Local domain 只拒绝 network policy，未处理凭证和数据边界。

### 缺失内容

1. 缺少 CredentialScope 数据结构和策略输入。
2. 缺少 DataBoundary 数据结构和执行域校验。
3. 缺少 credential broker / credential resolver 边界。
4. 缺少 credential.used Audit 写入。
5. 缺少对 worker/sandbox/managed adapter 的凭证下发约束。

### 上线风险

ExecutionDomain 是工具执行安全边界。缺少凭证和数据边界后：

- 高敏工具可能拿到超出任务范围的凭证。
- 托管执行域可能接收不应离开私有边界的数据。
- 凭证使用无法审计。
- 出现安全事故时难以证明最小权限。

### 建议修复

1. 在 `ExecutionProfile` 中增加 `CredentialScope` 和 `DataBoundary`。
2. ToolRuntime 调用 ExecutionDomain 前，基于 PolicySet / ToolDefinition / RuntimeContext 计算允许凭证。
3. 增加 credential resolver，只向执行域传递 scoped credential ref，不直接暴露 secret。
4. 每次凭证解析或使用写 `credential.used` Audit。
5. 对 managed domain 增加数据边界拒绝规则，例如敏感数据禁止进入外部托管 runtime。

### 验收标准

- 未授权凭证 scope 的工具调用被拒绝。
- 允许凭证调用只记录 credential ref，不在 Trace/Audit 中泄漏 secret。
- managed domain 遇到禁止外传的数据边界时拒绝执行。
- `credential.used` Audit 可按 tenant/task/run/tool 查询。
- worker/sandbox/managed adapter 都能接收并执行边界约束。

## 10. K-07：Go/No-Go 上线门禁覆盖

### 分析结论

部分实现。项目已经有 readiness、Go/No-Go 和 E2E matrix，但上线门禁没有显式覆盖 K-01 到 K-06，因此不能防止这些关键能力回退。

### 文档预期

全景文档 24.1 到 24.5 要求上线前验收：

- 模块有公开接口、输入输出类型、错误类型、单元测试、边界测试、Trace/Audit 触发点说明。
- 链路支持 reply、ask_clarification、tool_call、repair、waiting_input、waiting_approval、task resume、artifact create、trace replay、audit query。
- 策略验证低风险可执行、高风险进入 approval、无权限拒绝、危险参数拦截、PromptBundle 压缩、Task 事件恢复。
- v1.2 新增边界包括版本不静默切换、Skill Draft、Canary run 版本记录、Eval failed 不能 stable。
- 优化交接要求 Prompt/Skill/Policy/Eval 全流程可调优、可发布、可 rollback、全过程 Audit。

### 代码证据

已实现部分：

- `internal/readiness/report.go`：已有 config、database、tools、governance、migration、release switch 检查。
- `internal/release/report.go`：`BuildGoNoGo()` 已聚合 readiness、migration、contract snapshot、E2E matrix、metrics、production config gates。
- `docs/e2e_regression_matrix.md`：已有 package release、handoff、task upgrade、governance replay/metrics、release switches、auth role、runtime loop 等场景。
- `docs/ops_runbook.md`：已有 readiness / Go-No-Go 操作说明。

缺口证据：

- `BuildGoNoGo()` 没有检查 K-01 到 K-06 的专项能力或测试结果。
- `docs/e2e_regression_matrix.md` 没有列出 policy version pinned after policy stable change、patch_agents_md/skill draft、management approval pending、canary hit record、eval final output/tool trace、credential scope/data boundary。
- Go/No-Go 现在只检查 E2E matrix 文件存在，不知道矩阵内关键场景是否实际覆盖。

### 缺失内容

1. 缺少上线前专项 gate：version pinning。
2. 缺少上线前专项 gate：AGENTS.md / SKILL.md Draft 管理。
3. 缺少上线前专项 gate：管理类审批 pending/resume。
4. 缺少上线前专项 gate：canary hit 记录。
5. 缺少上线前专项 gate：Eval result final output / tool trace。
6. 缺少上线前专项 gate：CredentialScope / DataBoundary / credential.used audit。

### 上线风险

即使后续修完 K-01 到 K-06，如果 Go/No-Go 不覆盖它们，后续重构或回归可能再次破坏关键能力，而 release report 仍然显示 go。

### 建议修复

1. 在 `docs/e2e_regression_matrix.md` 增加 K-01 到 K-06 专项场景。
2. 给每项新增自动化测试入口。
3. Go/No-Go 增加能力门禁清单，可以先基于测试报告/manifest，再逐步接 CI 结果。
4. release report 中输出关键能力 gate，例如 `runtime.policy_version_pinning`、`agent_package.skill_draft`、`management.approval_flow`。
5. ops runbook 增加上线前必查项。

### 验收标准

- K-01 到 K-06 都有自动化测试或明确人工验收项。
- `/v1/release/go-no-go` 能显示这些 gate 的 pass/fail/warn。
- 任一 P0/P1 gate fail 时 decision 为 `no-go`。
- CI 中同样执行这些 gate。

## 11. 建议修复顺序

### 第一批：上线安全与审计 P0

1. K-01 AgentRun / PolicySet 版本钉住。
2. K-03 管理类工具审批闭环。

理由：这两项直接影响运行可审计、安全审批和事故追溯，是内核产品上线可信度的基础。

### 第二批：发布与调优闭环 P1

1. K-04 Canary 流量与命中记录。
2. K-05 Eval 结果展示与治理事件。
3. K-02 AGENTS.md / SKILL.md Draft 编辑。

理由：这三项决定优化人员能否持续调优、灰度、回滚和复盘。

### 第三批：执行安全与上线门禁 P1

1. K-06 ExecutionDomain 凭证范围和数据边界。
2. K-07 Go/No-Go 上线门禁覆盖。

理由：ExecutionDomain 是工具执行安全底座；Go/No-Go 应在上述修复完成后防止回归。

## 12. 后续任务清单

### 任务 K-01

分类：B 文档要求但部分实现

优先级：P0

依据：全景文档 4.3 版本钉住与长任务升级。

涉及文件：

- `internal/contracts/run.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/app/core/core.go`
- `internal/policy/engine/engine.go`
- `internal/storage/postgres/postgres.go`

任务说明：让运行时真正按 AgentRun snapshot 中的 policy version 执行，而不是每轮读取当前 stable policy。

验收标准：旧 run 在 policy stable 变更后继续使用旧 policy version，新 run 使用新 stable。

### 任务 K-02

分类：B 文档要求但部分实现

优先级：P1

依据：全景文档 6.9 和 24.5。

涉及文件：

- `internal/agentdef/package/service.go`
- `internal/agentdef/package/compiler.go`
- `internal/server/server.go`
- `docs/openapi.clean-core.v1.json`

任务说明：补齐 `patch_agents_md` 和 skill draft create/update/delete 管理命令。

验收标准：优化人员可不改 Go 代码维护 AGENTS.md / SKILL.md 并发布新版本。

### 任务 K-03

分类：B 文档要求但部分实现

优先级：P0

依据：全景文档 6.9、13.7、21.4、24.4。

涉及文件：

- `internal/server/server.go`
- `internal/policy/engine/release.go`
- `internal/task/runtime`
- `internal/governance/audit`

任务说明：管理类高风险操作进入真实 approval request / waiting_approval / resume 闭环。

验收标准：伪造 approval_id 无效；审批通过后才能执行对应资源和动作。

### 任务 K-04

分类：B 文档要求但部分实现

优先级：P1

依据：全景文档 6.11、13.8、24.4。

涉及文件：

- `internal/agentdef/package/service.go`
- `internal/policy/engine/engine.go`
- `internal/server/server.go`
- `internal/runtime/kernel/coordinator.go`
- `internal/storage/postgres/postgres.go`

任务说明：实现 canary traffic rule、运行命中解析和 canary hit 记录。

验收标准：每个 canary run 都可查询命中的 release/rule/run 证据。

### 任务 K-05

分类：B 文档要求但部分实现

优先级：P1

依据：全景文档 6.10 和 24.5。

涉及文件：

- `internal/eval/runner.go`
- `internal/eval/store.go`
- `internal/contracts/governance.go`
- `internal/server/server.go`
- `docs/openapi.clean-core.v1.json`

任务说明：Eval result 增加最终输出、工具轨迹引用和 eval.* 治理事件。

验收标准：Eval result API 可直接展示失败原因、工具调用轨迹和最终输出。

### 任务 K-06

分类：B 文档要求但部分实现

优先级：P1

依据：全景文档 15.2、15.3、17.4。

涉及文件：

- `internal/execution/domain/domain.go`
- `internal/tool/runtime/runtime.go`
- `internal/contracts/governance.go`
- `internal/policy/engine`

任务说明：补齐 CredentialScope、DataBoundary 和 credential.used Audit。

验收标准：工具执行域按凭证范围和数据边界放行/拒绝，并记录凭证使用审计。

### 任务 K-07

分类：G 工程风险

优先级：P1

依据：全景文档 24.1 到 24.5。

涉及文件：

- `internal/release/report.go`
- `internal/readiness/report.go`
- `docs/e2e_regression_matrix.md`
- `docs/ops_runbook.md`

任务说明：将 K-01 到 K-06 纳入 E2E matrix 和 Go/No-Go gate。

验收标准：任一关键 gate fail 时 `/v1/release/go-no-go` 返回 `no-go`。

## 13. 当前最终判断

以“能否作为可上线的 CleanCore 内核产品”为标准，当前代码不是完全不符合产品文档，但仍不能判断为全部完成。

当前状态更准确地说是：

```text
核心运行链路：基本完成
治理/发布/评测基础：基本完成
上线级强审计和强边界：仍有缺口
优化人员交接闭环：仍有颗粒度缺口
灰度和审批产品化：仍需补强
```

建议先处理 K-01 和 K-03，再处理 K-04 / K-05 / K-02，最后用 K-06 / K-07 收口上线安全和门禁。

## 14. v0.2 修复闭环状态

日期：2026-05-31

依据：`docs/原智能体CleanCore问题修复进度_v0.1.md` 的 P7 修复记录，以及当前代码验证结果。

| 编号 | 修复状态 | 代码/文档证据 | 验证 |
|---|---|---|---|
| K-01 | 已闭环 | `internal/contracts/run.go` 增加 `policy_version_id`；`internal/runtime/kernel/coordinator.go` 按 run snapshot 加载历史策略版本 | `TestCoordinatorPinsPolicyVersionAcrossRunSteps` |
| K-02 | 已闭环 | `agent.package.draft.patch_agents_md`、`agent.package.skill.add/update/remove`；skill metadata 编译进入 SkillDefinition | `TestDraftPatchAgentsMDAndSkillLifecycle` |
| K-03 | 已闭环 | `internal/governance/approval`；`approval.approve/reject`；release approval 校验 tenant/resource/action | `TestPolicyStableRequiresConcreteApprovalRequest` |
| K-04 | 已闭环 | package canary percent/scope；默认 agent.run canary 路由；`canary.routed` Trace；canary hit 查询入口 | `TestPackageCanaryRoutesDefaultTrafficAndRecordsHit` |
| K-05 | 已闭环 | Eval Result 保存 final reply、tool calls/results、artifact refs；`eval.run.started`、`eval.case.completed`、`eval.case.failed`、`eval.summary.created` Trace | `TestRunnerPassesFinalReplyContains`、`TestRunnerToolAssertions`、`TestRunnerRecordsFailedCaseTrace` |
| K-06 | 已闭环 | ExecutionProfile 增加 `credential_scope` / `data_boundary`；ToolRuntime 写 `credential.used` Trace/Audit | `TestLocalDomainRejectsCredentialScopeAndDataBoundary`、`TestInvokeUsesExecutionDomainProfileAndTracesMetadata` |
| K-07 | 已闭环 | `docs/e2e_regression_matrix.md` 增加 Online Kernel Capability Gates；`internal/release/report.go` 增加 K-01 ～ K-06 Go/No-Go gates | `go test ./internal/release` |

最终验证：

- `gofmt -l cmd internal pkg`：无输出。
- `go vet ./...`：通过。
- `go test ./...`：通过。

因此，本文件第 13 节为 v0.1 分析时点结论；以 v0.2 修复结果看，K-01 ～ K-07 已按“可上线内核产品”目标闭环。
