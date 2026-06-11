# 原智能体 CleanCore 提示词优化师工作流九步开发文档 v0.1

## 1. 目标

本任务的目标是把 CleanCore 从“工程师能配置、能测试”推进到“提示词优化师和 Agent 训练师可以日常使用、迭代、验证、发布”的状态。

核心原则不变：

1. Agent 仍然通过 AgentPackage 配置，不为单个 Agent 写死业务代码。
2. Runtime 仍然按通用内核运行，不把专业业务能力塞进协调智能体。
3. 真实模型 key 只通过 env/secret 注入，不进入仓库文件。
4. Eval、Release Gate、Trace/Audit 继续作为上线门禁。

## 2. 已落地入口

### AgentPackage 文件

当前协调智能体提示词在：

```text
agent_packages/origin-coordinator/
  package.yaml
  prompt.md
  system.md
  developer.md
  agents.md
  eval/
    smoke.yaml
    release.yaml
```

提示词优化师主要改：

1. `prompt.md`：身份、角色、整体行为边界。
2. `system.md`：输出协议、运行时硬约束。
3. `developer.md`：协调策略、工具使用原则、拒绝和澄清策略。
4. `agents.md`：面向包的人类说明。
5. `eval/smoke.yaml`、`eval/release.yaml`：真实模型回归用例。

### 常用命令

```powershell
powershell -File scripts\package_validate.ps1 `
  -PackageDir agent_packages\origin-coordinator

powershell -File scripts\prompt_lint.ps1 `
  -PackageDir agent_packages\origin-coordinator

powershell -File scripts\prompt_preview.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -Input "你能做什么？" `
  -OutputPath tmp\prompt-preview.md

powershell -File scripts\e2e_deepseek_smoke.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -EnvFile .\local.deepseek.env.ps1
```

## 3. 九步实现状态

### Step 1：AgentPackage 文件化

状态：已实现。

实现内容：

1. 新增 `agent_packages/origin-coordinator/` 示例包。
2. `scripts/e2e_common.ps1` 新增 `Read-AgentPackage`，支持从 `package.yaml` 和 md 文件读取包定义。
3. `Publish-OriginCoordinatorPackage` 支持 `-PackageDir`，发布时把文件内容写入 draft、system prompt、developer prompt、agents.md 和 metadata。
4. 保留旧硬编码 fallback，避免原 API/Postgres 冒烟脚本被打断。

验收：

```powershell
powershell -File scripts\package_validate.ps1 `
  -PackageDir agent_packages\origin-coordinator
```

### Step 2：CLI/脚本发布工具

状态：已实现 PowerShell wrapper，后续可迁入正式 `clean-core` CLI。

实现内容：

1. `scripts/package_validate.ps1`：校验包结构、提示词、Eval 文件。
2. `scripts/package_publish.ps1`：调用现有 `/v1/commands` 完成 publish/canary/stable/rollback。
3. 所有命令只读 env，不输出 API key。

示例：

```powershell
powershell -File scripts\package_publish.ps1 `
  -BaseUrl http://localhost:8080 `
  -PackageDir agent_packages\origin-coordinator `
  -CanaryPercent 25
```

### Step 3：PromptBundle 预览

状态：已实现本地预览。

实现内容：

1. `scripts/prompt_preview.ps1` 生成 markdown/json 预览。
2. 输出 system、developer、task、context、tool bindings、constraints、估算 token 和 prompt bundle hash。
3. 用于模型调用前检查“最终大概喂给模型什么”。

验收：

```powershell
powershell -File scripts\prompt_preview.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -Input "你能做什么？" `
  -OutputPath tmp\prompt-preview.md
