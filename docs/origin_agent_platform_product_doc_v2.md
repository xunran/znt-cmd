# 原智能体平台产品架构文档 v2.0

版本：v2.0 Product Architecture  
日期：2026-06-01  
定位：面向产品、架构、研发、解决方案和企业客户的多智能体平台产品文档  
重构依据：在 v1.2 Clean Core 研发文档基础上，引入 AgentPlugin Service、API 化资产注册、控制面 / 执行面分离、企业数据本地化、安全执行域、多智能体互操作、可治理运营等产品理念。

---

## 0. 文档目标

本文档用于重新定义“原智能体”的产品形态。

老版本文档主要回答：

```text
如何开发一个可治理、可审计、可恢复、可扩展的 Agent Runtime Core？
```

新版本文档重点回答：

```text
我们如何把原智能体升级为一个多智能体运行与治理平台？
为什么用户不直接自己搭 Agent，而要使用我们的平台？
开发者如何通过 API 注册 Prompt / Skill / Tool Manifest，并通过 AgentPlugin Service 提供工具实现？
企业如何在数据本地化要求下使用平台能力？
平台如何统一运行、治理、审计、多智能体协作和版本运营？
```

本文不是纯研发接口文档，也不是营销白皮书，而是一份产品架构文档。它兼顾：

- 产品定位
- 用户价值
- 平台能力
- 架构边界
- 关键流程
- API 注册模式
- 企业部署模式
- MVP 范围
- 后续路线图

---

## 1. 产品定位

### 1.1 一句话定位

原智能体平台是一个面向生产环境的多智能体运行与治理平台。

它让企业和开发者可以通过标准 API 注册智能体的提示词、Skill、工具声明、运行策略和权限配置，并通过独立的 AgentPlugin Service 提供专属工具实现；平台 CleanCore 统一负责智能体运行、任务状态、工具治理、权限审批、记忆管理、全链路 Trace / Audit、多智能体协作和版本运营。

### 1.2 产品核心公式

```text
原智能体平台
  = CleanCore 控制面
  + AgentPlugin Service 执行面
  + API 化 Agent 资产注册
  + 统一 Tool / Skill / Prompt / Memory / Task 协议
  + 全链路治理与审计
```

进一步拆解：

```text
Agent = 平台托管的智能体定义
      + API 注册的 Prompt / Skill / Tool Manifest / RuntimeProfile
      + AgentPlugin Service 提供的工具实现
      + 平台 CleanCore 提供的运行与治理能力
```

### 1.3 产品不是 什么

原智能体平台不是：

```text
一个单体 Agent Demo
一个单纯聊天机器人
一个只有 Prompt 管理的后台
一个让开发者完全自建 Runtime 的 SDK
一个把所有业务代码都上传到平台执行的插件市场
一个绕过企业数据边界的大模型代理
```

它真正解决的是：

```text
多智能体如何被统一创建、运行、协作、治理、审计、调优和持续运营。
```

---

## 2. 核心用户价值

### 2.1 用户为什么不直接自己搭 Agent？

自己搭第一个 Agent 很容易：

```text
Prompt + LLM API + Tool Calling + 几个函数
```

但进入真实业务后，用户马上会遇到：

```text
这个 Agent 调了什么工具？
为什么调这个工具？
是否越权？
是否需要审批？
出了问题怎么回放？
不同 Agent 如何互相调用？
工具 schema 如何统一？
记忆写错了怎么办？
Prompt 改了如何灰度？
多个团队如何复用 Skill 和 Tool？
线上版本如何回滚？
客户数据如何隔离？
成本如何管控？
效果如何评测？
```

平台的价值是把这些能力产品化。

### 2.2 最重要的价值主张

```text
让开发者专注智能体领域能力，平台负责运行、治理、协作和观测。
```

或：

```text
从 Agent Demo 到 Agent Production 的统一运行平台。
```

### 2.3 六类核心价值

#### 价值一：让 Agent 从 Demo 进入生产

平台提供：

```text
任务状态
长任务恢复
工具治理
权限审批
Trace / Audit
版本灰度
回滚
Eval
多智能体协作
```

