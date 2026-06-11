# 原智能体 CleanCore 真实测试与上线验收开发计划 v0.1

## 1. 背景

CleanCore 当前已经具备基础代码测试、AgentPackage 发布流、Eval Suite、Readiness、Go/No-Go、真实模型接入能力。最近一次 DeepSeek 真实模型验证已经证明：协调智能体可以通过 AgentPackage 配置发布，并在真实模型下完成 `agent.run`、Eval、canary、stable、go/no-go 闭环。

但“能跑通一次”不等于“可以稳定上线”。后续需要把真实测试固化成可重复执行的开发任务，覆盖代码、接口、配置、真实模型、真实数据库、发布门禁、观测证据和回滚路径，避免上线时出现“文档有但代码没接”“代码有但 API 没暴露”“测试只测 stub 不测真实模型”“memory 通过但 Postgres 不通过”等问题。

## 2. 总目标

建立一套可重复、可审计、可扩展的真实测试与上线验收体系。

核心标准：

1. 使用真实部署方式启动服务。
2. 使用真实 Postgres 验证 migration、readiness 和持久化。
3. 使用真实大模型验证 PromptBundle、决策 JSON、Eval 和边界行为。
4. 使用黑盒 HTTP API 覆盖 AgentPackage、Eval、Release、Runtime、Trace。
5. 每个核心能力都能在“代码、文档、API、测试、Trace 证据”之间闭环。
6. 上线前必须具备 go/no-go、canary、stable、rollback 的完整证据。

## 3. 测试分层设计

### 3.1 代码健康层

目的：发现编译、格式、静态检查、基础逻辑、并发和依赖漏洞问题。

固定命令：

```powershell
gofmt -w ./cmd ./internal ./pkg
go vet ./...
go test ./... -count=1
go test ./... -race -count=1
go test ./... -coverprofile coverage.out
```

建议补充：

```powershell
staticcheck ./...
govulncheck ./...
```

验收标准：

1. 所有命令通过。
2. 关键包覆盖率不能明显下降。
3. 不允许出现高危漏洞依赖。
4. 不允许出现未使用、不可达、明显冗余的关键代码路径。

### 3.2 架构契约层

目的：防止文档、OpenAPI、server command、配置项和测试用例互相漂移。

需要检查：

1. `docs/openapi.clean-core.v1.json` 中声明的 command 必须在 server allowlist 中存在。
2. server 支持的 command 必须在 OpenAPI 中声明。
3. 每个管理命令至少有一个 server 黑盒或 E2E 测试。
4. `config.example.json`、env、docker-compose、Helm values、ConfigMap/Secret 的配置项必须保持一致。
5. 每个核心模块必须能映射到至少一个 E2E 场景。

建议新增脚本：

```text
scripts/verify_contracts.ps1
```

验收标准：

1. command 枚举无缺失。
2. 配置项无缺失。
3. OpenAPI 与实现同步。
4. 新增 command 时必须同步文档和测试。

### 3.3 黑盒 API E2E 层

目的：不直接调用 Go 内部函数，而是像真实客户端一样通过 HTTP 调用系统。

基础链路：

1. 启动真实服务。
2. 调用 `/healthz`。
3. 调用 `/readyz`。
4. 创建 AgentPackage draft。
5. patch system/developer prompt。
6. validate draft。
7. publish package。
8. canary package。
9. 创建 Eval suite。
10. 执行 Eval run。
11. 查询 Eval result。
12. 标记 stable。
13. 执行 `agent.run`。
14. 查询 trace、run、task。
15. 调用 `/v1/release/go-no-go`。
16. rollback package。
17. 验证 rollback 后运行门禁生效。

建议新增脚本：

```text
scripts/e2e_api_smoke.ps1
```

验收标准：

1. 全链路通过。
2. 所有响应为 UTF-8 JSON。
3. 所有关键资源 ID 能被后续查询使用。
4. Trace 中存在关键事件。
5. rollback 后不能继续运行被回滚版本。

### 3.4 真实模型 Eval 层

目的：验证真实 LLM 在 CleanCore PromptBundle 和 Decision Contract 下是否稳定执行协议。

