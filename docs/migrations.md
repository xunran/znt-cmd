# Migration Development Guide

Clean Core migrations are append-only SQL files under `migrations/`.

Rules:

- Use monotonically increasing version prefixes such as `001_`.
- Re-running `migration up` must be safe; the runner skips already applied versions after checksum verification.
- Never mutate historical TaskEvent, ToolResult, TraceEvent, or AuditEvent rows in place.
- JSONB additions must be optional and readers must tolerate missing fields.
- Every schema change needs a rollback note in the PR or release checklist.
- Readiness checks assert the base schema includes AgentPackage, AgentDefinition, PolicySet, Task, TaskEvent, AgentRun, TaskPlan, PlanStep, PlanEvent, Handoff, ToolCall, ToolResult, Artifact, Memory, Trace, Audit, and ExternalTaskBinding objects.
- Runtime recovery must be possible from append-only fact tables; do not rewrite plan, task, tool result, trace, or audit history.