用户不只是“做出一个能回答的 Agent”，而是“运营一组可治理的生产级 Agent”。

#### 价值二：统一治理，避免企业内部野蛮生长

如果每个团队自己搭 Agent，会产生：

```text
多套工具调用协议
多套权限系统
多套日志系统
多套记忆系统
多套 Prompt 管理方式
```

平台统一：

```text
Agent Protocol
Task / Run
Tool Manifest
Tool Runtime
Prompt Registry
Skill Registry
Memory Service
Policy / Approval
Trace / Audit
Handoff
Runtime Profile
```

#### 价值三：降低开发门槛

开发者只需负责：

```text
注册 Agent
维护 Prompt
定义 Skill
注册 Tool Manifest
实现 AgentPlugin Service 工具能力
声明权限和运行策略
```

平台负责：

```text
运行循环
模型决策
工具调度
任务状态
权限审批
Trace / Audit
记忆治理
多智能体通信
版本管理
```

#### 价值四：让多个 Agent 互操作

平台提供标准能力：

```text
agent.task.start
agent.task.status
agent.task.command
agent.task.result
agent.tools.list
agent.tools.describe
agent.tools.invoke
```

这样每个 Agent 都可以被发现、被委派、被查询进度、被调用公开工具。

#### 价值五：沉淀可复用资产

平台沉淀：

```text
Prompt 模板
Skill 包
Tool Manifest
Runtime Profile
Memory Policy
Approval Policy
Eval Case
Agent Template
行业 Agent 蓝图
```

用户越用，平台资产越多，后续 Agent 创建越快。

#### 价值六：支持企业数据本地化和安全执行

通过 AgentPlugin Service：

```text
敏感数据可以留在企业本地
平台只管理密文、引用、hash、任务状态和审计事件
解密、模型调用、私有系统访问在企业可控环境中完成
```

这让平台可以进入强合规、私有化、数据本地化场景。

---

## 3. 新产品架构总览

### 3.1 控制面 / 执行面分离

新架构采用 Control Plane / Execution Plane 分离。

```text
Multi-Agent Platform CleanCore = 控制面
AgentPlugin Service = 执行面
```

### 3.2 控制面职责

平台 CleanCore 负责：

```text
Agent Registry
Prompt Registry
Skill Registry
Tool Manifest Registry
Runtime Profile Registry
Policy / Approval
Task / Run Runtime
Decision Center
Tool Runtime
Memory Service
Handoff Service
Trace / Audit
Eval / Release / Rollback
Tenant / Permission
Artifact Store
```

### 3.3 执行面职责

AgentPlugin Service 负责：

```text
专属工具实现
领域业务逻辑
私有系统连接
私有数据库访问
企业本地模型调用
数据解密 / 加密
特定 Hook 实现
特殊计算能力
```

### 3.4 总体架构图

```text
开发者 / 智能体工厂 / 低代码 Builder
        ↓ API 注册
┌─────────────────────────────────────────────┐
│ Multi-Agent Platform CleanCore              │
│                                             │
│  Agent Registry                             │
│  Prompt Registry                            │
│  Skill Registry                             │
│  Tool Manifest Registry                     │
│  Runtime Profile Registry                   │
│  Policy / Approval                          │
│  Task / Run Repository                      │
│  Decision Center                            │
│  Tool Runtime                               │
│  Memory Service                             │
│  Handoff Service                            │
│  Trace / Audit / Metrics                    │
└─────────────────────────────────────────────┘
        ↓ 标准工具调用协议
┌─────────────────────────────────────────────┐
│ AgentPlugin Service                         │
│                                             │
│  Tool Implementation                        │
│  Hook Implementation                        │
│  Domain Logic                               │
│  Private System Connector                   │
│  Local Model Provider                       │
│  Encryption / Decryption                    │
└─────────────────────────────────────────────┘
        ↓
企业私有系统 / 本地数据库 / 本地模型 / HSM / KMS
```

---

## 4. 核心概念重构

### 4.1 Agent

Agent 是平台中可被调用、可被委派、可被查询状态、可暴露工具能力的智能体实体。

Agent 由平台定义和运行，不等于某个独立服务本身。

