
# Origin Agent Platform P3 全景开发计划与 MMR 交付计划

版本：v1.1  
日期：2026-06-02  
定位：在 V3 P0 / P1 / P2 已完成基础上，规划 P3 全景路线，并切出可销售的 MMR 版本  
架构口径：PloyKit 做模块化产品宿主，CleanCore 做后端核心，AgentPlugin Service / ToolHost Service 做企业执行面  
执行口径：先完善 CleanCore 并完成 MMR 能力 API 化与真实服务验收，再对接 PloyKit 产品模块  

---

## 0. 文档前提

本文档基于以下前提：

```text
1. 《Origin Agent Platform V3 产品文档代码对照与开发计划 v0.1》中的 V3 MVP / P3 前置主干已经基本完成，但 ToolHost 真实 secret resolver、mTLS、健康调度、配额治理、PloyKit API Key 合约等仍属于 MMR 门禁项。
2. CleanCore 已具备 Agent 资产、Task / Run、PromptBundle、Decision、ToolRuntime、Policy / Approval、Trace / Audit / Replay、AgentPackage、Eval、Handoff、动态工具目录、Runtime Hook MVP、协作能力包基础服务等核心能力主干。
3. 当前目标不是继续补 P0 / P1 / P2 缺口，而是进入 P3：完整平台建设。
4. 但 P3 不能一次性全做完后才销售，必须从 P3 中切出 MMR，也就是最小可销售版本。
5. MMR 执行拆成两部分：第一部分先把 CleanCore 做到真实服务可用、能力 API 化、全链路测试通过；第二部分再把 PloyKit 作为产品宿主接上。
```

本文档解决两个问题：

```text
第一层：P3 全景路线图
- 完整平台最终怎么建设？
- PloyKit、CleanCore、AgentPlugin Service 如何分工？
- Runtime Extension、Collaboration、Knowledge、Commercial 如何演进？

第二层：P3 MMR 交付计划
- 第一版可销售版本到底做哪些？
- 哪些必须做？
- 哪些可以后置？
- 第一批客户能完成哪些用户旅程？
- 什么条件下可以进入 MMR Go？
- CleanCore Service Go 通过前，哪些 PloyKit 工作不得提前成为主线？
```

---

## 1. 核心结论

### 1.1 P3 的本质

P3 不是重做 CleanCore，也不是无序堆后端功能。

P3 的本质是：

```text
先把 CleanCore 中适合 MMR 的后端核心能力补齐、接口化、真实服务验收通过，
再通过 PloyKit 模块化产品宿主交付给企业用户，
并通过 AgentPlugin Service / ToolHost Service 把企业执行面接入，
最终形成一个可以创建、运行、治理、审计、发布、回滚和商业化的完整多智能体平台。
```

换句话说，P3 MMR 不是先做 UI，也不是先接 PloyKit。

P3 MMR 的第一道门是：

```text
CleanCore 作为独立服务已经可被外部产品稳定消费。
```

第二道门才是：

```text
把已经验收通过的 CleanCore 服务，
通过 PloyKit 模块化产品宿主交付给企业用户，
并通过 AgentPlugin Service 把企业执行面接入，
```

### 1.2 MMR 的本质

MMR 不是 P3 全部完成。

MMR 是：

```text
从 P3 全景中切出一条最小但完整、可销售、可部署、可试点验收的闭环。
这条闭环分两段交付：
第一段是 CleanCore MMR Service。
第二段是 PloyKit MMR Product。
```

MMR 完成后，第一批企业客户应该可以：

```text
1. 登录平台。
2. 创建 Workspace。
3. 创建和发布一个 Agent。
4. 接入一个 AgentPlugin Service / ToolHost。
5. 让 Agent 调用企业工具。
6. 对高风险工具调用进行审批。
7. 查看 Task / Run / Trace / Audit / Replay。
8. 通过 PloyKit API Key 调用 Agent。
9. 查看基础用量。
10. 完成一次可审计、可回滚的试点交付。
```

### 1.3 三层边界

```text
PloyKit = 模块优先的产品宿主 / Web Shell / Dashboard / Admin / Auth / RBAC / Workspace / API Key / Billing / Module Host
CleanCore = Agent Runtime 后端核心 / Task / Run / Decision / ToolRuntime / Policy / Handoff / Trace / Audit
AgentPlugin Service = ToolHost Service / 企业工具执行面 / 私有数据 / 本地凭证 / KMS / HSM / 本地模型 / 企业系统连接
```

---

## 2. PloyKit、CleanCore、AgentPlugin Service 分工

### 2.1 PloyKit 负责

```text
1. Web Shell。
2. Dashboard。
3. Admin。
4. Auth。
5. RBAC。
6. Workspace。
7. API Key 管理。
8. 计费、Credits、Entitlement、Usage / Metering 权威事实。
9. 文件和静态资源。
10. 通知。
11. 模块化产品页面、routes、navigation、surfaces。
12. module.ts contract、module map、module doctor、Release Gate。
13. PloyKit 自己的 Audit / Usage / Metering / Service Connection。
14. 通过 Origin 产品模块消费 CleanCore 受控服务。
```

PloyKit 不负责：

```text
Origin 专属 Shell 硬编码
在 host/shared 代码中硬编码 Origin module id / route
把每个菜单拆成宿主级特殊模块
Agent Decision Loop
ToolRuntime
PolicyDecision
Task / Run 权威事实
Handoff 生命周期
PromptBundle 构建
CleanCore Trace / Audit 权威事实
企业工具执行面
CleanCore service token 明文管理
```

### 2.2 CleanCore 负责

