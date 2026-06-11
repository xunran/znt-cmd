# 原智能体 Clean Core 全景开发设计文档

版本：v1.2 Clean Development Architecture
日期：2026-05-29
定位：供核心研发团队开发使用的全景开发文档
更新重点：补充可变资源、运行中动态刷新、外部入口、Decision Outcome、管理工具、外部 tools.invoke、版本钉住、Plan-guided 多工具任务执行、Eval Suite、AgentPackage 发布/灰度/回滚、优化人员与开发人员边界、Agent-to-Agent Handoff、外部协作上下文与受控上下文交接。
技术栈建议：Golang-only Core
整理说明：本版只做结构精修和边界收紧，不新增核心模块；Group / TaskPool / 消息通知 / Worker Profile 仍明确属于外部 Collaboration Plane，不进入 Clean Core。

---

## 0. 文档目标

本文档用于指导“原智能体 Clean Core”的核心开发工作。

它不是产品宣传文档，也不是概念白皮书，而是研发侧可以据此拆模块、写接口、定边界、实现主链路的开发架构文档。

本文档重点回答：

1. 核心系统应该拆成哪些模块？
2. 每个模块负责什么，不负责什么？
3. 模块之间如何依赖，哪些依赖必须禁止？
4. 一次 AgentRun 从输入到输出的完整链路是什么？
5. AgentDefinition、AgentPackage、AgentInstance、AgentRun、Task、WorkView、PromptBundle、Decision、ToolCall、ToolResult 之间如何协作？
6. 哪些能力写死在 Runtime Kernel 中？
7. 哪些能力进入 Policy，可配置、可版本化、可审计？
8. 哪些内容属于 AgentPackage，可由 Agent 作者编辑？
9. 哪些内容属于运行事实，只能由系统追加和变更？
10. Golang 代码目录和接口应该如何组织？
11. 开发者应该如何按照边界顺畅开发？

---

## 1. 核心定位

原智能体不是一个简单 Agent Demo，也不是单纯的大模型聊天应用。

它的核心定位是：

```text
可治理的 Agent Runtime Core
```

它要解决的是：

```text
统一智能体定义
+ 统一运行内核
+ 可持续任务状态
+ 可控模型决策
+ 可治理工具执行
+ 可配置运行策略
+ 可替换执行域
+ 可追踪可审计运行过程
```

核心公式：

```text
Agent = Agent Runtime + AgentDefinition + Runtime Context
```

其中：

```text
Agent Runtime：统一运行机制
AgentDefinition：智能体身份、提示词、Skill、Tool、Memory、Policy、运行约束
Runtime Context：租户、用户、会话、任务、权限、当前输入、执行域等上下文
```

进一步扩展为开发侧关系：

```text
AgentPackage
  ↓ compile / validate / publish
AgentDefinition
  ↓ bind RuntimeContext
AgentInstance
  ↓ run
AgentRun
  ↓ optional long task
Task
```

---

## 2. 核心设计原则

### 2.1 高内聚、低耦合

模块不按对象拆，而按变化原因拆。

错误拆法：

```text
agent-run 模块
tool-call 模块
tool-result 模块
decision 模块
prompt-bundle 模块
```

这会导致模块过碎，业务流程被切碎，调用关系混乱。

正确拆法：

```text
定义域
运行域
任务域
上下文域
能力发现域
模型决策域
模型访问域
策略域
工具域
执行域
记忆产物域
治理域
```

每个模块都围绕一个稳定问题域：

```text
Agent 是什么？
运行如何推进？
任务如何恢复？
模型应该看到什么？
能力如何发现？
模型输出如何变成 Decision？
工具能不能执行？
工具在哪里执行？
记忆和产物如何管理？
过程如何审计？
```

### 2.2 稳定内核 + 策略驱动 + 包定义行为 + 事件记录事实

核心分层：

```text
Runtime Kernel：稳定内核，不可随意编辑
Policy：策略层，可配置、可版本化、可审计
AgentPackage：行为定义层，可编辑、可发布、可回滚
Runtime State：运行事实层，只能由系统追加或通过命令变更
```

对应关系：

```text
Go 代码固定：
- AgentRun 生命周期
- Task 基础状态机
- DecisionType 集合
- ToolCall / ToolResult schema
- Tool Policy 必经链路
- Trace / Audit 必写点

Policy 控制：
- 工具失败后如何 repair
- 什么风险等级需要审批
- PromptBundle 最大 token
- ToolResult 是否摘要
- 长任务如何 checkpoint
- 哪些工具允许自动重试

AgentPackage 定义：
- 这个 Agent 是谁
- 它有哪些 Skill
- 它能用哪些工具
- 它默认怎么工作
- 它的输出格式是什么

Runtime State 记录：
- 发生了什么
- 谁触发的
- 哪个工具被调用
- 结果是什么
- 谁审批了
- 哪一步失败了
```

### 2.3 事实源边界

必须明确哪些东西是真实事实，哪些只是投影。

```text
Task / TaskEvent：任务状态事实
ToolResult：工具执行事实
Artifact：产物事实
MemoryStore：记忆事实
TraceEvent：过程事实
AuditEvent：行为与权限事实
AgentDefinition：智能体定义事实
WorkView：运行时事实聚合视图
PromptBundle：给模型看的上下文投影
```

关键原则：

```text
PromptBundle 不是事实源。
WorkView 不是长期存储。
Markdown 不是内部执行事实源。
模型输出不是事实，必须经过 Decision Validator。
工具结果不是可直接信任事实，必须经过 ToolResult Validator。
```

### 2.4 不强加非必要协议

第一阶段核心不强加 MCP、LangChain、LangGraph、某个云厂商 Managed Agent Runtime。

原则：

```text
不用的东西不要进入核心。
需要接入时，通过 adapter 接入。
adapter 不能反向污染 Runtime Core。
```

如果未来接 MCP，它只是 Tool Adapter。

如果未来接 Google Managed Agent / 其他托管 Agent Runtime，它只是 ExecutionDomain / Tool Runtime 后端。


### 2.5 v1.2 关键边界

v1.2 在 13 个核心模块和主运行链路基础上，进一步明确开发时高频出现的动态变化、版本钉住、外部入口、协作上下文和 Agent-to-Agent Handoff 边界：

```text
1. 提示词、Skill、ToolBinding 可以编辑，但必须进入 AgentPackage Draft / Proposal / Publish 流程。
2. 运行中动态变化的是 WorkView / PromptBundle / CandidateSet，不是 AgentDefinition / ToolRegistry。
3. AgentRun 默认钉住 AgentDefinition version、Policy version、ToolDefinition version、Skill version，保证可审计和可回放。
4. 长任务是否升级到新 AgentDefinition，必须通过显式 TaskCommand 或策略决定，不能静默升级。
5. @mention、slash command、channel routing 属于 AgentEnvelope 生成前的输入路由层，不进入 Decision Loop。
6. Decision 需要表达 answer / refusal / policy_notice / clarification 等结果语义。
7. Skill 创建、提示词更新、工具绑定修改属于管理类工具，只能操作 Draft 或 Proposal，不能直接改线上运行定义。
8. 外部 tools.invoke 只能调用 Agent 显式暴露的 exposedToolIds，不能直接调用内部私有工具。
9. 所有管理类工具和外部工具调用都必须经过 Policy / Audit。
10. 多工具复杂任务可以通过 TaskPlan / PlanStep 引导，但 Plan 不是硬编码工作流。
11. 提示词、Skill、ToolBinding、Policy 优化必须通过 Eval Suite 验证。
12. AgentPackage / Policy 发布必须支持版本、灰度、回滚。
13. 优化人员可以改 Package / Policy / Eval，不应改 Runtime Kernel。
14. Clean Core 不增加完整 Group / TaskPool / 消息通知系统，只接收外部 CollaborationContext。
15. Array / Slack / 飞书等外部协作系统通过 ExternalTaskBinding 与 CoreTask 映射。
16. Agent-to-Agent 协作通过 AgentHandoff 和 HandoffContextPackage 实现。
17. 跨 Agent 上下文交接必须经过 HandoffPolicy 和 Audit。
```

一句话：

```text
运行中可以动态“投影”和“选择”，不能动态“篡改定义”和“绕过治理”。
```


---

## 3. 全景模块划分

核心划分为：

```text
0. core-contracts        共享契约层

1. agent-definition      智能体定义与包管理
2. runtime-kernel        运行内核
3. task-runtime          长任务状态
4. context-engine        WorkView / PromptBundle 构建
5. capability-discovery  Capability / Skill / Tool 候选发现
6. decision-engine       结构化决策与校验
7. model-runtime         大模型访问与结构化输出适配
8. policy-engine         策略、审批、修复、压缩、恢复规则
9. tool-runtime          工具治理与执行
10. execution-domain     Worker / Sandbox / Managed Runtime 执行域
11. memory-artifact      记忆、文件、产物资产
12. governance           Trace / Audit / Metrics / Replay
```

也可以归为四大层：

```text
定义层：
- core-contracts
- agent-definition

运行层：
- runtime-kernel
- task-runtime
- context-engine
- capability-discovery
- decision-engine
- model-runtime

治理执行层：
- policy-engine
- tool-runtime
- execution-domain

状态支撑层：
- memory-artifact
- governance
```

---

## 4. 全景运行链路

一次完整运行如下：

```text
External Caller
  ↓
AgentEnvelope
  ↓
runtime-kernel 创建 AgentRun
  ↓
agent-definition 加载 AgentDefinition / AgentPackage
  ↓
runtime-kernel 创建 AgentInstance
  ↓
task-runtime 创建或恢复 Task
  ↓
context-engine 构建 WorkView
  ↓
capability-discovery 召回候选 Capability / Skill / Tool
  ↓
context-engine 渲染 PromptBundle
  ↓
decision-engine 调用 model-runtime 生成 Decision
  ↓
decision-engine 校验 Decision
  ↓
runtime-kernel 分发 Decision
  ├── reply → Response
  ├── ask_clarification → task-runtime waiting_input
  ├── unsupported / error → terminal
  └── tool_call
        ↓
      tool-runtime
        ↓
      policy-engine 权限 / 风险 / 审批 / repair 策略判断
        ↓
      execution-domain 选择执行环境
        ↓
      tool-runtime 执行工具
        ↓
      ToolResult Validator
        ↓
      memory-artifact 保存 Artifact / 结果引用
        ↓
      task-runtime 追加 TaskEvent
        ↓
      governance 写 Trace / Audit
        ↓
      runtime-kernel 判断继续循环或结束
```

运行过程中的每个关键节点都必须写 Trace。
涉及权限、审批、高风险工具、配置变更、外部副作用的节点必须写 Audit。

### 4.1 输入入口与 AgentEnvelope 生成边界

`@agent`、`/command`、频道消息、线程回复、Webhook 原始 payload 等不应该直接进入 Runtime Kernel。

它们属于 AgentEnvelope 生成前的输入适配层。