```text
Agent = Identity + Prompt + Skill + Tool Manifest + RuntimeProfile + Policy + AgentPlugin Binding
```

### 4.2 AgentPlugin Service

AgentPlugin Service 是 Agent 的专属插件服务。

它不是完整 Agent Runtime，而是 Agent 的工具实现与领域能力服务。

```text
AgentPlugin Service 不负责：
- Agent 主运行循环
- Task / Run 状态机
- Decision Center
- Tool Policy
- Trace / Audit 主链路
- Memory 全局治理

AgentPlugin Service 负责：
- 执行平台调用的工具
- 访问业务系统
- 解密敏感输入
- 调用企业本地模型
- 返回标准 ToolResult
```

### 4.3 Prompt

Prompt 是平台管理的 Agent 资产。

它可以通过 API 新增、修改、版本化、发布和回滚。

Prompt 不直接写入运行事实。

运行时每一轮会基于当前 Task / Memory / Skill / Tool Candidate / RuntimePhase 构建 PromptBundle。

### 4.4 Skill

Skill 是任务能力包。

它定义：

```text
什么时候使用
处理某类任务的方法
推荐工具
推荐记忆读写
推荐委派 Agent
输出格式
风险边界
完成标准
```

Skill 不是工具实现，也不是 RuntimeDriver。

Skill 是认知层方法包，工具是执行层能力。

### 4.5 Tool Manifest

Tool Manifest 是工具声明。

它定义：

```text
工具名称
工具描述
输入 schema
输出 schema
风险等级
可见性
执行模式
权限要求
对应 AgentPlugin Service endpoint
```

平台基于 Tool Manifest 做候选召回、模型提示、参数校验、权限审批和审计。

### 4.6 Tool Implementation

Tool Implementation 由 AgentPlugin Service 提供。

平台不需要知道内部实现，只需通过标准协议调用。

### 4.7 RuntimeProfile

RuntimeProfile 定义 Agent 使用哪种运行策略。

可选模式包括：

```text
single_turn
react_loop
plan_guided_loop
tree_search
parallel_planning
multi_worker
human_in_the_loop
streaming_continuous
external_runtime_driver
```

RuntimeProfile 控制“怎么跑”，但仍不能绕过平台 CleanCore。

### 4.8 CleanCore

CleanCore 是平台稳定内核。

它负责所有不可被 AgentPlugin 绕过的能力：

```text
Agent 输入输出协议
Task / Run 状态
Decision Validator
ToolRuntime
Policy / Approval
MemoryService
Trace / Audit
Tenant Isolation
HandoffService
ArtifactStore
```

---

## 5. 产品能力地图

### 5.1 Agent 资产管理

能力：

```text
创建 Agent
编辑 Agent 元信息
注册 Prompt
注册 Skill
注册 Tool Manifest
绑定 AgentPlugin Service
配置 RuntimeProfile
配置 MemoryPolicy
配置 HandoffPolicy
配置权限
版本发布
灰度
回滚
```

### 5.2 智能体运行能力

能力：

```text
AgentEnvelope 输入
Task 创建 / 恢复
多轮 Decision Loop
结构化 Decision
Tool Call
Tool Result
Artifact 生成
长任务状态
任务暂停 / 恢复 / 取消
审批等待
最终结果输出
```

### 5.3 AgentPlugin Service 接入能力

能力：

```text
插件服务注册
插件健康检查
工具发现
工具调用
Hook 调用
服务认证
超时 / 重试 / 熔断
版本兼容检查
ToolResult 校验
```

### 5.4 多智能体协作能力

能力：

```text
Agent 发现
Agent Capability 查询
任务委派
HandoffContextPackage
父子任务
进度查询
结果回流
跨 Agent Trace / Audit
```

### 5.5 治理与安全能力

能力：

```text
权限控制
租户隔离
工具可见性
风险等级
审批流
数据边界
凭证隔离
敏感数据脱敏
Memory 写入治理
Prompt 注入防护
```

### 5.6 可观测与运营能力

能力：

