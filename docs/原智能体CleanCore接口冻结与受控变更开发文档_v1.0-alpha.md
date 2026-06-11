# 原智能体 Clean Core v1.0-alpha 接口冻结与受控变更开发文档

版本：v1.0-alpha  
日期：2026-05-29  
定位：开发基线冻结、接口变更控制、兼容演进指南  
配套文档：  
- 《原智能体 Clean Core 全景开发设计文档 v1.2 Clean》  
- 《原智能体 Clean Core 工程实施规格文档 v1.0》  

---

## 0. 文档目的

本文档用于回答一个关键问题：

> 核心接口冻结后，如果开发中发现设计不完善，应该怎么办？

答案不是“永远不能改”，而是：

```text
冻结的是当前开发基线，不是冻结未来演进。
```

本文档定义：

```text
1. 哪些接口现在进入 v1.0-alpha 冻结。
2. 哪些结构允许兼容扩展。
3. 哪些部分属于实现细节，可以边开发边优化。
4. 哪些策略和配置本来就应该动态调整。
5. 开发中发现设计问题时如何提变更。
6. 变更如何分级、评审、兼容、迁移、回归。
```

目标是同时避免两个极端：

```text
不冻结 → 各模块各写各的，最后全部对不上。
完全冻结 → 开发中发现问题也改不动，系统僵死。
```

正确做法是：

```text
分层冻结
受控变更
兼容优先
版本钉住
变更留痕
测试回归
```

---

## 1. 总原则

### 1.1 冻结不是永不修改

本文档中的“冻结”指：

```text
当前开发阶段所有人必须以此为基线开发。
任何破坏性修改都不能私自进行。
必要修改必须经过变更流程。
```

不表示：

```text
接口永远不能演进。
字段永远不能增加。
状态机永远不能修正。
实现方式不能优化。
```

### 1.2 受控冻结的目的

受控冻结的核心目的是：

```text
让团队有共同基线。
让模块可以并行开发。
让改动可评估、可回滚、可追踪。
让核心链路不被随意破坏。
```

不是为了限制合理改进。

### 1.3 兼容优先

开发阶段发现不足时，优先使用：

```text
新增 optional 字段
新增 enum value
新增 metadata
新增版本
新增 adapter
新增 policy 字段
```

尽量避免：

```text
删除字段
修改字段语义
重命名核心字段
破坏状态机
改变事实源边界
绕过 Policy / Audit
```

---

## 2. 四层冻结模型

Clean Core 的冻结分为四层。

```text
L1：硬冻结
L2：软冻结
L3：实现不冻结
L4：策略配置不冻结
```

---

## 3. L1 硬冻结

### 3.1 定义

硬冻结表示：

```text
这些是全局核心契约。
修改会影响多个模块、数据库、状态机、测试和审计。
默认不改。
确实需要改，必须走 P0 / P1 级变更流程。
```

### 3.2 当前 v1.0-alpha 硬冻结项

```text
1. AgentEnvelope 主结构
2. RuntimeContext 主结构
3. CollaborationContext 主结构
4. AgentRun 主结构
5. Task 主结构
6. TaskEvent append-only 原则
7. TaskStatus 基础集合
8. DecisionType 基础集合
9. Decision 主结构
10. ToolCall 主结构
11. ToolResult 主结构
12. ToolPolicy 必经链路
13. TraceEvent 主结构
14. AuditEvent 主结构
15. AgentRun 版本钉住原则
16. PromptBundle 不是事实源原则
17. WorkView 每轮重建原则
18. 外部协作系统不进入 Clean Core 原则
19. AgentHandoff 必须经过 HandoffPolicy 原则
20. 工具执行必须经过 Policy / Audit 原则
```

### 3.3 修改硬冻结项的要求

修改硬冻结项必须：

