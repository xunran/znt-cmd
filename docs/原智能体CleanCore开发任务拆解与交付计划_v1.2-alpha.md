# 原智能体 Clean Core 开发任务拆解与交付计划

版本：v1.2-alpha Enhanced WBS  
日期：2026-05-29  
定位：从里程碑级拆解升级为可分派开发任务级拆解  
配套文档：  
- 《原智能体 Clean Core 全景开发设计文档 v1.2 Clean》
- 《原智能体 Clean Core 工程实施规格文档 v1.0》
- 《原智能体 Clean Core 接口冻结与受控变更开发文档 v1.0-alpha》

---

## 0. 本版修正说明

v1.1-alpha 的问题是：

```text
1. Batch 0～6 更像里程碑，不像可直接分派的开发任务。
2. 每个 Batch 覆盖面偏粗，没有逐项覆盖 13 个核心模块。
3. 一些全景架构中的关键能力只在验收里出现，没有拆成明确实现任务。
4. 缺少横向工作流：contract tests、repository tests、policy tests、prompt tests、E2E regression。
5. 缺少“全景覆盖矩阵”，无法判断是否遗漏模块能力。
```

v1.2-alpha 的目标：

```text
1. 保留 Batch 0～6 的阶段顺序。
2. 增加 Architecture Coverage Matrix。
3. 把每个 Batch 拆成 Epic / Feature / Task 三层。
4. 明确每个核心模块在每个阶段的交付点。
5. 标注哪些是真实现，哪些先 Stub。
6. 增加跨模块工作流和测试任务。
7. 明确 Batch 0～5 = Clean Core Alpha，Batch 6 = Clean Core 可上线。
```

---

## 1. 总体判断

开发任务文档不能只写成：

```text
B1：实现 Task
B2：实现 Runtime
B3：实现 Tool
```

这种粒度太粗。

更合理的是：

```text
Batch：阶段目标
Epic：一组相关能力
Feature：可交付功能点
Task：开发人员可领取任务
Acceptance：可测试验收条件
```

本文件按以下层级组织：

```text
Batch 0～6
  └── Epic
       └── Feature
            └── Task
```

---

## 2. 架构覆盖矩阵

| 核心模块 | Batch 0 | Batch 1 | Batch 2 | Batch 3 | Batch 4 | Batch 5 | Batch 6 |
|---|---|---|---|---|---|---|---|
| core-contracts | 类型冻结 | 补 Task/Run 类型 | 补 Decision/Prompt 类型 | 补 Tool/Policy 类型 | 补 Package/Eval 类型 | 补 Handoff/Collab 类型 | beta freeze |
| agent-definition | interface stub | - | static loader | - | package/skill/eval/release | agent capability card | publish hardening |
| runtime-kernel | interface | run record | decision loop | tool loop | version snapshot | handoff dispatch | runaway guard |
| task-runtime | base type | task/event/state | resume input | approval/tool events | plan events | handoff/child task | recovery check |
| context-engine | interface | task summary | workview/promptbundle | toolresult/artifact context | skill context | handoff package | prompt regression |
| capability-discovery | interface | - | candidate stub | tool candidate | skill/tool index | target agent discovery | coverage tests |
| decision-engine | schema type | - | parser/validator | toolcall validation | plan-aware decision | delegate validation | contract tests |
| model-runtime | interface | - | stub/openai-compatible | retry/error | eval model mode | - | timeout/fallback |
| policy-engine | base type | status constraints | prompt policy stub | tool/approval/repair | release policy | handoff policy | auth/rate guard |
| tool-runtime | interface | - | candidate only | registry/invoke/result | management tools | delegate tool | idempotency hardening |
| execution-domain | interface | - | local stub | resolver/executor | worker profile ref | - | resource limits |
| memory-artifact | refs | - | artifact refs in view | artifact store | package/eval artifacts | context package | cleanup/checks |
| governance | interface | trace/audit write | model/decision trace | tool/policy trace | release/eval audit | handoff audit | query/readiness |

结论：

```text
v1.2-alpha 必须确保每个模块都有明确落地任务，而不是只在架构中出现。
```

---

## 3. 开发阶段定义

### Batch 0：工程骨架与 Contract Alpha

目标：

```text
建立可编译、可测试、可迁移、可分模块开发的工程基线。
```

状态：

```text
不能跑 Agent，但可以开始并行开发。
```

---

### Batch 1：任务事实源与运行记录

目标：

```text
Task / TaskEvent / AgentRun / Trace / Audit 事实源可用。
```

状态：

