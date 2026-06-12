# Agent Run 日志追踪与提示词诊断设计

日期：2026-06-12

## 1. 目标

补齐一套面向两类角色的运行日志查询能力：

1. 运维：
   - 能按 run、trace、agent、状态、时间定位一次运行。
   - 能看到 run 生命周期、失败原因、模型调用、工具调用、hook、audit 证据。
   - 能从 HTTP 日志里的 `trace_id` 追到业务运行事实。
2. 提示词优化师：
   - 能看到 agent/version/package、prompt bundle hash、模型、token、decision、repair、工具候选与工具结果。
   - 能复盘“这次为什么答偏/失败/调用错工具”。
   - 默认不暴露完整 PromptBundle 明文，避免把 system/developer prompt、隐藏策略、敏感上下文写入运行日志。

## 2. 当前代码事实

已有底层账本：

- `agent_runs`：run 主表，包含 `run_id`、`trace_id`、agent/version、status、input、step/tool count、`version_snapshot`。
- `trace_events`：运行事件流，包含 `input.received`、`agent.loaded`、`run.created`、`promptbundle.built`、`model.called`、`model.completed`、`decision.*`、`tool.*`、`response.sent` 等。
- `audit_events`：治理审计事件。
- `tool_calls` / `tool_results`：工具调用与结果。
- `runtime_hook_events`：runtime hook 观察、应用、失败、拒绝等事件。

已有接口：

- `GET /v1/traces/{trace_id}`
- `GET /v1/traces/{trace_id}/replay`
- `GET /v1/audit`
- `GET /v1/usage/evidence`
- `GET /v1/runtime-hook-events`
- `GET /v1/tools/{tool_call_id}/trace`

缺口：

- 没有面向 run 的查询入口。
- 没有把 run、trace、audit、tool、hook 拼成一条可读运行日志。
- 没有面向提示词优化师的诊断摘要。
- 当前 trace 只记录 `prompt_bundle_hash`，不会记录完整 PromptBundle 明文。

## 3. 新增 API

### 3.1 Run 列表

```text
GET /v1/runs?agent_id=&status=&trace_id=&task_id=&from=&to=&limit=&offset=
```

返回：

```json
{
  "runs": [],
  "limit": 50,
  "offset": 0
}
```

用途：

- 运维按状态或 agent 查最近运行。
- 从 trace_id 反查 run。
- 提示词优化师查某个 agent/version 的样本。

### 3.2 Run 详情

```text
GET /v1/runs/{run_id}
```

返回：

```json
{
  "run": {},
  "trace_summary": {},
  "tool_summary": {},
  "runtime_hook_summary": {},
  "audit_summary": {}
}
```

### 3.3 Run Timeline

```text
GET /v1/runs/{run_id}/timeline
```

返回按时间排序的混合时间线：

```json
{
  "run_id": "run_xxx",
  "trace_id": "trace_xxx",
  "timeline": [
    {
      "at": "2026-06-12T00:00:00Z",
      "source": "trace",
      "type": "model.called",
      "step_id": "step_run_xxx_1",
      "payload": {}
    }
  ],
  "meta": {
    "trace_events": 12,
    "task_events": 4,
    "tool_calls": 1,
    "tool_results": 1,
    "runtime_hook_events": 3,
    "audit_events": 2
  }
}
```

### 3.4 Run Diagnostics

```text
GET /v1/runs/{run_id}/diagnostics
GET /v1/traces/{trace_id}/diagnostics
```

面向提示词优化师和二线排障，返回：

```json
{
  "run": {},
  "prompt": {
    "prompt_bundle_hashes": [],
    "latest_prompt_bundle_hash": "",
    "prompt_preview_available": true
  },
  "model": {
    "calls_total": 1,
    "failures_total": 0,
    "prompt_tokens_total": 123,
    "completion_tokens_total": 45,
    "models": ["stub/scripted"]
  },
  "decision": {
    "created_total": 1,
    "validated_total": 1,
    "completed_total": 1,
    "types": {"reply": 1},
    "repair_attempts": 0,
    "validation_errors": []
  },
  "tools": {
    "calls_total": 1,
    "results_total": 1,
    "failures_total": 0,
    "tool_ids": ["echo"]
  },
  "hooks": {
    "events_total": 3,
    "statuses": {"observed": 3},
    "phases": {"on_run_started": 1}
  },
  "route": {
    "events": []
  },
  "problems": []
}
```

## 4. 敏感信息策略

默认运行日志不保存和不返回：

- 完整 PromptBundle 明文。
- system prompt / developer prompt 明文。
- raw model decision JSON 明文。
- credential、secret、authorization、token。
- 用户身份原始可识别字段。

允许返回：

- `prompt_bundle_hash`
- `agent_id`、`agent_version`、`agent_package`
- `policy_set_id`、`policy_version`
- model provider/name
- token usage
- decision type、repair attempt、validation warning/error
- tool_id、tool status、artifact refs
- redacted / summarized payload

如后续确实要给提示词优化师看完整 PromptBundle，应新增高权限、短保留、脱敏后的 `prompt_snapshot` 机制，而不是把明文塞进 `trace_events.payload`。

## 5. A/B 与版本路由日志

配合 agent version A/B，需要补统一事件：

```text
agent.route.resolved
```

payload：

```json
{
  "agent_id": "xxx",
  "requested_version": "",
  "resolved_version": "v2",
  "release_status": "stable|canary|none",
  "package_version_id": "pkg_xxx",
  "route_reason": "explicit|active_default|latest_stable_fallback|canary_percent",
  "canary_percent": 10,
  "assignment_key_hash": "..."
}
```

要求：

- stable 和 canary 都记录。
- 不记录原始 user_id / caller_id / session_id。
- `canary.routed` 可保留作为兼容事件，但不能只记录 B 组。

## 6. 实施顺序

1. 新增 `handlers_runs.go`。
2. 注册 `/v1/runs` 与 `/v1/runs/{run_id}` 子路由。
3. 聚合 run + trace + audit + tool + runtime hook 形成详情、timeline、diagnostics。
4. 给 `/v1/traces/{trace_id}/diagnostics` 复用 diagnostics 构建逻辑。
5. 补服务端测试：
   - run 列表可查到刚执行的 run。
   - run timeline 包含 trace/model/response。
   - run diagnostics 包含 prompt hash、model、decision 摘要。
   - 跨 tenant 查询被拒绝。
6. 已补 `agent.route.resolved`，stable/canary 都会记录路由原因、命中版本与分桶键哈希。

## 7. 验收标准

- 运维拿到 `trace_id` 能查到相关 run 和完整时间线。
- 运维拿到 `run_id` 能看到失败原因、模型/工具/hook/audit 摘要。
- 提示词优化师能看到 prompt hash、模型、token、decision、repair 和工具调用摘要。
- 默认响应不泄露完整 PromptBundle、credential、secret、token。
- Postgres 和 InMemory 模式都可用。