```text
1. 提交 Contract Change Proposal。
2. 标明影响范围。
3. 给出兼容策略。
4. 给出 migration 方案。
5. 更新架构文档和实施规格。
6. 更新数据库 migration。
7. 更新单元测试和链路测试。
8. 做全链路回归。
9. 得到架构负责人确认。
```

---

## 4. L2 软冻结

### 4.1 定义

软冻结表示：

```text
这些结构已经有基本形态。
允许兼容扩展。
不允许破坏已有语义。
```

### 4.2 当前 v1.0-alpha 软冻结项

```text
1. WorkView 字段集合
2. PromptBundle 字段集合
3. PolicySet 字段集合
4. HandoffContextPackage 字段集合
5. AgentDefinition metadata
6. SkillCard metadata
7. ToolDefinition metadata
8. TraceEvent payload
9. AuditEvent reason / metadata
10. Artifact metadata
11. EvalCase schema
12. Release metadata
13. ExecutionDomain metadata
```

### 4.3 允许的修改

```text
新增 optional 字段
新增 metadata 字段
新增 enum value
新增 policy 配置项
新增 trace payload 字段
新增 eval assertion 类型
新增 artifact metadata
```

### 4.4 不允许的修改

```text
删除已有字段
改变已有字段含义
让 optional 字段变 required
让旧数据无法读取
让旧 AgentRun 无法回放
让旧 Task 无法恢复
```

---

## 5. L3 实现不冻结

### 5.1 定义

实现不冻结表示：

```text
接口不变的前提下，模块内部实现可以持续优化。
```

### 5.2 当前实现不冻结项

```text
1. repository 实现
2. Prompt 渲染细节
3. ToolResult 摘要算法
4. Skill 召回算法
5. Tool 召回算法
6. CapabilityDiscovery 规则
7. EvalRunner 断言实现
8. Provider Adapter 实现
9. 缓存策略
10. 索引策略
11. 压缩策略
12. Retry 实现
13. Repair Prompt 实现
14. Array Bridge 内部映射细节
```

### 5.3 修改要求

实现层修改不需要走架构评审，但必须：

```text
1. 不破坏公开接口。
2. 不改变事实源。
3. 不绕过 Policy。
4. 不绕过 Audit。
5. 不破坏已有测试。
6. 增加必要单元测试。
```

---

## 6. L4 策略配置不冻结

### 6.1 定义

策略和配置本来就是为了可调优。

### 6.2 当前不冻结项

```text
1. AGENTS.md
2. SKILL.md
3. ToolBinding
4. PromptPolicy
5. ContextCompressionPolicy
6. ToolRepairPolicy
7. ApprovalPolicy
8. HandoffPolicy
9. TaskRecoveryPolicy
10. EvalCase
11. ReleasePolicy
12. CanaryPolicy
```

### 6.3 修改要求

策略配置可变，但必须：

```text
1. 版本化。
2. 可回滚。
3. 写 Audit。
4. 不突破系统硬约束。
5. 关键变更运行 Eval。
6. 发布走 Draft / Validate / Eval / Review / Publish。
```

---

## 7. v1.0-alpha 冻结清单

### 7.1 必须按文档实现的冻结项

以下内容进入 v1.0-alpha 开发基线。

```text
contracts:
- AgentEnvelope
- RuntimeContext
- CollaborationContext
- ArtifactRef
- ErrorCode / RuntimeError
- DecisionType
- TaskStatus
- ToolResultStatus
- RiskLevel

runtime:
- AgentRun
- RunStatus
- Run 主循环阶段
- 版本钉住

task:
- Task
- TaskEvent
- TaskCommand
- TaskEvent append-only
- Task 乐观锁

decision:
- Decision
- DecisionReply
- ClarificationRequest
- Decision Validator 边界

tool:
- ToolDefinition
- ToolCall
- ToolResult
- ToolVisibility
- ToolPolicy 必经链路

governance:
- TraceEvent
- AuditEvent
- 必写事件清单

handoff:
- AgentHandoff
- HandoffStatus
- HandoffContextPackage
- HandoffMode
- HandoffPolicy
- origin.agent.delegate 基本链路

collaboration:
- ExternalTaskBinding
- CollaborationContext
- CollaborationProvider interface
```

