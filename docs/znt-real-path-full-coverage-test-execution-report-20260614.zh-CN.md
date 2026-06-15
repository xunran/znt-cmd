# znt 真实路径全覆盖测试执行报告 2026-06-14

> 对应测试指南：`docs/znt-real-path-full-coverage-test-guide.zh-CN.md`

## 1. 结论

本轮按测试指南完成 L0-L7 真实路径测试，并最终通过发布候选一键门禁。

- 最终一键门禁：通过。
- 本地静态、单元、合约：通过。
- 本地 stub API smoke、全接口、业务主路径、runtime/source/context/async 专项：通过。
- Postgres release、upgrade、readiness negative：通过。
- 真实模型 smoke：通过。
- race、single-node perf、real-model perf 小样本：通过。

最终发布候选报告：

```text
tmp\e2e\release-candidate-20260614-121909-507-84f86f14\release-candidate-report.json
```

最终一键门禁包含以下步骤，全部 `passed`：

- `gofmt-check`
- `go-vet`
- `go-test`
- `go-test-race`
- `contract-verification`
- `api-smoke`
- `clean-core-mmr-service-e2e`
- `clean-core-all-interfaces-e2e`
- `postgres-release`
- `real-model-smoke`

## 2. 环境

- 系统：Windows / PowerShell。
- Go：本机 Go 1.24 toolchain。
- Postgres：Docker compose 服务 `postgres`，镜像 `postgres:16-alpine`。
- 真实模型：使用本机 `.env` 中的 OpenAI-compatible/DeepSeek 配置完成测试。报告不记录 API key 或任何密钥值。
- 时间：2026-06-14，Asia/Hong_Kong。

## 3. 分层测试结果

| 层级 | 范围 | 结果 | 证据 |
|---|---|---:|---|
| L0 | `go test ./internal/... -count=1` | 通过 | 最终一键门禁 `go-test=passed` |
| L0 | `go vet ./...` | 通过 | 最终一键门禁 `go-vet=passed` |
| L0 | `scripts/verify_contracts.ps1` | 通过 | `tmp\e2e\contract-20260614-122038-186-9b3abfc5`，commands=59 |
| L1 | `scripts/e2e_api_smoke.ps1` | 通过 | `tmp\e2e\release-candidate-20260614-121909-507-84f86f14\api-smoke` |
| L2 | `scripts/e2e_clean_core_all_interfaces.ps1` | 通过 | operations=189，calls=233 |
| L3 | `scripts/e2e_clean_core_mmr_service.ps1` | 通过 | CC-E2E-01 到 CC-E2E-18 全通过，go/no-go=`go` |
| L4 | `scripts/e2e_plugin_runtime_smoke.ps1` | 通过 | `tmp\e2e\plugin-runtime-20260614-114311-162-d7606a4f` |
| L4 | `scripts/e2e_context_strategy.ps1` | 通过 | `tmp\e2e\context-strategy-20260614-115042-481-8196ea13` |
| L4 | `scripts/e2e_clean_core_async_resilience.ps1` | 通过 | `tmp\e2e\clean-core-async-resilience-20260614-115042-490-78d0ade5` |
| L5 | `scripts/e2e_postgres_release.ps1` | 通过 | `readyz=ready`，重启后 `readyz=ready`，go/no-go=`go` |
| L5 | `scripts/e2e_postgres_upgrade.ps1` | 通过 | `tmp\e2e\postgres-upgrade-20260614-120210-863-475549ea` |
| L5 | `scripts/e2e_postgres_readiness_negative.ps1` | 通过 | 缺 schema / checksum mismatch 均返回 not ready 证据 |
| L6 | `scripts/e2e_deepseek_smoke.ps1 -EnvFile .\.env` | 通过 | eval_pass_rate=1，tool_misuse_rate=0 |
| L7 | `go test ./internal/... -race -count=1` | 通过 | 最终一键门禁 `go-test-race=passed` |
| L7 | `scripts/perf_single_node_agent_run.ps1 -TotalRequests 80 -Concurrency 8` | 通过 | success=80，failed=0，p95=142.84ms |
| L7 | `scripts/perf_real_model_agent_run.ps1 -TotalRequests 4 -Concurrency 1` | 通过 | success=4，failed=0，p95=3492.19ms |

## 4. 关键报告目录

