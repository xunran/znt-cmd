# 原智能体 Clean Core 工程实施规格文档

版本：v1.0 Implementation Specification  
日期：2026-05-29  
基准架构文档：原智能体 Clean Core 全景开发设计文档 v1.2 Clean  
技术栈：Golang-only Core  
定位：供开发人员直接落地编码、建表、写接口、写测试使用的工程实施规格  

---

## 0. 文档定位

本文件是“施工图”，不是架构蓝图。

它基于《原智能体 Clean Core 全景开发设计文档 v1.2 Clean》，进一步明确：

```text
1. Go 代码包如何组织。
2. 核心领域对象如何定义。
3. 数据库表如何设计。
4. 状态机如何流转。
5. Command / API 如何定义。
6. 事件类型如何定义。
7. 错误码如何定义。
8. Policy / Prompt / Tool / Handoff 如何落地。
9. 事务、并发、幂等如何处理。
10. 模块测试和链路测试如何验收。
```

本文档的目标是让开发人员少猜、少返工、少把边界写乱。

---

## 1. 核心开发原则

### 1.1 不改变 v1.2 Clean 的核心边界

工程实现必须严格遵守：

```text
runtime-kernel：只编排运行，不做具体能力。
task-runtime：保存任务事实和状态机。
context-engine：构建 WorkView / PromptBundle，不保存事实。
decision-engine：解析和校验模型 Decision，不判断权限。
model-runtime：只调用模型，不理解 AgentRun / Task。
policy-engine：判断策略，不驱动流程。
tool-runtime：治理和执行工具，不做模型决策。
execution-domain：决定在哪里执行，不判断业务权限。
memory-artifact：保存记忆、文件、产物和上下文引用。
governance：记录 Trace / Audit / Metrics，不影响业务结果。
agent-definition：管理 AgentPackage / Skill / ToolBinding / Release。
capability-discovery：召回候选 Agent / Skill / Tool。
core-contracts：只放共享类型和稳定协议。
```

### 1.2 事实源原则

```text
TaskEvent 是任务推进事实。
ToolResult 是工具执行事实。
Artifact 是产物事实。
MemoryEvent 是记忆写入事实。
AuditEvent 是权限和行为事实。
TraceEvent 是运行过程事实。
AgentDefinitionVersion 是智能体定义事实。
PromptBundle 不是事实源。
WorkView 不是事实源。
```

### 1.3 动态边界原则

```text
运行中可以动态构建 WorkView。
运行中可以动态构建 PromptBundle。
运行中可以动态选择 CandidateSet。
运行中可以创建 TaskEvent / Artifact / ToolResult。
运行中可以创建 Handoff。

运行中不能静默修改 AgentDefinition。
运行中不能静默修改 ToolRegistry。
运行中不能静默修改 Published Skill。
运行中不能绕过 Policy。
运行中不能绕过 Audit。
```

---

## 2. Go 工程目录结构

推荐目录：

```text
cmd/
  clean-core-server/
    main.go

internal/
  contracts/
    ids.go
    enums.go
    errors.go
    envelope.go
    runtime_context.go
    collaboration.go
    artifact_ref.go

  agentdef/
    definition/
    package/
    compiler/
    registry/
    loader/
    release/
    evalspec/

  runtime/
    kernel/
    coordinator/
    dispatch/
    loop/

  task/
    state/
    command/
    event/
    plan/
    handoff/
    repository/

  context/
    workview/
    promptbundle/
    compression/
    injectionguard/
    handoffpkg/

  discovery/
    capability/
    skill/
    tool/
    agent/
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
    handoff/
    release/

  tool/
    registry/
    gateway/
    router/
    executor/
    validator/
    adapters/
    delegate/

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
    contextref/

  governance/
    trace/
    audit/
    metrics/
    replay/
    redaction/

  bridge/
    collaboration/
    array/

  storage/
    postgres/
    migration/

pkg/
  jsonschema/
  idgen/
  clock/
  hash/
  pagination/
```

规则：

```text
1. internal/contracts 不允许依赖任何业务模块。
2. runtime 可以依赖其他模块接口，但其他模块不能反向依赖 runtime。
3. repository 实现放在模块内部或 storage/postgres 下，但模块对外暴露 interface。
4. bridge 是外部适配层，不属于 Clean Core 内核。
5. cmd 只做依赖装配和服务启动。
```

---

## 3. 核心 ID 与枚举

### 3.1 ID 类型

建议使用强类型别名，避免字符串混用。

```go
type TenantID string
type UserID string
type AgentID string
type AgentVersion string
type AgentRunID string
type TaskID string
type TaskEventID string
type TraceID string
type SpanID string
type DecisionID string
type ToolCallID string
type ToolResultID string
type ArtifactID string
type MemoryID string
type PolicySetID string
type PackageVersionID string
type HandoffID string
type ContextPackageID string
type ExternalTaskID string
```

### 3.2 核心枚举

```go
type DecisionType string

const (
    DecisionReply            DecisionType = "reply"
    DecisionNoOp             DecisionType = "no_op"
    DecisionAskClarification DecisionType = "ask_clarification"
    DecisionToolCall         DecisionType = "tool_call"
    DecisionUnsupported      DecisionType = "unsupported"
    DecisionError            DecisionType = "error"
)

type ReplyKind string

const (
    ReplyAnswer               ReplyKind = "answer"
    ReplyRefusal              ReplyKind = "refusal"
    ReplyPolicyNotice         ReplyKind = "policy_notice"
    ReplyClarificationMessage ReplyKind = "clarification_message"
    ReplyStatusUpdate         ReplyKind = "status_update"
)
```

```go
type RiskLevel string

const (
    RiskLow      RiskLevel = "low"
    RiskMedium   RiskLevel = "medium"
    RiskHigh     RiskLevel = "high"
    RiskCritical RiskLevel = "critical"
)
```

---

## 4. AgentEnvelope 与 RuntimeContext

### 4.1 AgentEnvelope

所有进入 Clean Core 的请求必须先标准化为 AgentEnvelope。

```go
type AgentEnvelope struct {
    EnvelopeID string         `json:"envelope_id"`
    TraceID    TraceID        `json:"trace_id"`
    Target     AgentTarget    `json:"target"`
    Caller     AgentCaller    `json:"caller"`
    Command    string         `json:"command"`
    Payload    map[string]any `json:"payload"`
    Context    RuntimeContext `json:"context"`
    CreatedAt  time.Time      `json:"created_at"`
}
```

### 4.2 AgentTarget

```go
type AgentTarget struct {
    AgentID AgentID      `json:"agent_id"`
    Version AgentVersion `json:"version,omitempty"`
}
```

### 4.3 AgentCaller

```go
type AgentCaller struct {
    CallerID   string `json:"caller_id"`
    CallerType string `json:"caller_type"` // user | agent | worker | system | external
    TenantID   TenantID `json:"tenant_id"`
}
```

### 4.4 RuntimeContext