```text
Agent Trace
Audit Log
Metrics
Prompt Snapshot
Decision Snapshot
ToolCall Trace
Memory Trace
Policy Decision Log
成本统计
模型调用统计
失败分析
Replay
```

### 5.7 企业数据本地化能力

能力：

```text
密文输入
AgentPlugin Service 本地解密
本地模型调用
本地业务系统访问
密文输出
平台只保存引用 / hash / 脱敏摘要
HSM / KMS 集成
数据不出企业边界
```

---

## 6. API 注册模式

### 6.1 API 注册的定位

平台通过 API 管理 Agent 的配置资产。

AgentPlugin Service 只负责实现工具能力。

```text
API 注册：定义 Agent 能力
AgentPlugin Service：执行 Agent 能力
CleanCore：治理和运行 Agent 能力
```

### 6.2 推荐资源模型

```text
Agent
Prompt
Skill
ToolManifest
RuntimeProfile
MemoryPolicy
HandoffPolicy
PermissionPolicy
AgentPluginBinding
Release
EvalSuite
```

### 6.3 核心 API 草案

```http
POST   /v1/agents
GET    /v1/agents/{agentId}
PATCH  /v1/agents/{agentId}

POST   /v1/agents/{agentId}/prompts
PUT    /v1/agents/{agentId}/prompts/{promptId}

POST   /v1/agents/{agentId}/skills
PUT    /v1/agents/{agentId}/skills/{skillId}

POST   /v1/agents/{agentId}/tools
PUT    /v1/agents/{agentId}/tools/{toolName}

PUT    /v1/agents/{agentId}/runtime-profile
PUT    /v1/agents/{agentId}/memory-policy
PUT    /v1/agents/{agentId}/handoff-policy
PUT    /v1/agents/{agentId}/permissions

POST   /v1/agents/{agentId}/validate
POST   /v1/agents/{agentId}/evals/run
POST   /v1/agents/{agentId}/publish
POST   /v1/agents/{agentId}/rollback
```

### 6.4 创建 Agent 示例

```json
{
  "agentId": "finance-agent",
  "name": "财务审核智能体",
  "description": "负责发票识别、财务材料审核和风险摘要生成。",
  "runtime": {
    "profileId": "plan-guided-runtime-v1"
  },
  "pluginService": {
    "serviceId": "finance-agent-plugin",
    "baseUrl": "https://finance-plugin.example.com"
  }
}
```

### 6.5 注册 Tool Manifest 示例

```json
{
  "name": "finance.invoice.extract",
  "description": "从发票文件中提取结构化字段。",
  "inputSchema": {
    "type": "object",
    "required": ["fileRef"],
    "properties": {
      "fileRef": { "type": "string" }
    }
  },
  "outputSchema": {
    "type": "object",
    "properties": {
      "invoiceNo": { "type": "string" },
      "amount": { "type": "number" },
      "seller": { "type": "string" }
    }
  },
  "visibility": "protected",
  "riskLevel": "medium",
  "implementation": {
    "type": "agent_plugin_service",
    "serviceId": "finance-agent-plugin",
    "endpoint": "/tools/finance.invoice.extract/invoke"
  }
}
```

### 6.6 注册 Skill 示例

```json
{
  "skillId": "invoice-risk-review",
  "name": "发票风险审核 Skill",
  "description": "用于检查发票字段完整性、金额异常和供应商风险。",
  "whenToUse": [
    "用户要求审核发票",
    "任务中包含发票文件或报销材料"
  ],
  "instructions": "先提取发票字段，再检查金额、供应商、日期和重复报销风险，最后输出结构化审核结论。",
  "recommendedTools": [
    "finance.invoice.extract",
    "finance.vendor.risk_check"
  ],
  "outputSchema": {
    "type": "object",
    "properties": {
      "riskLevel": { "type": "string" },
      "findings": { "type": "array" },
      "recommendation": { "type": "string" }
    }
  }
}
```

---

## 7. AgentPlugin Service 协议

### 7.1 标准接口

AgentPlugin Service 推荐实现：

```http
GET  /.well-known/agent-plugin.json
GET  /health
GET  /tools
GET  /tools/{toolName}
POST /tools/{toolName}/invoke
POST /hooks/{hookName}/invoke
```