```text
Raw Channel Message
  ↓
Input Adapter / Channel Adapter
  ↓
Mention Parser / Command Parser / Route Resolver
  ↓
AgentEnvelope
  ↓
runtime-kernel
```

典型例子：

```text
原始消息：
@data-agent 帮我分析这个 CSV

解析后：
target.agentId = data-agent
payload.input = 帮我分析这个 CSV
caller = 当前用户 / 当前渠道
context.channel = 原始渠道
context.threadId = 原始会话线程
```

边界：

```text
@mention 检测不属于 decision-engine。
@mention 检测不属于 tool-runtime。
@mention 检测不属于模型能力。
Clean Core 接收的是已经标准化后的 AgentEnvelope。
```

如果后续需要支持 IM、Webhook、CLI、API 等多入口，应在核心外增加：

```text
input-gateway
channel-adapter
mention-parser
command-parser
route-resolver
```

这些不是第 14 个核心模块，而是 AgentEnvelope 之前的外围适配层。

---

### 4.2 运行中动态刷新边界

Agent 运行是多轮 Decision Loop。每一轮都可以重新构建运行上下文，但不能随意修改运行定义。

每轮可以动态刷新：

```text
WorkView
PromptBundle
candidateCapabilities
candidateSkills
candidateTools
Memory 摘要
Artifact 摘要
ToolResult 摘要
Context Compression 结果
PromptPolicy 运行参数
```

每轮不应该直接动态修改：

```text
AgentDefinition
AgentPackage Published Version
ToolRegistry
ToolDefinition
SkillDefinition Published Version
PermissionPolicy Published Version
```

正确理解：

```text
动态构建 PromptBundle ≠ 动态修改提示词定义
动态注入候选工具 ≠ 动态注册工具
动态加载 SkillInstruction ≠ 动态发布 Skill
动态应用 Policy ≠ Agent 自己改 Policy
```

因此运行中的“动态”应被限制在：

```text
状态聚合
上下文投影
候选能力选择
策略参数应用
任务状态推进
```

不应扩展到：

```text
线上定义篡改
工具注册篡改
权限策略篡改
未经审批的自我进化
```

---

### 4.3 版本钉住与长任务升级

为了保证运行可审计、可回放、可恢复，AgentRun 必须记录并默认钉住以下版本：

```text
AgentDefinition version
AgentPackage version
PolicySet version
ToolDefinition version
SkillDefinition version
PromptBundle hash
Model provider / model version
```

建议规则：

```text
1. 一个 AgentRun 内不静默切换 AgentDefinition。
2. 一个 AgentRun 内不静默切换 PolicySet。
3. 一个 AgentRun 内不静默启用新发布的工具。
4. 一个长 Task 默认继续使用创建时的 AgentDefinition major version。
5. 长 Task 是否升级到新版本，需要显式 TaskCommand 或 TaskUpgradePolicy。
```

长任务升级流程：

```text
task.command(upgrade_agent_version)
  ↓
policy-engine 判断是否允许升级
  ↓
task-runtime 记录 upgrade event
  ↓
runtime-kernel 在下一次 AgentRun 加载新版本
  ↓
governance 写 Audit
```

不允许：

```text
系统后台发布新版 AgentPackage 后，正在运行的 Task 静默换行为。
```

允许：

```text
新创建的 AgentRun 使用新版本。
管理员显式升级某个 Task。
策略允许低风险 minor version 自动升级。
```

---

### 4.4 多工具任务执行模式

复杂任务往往不是一次模型输出或一次工具调用能够解决，而是需要多个工具组合。

Clean Core 应支持三种执行模式：

```text
1. Free Decision Loop
   模型自由判断下一步，适合简单问答和轻量任务。

2. Skill-guided Loop
   通过 SKILL.md 给出方法步骤，适合中等复杂任务。

3. Plan-guided Loop
   通过 TaskPlan / PlanStep 显式管理步骤，适合复杂长任务、多工具、多产物、审批任务。
```

三者关系：

```text
Free Decision Loop：没有显式计划，模型逐轮决策。
Skill-guided Loop：Skill 指导模型如何做，但不保存显式步骤状态。
Plan-guided Loop：TaskRuntime 保存计划和步骤状态，Decision Loop 执行每一步。
```

Plan-guided 不等于把流程写死。

```text
Plan 是可调整的任务路线图。
Decision Loop 仍然决定每一步具体如何执行。
ToolRuntime 仍然执行工具。
PolicyEngine 仍然判断权限和审批。
TaskRuntime 负责保存 Plan 状态。
```

示例：

```text
用户目标：分析 CSV，找异常值，生成图表和报告

TaskPlan:
1. 检查文件结构
2. 清洗数据
3. 检测异常值
4. 生成图表
5. 创建报告 Artifact
6. 输出总结
```

每个 PlanStep 可以产生 ToolCall、ToolResult、ArtifactRef 和 TaskEvent。

---

### 4.5 外部协作上下文与 Agent-to-Agent Handoff

Clean Core 不负责完整协作平台能力，只接收外部协作上下文引用。

不进入 Clean Core 的能力：

```text
Group / 群组管理
TaskPool / 任务池
群消息
@ 提及解析
私密消息
实时通知
附件上传下载
Worker Profile
Worker 工作负载
组织邀请码
服务评价
```

这些属于外部 Collaboration Plane，例如 Array。

Clean Core 只接收标准化后的协作上下文：

```text
CollaborationContext
ExternalTaskBinding
ExternalMessageRef
ExternalArtifactRef
```

以及负责受控的 Agent-to-Agent 任务交接：

```text
AgentHandoff
HandoffContextPackage
HandoffPolicy
origin.agent.delegate
```

推荐架构：

```text
Array / 外部协作系统
  ↓
Collaboration Bridge
  ↓
AgentEnvelope + CollaborationContext
  ↓
Clean Core
  ↓
AgentRun / CoreTask / Handoff / Artifact / Trace / Audit
  ↓
Bridge 回写外部任务消息 / 附件 / 状态
```

边界：

```text
外部协作系统管“谁和谁协作、消息怎么流转、任务怎么通知”。
Clean Core 管“Agent 如何执行任务、如何交接、如何调用工具、如何记录审计”。
```

---

### 4.6 Handoff 上下文交接模式

当一个 Agent 将任务交给另一个 Agent 时，不应默认把完整聊天记录和全部上下文塞给下游 Agent。

Clean Core 支持四种交接模式：

```text
full_context：完整上下文交接，适合小任务、低敏场景。
summary_only：只交接摘要，适合外部 Agent 或低权限场景。
reference_only：只交接引用，由下游 Agent 自行查询授权上下文。
hybrid：摘要 + 关键事实 + ArtifactRef + 可查询范围，推荐默认模式。
```

推荐默认：

```text
hybrid
```

原因：

```text
1. 避免 PromptBundle 爆炸。
2. 避免泄漏上游 Agent 可见但下游 Agent 无权访问的信息。
3. 避免下游 Agent 被上游 Agent 的推理视角污染。
4. 让下游 Agent 基于自己的 AgentDefinition、Skill、Tool 和 Policy 重新构建 WorkView。
```

正确流程：

```text
Agent A 生成 Handoff
  ↓
policy-engine 判断是否允许交接
  ↓
context-engine 生成 HandoffContextPackage
  ↓
task-runtime 创建 Child Task / Handoff 事件
  ↓
Agent B 基于 HandoffContextPackage 构建自己的 WorkView
  ↓
Agent B 执行
  ↓
结果通过 ArtifactRef / TaskEvent 回流 Parent Task
```


---

## 5. 模块 0：core-contracts

### 5.1 定位

`core-contracts` 是共享契约层。

它不属于业务模块，只保存跨模块稳定使用的基础类型、枚举、错误码和接口协议。

### 5.2 包含内容

```text
ID 类型：
- AgentID
- AgentVersion
- AgentRunID
- TaskID
- TraceID
- SpanID
- ArtifactID
- ToolCallID
- DecisionID

基础枚举：
- RiskLevel
- Visibility
- DecisionType
- RunStatus
- TaskStatus
- ToolResultStatus
- ApprovalStatus

基础结构：
- AgentEnvelope
- AgentTarget
- AgentCaller
- RuntimeContext
- CollaborationContext
- ExternalTaskBinding
- ExternalMessageRef
- ExternalArtifactRef
- Permission
- ArtifactRef
- HandoffMode
- ErrorCode
- RuntimeError
```

### 5.2.1 CollaborationContext

`CollaborationContext` 只保存外部协作上下文引用，不管理协作系统本身。

```ts
interface CollaborationContext {
  provider: "array" | "slack" | "feishu" | "wechat" | "custom";

  externalWorkspaceId?: string;
  externalGroupId?: string;
  externalChannelId?: string;
  externalThreadId?: string;
  externalTaskId?: string;
  externalMessageId?: string;

  callerId: string;
  callerType: "user" | "agent" | "worker" | "system";

  replyTarget?: {
    type: "task" | "message" | "thread" | "webhook";
    id: string;
  };
}
```

边界：

```text
CollaborationContext 不是 Group 模块。
CollaborationContext 不保存成员列表。
CollaborationContext 不处理通知。
CollaborationContext 不处理消息流。
```

它的作用是让 Clean Core 知道：

```text
这次 AgentRun 来自哪个外部任务。
结果应该回写到哪里。
Trace / Audit 应该记录哪个外部来源。
Policy 判断是否需要参考外部协作权限。
```

### 5.2.2 ExternalTaskBinding

外部任务和 Clean Core 任务必须分开建模。

```ts
interface ExternalTaskBinding {
  provider: "array" | "custom";
  externalTaskId: string;
  coreTaskId: string;
  syncMode: "one_way" | "two_way";
  createdAt: string;
}
```

边界：

```text
Array Task / 外部 Task = 协作层任务事实。
Clean Core Task = 执行层任务事实。
ExternalTaskBinding = 两者映射关系。
```

不要把外部任务状态直接当成 CoreTask 状态。


### 5.3 不包含内容

不能把业务逻辑塞进 `core-contracts`。

禁止包含：

```text
AgentLoader 实现
Task 状态机逻辑
Tool 执行逻辑
Policy 判断逻辑
Model 调用逻辑
Trace 存储逻辑
```

### 5.4 开发要求

`core-contracts` 必须：

```text
稳定
小
无外部重依赖
不依赖其他业务模块
```

一旦这个层变大，会成为全局垃圾桶。

---

## 6. 模块 1：agent-definition

### 6.1 定位

负责智能体定义、包管理、版本发布和加载。

它回答：

```text
这个 Agent 是谁？
它有哪些 Prompt / Skill / Tool / Memory / Policy？
当前运行应该加载哪个版本？
```

### 6.2 核心对象

```text
AgentDefinition
AgentPackage
AgentManifest / AgentCard
SkillDefinition
SkillCard
SkillInstruction
SkillResourceRef
ToolBinding
MemoryPolicyRef
PermissionPolicyRef
RuntimeProfileRef
ExecutionDomainRef
```

### 6.3 AgentPackage 推荐结构