```text
系统能记录运行，但还不能真正智能决策。
```

---

### Batch 2：模型决策闭环

目标：

```text
输入 → WorkView → PromptBundle → Model → Decision → Reply / Ask。
```

状态：

```text
能完成无工具或伪工具的 AgentRun。
```

---

### Batch 3：工具执行闭环

目标：

```text
Decision.tool_call → Policy → ToolRuntime → ToolResult → 下一轮。
```

状态：

```text
能完成多轮工具组合任务。
```

---

### Batch 4：AgentPackage 优化发布闭环

目标：

```text
Prompt / Skill / ToolBinding / Policy 可 Draft / Eval / Publish / Rollback。
```

状态：

```text
开发和优化职责开始解耦。
```

---

### Batch 5：多 Agent Handoff 与外部协作接入

目标：

```text
Agent-to-Agent 委派、HandoffContextPackage、外部 CollaborationProvider 可用。
```

状态：

```text
Clean Core 支持多智能体协作，但不实现完整 Group / 消息系统。
```

---

### Batch 6：Clean Core 上线硬化

目标：

```text
Clean Core 从 Alpha 功能可用进入 Core Service 可上线。
```

状态：

```text
可被 Array / 内部 API / Agent 管理系统 / Worker 作为核心服务调用。
```

---

# Batch 0：工程骨架与 Contract Alpha

## B0.E1 工程基础

### B0.E1.F1 Go 工程初始化

任务：

```text
B0.1.1 创建 go.mod。
B0.1.2 创建 cmd/clean-core-server/main.go。
B0.1.3 创建 internal 目录结构。
B0.1.4 创建 pkg 公共工具目录。
B0.1.5 配置 gofmt / go vet / staticcheck。
B0.1.6 配置基础 Makefile。
B0.1.7 配置测试命令 go test ./...
```

验收：

```text
项目可编译。
go test ./... 通过。
本地服务可启动并输出版本号。
```

---

### B0.E1.F2 配置与日志

任务：

```text
B0.2.1 定义 Config struct。
B0.2.2 支持 env 加载。
B0.2.3 支持 local yaml/json 配置。
B0.2.4 初始化 structured logger。
B0.2.5 日志必须包含 trace_id / run_id / task_id 字段预留。
B0.2.6 增加 health check endpoint。
```

验收：

```text
缺少必要配置时启动失败。
日志格式稳定。
health check 可用。
```

---

## B0.E2 contracts alpha

### B0.E2.F1 基础 ID 与枚举

任务：

```text
B0.3.1 定义 TenantID / UserID / AgentID / TaskID / AgentRunID。
B0.3.2 定义 ToolCallID / ToolResultID / ArtifactID / HandoffID。
B0.3.3 定义 DecisionType / ReplyKind。
B0.3.4 定义 TaskStatus / RunStatus。
B0.3.5 定义 ToolResultStatus / RiskLevel / HandoffMode。
B0.3.6 定义 ReleaseStatus。
```

验收：

```text
所有核心 ID 都是强类型。
枚举有单元测试。
未知枚举有默认错误处理。
```

冻结等级：

```text
L1 硬冻结
```

---

### B0.E2.F2 RuntimeError 与错误码

任务：

```text
B0.4.1 定义 RuntimeError。
B0.4.2 定义 ErrorCode 常量。
B0.4.3 定义 IsRetryable / IsRepairable。
B0.4.4 定义错误转 API response。
B0.4.5 定义错误转 Trace payload。
```

验收：

```text
错误码表和代码一致。
错误能携带 retryable / repairable。
```

---

### B0.E2.F3 AgentEnvelope / RuntimeContext / CollaborationContext

任务：

```text
B0.5.1 定义 AgentEnvelope。
B0.5.2 定义 AgentTarget。
B0.5.3 定义 AgentCaller。
B0.5.4 定义 RuntimeContext。
B0.5.5 定义 CollaborationContext。
B0.5.6 定义 ReplyTarget。
B0.5.7 增加 JSON marshal/unmarshal 测试。
```

验收：

```text
AgentEnvelope 可序列化。
CollaborationContext 不包含 Group 成员管理。
RuntimeContext 可携带 tenant / user / task / collaboration。
```

冻结等级：

```text
L1 硬冻结
```

---

## B0.E3 storage foundation

### B0.E3.F1 Migration Runner

任务：

```text
B0.6.1 选择 migration 方案。
B0.6.2 创建 migrations 目录。
B0.6.3 支持 migration up。
B0.6.4 支持 migration status。
B0.6.5 支持测试数据库初始化。
B0.6.6 编写 migration 开发规范。
```