```text
1. Agent Asset Registry。
2. PromptProfile / Skill / ToolBinding / Collaborator / ExportedTool。
3. Task / Run Runtime。
4. WorkView / PromptBundle。
5. Decision Center。
6. ToolRuntime。
7. Policy / Approval。
8. AgentHandoff。
9. Artifact / Memory。
10. Runtime Hook。
11. Trace / Audit / Replay。
12. Eval / Release。
13. ToolProvider / ToolManifest / ToolGroup。
14. API / SDK / Governance Evidence。
```

### 2.3 AgentPlugin Service / ToolHost Service 负责

```text
1. 企业工具实现。
2. 企业系统连接。
3. 私有数据读取。
4. 本地解密。
5. 本地模型调用。
6. KMS / HSM / Secret 适配。
7. 本地审计。
8. ToolHost 协议。
```

### 2.4 禁止跨界

```text
PloyKit 模块不得直接写 CleanCore DB。
PloyKit read model 不得成为 Task / Run / ToolResult 权威事实。
PloyKit host/shared 不得硬编码 Origin 具体模块、路由或菜单。
PloyKit 模块不得裸 fetch CleanCore；高权限外部调用必须通过 ctx.services.invoke / serviceRequirements / service connection。
PloyKit 模块不得用 Data v2 自建 Workspace / API Key / Billing / Usage 权威事实。
CleanCore 不得保存明文敏感业务数据。
CleanCore 不得直接访问企业私有数据库。
AgentPlugin Service 不得直接写 CleanCore Task / Run / Memory。
AgentPlugin Service 不得绕过 CleanCore Policy / Approval。
RuntimeDriver 不得直接执行工具或写状态。
```

---

## 3. P3 全景能力范围

### 3.1 P3 全景主线

```text
P3-A：PloyKit 产品宿主与 Origin 产品模块集成
P3-B：Agent Studio 产品化
P3-C：Tool Platform / AgentPlugin Service 产品化
P3-D：Runtime Ops / Trace / Audit / Replay 产品化
P3-E：Runtime Hook / RuntimeDriver 受控高级扩展
P3-F：Collaboration / Knowledge / CrossGroup 产品化
P3-G：Commercial / Entitlement / Usage
P3-H：企业级安全、部署、发布、回滚、运维
```

### 3.2 P3 MMR 必须做

MMR 必须完成以下能力：

```text
Part A：CleanCore MMR Service 必须完成
1. MMR 所需 Agent / PromptProfile / Skill / ToolBinding / Release / Eval 能力 API 化。
2. ToolProvider / ToolManifest / ToolGroup / ToolHost 注册、catalog sync、smoke invoke API 化。
3. Task / Run / waiting_input / waiting_approval / Approval resolve API 化。
4. Trace / Audit / Replay / ToolCall / ToolResult / ArtifactRef 查询 API 化。
5. Policy / Approval / PloyKit 派生 caller scope / usage evidence / entitlement guard bridge 的 CleanCore 侧基线。
6. Tenant isolation、secret ref / auth_ref、ToolHost security、idempotency、provider unhealthy guard。
7. OpenAPI freeze、contract tests、真实 HTTP service tests、E2E regression。
8. Demo tenant、示例 Agent、示例 ToolHost、真实服务 Quickstart。

Part B：PloyKit MMR Product 必须完成
9. PloyKit 宿主接入与 Origin 产品模块 contract。
10. 复用 PloyKit Workspace / Auth / RBAC / API Key。
11. CleanCore service connection 与 client facade。
12. Agent Studio / Tool Console / Runtime Console / Usage View。
13. PloyKit degraded mode、navigation、i18n、module doctor、module tests。
14. PloyKit + CleanCore 联调 E2E。
```

MMR 执行门禁：

```text
CleanCore Service Go 之前，不进入 PloyKit 产品模块主线开发。
PloyKit 可以提前做契约调研和模块骨架，但不得替代 CleanCore API 验收。
```

### 3.3 P3 MMR 可选做

以下能力可以作为 MMR 增强，但不是 MMR 必须条件：

```text
1. Runtime Hook UI 基础版。
2. Collaboration Adapter 单一实现，例如 Array。
3. KnowledgeBase 基础包。
4. Billing UI 初版。
5. Eval 可视化增强。
6. Operations Dashboard 初版。
7. Artifact viewer 增强。
```

### 3.4 P3 MMR 后置

以下能力不进入 MMR 主线：

```text
1. 完整 RuntimeDriver 开放。
2. Strategy Hook 全量开放。
3. Tree planning。
4. Parallel planning。
5. 多 worker / queue / managed execution domain。
6. 复杂 CrossGroup 高级查询。
7. 多协作平台全适配。
8. Marketplace。
9. 第三方 Agent Provider 深度集成。
10. managed MCP 深度集成。
11. 完整 SaaS 计费系统。
12. 不可信第三方 PloyKit 模块运行。
```

---

## 4. P3 全景开发批次