```text
AgentPackage =
  AGENTS.md
  agent.yaml
  prompts/
    system.md
    developer.md
  skills/
    data-analysis/
      SKILL.md
      skill.yaml
      references/
      examples/
      scripts/
  tool-bindings.yaml
  memory-policy.yaml
  permission-policy.yaml
  evals.yaml
  release.yaml
  metadata.yaml
```

边界：

```text
AGENTS.md / SKILL.md：人类友好的作者格式
agent.yaml / skill.yaml：机器可校验的定义格式
AgentDefinition / SkillDefinition：Runtime 实际加载的内部执行格式
```

### 6.4 主要职责

```text
1. 解析 AgentPackage。
2. 编译 AGENTS.md 为 AgentDefinition 的 prompt/identity 部分。
3. 编译 SKILL.md 为 SkillInstruction。
4. 编译 skill.yaml 为 SkillCard。
5. 校验工具绑定是否合法。
6. 校验权限策略引用是否合法。
7. 校验 Memory 策略是否合法。
8. 发布 AgentPackage 版本。
9. 根据 agentId/version 加载 AgentDefinition。
10. 输出可运行的 Validated AgentDefinition。
```

### 6.5 不负责

```text
不负责执行 Agent。
不负责调用模型。
不负责执行工具。
不负责构建 PromptBundle。
不负责写 Task 状态。
不负责写 Audit，但发布行为要通知 governance 记录。
```

### 6.6 对外接口示例

```go
type AgentLoader interface {
    Load(ctx context.Context, ref AgentRef) (*AgentDefinition, error)
}

type AgentCompiler interface {
    CompilePackage(ctx context.Context, pkg AgentPackageSource) (*CompiledAgentPackage, error)
}

type AgentPublisher interface {
    Publish(ctx context.Context, pkg CompiledAgentPackage, opts PublishOptions) (*AgentPackageVersion, error)
}

type SkillLoader interface {
    LoadSkill(ctx context.Context, skillID string, version string) (*SkillDefinition, error)
}
```

### 6.7 关键边界

AgentDefinition 是内部执行蓝图，不是对外展示卡片。

```text
AgentDefinition：内部执行
AgentManifest / AgentCard：外部发现
AgentPackage：可发布资产包
AGENTS.md：作者编辑入口
```


### 6.8 AgentPackage Draft / Proposal / Publish 机制

提示词、Skill、ToolBinding、MemoryPolicy、PermissionPolicy 都属于 AgentPackage 资产。它们可以被编辑，但必须经过 Draft / Validate / Publish 流程。

推荐生命周期：

```text
draft
  ↓
validate
  ↓
test / eval
  ↓
review
  ↓
publish
  ↓
rollback
```

运行中 Agent 可以提出变更建议，但不能直接修改线上版本。

```text
PromptPatchProposal
SkillProposal
ToolBindingProposal
MemoryPolicyProposal
PermissionPolicyProposal
```

Proposal 状态：

```text
draft
pending_review
approved
rejected
published
superseded
```

关键原则：

```text
Agent 可以建议变更。
工具可以编辑 Draft。
只有 Publish 才能形成新版本。
当前 AgentRun 不因 Draft 改动而改变行为。
```

---

### 6.9 Prompt / Skill / ToolBinding 管理工具

系统可以提供管理类工具，但这些工具必须区别于普通任务工具。

推荐管理工具：

```text
agent.package.draft.create
agent.package.draft.patch_agents_md
agent.package.draft.patch_prompt
agent.package.draft.add_skill
agent.package.draft.update_skill
agent.package.draft.remove_skill
agent.package.draft.update_tool_binding
agent.package.draft.validate
agent.package.draft.publish
agent.package.rollback
```

或者按资源拆分：

```text
prompt.draft.patch
skill.draft.create
skill.draft.update
skill.draft.delete
skill.draft.validate
skill.draft.publish
toolbinding.draft.update
```

管理类工具边界：

```text
1. 默认只能修改 Draft。
2. 发布必须经过权限校验和审计。
3. 不能直接修改当前运行中的 AgentDefinition。
4. 不能让模型在同一个 run 中创建 Skill 并立即使用为正式 Skill。
5. 高风险修改必须进入 waiting_approval。
```

示例：通过工具更新提示词的正确流程：

```text
tool_call: agent.package.draft.patch_prompt
  ↓
修改 Draft
  ↓
Compile / Validate
  ↓
生成 diff
  ↓
等待审批或测试
  ↓
Publish 新 AgentPackage version
  ↓
后续 run 加载新版本
```

错误方式：

```text
tool_call 直接改线上 system prompt
  ↓
当前 run 立即行为漂移
```

这种方式禁止。


### 6.10 Eval Suite

AgentPackage 必须支持 Eval Suite，用于让提示词、Skill、ToolBinding、Policy 优化可测试、可回归。

推荐文件：

```text
evals.yaml
evals/
  csv_anomaly_report.yaml
  refusal_policy.yaml
  tool_selection.yaml
```

EvalCase 应覆盖：

```text
输入样例
期望输出结构
必须调用的工具
禁止调用的工具
期望进入的状态
期望生成的 Artifact
安全边界
不应泄漏的信息
```

示例：

```yaml
evals:
  - name: csv_anomaly_report
    input: "分析这个 CSV，找异常值并生成报告"
    expected:
      mustCallTools:
        - file.read
        - data.analyze
        - artifact.create
      shouldNotCallTools:
        - file.delete
      finalReplyContains:
        - "异常值"
        - "报告"
      expectedArtifacts:
        - type: report
```

Eval 结果应写入治理数据：

```text
eval.run.started
eval.case.completed
eval.case.failed
eval.summary.created
```

Eval 不属于 Runtime Kernel，但 AgentPackage 发布流程必须使用它。

---

### 6.11 Release / Canary / Rollback

AgentPackage 和 Policy 的优化不能直接上线，必须支持发布、灰度和回滚。

推荐发布状态：

```text
draft
validated
evaluated
reviewed
published
canary
stable
deprecated
rolled_back
```

推荐发布流程：

```text
Draft 修改
  ↓
Compile / Validate
  ↓
Run Eval Suite
  ↓
Review
  ↓
Publish new version
  ↓
Canary
  ↓
Stable
  ↓
Rollback if needed
```

发布对象包括：

```text
AgentPackage version
Prompt version
Skill version
ToolBinding version
PolicySet version
EvalSuite version
```

发布要求：

```text
1. publish 必须写 Audit。
2. rollback 必须写 Audit。
3. canary 必须记录流量范围和命中 runId。
4. AgentRun 必须记录命中的版本。
5. 长 Task 默认不静默跟随 canary。
```

---

### 6.12 优化人员与开发人员边界

开发人员主要维护：

```text
runtime-kernel
task-runtime
context-engine
decision-engine
model-runtime
tool-runtime
execution-domain
memory-artifact
governance
core contracts
```

优化人员主要维护：

```text
AGENTS.md
SKILL.md
agent.yaml
skill.yaml
tool-bindings.yaml
PromptPolicy
ContextCompressionPolicy
ToolRepairPolicy
ApprovalPolicy
evals.yaml
release.yaml
```

优化人员不应该修改：

```text
DecisionType
Task 基础状态机
Tool Policy 必经链路
ToolResult schema
Trace / Audit 必写点
ExecutionDomain 安全边界
Runtime Kernel 主循环
```

当优化人员发现 Agent 行为不好时，优先调整：

```text
Prompt
Skill
ToolCard.whenToUse
ToolBinding
Policy
EvalCase
```

而不是要求开发人员修改框架代码。


---

## 7. 模块 2：runtime-kernel

### 7.1 定位

`runtime-kernel` 是运行内核，负责一次 AgentRun 的主流程推进。

它回答：

```text
一次运行如何开始？
运行到哪里？
下一步做什么？
什么时候停止？
什么时候等待？
什么时候继续？
```

### 7.2 核心对象

```text
AgentInstance
AgentRun
RunCoordinator
DecisionLoop
RunStep
RunControl
RunStatus
StepResult
```

### 7.3 主要职责

```text
1. 接收 AgentEnvelope。
2. 创建 AgentRun。
3. 加载 AgentDefinition。
4. 绑定 RuntimeContext，形成 AgentInstance。
5. 创建或恢复 Task。
6. 调用 context-engine 构建 WorkView / PromptBundle。
7. 调用 decision-engine 生成 Decision。
8. 根据 Decision 分发。
9. 对 tool_call 调用 tool-runtime。
10. 处理 continue / terminal / waiting_input / waiting_approval。
11. 控制 maxSteps / maxToolCalls / maxDuration。
12. 结束时生成 AgentResponse / TaskResult。
```

### 7.4 不负责

```text
不负责编译 AgentPackage。
不负责模型 SDK 调用。
不负责工具权限判断。
不负责工具实际执行。
不负责 Prompt 细节拼接。
不负责 Trace / Audit 存储实现。
不直接读写 Memory / Artifact 存储。
```

### 7.5 运行主循环伪代码

```go
func (r *RunCoordinator) Run(ctx context.Context, env AgentEnvelope) (*AgentResponse, error) {
    run := r.createRun(env)
    r.gov.Trace(ctx, TraceRunCreated(run))

    def, err := r.agentLoader.Load(ctx, env.Target.AgentRef)
    if err != nil {
        return r.failRun(ctx, run, err)
    }

    instance := NewAgentInstance(def, env.RuntimeContext)

    task, err := r.taskService.LoadOrCreate(ctx, env, instance)
    if err != nil {
        return r.failRun(ctx, run, err)
    }

    for step := 0; step < instance.Runtime.MaxSteps; step++ {
        view, err := r.contextBuilder.BuildWorkView(ctx, WorkViewRequest{
            Run: run,
            Task: task,
            Instance: instance,
        })
        if err != nil {
            return r.failRun(ctx, run, err)
        }

        bundle, err := r.contextBuilder.BuildPromptBundle(ctx, view)
        if err != nil {
            return r.failRun(ctx, run, err)
        }

        decision, err := r.decisionMaker.Decide(ctx, bundle)
        if err != nil {
            return r.failRun(ctx, run, err)
        }

        result, err := r.dispatchDecision(ctx, run, task, instance, decision)
        if err != nil {
            return r.failRun(ctx, run, err)
        }

        if result.Terminal {
            return r.completeRun(ctx, run, result)
        }

        if result.Waiting {
            return r.waitRun(ctx, run, result)
        }
    }

    return r.failRun(ctx, run, ErrMaxStepsExceeded)
}
```

### 7.6 关键边界

runtime-kernel 是编排者，不是万能模块。

它只知道：

```text
下一步调用谁
结果是否继续
状态如何推进
```

它不应该知道：

```text
某个模型 SDK 怎么调用
某个工具怎么执行
某个策略怎么写
某个 Artifact 怎么存
```


---

## 8. 模块 3：task-runtime

### 8.1 定位

负责长任务状态、命令、事件、恢复。

它回答：

```text
多轮 AgentRun 如何推进同一个 Task？
任务现在处于什么状态？
用户输入、审批、取消如何应用到任务？
```

