# 原智能体 CleanCore 群聊智能体扩展能力开发设计 v0.1

日期：2026-05-31

## 1. 背景

当前 CleanCore 已经具备原智能体协调智能体的第一版群聊上下文闭环：

```text
群聊消息结构化
-> Addressee Judge 判断是否在叫我
-> Context Sufficiency Judge 判断上下文是否足够
-> 必要时主动召回旧上下文
-> RetrievedContext 注入 PromptBundle
-> Route Guard 决定回复或 no_op
-> Trace / Eval / 真实模型回归
```

但真实群聊智能体不止是“能不能接话”。它还需要知道群里的人、权限、任务进度、专业智能体、知识库、多群记忆和跨群信息边界。

本设计文档的目标不是把十三项能力全部塞进 Coordinator，而是把它们收敛成可扩展、可治理、可测试的 CleanCore 能力面。

核心原则：

```text
Coordinator 保持轻：只做判断、路由、上下文编排。
能力做成服务：身份、权限、记忆、任务、知识库、Agent Factory 独立演进。
模型只做语义判断：不直接越权读库、不直接改配置、不直接跨群查询。
所有高风险动作：必须经过权限、策略、审批、审计和 eval。
```

## 2. 用户提出的十三项能力

```text
1. 群聊能知道群里的人。
2. 能接住话。
3. 能知道是不是叫它。
4. 能通过群里不同人不同权限判断是否能更新自己的 skills。
5. 能决定回复或者不回复。
6. 能控制回复语气。
7. 能拆分任务调用新的专业智能体。
8. 能创建新的专业智能体。
9. 群聊表现足够自然，像一个真实协作成员。
10. 拆分给专业智能体后，后续还能查询专业智能体进度。
11. 能创建外部知识库作为工具类。
12. 能在多个群中做不同任务，但有一部分共同记忆。
13. A 群 agent 能通过受控工具查询 B 群部分信息。
```

## 3. 是否会臃肿

如果直接做成十三套 Coordinator 内部逻辑，会臃肿。

正确方式是收敛成五个扩展面：

```text
1. Identity & Permission：身份、群成员、角色、权限、审批。
2. Conversation & Memory：群聊上下文、长期历史、记忆作用域、共享记忆。
3. Task & Agent Orchestration：任务拆分、专业智能体调用、进度查询、Agent Factory。
4. Knowledge & Tooling：外部知识库、检索工具、跨群查询工具、工具权限。
5. Personality & Eval：回复语气、群聊节奏、自然度测试、误抢话/漏接评估。
```

Coordinator 只依赖这些能力的接口，不拥有它们的业务实现。

## 4. 当前 CleanCore 支持度

| 能力 | 当前状态 | 需要扩展 |
|---|---|---|
| 群里谁说话 | 部分支持 | 群成员画像、别名、角色 |
| 是不是叫我 | 已支持第一版 | 长期话题线程、长期历史 |
| 接住话 | 部分支持 | 群聊长期消息库、话题状态 |
| 回复/不回复 | 已支持第一版 | 更细粒度置信策略 |
| 回复语气 | 主要靠提示词 | Tone Policy |
| 不同人权限 | 有 Policy/Audit 底座 | Group RBAC / ABAC |
| 更新 skills | 有 AgentPackage 底座 | SkillDraft / Approval |
| 调用专业智能体 | 有 handoff/tool 底座 | 能力匹配和进度绑定 |
| 创建专业智能体 | 有 package draft/publish 底座 | Agent Factory 工作流 |
| 查询进度 | 有 task/run/handoff 状态 | 群聊自然语言进度查询 |
| 外部知识库工具 | 有 tool/memory/artifact 底座 | KnowledgeBase / RetrieverTool |
| 多群共享记忆 | 有 memory 底座 | MemoryScope / ShareGrant |
| A 群查 B 群 | 无默认能力 | CrossGroupSearchTool + 权限审计 |