| 批次 | 名称 | 目标 | MMR 状态 |
|---|---|---|---|
| P3-0 | 集成边界冻结 | 冻结 CleanCore ↔ ToolHost ↔ PloyKit 的职责与事实源边界 | 必须 |
| P3-A1 | CleanCore API Freeze | 将 MMR 所需能力全面 API 化，冻结 OpenAPI 和错误模型 | 必须 |
| P3-A2 | CleanCore Agent Studio APIs | 完成 Agent、Prompt、Skill、ToolBinding、Release、Eval 的服务 API | 必须 |
| P3-A3 | CleanCore ToolHost APIs | 完成 ToolProvider、ToolManifest、ToolGroup、ToolHost sync / invoke / health | 必须 |
| P3-A4 | CleanCore Runtime Ops APIs | 完成 Task / Run / Trace / Audit / Replay / Approval 操作 API | 必须 |
| P3-A5 | CleanCore Governance & Security | 完成 Policy、Approval、tenant isolation、secret ref、usage event、idempotency | 必须 |
| P3-A6 | CleanCore Real Service Acceptance | 通过真实 HTTP 服务、真实 ToolHost、真实模型或稳定 stub 的全链路测试 | 必须 |
| P3-B1 | PloyKit 宿主接入 | 以 PloyKit product-app / signed-service contract 承载 Origin 产品模块 | CleanCore Service Go 后必须 |
| P3-B2 | PloyKit Product Console | 完成 Agent Studio、Tool Console、Runtime Console、Usage View 产品化 | CleanCore Service Go 后必须 |
| P3-B3 | PloyKit Integration Acceptance | PloyKit + CleanCore 联调、degraded mode、module doctor、E2E | 必须 |
| P3-C | Post-MMR Extension | Runtime Extension、Collaboration、Knowledge、Commercial Advanced | 后置 / 可选 |

---

## 5. MMR 目标客户与销售场景

### 5.1 目标客户

MMR 第一批客户不应该是所有类型企业，而应该聚焦：

```text
1. 有内部业务系统。
2. 有明确工具接入需求。
3. 需要可审计的 Agent 运行过程。
4. 需要权限、审批和版本治理。
5. 愿意先以试点形式部署。
6. 能接受平台初期只覆盖 1～3 个业务 Agent。
```

推荐客户画像：

```text
中小型企业 AI 团队
内部效率平台团队
企业数字化团队
有私有系统接入需求的 SaaS 团队
需要 AgentOps 治理能力的技术团队
```

### 5.2 MMR 主销售场景

MMR 主打一个场景：

```text
企业内部 AgentOps 平台
```

客户买到的是：

```text
一个可以管理 Agent、接入企业工具、运行任务、查看审计、发布回滚的多智能体运行平台。
```

不要在 MMR 同时主打：

```text
完整群聊协作平台
完整知识库平台
Agent 市场
复杂工作流平台
完整企业计费系统
```

这些是后续扩展。

---

## 6. MMR 用户旅程

### Journey 1：管理员初始化平台

用户角色：

```text
Admin
```

流程：

```text
1. 登录 PloyKit 平台。
2. 创建 Workspace。
3. 邀请成员。
4. 分配角色：viewer / operator / optimizer / admin。
5. 创建 API Key。
6. 查看 CleanCore 连接状态。
7. 配置基础配额。
```

验收：

```text
1. Workspace 与 CleanCore tenant 映射成功。
2. 不同角色权限不同。
3. API Key 只显示一次明文。
4. API Key 可撤销。
5. CleanCore 不可用时 UI degraded mode 生效。
```

---

### Journey 2：Agent 开发者创建并发布 Agent

用户角色：

```text
Optimizer / Admin
```

流程：

```text
1. 创建 Agent。
2. 编辑 AGENTS.md。
3. 添加一个 Skill。
4. 绑定 ToolGroup。
5. 运行 Eval。
6. 发布 Stable。
7. 发起一次 AgentRun。
```

验收：

```text
1. Agent Asset 写入 CleanCore。
2. PromptProfile / Skill / ToolBinding 进入版本管理。
3. Eval 失败时不能 Stable。
4. Publish 写 Audit。
5. 新 Run 命中新版本。
6. 旧 Run 不静默切换版本。
```

---

### Journey 3：企业接入 ToolHost

用户角色：

```text
Developer / Admin
```

流程：

```text
1. 下载 AgentPlugin Service 模板。
2. 实现 /tools/catalog。
3. 实现 /tools/invoke。
4. 部署 ToolHost。
5. 在 PloyKit 中注册 ToolProvider。
6. 同步 ToolManifest。
7. Smoke invoke。
8. 将 ToolGroup 绑定给 Agent。
```

验收：

```text
1. ToolHost health 通过。
2. catalog sync 成功。
3. ToolManifest 写入 CleanCore。
4. ToolCall 可执行。
5. ToolResult 可追踪。
6. ToolHost 不能直接写 Task / Run。
```

---

### Journey 4：业务用户运行 Agent

用户角色：

```text
Operator / API Caller
```

流程：

```text
1. 通过 UI 或 PloyKit API Key 发起 AgentRun。
2. Agent 构建 PromptBundle。
3. Agent 根据 Decision 调用工具。
4. ToolHost 返回结果。
5. Agent 输出回复或 Artifact。
6. 用户查看结果。
```

验收：

```text
1. AgentRun 可完成。
2. ToolCall 有 Trace。
3. ToolResult 写入 CleanCore。
4. ArtifactRef 可查看。
5. PloyKit API Key 派生 scope 生效。
```

---

### Journey 5：审批与治理

用户角色：

```text
Admin / Approver
```

流程：

```text
1. Agent 触发高风险 ToolCall。
2. Task 进入 waiting_approval。
3. 审批人在 Runtime Console 查看详情。
4. 审批通过或拒绝。
5. Agent 继续或失败。
6. Audit 可查询。
```

验收：

```text
1. 高风险工具不能绕过审批。
2. 审批操作写 Audit。
3. 拒绝后不会继续执行工具。
4. 用户可看到任务状态。
```

---

### Journey 6：故障定位与回滚

用户角色：

```text
Admin / Optimizer / Operator
```

流程：