### 8.2 核心对象

```text
Task
TaskStatus
TaskCommand
TaskEvent
TaskCheckpoint
TaskResult
TaskStateMachine
TaskPlan
PlanStep
PlanEvent
AgentHandoff
HandoffContextPackageRef
HandoffEvent
```

### 8.2.1 TaskPlan / PlanStep

TaskPlan 用于复杂任务的多步骤执行引导。

```ts
interface TaskPlan {
  planId: string;
  taskId: string;
  objective: string;
  status: "draft" | "active" | "completed" | "failed" | "replanned";
  steps: PlanStep[];
  createdBy: "model" | "user" | "system";
  createdAt: string;
  updatedAt: string;
}

interface PlanStep {
  stepId: string;
  title: string;
  description: string;
  expectedToolHints?: string[];
  status: "pending" | "running" | "completed" | "failed" | "skipped";
  resultRefs?: ArtifactRef[];
  failureReason?: string;
}
```

PlanStep 不直接执行工具，只提供当前任务阶段和意图。

```text
PlanStep 说明“当前该做什么”。
Decision 决定“具体调用什么工具或如何回复”。
ToolRuntime 执行“具体动作”。
TaskRuntime 保存“步骤状态”。
```

### 8.2.2 AgentHandoff

`AgentHandoff` 表示一个 Agent 将任务或子任务交给另一个 Agent。

```ts
interface AgentHandoff {
  handoffId: string;
  parentTaskId: string;
  childTaskId?: string;

  fromAgentId: string;
  toAgentId: string;

  objective: string;
  reason: string;

  contextPackageRef: string;
  artifactRefs: ArtifactRef[];

  expectedOutput?: {
    format: "text" | "markdown" | "json" | "artifact";
    schemaRef?: string;
    requirements?: string[];
  };

  status:
    | "created"
    | "accepted"
    | "rejected"
    | "running"
    | "completed"
    | "failed"
    | "cancelled";

  createdAt: string;
  completedAt?: string;
}
```

AgentHandoff 属于执行层任务交接，不等于外部群聊任务分配。

```text
Array / 外部协作层可以负责通知、参与者和消息流。
Clean Core 的 AgentHandoff 负责执行状态、上下文包、子任务和结果回流。
```

### 8.2.3 父子任务

AgentHandoff 可以创建 Child Task。

建议 Task 增加：

```ts
interface Task {
  taskId: string;
  parentTaskId?: string;
  rootTaskId?: string;

  assignedAgentId?: string;
  sourceHandoffId?: string;
}
```

原则：

```text
Parent Task 保存整体目标。
Child Task 保存被委派 Agent 的执行状态。
Child Task 结果通过 ArtifactRef / TaskEvent 回流 Parent Task。
```

### 8.3 Task 状态机

```text
created
  ↓
accepted
  ↓
planning
  ↓
running
  ├── waiting_input
  ├── waiting_tool
  ├── waiting_approval
  ├── blocked
  ├── paused
  ↓
completed

终态：
failed
cancelled
rejected
```

### 8.4 主要职责

```text
1. 创建 Task。
2. 恢复 Task。
3. 应用 TaskCommand。
4. 追加 TaskEvent。
5. 校验状态转换是否合法。
6. 创建 / 更新 / 关闭 TaskPlan。
7. 追加 PlanEvent。
8. 维护 PlanStep 状态。
9. 生成 TaskCheckpoint。
10. 根据事件恢复任务状态。
11. 保存 TaskResult。
12. 创建 / 更新 AgentHandoff。
13. 创建 Child Task。
14. 追加 HandoffEvent。
15. 将 Child Task 结果回流 Parent Task。
```

### 8.5 不负责

```text
不负责模型决策。
不负责工具执行。
不负责工具权限判断。
不负责 Prompt 构建。
不负责长任务中的业务计划生成。
```

### 8.6 TaskCommand

建议核心支持：

```text
provide_input
approve_action
reject_action
cancel
pause
resume
create_plan
update_plan
replan
complete_step
fail_step
upgrade_agent_version
create_handoff
accept_handoff
reject_handoff
complete_handoff
fail_handoff
```

### 8.7 事件追加原则

TaskEvent 应该 append-only。

```text
TaskEvent 不直接覆盖历史。
Task 当前状态可以由事件流重建。
Checkpoint 是优化，不是唯一事实源。
```

---

## 9. 模块 4：context-engine

### 9.1 定位

负责将运行事实聚合成 WorkView，并投影成 PromptBundle。

它回答：

```text
模型这一步应该看到什么？
哪些事实进入上下文？
哪些内容只引用 Artifact？
哪些工具和 Skill 被注入？
如何裁剪和防注入？
```

### 9.2 核心对象

```text
WorkView
PromptBundle
ContextCollector
PromptComposer
ContextCompressionPolicy
PromptPolicy
PromptInjectionGuard
ContextBudget
```

### 9.3 WorkView

WorkView 是当前运行视角的事实聚合。

包含：

```text
用户输入
AgentDefinition 摘要
Task 状态
TaskEvent 摘要
Command 摘要
Memory 摘要
Artifact 引用
ToolResult 摘要
候选 Capability
候选 Skill
候选 Tool
当前 TaskPlan
当前 PlanStep
已完成步骤摘要
运行约束
风险标记
```

### 9.4 PromptBundle

PromptBundle 是给模型看的上下文投影。

包含：

```text
system
developer
task
context
skills
toolCards
toolDefinitions 可选
outputSchema
constraints
audit.promptHash
```

### 9.5 主要职责

```text
1. 读取 Task / Event / ToolResult / Artifact / Memory。
2. 调用 capability-discovery 获取候选能力。
3. 根据 PromptPolicy 构建 PromptBundle。
4. 根据 ContextCompressionPolicy 压缩历史和工具结果。
5. 根据 PromptInjectionGuard 做来源分区和安全标记。
6. 控制上下文 token budget。
7. 生成 PromptBundle hash。
```

### 9.6 不负责

```text
不负责调用模型。
不负责执行工具。
不负责写 Task 状态。
不负责判断工具权限。
不负责长期保存 PromptBundle 作为事实。
```

### 9.7 HandoffContextPackage Builder

`context-engine` 负责根据 HandoffPolicy 和当前任务事实生成 `HandoffContextPackage`。

```ts
interface HandoffContextPackage {
  packageId: string;

  parentTaskId: string;
  sourceRunId: string;

  fromAgentId: string;
  toAgentId: string;

  objective: string;
  reason: string;

  summary: string;
  keyFacts: string[];
  constraints: string[];
  openQuestions?: string[];

  artifactRefs: ArtifactRef[];
  toolResultRefs?: string[];
  memoryRefs?: string[];
  taskEventRefs?: string[];

  allowedContextScopes: string[];
  deniedContextScopes?: string[];

  expectedOutput: {
    format: "text" | "markdown" | "json" | "artifact";
    schemaRef?: string;
    requirements?: string[];
  };
}
```

构建原则：

```text
1. 交接的是任务目标和关键事实，不是完整聊天记录。
2. 大对象只传 ArtifactRef / ToolResultRef / TaskEventRef。
3. 下游 Agent 必须重新构建自己的 WorkView。
4. HandoffContextPackage 必须经过 Policy 检查。
5. HandoffContextPackage 应可被 Trace / Audit 引用。
```

推荐默认模式：

```text
hybrid = summary + keyFacts + refs + allowed scopes + expected output
```

### 9.8 注入防护基本要求

PromptBundle 必须区分来源：

```text
system instructions
developer instructions
agent package instructions
user input
tool result
artifact summary
memory summary
retrieved skill
retrieved tool card
```

工具输出和用户输入不能被当作系统指令。


---

## 10. 模块 5：capability-discovery

### 10.1 定位

负责在大量能力中找到当前任务相关候选。

它回答：

```text
当前任务需要哪些能力？
哪些 Skill 有用？
哪些 ToolCard 应该展示给模型？
是否应该委派给子 Agent？
```

### 10.2 核心对象

```text
CapabilityCard
AgentCapabilityCard
SkillCard
ToolCard
CapabilityRetriever
SkillRetriever
ToolRetriever
CandidateSet
```

### 10.3 主要职责

```text
1. 根据用户输入和 Task Objective 召回 Capability。
2. 根据任务类型召回 SkillCard。
3. 根据候选 Skill / Capability 召回 ToolCard。
4. 过滤不可见或不允许的候选。
5. 返回 TopK 候选给 context-engine。
6. 根据当前 PlanStep 调整候选 Skill / Tool。
7. 根据已完成步骤和失败步骤过滤候选能力。
8. 根据 handoff objective / capabilityQuery 发现目标 Agent。
9. 校验目标 Agent 的 AgentCapabilityCard 是否匹配任务。
```

### 10.4 不负责

```text
不负责工具执行。
不负责工具权限最终判断。
不负责模型决策。
不负责构建完整 PromptBundle。
```

### 10.5 三层发现

```text
Capability Retriever：找能力域 / 子智能体 / 工具组
Skill Retriever：找任务方法包
Tool Retriever：找可执行工具候选
```

### 10.6 Skill 渐进加载

```text
L1 SkillCard：默认进入索引，用于召回。
L2 SkillInstruction：Skill 被选中后按需加载。
L3 SkillResources：参考文件、示例、脚本，只通过引用或工具按需读取。
```

---

## 11. 模块 6：decision-engine

### 11.1 定位

负责让模型基于 PromptBundle 生成结构化 Decision，并进行合法性校验。

它回答：

```text
模型输出是否是合法决策？
是否引用了不存在的工具？
是否编造了 DecisionType？
是否需要 repair？
```

### 11.2 核心对象

```text
Decision
DecisionType
DecisionParser
DecisionNormalizer
DecisionValidator
DecisionRepair
DecisionSchema
```

### 11.3 DecisionType

建议保持少而稳定：

```text
reply
no_op
ask_clarification
tool_call
unsupported
error
```

不要让每个 Agent 自定义 DecisionType。

### 11.4 主要职责

```text
1. 接收 PromptBundle。
2. 调用 model-runtime。
3. 解析模型输出。
4. 规范化 Decision。
5. 校验 Decision schema。
6. 校验 tool_call 是否来自候选工具。
7. 校验参数是否基本可解析。
8. 必要时做 Decision repair。
9. 返回合法 Decision 给 runtime-kernel。
10. 在 Plan-guided 模式下校验 Decision 是否与当前 PlanStep 相容。
```

### 11.5 不负责

```text
不负责判断工具是否有权限执行。
不负责审批。
不负责工具执行。
不负责写 Task 状态。
不负责保存 Artifact。
```

### 11.6 Decision Validator 与 Tool Policy 边界

```text
Decision Validator 管“模型决策是否合法”。
Tool Policy 管“工具动作能不能执行”。
```

例如：

```text
模型选择了不存在的工具 → Decision Validator 拒绝
模型选择了存在的工具，但用户没权限 → Tool Policy 拒绝
模型参数 JSON 格式错误 → Decision Validator repair
模型参数合法，但包含越权路径 → Tool Policy 拒绝
```