模型配置通过 env 或 Secret 注入，不能写入仓库：

```powershell
. .\local.deepseek.env.ps1
```

固定评测维度：

1. 能力介绍：模型应说明自己是协调智能体。
2. 能力边界：没有工具时不能假装能处理业务。
3. 假工具诱导：不能调用不存在的工具。
4. Prompt injection：不能泄露 system/developer prompt。
5. 外部联网诱导：没有联网工具时应明确能力不可用。
6. 缺参数任务：需要澄清时应澄清。
7. 多轮上下文：能使用已有 task/context。
8. 决策 JSON：输出必须能被 parser 和 validator 接受。
9. repair 链路：非法输出应触发修复，不应直接崩溃。
10. 中文稳定性：不能乱码，不能输出内部字段。

建议 Eval suite 规模：

1. P0 smoke：5 条，开发时快速跑。
2. P1 release：30 条，上线前必须跑。
3. P2 regression：50 条以上，每日或每周跑。

验收标准：

1. P0 smoke pass rate = 100%。
2. P1 release pass rate >= 95%。
3. safety case 必须 100%。
4. critical case 必须 100%。
5. tool misuse rate = 0。
6. 每次 Eval 必须保存 final reply、tool calls、tool results、artifact refs 和 trace。

### 3.5 真实数据库与 Migration 层

目的：验证 memory repo 之外的真实持久化能力。

测试内容：

1. 使用 Postgres 启动服务。
2. 执行 migration status。
3. 执行 migration up。
4. 重复执行 migration up，验证幂等。
5. 校验 live schema readiness。
6. 校验关键表、NOT NULL 字段、索引。
7. 执行 AgentPackage、Eval、Run、Trace、Audit 全链路。
8. 重启服务后验证数据仍可查询。

建议新增脚本：

```text
scripts/e2e_postgres_release.ps1
```

验收标准：

1. migration status ready。
2. `/readyz` ready。
3. `/v1/readiness/report` ready 或可解释 degraded。
4. 所有核心数据真实落库。
5. 服务重启后关键数据不丢失。

### 3.6 故障与稳定性层

目的：验证异常情况下系统不会乱调用工具、不会跳过策略、不会丢失审计证据。

需要模拟：

1. 模型超时。
2. 模型 429。
3. 模型 5xx。
4. 模型返回非 JSON。
5. 模型返回非法 tool call。
6. 工具执行失败。
7. 工具连续失败。
8. 重复请求 idempotency。
9. 并发多个 `agent.run`。
10. release switch 禁用 agent/tool/handoff/external invoke。
11. canary routing 稳定性。
12. rollback 后运行门禁。

验收标准：

1. 可重试错误按策略重试。
2. 不可重试错误快速失败并保留 trace。
3. 非法 decision 能进入 repair 链路。
4. 工具失败不会绕过 policy。
5. release switch 生效。
6. 所有失败都可被 trace、audit 或 metrics 观察。

## 4. 能力覆盖矩阵

后续需要维护一张能力覆盖矩阵，用来判断是否存在“代码实现了但真实系统没有用上”的情况。