```text
1. AgentRun 失败。
2. 打开 Trace timeline。
3. 查看 Model / Decision / Tool / Policy / Handoff 证据。
4. 定位原因。
5. 修改 Draft。
6. 跑 Eval。
7. Publish 或 Rollback。
```

验收：

```text
1. Run 失败原因可定位。
2. Trace / Audit 证据完整。
3. Rollback 后新 Run 命中回滚版本。
4. 无需手工改库修复。
```

---

## 第一部分：CleanCore MMR Service

本部分是 MMR 的第一阶段，也是 PloyKit 对接前的硬门槛。

目标不是做 UI，而是把 CleanCore 做成一个可被外部产品稳定消费的真实后端服务：

```text
1. MMR 所需能力全部有正式 API。
2. API 契约有 OpenAPI、错误模型、鉴权、tenant scope、trace/correlation。
3. 真实 CleanCore HTTP 服务可启动、可部署、可迁移、可观测。
4. 至少一个真实 ToolHost 可注册、同步、调用、审批、追踪。
5. Agent 创建、发布、运行、审批、回滚、审计、Replay 全链路通过。
6. 所有验收先通过 CleanCore API 完成，不依赖 PloyKit UI。
```

### CleanCore Service Go 条件

```text
1. OpenAPI 覆盖 MMR 所需全部接口。
2. Contract test 覆盖所有冻结接口。
3. HTTP API smoke 覆盖 Agent / ToolHost / Runtime / Governance / Audit。
4. E2E 覆盖创建 Agent、发布、运行、ToolHost 调用、审批、Trace、Replay、Rollback。
5. Tenant isolation、secret redaction、idempotency、provider unhealthy guard 通过。
6. 真实服务启动后无需手工改库即可跑完整 demo。
7. No-Go 条件中与 CleanCore 有关的项全部清零。
```

CleanCore Service Go 通过后，才进入第二部分 PloyKit MMR Product。

---

## 7. CleanCore MMR API 范围

### 7.1 MMR 必须 API 化能力

```text
1. Agent Asset API：
   - list / create / get / patch / delete
   - versions / activate stable version

2. PromptProfile / Skill / ToolBinding API：
   - draft / validate / preview / activate / versions / governance

3. Release / Eval API：
   - publish / canary / stable / rollback
   - eval suite / eval run / eval result

4. Tool Platform API：
   - ToolProvider CRUD / health / sync
   - ToolManifest CRUD / versions / activate
   - ToolGroup CRUD / bind
   - ToolHost catalog sync / smoke invoke

5. Runtime API：
   - task.start / agent.run
   - Task / Run list / detail
   - waiting_input submit
   - waiting_approval approve / reject
   - ToolCall / ToolResult trace

6. Governance API：
   - Policy read / draft / validate / publish
   - Approval request / approve / reject / consume
   - Release approval
   - Tool policy evidence

7. Evidence API：
   - Trace timeline
   - Audit search
   - Replay report
   - Readiness report
   - Go / No-Go report

8. PloyKit Commercial Bridge API：
   - CleanCore runtime usage evidence emit / query baseline
   - PloyKit API Key session 派生 caller scope validation baseline
   - PloyKit entitlement preflight result / policy guard bridge baseline
   - 不在 CleanCore 内自建 API Key、Credits、Entitlement、Usage 权威账本
```

### 7.2 CleanCore API 验收原则

```text
1. 所有 API 都必须 tenant scoped。
2. 所有写操作必须产生 audit 或 trace evidence。
3. 所有外部工具调用必须可追踪 ToolCall / ToolResult。
4. 所有高风险工具必须进入 approval gate。
5. 所有 side-effect tool invoke 必须具备 idempotency key。
6. 所有 secret / auth_ref 只记录引用，不进入 PromptBundle / ToolCard / Trace / Audit 明文。
7. API error code 必须稳定，PloyKit 只能消费错误模型，不解析内部日志。
8. Read model 可以给 UI 用，但不能替代 Task / Run / ToolResult / Trace / Audit 权威事实。
```

---

## 8. CleanCore MMR 稳定计划

### MMR-CC-1：OpenAPI Freeze

任务：

```text
1. 冻结 Agent API。
2. 冻结 PromptProfile API。
3. 冻结 Skill API。
4. 冻结 ToolBinding API。
5. 冻结 Release / Eval API。
6. 冻结 ToolProvider / ToolManifest API。
7. 冻结 Task / Run API。
8. 冻结 Trace / Audit / Replay API。
9. 冻结 Usage Event API。
10. 生成 TS SDK。
```

验收：

```text
1. 外部产品 / PloyKit 可稳定消费。
2. OpenAPI 变更有版本。
3. Contract test 通过。
```

---

### MMR-CC-2：Runtime Readiness

任务：

```text
1. Task / Run 状态稳定。
2. waiting_input / waiting_approval 稳定。
3. ToolCall idempotency。
4. ToolResult 写入。
5. ArtifactRef 写入。
6. Handoff 基础链路保留但不作为 MMR 主卖点。
7. Trace / Audit 完整。
8. Replay lite。
```

验收：

```text
1. 运行一次 Agent 可完整追踪。
2. 重复请求不重复执行副作用工具。
3. 审批状态可恢复。
```

---

### MMR-CC-3：ToolHost 生产基线

任务：

```text
1. ToolHost catalog contract。
2. ToolHost invoke contract。
3. ToolHost health contract。
4. auth_ref / secret resolver 基础版。
5. timeout / retry。
6. provider health。
7. ToolHost smoke tests。
8. Provider unhealthy guard。
```