### 7.2 插件发现接口

```http
GET /.well-known/agent-plugin.json
```

响应：

```json
{
  "serviceId": "finance-agent-plugin",
  "name": "Finance Agent Plugin Service",
  "version": "1.0.0",
  "supportedAgents": ["finance-agent"],
  "tools": [
    {
      "name": "finance.invoice.extract",
      "executionMode": "sync",
      "inputSchemaRef": "/schemas/finance.invoice.extract.input.json",
      "outputSchemaRef": "/schemas/finance.invoice.extract.output.json"
    }
  ],
  "security": {
    "auth": "mTLS + signed_request",
    "dataBoundary": "enterprise_local"
  }
}
```

### 7.3 工具调用请求

```json
{
  "traceId": "trace_001",
  "runId": "run_001",
  "taskId": "task_001",
  "toolCallId": "toolcall_001",
  "caller": {
    "tenantId": "tenant_001",
    "agentId": "finance-agent",
    "userId": "user_001"
  },
  "arguments": {
    "fileRef": "artifact://file_123"
  },
  "context": {
    "locale": "zh-CN",
    "timezone": "Asia/Shanghai"
  }
}
```

### 7.4 工具调用响应

```json
{
  "toolCallId": "toolcall_001",
  "status": "success",
  "result": {
    "invoiceNo": "INV-001",
    "amount": 1200.5,
    "seller": "某某供应商"
  },
  "artifacts": [],
  "metadata": {
    "durationMs": 830,
    "dataAccessMode": "local_decrypt"
  }
}
```

### 7.5 AgentPlugin Service 不能做什么

AgentPlugin Service 不能：

```text
直接改平台 Task / Run 状态
直接绕过 ToolRuntime 调用其他平台工具
直接写平台长期 Memory
直接发布 Prompt / Skill / Tool Manifest
直接绕过平台 Policy / Approval
直接伪造 Trace / Audit
直接调用其他 Agent 私有工具
```

所有这些动作必须回到平台 CleanCore。

---

## 8. 企业数据本地化与加密执行

### 8.1 场景定位

面向以下客户：

```text
金融
政企
医疗
大型制造
法律
核心研发团队
数据强监管行业
```

他们要求：

```text
敏感数据不出企业边界
平台不能持有明文
模型调用必须发生在企业可控环境
密钥由企业掌控
审计仍需完整
```

### 8.2 加密执行架构

```text
企业客户端 / 企业系统
  ↓ 本地加密
平台 Agent Gateway
  ↓ 密文 / 引用 / hash
CleanCore Task / Run / Trace
  ↓ ToolRuntime 调用
AgentPlugin Service
  ↓ 本地解密
本地模型 / 私有工具 / 企业数据库
  ↓ 结果生成
AgentPlugin Service 加密输出
  ↓
平台保存密文结果 / ArtifactRef / hash
  ↓
企业客户端解密查看
```

### 8.3 数据状态表

| 环节 | 数据状态 | 责任方 |
|---|---|---|
| 企业客户端 | 明文 | 企业 |
| 进入平台前 | 加密 | 企业 SDK / 客户端 |
| 平台 CleanCore | 密文 / 引用 / hash | 平台 |
| AgentPlugin Service | 明文短暂可见 | 企业可控执行域 |
| 模型调用 | 明文或受控脱敏明文 | 企业本地模型 / 企业授权模型 |
| 返回平台 | 密文 / 脱敏摘要 | AgentPlugin Service |
| 用户查看 | 解密后明文 | 企业客户端 |

### 8.4 平台可记录什么

平台可以记录：

```text
traceId
runId
taskId
toolName
调用时间
调用状态
耗时
密文 hash
artifactRef
权限判断结果
审批结果
错误类型
成本指标
```

平台不应记录：

```text
敏感明文输入
完整 prompt 明文
完整模型明文输出
解密后的工具参数
私有数据库查询明细
密钥
```

### 8.5 AgentPlugin Service 安全要求

企业本地 AgentPlugin Service 应支持：

```text
mTLS
请求签名
KMS / HSM
密钥轮换
内存中短暂解密
日志脱敏
最小权限访问
网络出口控制
工具级权限
本地审计副本
```

