# 原智能体 CleanCore 剩余架构问题修复跟踪 v0.1

依据：

- `docs/原智能体CleanCore全景开发设计文档_v1.2_Clean.md`
- `docs/原智能体CleanCore十三模块架构与代码审查报告_v0.1.md`
- 当前代码审计结论

目标：只跟踪架构文档明确要求但当前仍未完全闭环的问题。每解决一项即标记完成，并记录验证命令。

## 剩余问题清单

- [x] R1 AgentRun VersionSnapshot 需要完整记录 PolicySet version 与 PromptBundle hash。
- [x] R2 ToolRuntime 需要校验 ToolResult / OutputSchema。
- [x] R3 ToolRepairPolicy 需要进入 PolicySet，并驱动工具失败后的 repair 决策。
- [x] R4 `origin.agent.delegate` 需要作为 Clean Core 内部工具注册并经过 ToolRuntime，同时保留 HTTP command 入口。
- [x] R5 Handoff 需要通过 CapabilityDiscovery 校验或选择目标 Agent。
- [x] R6 `task.command(upgrade_agent_version)` 需要实现长任务显式升级流程。
- [x] R7 Governance Metrics / Replay 需要补齐模型、工具、Handoff、PromptBundle hash / Policy version 等运行证据。
- [x] R8 PolicySet 需要具备持久化 Store，并能在 Postgres 模式下加载真实 PolicySet。

## 解决方案

### R1 VersionSnapshot 完整化

方案：

- 在 `VersionSnapshot` 增加 `policy_set_version`。
- `runtime-kernel` 创建 run 时写入 PolicySet version。
- 每次 PromptBundle 构建后回写 `prompt_bundle_hash` 到 AgentRun snapshot。

验收：

- 单元测试能读取 run snapshot 中的 `policy_set_version` 与 `prompt_bundle_hash`。

### R2 ToolResult Validator

方案：

- ToolRuntime 在执行成功后按 `ToolDefinition.OutputSchema` 校验输出。
- 输出不符合 schema 时返回标准失败 `ToolResult`，并记录 `tool.failed`。

验收：

- 单元测试覆盖输出 schema 失败场景。

### R3 ToolRepairPolicy

方案：

- 在 `PolicySet` 增加 `ToolRepairPolicy`。
- PolicyEngine 暴露 `EvaluateRepair`。
- RuntimeKernel 在工具失败/拒绝后根据 ToolRepairPolicy 决定继续、停止或请求模型修复。

验收：

- 单元测试覆盖工具失败后继续 repair loop 与超过限制后失败。

### R4 内部 Handoff 工具

方案：

- 注册 `origin.agent.delegate` 为 private/protected 内部工具。
- ToolRuntime 支持 internal visibility 的 agent runtime 调用。
- HTTP command 入口复用同一套 ToolRuntime invoke 链路，不绕过 ToolPolicy/Audit/Trace。

验收：

- HTTP `origin.agent.delegate` 与模型 decision tool_call 均能创建 Handoff、ChildTask、目标 AgentRun 并回流结果。

### R5 Handoff CapabilityDiscovery

方案：

- 增加 Agent capability resolver。
- `to_agent_id` 为空时按 `capability_query` / objective 选择目标 Agent。
- `to_agent_id` 不为空时校验目标 Agent 能力匹配。

验收：

- 单元测试覆盖按能力选择目标 Agent，以及目标 Agent 不匹配时拒绝。

### R6 upgrade_agent_version

方案：

- `task.command` 支持 `upgrade_agent_version`。
- 校验目标版本属于同租户、同 agent，且 release 状态可运行。
- 更新 Task 的 `agent_version` / `policy_set_id`，追加 audit/task event。
- 后续 AgentRun 使用新版本。

验收：

- 单元测试覆盖升级成功、跨 agent 拒绝、不可运行版本拒绝。

### R7 Governance Metrics / Replay

方案：

- Metrics 统计 model/tool/handoff/replay 关键事件计数。
- Replay 报告输出 PolicySet version、PromptBundle hash、ToolResult 与 Handoff 完整性检查。

验收：

- 单元测试覆盖 replay 报告包含版本和 prompt hash 缺失检查。

### R8 PolicySet Postgres Store

方案：

- 增加 `policy_sets` 表。
- 实现 Postgres PolicyStore。
- Core 在 Postgres 模式下使用真实 PolicyStore，而不是只使用内存默认策略。

验收：

- 接口实现测试覆盖 Postgres PolicyStore 满足 `policyengine.Store`。

## 修复记录

- 2026-05-31：完成 R1。`VersionSnapshot` 增加 `policy_set_version`，runtime 在 PromptBundle 构建后回写 `prompt_bundle_hash`。验证：`go test ./internal/runtime/kernel ./internal/runtime/run`。
- 2026-05-31：完成 R2。ToolRuntime 对成功工具输出执行 `OutputSchema` 校验，失败时返回标准 `ToolResultFailed`。验证：`go test ./internal/tool/runtime`。
- 2026-05-31：完成 R3。`PolicySet` 增加 `ToolRepairPolicy`，runtime-kernel 按修复策略决定工具失败后继续模型修复或终止。验证：`go test ./internal/policy/engine ./internal/runtime/kernel`。
- 2026-05-31：完成 R4。`origin.agent.delegate` 注册为内部工具，HTTP command 与模型 decision tool_call 均经过 ToolRuntime/ToolPolicy/Trace。验证：`go test ./internal/server ./internal/runtime/kernel`。
- 2026-05-31：完成 R5。Handoff 创建输入增加 capability checked/matched 语义，内部 Handoff 工具支持 `capability_query` 校验和默认目标选择。验证：`go test ./internal/task/handoff ./internal/server`。
- 2026-05-31：完成 R6。`task.command(upgrade_agent_version)` 支持同 Agent 可运行版本升级，更新 Task `agent_version` / `policy_set_id` 并写 TaskEvent/Audit；后续 run 使用升级后的版本。验证：`go test ./internal/server`。
- 2026-05-31：完成 R7。Replay 报告输出 PromptBundle hash、Policy version、Tool/Handoff 统计并检查缺失证据；新增 governance metrics trace 汇总。验证：`go test ./internal/governance/replay ./internal/governance/metrics ./internal/server`。
- 2026-05-31：完成 R8。新增 `policy_sets` migration、Postgres `PolicyStore`、readiness schema 检查，并让 Postgres 模式使用持久化 PolicyStore。验证：`go test ./internal/storage/postgres ./internal/storage/migration ./internal/readiness ./internal/app/core`。
- 2026-05-31：全量验证通过：`go test ./...`、`go vet ./...`。