验收：

```text
1. ToolHost 接入可稳定运行。
2. unhealthy provider 下工具执行被拒绝。
3. ToolHost 不能绕过 Policy。
```

---

### MMR-CC-4：Policy / Approval / Commercial Bridge

任务：

```text
1. ToolPolicy。
2. ApprovalPolicy。
3. exposed tools policy。
4. PloyKit API Key session 派生 caller scope claims。
5. Entitlement preflight result / policy guard bridge。
6. Runtime usage evidence。
7. Audit required actions。
```

验收：

```text
1. high risk tool 进入 waiting_approval。
2. private tool 外部不可调。
3. PloyKit entitlement guard 结果可阻止未授权能力。
4. usage evidence 可被 PloyKit metering / Usage View 消费。
5. CleanCore 不保存 API key hash，不自建 Credits / Entitlement / Usage 权威表。
```

---

## 9. ToolHost Service MMR 交付计划

### MMR-PS-1：ToolHost 模板

任务：

```text
1. GET /health。
2. GET /tools/catalog。
3. POST /tools/invoke。
4. idempotency key。
5. trace_id logging。
6. error schema。
7. sample CRM tool。
8. sample document tool。
9. local audit log。
```

验收：

```text
1. 模板可启动。
2. 可被 CleanCore catalog sync。
3. 可完成 smoke invoke。
```

---

### MMR-PS-2：安全基线

任务：

```text
1. service token。
2. request signature。
3. secret ref。
4. local env secret loading。
5. output redaction helper。
6. deny direct platform state write。
7. mTLS 预留。
```

验收：

```text
1. 未授权请求被拒绝。
2. ToolHost 不返回 secret。
3. ToolHost 不直接写 CleanCore 状态。
```

---

### MMR-PS-3：开发者文档

任务：

```text
1. ToolHost Quickstart。
2. Catalog schema 文档。
3. Invoke schema 文档。
4. Error code 文档。
5. Deployment guide。
6. Troubleshooting。
7. Smoke test guide。
```

验收：

```text
1. 新开发者可在 30～60 分钟内跑起示例 ToolHost。
2. 可通过 CleanCore API 注册并完成 smoke invoke。
3. 进入第二部分后，可由 PloyKit UI 承载同一注册流程。
```

---

## 第二部分：PloyKit MMR Product

本部分只在 CleanCore Service Go 通过后进入主线。

PloyKit 的职责是把已经可用的 CleanCore 服务产品化，而不是补 CleanCore 的 API 缺口。

```text
1. PloyKit 不作为 CleanCore 能力未完成时的绕路实现。
2. PloyKit 不直接写 CleanCore DB。
3. PloyKit 不保存 CleanCore Task / Run / ToolResult / Trace / Audit 权威事实副本。
4. PloyKit 通过 module.ts contract、ctx.services.invoke、service connection 消费 CleanCore。
```

---

## 10. PloyKit MMR 产品模块

### 10.1 PloyKit MMR 模块形态

MMR 不新增 Origin 专属 Shell，也不把每个菜单拆成宿主级模块。

PloyKit 侧采用“一个完整产品模块优先”的形态：

```text
1. origin-agentops
   - 类型：PloyKit product-app module。
   - 契约入口：module.ts。
   - 声明 dashboard routes：Agent Studio、Tool Console、Runtime Console、Usage View、ToolHost setup wizard。
   - 声明 admin routes：CleanCore health、governance evidence、release readiness、service connection health。
   - 声明 navigation / surfaces / i18n / product.pages。
   - 声明 CleanCore signed-http serviceRequirements。
   - 声明必要 resourceBindings / actions / routes.api。
   - 通过 ctx.services.invoke 调用 CleanCore 受控服务。
```

MMR 阶段不默认新增以下独立模块：

```text
1. origin-toolhost-connector
   - MMR 中作为 origin-agentops 内部功能组。
   - 承载 ToolHost setup wizard、health、catalog sync、smoke invoke 的 route / action / API。
   - 不直接保存 ToolHost secret 明文。
   - 只有当它要服务多个产品、具备独立 module.ts contract、独立 product.pages、navigation、quality evidence 时，才后置拆成 connector / signed-service module。

2. origin-runtime-extension-lite
   - MMR 中最多作为 origin-agentops 内部 Runtime Hook route / surface。
   - 必须仍通过 CleanCore RuntimeHook API。
   - 不因为“Runtime Hook 有独立菜单”而拆模块。

3. origin-collab-array-lite
   - MMR 中最多作为 origin-agentops 内部协作视图。
   - 不替代 CleanCore Task / Handoff / Trace 权威事实。
   - 不因为“Collaboration 有独立菜单”而拆模块。

4. origin-knowledge-lite
   - MMR 中最多作为 origin-agentops 内部 Knowledge route / surface。
   - 不自建与 CleanCore 冲突的知识库事实源。
   - 不因为“Knowledge 有独立菜单”而拆模块。
```

MMR 后置模块：

```text
1. origin-marketplace
2. origin-advanced-runtime-console
3. origin-crossgroup-console
4. origin-multi-provider-agent-console
5. origin-billing-advanced
```

后置模块成立条件：

```text
1. 有可独立安装、独立运营、独立验收的产品边界。
2. 有独立 module.ts contract、product.pages、routes、navigation、surfaces、quality evidence。
3. 不要求 PloyKit host/shared 写 Origin module id、路径、菜单或 package script 特例。
4. 不复制 PloyKit Workspace / API Key / Billing / Usage / Entitlement 权威事实。
5. 不复制 CleanCore Task / Run / ToolResult / Trace / Audit 权威事实。
```

