# Native Runtime Driver 抽取说明

日期：2026-06-14

## 当前状态

`internal/runtime/kernel.Coordinator` 已被包装为 `internal/runtime/driver.NativeDriver`，并在 `Core.RuntimeDrivers` 中注册：

- `native_agent / managed`
- `agent_plugin_source / managed`

`/v1/commands` 中的 `agent.run` 不再直接调用 `Coordinator`，而是先解析 agent route，再根据 release 的 `carrier_kind` 与 `runtime_contract` 从 registry 获取 driver。

## 已完成的边界

- 定义 `Driver` 与 `PreparedDriver` 接口，包含 `StartRun`、`PrepareRun`、`ExecutePreparedRun`、`ResumeRun`、`CancelRun`、`Preview`、`ValidateSource`。
- `NativeDriver` 以指针持有 `Coordinator`，保证测试和运行期替换 `Core.Coordinator.Model` 等字段后 driver 能看到最新状态。
- registry 对缺失 driver fail closed，错误码为 `AGENT_RUNTIME_DRIVER_UNAVAILABLE`。
- async run 使用 `PreparedDriver`，不再假设所有 runtime 都支持 native coordinator 的 prepared execution。
- run 创建事件和 run ledger 都写入 carrier/source/runtime snapshot。

## Coordinator 中仍混合的控制平面职责

以下职责当前仍在 `Coordinator` 内部，后续拆分时应上移到控制平面服务或 run orchestrator：

| 职责 | 当前位置 | 建议目标 |
| --- | --- | --- |
| run id / task id / trace id 派生 | `Coordinator.PrepareEnvelopeRun`、`HandleEnvelope` | control-plane run orchestration |
| `Runs.Create` 与状态更新 | `Coordinator` | run ledger service |
| route release snapshot 转换 | `Coordinator.versionSnapshot` | control-plane route resolver / run creator |
| `TraceRunCreated` 等生命周期 trace | `Coordinator` | run orchestration service，runtime 只上报 execution event |
| prompt/context 构建 | `Coordinator` | native managed runtime 保留 |
| model 调用与 repair loop | `Coordinator` | native managed runtime 保留 |
| tool invocation | `Coordinator` 经 `ToolRuntime` | gateway 保持控制平面，runtime 只发起受控请求 |
| approval pause/resume | `Coordinator`、tool policy path | control-plane approval handshake |
| policy pinning / policy refs | `Coordinator.versionSnapshot` | release/run snapshot service |

## 后续拆分建议

1. 抽出 `RunOrchestrator`，负责 route、admission、run ledger、trace/audit lifecycle。
2. 将 `Coordinator` 缩小为 native managed execution engine，只接收已创建的 run context 与 carrier snapshot。
3. 为 `connected` runtime 定义 pause/resume、tool gateway、credential delegation 的最小协议。
4. 将 conformance 扩展到 tool policy、approval、secret redaction，作为新增 driver 的强制验收。

## 不做的事情

- 当前不拆微服务。
- 当前不产品化 `connected` 或 `observed` external runtime。
- 当前不把 `AgentDefinition` 扩成所有 carrier 的公共大模型。
- 当前不允许未知 carrier 静默回退到 native runtime。