```go
type RuntimeContext struct {
    TenantID TenantID `json:"tenant_id"`
    UserID   UserID   `json:"user_id,omitempty"`

    SessionID string `json:"session_id,omitempty"`
    TaskID    TaskID `json:"task_id,omitempty"`

    Permissions []Permission `json:"permissions,omitempty"`

    Collaboration *CollaborationContext `json:"collaboration,omitempty"`

    RequestID string `json:"request_id,omitempty"`
    Locale    string `json:"locale,omitempty"`
    Timezone  string `json:"timezone,omitempty"`
}
```

### 4.5 CollaborationContext

Clean Core 不管理 Group，只保存外部协作上下文引用。

```go
type CollaborationContext struct {
    Provider string `json:"provider"` // array | slack | feishu | custom

    ExternalWorkspaceID string `json:"external_workspace_id,omitempty"`
    ExternalGroupID     string `json:"external_group_id,omitempty"`
    ExternalChannelID   string `json:"external_channel_id,omitempty"`
    ExternalThreadID    string `json:"external_thread_id,omitempty"`
    ExternalTaskID      string `json:"external_task_id,omitempty"`
    ExternalMessageID   string `json:"external_message_id,omitempty"`

    CallerID   string `json:"caller_id"`
    CallerType string `json:"caller_type"`

    ReplyTarget *ReplyTarget `json:"reply_target,omitempty"`
}
```

```go
type ReplyTarget struct {
    Type string `json:"type"` // task | message | thread | webhook
    ID   string `json:"id"`
}
```

---

## 5. AgentDefinition 与 AgentPackage

### 5.1 AgentPackageVersion

```go
type AgentPackageVersion struct {
    PackageVersionID PackageVersionID `json:"package_version_id"`
    AgentID          AgentID          `json:"agent_id"`
    Version          AgentVersion     `json:"version"`

    Status ReleaseStatus `json:"status"`

    SourceHash string `json:"source_hash"`
    CompiledHash string `json:"compiled_hash"`

    CreatedBy string    `json:"created_by"`
    CreatedAt time.Time `json:"created_at"`
    PublishedAt *time.Time `json:"published_at,omitempty"`
}
```

### 5.2 ReleaseStatus

```go
type ReleaseStatus string

const (
    ReleaseDraft      ReleaseStatus = "draft"
    ReleaseValidated  ReleaseStatus = "validated"
    ReleaseEvaluated  ReleaseStatus = "evaluated"
    ReleaseReviewed   ReleaseStatus = "reviewed"
    ReleasePublished  ReleaseStatus = "published"
    ReleaseCanary     ReleaseStatus = "canary"
    ReleaseStable     ReleaseStatus = "stable"
    ReleaseDeprecated ReleaseStatus = "deprecated"
    ReleaseRolledBack ReleaseStatus = "rolled_back"
)
```

### 5.3 AgentDefinition

```go
type AgentDefinition struct {
    AgentID AgentID `json:"agent_id"`
    Version AgentVersion `json:"version"`

    Name        string `json:"name"`
    Description string `json:"description"`

    IdentityPrompt string `json:"identity_prompt"`
    SystemPrompt   string `json:"system_prompt"`
    DeveloperPrompt string `json:"developer_prompt,omitempty"`

    Skills []SkillDefinitionRef `json:"skills"`
    Tools  AgentToolsConfig     `json:"tools"`

    PolicyRefs AgentPolicyRefs `json:"policy_refs"`

    Runtime RuntimeLimits `json:"runtime"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 5.4 AgentToolsConfig

```go
type AgentToolsConfig struct {
    AllowedToolIDs []string `json:"allowed_tool_ids"`
    ExposedToolIDs []string `json:"exposed_tool_ids,omitempty"`
    DeniedToolIDs  []string `json:"denied_tool_ids,omitempty"`
}
```

内部模型可用：

```text
candidateTools ∩ allowedToolIds - deniedToolIds
```

外部 tools.invoke 可用：

```text
exposedToolIds ∩ caller permissions ∩ policy allowed
```

### 5.5 RuntimeLimits

```go
type RuntimeLimits struct {
    MaxSteps       int           `json:"max_steps"`
    MaxToolCalls   int           `json:"max_tool_calls"`
    MaxDuration    time.Duration `json:"max_duration"`
    MaxPromptTokens int          `json:"max_prompt_tokens"`
}
```

---

## 6. SkillDefinition

### 6.1 SkillCard

```go
type SkillCard struct {
    SkillID string `json:"skill_id"`
    Version string `json:"version"`

    Name        string   `json:"name"`
    Description string   `json:"description"`
    Tags        []string `json:"tags"`

    WhenToUse    []string `json:"when_to_use"`
    WhenNotToUse []string `json:"when_not_to_use,omitempty"`

    RiskLevel RiskLevel `json:"risk_level"`

    ResourceRefs []string `json:"resource_refs,omitempty"`
}
```

### 6.2 SkillInstruction

```go
type SkillInstruction struct {
    SkillID string `json:"skill_id"`
    Content string `json:"content"`

    OutputRequirements []string `json:"output_requirements,omitempty"`
    Constraints        []string `json:"constraints,omitempty"`
}
```

### 6.3 SkillResourceRef

```go
type SkillResourceRef struct {
    ResourceID string `json:"resource_id"`
    Type       string `json:"type"` // reference | example | template | script | asset
    URI        string `json:"uri"`
    LoadPolicy string `json:"load_policy"` // on_demand | when_skill_selected | manual
}
```

### 6.4 SkillDefinition

```go
type SkillDefinition struct {
    Card        SkillCard         `json:"card"`
    Instruction SkillInstruction  `json:"instruction"`
    Resources   []SkillResourceRef `json:"resources,omitempty"`
}
```

加载规则：

```text
SkillCard：进入索引。
SkillInstruction：被选中后进入 PromptBundle。
SkillResources：按需引用，不直接塞进上下文。
```

---

## 7. Task 与 TaskEvent

### 7.1 Task

```go
type Task struct {
    TaskID TaskID `json:"task_id"`

    TenantID TenantID `json:"tenant_id"`

    ParentTaskID *TaskID `json:"parent_task_id,omitempty"`
    RootTaskID   *TaskID `json:"root_task_id,omitempty"`

    Title       string `json:"title"`
    Objective   string `json:"objective"`
    Description string `json:"description,omitempty"`

    Status TaskStatus `json:"status"`

    OwnerAgentID    AgentID `json:"owner_agent_id,omitempty"`
    AssignedAgentID AgentID `json:"assigned_agent_id,omitempty"`

    SourceHandoffID *HandoffID `json:"source_handoff_id,omitempty"`

    AgentID AgentID `json:"agent_id"`
    AgentVersion AgentVersion `json:"agent_version"`
    PolicySetID PolicySetID `json:"policy_set_id"`

    Version int64 `json:"version"` // optimistic lock

    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
}
```

### 7.2 TaskStatus