```

说明：当前是文件级预览，hash 采用与内核同类的稳定 JSON hash 规则。后续如果要 100% 对齐真实 run 的 PromptBundle，可把内核 PromptBundle builder 暴露成正式 preview API。

### Step 4：Eval 用例文件化

状态：已实现。

实现内容：

1. `eval/smoke.yaml`、`eval/release.yaml` 进入包目录。
2. `Read-OriginEvalSuiteFile` 支持当前受控 YAML 格式。
3. `Invoke-OriginCoordinatorSmokeEval` 支持 `-EvalFile`。
4. `scripts/e2e_deepseek_smoke.ps1` 支持 `-PackageDir` 后自动读取 `eval/smoke.yaml`。
5. 支持 `final_reply_contains`、`final_reply_not_contains`、`must_call_tools`、`should_not_call_tools`、`critical`、`safety`、`category`、`should_end_status`。

验收：

```powershell
powershell -File scripts\e2e_deepseek_smoke.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -EnvFile .\local.deepseek.env.ps1
```

### Step 5：失败诊断报告

状态：已实现基础版。

实现内容：

1. `scripts/eval_diagnostics.ps1` 从 Eval result 生成 `diagnostics.md` 和 `diagnostics.json`。
2. `scripts/e2e_deepseek_smoke.ps1` 在真实模型 Eval 后自动生成诊断报告。
3. 报告包含失败 case、category、trace_id、run_id、final_reply、失败断言、tool misuse 和修复提示。

验收：

```powershell
powershell -File scripts\eval_diagnostics.ps1 `
  -EvalResultPath tmp\e2e\<run>\deepseek-eval-result.json
```

说明：当前诊断基于 Eval result 聚合。后续可以继续增强为自动拉取 trace 里的 raw decision、model provider/name、prompt_bundle_hash。

### Step 6：版本 Diff 与 Eval Compare

状态：已实现基础版。

实现内容：

1. `scripts/package_diff.ps1` 对比两个 AgentPackage 目录下的 prompt、system、developer、agents、package、eval 文件。
2. `scripts/eval_compare.ps1` 对比两次 Eval result 的 pass rate、tool misuse rate 和 case 变化。

验收：

```powershell
powershell -File scripts\package_diff.ps1 `
  -LeftDir agent_packages\origin-coordinator `
  -RightDir agent_packages\origin-coordinator `
  -OutputPath tmp\package-diff.md

powershell -File scripts\eval_compare.ps1 `
  -LeftResultPath tmp\e2e\<run-a>\deepseek-eval-result.json `
  -RightResultPath tmp\e2e\<run-b>\deepseek-eval-result.json `
  -OutputPath tmp\eval-compare.md
```

### Step 7：实验能力

状态：已实现本地实验 runner；真实模型批量实验可通过开关启用。

实现内容：

1. 新增 `experiments/origin-coordinator-boundary.json`。
2. `scripts/experiment_run.ps1` 支持复制基础包、生成 variant、追加 developer prompt、生成 preview、跑 lint。
3. 可选 `-RunRealModel` 对每个 variant 跑真实模型 smoke。
4. 实验版本默认只在 `tmp/e2e` 或指定 `ReportDir` 下生成，不会污染 stable。

验收：

```powershell
powershell -File scripts\experiment_run.ps1 `
  -ExperimentPath experiments\origin-coordinator-boundary.json `
  -ReportDir tmp\experiment-test
```

真实模型实验：

```powershell
powershell -File scripts\experiment_run.ps1 `
  -ExperimentPath experiments\origin-coordinator-boundary.json `
  -ReportDir tmp\experiment-real `
  -RunRealModel `
  -EnvFile .\local.deepseek.env.ps1
```

### Step 8：提示词 Lint

状态：已实现。

实现内容：

1. `scripts/prompt_lint.ps1` 检查 Decision JSON、unsupported/capability_not_available、prompt injection 防护、secret pattern、外部/业务能力承诺与工具绑定一致性、token 风险。
2. `scripts/package_validate.ps1` 会调用 lint。
3. `scripts/release_candidate_check.ps1 -PackageDir ...` 会把 lint 纳入 RC 报告。

验收：

```powershell
powershell -File scripts\prompt_lint.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -ReportPath tmp\prompt-lint.json
```

### Step 9：工具/技能可视化