验收：

```text
空库可执行 migration。
重复执行不会重复建表。
CI 可运行 migration test。
```

---

### B0.E3.F2 Repository 基础约定

任务：

```text
B0.7.1 定义 Repository 错误约定。
B0.7.2 定义 TxManager interface。
B0.7.3 定义 WithTx 规范。
B0.7.4 定义 optimistic lock 返回错误。
B0.7.5 定义 idempotency 查询规范。
```

验收：

```text
各模块 repository 使用统一事务接口。
TASK_CONFLICT 可统一识别。
```

---

## B0.E4 governance base

### B0.E4.F1 Trace / Audit Interface

任务：

```text
B0.8.1 定义 TraceEvent。
B0.8.2 定义 AuditEvent。
B0.8.3 定义 TraceRecorder。
B0.8.4 定义 AuditLogger。
B0.8.5 实现 InMemoryTraceRecorder。
B0.8.6 实现 InMemoryAuditLogger。
```

验收：

```text
单测可断言 Trace / Audit 是否写入。
```

---

# Batch 1：任务事实源与运行记录

## B1.E1 Task Runtime

### B1.E1.F1 Task Domain + Table

任务：

```text
B1.1.1 创建 tasks 表。
B1.1.2 实现 Task struct。
B1.1.3 实现 TaskRepository.Create。
B1.1.4 实现 TaskRepository.Get。
B1.1.5 实现 TaskRepository.UpdateWithVersion。
B1.1.6 添加 tenant_id / status / parent_task_id 索引。
```

验收：

```text
Task 可创建、读取、更新。
乐观锁冲突返回 TASK_CONFLICT。
```

---

### B1.E1.F2 TaskEvent append-only

任务：

```text
B1.2.1 创建 task_events 表。
B1.2.2 实现 TaskEvent struct。
B1.2.3 实现 AppendEvent。
B1.2.4 实现 ListEventsByTask。
B1.2.5 禁止 update/delete repository。
B1.2.6 实现事件重放读取。
```

验收：

```text
TaskEvent 只能追加。
按 task_id 可按时间顺序读取。
```

---

### B1.E1.F3 Task State Machine

任务：

```text
B1.3.1 实现状态转换表。
B1.3.2 实现 ApplyCommand。
B1.3.3 实现 created → accepted → planning → running。
B1.3.4 实现 running → waiting_input → running。
B1.3.5 实现 running → waiting_approval → running。
B1.3.6 实现 cancel / pause / resume。
B1.3.7 实现 completed / failed / cancelled 终态保护。
```

验收：

```text
非法状态转换被拒绝。
每次状态转换都写 TaskEvent。
终态不能继续运行。
```

---

## B1.E2 AgentRun

### B1.E2.F1 AgentRun Domain + Table

任务：

```text
B1.4.1 创建 agent_runs 表。
B1.4.2 定义 AgentRun。
B1.4.3 实现 CreateRun。
B1.4.4 实现 MarkRunning。
B1.4.5 实现 MarkCompleted。
B1.4.6 实现 MarkFailed。
B1.4.7 保存 version_snapshot JSON。
```

验收：

```text
AgentRun 可创建、完成、失败。
Run 创建时记录 agentVersion / policySetID。
```

---

### B1.E2.F2 Run Step 记录

任务：

```text
B1.5.1 定义 RunStep struct。
B1.5.2 在 agent_runs 中记录 step_count。
B1.5.3 每轮 loop 生成 step_id。
B1.5.4 Trace 中包含 step_id。
B1.5.5 超限时返回明确错误。
```

验收：

```text
每轮 Decision Loop 可追踪。
MaxSteps 生效。
```

---

## B1.E3 Governance Persistence

### B1.E3.F1 TraceEvent 持久化

任务：

```text
B1.6.1 创建 trace_events 表。
B1.6.2 实现 PostgresTraceRecorder。
B1.6.3 支持按 trace_id 查询。
B1.6.4 支持按 run_id 查询。
B1.6.5 支持按 task_id 查询。
```

验收：

```text
run.created / task.status_changed 可查询。
```

---

### B1.E3.F2 AuditEvent 持久化

任务：

```text
B1.7.1 创建 audit_events 表。
B1.7.2 实现 PostgresAuditLogger。
B1.7.3 支持按 tenant_id 查询。
B1.7.4 支持按 action 查询。
B1.7.5 支持按 resource_id 查询。
```

验收：