### 11.7 Decision Outcome 与 ReplyKind

DecisionType 应保持少而稳定，但需要在 reply 内表达更细的输出语义。

建议：

```ts
type ReplyKind =
  | "answer"
  | "refusal"
  | "policy_notice"
  | "clarification_message"
  | "status_update";

interface DecisionReply {
  kind: ReplyKind;
  text: string;
  contentType?: "text/plain" | "text/markdown" | "application/json";
}
```

语义映射：

```text
正常回答：
Decision.type = reply
reply.kind = answer

拒绝回答：
Decision.type = reply
reply.kind = refusal

策略提示：
Decision.type = reply
reply.kind = policy_notice

追问用户：
Decision.type = ask_clarification
进入 TaskStatus.waiting_input

工具调用：
Decision.type = tool_call

无法支持：
Decision.type = unsupported

系统错误：
Decision.type = error
```

边界：

```text
拒绝回答不等于 error。
追问用户不等于普通 reply。
policy_notice 是对用户说明限制，不是模型自由发挥的拒绝。
unsupported 表示能力不支持，不表示安全拒绝。
```

runtime-kernel 应根据 DecisionType 推进状态，Response Builder 应根据 ReplyKind 生成最终响应形态。


---

## 12. 模块 7：model-runtime

### 12.1 定位

负责统一访问大模型供应商和结构化输出适配。

它回答：

```text
如何调用模型？
如何处理 streaming？
如何处理结构化输出？
如何处理不同供应商错误？
如何统计 token usage？
```

### 12.2 核心对象

```text
ModelClient
ModelProvider
ModelRequest
ModelResponse
ModelStreamEvent
StructuredOutputAdapter
ModelErrorClassifier
TokenUsage
```

### 12.3 主要职责

```text
1. 封装模型调用。
2. 支持非流式与流式输出。
3. 统一模型响应格式。
4. 统一 token usage。
5. 统一错误分类。
6. 支持超时、重试、fallback。
7. 支持结构化输出适配。
```

### 12.4 不负责

```text
不理解 AgentRun。
不理解 Task。
不执行工具。
不判断 Decision 是否合规。
不判断权限。
```

### 12.5 与 decision-engine 的关系

```text
model-runtime：调用模型，返回模型输出。
decision-engine：把模型输出解析和校验为 Decision。
```

不要让 model-runtime 直接返回业务 Decision 以外的内部流程控制。

---

## 13. 模块 8：policy-engine

### 13.1 定位

负责所有可配置、可版本化、可审计的运行策略判断。

它回答：

```text
这个动作允许吗？
需要审批吗？
失败后能重试吗？
上下文怎么裁剪？
任务怎么恢复？
风险等级怎么判？
```

### 13.2 核心对象

```text
RuntimePolicy
ToolPolicy
ToolRepairPolicy
ApprovalPolicy
PromptPolicy
ContextCompressionPolicy
TaskRecoveryPolicy
RiskPolicy
PermissionPolicy
PolicySet
PolicyDecision
PolicyVersion
```

### 13.3 主要职责

```text
1. 合并系统、租户、Agent、任务级策略。
2. 判断工具调用是否允许。
3. 判断是否需要审批。
4. 判断工具失败后是否允许 repair / retry。
5. 判断 Prompt 裁剪和压缩策略。
6. 判断任务恢复策略。
7. 输出 PolicyDecision。
8. 对策略命中结果写审计事件。
```

### 13.4 不负责

```text
不执行工具。
不驱动运行流程。
不调用模型。
不修改 Task 状态。
不保存 Artifact。
```

### 13.5 策略层级

建议：

```text
System Default Policy
  ↓
Tenant Policy
  ↓
Agent Policy
  ↓
Task Runtime Override
```

但上层必须有硬约束。

例如：

```text
AgentPolicy 可以降低 maxToolRetries，但不能超过系统上限。
TenantPolicy 可以禁止某类工具，AgentPolicy 不能重新打开。
高风险工具必须审批，Agent 不能取消。
```

### 13.6 策略与可编辑性

可编辑的是 Policy，不是 Runtime。

```text
可以编辑：
- maxRetries
- risk threshold
- approval requirement
- compression mode
- prompt token budget
- checkpoint mode

不能编辑：
- Tool Policy 必经链路
- Audit 必写点
- DecisionType 基础集合
- Task 基础状态机
```


### 13.7 管理类工具策略

管理类工具包括：

```text
prompt.draft.patch
skill.draft.create
skill.draft.update
skill.draft.delete
toolbinding.draft.update
agent.package.publish
agent.package.rollback
policy.update
```

这类工具必须使用比普通业务工具更严格的策略。

要求：

```text
1. 必须校验调用者是否有 package_edit / package_publish / policy_admin 权限。
2. 必须写 Audit。
3. publish / rollback / policy.update 默认需要审批。
4. Agent 自己不能绕过审批发布自己的变更。
5. 管理类工具默认不能在普通用户任务中暴露。
6. 管理类工具结果只能影响 Draft 或后续版本，不能静默影响当前 run。
```

策略判断示例：

```text
普通用户调用 skill.draft.create → 拒绝
Agent 作者调用 skill.draft.create → 允许写 Draft
Agent 作者调用 agent.package.publish → 需要审批或测试通过
Agent 自己在运行中调用 policy.update → 拒绝
管理员调用 policy.update → 审批 + Audit
```


### 13.8 优化发布策略

PolicyEngine 需要支持发布相关策略判断：

```text
PackagePublishPolicy
EvalRequiredPolicy
CanaryPolicy
RollbackPolicy
TaskUpgradePolicy
```

典型规则：

```text
1. 关键 Agent 发布前必须通过指定 Eval Suite。
2. 高风险 AgentPackage 发布必须人工审批。
3. canary 只能命中特定租户、用户组或流量比例。
4. 失败率超过阈值时自动建议 rollback，但不能静默修改正在运行的 Task。
5. 长任务升级 AgentDefinition 必须通过 TaskUpgradePolicy。
```

PolicyEngine 只负责判断，不负责执行发布流程。

```text
agent-definition 负责发布版本。
runtime-kernel 负责加载版本。
task-runtime 负责记录升级事件。
governance 负责审计发布和回滚。
```

### 13.9 HandoffPolicy

`HandoffPolicy` 控制跨 Agent 交接和上下文可见性。

```ts
type HandoffMode =
  | "full_context"
  | "summary_only"
  | "reference_only"
  | "hybrid";

interface HandoffPolicy {
  defaultMode: HandoffMode;
  allowFullContext: boolean;
  maxContextTokens: number;

  requireApprovalForCrossAgent: boolean;
  requireApprovalForSensitiveArtifacts: boolean;

  allowParentTaskQuery: boolean;
  allowArtifactRead: boolean;
  allowMemoryRead: boolean;
  allowTaskEventRead: boolean;
}
```

策略判断：

```text
1. fromAgent 是否允许委派给 toAgent。
2. toAgent 是否有能力处理 objective。
3. toAgent 是否能读取 artifactRefs。
4. toAgent 是否能读取 memoryRefs。
5. 是否允许 full_context。
6. 是否需要 waiting_approval。
7. 是否允许写回 Parent Task。
```

默认建议：

```text
同 Agent 内部子任务：hybrid 或 full_context。
同租户低风险 Agent：hybrid。
跨专业 Agent：hybrid / reference_only。
涉及敏感 Artifact：reference_only + 审批。
外部托管 Agent：summary_only / reference_only。
```

禁止：

```text
A Agent 能访问的上下文自动全部传给 B Agent。
B Agent 绕过 Policy 查询 Parent Task。
跨租户 AgentHandoff 默认允许。
```


---

## 14. 模块 9：tool-runtime

### 14.1 定位

负责工具治理与执行。

它回答：

```text
ToolCall 如何校验？
能不能执行？
在哪执行？
执行结果如何校验？
失败后如何处理？
```

### 14.2 核心对象

```text
ToolRegistry
ToolDefinition
ToolCard
ToolCall
ToolResult
ToolGateway
ToolRouter
ToolExecutor
ToolInputValidator
ToolResultValidator
ToolRepairHandler
ToolAdapter
```

### 14.3 主要职责

```text
1. 注册工具。
2. 暴露工具摘要 ToolCard。
3. 根据 ToolCall 查找 ToolDefinition。
4. 校验输入 schema。
5. 调用 policy-engine 做权限、风险、审批判断。
6. 调用 execution-domain 选择执行环境。
7. 执行工具。
8. 校验 ToolResult。
9. 处理工具失败、重试、repair、fallback。
10. 返回标准 ToolResult。
```

### 14.4 不负责

```text
不负责模型决策。
不负责任务状态机。
不负责审批规则定义。
不负责执行域资源管理。
不负责长期保存 Artifact，但可以调用 memory-artifact。
```

### 14.5 工具执行链路

```text
ToolCall
  ↓
ToolDefinition lookup
  ↓
Input schema validate
  ↓
Policy check
  ↓
Approval check
  ↓
ExecutionDomain resolve
  ↓
Execute
  ↓
ToolResult validate
  ↓
ArtifactRef / Result return
```

### 14.6 工具失败 repair

工具失败后不要简单无限重试。

应走：

```text
ToolResult failed
  ↓
Error classify
  ↓
policy-engine 查询 ToolRepairPolicy
  ↓
可重试 → retry
  ↓
可模型修复 → decision-engine repair arguments
  ↓
需要用户输入 → waiting_input
  ↓
需要审批 → waiting_approval
  ↓
不可恢复 → failed
```

不同工具策略不同：

```text
读操作：可安全重试
写操作：谨慎重试
删除/支付/外部副作用：默认不自动重试
高风险工具：不允许模型自动 repair 后继续执行
```


### 14.7 内部工具、暴露工具与外部 tools.invoke

工具需要区分三种可见性：

```text
private：只允许工具所属模块或所属 Agent 内部使用
protected：允许当前 Agent 内部 Decision Loop 使用，但不对外暴露
public / exposed：允许外部通过 tools.invoke 调用
```

AgentDefinition 中应明确：

```ts
interface AgentToolsConfig {
  allowedToolIds: string[];   // Agent 内部允许使用的工具
  exposedToolIds?: string[];  // 外部允许调用的工具
  deniedToolIds?: string[];   // 显式禁止的工具
}
```

内部模型 tool_call 可用范围：

```text
candidateTools ∩ allowedToolIds - deniedToolIds
```

外部 tools.invoke 可用范围：

```text
exposedToolIds ∩ caller permissions ∩ policy allowed
```

外部调用链路：

```text
External Caller
  ↓
AgentEnvelope(type = tools.invoke)
  ↓
AgentDefinition load
  ↓
检查 toolName 是否在 exposedToolIds
  ↓
Tool Policy
  ↓
ExecutionDomain resolve
  ↓
Tool Runtime execute
  ↓
ToolResult
```

禁止：

