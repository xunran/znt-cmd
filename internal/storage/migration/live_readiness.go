package migration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type ColumnInfo struct {
	Name     string
	NotNull  bool
	DataType string
}

type ColumnRequirement struct {
	Table   string
	Column  string
	NotNull bool
}

type LiveInspector interface {
	Tables(ctx context.Context) (map[string]bool, error)
	Columns(ctx context.Context, table string) (map[string]ColumnInfo, error)
	Indexes(ctx context.Context) (map[string]bool, error)
	AppliedMigrations(ctx context.Context) (map[string]AppliedMigration, error)
}

type LiveSchemaReport struct {
	Status             string   `json:"status"`
	MissingTables      []string `json:"missing_tables,omitempty"`
	MissingColumns     []string `json:"missing_columns,omitempty"`
	NullableColumns    []string `json:"nullable_columns,omitempty"`
	MissingIndexes     []string `json:"missing_indexes,omitempty"`
	MissingMigrations  []string `json:"missing_migrations,omitempty"`
	ChecksumMismatches []string `json:"checksum_mismatches,omitempty"`
}

var RequiredLiveTables = []string{
	"clean_core_schema_migrations",
	"agent_package_versions",
	"agent_assets",
	"agent_package_canary_hits",
	"agent_package_drafts",
	"agent_package_proposals",
	"agent_package_eval_results",
	"agent_prompt_profiles",
	"agent_skill_definitions",
	"agent_tool_bindings",
	"agent_collaborators",
	"agent_exported_tools",
	"agent_definitions",
	"policy_sets",
	"policy_drafts",
	"policy_versions",
	"eval_suites",
	"eval_suite_results",
	"tasks",
	"task_events",
	"agent_runs",
	"task_plans",
	"plan_steps",
	"plan_events",
	"agent_handoffs",
	"handoff_context_packages",
	"service_connections",
	"service_connection_resources",
	"service_connection_health_events",
	"service_connection_secret_rotations",
	"tool_providers",
	"tool_adapter_operations",
	"tool_groups",
	"tool_manifests",
	"tool_manifest_versions",
	"tool_runtime_registry_cache",
	"runtime_hook_providers",
	"runtime_hook_manifests",
	"agent_runtime_hook_bindings",
	"runtime_hook_events",
	"tool_calls",
	"tool_results",
	"artifacts",
	"artifact_contents",
	"memory_events",
	"trace_events",
	"audit_events",
	"external_task_bindings",
	"group_members",
	"group_permission_policies",
	"memory_scopes",
	"knowledge_bases",
	"knowledge_documents",
	"knowledge_ingestion_jobs",
	"cross_group_share_policies",
	"group_task_bindings",
	"skill_update_requests",
	"agent_capabilities",
	"agent_draft_requests",
	"tone_policies",
}