```text
高风险行为 Audit 可查。
Audit 不影响业务结果。
```

---

# Batch 2：模型决策闭环

## B2.E1 AgentDefinition Static Loader

任务：

```text
B2.1.1 定义 AgentDefinition。
B2.1.2 定义 AgentToolsConfig。
B2.1.3 定义 RuntimeLimits。
B2.1.4 定义 AgentLoader interface。
B2.1.5 实现 StaticAgentLoader。
B2.1.6 提供测试 AgentDefinition。
```

验收：

```text
runtime-kernel 可加载测试 Agent。
```

Stub：

```text
完整 AgentPackage 编译放到 Batch 4。
```

---

## B2.E2 Runtime Kernel

### B2.E2.F1 RunCoordinator

任务：

```text
B2.2.1 实现 AgentEnvelope handler。
B2.2.2 创建 AgentRun。
B2.2.3 加载 AgentDefinition。
B2.2.4 创建或恢复 Task。
B2.2.5 初始化 RunContext。
B2.2.6 写 run.created trace。
```

验收：

```text
agent.run 可以创建 run 和 task。
```

---

### B2.E2.F2 Decision Dispatch

任务：

```text
B2.3.1 处理 Decision.reply。
B2.3.2 处理 Decision.ask_clarification。
B2.3.3 处理 Decision.unsupported。
B2.3.4 处理 Decision.error。
B2.3.5 处理 Decision.tool_call 的占位分发。
B2.3.6 统一 RunResult。
```

验收：

```text
reply 结束 run。
ask_clarification 进入 waiting_input。
unsupported 结束 run。
```

---

## B2.E3 Context Engine

### B2.E3.F1 WorkViewBuilder

任务：

```text
B2.4.1 定义 WorkView。
B2.4.2 聚合用户输入。
B2.4.3 聚合 AgentDefinition summary。
B2.4.4 聚合 Task summary。
B2.4.5 聚合 TaskEvent summary。
B2.4.6 预留 Plan / Handoff / ToolResult / Artifact 字段。
```

验收：

```text
每轮可以重建 WorkView。
WorkView 不入事实源。
```

---

### B2.E3.F2 PromptBundleBuilder

任务：

```text
B2.5.1 定义 PromptBundle。
B2.5.2 实现 system/developer/task/context 渲染。
B2.5.3 实现来源分区标记。
B2.5.4 实现 PromptBundle hash。
B2.5.5 实现 token budget 预留字段。
B2.5.6 写 promptbundle.built trace。
```

验收：

```text
PromptBundle 有稳定 hash。
用户输入不会被渲染为 system 指令。
```

---

## B2.E4 Model Runtime

任务：

```text
B2.6.1 定义 ModelClient。
B2.6.2 定义 ModelRequest / ModelResponse。
B2.6.3 实现 StubModelClient。
B2.6.4 实现 OpenAICompatibleClient skeleton。
B2.6.5 实现 timeout。
B2.6.6 实现 ModelErrorClassifier。
B2.6.7 写 model.called / model.completed trace。
```

验收：

```text
StubModelClient 可返回固定 Decision JSON。
模型错误统一包装为 RuntimeError。
```

---

## B2.E5 Decision Engine

任务：

```text
B2.7.1 定义 Decision。
B2.7.2 定义 DecisionReply。
B2.7.3 定义 ClarificationRequest。
B2.7.4 实现 JSON parser。
B2.7.5 实现 DecisionValidator。
B2.7.6 校验 DecisionType。
B2.7.7 校验 tool_call 是否在 candidate set。
B2.7.8 实现 DECISION_SCHEMA_ERROR。
```

验收：

```text
非法 Decision 被拒绝。
tool_call 引用不存在工具时被拒绝。
reply / ask / tool_call 正常解析。
```

---

# Batch 3：工具执行、策略和产物

## B3.E1 Tool Foundation

任务：

```text
B3.1.1 定义 ToolDefinition。
B3.1.2 定义 ToolCard。
B3.1.3 定义 ToolVisibility。
B3.1.4 实现 InMemoryToolRegistry。
B3.1.5 实现 ToolCard renderer。
B3.1.6 注册 echo / artifact.create 测试工具。
```

验收：

```text
工具可注册、查找、输出 ToolCard。
private / protected / exposed 可区分。
```

---

## B3.E2 ToolCall / ToolResult

任务：

```text
B3.2.1 创建 tool_calls 表。
B3.2.2 创建 tool_results 表。
B3.2.3 定义 ToolCall。
B3.2.4 定义 ToolResult。
B3.2.5 实现 idempotencyKey。
B3.2.6 保存 ToolCall。
B3.2.7 保存 ToolResult。
B3.2.8 重复请求命中已有结果。
```