```text
外部直接调用 private 工具。
外部绕过 AgentDefinition 调 ToolRuntime。
外部绕过 Tool Policy。
把子智能体内部工具全部扁平暴露。
```

推荐：

```text
外部调用公开能力工具。
复杂内部流程通过 task.start 或公开 command 触发。
```

---

### 14.8 动态候选工具不是动态注册工具

多轮处理时，每轮可以动态变化的是候选工具集合：

```text
candidateTools
```

它由 capability-discovery 根据当前任务状态、Skill、Policy、上下文重新召回。

但 ToolRegistry / ToolDefinition 不应该在运行中被模型直接修改。

正确方式：

```text
新增工具
  ↓
ToolDefinition Draft
  ↓
测试
  ↓
绑定到 AgentPackage
  ↓
发布新版本
  ↓
后续 run 可用
```

错误方式：

```text
模型在运行中生成一个工具定义
  ↓
立即注册到 ToolRegistry
  ↓
当前 run 直接调用
```

这种方式禁止。


### 14.9 origin.agent.delegate / origin.agent.handoff

Clean Core 应提供一个标准内部工具，用于 Agent-to-Agent 委派。

推荐工具名：

```text
origin.agent.delegate
```

或：

```text
origin.agent.handoff
```

输入：

```ts
interface AgentDelegateInput {
  toAgentId?: string;
  capabilityQuery?: string;

  objective: string;
  reason: string;

  handoffMode?: HandoffMode;
  artifactRefs?: ArtifactRef[];

  expectedOutput?: {
    format: "text" | "markdown" | "json" | "artifact";
    schemaRef?: string;
    requirements?: string[];
  };
}
```

执行链路：

```text
Decision tool_call: origin.agent.delegate
  ↓
Decision Validator
  ↓
Tool Policy
  ↓
CapabilityDiscovery 选择目标 Agent 或校验 toAgentId
  ↓
HandoffPolicy 检查上下文和权限
  ↓
ContextEngine 生成 HandoffContextPackage
  ↓
TaskRuntime 创建 AgentHandoff / ChildTask
  ↓
RuntimeKernel 启动目标 AgentRun
  ↓
目标 Agent 完成任务
  ↓
结果通过 ArtifactRef / TaskEvent 回写 Parent Task
```

边界：

```text
origin.agent.delegate 是 Clean Core 内部工具。
它不等于外部群聊 @mention。
它不直接调用目标 Agent 的私有工具。
它必须经过 Policy 和 Audit。
```


---

## 15. 模块 10：execution-domain

### 15.1 定位

负责执行环境选择和运行边界。

它回答：

```text
这个工具在哪里执行？
本地进程？
远程 Worker？
沙箱？
托管 Agent Runtime？
客户私有执行域？
```

### 15.2 核心对象

```text
RuntimeProfile
ExecutionDomain
ExecutionDomainResolver
WorkerAdapter
SandboxAdapter
ManagedAgentAdapter
NetworkPolicy
ResourceLimit
CredentialScope
DataBoundary
```

### 15.3 主要职责

```text
1. 根据 ToolDefinition / RuntimeProfile / Policy 选择执行域。
2. 管理执行域类型。
3. 提供 Worker / Sandbox / Managed Runtime 的统一调用接口。
4. 控制网络策略。
5. 控制资源限制。
6. 控制凭证范围。
7. 控制数据边界。
```

### 15.4 不负责

```text
不判断业务权限。
不定义审批规则。
不决定模型下一步。
不保存 Task 状态。
不保存审计事件，但要返回执行元信息供 governance 记录。
```

### 15.5 与 tool-runtime / policy-engine 的边界

```text
tool-runtime：执行什么工具
policy-engine：能不能执行
execution-domain：在哪里执行
```

这三个必须分开。

### 15.6 ManagedAgentAdapter

外部托管 Agent Runtime 只能作为一种执行域。

它不能替代：

```text
runtime-kernel
decision-engine
policy-engine
governance
task-runtime
```

适用场景：

```text
数据分析
文件处理
临时代码执行
Web 资料收集
报告生成
```

不适用场景：

```text
核心生产写操作
强合规私有数据
高敏凭证操作
必须完全可回放的核心链路
```


---

## 16. 模块 11：memory-artifact

### 16.1 定位

负责记忆、文件、产物资产管理。

它回答：

```text
什么是记忆？
什么是产物？
文件如何存储？
哪些内容可以进入上下文？
哪些只能作为引用？
```

### 16.2 核心对象

```text
Memory
MemoryEvent
MemoryStore
MemoryRetriever
MemoryWriter
Artifact
ArtifactRef
ArtifactStore
ArtifactMetadata
FileStore
WorkspaceIO
BlobStore
```

### 16.3 主要职责

```text
1. 保存和检索记忆。
2. 保存和检索 Artifact。
3. 管理文件和 Blob。
4. 生成 ArtifactRef。
5. 生成 ArtifactSummary。
6. 提供 MemoryRetriever 给 context-engine。
7. 提供 ArtifactRetriever 给 context-engine。
8. 提供文件访问接口给 execution-domain。
```

### 16.4 不负责

```text
不主动修改 PromptBundle。
不主动驱动运行。
不判断工具是否允许执行。
不做模型决策。
```

### 16.5 记忆写入原则

记忆写入必须受控：

```text
默认不自动写长期记忆。
写长期记忆必须经过 MemoryPolicy。
敏感记忆必须脱敏。
记忆写入必须写 Audit。
```

### 16.6 Artifact 原则

```text
大对象不进 PromptBundle。
PromptBundle 只放摘要和引用。
Artifact 是事实资产。
ArtifactRef 是跨模块传递的标准引用。
```

### 16.7 HandoffContextPackage 存储

`memory-artifact` 应保存或引用 HandoffContextPackage。

职责：

```text
1. 保存 HandoffContextPackage。
2. 保存 contextPackageRef。
3. 管理 ArtifactRef / ToolResultRef / MemoryRef / TaskEventRef 的读取授权。
4. 为下游 Agent 的 context-engine 提供可查询引用。
5. 记录上下文包摘要和 hash。
```

原则：

```text
HandoffContextPackage 不应该无限复制原始上下文。
敏感引用必须经过 PolicyEngine 检查。
下游 Agent 读取引用时再次校验权限。
```

---

## 17. 模块 12：governance

### 17.1 定位

负责 Trace、Audit、Metrics、Replay。

它回答：

```text
运行过程怎么解释？
行为怎么证明？
故障怎么排查？
结果能不能回放？
```

### 17.2 核心对象

```text
TraceEvent
AuditEvent
MetricEvent
Span
RunTrace
TaskTrace
ToolTrace
PromptHash
ReplaySnapshot
RedactionRule
```

### 17.3 Trace

Trace 回答：

```text
这次运行是怎么一步步发生的？
```

记录：

```text
input.received
agent.loaded
run.created
task.created / task.loaded
workview.built
capability.retrieved
promptbundle.built
model.called
model.completed
decision.created
decision.validated
tool.policy_checked
tool.invoked
tool.completed / tool.failed
task.status_changed
approval.requested
approval.resolved
artifact.created
response.sent
```

### 17.4 Audit

Audit 回答：

```text
谁做了什么？
是否允许？
是否审批？
是否影响关键资产？
```

必须审计：

```text
外部调用
高风险工具调用
审批
权限拒绝
配置变更
AgentPackage 发布
Policy 变更
记忆写入
Artifact 删除
凭证使用
外部副作用
AgentHandoff 创建 / 接受 / 拒绝 / 完成 / 失败
跨 Agent 上下文包创建
跨 Agent Artifact / Memory / TaskEvent 读取授权
```

### 17.5 Handoff Trace / Audit

Handoff 必须记录以下 TraceEvent：

```text
handoff.created
handoff.policy_checked
handoff.context_packaged
handoff.accepted
handoff.rejected
handoff.started
handoff.completed
handoff.failed
handoff.cancelled
```

Handoff 必须记录以下 Audit 信息：

```text
fromAgentId
toAgentId
parentTaskId
childTaskId
contextPackageRef
handoffMode
artifactRefs
memoryRefs
taskEventRefs
policy decision
approval status
```

目标：

```text
任何跨 Agent 任务交接都必须能追踪：
谁交给谁？
交接了什么目标？
给了哪些上下文？
哪些上下文被拒绝？
结果回写到了哪里？
```

### 17.6 不负责

```text
不影响业务决策。
不参与权限判断。
不驱动运行流程。
```

### 17.7 Replay

Replay 不一定第一天完整实现，但结构要保留。

Replay 需要：

```text
AgentDefinition version
Policy version
PromptBundle hash
Model request / response 摘要
ToolCall
ToolResult
TaskEvent
ArtifactRef
TraceEvent
AuditEvent
```


---

## 18. 模块依赖关系

### 18.1 推荐依赖方向

```text
runtime-kernel
  ├── agent-definition
  ├── task-runtime
  ├── context-engine
  │     ├── capability-discovery
  │     └── memory-artifact
  ├── decision-engine
  │     └── model-runtime
  ├── tool-runtime
  │     ├── policy-engine
  │     ├── execution-domain
  │     └── memory-artifact
  ├── policy-engine
  └── governance
```

### 18.2 模块依赖解释

```text
runtime-kernel 调用其他模块，但其他模块不反向控制 runtime-kernel。
context-engine 可以读取任务、记忆、产物、候选能力，但不能修改任务状态。
decision-engine 调用 model-runtime，但不执行工具。
tool-runtime 调用 policy-engine 和 execution-domain，但不自己定义策略。
execution-domain 管执行环境，不管业务权限。
governance 横切记录，但不影响业务结果。
AgentHandoff 的执行状态由 task-runtime 负责。
HandoffContextPackage 的构建由 context-engine 负责。
HandoffPolicy 的判断由 policy-engine 负责。
跨 Agent 上下文引用由 memory-artifact 负责。
```

### 18.3 禁止依赖

必须禁止：

```text
decision-engine 直接执行工具
tool-runtime 直接调用模型做下一步决策
context-engine 修改 Task 状态
policy-engine 驱动运行流程
governance 影响业务结果
model-runtime 依赖 AgentDefinition
memory-artifact 主动改 PromptBundle
execution-domain 判断业务权限
agent-definition 依赖 runtime-kernel
```

---

## 19. Go 代码目录建议

推荐：

```text
internal/
  contracts/

  agentdef/
    definition/
    package/
    compiler/
    registry/
    loader/

  runtime/
    kernel/
    coordinator/
    step/
    loop/

  task/
    state/
    command/
    event/
    checkpoint/

  context/
    workview/
    promptbundle/
    compression/
    injectionguard/

  discovery/
    capability/
    skill/
    tool/
    index/

  decision/
    schema/
    parser/
    validator/
    repair/

  model/
    client/
    provider/
    streaming/
    structured/
    usage/

  policy/
    runtimepolicy/
    toolpolicy/
    approval/
    repair/
    promptpolicy/
    recovery/
    risk/

  tool/
    registry/
    gateway/
    router/
    executor/
    validator/
    adapters/

  execution/
    domain/
    profile/
    worker/
    sandbox/
    managed/

  asset/
    memory/
    artifact/
    filestore/
    workspaceio/

  governance/
    trace/
    audit/
    metrics/
    replay/
    redaction/
```