结论：

```text
CleanCore 不需要推倒重来。
现有内核是合适底座。
完整群聊智能体需要在内核之外补扩展服务和受控工具。
```

## 5. 架构分层

### 5.1 内核层必须保持的职责

CleanCore Kernel / Coordinator 负责：

```text
1. 接收 AgentEnvelope。
2. 构建 WorkView。
3. 构建 ConversationContext。
4. 判断是否接话、是否需要召回上下文。
5. 构建 PromptBundle。
6. 调用主模型。
7. 校验 Decision。
8. 执行 reply / no_op / ask / tool_call / handoff / unsupported。
9. 记录 trace、audit、eval 结果。
```

不应放进 Coordinator 的东西：

```text
1. 群成员数据库细节。
2. 具体业务权限规则。
3. 外部知识库索引实现。
4. Agent Factory 创建逻辑。
5. 跨群信息查询策略细节。
6. 人设和口吻大段规则。
```

### 5.2 扩展服务层

建议新增或强化以下服务：

```text
IdentityService
PermissionService
ConversationHistoryStore
MemoryService
TaskProgressService
AgentFactoryService
KnowledgeBaseService
CrossGroupContextService
TonePolicyService
EvalScenarioService
```

### 5.3 工具层

高风险能力做成受控工具：

```text
origin.identity.resolve_member
origin.permission.check
origin.skill.propose_update
origin.agent.delegate
origin.agent.create_draft
origin.agent.progress_query
origin.knowledge.search
origin.knowledge.create
origin.cross_group.search
origin.memory.share
```

工具调用必须经过 ToolPolicy、权限检查、审计和 trace。

### 5.4 AgentPackage 层

用户可改的提示词和策略放在 AgentPackage：

```text
system.md
developer.md
prompt.md
agents.md
eval/*.yaml
tone/*.md 或 metadata.tone_policy
skills/*.md 或 metadata.skills
```

AgentPackage 不直接拥有数据库权限，只声明行为边界和期望输出。

## 6. 核心数据模型设计

### 6.1 GroupMemberProfile

```go
type GroupMemberProfile struct {
    TenantID TenantID `json:"tenant_id"`
    GroupID string `json:"group_id"`
    MemberID string `json:"member_id"`
    ExternalUserID string `json:"external_user_id,omitempty"`
    DisplayName string `json:"display_name,omitempty"`
    Aliases []string `json:"aliases,omitempty"`
    MemberType string `json:"member_type"` // human, agent, bot, system
    Roles []string `json:"roles,omitempty"`
    PermissionRefs []string `json:"permission_refs,omitempty"`
    Status string `json:"status,omitempty"` // active, muted, left
    LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}
```

用途：

```text
1. 判断谁说话。
2. 判断谁有权让 agent 更新 skill。
3. 判断这个人是普通成员、管理员、业务负责人、外部客户。
4. 支撑回复语气个性化。
```

### 6.2 GroupPermissionPolicy

```go
type GroupPermissionPolicy struct {
    TenantID TenantID `json:"tenant_id"`
    GroupID string `json:"group_id"`
    SubjectID string `json:"subject_id"`
    SubjectType string `json:"subject_type"` // user, role, agent
    Actions []string `json:"actions"`
    ResourceScopes []string `json:"resource_scopes"`
    RequiresApproval bool `json:"requires_approval"`
}
```

典型 action：

```text
agent.skill.propose_update
agent.skill.publish
agent.package.create
agent.package.publish
agent.delegate
knowledge.create
knowledge.search
memory.read
memory.write
memory.share
cross_group.search
```

### 6.3 SkillUpdateRequest