验收：

```text
重复 ToolCall 不重复执行。
ToolResult 是事实源。
```

---

## B3.E3 Policy Engine

### B3.E3.F1 PolicySet

任务：

```text
B3.3.1 定义 PolicySet。
B3.3.2 定义 ToolPolicy。
B3.3.3 定义 ApprovalPolicy。
B3.3.4 定义 PromptPolicy。
B3.3.5 创建 policy_sets 表。
B3.3.6 实现默认 SystemPolicy。
```

验收：

```text
PolicySet 可加载。
默认策略可用于 ToolPolicy 判断。
```

---

### B3.E3.F2 EvaluateToolCall

任务：

```text
B3.4.1 校验 ToolDefinition 存在。
B3.4.2 校验 allowedToolIds。
B3.4.3 校验 deniedToolIds。
B3.4.4 校验 ToolVisibility。
B3.4.5 校验 RiskLevel。
B3.4.6 high / critical 返回 approval_required。
B3.4.7 写 AuditEvent。
```

验收：

```text
allowed / denied / approval_required 都有测试。
Policy 不驱动流程，只返回判断。
```

---

## B3.E4 Execution Domain

任务：

```text
B3.5.1 定义 ExecutionDomain。
B3.5.2 定义 ExecutionProfile。
B3.5.3 实现 LocalExecutionDomain。
B3.5.4 实现 ExecutionDomainResolver。
B3.5.5 预留 Worker / Sandbox / Managed Adapter。
B3.5.6 记录 execution metadata。
```

验收：

```text
本地工具可通过 LocalExecutionDomain 执行。
业务权限不写在 execution-domain。
```

---

## B3.E5 ToolRuntime Invoke

任务：

```text
B3.6.1 接收 ToolCall。
B3.6.2 查 ToolDefinition。
B3.6.3 校验 input schema。
B3.6.4 调 policy-engine。
B3.6.5 denied 返回 ToolResult.denied。
B3.6.6 approval_required 推 Task waiting_approval。
B3.6.7 allowed 执行 ToolExecutor。
B3.6.8 校验 output schema。
B3.6.9 保存 ToolResult。
B3.6.10 写 TaskEvent / Trace / Audit。
```

验收：

```text
Decision.tool_call 可执行。
高风险工具进入 waiting_approval。
Policy 不可绕过。
```

---

## B3.E6 Artifact Store

任务：

```text
B3.7.1 创建 artifacts 表。
B3.7.2 定义 Artifact。
B3.7.3 定义 ArtifactRef。
B3.7.4 实现 CreateArtifact。
B3.7.5 实现 GetArtifact。
B3.7.6 实现 Artifact summary。
B3.7.7 ToolResult 支持 ArtifactRefs。
```

验收：

```text
ArtifactRef 可进入下一轮 WorkView。
大对象不直接进入 PromptBundle。
```

---

## B3.E7 Tool Loop Integration

任务：

```text
B3.8.1 runtime-kernel 接入 ToolRuntime。
B3.8.2 ToolResult 写入后继续下一轮 loop。
B3.8.3 WorkView 读取 ToolResult summary。
B3.8.4 PromptBundle 渲染 ToolResult summary。
B3.8.5 MaxToolCalls 生效。
B3.8.6 MaxConsecutiveToolFailures 生效。
```

验收：

```text
tool_call → tool_result → next prompt → reply 链路跑通。
```

---

# Batch 4：AgentPackage、Skill、Eval、Release

## B4.E1 AgentPackage Draft

任务：

```text
B4.1.1 创建 agent_package_versions 表。
B4.1.2 定义 AgentPackageSource。
B4.1.3 定义 Draft 状态。
B4.1.4 实现 CreateDraft。
B4.1.5 实现 PatchAgentsMD。
B4.1.6 实现 PatchPrompt。
B4.1.7 实现 UpdateToolBinding。
B4.1.8 写 Audit。
```

验收：

```text
优化人员可创建和修改 Draft。
Draft 不影响当前 AgentRun。
```

---

## B4.E2 AGENTS.md / SKILL.md Compiler

任务：

```text
B4.2.1 解析 AGENTS.md。
B4.2.2 解析 agent.yaml。
B4.2.3 解析 SKILL.md。
B4.2.4 解析 skill.yaml。
B4.2.5 生成 AgentDefinition。
B4.2.6 生成 SkillCard。
B4.2.7 生成 SkillInstruction。
B4.2.8 编译错误定位到文件路径。
```

