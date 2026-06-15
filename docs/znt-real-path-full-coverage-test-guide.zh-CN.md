# ZNT / Clean Core 真实路径全覆盖测试指南

日期：2026-06-14

## 1. 目标

本文定义 ZNT / Clean Core 的“真实全面测试”方法。这里的真实测试不是只跑接口存活，也不是只跑 isolated unit test，而是沿产品逻辑走完整路径：

```text
配置/迁移 -> HTTP API -> 控制平面事实源 -> runtime driver -> run/task/tool/model
          -> trace/audit/evidence -> diagnostics/replay/go-no-go
```

每个功能验收都必须至少回答：

- API 是否可调用，鉴权和 tenant 是否正确。
- 控制平面状态是否落库或写入内存事实源。
- runtime 行为是否真实执行到目标组件，而不是 mock 掉主流程。
- trace、audit、usage、diagnostics 是否能复盘。
- 失败路径是否明确、可诊断、不会绕过 policy/approval/credential boundary。
- Postgres 模式与 in-memory 模式是否一致。
- 真实模型路径与 stub 模型路径是否都能解释结果。

## 2. 测试分层

| 层级 | 目的 | 入口 | 必须通过条件 |
| --- | --- | --- | --- |
| L0 静态与单元 | 编译、类型、单元逻辑、合约 | `go test ./internal/...`、`go vet ./...`、`scripts/verify_contracts.ps1` | 无失败、无 schema/command 漏项 |
| L1 本地 stub smoke | 无外部依赖跑通核心链路 | `scripts/e2e_api_smoke.ps1` | agent package -> eval -> stable -> run -> trace/report 通过 |
| L2 全接口覆盖 | OpenAPI 暴露的 HTTP 操作全走一遍 | `scripts/e2e_clean_core_all_interfaces.ps1` | `all-interfaces-report.json.status=passed`，无 missing operations |
| L3 真实业务主路径 | MMR/service/tool/approval/tenant/evidence 组合链路 | `scripts/e2e_clean_core_mmr_service.ps1` | 所有业务步骤通过，trace/audit/go-no-go 有证据 |
| L4 runtime/source 专项 | plugin source、runtime driver、context strategy、async | `e2e_plugin_runtime_smoke.ps1`、`e2e_context_strategy.ps1`、`e2e_clean_core_async_resilience.ps1` | carrier/source/runtime、hook、async、context evidence 正确 |
| L5 Postgres 真实持久化 | migration、repository、重启后事实可恢复 | `e2e_postgres_release.ps1`、`e2e_postgres_upgrade.ps1`、`e2e_postgres_readiness_negative.ps1` | migration ready，release/run/trace/audit 持久化一致 |
| L6 真实模型 | OpenAI-compatible/DeepSeek 等真实模型路径 | `e2e_deepseek_smoke.ps1` | eval pass、tool misuse 为 0、diagnostics 可解释 |
| L7 性能与韧性 | race、并发、延迟、长时间运行 | `go test -race`、`perf_single_node_agent_run.ps1`、`perf_real_model_agent_run.ps1` | 错误率、延迟、吞吐、资源占用满足阈值 |

## 3. 环境准备

### 3.1 基础依赖

- Windows PowerShell。
- Go 工具链可通过 `go` 命令访问。
- 可选：Docker Desktop，用于 Postgres / compose 路径。
- 可选：真实模型 API key，用于 L6。

建议先设置本地 Go cache，避免测试污染用户目录：

```powershell
$env:GOCACHE = Join-Path (Get-Location) ".gocache"
```

### 3.2 服务 token

多数 E2E 脚本会自动设置：

```powershell
$env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
```

如果手动启动服务，HTTP 请求需要携带脚本生成的认证 header。优先使用已有脚本里的 `Get-E2EHeaders` / `Invoke-CleanCoreCommand`，不要手写散落的 header。

### 3.3 真实模型环境

真实模型可以用 `.env` 或 `.ps1` 文件。脚本 `Import-E2EEnvFile` 支持两类格式：