| 能力 | 代码模块 | API | 文档 | 单测 | E2E | 真实模型 Eval | Trace 证据 | 状态 |
|---|---|---|---|---|---|---|---|---|
| AgentPackage draft/publish | `internal/agentdef/package` | command API | OpenAPI/设计文档 | 已有 | 待固化脚本 | 部分覆盖 | audit/trace | 待完善 |
| PromptBundle 构建 | `internal/context/promptbundle` | runtime 间接触发 | PromptBundle 文档 | 已有 | 已部分覆盖 | 已部分覆盖 | `promptbundle.built` | 待扩展 |
| Model Client | `internal/model/client` | runtime 间接触发 | 配置文档 | 已有 | 已部分覆盖 | 已接 DeepSeek | `model.called/model.completed` | 待固化 |
| Decision Validator | `internal/decision/validator` | runtime 间接触发 | contract 文档 | 已有 | 已部分覆盖 | 已部分覆盖 | `decision.validated` | 待扩展 |
| Eval Suite | `internal/eval` | command API | OpenAPI/设计文档 | 已有 | 已部分覆盖 | 已真实执行 | `eval.*` | 待扩展 |
| Release Gate | `internal/policy/engine`, `internal/release` | command/API | runbook | 已有 | 已部分覆盖 | 间接覆盖 | go/no-go | 待固化 |
| Postgres 持久化 | `internal/storage/postgres` | 全链路间接触发 | migration 文档 | 类型测试 | 待补 | 不适用 | DB/readiness | 待补 |
| Handoff | `internal/task/handoff` | command/API | 设计文档 | 已有 | 已有 | 待扩展 | handoff trace | 待扩展 |
| Tool Runtime | `internal/tool/runtime` | tools.invoke/runtime | 设计文档 | 已有 | 已部分覆盖 | 待扩展 | tool trace | 待扩展 |
| Governance Trace | `internal/governance/trace` | query/API | runbook | 已有 | 已部分覆盖 | 已部分覆盖 | trace query | 待固化 |

## 5. 开发任务拆解

### P0：固化真实模型 Smoke 测试

目标：把当前手工 DeepSeek 测试变成可重复脚本。

任务：

1. 新增 `scripts/e2e_deepseek_smoke.ps1`。
2. 从 env 读取模型配置，不写死 key。
3. 自动启动服务到空闲端口。
4. 自动发布 origin coordinator AgentPackage。
5. 自动创建 5 条 P0 Eval case。
6. 自动执行 Eval run。
7. 自动判断 pass rate、tool misuse rate、go/no-go。
8. 自动停止服务。
9. 输出 JSON 测试报告到 `tmp/e2e/`。

验收：

1. 脚本一条命令可执行。
2. 不泄露 key。
3. 失败时返回非 0 exit code。
4. 通过时输出 package_version_id、eval_run_id、pass_rate、go_no_go decision。

### P1：补黑盒 API E2E

目标：验证真实 HTTP API，而不是只依赖 Go 内部测试。

任务：

1. 新增 `scripts/e2e_api_smoke.ps1`。
2. 覆盖 package draft/prompt patch/validate/publish/canary/eval/stable/run/rollback。
3. 查询 trace、run、task、eval result。
4. 验证 UTF-8 响应。
5. 验证 rollback 后运行门禁。

验收：

1. 全链路通过。
2. 关键响应字段完整。
3. Trace 事件顺序合理。
4. rollback 行为正确。

### P2：补 Postgres 真实持久化 E2E

目标：验证上线数据库路径。

任务：

1. 新增 `scripts/e2e_postgres_release.ps1`。
2. 支持读取 `CLEAN_CORE_DATABASE_URL`。
3. 执行 migration status/up/status。
4. 启动服务并跑 API E2E。
5. 重启服务后查询 package/eval/run/trace。
6. 输出持久化验证报告。

验收：

1. migration readiness 通过。
2. 服务重启后数据存在。
3. go/no-go 为 go。

### P3：补契约一致性检查

目标：防止 API、配置、文档和代码漂移。

任务：

1. 新增 `scripts/verify_contracts.ps1`。
2. 检查 OpenAPI command enum 与 server allowlist。
3. 检查 config.example、docker-compose、Helm values、ConfigMap/Secret 配置项。
4. 检查关键 command 是否有测试名称引用。
5. 将检查接入 Makefile。

验收：

1. 本地一条命令可运行。
2. 漏 command 或漏配置时失败。
3. 输出清晰的差异列表。

### P4：扩充真实模型 Eval Suite

目标：从 5 条 smoke 扩到可上线使用的 30-50 条。

任务：

1. 新增 P0/P1/P2 三档 Eval case 定义。
2. 覆盖 prompt injection、假工具、联网诱导、业务越界、缺参数、多轮、中文稳定性。
3. 每条 case 标记 `critical`、`safety`、普通等级。
4. 支持 final reply contains/not contains。
5. 支持 expected final status。
6. 支持 expected/no tool calls。

验收：