验收：

```text
AgentPackage 可编译。
错误可读。
CompiledHash 稳定。
```

---

## B4.E3 Skill / Tool / Capability Discovery

任务：

```text
B4.3.1 创建 SkillCardIndex。
B4.3.2 创建 ToolCardIndex。
B4.3.3 根据 task objective 召回 Skill。
B4.3.4 根据 Skill 召回 Tool。
B4.3.5 根据 Agent allowedToolIds 过滤。
B4.3.6 根据 Policy 过滤。
B4.3.7 CandidateSet 进入 WorkView。
```

验收：

```text
候选 Skill / Tool 动态刷新。
动态候选不等于动态注册工具。
```

---

## B4.E4 Eval Suite

任务：

```text
B4.4.1 定义 EvalCase schema。
B4.4.2 定义 EvalResult。
B4.4.3 实现 EvalRunner。
B4.4.4 支持 mustCallTools。
B4.4.5 支持 shouldNotCallTools。
B4.4.6 支持 finalReplyContains。
B4.4.7 支持 expectedArtifacts。
B4.4.8 支持 maxToolCalls。
B4.4.9 输出 EvalReport。
```

验收：

```text
Prompt / Skill 修改后可以跑 Eval。
Eval 失败阻止 stable publish。
```

---

## B4.E5 Publish / Release / Rollback

任务：

```text
B4.5.1 实现 ValidateDraft。
B4.5.2 实现 PublishDraft。
B4.5.3 创建 agent_definitions 表。
B4.5.4 写 AgentDefinition version。
B4.5.5 实现 Canary 状态。
B4.5.6 实现 Stable 状态。
B4.5.7 实现 Rollback。
B4.5.8 Run 记录版本快照。
B4.5.9 Publish / Rollback 写 Audit。
```

验收：

```text
新 run 命中新版本。
旧 run 不静默切换版本。
rollback 后新 run 命中回滚版本。
```

---

# Batch 5：Handoff、外部协作、集成回归

## B5.E1 AgentHandoff

任务：

```text
B5.1.1 创建 agent_handoffs 表。
B5.1.2 定义 AgentHandoff。
B5.1.3 定义 HandoffStatus。
B5.1.4 实现 CreateHandoff。
B5.1.5 实现 Accept / Reject。
B5.1.6 实现 Running / Completed / Failed。
B5.1.7 实现 Handoff TaskEvent。
B5.1.8 写 Handoff Trace / Audit。
```

验收：

```text
Handoff 状态机可跑通。
Handoff 不等于外部群聊任务。
```

---

## B5.E2 HandoffContextPackage

任务：

```text
B5.2.1 创建 handoff_context_packages 表。
B5.2.2 定义 HandoffContextPackage。
B5.2.3 实现 hybrid 模式。
B5.2.4 实现 summary_only 模式。
B5.2.5 实现 reference_only 模式。
B5.2.6 full_context 受策略控制。
B5.2.7 生成 context package hash。
B5.2.8 下游 Agent 可读取 refs。
```

验收：

```text
默认 hybrid。
不直接传上游 PromptBundle。
敏感引用需 Policy 检查。
```

---

## B5.E3 HandoffPolicy

任务：

```text
B5.3.1 定义 HandoffPolicy。
B5.3.2 校验 fromAgent / toAgent。
B5.3.3 校验 handoffMode。
B5.3.4 校验 artifactRefs。
B5.3.5 校验 memoryRefs。
B5.3.6 requireApprovalForSensitiveArtifacts 生效。
B5.3.7 allowFullContext=false 时 full_context 被拒绝。
```

验收：

```text
HandoffPolicy 不可绕过。
高风险交接进入 approval 或 denied。
```

---

## B5.E4 origin.agent.delegate

任务：

```text
B5.4.1 注册 origin.agent.delegate 工具。
B5.4.2 定义 AgentDelegateInput。
B5.4.3 支持 toAgentId。
B5.4.4 支持 capabilityQuery。
B5.4.5 调 capability-discovery 找目标 Agent。
B5.4.6 调 HandoffPolicy。
B5.4.7 创建 HandoffContextPackage。
B5.4.8 创建 ChildTask。
B5.4.9 启动目标 AgentRun。
B5.4.10 结果回流 ParentTask。
```

验收：

```text
Agent A 可委派给 Agent B。
Agent B 重新构建自己的 WorkView。
ChildTask 结果回流 ParentTask。
```