---

## 8. 可扩展清单

这些结构允许兼容扩展。

```text
WorkView:
- 可新增 summary 字段
- 可新增 context source
- 可新增风险标记

PromptBundle:
- 可新增 section
- 可新增 rendering metadata
- 可新增 token budget 统计

PolicySet:
- 可新增 policy 子项
- 可新增 policy metadata
- 可新增命中解释字段

ToolDefinition:
- 可新增 metadata
- 可新增 execution hints
- 可新增 rate limit hints

HandoffContextPackage:
- 可新增 refs
- 可新增 expected output 限制
- 可新增 context scope

EvalCase:
- 可新增 assertion 类型
- 可新增 trace assertion
- 可新增 artifact assertion
```

要求：

```text
新增必须向后兼容。
新增字段默认 optional。
新增字段必须有测试。
```

---

## 9. 待探索清单

以下内容暂不冻结，允许开发中根据实际效果调整。

```text
1. CapabilityDiscovery 算法
2. Skill / Tool 召回排序算法
3. PromptBundle 具体模板
4. Decision repair prompt
5. Tool repair prompt
6. ToolResult 摘要算法
7. ContextCompression 算法
8. EvalRunner 断言细节
9. Canary 命中策略
10. Array Bridge 具体 API 适配
11. Model Provider 具体 SDK
12. Streaming event 合并策略
13. Artifact 摘要生成方式
14. Memory 写入策略细节
```

这些内容开发时可以先用简单实现：

```text
规则优先
关键词优先
配置优先
stub 优先
先可跑通，再优化
```

---

## 10. 变更分级

### 10.1 P0：核心破坏性变更

定义：

```text
会破坏核心事实源、状态机、主结构或安全链路的变更。
```

例子：

```text
TaskStatus 重构。
DecisionType 删除或重命名。
ToolResult 主结构改变。
TaskEvent 不再 append-only。
ToolPolicy 必经链路改变。
Audit 必写点取消。
PromptBundle 变成事实源。
```

要求：

```text
1. 必须架构评审。
2. 必须更新所有相关文档。
3. 必须有 migration。
4. 必须全链路回归。
5. 必须明确兼容期。
6. 必须记录 Architecture Decision Record。
```

---

### 10.2 P1：核心兼容扩展

定义：

```text
影响多个模块，但保持向后兼容的扩展。
```

例子：

```text
ToolResult 增加 executionDomainRef。
Task 增加 priority。
AgentRun 增加 contractVersion。
Handoff 增加 deadline。
PromptBundle 增加 safetyContext。
PolicySet 增加 BudgetPolicy。
```

要求：

```text
1. 提交 Contract Change Proposal。
2. 更新 contracts。
3. 更新相关模块测试。
4. 更新实施规格。
5. 保持旧字段兼容。
```

---

### 10.3 P2：模块内部实现变更

定义：

```text
不改变公开接口，只调整模块内部实现。
```

例子：

```text
Skill 召回从关键词改成 embedding。
Prompt 压缩算法优化。
Tool retry 实现优化。
Repository 查询优化。
Provider Adapter 错误处理优化。
```

要求：

```text
1. 模块内评审。
2. 测试通过。
3. 不破坏接口。
4. 不破坏 Trace / Audit。
```

---

### 10.4 P3：策略配置变更

定义：

```text
AgentPackage、Policy、Eval、Release 配置调整。
```

例子：

```text
maxRetries 调整。
ApprovalPolicy 调整。
Handoff 默认模式调整。
Skill 文案调整。
ToolBinding whenToUse 调整。
EvalCase 增加。
```

要求：

