# Origin Agent Platform V3 全面真实测试报告

日期：2026-06-02 09:31 HKT

测试对象：`docs/Origin Agent Platform V3产品文档代码对照与开发计划_v0.1.md`

范围：按文档当前进度从主干验收到 T11（Knowledge / CrossGroup 产品化）进行代码级、契约级、真实 HTTP 服务级、Postgres 持久化、真模型语义评测覆盖。

## 总结论

结论：通过，带 3 个非阻断说明。

- V3 文档中 T11 当前标注为“已完成 MVP 主干”，本次已用 Go 单测、OpenAPI 契约、真实 HTTP smoke、Postgres 持久化 E2E 和 DeepSeek 真模型 eval 进行验证。
- 新增 `scripts/e2e_v3_mvp_smoke.ps1`，补上文档第 8 节建议的 V3 MVP 主链路 smoke 入口。
- 发现 `release.yaml` 这类语义评测不适合用 `stub` 模型跑：stub 固定回复 `ok`，导致语义断言失败；同一套 release eval 使用 DeepSeek 真模型已通过。
- CrossGroup 的“已授权后跨群脱敏搜索”全 HTTP 流程仍缺 REST/admin 入口授予 `GroupPermissionPolicy`，当前由 server/service 单测覆盖；V3 smoke 已覆盖知识库检索、共享策略创建、未授权跨群搜索拒绝。

## 测试证据

| 层级 | 命令 / 脚本 | 结果 | 证据 |
|---|---|---:|---|
| Go 全包单测 | `go test ./... -count=1`（由 release candidate 脚本执行） | Passed | `D:\code2\znt\tmp\e2e\release-candidate-20260602-091327-222-ac044709\release-candidate-report.json` |
| gofmt / go vet | `scripts/release_candidate_check.ps1 -PackageDir agent_packages\origin-coordinator` | Passed | 同上 |
| Race 检查 | `go test ./... -race -count=1` | Passed | 终端输出全包 ok |
| OpenAPI / 命令契约 | `scripts/verify_contracts.ps1` | Passed with warnings | `D:\code2\znt\tmp\e2e\contract-20260602-092507-210-fd0ee253\contract-report.json` |
| 包校验 / prompt lint / prompt preview / capabilities | `scripts/release_candidate_check.ps1 -PackageDir agent_packages\origin-coordinator` | Passed | release-candidate report |
| API 主链路 smoke | release candidate 内置 `e2e_api_smoke.ps1` | Passed | `D:\code2\znt\tmp\e2e\release-candidate-20260602-091327-222-ac044709\api-smoke` |
| Plugin / ToolRuntime smoke | `scripts/e2e_plugin_runtime_smoke.ps1` | Passed | `D:\code2\znt\tmp\e2e\plugin-runtime-20260602-091436-899-ccea3014\plugin-runtime-report.json` |
| V3 MVP smoke | `scripts/e2e_v3_mvp_smoke.ps1` | Passed | `D:\code2\znt\tmp\e2e\v3-mvp-20260602-092440-456-9109a8f1\v3-mvp-smoke-report.json` |
| Postgres 迁移 / 持久化 E2E | `scripts/e2e_postgres_release.ps1 -PackageDir agent_packages\origin-coordinator` | Passed | `D:\code2\znt\tmp\e2e\postgres-release-20260602-092541-811-13e03c92\postgres-release-report.json` |
| DeepSeek smoke | `scripts/e2e_deepseek_smoke.ps1 -PackageDir agent_packages\origin-coordinator -EnvFile .\.env` | Passed | `D:\code2\znt\tmp\e2e\deepseek-smoke-20260602-092558-904-51aa0ac4\deepseek-smoke-report.json` |
| 真模型 release eval | `e2e_eval_suite.ps1 ... eval\release.yaml -ModelProvider openai-compatible` | Passed | `D:\code2\znt\tmp\e2e\eval-suite-20260602-092941-419-fc08db80\eval-suite-report.json` |
| 真模型 group chat eval | `e2e_eval_suite.ps1 ... eval\group_chat.yaml -ModelProvider openai-compatible` | Passed | `D:\code2\znt\tmp\e2e\eval-suite-20260602-093025-766-ff76de17\eval-suite-report.json` |
| 真模型 context retrieval eval | `e2e_eval_suite.ps1 ... eval\context_retrieval.yaml -ModelProvider openai-compatible` | Passed | `D:\code2\znt\tmp\e2e\eval-suite-20260602-093059-426-e0d76bc7\eval-suite-report.json` |
| 真模型 group chat retrieval eval | `e2e_eval_suite.ps1 ... eval\group_chat_retrieval.yaml -ModelProvider openai-compatible` | Passed | `D:\code2\znt\tmp\e2e\eval-suite-20260602-093134-986-f0b21cd6\eval-suite-report.json` |

## T11 验证情况

`scripts/e2e_v3_mvp_smoke.ps1` 已真实启动服务并覆盖：

- `/v1/knowledge-bases` 创建 shared / hybrid KnowledgeBase。
- `/v1/knowledge-bases/{id}/documents` 写入文档并生成 completed ingestion job。
- `/v1/knowledge-bases/{id}/index-jobs` 和单个 job 查询。
- `/v1/knowledge-search` 本群 hybrid 检索返回结果。
- `/v1/cross-groups/search` 在缺少显式 `cross_group.search` 权限时拒绝。
- `/v1/cross-group-share-policies` 可创建带 `mask_emails` 的共享策略。
- 创建策略后仍缺显式 search 权限时，跨群搜索继续拒绝。

补充单测证据：

- `internal/knowledge`：KnowledgeBase / ingestion job / hybrid search mode。
- `internal/crossgroup`：显式 share policy、权限双门禁、脱敏策略。
- `internal/server.TestKnowledgeAndCrossGroupResourceAPIs`：服务层覆盖 permission policy 注入后的脱敏跨群搜索。

## 非阻断发现

1. `scripts/verify_contracts.ps1` 通过，但仍有 27 条 warning：若干 command enum 在 `internal/server/server_test.go` 中没有直接字符串证据。建议后续补 server 层命令回归，或让契约脚本识别资源 API / 间接 helper 证据。
2. `release.yaml` 用 `stub` 模型跑会失败，失败原因是 stub 回复固定 `ok`，无法满足 `capability_not_available`、`协调`、`waiting_input` 等语义断言。使用 DeepSeek 真模型运行同一 release suite 已通过，建议将 semantic eval 默认要求 real model，或扩展 stub fixture 覆盖 release.yaml。
3. CrossGroup 缺少 REST/admin 入口来创建 `GroupPermissionPolicy`。因此“授权后跨群脱敏搜索”的全 HTTP 端到端链路目前不能只靠 REST 完成，仍依赖 server/service 单测注入权限策略。

## 新增文件

- `scripts/e2e_v3_mvp_smoke.ps1`

该脚本覆盖文档第 8 节建议的 V3 MVP 主链路，并额外纳入 T11 Knowledge / CrossGroup HTTP 验收。