### 8.6 与模型定义的关系

如果客户要求模型也本地化，则模型供应商定义也可以放在 AgentPlugin Service 中。

平台只知道：

```text
该 Agent 使用 plugin_managed_model
```

但不直接接触模型明文请求。

模型调用链路：

```text
Decision Center 生成模型调用意图
  ↓
ModelRuntime 识别该 Agent 使用 plugin-managed model
  ↓
调用 AgentPlugin Service /model/generate
  ↓
Plugin 本地解密上下文并调用本地模型
  ↓
返回结构化 Decision 或加密响应
```

这种模式适合高安全客户，但需要更严格的可观测设计，因为平台无法看到完整 Prompt 明文。

可替代方案：

```text
平台保留 PromptBundle hash + 模板版本 + 上下文引用
企业本地保存完整 Prompt 明文审计副本
双方通过 traceId 关联
```

---

## 9. 运行链路

### 9.1 普通任务运行

```text
用户 / 外部 Agent
  ↓
AgentEnvelope
  ↓
Agent Gateway
  ↓
Task Runtime 创建 / 恢复 Task
  ↓
PromptKit 构建 Runtime-aware PromptBundle
  ↓
SkillKit 召回相关 Skill
  ↓
Decision Center 调用模型生成 Decision
  ↓
Decision Validator 校验
  ↓
ToolRuntime 准备执行 ToolCall
  ↓
Policy / Approval 判断
  ↓
AgentPlugin Service 执行工具
  ↓
ToolResult 返回
  ↓
Task / Artifact / Memory 更新
  ↓
继续下一轮或生成最终回复
```

### 9.2 外部 Agent 调用本 Agent 公开工具

```text
External Agent
  ↓ agent.tools.list / describe
Platform Tool Gateway
  ↓ 检查 exposedToolIds
Policy / Permission
  ↓
ToolRuntime
  ↓
AgentPlugin Service
  ↓
ToolResult
```

### 9.3 Agent-to-Agent Handoff

```text
Agent A
  ↓ origin.agent.delegate
Policy / HandoffPolicy
  ↓
ContextEngine 生成 HandoffContextPackage
  ↓
TaskRuntime 创建 ChildTask
  ↓
Agent B 执行
  ↓
结果回流 ParentTask
  ↓
Trace / Audit 记录完整链路
```

---

## 10. Runtime 模式

平台应支持多种 RuntimeProfile。

### 10.1 Single Turn

适合：

```text
简单问答
简单分类
轻量转换
```

### 10.2 ReAct Loop

适合：

```text
多轮工具调用
查询 + 分析 + 回复
```

### 10.3 Plan-guided Loop

适合：

```text
多步骤任务
报告生成
数据分析
审批任务
```

### 10.4 Tree Search / Candidate Planning

适合：

```text
复杂方案比较
研究任务
策略规划
```

注意：

```text
不要求模型输出完整思维链，而是输出结构化候选方案、评分、风险和下一步动作。
```

### 10.5 Parallel Planning

适合：

```text
多视角规划
架构评审
风险审查
复杂决策
```

### 10.6 Multi Worker

适合：

```text
大文件处理
多模块代码分析
资料收集
并行子任务执行
```

### 10.7 Human-in-the-loop

适合：

```text
高风险工具
配置修改
发布操作
长期记忆写入
```

### 10.8 External Runtime Driver

适合：

```text
客户自定义执行循环
特殊并行调度
专属 Planner / Worker 架构
企业本地强控制场景
```

边界：

```text
RuntimeDriver 可以决定下一步想做什么。
KernelGateway 决定能不能做。
ToolRuntime / Policy / Memory / Trace / Audit 仍不可绕过。
```

---

## 11. Prompt / Skill / Tool 的关系

### 11.1 三者定位

```text
Prompt：模型当前应该如何理解任务和输出。
Skill：某类任务通常应该怎么做。
Tool：具体执行能力。
```

### 11.2 运行时关系