状态：已实现包级可视化。

实现内容：

1. `scripts/capabilities_show.ps1` 输出当前包声明的 allowed/exposed/denied tools。
2. 扫描本地 `skills/` 目录并输出技能文件摘要。
3. Prompt preview 中也会展示 tool bindings。

验收：

```powershell
powershell -File scripts\capabilities_show.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -OutputPath tmp\capabilities.md
```

说明：当前展示的是 AgentPackage 声明层。后续如果需要完整 tool registry、input schema、output schema、risk level、execution profile，需要新增 registry 查询 API 或正式 CLI 子命令。

## 4. Release Candidate 集成

`scripts/release_candidate_check.ps1` 已支持 `-PackageDir`：

```powershell
powershell -File scripts\release_candidate_check.ps1 `
  -PackageDir agent_packages\origin-coordinator `
  -RunRealModel `
  -DeepSeekEnvFile .\local.deepseek.env.ps1
```

开启 `-PackageDir` 后会额外执行：

1. package validate
2. prompt lint
3. prompt preview
4. capabilities show
5. real-model smoke 使用文件化 AgentPackage 和 `eval/smoke.yaml`

## 5. 当前真实模型验收记录

已用 DeepSeek OpenAI-compatible 接入跑过文件化包真实模型 smoke：

```text
status: passed
agent: origin-coordinator@v1
eval_file: agent_packages/origin-coordinator/eval/smoke.yaml
pass_rate: 1
tool_misuse_rate: 0
go_no_go: go
```

报告位于 `tmp/e2e/deepseek-smoke-*`，每次运行会重新生成。

## 6. 剩余可增强项

当前九步已经可用，但还有几个后续增强方向：

1. 把 PowerShell wrapper 收敛成正式 `clean-core package/prompt/eval/experiment` CLI。
2. 新增正式 PromptBundle preview API，确保 preview hash 与真实 run hash 逐字节一致。
3. 诊断报告自动拉取 trace，补齐 raw decision JSON、model provider/name、prompt_bundle_hash。
4. capabilities show 接入运行时 tool registry，展示 tool schema、risk level、execution profile。
5. experiment runner 支持更多 variant 形式：替换 system/prompt 文件、模型参数矩阵、自动推荐胜出版本。

## 7. 2026-05-31 增量落地记录

本轮继续把提示词优化师工作流从“脚本可用”推进到“服务端真实可验收”：

1. 已新增正式服务端 `prompt.preview` 命令，可以通过 `/v1/commands` 在不发布、不调用模型的情况下预览真实内核构建出的 PromptBundle。
2. `scripts/prompt_preview.ps1` 继续保留本地预览，同时支持通过 `-BaseUrl` 调用服务端预览。
3. API smoke 已纳入服务端 PromptBundle preview，并记录 `server-prompt-preview.json` 与 `prompt_bundle_hash`。
4. Eval diagnostics 已补充 trace 反查能力，可在失败报告中带出 `prompt_bundle_hash`、model provider/name、prompt/completion token 等信息。
5. `eval/smoke.yaml` 和 `eval/release.yaml` 已增加工具伪造、混合提示注入、角色边界混淆等真实用户更容易触发的边界用例。
6. DeepSeek 真实模型烟测已改为使用隔离测试 AgentID，避免复用 Postgres 持久化环境时撞上历史 `agent_id@version`。
7. OpenAPI `AgentEnvelope.command` 已补齐 `prompt.preview`，契约验证重新通过。

最新通过记录：

```text
contract: tmp/e2e/contract-20260531-162824
deepseek smoke: tmp/deepseek-smoke-unique-agent
real user acceptance: tmp/e2e/real-user-acceptance-20260531-162848
```

因此，原“剩余可增强项”中的正式 PromptBundle preview API 与 trace-enriched diagnostics 已经完成第一版闭环。后续重点应转向正式 CLI 化、运行时 tool registry 可视化、多轮对话 eval、长上下文 eval 与更大规模 A/B 实验。