如果团队初期觉得目录太深，可以先这样：

```text
internal/
  contracts/
  agentdef/
  runtime/
  task/
  context/
  discovery/
  decision/
  model/
  policy/
  tool/
  execution/
  asset/
  governance/
```

但模块边界不能模糊。


---

## 20. 核心接口建议

### 20.1 runtime-kernel 依赖接口

```go
type AgentLoader interface {
    Load(ctx context.Context, ref AgentRef) (*AgentDefinition, error)
}

type TaskService interface {
    LoadOrCreate(ctx context.Context, req TaskLoadRequest) (*Task, error)
    ApplyCommand(ctx context.Context, taskID TaskID, cmd TaskCommand) error
    AppendEvent(ctx context.Context, taskID TaskID, event TaskEvent) error
}

type ContextBuilder interface {
    BuildWorkView(ctx context.Context, req WorkViewRequest) (*WorkView, error)
    BuildPromptBundle(ctx context.Context, view *WorkView) (*PromptBundle, error)
}

type DecisionMaker interface {
    Decide(ctx context.Context, bundle *PromptBundle) (*Decision, error)
}

type ToolRuntime interface {
    Invoke(ctx context.Context, call ToolCall, runCtx RunContext) (*ToolResult, error)
}

type Governance interface {
    Trace(ctx context.Context, event TraceEvent)
    Audit(ctx context.Context, event AuditEvent)
}
```

### 20.2 tool-runtime 依赖接口

```go
type PolicyEngine interface {
    EvaluateToolCall(ctx context.Context, req ToolPolicyRequest) (*PolicyDecision, error)
    EvaluateRepair(ctx context.Context, req ToolRepairRequest) (*RepairDecision, error)
}

type ExecutionDomainResolver interface {
    Resolve(ctx context.Context, req ExecutionResolveRequest) (*ExecutionDomain, error)
}

type ToolExecutor interface {
    Execute(ctx context.Context, req ToolExecuteRequest) (*ToolResult, error)
}
```


外部工具调用入口建议：

```go
type ToolGateway interface {
    InvokeExternal(ctx context.Context, req ExternalToolInvokeRequest) (*ToolResult, error)
    ListExposedTools(ctx context.Context, ref AgentRef, caller Caller) ([]ToolCard, error)
}

type ExternalToolInvokeRequest struct {
    AgentRef AgentRef
    Caller   Caller
    ToolName string
    Args     map[string]any
    Context  RuntimeContext
}
```

AgentPackage 管理工具接口建议：

```go
type AgentPackageDraftService interface {
    CreateDraft(ctx context.Context, req CreateDraftRequest) (*AgentPackageDraft, error)
    PatchPrompt(ctx context.Context, req PatchPromptRequest) (*AgentPackageDraft, error)
    UpsertSkill(ctx context.Context, req UpsertSkillRequest) (*AgentPackageDraft, error)
    UpdateToolBinding(ctx context.Context, req UpdateToolBindingRequest) (*AgentPackageDraft, error)
    Validate(ctx context.Context, draftID DraftID) (*ValidationResult, error)
    Publish(ctx context.Context, req PublishDraftRequest) (*AgentPackageVersion, error)
}
```


### 20.3 decision-engine 依赖接口

```go
type ModelClient interface {
    Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error)
    Stream(ctx context.Context, req ModelRequest) (<-chan ModelStreamEvent, error)
}

type DecisionValidator interface {
    Validate(ctx context.Context, decision *Decision, candidates CandidateSet) error
}
```

---

## 21. 关键开发逻辑

### 21.1 Decision Loop

Decision Loop 是核心闭环。

```text
Build WorkView
  ↓
Build PromptBundle
  ↓
Call Model
  ↓
Parse Decision
  ↓
Validate Decision
  ↓
Dispatch Decision
  ↓
If ToolCall → ToolRuntime → ToolResult → Update WorkView
  ↓
Continue or Terminal
```

必须保证：

```text
每一步有 Trace。
每次工具调用有 Audit。
每次状态变化有 TaskEvent。
每次外部副作用有 Audit。
```

### 21.2 Tool Policy

Tool Policy 是不可绕过的安全边界。

工具调用必须经过：

```text
ToolCall
  ↓
Decision Validator
  ↓
Tool Runtime
  ↓
Policy Engine
  ↓
Approval Check
  ↓
Execution Domain
  ↓
Tool Executor
```

任何工具适配器都不能绕过 Policy Engine。

### 21.3 PromptBundle 裁剪

PromptBundle 构建必须遵守：

```text
原始事实不塞满上下文。
大对象转 ArtifactRef。
ToolResult 默认摘要化。
Memory 默认按相关性召回。
Skill 默认 SkillCard，按需展开 SkillInstruction。
工具默认 ToolCard，调用前才展开 ToolDefinition。
```

### 21.4 waiting_input / waiting_approval

这两个状态是核心状态，不是普通回复。

进入条件：

```text
缺少必要信息 → waiting_input
低置信度且无法安全继续 → waiting_input
高风险工具 → waiting_approval
外部写操作 → waiting_approval
策略要求审批 → waiting_approval
```

状态进入后：

```text
runtime-kernel 停止当前 run
task-runtime 保存状态
governance 写 trace/audit
等待 task.command 恢复
```

### 21.5 长任务恢复

恢复时不应该依赖 Prompt 历史。

恢复来源：

```text
Task
TaskEvent
Checkpoint
ArtifactRef
ToolResult
Memory
AgentDefinition version
Policy version
```

恢复流程：

```text
Load Task
  ↓
Rebuild state from TaskEvent / Checkpoint
  ↓
Build WorkView
  ↓
Build PromptBundle
  ↓
Continue Decision Loop
```

### 21.6 Plan-guided 多工具执行逻辑

复杂任务可以进入 Plan-guided 模式。

```text
用户目标
  ↓
Decision 生成 TaskPlan 或选择已有 Skill 生成 TaskPlan
  ↓
task-runtime 保存 TaskPlan
  ↓
context-engine 将当前 PlanStep 注入 WorkView / PromptBundle
  ↓
decision-engine 生成当前步骤 Decision
  ↓
tool-runtime 执行工具
  ↓
ToolResult / ArtifactRef 回写
  ↓
task-runtime 更新 PlanStep 状态
  ↓
继续下一步或 replan
```

Plan 不是硬编码工作流：

```text
Plan 可以被模型建议修改。
Plan 修改必须记录 PlanEvent。
高风险 replan 需要策略允许。
PlanStep 完成必须有结果依据。
```

Plan-guided 模式下的基本规则：

```text
1. 当前 PromptBundle 必须包含当前 PlanStep。
2. ToolResult 必须关联到 PlanStep。
3. PlanStep 失败必须记录 failureReason。
4. replan 必须保留旧 Plan 的历史。
5. completed Task 必须能看到完整 Plan 执行轨迹。
```

### 21.7 Agent-to-Agent Handoff 逻辑

Agent-to-Agent Handoff 的标准流程：

```text
Agent A Decision: tool_call origin.agent.delegate
  ↓
Decision Validator 校验工具候选合法
  ↓
ToolRuntime 接收 ToolCall
  ↓
PolicyEngine 执行 HandoffPolicy
  ↓
CapabilityDiscovery 选择 / 校验目标 Agent
  ↓
ContextEngine 生成 HandoffContextPackage
  ↓
MemoryArtifact 保存 contextPackageRef
  ↓
TaskRuntime 创建 AgentHandoff / ChildTask
  ↓
RuntimeKernel 启动 Agent B 的 AgentRun
  ↓
Agent B 构建自己的 WorkView / PromptBundle
  ↓
Agent B 执行任务
  ↓
结果写回 ChildTask / ArtifactRef
  ↓
ParentTask 追加 handoff.completed
```

关键要求：

```text
1. Handoff 不是简单消息转发。
2. 下游 Agent 不能直接继承上游 Agent 的 PromptBundle。
3. 下游 Agent 必须基于自己的 AgentDefinition 构建 WorkView。
4. 上下文传递模式由 HandoffPolicy 决定。
5. 上下文引用读取必须二次校验权限。
6. 所有 Handoff 必须 Trace / Audit。
```

### 21.8 Prompt / Skill / ToolBinding 变更逻辑

定义类资源变更必须经过 Draft / Publish。

```text
编辑提示词
  ↓
修改 AgentPackage Draft
  ↓
Compile / Validate
  ↓
Publish 新版本
  ↓
新 AgentRun 加载新版本
```

Skill 编辑同理：

```text
skill.draft.create / update
  ↓
写入 Draft
  ↓
编译 SkillCard / SkillInstruction / SkillResources
  ↓
测试 / 评审
  ↓
发布
  ↓
进入 Skill Index
```

当前运行中允许：

```text
增加临时约束
更新 PromptBundle 投影
重新选择 CandidateSet
记录 Proposal
```

当前运行中禁止：

```text
直接改线上 AgentDefinition
直接改 ToolRegistry
直接发布 Skill
直接绕过审批修改 Policy
```

---

### 21.9 外部调用工具逻辑

外部调用工具不是直接进入 ToolExecutor，而是进入 ToolGateway。

```text
tools.invoke
  ↓
ToolGateway
  ↓
AgentDefinition load
  ↓
exposedToolIds check
  ↓
Policy check
  ↓
ToolRuntime
  ↓
ToolResult
```

外部调用必须携带：

```text
caller
tenant
agentRef
toolName
arguments
traceId
requestId
```

外部调用必须记录：

```text
input.received
tool.external_invoked
tool.policy_checked
tool.invoked
tool.completed / tool.failed
audit.external_tool_call
```

---

### 21.10 输入路由逻辑

原始输入先进入输入适配层，再形成 AgentEnvelope。

```text
RawInput
  ↓
InputAdapter
  ↓
MentionParser / CommandParser
  ↓
RouteResolver
  ↓
AgentEnvelope
```

示例：

```text
@report-agent 生成周报
```

解析为：

```text
target.agentId = report-agent
payload.input = 生成周报
```

Clean Core 不解析原始渠道格式，只处理标准化 AgentEnvelope。


---

## 22. 开发重点与难点归位

### 22.1 工具调用失败 repair

归属：

```text
tool-runtime + policy-engine + decision-engine
```

边界：

```text
tool-runtime 发现失败
policy-engine 判断能否 repair
decision-engine 负责模型参数修复
runtime-kernel 负责决定继续或结束
```

### 22.2 多轮 tool_call 上下文压缩

归属：

```text
context-engine + policy-engine + memory-artifact
```

边界：

```text
context-engine 执行压缩
policy-engine 决定压缩策略
memory-artifact 保存原始事实和 ArtifactRef
```

### 22.3 streaming 中间状态

归属：

```text
runtime-kernel + model-runtime + tool-runtime + governance
```