```text
RuntimeDriver 决定当前 phase / role / step
  ↓
PromptKit 构建 PromptBundle
  ↓
SkillKit 注入相关 Skill
  ↓
Tool Manifest 注入可用工具摘要
  ↓
Decision Center 选择 reply / no_op / ask_clarification / tool_call
  ↓
ToolRuntime 执行工具
```

### 11.3 Skill 可以定义什么

Skill 可以定义：

```text
推荐步骤
推荐工具
推荐记忆读取
推荐记忆写入
推荐委派 Agent
完成标准
风险边界
输出 schema
```

### 11.4 Skill 不能定义什么

Skill 不能定义：

```text
底层运行循环
并发调度
失败重试机制
状态恢复机制
审批绕过
直接工具执行
直接 memory 写入
```

这些属于 RuntimeDriver + CleanCore。

---

## 12. Trace / Audit 产品能力

### 12.1 Trace 回答什么

Trace 回答：

```text
这次 Agent 从输入到输出，每一步是怎么发生的？
```

记录：

```text
input.received
agent.loaded
task.created
workview.built
skill.retrieved
promptbundle.built
model.called
model.completed
decision.created
decision.validated
tool.policy_checked
tool.invoked
tool.completed
task.status_changed
response.sent
```

### 12.2 Audit 回答什么

Audit 回答：

```text
谁做了什么？是否允许？是否审批？是否影响关键资产？
```

必须审计：

```text
外部调用
高风险工具调用
权限拒绝
审批
Prompt / Skill / Tool Manifest 变更
Agent 发布 / 回滚
Policy 变更
Memory 写入
Artifact 删除
跨 Agent Handoff
数据解密请求
本地模型调用请求
```

### 12.3 企业本地化场景的 Trace / Audit

当平台不持有明文时，Trace 记录：

```text
密文 hash
ArtifactRef
PromptBundle hash
模板版本
上下文引用
插件服务调用结果
企业本地审计引用
```

企业本地保存完整明文审计副本，并通过 traceId 与平台关联。

---

## 13. 产品边界

### 13.1 平台负责

```text
Agent 注册
Prompt / Skill / Tool Manifest 管理
RuntimeProfile 管理
Task / Run 管理
Decision Center
ToolRuntime
Policy / Approval
MemoryService
HandoffService
Trace / Audit
Eval / Release / Rollback
多租户和权限
```

### 13.2 AgentPlugin Service 负责

```text
工具实现
业务系统连接
私有数据访问
数据解密 / 加密
本地模型调用
专属 Hook 服务
领域算法
```

### 13.3 外部协作系统负责

```text
群组
频道
消息通知
@mention 解析
外部任务池
人员组织
IM 消息流
```

平台通过 Input Adapter 接收标准 AgentEnvelope，不直接实现完整协作系统。

---

## 14. 权限与可见性模型

### 14.1 工具可见性

```text
private：只允许 Agent 内部或系统内部使用
protected：允许 Agent 决策使用，但不对外公开调用
public / exposed：允许外部 Agent 或调用方通过 tools.invoke 调用
```

### 14.2 Agent 权限

Agent 需要声明：

```text
可调用哪些平台工具
可调用哪些 AgentPlugin 工具
可读取哪些 Memory Scope
可写入哪些 Memory Scope
可委派哪些 Agent
可暴露哪些工具给外部
是否允许使用本地模型
是否允许解密数据
```

### 14.3 AgentPlugin Service 权限

插件服务需要声明：

```text
服务身份
支持的 Agent
支持的工具
数据边界
认证方式
加密能力
模型调用方式
私有系统连接范围
```

---

## 15. 版本、发布、灰度和回滚

### 15.1 可版本化资产

```text
Agent Definition
Prompt
Skill
Tool Manifest
RuntimeProfile
MemoryPolicy
HandoffPolicy
PermissionPolicy
EvalSuite
AgentPlugin Service Version
```

### 15.2 发布流程

```text
Draft
  ↓
Validate
  ↓
Eval
  ↓
Review
  ↓
Publish
  ↓
Canary
  ↓
Stable
  ↓
Rollback if needed
```

### 15.3 版本钉住

一次 AgentRun 必须记录：