```text
1. 版本化。
2. 写 Audit。
3. 关键变更跑 Eval。
4. 支持 rollback。
```

---

## 11. Contract Change Proposal 模板

任何 P0 / P1 变更必须提交以下内容。

```text
标题：
变更等级：P0 / P1
提交人：
日期：

一、背景
为什么需要修改？
现有设计哪里不满足？

二、变更内容
修改哪些结构？
新增哪些字段？
删除或重命名哪些字段？
影响哪些状态机？

三、影响范围
涉及模块：
涉及数据库表：
涉及 API / Command：
涉及 Trace / Audit：
涉及 Eval：
涉及 Bridge / Adapter：

四、兼容策略
是否向后兼容？
旧数据如何读取？
旧 AgentRun 如何回放？
旧 Task 如何恢复？

五、迁移方案
是否需要 DB migration？
是否需要数据修复？
是否需要双写或灰度？

六、测试方案
单元测试：
状态机测试：
链路测试：
安全边界测试：
回归测试：

七、发布方案
是否灰度？
是否可回滚？
回滚条件是什么？

八、结论
批准 / 拒绝 / 延后
```

---

## 12. Architecture Decision Record 模板

P0 级变更必须记录 ADR。

```text
ADR 编号：
标题：
状态：Proposed / Accepted / Rejected / Superseded
日期：

上下文：
我们遇到了什么问题？

决策：
我们决定怎么做？

原因：
为什么这样做？

影响：
影响哪些模块？
有什么风险？
有什么迁移成本？

替代方案：
我们考虑过哪些方案？
为什么没有选？

后续行动：
需要做哪些实现、测试、文档更新？
```

---

## 13. 兼容策略

### 13.1 字段兼容

```text
新增字段默认 optional。
读取方必须忽略未知字段。
写入方不得依赖新字段必然存在。
删除字段必须经过 deprecation 周期。
```

### 13.2 枚举兼容

```text
新增 enum value 允许。
删除 enum value 禁止。
重命名 enum value 禁止。
处理方遇到未知 enum 必须返回明确错误或降级处理。
```

### 13.3 数据兼容

```text
TaskEvent 不修改历史。
AuditEvent 不修改历史。
ToolResult 不修改历史。
旧 AgentRun 使用旧 version snapshot 回放。
旧 Task 使用创建时的 AgentVersion 和 PolicySet。
```

### 13.4 API 兼容

```text
新增字段允许。
删除字段不允许。
必填字段不能变 optional 后改变语义。
optional 字段不能无预警变 required。
```

---

## 14. 版本策略

### 14.1 Contract Version

建议在系统中增加：

```text
contractVersion
```

用于记录核心契约版本。

建议记录位置：

```text
AgentRun.version_snapshot.contractVersion
Task.schemaVersion
AgentDefinition.contractVersion
ToolDefinition.contractVersion
PolicySet.contractVersion
```

### 14.2 AgentRun 版本快照

AgentRun 创建时必须记录：

```text
contractVersion
AgentDefinition version
AgentPackage version
PolicySet version
ToolDefinition versions
SkillDefinition versions
Model provider / model version
```

### 14.3 Task 版本策略

```text
Task 创建时记录 AgentVersion 和 PolicySetID。
长 Task 默认不静默升级。
升级必须通过 task.command(upgrade_agent_version) 或 TaskUpgradePolicy。
```

---

## 15. Migration 规则

### 15.1 数据库 migration

每个 DB 变更必须有：

```text
up migration
down migration 或 rollback 说明
影响表
是否锁表
是否需要后台数据修复
```

### 15.2 事件 migration

默认不迁移历史 TaskEvent / AuditEvent。

如果必须迁移，必须：

```text
1. 保留原始事件。
2. 追加 migration event。
3. 记录 migration actor。
4. 保留映射关系。
```

### 15.3 JSONB migration

对 JSONB 字段新增字段时：

```text
读取时兼容缺失字段。
写入时写新字段。
不强制立即回填历史数据。
```