### 10.2 PloyKit 对齐原则

```text
1. Origin 产品代码放在配置的 PloyKit module root 中。
2. Host / shared runtime 不 import Origin 具体模块。
3. Host / shared runtime 不写 moduleId == "origin-agentops" 这类特例。
4. Origin module.ts 是唯一能力声明入口。
5. Origin module 使用 routes.dashboard / routes.admin / routes.api / navigation / surfaces 表达产品形态。
6. CleanCore 调用走 ctx.services.invoke，不裸 fetch，不在模块内读取 process.env service token。
7. PloyKit Workspace / API Key / Billing / Usage / Entitlement 不被 Origin 模块重做。
8. Origin 模块只保存必要 read model / UI state / draft view，不保存 CleanCore 权威事实副本。
9. 新增模块必须由独立产品边界驱动，不能由菜单、页面或功能分组驱动。
10. MMR 默认只交付 origin-agentops 一个产品模块；ToolHost / Runtime Hook / Knowledge / Collaboration 是该模块内的 route / surface / action。
```

---

## 11. PloyKit MMR 实施计划

### MMR-PK-1：Origin AgentOps Product Module

任务：

```text
1. 使用 PloyKit product-app 模板创建 origin-agentops module。
2. 定义 module.ts：product、routes.dashboard、routes.admin、routes.api、navigation、surfaces、i18n。
3. Dashboard route 承载 Agent Studio / Tool Console / Runtime Console / Usage View / ToolHost setup wizard。
4. Admin route 承载 CleanCore health / governance evidence / release readiness / service connection health。
5. 复用 PloyKit workspace switcher，不自建 Workspace。
6. 复用 PloyKit RBAC / API Key / Entitlement，不自建权限和密钥账本。
7. 接入 CleanCore health/status 并实现 degraded mode。
8. 注册 Origin 产品导航，不硬编码 host 菜单。
9. 接入 i18n。
10. 不新增 origin-toolhost-connector、origin-runtime-extension-lite 等 MMR 独立模块。
11. 运行 modules:scan、module:doctor、module:test、host boundary check。
```

验收：

```text
1. 登录后可从 PloyKit Dashboard 进入 Origin AgentOps。
2. 工作区切换生效。
3. CleanCore 断开时 UI 不崩溃。
4. 菜单按角色显示。
5. host/shared runtime 无 Origin module id 特例。
6. module.ts contract、navigation、routes、surfaces 通过 PloyKit doctor。
7. ToolHost / Runtime Hook / Knowledge / Collaboration 入口作为 origin-agentops 内部 route / surface 出现，不作为 MMR 独立模块出现。
```

---

### MMR-PK-2：CleanCore Service Connection 与 Client Facade

任务：

```text
1. 根据 OpenAPI 生成 clean-core-client-ts，仅作为 typed facade 的内部实现。
2. 在 origin-agentops module.ts 声明 CleanCore serviceRequirements。
3. 通过 PloyKit service connection 保存 baseUrl、secretRefs、health。
4. 模块 action / API 通过 ctx.services.invoke 调 CleanCore。
5. 注入 PloyKit session 派生的 tenant / workspace / actor / role / API key scope / entitlement preflight claims。
6. 注入 trace_id / request_id / correlation_id。
7. 映射 CleanCore error 为 UI 可理解错误。
8. 加 OpenAPI contract tests 和 service invocation tests。
```

验收：

```text
1. PloyKit 模块不裸 fetch CleanCore。
2. CleanCore service token 不进入模块代码、Data v2、日志、artifact、trace。
3. 所有请求有身份、tenant、trace 和 correlation。
4. service connection unhealthy 时 Origin UI 进入 degraded mode。
5. schema 变更会触发测试失败。
6. CleanCore 只消费 PloyKit 派生 claims，不自建 API Key、Credits、Entitlement、Usage 权威事实。
```

---

### MMR-PK-3：Agent Studio Lite

任务：

```text
1. Agent list / create / detail。
2. PromptProfile editor。
3. Skill editor。
4. ToolBinding editor。
5. Eval run button。
6. Publish / Rollback。
7. Version history。
8. Governance summary。
```

验收：

```text
1. 可创建 Agent。
2. 可编辑 Prompt / Skill / ToolBinding。
3. 可运行 Eval。
4. 可发布 / 回滚。
5. 所有写操作通过 CleanCore API。
```

---

### MMR-PK-4：Tool Console Lite

任务：

```text
1. ToolProvider list / create。
2. Static ToolHost registration。
3. Provider health。
4. Catalog sync。
5. ToolManifest list / detail。
6. ToolGroup create。
7. ToolCard preview。
8. Smoke invoke。
```

验收：

```text
1. 可注册一个 ToolHost。
2. 可同步 ToolManifest。
3. 可创建 ToolGroup。
4. 可绑定 Agent。
5. Smoke invoke 成功。
```

---

### MMR-PK-5：Runtime Console Lite

任务：

```text
1. Task list / detail。
2. Run list / detail。
3. Timeline。
4. ToolCall / ToolResult。
5. ArtifactRef。
6. waiting_input 操作。
7. waiting_approval 操作。
8. Trace viewer。
9. Audit search。
10. Replay report lite。
```

验收：

```text
1. 可查看完整 Run。
2. 可审批高风险 ToolCall。
3. 可定位失败原因。
4. 可查询 Audit。
```

---

### MMR-PK-6：ToolHost Connector Lite

任务：