var RequiredLiveColumns = []ColumnRequirement{
	{Table: "agent_package_versions", Column: "tenant_id", NotNull: true},
	{Table: "agent_package_versions", Column: "canary_percent", NotNull: true},
	{Table: "agent_package_versions", Column: "canary_scope_json", NotNull: true},
	{Table: "agent_assets", Column: "tenant_id", NotNull: true},
	{Table: "agent_assets", Column: "agent_id", NotNull: true},
	{Table: "agent_assets", Column: "status", NotNull: true},
	{Table: "agent_package_canary_hits", Column: "tenant_id", NotNull: true},
	{Table: "agent_package_drafts", Column: "tenant_id", NotNull: true},
	{Table: "agent_package_proposals", Column: "tenant_id", NotNull: true},
	{Table: "agent_prompt_profiles", Column: "tenant_id", NotNull: true},
	{Table: "agent_prompt_profiles", Column: "agent_id", NotNull: true},
	{Table: "agent_prompt_profiles", Column: "source_id", NotNull: true},
	{Table: "agent_skill_definitions", Column: "tenant_id", NotNull: true},
	{Table: "agent_skill_definitions", Column: "agent_id", NotNull: true},
	{Table: "agent_skill_definitions", Column: "skill_id", NotNull: true},
	{Table: "agent_tool_bindings", Column: "tenant_id", NotNull: true},
	{Table: "agent_tool_bindings", Column: "agent_id", NotNull: true},
	{Table: "agent_tool_bindings", Column: "source_id", NotNull: true},
	{Table: "agent_collaborators", Column: "tenant_id", NotNull: true},
	{Table: "agent_collaborators", Column: "agent_id", NotNull: true},
	{Table: "agent_collaborators", Column: "collaborator_agent_id", NotNull: true},
	{Table: "agent_exported_tools", Column: "tenant_id", NotNull: true},
	{Table: "agent_exported_tools", Column: "agent_id", NotNull: true},
	{Table: "agent_exported_tools", Column: "tool_id", NotNull: true},
	{Table: "agent_definitions", Column: "tenant_id", NotNull: true},
	{Table: "policy_sets", Column: "tenant_id", NotNull: true},
	{Table: "policy_sets", Column: "policy_set_id", NotNull: true},
	{Table: "policy_sets", Column: "version", NotNull: true},
	{Table: "policy_drafts", Column: "tenant_id", NotNull: true},
	{Table: "policy_versions", Column: "tenant_id", NotNull: true},
	{Table: "eval_suites", Column: "tenant_id", NotNull: true},
	{Table: "eval_suite_results", Column: "tenant_id", NotNull: true},
	{Table: "tasks", Column: "tenant_id", NotNull: true},
	{Table: "task_events", Column: "tenant_id", NotNull: true},
	{Table: "agent_runs", Column: "tenant_id", NotNull: true},
	{Table: "agent_handoffs", Column: "tenant_id", NotNull: true},
	{Table: "handoff_context_packages", Column: "tenant_id", NotNull: true},
	{Table: "service_connections", Column: "tenant_id", NotNull: true},
	{Table: "service_connections", Column: "connection_id", NotNull: true},
	{Table: "service_connections", Column: "connection_type", NotNull: true},
	{Table: "service_connections", Column: "status", NotNull: true},
	{Table: "service_connections", Column: "health_status", NotNull: true},
	{Table: "service_connection_resources", Column: "tenant_id", NotNull: true},
	{Table: "service_connection_resources", Column: "connection_id", NotNull: true},
	{Table: "service_connection_resources", Column: "resource_id", NotNull: true},
	{Table: "service_connection_health_events", Column: "tenant_id", NotNull: true},
	{Table: "service_connection_health_events", Column: "connection_id", NotNull: true},
	{Table: "service_connection_health_events", Column: "health_status", NotNull: true},
	{Table: "service_connection_health_events", Column: "latency_ms", NotNull: true},
	{Table: "service_connection_health_events", Column: "checked_at", NotNull: true},
	{Table: "service_connection_secret_rotations", Column: "rotation_id", NotNull: true},
	{Table: "service_connection_secret_rotations", Column: "tenant_id", NotNull: true},
	{Table: "service_connection_secret_rotations", Column: "connection_id", NotNull: true},
	{Table: "service_connection_secret_rotations", Column: "new_auth_ref_hash", NotNull: true},
	{Table: "service_connection_secret_rotations", Column: "rotated_at", NotNull: true},
	{Table: "tool_providers", Column: "tenant_id", NotNull: true},
	{Table: "tool_providers", Column: "provider_id", NotNull: true},
	{Table: "tool_providers", Column: "health_status", NotNull: true},
	{Table: "tool_adapter_operations", Column: "tenant_id", NotNull: true},
	{Table: "tool_adapter_operations", Column: "provider_id", NotNull: true},
	{Table: "tool_adapter_operations", Column: "operation_id", NotNull: true},
	{Table: "tool_adapter_operations", Column: "tool_id", NotNull: true},
	{Table: "tool_adapter_operations", Column: "service_connection_id", NotNull: true},
	{Table: "tool_adapter_operations", Column: "max_rows", NotNull: true},
	{Table: "tool_adapter_operations", Column: "redact_columns_json", NotNull: true},
	{Table: "tool_adapter_operations", Column: "read_only", NotNull: true},
	{Table: "tool_adapter_operations", Column: "status", NotNull: true},
	{Table: "tool_groups", Column: "tenant_id", NotNull: true},
	{Table: "tool_groups", Column: "group_id", NotNull: true},
	{Table: "tool_manifests", Column: "tenant_id", NotNull: true},
	{Table: "tool_manifests", Column: "tool_id", NotNull: true},
	{Table: "tool_manifests", Column: "status", NotNull: true},
	{Table: "tool_manifest_versions", Column: "tenant_id", NotNull: true},
	{Table: "tool_manifest_versions", Column: "tool_id", NotNull: true},
	{Table: "tool_runtime_registry_cache", Column: "tenant_id", NotNull: true},
	{Table: "tool_runtime_registry_cache", Column: "tool_id", NotNull: true},
	{Table: "runtime_hook_providers", Column: "tenant_id", NotNull: true},
	{Table: "runtime_hook_providers", Column: "provider_id", NotNull: true},
	{Table: "runtime_hook_providers", Column: "health_status", NotNull: true},
	{Table: "runtime_hook_manifests", Column: "tenant_id", NotNull: true},
	{Table: "runtime_hook_manifests", Column: "hook_id", NotNull: true},
	{Table: "agent_runtime_hook_bindings", Column: "tenant_id", NotNull: true},
	{Table: "agent_runtime_hook_bindings", Column: "agent_id", NotNull: true},
	{Table: "agent_runtime_hook_bindings", Column: "hook_id", NotNull: true},
	{Table: "runtime_hook_events", Column: "tenant_id", NotNull: true},
	{Table: "runtime_hook_events", Column: "hook_id", NotNull: true},
	{Table: "tool_calls", Column: "tenant_id", NotNull: true},
	{Table: "artifacts", Column: "tenant_id", NotNull: true},
	{Table: "memory_events", Column: "tenant_id", NotNull: true},
	{Table: "trace_events", Column: "tenant_id", NotNull: true},
	{Table: "audit_events", Column: "tenant_id", NotNull: true},
	{Table: "external_task_bindings", Column: "tenant_id", NotNull: true},
	{Table: "group_members", Column: "tenant_id", NotNull: true},
	{Table: "group_members", Column: "group_id", NotNull: true},
	{Table: "group_members", Column: "member_id", NotNull: true},
	{Table: "group_permission_policies", Column: "tenant_id", NotNull: true},
	{Table: "group_permission_policies", Column: "group_id", NotNull: true},
	{Table: "memory_scopes", Column: "tenant_id", NotNull: true},
	{Table: "memory_scopes", Column: "memory_id", NotNull: true},
	{Table: "knowledge_bases", Column: "tenant_id", NotNull: true},
	{Table: "knowledge_bases", Column: "search_mode", NotNull: true},
	{Table: "knowledge_bases", Column: "document_count", NotNull: true},
	{Table: "knowledge_documents", Column: "tenant_id", NotNull: true},
	{Table: "knowledge_documents", Column: "index_status", NotNull: true},
	{Table: "knowledge_ingestion_jobs", Column: "tenant_id", NotNull: true},
	{Table: "knowledge_ingestion_jobs", Column: "knowledge_base_id", NotNull: true},
	{Table: "knowledge_ingestion_jobs", Column: "status", NotNull: true},
	{Table: "cross_group_share_policies", Column: "tenant_id", NotNull: true},
	{Table: "cross_group_share_policies", Column: "source_group_id", NotNull: true},
	{Table: "cross_group_share_policies", Column: "target_group_id", NotNull: true},
	{Table: "cross_group_share_policies", Column: "redaction_policy", NotNull: true},
	{Table: "cross_group_share_policies", Column: "status", NotNull: true},
	{Table: "group_task_bindings", Column: "tenant_id", NotNull: true},
	{Table: "skill_update_requests", Column: "tenant_id", NotNull: true},
	{Table: "agent_capabilities", Column: "tenant_id", NotNull: true},
	{Table: "agent_draft_requests", Column: "tenant_id", NotNull: true},
	{Table: "tone_policies", Column: "tenant_id", NotNull: true},
}