1. P0 100% 通过。
2. P1 pass rate >= 95%。
3. safety/critical 100% 通过。
4. tool misuse rate = 0。

### P5：补故障注入和稳定性测试

目标：验证异常条件下的真实运行质量。

任务：

1. 增加 fake model server，用于返回 timeout、429、5xx、bad JSON、bad tool call。
2. 增加并发 `agent.run` 测试。
3. 增加 idempotency 测试。
4. 增加 release switch 测试。
5. 增加 canary routing 分布测试。

验收：

1. 错误分类正确。
2. retry/repair 生效。
3. 并发无数据污染。
4. release switch 生效。
5. trace/audit/metrics 可观察。

### P6：形成上线验收命令

目标：把所有检查聚合成一个 release candidate 命令。

建议命令：

```powershell
.\scripts\release_candidate_check.ps1
```

执行内容：

1. fmt/vet/test/race。
2. contract verification。
3. API E2E。
4. Postgres E2E。
5. real model Eval。
6. readiness。
7. go/no-go。
8. 输出 release candidate report。

验收：

1. 通过即具备上线候选资格。
2. 失败时能定位到具体层级和 case。
3. 报告可被人工复核。

## 6. 推荐上线门槛

上线前必须满足：

1. `go test ./...` 通过。
2. `go test ./... -race` 通过。
3. 契约一致性检查通过。
4. Postgres migration readiness 通过。
5. `/readyz` 为 ready。
6. `/v1/release/go-no-go` 为 go。
7. P0 real model Eval 100% 通过。
8. P1 real model Eval pass rate >= 95%。
9. safety/critical Eval 100% 通过。
10. tool misuse rate = 0。
11. canary/stable/rollback 全链路真实跑过。
12. key 不进入日志、文档、镜像、仓库。
13. 所有关键链路有 trace/audit 证据。

## 7. 当前状态判断

当前已经完成：

1. DeepSeek 真实模型接入验证。
2. 协调智能体 AgentPackage 发布链路验证。
3. P0 级别 Eval smoke 初步验证。
4. PromptBundle 到模型请求的真实渲染修复。
5. 模型参数 env 配置支持。
6. UTF-8 JSON 响应修复。
7. `go test ./...` 通过。
8. go/no-go 初步通过。

当前仍需补齐：

1. 真实测试脚本化。
2. 黑盒 API E2E。
3. Postgres 持久化 E2E。
4. 契约一致性检查。
5. 30-50 条真实模型 Eval suite。
6. 故障注入与并发稳定性测试。
7. release candidate 聚合报告。

## 8. 交付顺序建议

推荐顺序：

1. P0：固化真实模型 Smoke 测试。
2. P1：补黑盒 API E2E。
3. P3：补契约一致性检查。
4. P2：补 Postgres 真实持久化 E2E。
5. P4：扩充真实模型 Eval Suite。
6. P5：补故障注入和稳定性测试。
7. P6：形成上线验收命令。

原因：

1. 先固化已经跑通过的真实模型链路，收益最高。
2. 再补黑盒 API，防止内部测试掩盖接口问题。
3. 同步补契约检查，防止后续继续漂移。
4. Postgres 和故障注入复杂度更高，适合在基础链路稳定后推进。

## 9. 最终产物

预期新增或完善：

1. `scripts/e2e_deepseek_smoke.ps1`
2. `scripts/e2e_api_smoke.ps1`
3. `scripts/e2e_postgres_release.ps1`
4. `scripts/verify_contracts.ps1`
5. `scripts/release_candidate_check.ps1`
6. `docs/真实模型Eval用例集.md`
7. `docs/上线验收报告模板.md`
8. Makefile 增加对应命令。
9. CI 增加 fast/integration/release-candidate 三档流水线。

## 10. 备注

真实模型 key 只允许通过本地 env、部署 Secret 或 CI Secret 注入。测试文档、脚本、日志和报告中不得输出完整 key。

本计划中的真实模型测试默认以 DeepSeek OpenAI-compatible API 为当前目标，但测试框架应保持 provider-neutral，后续可以替换成其他 OpenAI-compatible 模型服务。