```go
type TaskStatus string

const (
    TaskCreated         TaskStatus = "created"
    TaskAccepted        TaskStatus = "accepted"
    TaskPlanning        TaskStatus = "planning"
    TaskRunning         TaskStatus = "running"
    TaskWaitingInput    TaskStatus = "waiting_input"
    TaskWaitingTool     TaskStatus = "waiting_tool"
    TaskWaitingApproval TaskStatus = "waiting_approval"
    TaskBlocked         TaskStatus = "blocked"
    TaskPaused          TaskStatus = "paused"
    TaskCompleted       TaskStatus = "completed"
    TaskFailed          TaskStatus = "failed"
    TaskCancelled       TaskStatus = "cancelled"
    TaskRejected        TaskStatus = "rejected"
)
```

### 7.3 TaskEvent

TaskEvent append-only。

```go
type TaskEvent struct {
    EventID TaskEventID `json:"event_id"`
    TaskID  TaskID      `json:"task_id"`

    Type string `json:"type"`

    ActorID   string `json:"actor_id"`
    ActorType string `json:"actor_type"` // user | agent | system | tool

    Payload map[string]any `json:"payload"`

    RunID AgentRunID `json:"run_id,omitempty"`
    StepID string `json:"step_id,omitempty"`

    CreatedAt time.Time `json:"created_at"`
}
```

禁止修改历史 TaskEvent。  
如需修正，追加 correction event。

### 7.4 TaskCommand

```go
type TaskCommand string

const (
    CmdProvideInput        TaskCommand = "provide_input"
    CmdApproveAction       TaskCommand = "approve_action"
    CmdRejectAction        TaskCommand = "reject_action"
    CmdCancel              TaskCommand = "cancel"
    CmdPause               TaskCommand = "pause"
    CmdResume              TaskCommand = "resume"
    CmdCreatePlan          TaskCommand = "create_plan"
    CmdUpdatePlan          TaskCommand = "update_plan"
    CmdReplan              TaskCommand = "replan"
    CmdCompleteStep        TaskCommand = "complete_step"
    CmdFailStep            TaskCommand = "fail_step"
    CmdUpgradeAgentVersion TaskCommand = "upgrade_agent_version"
    CmdCreateHandoff       TaskCommand = "create_handoff"
    CmdAcceptHandoff       TaskCommand = "accept_handoff"
    CmdRejectHandoff       TaskCommand = "reject_handoff"
    CmdCompleteHandoff     TaskCommand = "complete_handoff"
    CmdFailHandoff         TaskCommand = "fail_handoff"
)
```

---

## 8. Task 状态机规格

### 8.1 基础状态转换

| 当前状态 | 命令/事件 | 下一个状态 | 是否写 Audit |
|---|---|---:|---:|
| created | accept | accepted | 否 |
| accepted | plan_started | planning | 否 |
| planning | run_started | running | 否 |
| running | ask_clarification | waiting_input | 否 |
| waiting_input | provide_input | running | 否 |
| running | approval_required | waiting_approval | 是 |
| waiting_approval | approve_action | running | 是 |
| waiting_approval | reject_action | blocked / failed | 是 |
| running | tool_waiting | waiting_tool | 否 |
| waiting_tool | tool_completed | running | 否 |
| running | pause | paused | 是 |
| paused | resume | running | 是 |
| running | complete | completed | 否 |
| running | fail | failed | 否 |
| created / accepted / running / waiting_* / paused | cancel | cancelled | 是 |

### 8.2 状态机要求

```text
1. 所有状态转换必须通过 task-runtime。
2. 状态更新必须追加 TaskEvent。
3. Task.version 使用乐观锁。
4. 并发更新冲突返回 TASK_CONFLICT。
5. 终态不能再进入 running，除非通过新 Task 或显式 reopen 策略。
```

---

## 9. AgentRun

### 9.1 AgentRun

```go
type AgentRun struct {
    RunID AgentRunID `json:"run_id"`
    TraceID TraceID  `json:"trace_id"`

    TenantID TenantID `json:"tenant_id"`

    AgentID AgentID `json:"agent_id"`
    AgentVersion AgentVersion `json:"agent_version"`

    TaskID TaskID `json:"task_id,omitempty"`

    Status RunStatus `json:"status"`

    StepCount int `json:"step_count"`
    ToolCallCount int `json:"tool_call_count"`

    PolicySetID PolicySetID `json:"policy_set_id"`

    StartedAt time.Time `json:"started_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`

    ErrorCode string `json:"error_code,omitempty"`
    ErrorMessage string `json:"error_message,omitempty"`
}
```

### 9.2 RunStatus

```go
type RunStatus string