var RequiredLiveIndexes = []string{
	"idx_tasks_tenant_status",
	"idx_task_events_task_time",
	"idx_tool_calls_tenant_run",
	"idx_trace_events_tenant_trace_time",
	"idx_memory_events_tenant_agent",
	"idx_memory_events_tenant_user",
	"idx_policy_versions_lookup",
	"idx_agent_package_canary_hits_tenant_agent",
	"idx_agent_assets_tenant_status",
	"idx_agent_package_proposals_tenant_draft",
	"idx_agent_prompt_profiles_agent_status",
	"idx_agent_skill_definitions_agent_status",
	"idx_agent_tool_bindings_agent_status",
	"idx_agent_collaborators_agent_status",
	"idx_agent_exported_tools_agent_status",
	"idx_eval_suites_tenant",
	"idx_eval_suite_results_tenant_suite",
	"idx_group_members_group",
	"idx_group_permission_policies_group",
	"idx_memory_scopes_owner_group",
	"idx_knowledge_bases_tenant_group",
	"idx_knowledge_documents_tenant_base",
	"idx_knowledge_ingestion_jobs_base",
	"idx_cross_group_share_policies_groups",
	"idx_group_task_bindings_group_time",
	"idx_group_task_bindings_task",
	"idx_skill_update_requests_group",
	"idx_agent_capabilities_tenant_agent",
	"idx_agent_draft_requests_group",
	"idx_service_connections_tenant_type",
	"idx_service_connections_tenant_status",
	"idx_service_connection_resources_connection",
	"idx_service_connection_health_events_connection",
	"idx_service_connection_secret_rotations_connection",
	"idx_tool_providers_tenant_status",
	"idx_tool_providers_tenant_health",
	"idx_tool_providers_tenant_connection",
	"idx_tool_adapter_operations_tenant_provider",
	"idx_tool_adapter_operations_connection",
	"idx_tool_groups_tenant_status",
	"idx_tool_manifests_tenant_group",
	"idx_tool_manifests_tenant_status",
	"idx_tool_manifest_versions_tool",
	"idx_runtime_hook_providers_tenant_status",
	"idx_runtime_hook_providers_tenant_health",
	"idx_runtime_hook_manifests_phase",
	"idx_agent_runtime_hook_bindings_agent_phase",
	"idx_runtime_hook_events_run",
	"idx_runtime_hook_events_trace",
}