```text
CLEAN_CORE_MODEL_PROVIDER=openai-compatible
CLEAN_CORE_MODEL_BASE_URL=https://api.deepseek.com
CLEAN_CORE_MODEL_API_KEY=...
CLEAN_CORE_MODEL_NAME=...
```

或：

```powershell
$env:CLEAN_CORE_MODEL_PROVIDER = "openai-compatible"
$env:CLEAN_CORE_MODEL_BASE_URL = "https://api.deepseek.com"
$env:CLEAN_CORE_MODEL_API_KEY = "..."
$env:CLEAN_CORE_MODEL_NAME = "..."
```

注意：真实 API key 不应提交到 Git。若本地 `.env` 曾包含真实 key，发布或共享仓库前应确认它没有进入提交历史，并按需要轮换。

### 3.4 Postgres

使用 compose：

```powershell
docker compose up -d postgres
$env:CLEAN_CORE_DATABASE_URL = "postgres://clean_core:clean_core_dev@localhost:5432/clean_core?sslmode=disable"
```

执行迁移：

```powershell
go run ./cmd/clean-core-server -migration up -migration-dir migrations
go run ./cmd/clean-core-server -migration status -migration-dir migrations
```

也可以直接跑 Postgres E2E，脚本会执行 migration status/up/status。

## 4. 推荐执行顺序

### 4.1 快速开发门禁

每次较大改动后先跑：

```powershell
go test ./internal/... -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_contracts.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_api_smoke.ps1
```

验收：

- Go test 全绿。
- contract verification 无缺失 command/schema。
- `tmp/e2e/api-smoke-*/api-smoke-report.json` 状态为 `passed`。

### 4.2 全接口门禁

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_all_interfaces.ps1
```

该脚本会读取 `docs/openapi.clean-core.v1.json`，对每个公开 HTTP operation 注册覆盖。验收：

- `all-interfaces-report.json.status = passed`
- 控制台无 `all-interface coverage missing operations`
- `calls` 中每个关键操作都有实际 concrete path 和状态码。

### 4.3 业务主路径门禁

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_mmr_service.ps1
```

覆盖主链路：

- readiness。
- agent create / prompt edit / skill edit / tool binding。
- service connection create / provider create / provider health / provider sync。
- tool manifest / tool group / active binding。
- agent.run。
- tools.invoke idempotency。
- task.start / task.command。
- high-risk tool approval approve/reject。
- trace / replay / audit / metrics / provider governance。
- release canary / stable / rollback。
- role deny。
- usage evidence。
- bad provider、cross-tenant isolation。
- go-no-go。

验收：

- `clean-core-mmr-service-report.json.status = passed`
- `toolhost-invocations.ndjson` 中不出现 secret 明文。
- cross-tenant 请求被拒绝。
- approval required 不能被伪造字段绕过，只能通过真实 approval id。

### 4.4 Runtime/source 专项门禁

AgentPlugin / runtime smoke：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_plugin_runtime_smoke.ps1
```

Context strategy：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_context_strategy.ps1
```

Async resilience：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_async_resilience.ps1
```

验收重点：

- `agent_plugin_source/managed` 不回落为 native 假路径。
- run、route、diagnostics 包含 `carrier_kind`、`runtime_contract`、`source_kind`、`manifest_hash`。
- context strategy 真实影响 prompt preview / run diagnostics。
- async 模式下 HTTP 不被慢工具阻塞，后台 run 可最终完成，trace 可追。

### 4.5 Postgres 门禁

```powershell
$env:CLEAN_CORE_DATABASE_URL = "postgres://clean_core:clean_core_dev@localhost:5432/clean_core?sslmode=disable"
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_release.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_upgrade.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_readiness_negative.ps1
```

验收重点：

- migration checksum 与 live schema 一致。
- `/readyz` deep readiness 正常。
- release / run / trace / audit / service connection 在 Postgres store 下行为与 in-memory 一致。
- readiness negative 能发现缺失 schema 或不健康 DB，而不是误报 ready。

### 4.6 真实模型门禁

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1 -EnvFile .\.env
```