```go
type SkillUpdateRequest struct {
    RequestID string `json:"request_id"`
    TenantID TenantID `json:"tenant_id"`
    AgentID AgentID `json:"agent_id"`
    GroupID string `json:"group_id"`
    RequestedBy string `json:"requested_by"`
    Objective string `json:"objective"`
    TargetSkillID string `json:"target_skill_id,omitempty"`
    ProposedPatch map[string]any `json:"proposed_patch,omitempty"`
    Status string `json:"status"` // draft, waiting_approval, approved, published, rejected
    ApprovalTaskID TaskID `json:"approval_task_id,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}
```

原则：

```text
模型不能直接改 skill。
模型只能提出 SkillUpdateRequest。
runtime 负责权限、审批、发布、回滚。
```

### 6.4 ConversationTopicThread

```go
type ConversationTopicThread struct {
    TenantID TenantID `json:"tenant_id"`
    GroupID string `json:"group_id"`
    ThreadID string `json:"thread_id"`
    TopicID string `json:"topic_id"`
    Summary string `json:"summary"`
    Participants []string `json:"participants"`
    RelatedTaskIDs []TaskID `json:"related_task_ids,omitempty"`
    RelatedAgentIDs []AgentID `json:"related_agent_ids,omitempty"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

用途：

```text
1. 判断“第二个问题”指什么。
2. 判断用户在延续哪个话题。
3. 后续查询专业智能体进度时找到对应 task。
```

### 6.5 MemoryScope

```go
type MemoryScope struct {
    TenantID TenantID `json:"tenant_id"`
    MemoryID MemoryID `json:"memory_id"`
    ScopeType string `json:"scope_type"` // global, tenant, group, user, task, shared
    ScopeID string `json:"scope_id"`
    Visibility string `json:"visibility"` // private, group, shared_groups, tenant
    SharedWithGroupIDs []string `json:"shared_with_group_ids,omitempty"`
    ReadRoles []string `json:"read_roles,omitempty"`
    WriteRoles []string `json:"write_roles,omitempty"`
}
```

### 6.6 KnowledgeBase

```go
type KnowledgeBase struct {
    KnowledgeBaseID string `json:"knowledge_base_id"`
    TenantID TenantID `json:"tenant_id"`
    Name string `json:"name"`
    OwnerGroupID string `json:"owner_group_id,omitempty"`
    Visibility string `json:"visibility"` // private, group, shared, tenant
    SourceType string `json:"source_type"` // files, url, api, db, manual
    IndexType string `json:"index_type"` // bm25, embedding, hybrid
    Status string `json:"status"` // creating, ready, failed, archived
}
```

### 6.7 GroupTaskBinding

```go
type GroupTaskBinding struct {
    TenantID TenantID `json:"tenant_id"`
    GroupID string `json:"group_id"`
    MessageID string `json:"message_id"`
    TaskID TaskID `json:"task_id"`
    RunID AgentRunID `json:"run_id,omitempty"`
    HandoffID HandoffID `json:"handoff_id,omitempty"`
    AgentID AgentID `json:"agent_id"`
    Objective string `json:"objective"`
    CreatedBy string `json:"created_by"`
    CreatedAt time.Time `json:"created_at"`
}
```

用途：

```text
用户问“刚才那个任务怎么样了？”
-> 找最近 GroupTaskBinding
-> 查 task/run/handoff 状态
-> 生成进度回复
```

## 7. 五个扩展面详细设计

## 7.1 Identity & Permission

### 目标

让 CleanCore 知道群里的人是谁、有什么角色、是否能发起高风险动作。

### 当前已有

```text
AgentEnvelope.Caller
RuntimeContext.Collaboration.CurrentSpeakerName
RuntimeContext.Collaboration.Participants
Policy / Audit 基础
```

### 需要扩展

```text
1. GroupMemberProfile 存储。
2. IdentityService.ResolveMember。
3. PermissionService.Check。
4. 群成员同步 Adapter。
5. 高风险动作审批策略。
```

### 接口建议

```go
type IdentityService interface {
    ResolveMember(ctx context.Context, tenantID TenantID, groupID string, externalUserID string) (GroupMemberProfile, bool, error)
    ListGroupMembers(ctx context.Context, tenantID TenantID, groupID string) ([]GroupMemberProfile, error)
}

type PermissionService interface {
    Check(ctx context.Context, input PermissionCheckInput) (PermissionDecision, error)
}
```

### 验收

```text
1. 普通成员不能发布 skill。
2. 管理员可以发起 skill 修改草稿。
3. 高风险发布需要审批。
4. 所有拒绝都有 reason_code。
5. trace 能看到 permission.check 结果。
```

## 7.2 Conversation & Memory

### 目标

让智能体在多群长期运行时，既能记住该记的，又不会串群泄露。

### 当前已有

```text
ConversationContext
RecentMessages
ContextRetriever
MemorySummary
TaskEvent
RetrievedContext
```

### 需要扩展

```text
1. 长期群聊消息库 ConversationHistoryStore。
2. TopicThread 跟踪。
3. MemoryScope。
4. SharedMemoryGrant。
5. History Retriever Adapter。
```

### 召回链路

```text
当前消息
-> 判断上下文不足
-> 生成 retrieval query
-> ConversationHistoryStore 检索本群历史
-> MemoryService 检索当前 scope memory
-> SharedMemoryGrant 判断共享记忆
-> RetrievedContext 注入 PromptBundle
```

### 验收

```text
1. A 群普通历史不会进入 B 群。
2. 被授权共享的记忆可跨群召回。
3. “第二个问题”能查到老话题。
4. 找不到上下文时追问，不编造。
5. 历史 prompt injection 不能变成系统指令。
```

## 7.3 Task & Agent Orchestration

### 目标

让原智能体能拆任务、调用专业智能体、创建新专业智能体、查询进度。

### 当前已有

```text
Task
Run
Handoff
ContextPackage
origin.agent.delegate
AgentPackage draft/publish
```

### 需要扩展

```text
1. AgentCapabilityIndex：专业智能体能力索引。
2. TaskDecomposer：任务拆分策略。
3. GroupTaskBinding：群消息和任务绑定。
4. TaskProgressService：自然语言进度查询。
5. AgentFactoryService：创建专业智能体。
```

### 任务拆分链路

```text
用户提出复杂任务
-> 主协调模型判断需要拆分
-> TaskDecomposer 生成子任务
-> PermissionService 检查是否允许 delegate
-> origin.agent.delegate 调用专业智能体
-> 保存 GroupTaskBinding
-> 后续用户查询进度时读取 task/run/handoff
```

### 创建专业智能体链路

```text
用户提出新专业能力需求
-> PermissionService 检查 agent.package.create
-> AgentFactoryService 生成 AgentPackage 草稿
-> 自动生成 system/developer/prompt/skills/tools/eval
-> package_validate
-> 真实模型 eval
-> 等待审批
-> publish
-> 写入 AgentCapabilityIndex
```

### 验收

```text
1. 已有专业智能体时优先调用，不重复创建。
2. 没有合适专业智能体时生成草稿，不直接上线。
3. 用户可问“刚才那个任务到哪了”。
4. 专业智能体失败时原智能体能说明失败原因。
5. handoff 上下文不越权。
```

## 7.4 Knowledge & Tooling

### 目标

支持创建外部知识库作为工具，并支持受控跨群查询。

### 当前已有

```text
ToolRegistry
ToolRuntime
ExternalTools
Artifact
Memory
ToolPolicy
Audit
```

### 需要扩展

```text
1. KnowledgeBaseService。
2. KnowledgeIndexer。
3. RetrieverTool。
4. CrossGroupContextService。
5. DataClassification / Redaction。
```

### 工具建议

```text
origin.knowledge.create
origin.knowledge.ingest
origin.knowledge.search
origin.cross_group.search
```

### 跨群查询流程

```text
A 群用户发起查询
-> PermissionService 检查 requester 是否能查 B 群
-> CrossGroupContextService 检查 B 群共享策略
-> 检索 B 群允许共享的摘要或知识库
-> 脱敏
-> 返回 RetrievedContext
-> Audit 记录 requester/source_group/query/result_count
```

### 验收

```text
1. 默认不能跨群查。
2. 未授权时返回 permission_denied。
3. 授权后只返回允许共享的信息。
4. 返回内容带 source 和 visibility。
5. 所有跨群查询可审计。
```

## 7.5 Personality & Eval

### 目标

让群聊回复自然、合适、不抢话，但不欺骗用户它是人类。

### 当前已有

```text
AgentPackage 提示词
Addressee Judge
no_op
Eval
真实模型 smoke
```

### 需要扩展

```text
1. TonePolicy。
2. GroupToneProfile。
3. SpeakerPreference。
4. NaturalChatEval。
5. Interruption / silence / short-reply 策略。
```

### TonePolicy 示例

```json
{
  "default_style": "concise_collaborative",
  "group_style": "work_group",
  "rules": [
    {
      "when": "high_risk_action",
      "style": "formal_confirmation"
    },
    {
      "when": "human_to_human_discussion",
      "style": "silent"
    },
    {
      "when": "direct_question",
      "style": "clear_short_answer"
    }
  ]
}
```

### 验收

```text
1. 明确不是叫它时不回复。
2. 被叫到时能自然接住。
3. 不每次都长篇总结。
4. 不伪装成人类身份。
5. 高风险动作会正式确认。
6. 自然度 eval 通过人工抽样。
```

## 8. Prompt 与 Runtime 的边界

### 可以靠提示词的部分

```text
1. 回复语气。
2. 缺信息时不要猜。
3. 历史上下文是 untrusted context。
4. 当前用户输入优先。
5. 遇到权限/工具不足时说明边界。
```

### 不能只靠提示词的部分

```text
1. 查群成员权限。
2. 改 skills。
3. 发布 AgentPackage。
4. 查询专业智能体进度。
5. 创建知识库。
6. 跨群查信息。
7. 持久化长期记忆。
8. 做审计和审批。
```

这些必须由 runtime/service/tool 完成。

## 9. 推荐开发阶段

### P0：已完成的群聊上下文闭环

状态：已完成第一版。

```text
ConversationContext
Addressee Judge
Context Sufficiency Judge
ContextRetriever
RetrievedContext
Route Guard
Trace/Eval/DeepSeek 回归
```

### P1：群成员身份与权限

目标：

```text
1. 保存群成员和角色。
2. 给高风险动作加权限判断。
3. 支持 skill 修改请求的权限判断。
```

主要文件：

```text
internal/contracts/group.go
internal/identity/*
internal/permission/*
internal/server/server.go
internal/runtime/kernel/*
```

验收：

```text
普通成员不能修改 skill。
管理员可创建修改草稿。
发布必须有审批或明确授权。
```

### P2：长期群聊历史与记忆作用域

目标：

```text
1. 群消息长期存储。
2. topic/thread 摘要。
3. MemoryScope。
4. 共享记忆授权。
```

验收：

```text
多群不串记忆。
共享记忆可被授权群召回。
历史上下文不足时可主动检索。
```

### P3：任务拆分、调用专业智能体、进度查询

目标：

```text
1. 专业智能体能力索引。
2. GroupTaskBinding。
3. 进度查询意图识别。
4. 子任务状态聚合。
```

验收：

```text
用户能问“刚才那个任务怎么样了”。
原智能体能返回专业智能体进度。
失败、等待输入、等待审批能说明清楚。
```

### P4：Skill 更新治理

目标：

```text
1. SkillUpdateRequest。
2. SkillDraft。
3. ApprovalFlow。
4. Eval 后发布。
```

验收：

```text
skill 修改不能绕过权限。
发布前必须有测试或审批。
可回滚。
```

### P5：Agent Factory

目标：

```text
1. 从需求生成专业智能体草稿。
2. 自动生成提示词、工具绑定、eval。
3. package_validate。
4. 真实模型测试。
5. 审批发布。
```

验收：

```text
没有合适专业智能体时能生成草稿。
不能自动无审批上线高风险专业智能体。
```

### P6：外部知识库工具

目标：

```text
1. KnowledgeBase。
2. 文档接入。
3. 检索工具。
4. 权限过滤。
```

验收：

```text
可创建知识库。
可作为工具搜索。
返回引用来源。
无权限不能搜索。
```

### P7：跨群查询工具

目标：

```text
1. CrossGroupContextService。
2. cross_group.search 工具。
3. 脱敏与审计。
```

验收：

```text
默认禁止跨群。
授权后可查摘要。
全程审计。
```

### P8：自然群聊体验

目标：

```text
1. TonePolicy。
2. 群聊节奏。
3. 短回复。
4. 沉默策略。
5. 自然度 eval。
```

验收：

```text
误抢话率降低。
漏接率可接受。
人工抽样认为自然、可靠、不机械。
```

## 10. Eval 矩阵

建议新增 eval：

```text
agent_packages/origin-coordinator/eval/group_identity_permission.yaml
agent_packages/origin-coordinator/eval/skill_update_governance.yaml
agent_packages/origin-coordinator/eval/agent_orchestration_progress.yaml
agent_packages/origin-coordinator/eval/knowledge_tooling.yaml
agent_packages/origin-coordinator/eval/multi_group_memory.yaml
agent_packages/origin-coordinator/eval/cross_group_search.yaml
agent_packages/origin-coordinator/eval/natural_chat_tone.yaml
```

核心测试场景：

```text
1. 群管理员要求修改 skill：生成草稿。
2. 普通成员要求修改 skill：拒绝或请求管理员确认。
3. 用户要求调用已有专业智能体：正确 handoff。
4. 用户要求创建新专业智能体：生成 draft，不直接上线。
5. 用户追问进度：找到 GroupTaskBinding 并返回状态。
6. A 群查 B 群未授权信息：拒绝。
7. A 群查 B 群授权共享摘要：返回脱敏摘要。
8. 多群共享记忆：只召回共享部分。
9. 群聊闲聊不是叫它：no_op。
10. 被叫到但上下文不足：先召回，仍不足再追问。
11. 历史 prompt injection：不执行。
12. 回复语气：工作群简洁，审批场景正式，闲聊轻量。
```

## 11. Trace 与审计事件

建议新增 trace：

```text
identity.member_resolved
permission.checked
skill.update_requested
skill.update_approved
skill.update_published
conversation.topic_matched
memory.scope_checked
memory.shared_retrieved
agent.capability_matched
agent.factory.draft_created
agent.progress.queried
knowledge.created
knowledge.search_requested
knowledge.search_completed
cross_group.search_requested
cross_group.search_denied
cross_group.search_completed
tone.policy_applied
```

审计必须覆盖：

```text
1. skill 修改。
2. AgentPackage 发布。
3. 专业智能体创建。
4. 知识库创建和检索。
5. 跨群查询。
6. 共享记忆授权。
7. 权限拒绝。
```

## 12. 上线风险

```text
1. 误抢话：群聊中错误接话。
2. 漏接：用户其实叫它，但它沉默。
3. 越权：普通成员修改 skill 或读跨群信息。
4. 串群：A 群看到 B 群私有记忆。
5. 幻觉进度：没有真实 task 状态却编造进度。
6. 自动创建专业智能体过度：重复创建、低质量创建。
7. 知识库污染：不可信文档影响回答。
8. 拟人化过度：让用户误以为它是人类。
```

治理要求：

```text
1. 所有权限动作先 check 再执行。
2. 所有跨群信息默认拒绝。
3. 所有记忆必须带 scope。
4. 所有创建/发布必须可回滚。
5. 所有真实模型测试必须可复现。
```

## 13. 最小可行路线

推荐先做四个最关键扩展，而不是十三项一起做：

```text
1. Group Identity & Permission
2. GroupTaskBinding + Progress Query
3. MemoryScope + Long-term Group History
4. SkillUpdateRequest + Approval
```

这四个补齐后，CleanCore 才能安全地做：

```text
1. 更新自己的 skills。
2. 多群长期运行。
3. 专业智能体任务追踪。
4. 跨群共享和知识库。
```

## 14. 结论

十三项能力都可以在 CleanCore 上实现，但不能做成十三个 Coordinator 内部分支。

正确路线是：

```text
内核保持薄：
ConversationContext、Permission Check、Task/Handoff、Tool Call、Trace/Audit。

能力做成扩展：
Identity、Memory、AgentFactory、KnowledgeBase、CrossGroupSearch、TonePolicy。

高风险动作工具化：
skill update、agent creation、knowledge creation、cross group search 全部走受控工具。
```

这样 CleanCore 会变成可扩展的群聊智能体内核，而不是臃肿的大提示词脚本。

## 15. v0.1 落地记录

日期：2026-05-31

本轮已按“薄内核 + 扩展服务 + 受控工具”的路线完成第一版核心能力落地。重点不是把十三项需求塞进 Coordinator，而是把它们拆成可替换、可测试、可治理的能力面。

### 15.1 已新增契约

```text
internal/contracts/group_extensions.go
internal/contracts/ids.go
internal/contracts/governance.go
```

覆盖：

```text
GroupMemberProfile
GroupPermissionPolicy / PermissionDecision
SkillUpdateRequest
ConversationTopicThread
MemoryScope
KnowledgeBase / KnowledgeDocument / KnowledgeSearchResult
GroupTaskBinding / TaskProgressSummary
AgentCapability / AgentDraftRequest
TonePolicy / ToneDecision
Trace/Audit 事件常量
```

### 15.2 已新增服务

```text
internal/identity
internal/permission
internal/memoryscope
internal/skillupdate
internal/knowledge
internal/crossgroup
internal/task/progress
internal/agentcapability
internal/agentfactory
internal/tone
```

能力说明：

```text
IdentityService：保存和解析群成员、角色、别名。
PermissionService：群级 RBAC/显式授权检查；cross_group.search 必须显式授权，管理员不能默认越权。
MemoryScopeService：记忆作用域、群内可见、跨群共享授权。
SkillUpdateService：只创建 skill 更新请求，不直接修改 skill。
KnowledgeBaseService：创建知识库、写入文档、按可见性检索。
CrossGroupContextService：A 群查 B 群时先权限检查，再只检索 B 群允许共享的信息。
TaskProgressService：保存群消息与 task/run/handoff 的绑定，支持后续自然语言查进度。
AgentCapabilityService：匹配已有专业智能体能力，避免重复创建。
AgentFactoryService：生成专业 AgentPackage 草稿，不直接发布。
TonePolicyService：根据群聊信号决定是否回复和回复风格。
```

### 15.3 已新增受控工具

```text
origin.identity.resolve_member
origin.permission.check
origin.skill.propose_update
origin.knowledge.create
origin.knowledge.ingest
origin.knowledge.search
origin.cross_group.search
origin.memory.share
origin.agent.progress_query
origin.agent.capability_match
origin.agent.create_draft
origin.tone.decide
```

其中高风险工具使用 `RiskHigh` 或 `RiskMedium`，仍然受现有 ToolPolicy、AgentPackage tool_bindings、审批与审计约束。

### 15.4 已接入 Core

```text
internal/app/core/core.go
```

Core 初始化时会创建扩展服务，并注册 Origin 扩展工具。Coordinator 仍然只负责输入、上下文、PromptBundle、模型决策、工具执行、trace/audit，不直接内置群成员库、知识库、Agent Factory 业务逻辑。

### 15.5 已补测试

新增或覆盖测试：

```text
identity：成员解析和群成员列表。
permission：默认拒绝、管理员普通动作放行、显式审批策略。
memoryscope：多群默认不串记忆，共享后可读。
skillupdate：无权拒绝，有权创建等待审批请求。
knowledge：群内/共享知识可见性过滤。
crossgroup：跨群默认拒绝，显式授权后返回共享结果。
task/progress：通过 GroupTaskBinding 查询专业智能体任务进度。
agentcapability：已有专业智能体能力匹配。
agentfactory：有权限时创建 AgentPackage 草稿。
tone：不该接话时沉默，高风险正式确认，被问到时简短清晰。
tool/originext：扩展工具注册和权限工具执行。
app/core：Core 初始化扩展服务与工具，跨群工具必须显式授权。
```

### 15.6 本轮验证结果

已通过：

```powershell
.\.tools\go-go1.26.3\go\bin\go.exe test ./... -count=1
.\.tools\go-go1.26.3\go\bin\go.exe vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\package_validate.ps1 -PackageDir agent_packages\origin-coordinator
```

### 15.7 当前仍属于生产化扩展的部分

本轮完成的是核心能力面和可测闭环。2026-05-31 继续补齐了第一批 Postgres 持久化，因此“内存实现升级为生产存储”已经完成第一版。

```text
已完成：
1. GroupMemberProfile / GroupPermissionPolicy / MemoryScope / KnowledgeBase / GroupTaskBinding 的 Postgres 持久化。
2. SkillUpdateRequest / AgentCapability / AgentDraftRequest / TonePolicy 的 Postgres 持久化。
3. Core 在存在 DATABASE_URL 时自动使用 Postgres Store；无数据库时继续使用内存 Store。
4. migration/readiness 已纳入扩展表和关键索引检查。

仍待生产化：
1. 群聊平台 Adapter：企业微信、飞书、Discord、Telegram 等真实成员同步和消息事件接入。
2. KnowledgeIndexer：文件、URL、API、数据库来源的增量索引和向量/混合检索。
3. CrossGroupContextService 的脱敏、数据分级、可审计引用链。
4. AgentFactory 的完整 package_validate、真实模型 eval、审批发布、回滚流水线。
5. TaskProgressService 与真实 handoff/run/event 的更细粒度进度聚合。
6. TonePolicy 从代码默认值升级为 AgentPackage 可配置文件，例如 tone/*.md 或 metadata.tone_policy。
7. 面向真实群聊的自然度 eval、人审抽样、误抢话/漏接话指标。
```

结论：十三项能力已经有可运行、可持久化的核心扩展面。生产上线还需要接入真实群聊平台、知识索引基础设施、脱敏治理、真实流量评测与发布审批流水线。

### 15.8 v0.2 持久化落地记录

日期：2026-05-31

本轮新增：

```text
migrations/001_clean_core_base.sql
internal/storage/postgres/group_extensions.go
internal/storage/postgres/postgres.go
internal/storage/postgres/postgres_test.go
internal/storage/migration/live_readiness.go
internal/storage/migration/readiness.go
```

新增 Postgres 表：

```text
group_members
group_permission_policies
memory_scopes
knowledge_bases
knowledge_documents
group_task_bindings
skill_update_requests
agent_capabilities
agent_draft_requests
tone_policies
```

服务层改造：

```text
identity.Store
permission.Store
memoryscope.Store
knowledge.Store
task/progress.Store
skillupdate.Store
agentcapability.Store
agentfactory.Store
tone.Store
```

验证通过：

```powershell
.\.tools\go-go1.26.3\go\bin\go.exe test ./... -count=1
.\.tools\go-go1.26.3\go\bin\go.exe vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\package_validate.ps1 -PackageDir agent_packages\origin-coordinator
```