func ValidateLiveSchema(ctx context.Context, inspector LiveInspector, migrations []Migration) (LiveSchemaReport, error) {
	report := LiveSchemaReport{Status: "ready"}
	tables, err := inspector.Tables(ctx)
	if err != nil {
		return LiveSchemaReport{Status: "not_ready"}, err
	}
	for _, table := range RequiredLiveTables {
		if !tables[table] {
			report.MissingTables = append(report.MissingTables, table)
		}
	}
	columnCache := map[string]map[string]ColumnInfo{}
	for _, requirement := range RequiredLiveColumns {
		if !tables[requirement.Table] {
			continue
		}
		columns, ok := columnCache[requirement.Table]
		if !ok {
			columns, err = inspector.Columns(ctx, requirement.Table)
			if err != nil {
				return LiveSchemaReport{Status: "not_ready"}, err
			}
			columnCache[requirement.Table] = columns
		}
		column, ok := columns[requirement.Column]
		if !ok {
			report.MissingColumns = append(report.MissingColumns, requirement.Table+"."+requirement.Column)
			continue
		}
		if requirement.NotNull && !column.NotNull {
			report.NullableColumns = append(report.NullableColumns, requirement.Table+"."+requirement.Column)
		}
	}
	indexes, err := inspector.Indexes(ctx)
	if err != nil {
		return LiveSchemaReport{Status: "not_ready"}, err
	}
	for _, index := range RequiredLiveIndexes {
		if !indexes[index] {
			report.MissingIndexes = append(report.MissingIndexes, index)
		}
	}
	if tables["clean_core_schema_migrations"] {
		applied, err := inspector.AppliedMigrations(ctx)
		if err != nil {
			return LiveSchemaReport{Status: "not_ready"}, err
		}
		for _, migration := range migrations {
			record, ok := applied[migration.Version]
			if !ok {
				report.MissingMigrations = append(report.MissingMigrations, migration.Version)
				continue
			}
			if record.Checksum != checksum(migration.SQL) {
				report.ChecksumMismatches = append(report.ChecksumMismatches, migration.Version)
			}
		}
	}
	sort.Strings(report.MissingTables)
	sort.Strings(report.MissingColumns)
	sort.Strings(report.NullableColumns)
	sort.Strings(report.MissingIndexes)
	sort.Strings(report.MissingMigrations)
	sort.Strings(report.ChecksumMismatches)
	if len(report.MissingTables)+len(report.MissingColumns)+len(report.NullableColumns)+len(report.MissingIndexes)+len(report.MissingMigrations)+len(report.ChecksumMismatches) > 0 {
		report.Status = "not_ready"
	}
	return report, nil
}

