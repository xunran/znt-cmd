# 原智能体 CleanCore 真实用户验收测试报告 2026-05-31

## 1. 测试目标

本轮测试模拟提示词优化师 / Agent 训练师作为真实用户使用 CleanCore：

1. 不改 Go 代码，只复制并修改 AgentPackage 文件。
2. 从文件化提示词包完成校验、预览、能力查看、版本 diff、实验、API 冒烟、真实模型 Eval。
3. 验证负向场景能被拦截。
4. 验证上线前 RC 门禁能覆盖 Go 测试、契约检查、API smoke、Postgres 持久化、DeepSeek 真实模型 smoke。

## 2. 测试命令

本轮新增并执行了可复跑脚本：

```powershell
powershell -File scripts\real_user_acceptance_test.ps1 `
  -RunRealModel `
  -RunPostgres `
  -EnvFile .\local.deepseek.env.ps1
```

脚本不会修改正式 `agent_packages/origin-coordinator`，会把用户编辑后的临时包复制到 `tmp/e2e/<run>/user-edited-package`。

## 3. 本次结果

状态：通过。

主报告：

```text
tmp/e2e/real-user-acceptance-20260531-160128/real-user-acceptance-report.json
tmp/e2e/real-user-acceptance-20260531-160128/real-user-acceptance-report.md
```

真实模型报告：

```text
tmp/e2e/real-user-acceptance-20260531-160128/deepseek-smoke/deepseek-smoke-report.json
tmp/e2e/real-user-acceptance-20260531-160128/deepseek-smoke/deepseek-eval-result.json
tmp/e2e/real-user-acceptance-20260531-160128/deepseek-smoke/diagnostics.md
```

RC 报告：

```text
tmp/e2e/release-candidate-20260531-160156/
```

## 4. 覆盖步骤

本轮真实用户验收覆盖并通过：

1. `simulate-user-edits`：复制正式包并模拟用户修改 `developer.md`。
2. `package-validate`：包结构、manifest、Eval 文件校验通过。
3. `prompt-lint`：提示词风险检查通过。
4. `prompt-preview`：生成 PromptBundle 预览。
5. `capabilities-show`：生成工具/技能可见性报告。
6. `package-diff`：生成正式包与用户编辑包 diff。
7. `experiment-local`：本地 A/B prompt 实验跑通。
8. `negative-missing-file`：缺少 `developer.md` 被正确拦截。
9. `negative-secret-lint`：提示词中出现疑似 `sk-` secret 被正确拦截。
10. `api-smoke-user-package`：文件化包 API smoke 通过。
11. `real-model-smoke-user-package`：DeepSeek 真实模型 smoke 通过。
12. `postgres-start`：Docker Postgres 启动并健康。
13. `release-candidate-user-package`：完整 RC 通过。

## 5. 真实模型结果

DeepSeek OpenAI-compatible 真实模型 smoke：

```text
status: passed
model_base_url: https://api.deepseek.com
model_name: deepseek-v4-flash
agent: origin-coordinator@v1
eval_file: tmp/e2e/real-user-acceptance-20260531-160128/user-edited-package/eval/smoke.yaml
eval_passed: true
eval_pass_rate: 1
eval_tool_misuse_rate: 0
go_no_go: go
```

诊断报告生成成功，失败数为 0。

## 6. RC 门禁结果

完整 RC 通过，包含：

1. `gofmt-check`
2. `go-vet`
3. `go-test`
4. `contract-verification`
5. `package-validate`
6. `prompt-lint`
7. `prompt-preview`
8. `capabilities-show`
9. `api-smoke`
10. `postgres-release`
11. `real-model-smoke`

Postgres release E2E 通过，说明 migration、持久化 eval、trace、重启后读取等上线关键路径可用。

## 7. 结论

当前 CleanCore 对“原智能体协调智能体”已经具备可真实使用的提示词优化师工作流：

1. 用户可直接改 `agent_packages/origin-coordinator/*.md` 和 `eval/*.yaml`。
2. 不需要改 Go 代码即可完成校验、预览、真实模型测试、诊断和 RC。
3. 高风险负向场景能被拦截。
4. 真实模型和 Postgres 上线形态均已跑通。

## 8. 剩余风险

仍建议后续增强：

1. PromptBundle preview 当前是脚本级预览，后续应暴露正式内核 preview API，确保 hash 与真实 run 逐字节一致。
2. diagnostics 当前主要基于 Eval result，后续可自动聚合 trace raw decision、model provider/name、prompt_bundle_hash。
3. capabilities show 当前展示包声明层，后续可接入 runtime tool registry，显示 tool schema、risk level、execution profile。
4. 当前 Eval 仍是 smoke/release 基础集，后续应继续扩充多轮对话、长上下文、更多提示注入和边界混淆用例。

## 9. 2026-05-31 16:28 增量复测

本轮在新增服务端 `prompt.preview`、trace-enriched diagnostics、扩展 Eval 边界用例之后，重新执行完整真实用户验收：

```powershell
powershell -File scripts\real_user_acceptance_test.ps1 `
  -RunRealModel `
  -RunPostgres `
  -EnvFile .\local.deepseek.env.ps1
```

结果：

```text
status: passed
report: tmp/e2e/real-user-acceptance-20260531-162848
real_model: passed
postgres_release: passed
contract_verification: passed
```

同时单独确认：

```text
go test ./...: passed
package_validate: passed
prompt_lint: passed
api_smoke with server prompt.preview: passed
deepseek smoke with expanded eval: passed
contract verification: passed
```

本轮发现并修复的问题：

1. `prompt.preview` 已实现但未加入 OpenAPI command enum，已补齐并通过契约验证。
2. 真实模型烟测使用固定 `origin-coordinator@v1`，在复用 Postgres 持久化环境时可能与历史发布版本冲突，已改为测试时生成隔离 AgentID。
3. 混合“提示注入 + 未配置业务执行”场景下，真实模型会安全拒绝但不稳定输出 `capability_not_available`，已通过 AgentPackage 提示词收紧边界规则，扩展 eval 通过。

剩余风险更新：

1. 正式 PromptBundle preview API 第一版已完成；后续要继续验证 preview hash 与真实 run hash 在更多上下文、工具候选、policy 场景下一致。
2. diagnostics 已能自动聚合 trace 中的 prompt hash、模型名和 token；后续可继续补 raw decision、repair attempt diff 和失败归因聚类。
3. capabilities show 仍是包声明层，运行时 tool registry 可视化仍待增强。
4. 仍建议继续扩展多轮对话、长上下文、工具候选变化、canary/rollback 异常流的真实模型 eval。

## 10. 2026-05-31 17:04 旧提示词整合复测

本轮将旧运行时提示词中适合“原智能体协调智能体”的通用规则整合进新架构 AgentPackage，并重新执行全面真实测试。

整合范围：

```text
agent_packages/origin-coordinator/system.md
agent_packages/origin-coordinator/developer.md
agent_packages/origin-coordinator/prompt.md
agent_packages/origin-coordinator/agents.md
agent_packages/origin-coordinator/eval/smoke.yaml
agent_packages/origin-coordinator/eval/release.yaml
```

整合原则：

```text
1. 保留协调身份、任务流、低信息接话、记忆/上下文新近性、工具/智能体不编造、用户可见回复风格。
2. 不迁入提钱罐融资、商家测额、审批助手等专业业务能力。
3. 不恢复旧 delegate_agent / schedule_wait DecisionType；新架构仍使用 tool_call / unsupported / ask_clarification 等统一契约。
4. 业务能力必须通过独立 AgentPackage / Skill / ToolBinding 显式接入。
```

最终验证结果：

```text
status: passed
go test ./...: passed
contract_verification: passed
package_validate: passed
prompt_lint: passed
api_smoke: passed
DeepSeek smoke eval: passed
DeepSeek release eval: passed
real_user_acceptance: passed
```

最新完整验收报告：

```text
tmp/e2e/real-user-acceptance-20260531-170448
```

本轮真实模型修正点：

```text
1. 能力介绍必须稳定包含“协调”。
2. 用户声称工具/业务系统已注册时，未检索到仍按 capability_not_available 处理。
3. 业务智能体配置目标不清时进入 ask_clarification。
4. “啥啊/？”等低信息消息使用 reply 短接，不进入旧业务上下文或澄清态。
```

## 11. 2026-05-31 17:21 中文提示词复测

本轮将 `agent_packages/origin-coordinator/*.md` 的可编辑提示词正文中文化，保留必要机器契约字段英文，例如 `Decision JSON`、`reply`、`ask_clarification`、`tool_call`、`unsupported`、`capability_not_available`、`origin.agent.delegate`、`PromptBundle`。

验证结果：

```text
status: passed
package_validate: passed
prompt_lint: passed
prompt_preview: passed
DeepSeek smoke eval: passed
DeepSeek release eval: passed
real_user_acceptance: passed
```

最新完整验收报告：

```text
tmp/e2e/real-user-acceptance-20260531-172120
```

## 12. 2026-05-31 17:57 高保真中文提示词 v2 复测

本轮针对提示词“提炼不够精准”的问题，重新按旧 Origin runtime 的原智能体模板做高保真迁移，但仍只保留协调智能体通用能力，不迁入提钱罐融资、商家测额、审批助手等专业业务执行规则。

重构范围：

```text
agent_packages/origin-coordinator/system.md
agent_packages/origin-coordinator/developer.md
agent_packages/origin-coordinator/prompt.md
agent_packages/origin-coordinator/agents.md
agent_packages/origin-coordinator/eval/smoke.yaml
agent_packages/origin-coordinator/eval/release.yaml
```

本轮新增/强化的行为：

```text
1. system.md 聚焦 Decision JSON 硬契约、旧类型映射、能力边界和安全底线。
2. developer.md 聚焦接话路由、上下文优先级、工具/Agent 不编造、专业业务边界、业务智能体配置分类。
3. prompt.md 聚焦原智能体协调身份和短决策流程。
4. agents.md 明确为提示词优化师可读的包说明，不再误导为主要运行时提示词。
5. eval 新增配置流程咨询、纠正上一轮表达、专业业务边界等真实模型用例。
```

真实模型修正点：

```text
1. 混合提示注入 + 不支持业务执行：强制输出 unsupported/capability_not_available。
2. “帮我安排一下”这类笼统行动请求：强制进入 ask_clarification，而不是普通 reply。
3. 业务智能体配置流程咨询：可见回复必须稳定包含“协调”。
```

最终验证结果：

```text
status: passed
package_validate: passed
prompt_lint: passed
prompt_preview: passed
go test ./...: passed
contract_verification: passed
DeepSeek smoke eval: passed
DeepSeek release eval: passed
real_user_acceptance with real model + Postgres + RC: passed
```

报告路径：

```text
tmp/deepseek-smoke-prompt-v2d
tmp/deepseek-release-prompt-v2d
tmp/e2e/real-user-acceptance-prompt-v2d
```

真实用户验收关键结果：

```text
real_user_acceptance status: passed
real_model_smoke_user_package: passed
postgres_start: passed
release_candidate_user_package: passed
```

结论：当前提示词 v2 已能让提示词优化师在不改 Go 代码的情况下维护原智能体协调智能体，并能通过真实模型、用户编辑包、Postgres 和上线 RC 门禁。