```text
1. ToolHost Service / AgentPlugin Service 模板下载。
2. ToolHost setup wizard。
3. 通过 PloyKit service connection / resource binding 配置 CleanCore 可见的 provider 信息。
4. auth_ref / secretRefs 配置，不保存明文 secret。
5. health check。
6. catalog sync。
7. smoke invoke。
8. troubleshooting guide。
9. MMR 中作为 origin-agentops 内部功能，不拆成 origin-toolhost-connector module。
10. 后置拆分前必须证明它有独立产品边界、独立 module.ts contract、独立 product.pages / navigation / quality evidence。
```

验收：

```text
1. 企业开发者能按向导接入 ToolHost。
2. 接入失败有明确错误。
3. 成功后可绑定 Agent。
4. ToolHost secret 不进入 PloyKit 模块 Data v2 明文字段。
5. ToolHost 仍不能绕过 CleanCore ToolRuntime / Policy / Approval。
```

---

### MMR-PK-7：Origin Admin / Usage View

任务：

```text
1. 复用 PloyKit Workspace settings，不自建 Workspace。
2. 复用 PloyKit Member / role 管理，不自建成员账本。
3. 复用 PloyKit API Key 管理，不保存 API key hash。
4. Origin AgentOps usage summary。
5. AgentRun / ToolCall / Eval / Trace 查询维度的用量视图。
6. Quota / limit 基础配置映射到 PloyKit entitlement / metering 或 CleanCore policy。
7. Entitlement 基础开关通过 PloyKit capability guard 生效。
```

验收：

```text
1. 不同角色权限不同。
2. API Key 可创建 / 撤销。
3. usage 可按 workspace 查看。
4. entitlement 可阻止未授权能力。
5. Origin 模块没有自建第二套 API Key / Credits / Entitlement / Usage 权威表。
6. CleanCore Task / Run / ToolResult / Trace / Audit 仍以 CleanCore 为事实源。
```

---

## 12. MMR 示例资产

为了能卖，MMR 必须带示例资产。

### 12.1 Demo Workspace

包含：

```text
1. 一个默认 Workspace。
2. 三个示例成员角色。
3. 一个 API Key 示例。
4. 一个 Usage dashboard 示例。
5. 一个 AgentRun 示例。
```

### 12.2 示例 Agent

建议提供三个：

```text
1. Internal Knowledge Assistant
2. CRM Assistant
3. Invoice Review Agent
```

每个 Agent 包含：

```text
AGENTS.md
至少 1 个 Skill
ToolBinding
EvalCase
Release version
Demo input
Demo trace
```

### 12.3 示例 ToolHost

建议提供三个模板：

```text
1. CRM ToolHost
2. Document ToolHost
3. Approval / Ticket ToolHost
```

### 12.4 Quickstart 文档

必须提供：

```text
Admin Quickstart
Agent Builder Quickstart
ToolHost Developer Quickstart
Operator Quickstart
Security / Deployment Guide
Pilot Acceptance Checklist
```

---

## 13. MMR E2E 测试

必须固定两组 E2E。

第一组是 CleanCore Service E2E，必须先通过：

```text
CC-E2E-01 CleanCore service 启动、ready、migration readiness 通过。
CC-E2E-02 通过 CleanCore API 创建 Agent。
CC-E2E-03 通过 CleanCore API 编辑 Prompt / Skill / ToolBinding。
CC-E2E-04 Eval 通过后 Publish / Stable。
CC-E2E-05 注册真实 ToolHost provider。
CC-E2E-06 catalog sync 生成 ToolManifest。
CC-E2E-07 ToolGroup 绑定到 Agent。
CC-E2E-08 通过 CleanCore API 发起 AgentRun。
CC-E2E-09 Agent 调用 ToolHost。
CC-E2E-10 高风险 ToolCall 进入 waiting_approval。
CC-E2E-11 通过 Approval API 审批后继续运行。
CC-E2E-12 Trace / Audit / Replay 可解释完整链路。
CC-E2E-13 AgentPackage Rollback。
CC-E2E-14 PloyKit 派生 API caller scope / service caller scope 生效。
CC-E2E-15 CleanCore runtime usage evidence 可查询，并可被 PloyKit metering 消费。
CC-E2E-16 ToolHost unhealthy 时执行被拒绝。
CC-E2E-17 Tenant isolation 通过。
CC-E2E-18 Secret / auth_ref 不进入 Trace / Audit 明文。
```

第二组是 PloyKit Integration E2E，在 CleanCore Service Go 后执行：

```text
PK-E2E-01 Admin 创建 Workspace、成员和 API Key。
PK-E2E-02 PloyKit service connection 连接 CleanCore。
PK-E2E-03 Agent Builder 通过 UI 创建 Agent。
PK-E2E-04 通过 UI 编辑 Prompt / Skill / ToolBinding。
PK-E2E-05 Eval 通过后 Publish。
PK-E2E-06 通过 UI 注册 ToolHost 并 catalog sync。
PK-E2E-07 Operator 通过 UI 或 PloyKit API Key 发起 AgentRun。
PK-E2E-08 Admin 通过 Runtime Console 审批 high risk ToolCall。
PK-E2E-09 Runtime Console 展示 Trace / Audit / Replay。
PK-E2E-10 CleanCore unavailable 时 PloyKit degraded mode。
PK-E2E-11 Usage View 可展示基础用量。
```

---

## 14. MMR Go / No-Go

### 14.1 Go 条件

CleanCore Service Go：