```text
tmp\e2e\release-candidate-20260614-121909-507-84f86f14
tmp\e2e\plugin-runtime-20260614-114311-162-d7606a4f
tmp\e2e\context-strategy-20260614-115042-481-8196ea13
tmp\e2e\clean-core-async-resilience-20260614-115042-490-78d0ade5
tmp\e2e\postgres-upgrade-20260614-120210-863-475549ea
tmp\e2e\postgres-readiness-negative-20260614-120658-329-10cfa040
tmp\e2e\perf-single-node-20260614-120838-211-010722f7
tmp\e2e\real-model-perf-20260614-121040-471-2b583ba9
```

最终一键门禁中的关键结果：

- API smoke：`eval_passed=true`，go/no-go=`go`。
- 全接口：operations=189，calls=233，无 missing operations。
- MMR/service：18 个业务步骤全部通过，go/no-go=`go`。
- Postgres release：`readyz_status=ready`，`readyz_after_restart_status=ready`，go/no-go=`go`。
- 真实模型 smoke：`eval_passed=true`，`eval_pass_rate=1`，`eval_tool_misuse_rate=0`，go/no-go=`go`。

## 5. 本轮修复

本轮测试过程中发现并修复了以下问题：

1. E2E 脚本仍把 agent 行为字段放在 `metadata` 中，和当前结构化 `strategies` 合约不一致。
   - 修复：在 `scripts/e2e_common.ps1` 中增加 legacy metadata 到 strategies 的转换。
   - 修复：更新 MMR、async、all-interfaces 脚本中的 agent 创建/草稿创建 payload。

2. 全接口 E2E 未覆盖 run 运维新端点。
   - 修复：补充 `/v1/runs`、`/v1/runs/{run_id}`、timeline、diagnostics、final-response、run replay GET/POST、trace diagnostics 的真实调用。

3. PowerShell migration checksum 脚本和 Go 迁移器 checksum 算法不一致。
   - 根因：PowerShell 直接 hash 文件字节，Go 端先归一化 CRLF/CR 为 LF。
   - 修复：`scripts/check_migration_checksums.ps1` 改为归一化换行后计算 SHA256。
   - 修复：更新 `migrations/checksums.json`。

4. 未迁移 Postgres 库启动时，服务在 readiness handler 可用前访问业务表并退出。
   - 修复：`internal/app/core/core.go` 对迁移尚未就绪的缺表错误做窄范围降级，保留内置 agent，让 deep readiness 报告 not ready。
   - 补充：`internal/app/core/core_test.go` 覆盖 SQLSTATE 42P01 场景。

5. `scripts/release_candidate_check.ps1` 在 `RunPostgres` 时让普通本地 smoke 继承了 Postgres URL，导致空库未迁移时提前失败。
   - 修复：一键门禁中隔离 `CLEAN_CORE_DATABASE_URL`，只有 Postgres 子步骤使用原始数据库连接。

6. 一键发布门禁暴露若干 Go 文件未 gofmt。
   - 修复：对门禁列出的 Go 文件执行 gofmt，使 `gofmt-check` 通过。

## 6. 执行命令备忘

核心分层命令：

```powershell
go test ./internal/... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_contracts.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_api_smoke.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_plugin_runtime_smoke.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_context_strategy.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_async_resilience.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_all_interfaces.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_clean_core_mmr_service.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_release.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_upgrade.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_readiness_negative.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1 -EnvFile .\.env
go test ./internal/... -race -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/perf_single_node_agent_run.ps1 -TotalRequests 80 -Concurrency 8 -CollectUsageEvidence
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/perf_real_model_agent_run.ps1 -EnvFile .\.env -TotalRequests 4 -Concurrency 1 -TimeoutSec 600
```

最终一键门禁命令形态：

```powershell
docker compose up -d postgres
$env:CLEAN_CORE_DATABASE_URL = "postgres://clean_core:clean_core_dev@localhost:5432/<isolated_db>?sslmode=disable"
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1 -RunRace -RunPostgres -RunRealModel -DeepSeekEnvFile .\.env -StrictContractTests
```

## 7. 后续建议

- 将 `release_candidate_check.ps1 -RunRace -RunPostgres -RunRealModel -StrictContractTests` 纳入发布前人工门禁或 CI 夜间门禁。
- 为 Postgres release/upgrade 脚本增加自动创建隔离数据库的可选参数，减少手工 wrapper。
- 为性能测试补明确阈值，例如单机 stub P95、真实模型 P95、错误率、并发 run 限流命中率。
- 增加专门的 secret scan E2E，扫描 report/log/trace/audit 中是否出现 API key、secret、auth ref 明文。