```text
AgentDefinition version
Prompt version
Skill version
ToolManifest version
Policy version
RuntimeProfile version
AgentPlugin Service version
Model provider / model version
PromptBundle hash
```

长任务默认不静默升级。

---

## 16. MVP 范围建议

### 16.1 MVP 必须包含

```text
Agent API 注册
Prompt 注册
Skill 注册
Tool Manifest 注册
AgentPlugin Service HTTP Tool 调用
基础 Task / Run
基础 Decision Loop
ToolRuntime + Policy Check
基础 Trace / Audit
exposed tools.invoke
AgentPlugin Service 健康检查
基础发布 / 回滚
```

### 16.2 MVP 可以暂缓

```text
复杂 RuntimeDriver 插件
Sandbox Function 执行
完整插件市场
复杂并行 worker
高级自动 eval
全量 replay
多云模型路由
复杂成本优化
```

### 16.3 MVP 最重要闭环

```text
通过 API 创建 Agent
  ↓
添加 Prompt
  ↓
添加 Skill
  ↓
注册 Tool Manifest
  ↓
绑定 AgentPlugin Service
  ↓
发布 Agent
  ↓
用户发起任务
  ↓
平台决策并调用插件服务工具
  ↓
返回结果
  ↓
Trace / Audit 可查询
```

---

## 17. 产品路线图

### 阶段一：Agent 资产注册与运行闭环

目标：跑通从 API 注册到 Agent 执行。

能力：

```text
Agent Registry
Prompt Registry
Skill Registry
Tool Manifest Registry
AgentPlugin Service HTTP Connector
Decision Loop
Trace / Audit
```

### 阶段二：生产级治理

目标：让 Agent 可安全上线。

能力：

```text
权限策略
审批
版本发布
灰度
回滚
Eval
外部 tools.invoke
租户隔离
```

### 阶段三：多智能体协作

目标：形成 Agent 网络。

能力：

```text
Agent Capability Discovery
Agent Handoff
Child Task
HandoffContextPackage
跨 Agent Trace / Audit
```

### 阶段四：企业本地化与安全执行

目标：满足强合规客户。

能力：

```text
密文数据流
AgentPlugin 本地解密
本地模型调用
KMS / HSM 集成
平台 / 企业双审计
数据边界策略
```

### 阶段五：高级 Runtime 与生态

目标：形成可扩展平台生态。

能力：

```text
RuntimeDriver 插件
Parallel Planning
Multi Worker
Skill Marketplace
Tool Marketplace
Agent Template
行业解决方案包
```

---

## 18. 与老 Clean Core 文档的关系

老文档的核心资产仍然保留：

```text
Runtime Kernel
Task Runtime
Context Engine
Capability Discovery
Decision Engine
Model Runtime
Policy Engine
Tool Runtime
Execution Domain
Memory Artifact
Governance
```

新文档的核心升级是：

```text
1. 从研发内核文档升级为产品平台文档。
2. 从 AgentPackage 内部包管理升级为 API 化 Agent 资产管理。
3. 从平台内部执行工具升级为 AgentPlugin Service 外部执行工具。
4. 从 Runtime Core 定位升级为 Multi-Agent Runtime Platform。
5. 从单一执行域升级为控制面 / 执行面分离。
6. 从普通工具执行升级为企业数据本地化安全执行。
7. 从单 Agent 运行升级为多 Agent 互操作和 Agent 网络。
```

---

## 19. 最终总结

原智能体平台的新定位是：

```text
一个面向生产环境的多智能体运行与治理平台。
```

它的关键设计是：

```text
平台 CleanCore 统一管理 Agent 定义、Prompt、Skill、Tool Manifest、任务、决策、治理和审计。
AgentPlugin Service 独立部署，负责工具实现、私有系统连接、数据解密和本地模型调用。
开发者通过 API 注册和更新智能体资产，不需要重建多智能体运行基础设施。
企业可以在数据不出边界的情况下获得平台级多智能体治理能力。
```

最重要的一句话：

```text
我们不是让用户再写一个 Agent，而是让用户把自己的 Agent 能力接入一个可运行、可治理、可协作、可审计、可持续运营的多智能体平台。
```