```text
1. MMR 所需 CleanCore API 全部冻结并有 OpenAPI。
2. Contract tests 通过。
3. CC-E2E-01 到 CC-E2E-18 全通。
4. 至少一个真实 ToolHost 接入成功。
5. 至少一个真实 Agent 完成发布、运行、审批、回滚。
6. Trace / Audit / Replay 可解释完整运行。
7. Tenant isolation 通过。
8. Secret ref / auth_ref 基线通过。
9. ToolHost unhealthy guard、idempotency、approval gate 通过。
10. 新 CleanCore 环境无需手工改库即可跑 demo。
```

PloyKit Product Go：

```text
1. CleanCore Service Go 已通过。
2. PK-E2E-01 到 PK-E2E-11 全通。
3. 六条 MMR 用户旅程全通。
4. API Key 可通过 PloyKit module API 调用 Agent，且 PloyKit route auth / runtime session 合约已验证。
5. Usage 基础视图可用，且事实源来自 PloyKit metering / CleanCore runtime evidence bridge。
6. Workspace / tenant 映射通过。
7. PloyKit degraded mode 通过。
8. 无 P0 bug。
9. P1 bug 有明确 workaround。
10. 新环境可在 1 小时内按文档部署起来。
11. 试点客户可以按 Pilot Acceptance Checklist 完成验收。
```

### 14.2 No-Go 条件

```text
1. CleanCore MMR 能力没有正式 API，只能靠内部函数或手工改库完成。
2. CleanCore 真实 HTTP 服务未通过 CC-E2E。
3. PloyKit 可绕过 CleanCore 事实源。
4. ToolHost 可绕过 Policy。
5. Trace / Audit 断链。
6. AgentPackage 发布 / 回滚不可靠。
7. Tenant 隔离失败。
8. PloyKit 派生 API caller scope 不生效，或 CleanCore 试图自建 API Key 事实源。
9. 工具调用失败无法定位。
10. 需要开发人员手工改库才能跑 Demo。
11. Secret 明文进入 Trace / Audit。
12. high risk tool 可绕过 approval。
```

---

## 15. P3 Post-MMR 路线

MMR 完成后再进入完整 P3 深水区。

### Post-MMR 1：Runtime Extension

```text
1. Event Streaming 增强。
2. Checkpoint / Resume 完整产品化。
3. Strategy Hook。
4. RuntimeDriver Intent Gateway。
5. Driver marketplace 实验。
6. tree / parallel planning 实验。
```

### Post-MMR 2：Collaboration / Knowledge

```text
1. Array Adapter 完整化。
2. Slack / Feishu / WeChat Work Adapter。
3. KnowledgeBase Pack。
4. CrossGroup Search。
5. Desensitization Policy。
6. Collaboration eval suite。
```

### Post-MMR 3：Commercial Advanced

```text
1. 复杂套餐。
2. 细粒度 Entitlement。
3. 订阅 / 订单。
4. 成本预测。
5. Provider 成本治理。
6. 企业账单导出。
```

### Post-MMR 4：Enterprise Advanced

```text
1. SSO / SAML。
2. SCIM。
3. BYOK。
4. Private deployment。
5. High availability。
6. Multi-region。
7. Compliance pack。
```

---

## 16. 推荐开发顺序

### MMR 顺序

```text
1. P3-0 集成边界冻结。
2. MMR-CC-1 OpenAPI Freeze。
3. MMR-CC-2 Runtime Readiness。
4. MMR-CC-3 ToolHost 生产基线。
5. MMR-CC-4 Policy / Approval / Commercial Bridge。
6. MMR-PS-1 ToolHost 模板。
7. CleanCore 示例资产、Quickstart、CC-E2E。
8. CleanCore Service Go / No-Go。
9. MMR-PK-1 Origin AgentOps Product Module。
10. MMR-PK-2 CleanCore Service Connection 与 Client Facade。
11. MMR-PK-3 / 4 / 5 Agent Studio、Tool Console、Runtime Console。
12. MMR-PK-6 / 7 ToolHost Connector、Origin Admin / Usage View。
13. PloyKit + CleanCore Integration E2E。
14. PloyKit Product Go / No-Go。
```

### 为什么这样排

```text
先让 CleanCore 独立服务真实可用。
再把 MMR 所需 CleanCore 能力全面 API 化。
再用真实 HTTP API 跑 Agent、ToolHost、Runtime、Governance、Trace、Replay 全链路。
CleanCore Service Go 之后，才进入 PloyKit 产品模块对接。
PloyKit 只做产品宿主、UI、权限、service connection、degraded mode 和商业化视图。
最后做 PloyKit + CleanCore 联调、试点验收和 MMR Go。
```

---

## 17. 最终判断

P3 全景计划的方向是对的，但如果目标是 MMR，必须收敛。

MMR 不等于：

```text
把 P3 全部做完。
```

MMR 等于：

```text
完成一条可以销售、可以部署、可以试点验收的企业 AgentOps 闭环。
```

这条闭环是：

```text
Part A：CleanCore Service
  ↓
API 创建 Agent
  ↓
API 编辑 Prompt / Skill / ToolBinding
  ↓
API 注册 AgentPlugin Service / ToolHost
  ↓
API 发布 Agent
  ↓
API 发起 AgentRun
  ↓
调用企业工具
  ↓
API 审批高风险动作
  ↓
查看 Trace / Audit / Replay
  ↓
回滚版本
  ↓
CleanCore Service Go
  ↓
Part B：PloyKit Product
  ↓
PloyKit 登录 / Workspace / API Key
  ↓
Agent Studio / Tool Console / Runtime Console
  ↓
Usage 视图 / degraded mode / 试点验收
```

这条闭环打通后，才可以说：

```text
Origin Agent Platform 进入 MMR。
```
