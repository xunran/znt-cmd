# Clean Core

Golang-only Clean Core implementation scaffold for the original-agent runtime.

Current scope:

- Batch 0 engineering baseline: `go.mod`, server entrypoint, config loading, structured logging, health/readiness endpoints, migrations, repository conventions.
- Contract alpha: IDs, enums, errors, envelope/context, task/run/decision/tool/policy/handoff/artifact/governance contracts.
- Batch 1 foundations: task state machine, append-only task events, in-memory task repository, AgentRun repository, trace/audit recorders.
- Batch 2 baseline: static agent loader, WorkView, PromptBundle, stub model client, Decision parser/validator, multi-round `agent.run` coordinator.
- Batch 3 foundation: in-memory tool registry, `echo` and `artifact.create`, ToolPolicy evaluator, ToolRuntime invoke, ToolCall/ToolResult idempotency.
- Batch 4/5 alpha loop: AgentPackage publish/canary/stable/rollback, Eval gating for stable, capability discovery, TaskPlan/PlanStep/PlanEvent, AgentHandoff and external collaboration stubs.
- Batch 6 hardening: service auth, release disable switches, readiness and Go/No-Go reports, recovery checks, trace/audit/task/tool/handoff query endpoints.

Run with local Go:

```powershell
.tools\go1.26.3\go\bin\go.exe test ./...
.tools\go1.26.3\go\bin\go.exe run ./cmd/clean-core-server -config config.example.json
```

Or with a system Go installation:

```powershell
go test ./...
go run ./cmd/clean-core-server -config config.example.json
```

Container and ops entry points:

```powershell
docker compose up --build
go run ./cmd/clean-core-server -config config.example.json -migration status -migration-dir migrations
```

See `docs/ops_runbook.md` and `deploy/helm/clean-core` for deployment, migration, secret, backup, restore, and Kubernetes packaging guidance.

HTTP endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /version`
- `POST /v1/commands`
- `GET /v1/traces/{trace_id}`
- `GET /v1/tasks/{task_id}`
- `GET /v1/tasks/{task_id}/timeline`
- `GET /v1/tasks/{task_id}/plan`
- `GET /v1/tasks/{task_id}/recovery`
- `GET /v1/tools/{tool_call_id}/trace`
- `GET /v1/handoffs/{handoff_id}/trace`
- `GET /v1/external-tasks/{provider}/{external_task_id}`
- `GET /v1/audit`
- `GET /v1/readiness/report`
- `GET /v1/release/go-no-go`

Frozen command names:

- `agent.run`
- `task.start`
- `task.command`
- `tools.invoke`
- `agent.package.publish`
- `agent.package.canary`
- `agent.package.stable`
- `agent.package.rollback`
- `eval.run`
- `origin.agent.delegate`

Release switches are configured with `disabled_agent_ids`, `disabled_tool_ids`, `disable_handoff`, and `disable_external_tools_invoke`.

The docs in `docs/` remain the source of architectural truth. Code should evolve by compatible extension unless a controlled contract change is explicitly recorded.
