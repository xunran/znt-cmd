# External Agent Runtime Contract 草案

日期：2026-06-14

## 定位

`external_runtime` 表示用户自带或外部托管的 Agent runtime。它可以独立部署、独立运行；未接入 AgentOps 控制平面时，不属于平台治理范围。

当 external runtime 接入控制平面后，必须选择明确的 runtime contract。第一阶段只保留 contract 草案和 fail-closed driver 边界，不产品化 connected/observed 执行。

## Runtime Contract 等级

| Contract | 语义 | 第一阶段状态 |
| --- | --- | --- |
| `managed` | 平台托管执行并强治理 | external runtime 通常不适用，除非平台实际托管该 runtime。 |
| `connected` | 外部执行，但 run、tool、credential、approval、trace/audit 接入控制平面 | 草案保留，未注册 driver 时返回 `AGENT_RUNTIME_DRIVER_UNAVAILABLE`。 |
| `observed` | 外部执行，只上报 run/trace/audit，平台提供观测 | 草案保留，不承诺强制 tool policy、approval 或 credential isolation。 |

## Connected 最小治理要求

connected runtime 必须满足：

- run 必须由控制平面创建或登记，runtime 只能 ack/start/complete/fail。
- 所有 tool call 必须经控制平面 `ToolRuntime` 或等价 gateway。
- service credential 只能通过 delegated credential 使用，runtime 不直接读取 secret。
- policy decision 由控制平面产生。
- policy 触发 approval 时，runtime 必须支持 pause/resume handshake。
- trace/audit 事件必须按标准 schema 上报，并关联 run id、trace id、tenant id、carrier snapshot。
- runtime 不得直接写控制平面事实源，例如 release、asset、policy、approval、service connection。

## Observed 明确不承诺的能力

observed runtime 只提供观测接入，不承诺：

- 强制工具策略。
- 强制 approval pause/resume。
- credential 隔离或 delegated credential。
- 控制平面对 runtime 内部状态的恢复能力。
- 对 runtime tool side effects 的阻断能力。

observed 适合低治理要求的外部 agent 观测，不适合作为高风险生产动作的受控执行模式。

## 协议草案

### Run create / ack

控制平面创建 run，并向 runtime 发送：

```json
{
  "run_id": "run_x",
  "trace_id": "trace_x",
  "tenant_id": "tenant_1",
  "agent_id": "agent_1",
  "carrier_kind": "external_runtime",
  "runtime_contract": "connected",
  "input": "user input",
  "version_snapshot": {}
}
```

runtime 必须 ack：

```json
{
  "run_id": "run_x",
  "status": "accepted",
  "runtime_instance_id": "runtime-node-1"
}
```

### Status event

```json
{
  "run_id": "run_x",
  "status": "running|paused|completed|failed|cancelled",
  "reason": "",
  "occurred_at": "2026-06-14T00:00:00Z"
}
```

### Trace event

```json
{
  "trace_id": "trace_x",
  "run_id": "run_x",
  "tenant_id": "tenant_1",
  "type": "external.runtime.event",
  "payload": {}
}
```

### Tool invocation gateway

runtime 向控制平面请求 tool call；控制平面执行 policy、approval、credential delegation 和 actual invocation 后返回结果。

observed contract 可以不上接该能力；connected contract 必须上接。

### Approval request / resume

当 gateway 返回 approval required：

```json
{
  "run_id": "run_x",
  "status": "paused",
  "approval_id": "approval_x",
  "resume_token": "opaque"
}
```

runtime 必须停止相关 side effect，等待控制平面 resume 或 cancel。

### Credential delegation

runtime 不接收 secret 明文，只接收 scoped delegation token 或 gateway capability reference。token 必须有 tenant、provider、operation、ttl、audit id 等边界。

## Conformance 要求

新增 external runtime driver 前至少需要通过：

- driver can register exact `external_runtime/connected` or `external_runtime/observed`。
- unsupported contract fails closed。
- run includes carrier/source/runtime snapshot。
- trace/audit schema 标准化。
- connected tool call 走 gateway。
- connected policy denial blocks tool call。
- connected approval required pauses run and resumes deterministically。
- secret 不进入 trace/audit 明文。
- old run snapshot remains reproducible。

第一阶段只实现 fail-closed 和文档草案，不扩大 external runtime 的产品入口。