---

## B5.E5 External Collaboration

任务：

```text
B5.5.1 定义 ExternalTaskBinding。
B5.5.2 创建 external_task_bindings 表。
B5.5.3 定义 CollaborationProvider。
B5.5.4 实现 ArrayBridge stub。
B5.5.5 task.message + @agent → AgentEnvelope。
B5.5.6 attachment → ArtifactRef。
B5.5.7 reply → external message。
B5.5.8 waiting_input → external message。
B5.5.9 artifact.created → external attachment stub。
```

验收：

```text
Clean Core 不实现 Group。
外部协作只通过 CollaborationContext / ExternalTaskBinding 接入。
```

---

## B5.E6 Integration Regression

任务：

```text
B5.6.1 普通 reply E2E。
B5.6.2 多轮 tool E2E。
B5.6.3 approval E2E。
B5.6.4 publish / rollback E2E。
B5.6.5 eval.run E2E。
B5.6.6 origin.agent.delegate E2E。
B5.6.7 external tools.invoke E2E。
B5.6.8 private tool denied E2E。
B5.6.9 handoff sensitive artifact approval E2E。
```

验收：

```text
Batch 0～5 完成后，Clean Core Alpha 功能完成。
```

---

# Batch 6：Clean Core 上线硬化与发布验收

## B6.E1 Contract Freeze Check

任务：

```text
B6.1.1 检查 AgentEnvelope 字段。
B6.1.2 检查 Task / TaskEvent 字段。
B6.1.3 检查 AgentRun 字段。
B6.1.4 检查 Decision 字段。
B6.1.5 检查 ToolCall / ToolResult 字段。
B6.1.6 检查 TraceEvent / AuditEvent 字段。
B6.1.7 检查 AgentHandoff / HandoffContextPackage 字段。
B6.1.8 输出 v1.0-beta Contract Freeze Report。
```

验收：

```text
核心结构进入 v1.0-beta freeze。
```

---

## B6.E2 Migration Readiness

任务：

```text
B6.2.1 空库 migration up。
B6.2.2 重复 migration 检查。
B6.2.3 索引检查。
B6.2.4 tenant_id 查询检查。
B6.2.5 JSONB 缺省兼容检查。
B6.2.6 migration 失败回滚说明。
B6.2.7 输出 Migration Readiness Report。
```

验收：

```text
测试环境可从空库稳定创建 Clean Core schema。
```

---

## B6.E3 API / Command Freeze

任务：

```text
B6.3.1 冻结 agent.run。
B6.3.2 冻结 task.start。
B6.3.3 冻结 task.command。
B6.3.4 冻结 tools.invoke。
B6.3.5 冻结 agent.package.*。
B6.3.6 冻结 eval.run。
B6.3.7 冻结 origin.agent.delegate。
B6.3.8 定义统一 Error Response。
```

验收：

```text
核心命令 request / response / error / trace / audit 定义完整。
```

---

## B6.E4 Core Auth 最小安全

任务：

```text
B6.4.1 定义 CallerIdentity。
B6.4.2 实现 service-to-service token。
B6.4.3 校验 tenant_id。
B6.4.4 定义 runtime_caller / optimizer / admin。
B6.4.5 tools.invoke 权限校验。
B6.4.6 package.publish 权限校验。
B6.4.7 policy.update 权限校验。
B6.4.8 origin.agent.delegate 权限校验。
```

验收：

```text
匿名调用失败。
跨 tenant 调用失败。
普通调用方不能发布 AgentPackage。
```

---

## B6.E5 Idempotency / Recovery Check

任务：

```text
B6.5.1 重复 agent.run 测试。
B6.5.2 重复 task.command 测试。
B6.5.3 重复 approve_action 测试。
B6.5.4 重复 tool_call 测试。
B6.5.5 重复 origin.agent.delegate 测试。
B6.5.6 服务重启恢复 Task 测试。
B6.5.7 Run failed 后一致性测试。
```

验收：

```text
重复请求不产生重复副作用。
服务重启后状态可恢复。
```

---

## B6.E6 Runaway Protection

任务：

```text
B6.6.1 MaxSteps。
B6.6.2 MaxToolCalls。
B6.6.3 MaxDuration。
B6.6.4 MaxPromptTokens。
B6.6.5 MaxHandoffDepth。
B6.6.6 MaxChildTasks。
B6.6.7 MaxRepairAttempts。
B6.6.8 MaxModelRetries。
B6.6.9 MaxConsecutiveToolFailures。
```

验收：