---

## 16. 开发流程

### 16.1 开发前

开发人员开始模块开发前，必须确认：

```text
1. 使用的 contract 版本。
2. 依赖的接口是否在冻结清单内。
3. 是否需要 stub 其他模块。
4. 是否需要新增字段。
5. 是否涉及状态机。
6. 是否涉及 Audit。
```

### 16.2 开发中

发现文档不完善时：

```text
1. 不要私自改核心结构。
2. 先判断是 P0 / P1 / P2 / P3。
3. P2 可模块内解决。
4. P1 提 Contract Change Proposal。
5. P0 提 ADR。
6. 修改后同步文档、测试和 migration。
```

### 16.3 开发后

每个模块交付必须包含：

```text
1. interface 实现。
2. 单元测试。
3. 状态机测试。
4. 错误码测试。
5. Trace / Audit 点测试。
6. README 或 package doc。
7. 如果涉及 DB，提供 migration。
```

---

## 17. PR 检查清单

每个 PR 必须回答：

```text
1. 是否修改了 contracts？
2. 是否修改了数据库表？
3. 是否修改了状态机？
4. 是否修改了 Policy 语义？
5. 是否修改了 Trace / Audit？
6. 是否影响旧 AgentRun 回放？
7. 是否影响旧 Task 恢复？
8. 是否需要 Eval 更新？
9. 是否需要文档更新？
10. 是否需要 migration？
```

如果任一问题为“是”，必须标注变更等级。

---

## 18. 模块级冻结建议

### 18.1 第一轮冻结模块

优先冻结：

```text
contracts
task-runtime
agent-run
decision schema
tool call/result
trace/audit
```

原因：

```text
这些是其他模块的共同依赖。
不先稳定，后面所有模块都会反复返工。
```

### 18.2 第二轮冻结模块

```text
agent-definition
policy-engine
context-engine
tool-runtime
execution-domain
```

原因：

```text
这些依赖第一轮基础契约。
需要在主链路跑通后细化。
```

### 18.3 第三轮冻结模块

```text
handoff
eval
release
collaboration bridge
managed adapter
```

原因：

```text
这些复杂度较高，可以先按接口预留，后续在集成中稳定。
```

---

## 19. 开发中常见问题处理

### 19.1 发现 TaskStatus 不够用

优先判断：

```text
能否用 TaskEvent 表达？
能否用 metadata 表达？
是否真的需要新增状态？
```

如果确实需要新增状态：

```text
P1 变更
更新状态机表
更新 task-runtime 测试
更新文档
```

### 19.2 ToolResult 需要更多执行信息

优先新增：

```text
executionMetadata
executionDomainRef
durationMs
resourceUsage
```

不要重构 ToolResult 主结构。

### 19.3 PromptBundle 太长

不要改事实源。

应调整：

```text
ContextCompressionPolicy
ToolResult summary
ArtifactRef rendering
SkillInstruction loading
```

### 19.4 Handoff 上下文不够

优先新增：

```text
HandoffContextPackage optional refs
keyFacts
constraints
expectedOutput
```

不要把完整上游 PromptBundle 传给下游 Agent。

### 19.5 外部协作系统需要更多字段

优先扩展：

```text
CollaborationContext metadata
ExternalTaskBinding metadata
CollaborationProvider adapter
```

不要在 Clean Core 中新增 Group 模块。

---

## 20. 最终判断

接口冻结不是为了限制开发，而是为了让开发有共同基线。

正确理解：

```text
冻结 = 当前阶段按这版开发
变更 = 发现问题后受控调整
兼容 = 尽量新增，不破坏旧逻辑
版本 = 新旧任务可共存
审计 = 所有关键变化可追踪
```

一句话：

```text
Clean Core 的开发方式不是“先写死再祈祷不变”，
而是“先冻结基线，再用受控变更支持真实开发中的演进”。
```
