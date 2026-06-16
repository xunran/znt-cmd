# 商家测额智能体 max tool calls exceeded 修复记录

## 现象

列阵页面对商家测额智能体提问后，页面返回：

- `max tool calls exceeded`

已拉取线上 trace：

- `d:\lunlun\mvp_server\.codex-work\aijia-trace-runtime-run-create-1781592555204-jbmfgg.json`

该 trace 显示：

- modelCallsTotal = 2
- toolCallsTotal = 1
- 第二次模型决策仍为 `tool_call`
- run 失败原因为 `max tool calls exceeded`

## 根因

`znt-cmd/internal/context/collector/collector.go` 原先把所有成功工具输出摘要成固定文案：

- `tool output available`

因此模型第二轮看不到工具返回的 `reply/final_decision/should_continue=false`，容易继续发起工具调用或追问。

## 已做修复

1. `internal/context/collector/collector.go`
   - 工具结果摘要优先带出 `reply/response/final_decision/decision_instruction/should_continue/next_action`。
   - 摘要长度限制为 1500 rune，避免把过大的工具结果塞入上下文。

2. `internal/runtime/kernel/coordinator.go`
   - 工具成功返回 `output.final_decision` 时，平台先校验该 Decision。
   - 只允许终态 Decision 直通，不允许 `tool_call`。
   - 校验通过后直接走原有 `dispatch` 完成回复，不再触发第二次模型决策。

## 已跑测试

在 `d:\lunlun\mvp_server\znt-cmd` 执行：

```powershell
go test ./internal/context/collector
go test ./internal/runtime/kernel
go test ./...
git diff --check -- internal/context/collector/collector.go internal/context/collector/collector_test.go internal/runtime/kernel/coordinator.go internal/runtime/kernel/coordinator_test.go
```

结果：

- `go test ./internal/context/collector` passed
- `go test ./internal/runtime/kernel` passed
- `go test ./...` passed
- `git diff --check` passed

同时直连公网商家测额 ToolHost：

```powershell
POST http://47.104.8.74/znt-merchant-limit/tools/invoke
operation=toolhost-47-104-8-74.run_merchant_limit_agent
loanNo=2025031198704813
```

返回关键信号：

- HTTP 200
- `success=true`
- `final=true`
- `should_continue=false`
- `next_action=reply_to_user`
- `final_decision` exists
- `trace.toolCallCount=1`

## 待部署

当前宝塔服务器 `47.104.8.74` 上只看到 `znt-merchant-limit.service`，没有看到 `clean-core/znt-cmd` 服务。

因此页面上的 `aijia.yingasi.com` 还需要部署这版 `znt-cmd` 运行时修复，线上列阵对话才能消除该报错。