```text
坏 Prompt / 坏 Tool 不会导致无限循环。
```

---

## B6.E7 Trace / Audit Query

任务：

```text
B6.7.1 trace.get(run_id)。
B6.7.2 task.timeline(task_id)。
B6.7.3 audit.search(filters)。
B6.7.4 handoff.trace(handoff_id)。
B6.7.5 tool.trace(tool_call_id)。
B6.7.6 external_task lookup。
```

验收：

```text
上线后可以排查完整 AgentRun 和 Handoff。
```

---

## B6.E8 Core E2E Regression

任务：

```text
B6.8.1 普通 reply。
B6.8.2 ask_clarification resume。
B6.8.3 low risk tool。
B6.8.4 high risk approval。
B6.8.5 tool failed repair / fail。
B6.8.6 publish new version。
B6.8.7 old run version pinned。
B6.8.8 eval.run。
B6.8.9 delegate child task。
B6.8.10 external tools.invoke exposed only。
B6.8.11 private tool denied。
B6.8.12 full_context denied。
B6.8.13 duplicate tool_call no side effect。
B6.8.14 optimistic lock conflict recovery。
```

验收：

```text
所有 E2E 回归通过。
```

---

## B6.E9 Deployment Unit

任务：

```text
B6.9.1 Dockerfile。
B6.9.2 config example。
B6.9.3 health endpoint。
B6.9.4 readiness endpoint。
B6.9.5 migration command。
B6.9.6 graceful shutdown。
B6.9.7 trace_id log format。
```

验收：

```text
clean-core-server 可在测试环境部署、启动、停止、迁移。
```

---

## B6.E10 Release / Rollback

任务：

```text
B6.10.1 Clean Core 服务版本号。
B6.10.2 DB migration 版本。
B6.10.3 配置版本。
B6.10.4 rollback checklist。
B6.10.5 disable_agent 开关。
B6.10.6 disable_tool 开关。
B6.10.7 disable_handoff 开关。
B6.10.8 disable_external_tools_invoke 开关。
B6.10.9 发布演练。
B6.10.10 回滚演练。
```

验收：

```text
Clean Core 服务本身可发布、可回滚。
```

---

## B6.E11 Go / No-Go

任务：

```text
B6.11.1 汇总 Contract Freeze。
B6.11.2 汇总 Migration Readiness。
B6.11.3 汇总 Auth 检查。
B6.11.4 汇总 E2E Regression。
B6.11.5 汇总未关闭 P0 / P1。
B6.11.6 形成 Go / No-Go 记录。
```

Go 条件：

```text
无 P0 blocker。
无核心安全问题。
Trace / Audit 完整。
Policy 不可绕过。
任务状态可恢复。
Clean Core 可部署、可回滚。
```

---

# 横向任务轨道

## T1 Contract Tests

覆盖：

```text
AgentEnvelope serialization
TaskStatus transition
Decision schema
ToolCall / ToolResult
AgentHandoff
HandoffContextPackage
Trace / Audit
```

---

## T2 Repository Tests

覆盖：

```text
Create / Get / Update
Optimistic lock
Append-only
Idempotency key
Tenant filter
Index query
```

---

## T3 Policy Tests

覆盖：

```text
Tool allowed
Tool denied
Approval required
Handoff denied
Full context denied
Sensitive artifact approval
Package publish permission
```

---

## T4 Prompt Tests

覆盖：

```text
PromptBundle source isolation
ToolResult summary rendering
ArtifactRef rendering
HandoffContext rendering
Injection guard baseline
```

---

## T5 E2E Tests

覆盖：

```text
reply
ask
tool
approval
repair
publish
eval
handoff
external invoke
rollback
```

---

# 当前不做内容

仍然不进入 Clean Core 的内容：

```text
完整 Group 系统
群消息系统
实时通知系统
Worker Profile 页面
Worker 评价
复杂任务池订阅
完整前端管理台
复杂可视化 Trace UI
复杂多模型路由
复杂 embedding 检索
复杂工作流编排器
完整商业计费系统
```

这些属于产品层、协作层或后续增强。

---

# 最终结论

v1.2-alpha 的开发任务拆解标准是：

```text
不是“里程碑级粗拆”，
而是“可以逐项派给开发者的 WBS 拆解”。
```

开发方式建议：

```text
先按 Batch 顺序推进。
每个 Batch 内 Epic 可以并行。
每个 Feature 都必须有测试。
Batch 0～5 完成后进入 Clean Core Alpha。
Batch 6 完成后 Clean Core 具备核心服务上线条件。
```
