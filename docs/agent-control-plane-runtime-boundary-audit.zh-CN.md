# AgentOps 控制平面与 Runtime 边界审计

日期：2026-06-14

## 结论

本轮实现后，Clean Core 已经把“Agent 资产与治理事实”和“具体 Agent 执行范式”拆成两层语义：

- 控制平面稳定管理 agent asset、release、run ledger、trace、audit、policy、approval、tool catalog、service connection、runtime hook 与 conformance evidence。
- runtime 层通过 `internal/runtime/driver` 接入，当前已注册 `native_agent/managed` 与 `agent_plugin_source/managed`。
- `Coordinator` 仍然是当前 managed runtime 的执行内核，但调用入口已由 driver registry 调度，未注册 carrier 不再静默回落到 native runtime。

## 包边界分类

| 路径 | 当前角色 | 目标分类 | 备注 |
| --- | --- | --- | --- |
| `internal/app/core` | composition root，装配控制平面服务和 runtime driver | composition root | `Core.RuntimeDrivers` 是控制平面到 runtime 的调度边界。 |
| `internal/runtime/kernel` | 当前 native/managed 执行内核 | managed runtime implementation | 后续应继续剥离 run ledger、trace、policy pinning 等控制平面职责。 |
| `internal/runtime/driver` | runtime driver interface、registry、native wrapper | runtime boundary | 新 carrier 必须注册 driver，不允许隐式 native fallback。 |
| `internal/runtime/conformance` | carrier 治理验收基线 | conformance | 覆盖 driver 注册、run snapshot、trace、release/asset evidence、fail-closed。 |
| `internal/runtime/run` | run repository | control-plane | run 已保存 carrier/source/runtime snapshot。 |
| `internal/governance/trace` | trace recorder | control-plane | runtime 只能通过统一 recorder 写入 evidence。 |
| `internal/governance/audit` | audit logger | control-plane | 发布、资产、策略等事实归属控制平面。 |
| `internal/governance/approval` | approval service | control-plane | approval 是 policy 触发的例外门禁，不是 runtime 自决。 |
| `internal/policy/engine` | policy engine / manager | control-plane | runtime 不应绕过 policy decision。 |
| `internal/agentdef/package` | draft/release/asset 管理和 native/package compile | control-plane + source compiler | `AgentAsset` 已升级为 carrier view。 |
| `internal/agentdef/source` | package/plugin source adapter | source adapter boundary | package 与 plugin source 通过 adapter 暴露统一 normalize/validate/compile。 |
| `internal/agentdef/plugin` | agent plugin manifest/source 构建 | source adapter support | plugin source 是外部能力声明源，不是 external runtime。 |
| `internal/tool/catalog` | provider、manifest、group、operation catalog | control-plane | provider/service_connection 是外部能力与凭证边界事实源。 |
| `internal/tool/runtime` | governed tool execution | control-plane gateway | runtime 调工具必须走 gateway。 |
| `internal/serviceconnection` | connection asset、health、secret reference | control-plane | secret 不进入 agent source、trace/audit 明文。 |
| `internal/runtime/hook` | hook provider/manifest/binding | control-plane extension | hook host 可外置，但 binding 与 policy evidence 归控制平面。 |
| `internal/server` | HTTP/command API adapter | mixed API surface | `agent.run` 已改为按 route release 的 carrier/runtime 调度 driver。 |

## API 分类

| API | 分类 | carrier 注意事项 |
| --- | --- | --- |
| `/v1/commands` | legacy command API | `agent.run` 现在走 runtime driver registry；package/tool/policy 命令仍是控制平面命令。 |
| `/v1/runs`、`/v1/runs/{id}` | control-plane run ledger | response 暴露 carrier/source/runtime snapshot。 |
| `/v1/runs/{id}/diagnostics` | control-plane diagnostics | route/strategy diagnostic 暴露 carrier/runtime/source evidence。 |
| `/v1/traces` | control-plane evidence | 不应假设 trace 来自 native prompt loop。 |
| `/v1/audit` | control-plane evidence | 不应暴露 service credential 明文。 |
| `/v1/agents` | carrier asset view | 暂保留路径名，但 response 已是 carrier view。 |
| `/v1/agents/{id}/versions` | release/version view | release 暴露 source/carrier/runtime/conformance。 |
| `/v1/agents/{id}/prompt-profile` | native-managed 能力 | 非 native carrier 后续需要明确 unsupported，而不是返回空成功。 |
| `/v1/agents/{id}/tool-bindings` | control-plane binding view，当前偏 native/package | 后续按 carrier capability 判断可编辑性。 |
| `/v1/agents/{id}/skills` | package/native source 能力 | plugin/external carrier 不应被误导进入 native 编辑流。 |
| `/v1/tool-providers` | control-plane provider catalog | agent plugin provider 通过 service connection 接入。 |
| `/v1/service-connections` | control-plane credential boundary | connection fact 不写入 agent source。 |
| `/v1/runtime-hook-*` | control-plane extension | hook provider 外置不等于 external runtime。 |

## 治理不变量测试清单

已覆盖：

- release 写入 `source_kind`、`carrier_kind`、`runtime_contract`、`manifest_hash`、`conformance_status`。
- run ledger 写入 direct carrier snapshot 与 `version_snapshot` carrier snapshot。
- route/canary/strategy diagnostics 写入 carrier/runtime/source evidence。
- 未注册 `workflow_graph`、`external_runtime` driver 返回 `AGENT_RUNTIME_DRIVER_UNAVAILABLE`。
- native 与 agent_plugin_source 均通过 managed driver conformance baseline。
- plugin source metadata 拒绝直接携带 base URL、auth ref、secret ref 等连接事实。
- release report gate 要求 carrier evidence。

仍建议后续补强：

- tool call 必须经 `ToolRuntime` 的跨 driver conformance 场景。
- policy denial 与 approval-required pause/resume 的跨 driver conformance 场景。
- trace/audit secret redaction 的端到端断言。
- native-only API 对非 native carrier 的明确 unsupported 响应。
