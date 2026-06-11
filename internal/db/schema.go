package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const MinimumSchemaVersion = 30

type SchemaQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func CheckSchema(ctx context.Context, q SchemaQuerier) error {
	if q == nil {
		return fmt.Errorf("database is not configured")
	}

	gooseVersion, ok, err := gooseSchemaVersion(ctx, q)
	if err != nil {
		return err
	}
	if ok {
		if gooseVersion < MinimumSchemaVersion {
			return fmt.Errorf("database schema version %d is below required version %d", gooseVersion, MinimumSchemaVersion)
		}
		return nil
	}

	for _, table := range requiredTables {
		exists, err := relationExists(ctx, q, table)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("database schema is not migrated: missing table %s", table)
		}
	}
	for _, column := range requiredColumns {
		exists, err := columnExists(ctx, q, column.table, column.name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("database schema is not migrated: missing column %s.%s", column.table, column.name)
		}
	}
	return nil
}

func gooseSchemaVersion(ctx context.Context, q SchemaQuerier) (int64, bool, error) {
	exists, err := relationExists(ctx, q, "goose_db_version")
	if err != nil {
		return 0, false, err
	}
	if !exists {
		return 0, false, nil
	}

	var version int64
	if err := q.QueryRow(ctx, `
SELECT COALESCE(MAX(version_id), 0)::bigint
FROM goose_db_version
WHERE is_applied = true
`).Scan(&version); err != nil {
		return 0, true, err
	}
	return version, true, nil
}

func relationExists(ctx context.Context, q SchemaQuerier, name string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists)
	return exists, err
}

func columnExists(ctx context.Context, q SchemaQuerier, table, name string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM pg_attribute
  WHERE attrelid = to_regclass($1)
    AND attname = $2
    AND NOT attisdropped
)
`, table, name).Scan(&exists)
	return exists, err
}

var requiredTables = []string{
	"projects",
	"worker_pools",
	"task_types",
	"jobs",
	"workers",
	"worker_tokens",
	"worker_jobs",
	"job_events",
	"job_logs",
	"job_assets",
	"usage_events",
	"producer_api_keys",
	"producer_key_audit_events",
	"producer_webhook_endpoints",
	"producer_webhook_subscriptions",
	"idempotency_keys",
	"producer_rate_limits",
	"producer_job_callbacks",
	"producer_callback_deliveries",
	"producer_usage_daily_by_project",
	"producer_usage_daily_by_key",
	"usage_daily_counters",
	"project_quotas",
	"worker_rate_limits",
	"runtime_heartbeats",
	"audit_events",
	"schedules",
	"project_job_status_stats",
	"project_job_failed_minute_stats",
}

var requiredColumns = []struct {
	table string
	name  string
}{
	{table: "projects", name: "tenant_id"},
	{table: "task_types", name: "retry_backoff_sec"},
	{table: "jobs", name: "output_schema_json"},
	{table: "jobs", name: "storage_enabled"},
	{table: "jobs", name: "allow_encrypted_result"},
	{table: "workers", name: "instance_id"},
	{table: "producer_api_keys", name: "expires_at"},
	{table: "producer_api_keys", name: "allowed_task_keys"},
	{table: "worker_tokens", name: "expires_at"},
	{table: "worker_rate_limits", name: "bucket"},
	{table: "runtime_heartbeats", name: "last_run_at"},
	{table: "runtime_heartbeats", name: "last_count"},
	{table: "runtime_heartbeats", name: "last_error"},
	{table: "runtime_heartbeats", name: "expected_interval_sec"},
	{table: "task_types", name: "contract_revision"},
	{table: "jobs", name: "task_type_revision"},
	{table: "jobs", name: "external_user_hash"},
	{table: "worker_pools", name: "resource_profile_json"},
	{table: "audit_events", name: "tenant_id"},
	{table: "producer_job_callbacks", name: "ready_at"},
	{table: "producer_job_callbacks", name: "terminal_job_status"},
}