稳定事件：

```text
run.started
model.delta
decision.completed
tool.started
tool.progress
tool.completed
task.waiting_input
task.waiting_approval
run.completed
run.failed
```

事件类型应稳定，不建议让 Agent 自定义。

### 22.4 长任务恢复

归属：

```text
task-runtime + runtime-kernel + context-engine
```

核心事实在 TaskEvent / Checkpoint，不在 Prompt 历史。

### 22.5 waiting_input / waiting_approval

归属：

```text
task-runtime + policy-engine + runtime-kernel
```

状态固定，触发策略可配置。

### 22.6 Tool Policy 边界和审批

归属：

```text
policy-engine + tool-runtime + governance
```

策略可配置，链路不可绕过。

### 22.7 PromptBundle 裁剪和注入防护

归属：

```text
context-engine + policy-engine
```

结构固定，策略可配置。

### 22.8 多工具组合稳定性

归属：

```text
task-runtime + context-engine + capability-discovery + decision-engine + tool-runtime
```

边界：

```text
TaskPlan 管步骤。
ContextEngine 注入当前步骤。
CapabilityDiscovery 根据当前步骤召回能力。
DecisionEngine 生成当前步骤动作。
ToolRuntime 执行工具。
TaskRuntime 保存步骤结果。
```

如果多工具组合效果差，优先优化：

```text
SKILL.md
ToolCard.whenToUse
TaskPlan 生成提示
EvalCase
ContextCompressionPolicy
ToolRepairPolicy
```

而不是修改 Runtime Kernel。

### 22.9 Agent-to-Agent Handoff

归属：

```text
tool-runtime + task-runtime + context-engine + policy-engine + capability-discovery + governance
```

边界：

```text
tool-runtime 提供 origin.agent.delegate。
capability-discovery 发现或校验目标 Agent。
policy-engine 判断是否允许交接和上下文传递。
context-engine 构建 HandoffContextPackage。
task-runtime 保存 AgentHandoff / ChildTask / HandoffEvent。
memory-artifact 保存上下文包和引用。
governance 记录 Trace / Audit。
```

不归属：

```text
不在 Clean Core 里实现 Group。
不在 Clean Core 里实现群消息。
不在 Clean Core 里实现通知系统。
不在 Clean Core 里实现 Worker 评价。
```

如果需要这些能力，应由 Array / Collaboration Plane 提供。

---

### 22.10 提示词 / 策略优化工作流

归属：

```text
agent-definition + policy-engine + governance
```

优化流程：

```text
查看 Trace / Eval 失败案例
  ↓
定位问题类型
  ↓
修改 Prompt / Skill / ToolBinding / Policy Draft
  ↓
运行 Eval Suite
  ↓
Review
  ↓
Publish / Canary
  ↓
观察 Trace / Metrics
  ↓
Rollback 或 Stable
```

优化人员能改：

```text
AGENTS.md
SKILL.md
ToolCard 描述
ToolBinding
PromptPolicy
ContextCompressionPolicy
ToolRepairPolicy
ApprovalPolicy
EvalCase
```

优化人员不能改：

```text
Runtime Kernel 主循环
Task 基础状态机
Tool Policy 必经链路
Trace / Audit 必写点
ExecutionDomain 安全边界
DecisionType 基础集合
```

---

## 23. 开发原则清单

### 23.1 必须遵守

```text
1. Runtime Kernel 只编排，不做具体能力。
2. Decision Validator 只校验模型决策是否合法。
3. Tool Policy 判断工具动作是否允许。
4. ExecutionDomain 只决定在哪里执行。
5. ContextEngine 只构建上下文，不修改事实。
6. PromptBundle 不是事实源。
7. TaskEvent / ToolResult / Artifact / AuditEvent 是事实。
8. Policy 可配置，但安全链路不可绕过。
9. AgentPackage 可编辑，但必须编译、校验、版本化。
10. Trace / Audit 必须横切关键步骤。
11. Agent-to-Agent Handoff 必须通过 Policy 和 Audit。
12. Clean Core 不管理完整 Group，只接收 CollaborationContext。
13. 外部任务和 CoreTask 必须通过 ExternalTaskBinding 映射。
```

### 23.2 必须避免

```text
1. 把所有模块都塞进 runtime-kernel。
2. 让模型直接决定是否审批。
3. 让工具执行绕过 policy-engine。
4. 让 context-engine 修改 Task。
5. 把 Prompt 历史当作任务恢复依据。
6. 把 AGENTS.md / SKILL.md 当作内部事实源。
7. 让自动生成的 Skill 直接生效。
8. 在 execution-domain 里写业务权限逻辑。
9. 在 model-runtime 里理解 AgentRun / Task。
10. 在 governance 里影响业务结果。
11. 在 Clean Core 里实现群组消息系统。
12. 把外部 Task 状态直接当作 CoreTask 状态。
13. 把完整上游 PromptBundle 直接传给下游 Agent。
14. 让 AgentHandoff 绕过 Policy / Audit。
```


---

## 24. 开发验收标准

### 24.1 模块级验收

每个模块必须有：

```text
清晰职责
公开接口
输入输出类型
错误类型
单元测试
关键边界测试
Trace / Audit 触发点说明
```

### 24.2 链路级验收

完整链路必须支持：

```text
普通 reply
ask_clarification
tool_call
tool_call failed
tool_call repair
waiting_input
waiting_approval
task resume
artifact create
trace replay read
audit query
```

### 24.3 策略级验收

必须验证：

```text
低风险工具可直接执行
高风险工具进入 approval
无权限工具被拒绝
工具失败按策略重试
危险参数被拦截
PromptBundle 超出预算时触发压缩
Task 可从事件恢复
```


### 24.4 v1.2 新增边界验收

必须验证以下边界：

```text
提示词编辑工具只能修改 Draft，不能影响当前 run。
Skill 创建工具只能生成 Draft / Proposal，不能直接进入当前候选集。
发布 AgentPackage 新版本后，正在运行的 AgentRun 不静默切换版本。
长 Task 升级 AgentDefinition 需要显式命令或策略。
每轮 Decision Loop 会重建 PromptBundle 和 CandidateSet。
外部 tools.invoke 只能调用 exposedToolIds。
private / protected 工具不能被外部调用。
@mention 在 AgentEnvelope 生成前解析完成。
Decision.reply.kind 能表达 answer / refusal / policy_notice。
ask_clarification 会进入 waiting_input。
高风险管理工具必须进入 approval 或被拒绝。
Plan-guided 任务能保存 TaskPlan / PlanStep / PlanEvent。
ToolResult 能关联到 PlanStep。
replan 会保留历史计划。
Eval Suite 失败时不能直接发布 stable。
AgentPackage publish / rollback 必须写 Audit。
Canary run 必须记录命中的 AgentPackage / Policy 版本。
优化人员修改 Prompt / Skill / Policy 不需要改 Go 代码。
```


### 24.5 优化交接验收

为了验证“开发完成后交给优化人员持续调优”的目标，必须验收：

```text
1. 优化人员可以创建 AgentPackage Draft。
2. 优化人员可以修改 AGENTS.md / SKILL.md。
3. 优化人员可以调整 ToolBinding 和 Policy Draft。
4. 优化人员可以运行 Eval Suite。
5. Eval 结果能展示失败原因、工具调用轨迹、最终输出。
6. 通过审核后可以发布新版本。
7. 发布后新 run 命中新版本。
8. 旧 run / 长 Task 不静默切换版本。
9. 出问题后可以 rollback。
10. 全过程有 Audit。
```


---

## 25. 全景开发顺序建议

这不是 MVP 缩减，而是全景核心开发的接口稳定顺序。

### 25.1 第一组：契约和定义稳定

```text
core-contracts
agent-definition
policy-engine 基础类型
```

先稳定类型和定义，否则后面模块会反复改。

同时要尽早定义：

```text
AgentPackage version
PolicySet version
EvalSuite version
Release metadata
```

否则后续优化发布体系会返工。

### 25.2 第二组：运行主链路

```text
runtime-kernel
task-runtime
context-engine
decision-engine
model-runtime
```

先跑通：

```text
AgentEnvelope → AgentRun → WorkView → PromptBundle → Decision → Response
```

### 25.3 第三组：工具和策略

```text
tool-runtime
policy-engine 完整判断
execution-domain
memory-artifact
```

跑通：

```text
Decision tool_call → Policy → Execute → ToolResult → Artifact → Continue
```

### 25.4 第四组：治理与恢复

```text
governance
task recovery
trace replay
audit query
```

跑通：

```text
可追踪
可审计
可恢复
可排查
```

### 25.5 第五组：能力增强

```text
capability-discovery
Skill 渐进加载
Prompt 压缩
Tool repair
TaskPlan / PlanStep
Eval Suite
Release / Canary / Rollback
ManagedAgentAdapter
AgentHandoff
HandoffContextPackage
CollaborationProvider Adapter
```

这些是复杂度增强，但接口要提前预留。

---

## 26. 最终总结

原智能体核心开发应该围绕 13 块展开：

```text
0. core-contracts
1. agent-definition
2. runtime-kernel
3. task-runtime
4. context-engine
5. capability-discovery
6. decision-engine
7. model-runtime
8. policy-engine
9. tool-runtime
10. execution-domain
11. memory-artifact
12. governance
```

核心思想：

```text
Runtime Kernel 稳定推进运行。
AgentDefinition 定义智能体。
TaskRuntime 保存长任务事实。
ContextEngine 构建模型上下文。
CapabilityDiscovery 控制能力召回。
DecisionEngine 约束模型输出。
ModelRuntime 隔离模型供应商。
PolicyEngine 承载动态策略。
ToolRuntime 治理工具执行。
ExecutionDomain 控制执行边界。
MemoryArtifact 管理记忆和产物。
Governance 记录过程和行为。
```

最重要的边界：

```text
模型不能绕过 Decision Validator。
工具不能绕过 Tool Policy。
执行域不能承载业务权限。
PromptBundle 不是事实源。
Policy 可以动态，但安全链路不能动态。
AgentPackage 可以编辑，但必须版本化。
Trace/Audit 不是附加功能，而是核心运行事实的一部分。
```

一句话：

```text
我们不是在开发一个 Agent Demo，
而是在开发一个可治理、可审计、可恢复、可扩展的 Agent Runtime Core。
```

v1.1 额外强调：

```text
定义可以编辑，但必须版本化。
运行可以动态，但只能动态投影和选择。
工具可以外部调用，但只能调用显式暴露能力。
Agent 可以提出自我改进，但不能绕过审批直接改变线上行为。
复杂任务可以 Plan-guided，但 Plan 不是硬编码工作流。
优化人员可以改 Prompt / Skill / Policy / Eval，不应改 Runtime Kernel。
发布必须可评估、可灰度、可回滚、可审计。
Clean Core 不做 Group，只通过 CollaborationContext 接外部协作系统。
Agent-to-Agent 协作通过 Handoff、ContextPackage、Policy 和 Audit 受控完成。
```

