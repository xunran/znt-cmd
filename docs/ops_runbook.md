# Clean Core Ops Runbook

For the current single-node deployment standard, use `docs/single_node_runbook.md` as the authoritative step-by-step runbook. This file keeps the shorter generic ops checklist.

## Deployment Units

- Local dev: `go run ./cmd/clean-core-server -config config.example.json`
- Container dev: `docker compose up --build`
- Migration job: `clean-core-server -migration up -migration-dir /migrations`
- Kubernetes: use `deploy/helm/clean-core` and run the migration Job before rolling the Deployment.

## Kubernetes HA Baseline

The Helm chart is the baseline for production-style application redundancy:

- `replicaCount` defaults to 2.
- Rolling updates use `maxUnavailable=0` and `maxSurge=1`.
- A `PodDisruptionBudget` keeps at least one CleanCore pod available during voluntary disruption.
- The Deployment sets resource requests and limits.
- `/readyz` is the readiness probe and should run in deep mode for production.
- `/healthz` is the liveness probe.

Before claiming production HA, run a pod deletion drill in the target cluster:

```bash
kubectl get pods -l app.kubernetes.io/name=<release-name>
kubectl delete pod <one-clean-core-pod>
kubectl rollout status deployment/<release-name>
```

During the drill, continuously call `/readyz`, `/v1/readiness/report`, and at least one authenticated `agent.run` request through the production traffic entry point. The drill passes only if the entry point keeps routing to ready pods and user-visible requests do not fail.

Postgres HA is separate from application pod redundancy. A single Postgres instance is still a single point of failure; use a managed HA Postgres service, Patroni/operator-backed cluster, or write an explicit risk acceptance before calling the whole system highly available.

## Required Production Secrets

- `CLEAN_CORE_SERVICE_TOKEN`
- `CLEAN_CORE_DATABASE_URL`
- `CLEAN_CORE_MODEL_BASE_URL`
- `CLEAN_CORE_MODEL_API_KEY`
- `CLEAN_CORE_MODEL_NAME`
- Optional model tuning: `CLEAN_CORE_MODEL_MAX_TOKENS`, `CLEAN_CORE_MODEL_TEMPERATURE`, `CLEAN_CORE_MODEL_THINKING`, `CLEAN_CORE_MODEL_REASONING_EFFORT`
- Optional external collaboration credentials: `CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL`, `CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN`

Store these in the target platform secret manager. Do not bake them into images or ConfigMaps.

## Migration Procedure

1. Run `clean-core-server -migration status -migration-dir /migrations`.
2. Confirm `live_schema=ready` or review `live_schema_details`.
3. Run `clean-core-server -migration up -migration-dir /migrations`.
4. Re-run `status` and check `/readyz` plus `/v1/release/go-no-go`.

## Backup And Restore

Backup:

```bash
pg_dump "$CLEAN_CORE_DATABASE_URL" --format=custom --file clean-core-$(date +%Y%m%d%H%M%S).dump
```

Restore into a fresh database:

```bash
pg_restore --clean --if-exists --dbname "$CLEAN_CORE_DATABASE_URL" clean-core.dump
clean-core-server -migration status -migration-dir /migrations
```

## Rollback Controls

- Disable an agent with `CLEAN_CORE_DISABLED_AGENT_IDS`.
- Disable tools with `CLEAN_CORE_DISABLED_TOOL_IDS`.
- Disable handoff with `CLEAN_CORE_DISABLE_HANDOFF=true`.
- Disable external tool invocation with `CLEAN_CORE_DISABLE_EXTERNAL_TOOLS_INVOKE=true`.
- Use `agent.package.rollback` with a non-empty reason when release policy requires it.

## Health Gates

- `/healthz` checks process liveness.
- `/readyz` checks configured readiness.
- `/v1/readiness/report` returns config, DB, governance, migrations, tools, and release switches.
- `/v1/release/go-no-go` aggregates readiness, migration, contract, observability, and production gates.