type PostgresInspector struct {
	DB *sql.DB
}

func (i PostgresInspector) Tables(ctx context.Context) (map[string]bool, error) {
	rows, err := i.DB.QueryContext(ctx, `
SELECT table_name FROM information_schema.tables
WHERE table_schema='public' AND table_type='BASE TABLE'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (i PostgresInspector) Columns(ctx context.Context, table string) (map[string]ColumnInfo, error) {
	rows, err := i.DB.QueryContext(ctx, `
SELECT column_name, is_nullable, data_type
FROM information_schema.columns
WHERE table_schema='public' AND table_name=$1`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ColumnInfo{}
	for rows.Next() {
		var name, nullable, dataType string
		if err := rows.Scan(&name, &nullable, &dataType); err != nil {
			return nil, err
		}
		out[name] = ColumnInfo{Name: name, NotNull: nullable == "NO", DataType: dataType}
	}
	return out, rows.Err()
}

func (i PostgresInspector) Indexes(ctx context.Context) (map[string]bool, error) {
	rows, err := i.DB.QueryContext(ctx, `SELECT indexname FROM pg_indexes WHERE schemaname='public'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (i PostgresInspector) AppliedMigrations(ctx context.Context) (map[string]AppliedMigration, error) {
	rows, err := i.DB.QueryContext(ctx, `SELECT version, name, checksum, applied_at FROM clean_core_schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AppliedMigration{}
	for rows.Next() {
		var record AppliedMigration
		if err := rows.Scan(&record.Version, &record.Name, &record.Checksum, &record.AppliedAt); err != nil {
			return nil, err
		}
		record.AppliedAt = record.AppliedAt.UTC()
		out[record.Version] = record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r LiveSchemaReport) Details() string {
	if r.Status == "ready" {
		return "live database schema is ready"
	}
	return fmt.Sprintf("missing_tables=%v missing_columns=%v nullable_columns=%v missing_indexes=%v missing_migrations=%v checksum_mismatches=%v",
		r.MissingTables, r.MissingColumns, r.NullableColumns, r.MissingIndexes, r.MissingMigrations, r.ChecksumMismatches)
}