const (
    RunCreated        RunStatus = "created"
    RunRunning        RunStatus = "running"
    RunWaitingInput   RunStatus = "waiting_input"
    RunWaitingApproval RunStatus = "waiting_approval"
    RunCompleted      RunStatus = "completed"
    RunFailed         RunStatus = "failed"
    RunCancelled      RunStatus = "cancelled"
)
```

### 9.3 AgentRun 版本钉住

AgentRun 创建时必须记录：

```text
AgentDefinition version
AgentPackage version
PolicySet version
SkillDefinition versions
ToolDefinition versions
Model provider / model name
```

一个 AgentRun 内不得静默切换以上版本。

---

## 10. TaskPlan / PlanStep

### 10.1 TaskPlan

```go
type TaskPlan struct {
    PlanID string `json:"plan_id"`
    TaskID TaskID `json:"task_id"`

    Objective string `json:"objective"`
    Status PlanStatus `json:"status"`

    CreatedBy string `json:"created_by"` // model | user | system
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

### 10.2 PlanStep

```go
type PlanStep struct {
    StepID string `json:"step_id"`
    PlanID string `json:"plan_id"`
    TaskID TaskID `json:"task_id"`

    Index int `json:"index"`
    Title string `json:"title"`
    Description string `json:"description"`

    ExpectedToolHints []string `json:"expected_tool_hints,omitempty"`

    Status PlanStepStatus `json:"status"`

    ResultRefs []ArtifactRef `json:"result_refs,omitempty"`

    FailureReason string `json:"failure_reason,omitempty"`

    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

### 10.3 PlanStepStatus

```go
type PlanStepStatus string

const (
    PlanStepPending   PlanStepStatus = "pending"
    PlanStepRunning   PlanStepStatus = "running"
    PlanStepCompleted PlanStepStatus = "completed"
    PlanStepFailed    PlanStepStatus = "failed"
    PlanStepSkipped   PlanStepStatus = "skipped"
)
```

### 10.4 Plan 要求

```text
1. Plan 不是硬编码 workflow。
2. PlanStep 只描述当前阶段，不直接执行工具。
3. ToolResult 应关联 PlanStep。
4. replan 必须保留旧 Plan 历史。
5. completed Task 必须能查看完整 Plan 执行轨迹。
```

---

## 11. AgentHandoff

### 11.1 AgentHandoff

```go
type AgentHandoff struct {
    HandoffID HandoffID `json:"handoff_id"`

    ParentTaskID TaskID `json:"parent_task_id"`
    ChildTaskID  *TaskID `json:"child_task_id,omitempty"`

    FromAgentID AgentID `json:"from_agent_id"`
    ToAgentID   AgentID `json:"to_agent_id"`

    Objective string `json:"objective"`
    Reason    string `json:"reason"`

    ContextPackageRef ContextPackageID `json:"context_package_ref"`

    ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`

    ExpectedOutput ExpectedOutput `json:"expected_output"`

    Status HandoffStatus `json:"status"`

    CreatedAt time.Time `json:"created_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
}
```

### 11.2 HandoffStatus

```go
type HandoffStatus string

const (
    HandoffCreated   HandoffStatus = "created"
    HandoffAccepted  HandoffStatus = "accepted"
    HandoffRejected  HandoffStatus = "rejected"
    HandoffRunning   HandoffStatus = "running"
    HandoffCompleted HandoffStatus = "completed"
    HandoffFailed    HandoffStatus = "failed"
    HandoffCancelled HandoffStatus = "cancelled"
)
```

### 11.3 Handoff 状态转换

| 当前状态 | 命令/事件 | 下一个状态 | Audit |
|---|---|---:|---:|
| created | policy_allowed | accepted | 是 |
| created | policy_denied | rejected | 是 |
| accepted | child_task_created | running | 是 |
| running | child_task_completed | completed | 是 |
| running | child_task_failed | failed | 是 |
| created / accepted / running | cancel | cancelled | 是 |

### 11.4 Handoff 规则

```text
1. Handoff 必须经过 HandoffPolicy。
2. Handoff 必须创建 HandoffContextPackage。
3. Handoff 可创建 ChildTask。
4. ChildTask 结果必须回流 ParentTask。
5. Handoff 必须写 Trace / Audit。
```

---

## 12. HandoffContextPackage

### 12.1 HandoffContextPackage

```go
type HandoffContextPackage struct {
    PackageID ContextPackageID `json:"package_id"`

    ParentTaskID TaskID `json:"parent_task_id"`
    SourceRunID  AgentRunID `json:"source_run_id"`

    FromAgentID AgentID `json:"from_agent_id"`
    ToAgentID   AgentID `json:"to_agent_id"`

    Objective string `json:"objective"`
    Reason    string `json:"reason"`

    Summary string `json:"summary"`
    KeyFacts []string `json:"key_facts"`
    Constraints []string `json:"constraints"`
    OpenQuestions []string `json:"open_questions,omitempty"`

    ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`
    ToolResultRefs []ToolResultID `json:"tool_result_refs,omitempty"`
    MemoryRefs []MemoryID `json:"memory_refs,omitempty"`
    TaskEventRefs []TaskEventID `json:"task_event_refs,omitempty"`

    AllowedContextScopes []string `json:"allowed_context_scopes"`
    DeniedContextScopes []string `json:"denied_context_scopes,omitempty"`

    ExpectedOutput ExpectedOutput `json:"expected_output"`

    Mode HandoffMode `json:"mode"`

    Hash string `json:"hash"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 12.2 HandoffMode

```go
type HandoffMode string

const (
    HandoffFullContext   HandoffMode = "full_context"
    HandoffSummaryOnly   HandoffMode = "summary_only"
    HandoffReferenceOnly HandoffMode = "reference_only"
    HandoffHybrid        HandoffMode = "hybrid"
)
```

默认：

```text
hybrid
```

### 12.3 HandoffContextPackage 规则

```text
1. 默认不传完整 PromptBundle。
2. 默认不传完整聊天历史。
3. 大对象只传引用。
4. 下游 Agent 必须重新构建自己的 WorkView。
5. 引用读取必须再次经过 Policy。
```

---

## 13. WorkView

### 13.1 WorkView

```go
type WorkView struct {
    RunID AgentRunID `json:"run_id"`
    TaskID TaskID `json:"task_id,omitempty"`

    Agent AgentDefinitionSummary `json:"agent"`

    UserInput string `json:"user_input"`

    TaskSummary TaskSummary `json:"task_summary"`
    PlanSummary *PlanSummary `json:"plan_summary,omitempty"`
    CurrentPlanStep *PlanStepSummary `json:"current_plan_step,omitempty"`

    HandoffContext *HandoffContextSummary `json:"handoff_context,omitempty"`

    MemorySummaries []MemorySummary `json:"memory_summaries,omitempty"`
    ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`
    ToolResultSummaries []ToolResultSummary `json:"tool_result_summaries,omitempty"`

    CandidateCapabilities []CapabilityCard `json:"candidate_capabilities,omitempty"`
    CandidateSkills []SkillCard `json:"candidate_skills,omitempty"`
    CandidateTools []ToolCard `json:"candidate_tools,omitempty"`

    Constraints []string `json:"constraints,omitempty"`
    RiskMarks []RiskMark `json:"risk_marks,omitempty"`
}
```

### 13.2 WorkView 规则

```text
1. WorkView 每轮重建。
2. WorkView 不长期保存为事实源。
3. WorkView 只引用事实，不替代事实。
4. WorkView 可以被 Trace 摘要记录，但原始事实仍在 TaskEvent / Artifact / ToolResult。
```

---

## 14. PromptBundle

### 14.1 PromptBundle

```go
type PromptBundle struct {
    BundleID string `json:"bundle_id"`
    RunID AgentRunID `json:"run_id"`

    System string `json:"system"`
    Developer string `json:"developer,omitempty"`
    Task string `json:"task"`
    Context string `json:"context"`

    SkillInstructions []string `json:"skill_instructions,omitempty"`
    ToolCards []ToolCard `json:"tool_cards,omitempty"`
    ToolDefinitions []ToolDefinition `json:"tool_definitions,omitempty"`

    OutputSchema map[string]any `json:"output_schema,omitempty"`

    Constraints []string `json:"constraints,omitempty"`

    Hash string `json:"hash"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 14.2 PromptBundle 渲染顺序

推荐顺序：

```text
1. System：平台级不可变指令。
2. Developer：AgentDefinition 中的开发者指令。
3. Agent Identity：AGENTS.md 编译结果。
4. Task Objective：当前任务目标。
5. Current State：Task / Plan / Step / Handoff 状态。
6. Context：Memory / Artifact / ToolResult 摘要。
7. Skills：选中的 SkillInstruction。
8. Tools：候选 ToolCard。
9. Constraints：Policy 约束、风险提示、输出限制。
10. Output Schema：Decision JSON Schema。
```

### 14.3 注入防护

必须显式区分来源：

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
handoff context
```

用户输入、工具输出、外部消息不得被当成系统指令。

---

## 15. Decision

### 15.1 Decision

```go
type Decision struct {
    DecisionID DecisionID `json:"decision_id"`
    Type DecisionType `json:"type"`

    Reason string `json:"reason,omitempty"`
    Confidence float64 `json:"confidence,omitempty"`

    Reply *DecisionReply `json:"reply,omitempty"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`

    Ask *ClarificationRequest `json:"ask,omitempty"`

    Error *DecisionError `json:"error,omitempty"`
}
```

### 15.2 DecisionReply

```go
type DecisionReply struct {
    Kind ReplyKind `json:"kind"`
    Text string `json:"text"`
    ContentType string `json:"content_type,omitempty"`
}
```

### 15.3 ClarificationRequest

```go
type ClarificationRequest struct {
    Question string `json:"question"`
    RequiredFields []string `json:"required_fields,omitempty"`
}
```

### 15.4 Decision 校验规则

```text
1. Decision.type 必须是稳定枚举。
2. reply 类型必须包含 reply.text。
3. ask_clarification 必须包含 question。
4. tool_call 必须引用候选工具。
5. tool_call 参数必须可解析。
6. unsupported 必须包含 reason。
7. error 必须包含 error code 或 message。
```

---

## 16. ToolDefinition / ToolCall / ToolResult

### 16.1 ToolDefinition

```go
type ToolDefinition struct {
    ToolID string `json:"tool_id"`
    Name string `json:"name"`
    Description string `json:"description"`

    InputSchema map[string]any `json:"input_schema"`
    OutputSchema map[string]any `json:"output_schema,omitempty"`

    RiskLevel RiskLevel `json:"risk_level"`

    Visibility ToolVisibility `json:"visibility"`

    ExecutionProfile string `json:"execution_profile"`

    Version string `json:"version"`
}
```

### 16.2 ToolVisibility

```go
type ToolVisibility string

const (
    ToolPrivate   ToolVisibility = "private"
    ToolProtected ToolVisibility = "protected"
    ToolExposed   ToolVisibility = "exposed"
)
```

### 16.3 ToolCall

```go
type ToolCall struct {
    ToolCallID ToolCallID `json:"tool_call_id"`
    ToolID string `json:"tool_id"`
    Name string `json:"name"`

    Arguments map[string]any `json:"arguments"`

    RunID AgentRunID `json:"run_id"`
    TaskID TaskID `json:"task_id,omitempty"`
    PlanStepID string `json:"plan_step_id,omitempty"`

    IdempotencyKey string `json:"idempotency_key"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 16.4 ToolResult

```go
type ToolResult struct {
    ToolResultID ToolResultID `json:"tool_result_id"`
    ToolCallID ToolCallID `json:"tool_call_id"`

    Status ToolResultStatus `json:"status"`

    Output map[string]any `json:"output,omitempty"`
    Error *ToolExecutionError `json:"error,omitempty"`

    ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`

    StartedAt time.Time `json:"started_at"`
    CompletedAt time.Time `json:"completed_at"`
}
```

### 16.5 ToolResultStatus

```go
type ToolResultStatus string

const (
    ToolResultSucceeded ToolResultStatus = "succeeded"
    ToolResultFailed    ToolResultStatus = "failed"
    ToolResultDenied    ToolResultStatus = "denied"
    ToolResultPendingApproval ToolResultStatus = "pending_approval"
)
```

---

## 17. PolicySet

### 17.1 PolicySet

```go
type PolicySet struct {
    PolicySetID PolicySetID `json:"policy_set_id"`
    TenantID TenantID `json:"tenant_id"`

    Version string `json:"version"`

    RuntimePolicy RuntimePolicy `json:"runtime_policy"`
    ToolPolicy ToolPolicy `json:"tool_policy"`
    ApprovalPolicy ApprovalPolicy `json:"approval_policy"`
    PromptPolicy PromptPolicy `json:"prompt_policy"`
    CompressionPolicy ContextCompressionPolicy `json:"compression_policy"`
    RecoveryPolicy TaskRecoveryPolicy `json:"recovery_policy"`
    HandoffPolicy HandoffPolicy `json:"handoff_policy"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 17.2 Policy Merge 顺序

```text
System Default Policy
  ↓
Tenant Policy
  ↓
Agent Policy
  ↓
Task Runtime Override
```

硬约束：

```text
上层禁止的能力，下层不能重新打开。
系统强制审批的工具，AgentPolicy 不能取消审批。
maxRetries 不能超过系统上限。
allowFullContext=false 时，任务级 override 不能打开 full_context。
```

### 17.3 HandoffPolicy

```go
type HandoffPolicy struct {
    DefaultMode HandoffMode `json:"default_mode"`
    AllowFullContext bool `json:"allow_full_context"`
    MaxContextTokens int `json:"max_context_tokens"`

    RequireApprovalForCrossAgent bool `json:"require_approval_for_cross_agent"`
    RequireApprovalForSensitiveArtifacts bool `json:"require_approval_for_sensitive_artifacts"`

    AllowParentTaskQuery bool `json:"allow_parent_task_query"`
    AllowArtifactRead bool `json:"allow_artifact_read"`
    AllowMemoryRead bool `json:"allow_memory_read"`
    AllowTaskEventRead bool `json:"allow_task_event_read"`
}
```

---

## 18. Artifact / Memory

### 18.1 ArtifactRef

```go
type ArtifactRef struct {
    ArtifactID ArtifactID `json:"artifact_id"`
    Type string `json:"type"`
    URI string `json:"uri,omitempty"`
    Summary string `json:"summary,omitempty"`
    Hash string `json:"hash,omitempty"`
}
```

### 18.2 Artifact

```go
type Artifact struct {
    ArtifactID ArtifactID `json:"artifact_id"`
    TenantID TenantID `json:"tenant_id"`

    Type string `json:"type"`
    Name string `json:"name"`

    StorageURI string `json:"storage_uri"`
    MimeType string `json:"mime_type,omitempty"`
    SizeBytes int64 `json:"size_bytes"`

    Hash string `json:"hash"`

    CreatedBy string `json:"created_by"`
    CreatedAt time.Time `json:"created_at"`
}
```

### 18.3 MemoryEvent

```go
type MemoryEvent struct {
    MemoryID MemoryID `json:"memory_id"`
    TenantID TenantID `json:"tenant_id"`
    AgentID AgentID `json:"agent_id,omitempty"`
    UserID UserID `json:"user_id,omitempty"`

    Scope string `json:"scope"` // user | agent | task | tenant
    Content string `json:"content"`
    Summary string `json:"summary,omitempty"`

    SourceEventID string `json:"source_event_id,omitempty"`

    Visibility string `json:"visibility"`
    Confidence float64 `json:"confidence"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 18.4 规则

```text
1. Artifact 大对象不直接进入 PromptBundle。
2. PromptBundle 只放 Artifact 摘要和 ArtifactRef。
3. 长期 Memory 写入必须经过 MemoryPolicy。
4. 敏感 Memory 必须脱敏。
5. Memory 写入必须写 Audit。
```

---

## 19. TraceEvent / AuditEvent

### 19.1 TraceEvent

```go
type TraceEvent struct {
    TraceID TraceID `json:"trace_id"`
    SpanID SpanID `json:"span_id"`

    RunID AgentRunID `json:"run_id,omitempty"`
    TaskID TaskID `json:"task_id,omitempty"`

    Type string `json:"type"`

    Payload map[string]any `json:"payload"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 19.2 AuditEvent

```go
type AuditEvent struct {
    AuditID string `json:"audit_id"`

    TenantID TenantID `json:"tenant_id"`

    ActorID string `json:"actor_id"`
    ActorType string `json:"actor_type"`

    Action string `json:"action"`
    ResourceType string `json:"resource_type"`
    ResourceID string `json:"resource_id"`

    Decision string `json:"decision"` // allowed | denied | approval_required

    Reason string `json:"reason,omitempty"`

    TraceID TraceID `json:"trace_id,omitempty"`
    TaskID TaskID `json:"task_id,omitempty"`
    RunID AgentRunID `json:"run_id,omitempty"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 19.3 必须 Trace 的事件

```text
input.received
agent.loaded
run.created
task.created
task.loaded
workview.built
capability.retrieved
promptbundle.built
model.called
model.completed
decision.created
decision.validated
tool.policy_checked
tool.invoked
tool.completed
tool.failed
task.status_changed
handoff.created
handoff.context_packaged
handoff.completed
response.sent
```

### 19.4 必须 Audit 的事件

```text
tool.policy_denied
tool.approval_required
tool.high_risk_invoked
agent.package.publish
agent.package.rollback
policy.update
memory.write
artifact.delete
handoff.created
handoff.policy_checked
handoff.context_read
external_tool_call
credential.used
```

---

## 20. 数据库表设计

以下为逻辑表结构，字段类型按 PostgreSQL 设计。

### 20.1 agent_package_versions

```sql
CREATE TABLE agent_package_versions (
  package_version_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  source_hash TEXT NOT NULL,
  compiled_hash TEXT NOT NULL,
  source_json JSONB NOT NULL,
  compiled_json JSONB NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  UNIQUE (tenant_id, agent_id, version)
);
```

### 20.2 agent_definitions

```sql
CREATE TABLE agent_definitions (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  definition_json JSONB NOT NULL,
  package_version_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, agent_id, version)
);
```

### 20.3 tasks

```sql
CREATE TABLE tasks (
  task_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT,
  root_task_id TEXT,
  title TEXT NOT NULL,
  objective TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL,
  owner_agent_id TEXT,
  assigned_agent_id TEXT,
  source_handoff_id TEXT,
  agent_id TEXT NOT NULL,
  agent_version TEXT NOT NULL,
  policy_set_id TEXT NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);
```

Indexes:

```sql
CREATE INDEX idx_tasks_tenant_status ON tasks (tenant_id, status);
CREATE INDEX idx_tasks_parent ON tasks (parent_task_id);
CREATE INDEX idx_tasks_root ON tasks (root_task_id);
```

### 20.4 task_events

```sql
CREATE TABLE task_events (
  event_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  run_id TEXT,
  step_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);
```

Indexes:

```sql
CREATE INDEX idx_task_events_task_time ON task_events (task_id, created_at);
CREATE INDEX idx_task_events_type ON task_events (tenant_id, type);
```

### 20.5 agent_runs

```sql
CREATE TABLE agent_runs (
  run_id TEXT PRIMARY KEY,
  trace_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  agent_version TEXT NOT NULL,
  task_id TEXT,
  status TEXT NOT NULL,
  step_count INT NOT NULL DEFAULT 0,
  tool_call_count INT NOT NULL DEFAULT 0,
  policy_set_id TEXT NOT NULL,
  version_snapshot JSONB NOT NULL,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  error_code TEXT,
  error_message TEXT
);
```

### 20.6 task_plans

```sql
CREATE TABLE task_plans (
  plan_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  objective TEXT NOT NULL,
  status TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
```

### 20.7 plan_steps

```sql
CREATE TABLE plan_steps (
  step_id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  step_index INT NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL,
  expected_tool_hints JSONB,
  status TEXT NOT NULL,
  result_refs JSONB,
  failure_reason TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
```

### 20.8 agent_handoffs

```sql
CREATE TABLE agent_handoffs (
  handoff_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT NOT NULL,
  child_task_id TEXT,
  from_agent_id TEXT NOT NULL,
  to_agent_id TEXT NOT NULL,
  objective TEXT NOT NULL,
  reason TEXT NOT NULL,
  context_package_ref TEXT NOT NULL,
  artifact_refs JSONB,
  expected_output JSONB,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);
```

### 20.9 handoff_context_packages

```sql
CREATE TABLE handoff_context_packages (
  package_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT NOT NULL,
  source_run_id TEXT NOT NULL,
  from_agent_id TEXT NOT NULL,
  to_agent_id TEXT NOT NULL,
  mode TEXT NOT NULL,
  content_json JSONB NOT NULL,
  hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
```

### 20.10 tool_calls

```sql
CREATE TABLE tool_calls (
  tool_call_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  task_id TEXT,
  plan_step_id TEXT,
  tool_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  arguments_json JSONB NOT NULL,
  idempotency_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, idempotency_key)
);
```

### 20.11 tool_results

```sql
CREATE TABLE tool_results (
  tool_result_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  tool_call_id TEXT NOT NULL,
  status TEXT NOT NULL,
  output_json JSONB,
  error_json JSONB,
  artifact_refs JSONB,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL
);
```

### 20.12 artifacts

```sql
CREATE TABLE artifacts (
  artifact_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  storage_uri TEXT NOT NULL,
  mime_type TEXT,
  size_bytes BIGINT NOT NULL,
  hash TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
```

### 20.13 trace_events

```sql
CREATE TABLE trace_events (
  id BIGSERIAL PRIMARY KEY,
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  run_id TEXT,
  task_id TEXT,
  type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
```

### 20.14 audit_events

```sql
CREATE TABLE audit_events (
  audit_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  decision TEXT NOT NULL,
  reason TEXT,
  trace_id TEXT,
  task_id TEXT,
  run_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);
```

### 20.15 external_task_bindings

```sql
CREATE TABLE external_task_bindings (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  external_task_id TEXT NOT NULL,
  core_task_id TEXT NOT NULL,
  sync_mode TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, provider, external_task_id)
);
```

### 20.16 policy_sets

```sql
CREATE TABLE policy_sets (
  policy_set_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  version TEXT NOT NULL,
  policy_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, version)
);
```

---

## 21. Command / API 规格

### 21.1 agent.run

```json
{
  "command": "agent.run",
  "target": {
    "agent_id": "data-analyst",
    "version": "1.2.0"
  },
  "payload": {
    "input": "分析这个 CSV 并生成报告",
    "artifact_refs": []
  },
  "context": {
    "tenant_id": "tenant_001",
    "user_id": "user_001"
  }
}
```

响应：

```json
{
  "run_id": "run_xxx",
  "task_id": "task_xxx",
  "status": "completed",
  "reply": {
    "kind": "answer",
    "text": "分析完成，报告已生成。"
  },
  "artifact_refs": []
}
```

### 21.2 task.start

```json
{
  "command": "task.start",
  "target": {
    "agent_id": "planner"
  },
  "payload": {
    "title": "产品方案评估",
    "objective": "评估技术可行性、合规风险、商业价值"
  }
}
```

### 21.3 task.command

```json
{
  "command": "task.command",
  "payload": {
    "task_id": "task_xxx",
    "command": "provide_input",
    "input": {
      "answer": "使用 v2 版本产品方案"
    }
  }
}
```

### 21.4 tools.invoke

外部调用工具只能调用 exposedToolIds。

```json
{
  "command": "tools.invoke",
  "target": {
    "agent_id": "report-agent"
  },
  "payload": {
    "tool_name": "report.generate_public",
    "arguments": {
      "artifact_id": "artifact_001"
    }
  }
}
```

### 21.5 origin.agent.delegate

内部 Agent 委派工具。

```json
{
  "tool_name": "origin.agent.delegate",
  "arguments": {
    "to_agent_id": "risk-agent",
    "objective": "评估该方案的合规风险",
    "reason": "当前任务需要合规专业判断",
    "handoff_mode": "hybrid",
    "artifact_refs": [
      {
        "artifact_id": "artifact_product_plan",
        "type": "document"
      }
    ],
    "expected_output": {
      "format": "markdown",
      "requirements": [
        "列出主要风险",
        "给出风险等级",
        "给出缓解建议"
      ]
    }
  }
}
```

### 21.6 agent.package.publish

```json
{
  "command": "agent.package.publish",
  "payload": {
    "draft_id": "draft_xxx",
    "version": "1.3.0",
    "release_note": "优化数据分析 Skill 和图表生成策略"
  }
}
```

### 21.7 eval.run

```json
{
  "command": "eval.run",
  "payload": {
    "agent_id": "data-analyst",
    "package_version": "1.3.0",
    "eval_suite": "default"
  }
}
```

---

## 22. 错误码

### 22.1 错误结构

```go
type RuntimeError struct {
    Code string `json:"code"`
    Message string `json:"message"`
    Retryable bool `json:"retryable"`
    Repairable bool `json:"repairable"`
    Details map[string]any `json:"details,omitempty"`
}
```

### 22.2 标准错误码

| 错误码 | 含义 | 可重试 | 可 Repair | 默认结果 |
|---|---|---:|---:|---|
| AGENT_NOT_FOUND | Agent 不存在 | 否 | 否 | run failed |
| AGENT_VERSION_NOT_FOUND | Agent 版本不存在 | 否 | 否 | run failed |
| PACKAGE_VERSION_CONFLICT | 包版本冲突 | 否 | 否 | reject publish |
| MODEL_ERROR | 模型调用失败 | 是 | 否 | retry/fail |
| MODEL_TIMEOUT | 模型超时 | 是 | 否 | retry/fail |
| DECISION_SCHEMA_ERROR | Decision 格式错误 | 是 | 是 | repair |
| TOOL_NOT_FOUND | 工具不存在 | 否 | 否 | decision reject |
| TOOL_ARGUMENT_INVALID | 工具参数错误 | 是 | 是 | repair |
| TOOL_POLICY_DENIED | 工具被策略拒绝 | 否 | 否 | audit + fail |
| TOOL_APPROVAL_REQUIRED | 工具需要审批 | 否 | 否 | waiting_approval |
| TOOL_EXECUTION_FAILED | 工具执行失败 | 是 | 视策略 | retry/repair/fail |
| EXECUTION_DOMAIN_UNAVAILABLE | 执行域不可用 | 是 | 否 | retry/fail |
| ARTIFACT_WRITE_FAILED | 产物写入失败 | 是 | 否 | fail/compensate |
| TASK_CONFLICT | 任务并发冲突 | 是 | 否 | retry command |
| TASK_CANCELLED | 任务已取消 | 否 | 否 | stop |
| HANDOFF_DENIED | 交接被拒绝 | 否 | 否 | audit + fail |
| HANDOFF_CONTEXT_TOO_LARGE | 交接上下文过大 | 是 | 是 | compress |
| POLICY_VERSION_CONFLICT | 策略版本冲突 | 否 | 否 | reject |
```

---

## 23. 事务、并发和幂等

### 23.1 Task 更新

Task 更新必须使用乐观锁。

```sql
UPDATE tasks
SET status = $1, version = version + 1, updated_at = now()
WHERE task_id = $2 AND version = $3;
```

影响行数为 0 时返回：

```text
TASK_CONFLICT
```

### 23.2 TaskEvent append

TaskEvent append 与 Task 状态更新应在同一事务中完成。

```text
BEGIN
  INSERT task_events
  UPDATE tasks SET status = ...
COMMIT
```

如果 Task 状态更新失败，TaskEvent 也不能写入。

### 23.3 ToolCall 幂等

每个 ToolCall 必须有：

```text
idempotencyKey = hash(runId + stepId + toolName + normalizedArgs)
```

重复请求命中同一个 idempotencyKey 时：

```text
如果已有 ToolResult，直接返回已有结果。
如果 ToolCall 正在执行，返回 pending / conflict。
```

### 23.4 Artifact 写入失败

如果工具已经产生外部副作用，但 Artifact 写入失败：

```text
1. ToolResult 标记 failed。
2. 写 Trace。
3. 写 Audit，说明外部副作用可能已发生。
4. 触发补偿或人工检查。
```

### 23.5 Handoff 事务

创建 Handoff 时应在同一事务中完成：

```text
1. INSERT agent_handoffs
2. INSERT handoff_context_packages
3. INSERT child task 或记录待创建 child task event
4. INSERT task_event: handoff.created
```

如果 child task 创建失败：

```text
handoff.status = failed
task_event = handoff.failed
audit = handoff_failed
```

### 23.6 发布事务

AgentPackage publish 必须保证：

```text
1. agent_package_versions 写入成功。
2. agent_definitions 写入成功。
3. skill index 更新成功。
4. release audit 写入成功。
```

如果 index 更新失败：

```text
发布状态不得进入 stable。
```

---

## 24. Policy 执行规格

### 24.1 ToolPolicyDecision

```go
type PolicyDecision struct {
    Decision string `json:"decision"` // allowed | denied | approval_required
    Reason string `json:"reason,omitempty"`
    RiskLevel RiskLevel `json:"risk_level"`
    AppliedPolicyIDs []string `json:"applied_policy_ids,omitempty"`
}
```

### 24.2 ToolCall Policy 流程

```text
1. 校验 ToolDefinition 是否存在。
2. 校验工具是否在 Agent allowedToolIds。
3. 校验工具是否被 deniedToolIds 禁止。
4. 校验 caller 权限。
5. 校验 tenant policy。
6. 校验 riskLevel 是否需要审批。
7. 校验参数是否越界。
8. 返回 allowed / denied / approval_required。
9. 写 Audit。
```

### 24.3 HandoffPolicy 流程

```text
1. 校验 fromAgent 是否允许 delegate。
2. 校验 toAgent 是否存在。
3. 校验 toAgent capability 是否匹配。
4. 校验是否跨租户。
5. 校验 ArtifactRef 是否可传递。
6. 校验 MemoryRef 是否可读取。
7. 校验 HandoffMode 是否允许。
8. 如需审批，返回 approval_required。
9. 写 Audit。
```

---

## 25. Decision Loop 实施流程

### 25.1 主循环

```text
1. 创建 AgentRun。
2. 加载 AgentDefinition。
3. 钉住版本快照。
4. 创建或恢复 Task。
5. 构建 WorkView。
6. 构建 PromptBundle。
7. 调用 model-runtime。
8. decision-engine 解析 Decision。
9. decision-engine 校验 Decision。
10. runtime-kernel 分发 Decision。
11. reply → 完成。
12. ask_clarification → waiting_input。
13. tool_call → tool-runtime。
14. unsupported / error → 终止。
15. tool_result → TaskEvent / Artifact / WorkView 更新。
16. 判断是否继续下一轮。
```

### 25.2 终止条件

```text
Decision.type = reply
Decision.type = unsupported
Decision.type = error
RunStatus = waiting_input
RunStatus = waiting_approval
MaxSteps exceeded
MaxToolCalls exceeded
MaxDuration exceeded
Task cancelled
```

---

## 26. Tool Runtime 实施流程

```text
1. 接收 ToolCall。
2. 查 ToolDefinition。
3. 校验 input schema。
4. 调用 policy-engine。
5. 如果 denied：返回 ToolResult denied，写 Audit。
6. 如果 approval_required：Task 进入 waiting_approval。
7. Resolve ExecutionDomain。
8. 执行 ToolExecutor。
9. 校验 output schema。
10. 保存 ToolResult。
11. 保存 ArtifactRef。
12. 写 TaskEvent。
13. 写 Trace / Audit。
```

---

## 27. PromptBundle 实施规格

### 27.1 ToolCard 渲染

```text
工具名：{name}
说明：{description}
适用场景：{whenToUse}
风险等级：{riskLevel}
输入摘要：{inputSchema summary}
```

### 27.2 SkillInstruction 渲染

```text
技能：{skillName}
适用场景：{whenToUse}
方法：
{SKILL.md content}
约束：
{constraints}
输出要求：
{outputRequirements}
```

### 27.3 ArtifactRef 渲染

```text
产物引用：
- ID: artifact_xxx
- 类型: report
- 摘要: xxx
- 可读取方式: 通过工具读取，不直接展示完整内容
```

### 27.4 HandoffContext 渲染

```text
这是来自另一个 Agent 的任务交接。
上游 Agent：{fromAgentId}
任务目标：{objective}
交接原因：{reason}
关键事实：
- ...
约束：
- ...
可用产物：
- ArtifactRef ...
你必须基于自己的能力重新判断，不要盲从上游 Agent 的推理。
```

---

## 28. Eval Suite 规格

### 28.1 EvalCase

```yaml
name: csv_anomaly_report
input: "分析这个 CSV，找异常值并生成报告"
artifacts:
  - artifact_id: sample_csv
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
  maxToolCalls: 8
  shouldEndStatus: completed
```

### 28.2 EvalResult

```go
type EvalResult struct {
    EvalRunID string `json:"eval_run_id"`
    CaseName string `json:"case_name"`
    Passed bool `json:"passed"`
    Failures []string `json:"failures,omitempty"`

    RunID AgentRunID `json:"run_id"`
    TraceID TraceID `json:"trace_id"`

    CreatedAt time.Time `json:"created_at"`
}
```

### 28.3 发布门槛

建议规则：

```text
critical eval 全部通过。
普通 eval 通过率 >= 95%。
安全 eval 必须全部通过。
工具误调用率低于阈值。
```

---

## 29. CollaborationProvider / Array Bridge 规格

### 29.1 CollaborationProvider

```go
type CollaborationProvider interface {
    GetTask(ctx context.Context, ref ExternalTaskRef) (*ExternalTaskSummary, error)
    GetParticipants(ctx context.Context, ref ExternalTaskRef) ([]ParticipantSummary, error)
    SendMessage(ctx context.Context, req SendExternalMessageRequest) error
    AttachArtifact(ctx context.Context, req AttachArtifactRequest) error
    CheckAccess(ctx context.Context, req CollaborationAccessRequest) (*AccessDecision, error)
}
```

### 29.2 Array 事件映射

| Array 事件 | Clean Core 行为 |
|---|---|
| task.invited | 构造 AgentEnvelope，执行 agent.run 或 task.start |
| task.message + @agent | 构造 AgentEnvelope，执行 agent.run 或 task.command |
| task.attachment.uploaded | 映射为 ArtifactRef |
| task.cancelled | task.command cancel |
| task.message approval | task.command approve_action / reject_action |

### 29.3 Clean Core 回写 Array

| Clean Core 事件 | Array 回写 |
|---|---|
| reply.answer | task message |
| waiting_input | task message，询问用户 |
| waiting_approval | task message，等待审批 |
| artifact.created | task attachment |
| handoff.created | 添加参与者 / 创建子任务 / 发 @mention |
| run.failed | task trace message |

规则：

```text
Array message 不替代 TaskEvent。
Array attachment 不替代 Artifact。
Array task status 不替代 CoreTask status。
```

---

## 30. 测试规格

### 30.1 单元测试

每个模块必须覆盖：

```text
正常路径
非法输入
权限拒绝
状态转换拒绝
并发冲突
幂等重复请求
```

### 30.2 状态机测试

必须覆盖：

```text
created → running → completed
running → waiting_input → running
running → waiting_approval → running
running → failed
running → cancelled
handoff created → running → completed
handoff denied
plan replan
```

### 30.3 链路测试

必须覆盖：

```text
普通 reply
多轮 tool_call
tool_call repair
tool_call approval
TaskPlan 多步骤执行
AgentHandoff
HandoffContextPackage hybrid
external tools.invoke
AgentPackage publish
Eval run
Array Bridge event mapping
```

### 30.4 安全边界测试

必须覆盖：

```text
外部无法调用 private tool。
Agent 无法绕过 ToolPolicy。
Agent 无法发布自己的 Prompt 修改。
full_context handoff 被策略禁止时失败。
敏感 Artifact 交接需要审批。
Prompt injection 不覆盖 system 指令。
```

---

## 31. 开发交付清单

开发人员完成一个模块时，必须提交：

```text
1. interface 定义。
2. domain struct。
3. repository 或 storage adapter。
4. unit tests。
5. error codes。
6. trace / audit points。
7. integration test sample。
8. README 或 package doc。
```

核心链路交付时，必须能跑通：

```text
AgentEnvelope
  ↓
AgentRun
  ↓
WorkView
  ↓
PromptBundle
  ↓
Decision
  ↓
ToolCall
  ↓
ToolPolicy
  ↓
ToolResult
  ↓
TaskEvent
  ↓
Trace/Audit
  ↓
Response
```

---

## 32. 最终说明

本实施规格不是替代架构文档，而是架构文档的工程落地版。

开发时优先级：

```text
1. 遵守模块边界。
2. 遵守事实源规则。
3. 遵守状态机。
4. 遵守 Policy 必经链路。
5. 遵守 Trace / Audit 必写点。
6. 遵守版本钉住。
7. 遵守外部协作只通过 CollaborationContext / ExternalTaskBinding 接入。
```

一句话：

```text
架构文档告诉我们为什么这样拆；
实施规格告诉开发人员具体怎么写。
```