可选带 package：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1 -EnvFile .\.env -PackageDir <agent-package-dir>
```

验收重点：

- eval suite pass。
- `tool_misuse_rate = 0`。
- `diagnostics.json` 与 `diagnostics.md` 能解释失败或通过原因。
- `agent.run` 最终 `status=completed`。
- trace 中有 model streaming / decision evidence。

### 4.7 发布候选一键门禁

普通发布候选：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1
```

带 Postgres：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1 -RunPostgres
```

带真实模型：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1 -RunRealModel -DeepSeekEnvFile .\.env
```

最严格本地候选：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1 -RunRace -RunPostgres -RunRealModel -DeepSeekEnvFile .\.env -StrictContractTests
```

验收：

- `release-candidate-report.json.status = passed`
- 每个 step 状态为 `passed`。

## 5. 功能覆盖矩阵

| 功能域 | 必测真实路径 | 自动化入口 | 关键证据 |
| --- | --- | --- | --- |
| Health/readiness/version | `/healthz`、`/readyz`、`/v1/readiness/report`、migration readiness | `e2e_api_smoke.ps1`、Postgres E2E | readiness status、migration status、go-no-go |
| Auth/tenant/role | 缺 tenant、角色不匹配、跨 tenant 读 trace/tool/run | server tests、MMR E2E、all interfaces | 403/400、无跨租户数据泄漏 |
| Agent asset | create/list/get/patch/delete | all interfaces、MMR E2E | `/v1/agents` 返回 carrier view |
| Draft/package | draft create/validate/review/publish/proposal | all interfaces、server tests | release、audit、compiled hash |
| Release/canary/stable/rollback | publish -> canary -> eval -> stable -> rollback | api smoke、MMR、Postgres | release status、canary hit、trace evidence |
| Carrier/runtime | native、agent plugin source、unsupported external/workflow | conformance、plugin smoke、server tests | `carrier_kind`、`runtime_contract`、fail closed |
| Agent run | agent.run sync/async、existing task、conversation context | api smoke、async E2E、kernel tests | run ledger、trace、diagnostics、final response |
| Task | task.start、task.command、plan、timeline、recovery | all interfaces、MMR | task events、plan snapshot、recovery report |
| Tool catalog | provider、manifest、group、sync、health | all interfaces、MMR、plugin smoke | provider governance、manifest list、toolhost logs |
| Tool runtime | tools.invoke、idempotency、policy、credential boundary | MMR、tool runtime tests | tool_call/tool_result、trace、approval |
| Service connection | create/test/resources/health/secret rotation | all interfaces、serviceconnection tests | no secret plaintext、resource discovery |
| Runtime hooks | provider/catalog/sync/binding/invoke/governance | all interfaces、runtime hook tests | hook manifests、hook events、approval |
| Policy/approval | release approval、tool approval approve/reject | MMR、server tests | approval id、audit、resume/deny behavior |
| Trace/replay/diagnostics | trace query、replay、run diagnostics、usage evidence | all interfaces、MMR、api smoke | PromptBundle hash、strategy evidence、usage |
| Context/conversation | recent messages、retrieval、compression、source reports | context strategy E2E、kernel/context tests | prompt preview、compression report、diagnostics |
| Knowledge/memory | knowledge base/search、memory scopes | all interfaces、unit tests | search results、permission decisions |
| Handoff/collaboration | origin.agent.delegate、handoff trace、external task lookup | server tests、all interfaces | child task/run、handoff trace |
| Eval | eval.run、suite/result lookup、strategy assertions | api smoke、deepseek smoke、eval tests | pass rate、tool misuse、diagnostics |
| Governance process | templates/runs/gates/conflicts | all interfaces、governance tests | process/gate status、audit |
| Storage/migration | migration checksum/live schema/Postgres repository | Postgres E2E、storage tests | checksum match、live_schema=ready |
| OpenAPI contract | schema matches server operations | all interfaces、verify contracts | no missing operation/schema |
| Performance | single-node and real-model load | perf scripts | latency/throughput/error report |

## 6. 真实路径用例模板

每个新增功能都按以下格式补测试：

```text
用例 ID：
功能域：
前置条件：
真实入口：
操作步骤：
期望业务结果：
期望控制平面事实：
期望 trace/audit/diagnostics：
失败路径：
自动化入口：
报告位置：
```

示例：

```text
用例 ID：RUN-CARRIER-001
功能域：runtime carrier dispatch
前置条件：发布 plugin_service source agent
真实入口：POST /v1/commands agent.run
操作步骤：agent.plugin.sync -> draft publish -> agent.run -> GET /v1/runs/{run_id}/diagnostics
期望业务结果：run completed
期望控制平面事实：run.carrier_kind=agent_plugin_source, runtime_contract=managed
期望 trace/audit/diagnostics：route/strategy diagnostics 包含 source_provider_id 与 manifest_hash
失败路径：未注册 external_runtime 返回 AGENT_RUNTIME_DRIVER_UNAVAILABLE
自动化入口：internal/runtime/conformance、scripts/e2e_plugin_runtime_smoke.ps1
报告位置：tmp/e2e/plugin-runtime-*/plugin-runtime-report.json
```

## 7. 报告检查清单

每个 E2E report dir 至少检查：

- `server.log`：无 panic、无 unexpected 5xx。
- `*-report.json`：`status = passed`。
- `trace` / `diagnostics` 文件：关键 evidence 存在。
- toolhost `.ndjson`：请求真实到达 tool host；没有 secret 明文。
- OpenAPI coverage：无 missing operation。
- go-no-go：关键 gate 通过或 degraded 原因可解释。

建议报告目录命名：

```powershell
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$report = "tmp\e2e\manual-full-$stamp"
New-Item -ItemType Directory -Path $report -Force | Out-Null
```

## 8. 新功能测试补充规则

任何新功能合入前必须满足：

1. 有 unit test 或 package-level test 覆盖核心纯逻辑。
2. 有 server/httptest 覆盖真实 API 或 command path。
3. 如果是公开 `/v1/*` 能力，必须更新 `docs/openapi.clean-core.v1.json`，并让 `e2e_clean_core_all_interfaces.ps1` 覆盖。
4. 如果会写 run/trace/audit/release/tool/service connection，必须断言 evidence。
5. 如果涉及 policy、approval、credential、tenant，必须有拒绝路径测试。
6. 如果涉及 runtime/carrier/source，必须补 `internal/runtime/conformance` 或 source adapter test。
7. 如果涉及 Postgres schema，必须更新 migration checksum，并跑 Postgres E2E。
8. 如果涉及真实模型提示词或决策，必须补 eval case 或真实模型 smoke。

## 9. 当前重点回归建议

刚完成控制平面/runtime 解耦后，优先执行：

```powershell
go test ./internal/... -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_contracts.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_api_smoke.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_plugin_runtime_smoke.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_context_strategy.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_all_interfaces.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_mmr_service.ps1
```

如果本机有 Postgres：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_release.ps1
```

如果要证明真实模型路径：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1 -EnvFile .\.env
```

## 10. 已知边界与待补强

- PloyKit UI 不在当前仓库内，前端页面真实点击测试需要在 `D:\code2\znt\ploykit\modules\origin-agentops` 另建 Playwright/E2E 门禁。
- `external_runtime connected/observed` 当前是 contract 草案和 fail-closed，不应写成已产品化通过。
- 第三方真实 service connection 需要 sandbox host 或测试账号，不能用生产系统直接做破坏性工具调用。
- 性能阈值需要按部署目标补数值，例如 P95 latency、并发 run 数、toolhost 错误率、Postgres 连接池占用。
- 安全扫描还应补专门脚本：检查 report/log/audit/trace 中是否含 API key、secret、auth ref 明文。

## 11. 最终上线判定

上线前至少满足：

- L0-L4 全部通过。
- 有持久化部署时 L5 通过。
- 依赖真实模型能力时 L6 通过。
- release candidate report 通过。
- 所有失败都有可复现 report dir。
- 文档、OpenAPI、E2E matrix 与实际功能一致。
