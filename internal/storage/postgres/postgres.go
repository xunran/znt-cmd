package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/agentdelegation"
	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/eval"
	"znt/internal/governance/audit"
	tracequery "znt/internal/governance/trace"
	runtimehook "znt/internal/runtime/hook"
	runrepo "znt/internal/runtime/run"
	"znt/internal/storage/migration"
	storagerepo "znt/internal/storage/repository"
	toolcatalog "znt/internal/tool/catalog"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type Repositories struct {
	DB                  *sql.DB
	Tasks               *TaskStore
	Runs                *RunRepository
	Tools               *ToolRepository
	Conversations       *ConversationStore
	ToolCatalog         *ToolCatalogStore
	ServiceConnections  *ServiceConnectionStore
	RuntimeHooks        *RuntimeHookStore
	AgentDelegations    *AgentDelegationStore
	Trace               *TraceRecorder
	Audit               *AuditLogger
	Artifacts           *ArtifactStore
	Memory              *MemoryStore
	ContextPackages     *ContextPackageStore
	Plans               *PlanRepository
	Handoffs            *HandoffRepository
	GovernanceProcesses *GovernanceProcessStore
	Packages            *PackageStore
	Policies            *PolicyStore
	Evals               *EvalStore
	ExternalTasks       *ExternalTaskBindingStore
	GroupMembers        *GroupMemberStore
	GroupPermissions    *GroupPermissionPolicyStore
	MemoryScopes        *MemoryScopeStore
	Knowledge           *KnowledgeStore
	CrossGroupShares    *CrossGroupSharePolicyStore
	GroupTaskBindings   *GroupTaskBindingStore
	SkillUpdates        *SkillUpdateRequestStore
	AgentCapabilities   *AgentCapabilityStore
	AgentDraftRequests  *AgentDraftRequestStore
	TonePolicies        *TonePolicyStore
	Migrations          *MigrationStore
}

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func Open(ctx context.Context, databaseURL string, opts ...PoolConfig) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if len(opts) > 0 {
		applyPoolConfig(db, opts[0])
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applyPoolConfig(db *sql.DB, cfg PoolConfig) {
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
}

func NewRepositories(db *sql.DB) *Repositories {
	auditLogger := &AuditLogger{db: db}
	return &Repositories{
		DB:                  db,
		Tasks:               &TaskStore{db: db},
		Runs:                &RunRepository{db: db, now: time.Now},
		Tools:               &ToolRepository{db: db},
		Conversations:       &ConversationStore{db: db},
		ToolCatalog:         &ToolCatalogStore{db: db},
		ServiceConnections:  &ServiceConnectionStore{db: db},
		RuntimeHooks:        &RuntimeHookStore{db: db},
		AgentDelegations:    &AgentDelegationStore{db: db},
		Trace:               &TraceRecorder{db: db},
		Audit:               auditLogger,
		Artifacts:           &ArtifactStore{db: db},
		Memory:              &MemoryStore{db: db, audit: auditLogger, now: time.Now},
		ContextPackages:     &ContextPackageStore{db: db},
		Plans:               &PlanRepository{db: db},
		Handoffs:            &HandoffRepository{db: db},
		GovernanceProcesses: &GovernanceProcessStore{db: db},
		Packages:            &PackageStore{db: db},
		Policies:            &PolicyStore{db: db},
		Evals:               &EvalStore{db: db},
		ExternalTasks:       &ExternalTaskBindingStore{db: db},
		GroupMembers:        &GroupMemberStore{db: db},
		GroupPermissions:    &GroupPermissionPolicyStore{db: db},
		MemoryScopes:        &MemoryScopeStore{db: db},
		Knowledge:           &KnowledgeStore{db: db},
		CrossGroupShares:    &CrossGroupSharePolicyStore{db: db},
		GroupTaskBindings:   &GroupTaskBindingStore{db: db},
		SkillUpdates:        &SkillUpdateRequestStore{db: db},
		AgentCapabilities:   &AgentCapabilityStore{db: db},
		AgentDraftRequests:  &AgentDraftRequestStore{db: db},
		TonePolicies:        &TonePolicyStore{db: db},
		Migrations:          &MigrationStore{db: db},
	}
}

type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type PolicyStore struct {
	db *sql.DB
}

func (s *PolicyStore) Get(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) (contracts.PolicySet, bool, error) {
	policy, ok, err := s.get(ctx, tenantID, policySetID)
	if err != nil || ok || tenantID == "" {
		return policy, ok, err
	}
	return s.get(ctx, "", policySetID)
}

func (s *PolicyStore) Put(ctx context.Context, policy contracts.PolicySet) error {
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = time.Now().UTC()
	}
	value, err := jsonValue(policy)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO policy_sets (tenant_id, policy_set_id, version, policy_json, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, policy_set_id)
DO UPDATE SET version=EXCLUDED.version, policy_json=EXCLUDED.policy_json, created_at=EXCLUDED.created_at`,
		policy.TenantID, policy.PolicySetID, policy.Version, value, policy.CreatedAt.UTC(),
	)
	return err
}

func (s *PolicyStore) SaveDraft(ctx context.Context, draft contracts.PolicyDraft) error {
	value, err := jsonValue(draft.Policy)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO policy_drafts (
  draft_id, tenant_id, policy_set_id, version, policy_json, status,
  created_by, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (draft_id) DO UPDATE SET
  version=EXCLUDED.version,
  policy_json=EXCLUDED.policy_json,
  status=EXCLUDED.status,
  updated_at=EXCLUDED.updated_at`,
		draft.DraftID, draft.TenantID, draft.PolicySetID, draft.Version, value, draft.Status,
		draft.CreatedBy, draft.CreatedAt.UTC(), draft.UpdatedAt.UTC(),
	)
	return err
}

func (s *PolicyStore) GetDraft(ctx context.Context, draftID string) (contracts.PolicyDraft, bool, error) {
	var tenantID, policySetID, version, status string
	var policyJSON []byte
	draft := contracts.PolicyDraft{}
	err := s.db.QueryRowContext(ctx, `
SELECT draft_id, tenant_id, policy_set_id, version, policy_json, status, created_by, created_at, updated_at
FROM policy_drafts WHERE draft_id=$1`, draftID).
		Scan(&draft.DraftID, &tenantID, &policySetID, &version, &policyJSON, &status, &draft.CreatedBy, &draft.CreatedAt, &draft.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.PolicyDraft{}, false, nil
	}
	if err != nil {
		return contracts.PolicyDraft{}, false, mapSQLError(err)
	}
	draft.TenantID = contracts.TenantID(tenantID)
	draft.PolicySetID = contracts.PolicySetID(policySetID)
	draft.Version = version
	draft.Status = contracts.ReleaseStatus(status)
	if err := scanJSON(policyJSON, &draft.Policy); err != nil {
		return contracts.PolicyDraft{}, false, err
	}
	draft.CreatedAt = draft.CreatedAt.UTC()
	draft.UpdatedAt = draft.UpdatedAt.UTC()
	return draft, true, nil
}

func (s *PolicyStore) SaveVersion(ctx context.Context, version contracts.PolicyVersion, policy contracts.PolicySet) error {
	value, err := jsonValue(policy)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO policy_versions (
  policy_version_id, tenant_id, policy_set_id, version, status, policy_hash,
  policy_json, created_by, created_at, published_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (tenant_id, policy_set_id, version) DO UPDATE SET
  status=EXCLUDED.status,
  policy_hash=EXCLUDED.policy_hash,
  policy_json=EXCLUDED.policy_json,
  published_at=EXCLUDED.published_at`,
		version.PolicyVersionID, version.TenantID, version.PolicySetID, version.Version, version.Status,
		version.PolicyHash, value, version.CreatedBy, version.CreatedAt.UTC(), nullTime(version.PublishedAt),
	)
	if err != nil {
		return err
	}
	if version.Status == contracts.ReleaseStable {
		if _, err := tx.ExecContext(ctx, `
UPDATE policy_versions SET status=$4
WHERE tenant_id=$1 AND policy_set_id=$2 AND policy_version_id<>$3 AND status=$5`,
			version.TenantID, version.PolicySetID, version.PolicyVersionID, contracts.ReleaseDeprecated, contracts.ReleaseStable); err != nil {
			return err
		}
		if err := (&PolicyStore{db: s.db}).putWithTx(ctx, tx, policy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PolicyStore) UpdateVersionStatus(ctx context.Context, policyVersionID contracts.PolicyVersionID, status contracts.ReleaseStatus) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE policy_versions SET status=$2 WHERE policy_version_id=$1`, policyVersionID, status)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (s *PolicyStore) GetVersion(ctx context.Context, policyVersionID contracts.PolicyVersionID) (contracts.PolicyVersion, contracts.PolicySet, bool, error) {
	version, policy, err := scanPolicyVersion(s.db.QueryRowContext(ctx, policyVersionSelectSQL()+" WHERE policy_version_id=$1", policyVersionID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false, nil
	}
	return version, policy, err == nil, err
}

func (s *PolicyStore) ListVersions(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) ([]contracts.PolicyVersion, error) {
	rows, err := s.db.QueryContext(ctx, policyVersionSelectSQL()+" WHERE tenant_id=$1 AND policy_set_id=$2 ORDER BY created_at ASC", tenantID, policySetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.PolicyVersion, 0)
	for rows.Next() {
		version, _, err := scanPolicyVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

func (s *PolicyStore) putWithTx(ctx context.Context, q dbtx, policy contracts.PolicySet) error {
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = time.Now().UTC()
	}
	value, err := jsonValue(policy)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
INSERT INTO policy_sets (tenant_id, policy_set_id, version, policy_json, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, policy_set_id)
DO UPDATE SET version=EXCLUDED.version, policy_json=EXCLUDED.policy_json, created_at=EXCLUDED.created_at`,
		policy.TenantID, policy.PolicySetID, policy.Version, value, policy.CreatedAt.UTC(),
	)
	return err
}

func (s *PolicyStore) get(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) (contracts.PolicySet, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT policy_json FROM policy_sets
WHERE tenant_id=$1 AND policy_set_id=$2`, tenantID, policySetID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.PolicySet{}, false, nil
	}
	if err != nil {
		return contracts.PolicySet{}, false, mapSQLError(err)
	}
	var policy contracts.PolicySet
	if err := scanJSON(data, &policy); err != nil {
		return contracts.PolicySet{}, false, err
	}
	return policy, true, nil
}

func policyVersionSelectSQL() string {
	return `SELECT policy_version_id, tenant_id, policy_set_id, version, status,
policy_hash, policy_json, created_by, created_at, published_at FROM policy_versions`
}

func scanPolicyVersion(row interface {
	Scan(dest ...any) error
}) (contracts.PolicyVersion, contracts.PolicySet, error) {
	var id, tenantID, policySetID, status string
	var policyJSON []byte
	var published sql.NullTime
	version := contracts.PolicyVersion{}
	err := row.Scan(&id, &tenantID, &policySetID, &version.Version, &status,
		&version.PolicyHash, &policyJSON, &version.CreatedBy, &version.CreatedAt, &published)
	if err != nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, mapSQLError(err)
	}
	version.PolicyVersionID = contracts.PolicyVersionID(id)
	version.TenantID = contracts.TenantID(tenantID)
	version.PolicySetID = contracts.PolicySetID(policySetID)
	version.Status = contracts.ReleaseStatus(status)
	version.CreatedAt = version.CreatedAt.UTC()
	version.PublishedAt = timePtr(published)
	var policy contracts.PolicySet
	if err := scanJSON(policyJSON, &policy); err != nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, err
	}
	return version, policy, nil
}

func jsonValue(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func nullableJSONValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	return jsonValue(value)
}

func jsonBytes(value any) []byte {
	data, err := jsonValue(value)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scanJSON(data []byte, dst any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

func taskIDPtr(value sql.NullString) *contracts.TaskID {
	if !value.Valid {
		return nil
	}
	out := contracts.TaskID(value.String)
	return &out
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return storagerepo.ErrNotFound
	}
	return err
}

func isSchemaNotReadyError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "42P01", "42703": // undefined_table, undefined_column
		return true
	default:
		return false
	}
}

func duplicateIfNoRows(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrDuplicateRequest
	}
	return nil
}

func conflictOrNotFound(ctx context.Context, q dbtx, table string, idColumn string, id any) error {
	var exists bool
	query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)", table, idColumn)
	if err := q.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return storagerepo.ErrNotFound
	}
	return storagerepo.ErrConflict
}

type TaskStore struct {
	db *sql.DB
}

func (s *TaskStore) Create(ctx context.Context, task contracts.Task) error {
	result, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (
  task_id, tenant_id, parent_task_id, root_task_id, title, objective, description,
  status, owner_agent_id, assigned_agent_id, source_handoff_id,
  agent_id, agent_version, policy_set_id, schema_version, version,
  created_at, updated_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (task_id) DO NOTHING`,
		task.TaskID, task.TenantID, taskIDArg(task.ParentTaskID), taskIDArg(task.RootTaskID),
		task.Title, task.Objective, nullString(task.Description), task.Status,
		nullString(string(task.OwnerAgentID)), nullString(string(task.AssignedAgentID)), handoffIDArg(task.SourceHandoffID),
		task.AgentID, task.AgentVersion, task.PolicySetID, nullString(task.SchemaVersion), task.Version,
		task.CreatedAt.UTC(), task.UpdatedAt.UTC(), nullTime(task.CompletedAt),
	)
	return duplicateIfNoRows(result, err)
}

func (s *TaskStore) CreateTaskAndAppendEvent(ctx context.Context, task contracts.Task, event contracts.TaskEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := (&TaskStoreTx{tx: tx}).Create(ctx, task); err != nil {
		return err
	}
	if err := appendTaskEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) Get(ctx context.Context, taskID contracts.TaskID) (contracts.Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, taskSelectSQL()+" WHERE task_id = $1", taskID))
}

func (s *TaskStore) UpdateWithVersion(ctx context.Context, task contracts.Task, expectedVersion int64) error {
	return updateTask(ctx, s.db, task, expectedVersion)
}

func (s *TaskStore) UpdateWithVersionAndAppendEvent(ctx context.Context, task contracts.Task, expectedVersion int64, event contracts.TaskEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTask(ctx, tx, task, expectedVersion); err != nil {
		return err
	}
	if err := appendTaskEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) ListByTenantStatus(ctx context.Context, tenantID contracts.TenantID, status contracts.TaskStatus) ([]contracts.Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelectSQL()+" WHERE tenant_id = $1 AND status = $2 ORDER BY created_at ASC", tenantID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *TaskStore) Append(ctx context.Context, event contracts.TaskEvent) error {
	return appendTaskEvent(ctx, s.db, event)
}

func (s *TaskStore) ListByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TaskEvent, error) {
	return s.listByTask(ctx, taskID, 0)
}

func (s *TaskStore) ListByTaskLimit(ctx context.Context, taskID contracts.TaskID, limit int) ([]contracts.TaskEvent, error) {
	return s.listByTask(ctx, taskID, limit)
}

func (s *TaskStore) listByTask(ctx context.Context, taskID contracts.TaskID, limit int) ([]contracts.TaskEvent, error) {
	query := `
SELECT event_id, task_id, tenant_id, type, actor_id, actor_type, payload, run_id, step_id, created_at
FROM task_events
WHERE task_id = $1
ORDER BY created_at ASC, event_id ASC`
	args := []any{taskID}
	if limit > 0 {
		query = `
SELECT event_id, task_id, tenant_id, type, actor_id, actor_type, payload, run_id, step_id, created_at
FROM (
  SELECT event_id, task_id, tenant_id, type, actor_id, actor_type, payload, run_id, step_id, created_at
  FROM task_events
  WHERE task_id = $1
  ORDER BY created_at DESC, event_id DESC
  LIMIT $2
) limited_task_events
ORDER BY created_at ASC, event_id ASC`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.TaskEvent, 0)
	for rows.Next() {
		event, err := scanTaskEventRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

type TaskStoreTx struct {
	tx *sql.Tx
}

func (s *TaskStoreTx) Create(ctx context.Context, task contracts.Task) error {
	result, err := s.tx.ExecContext(ctx, `
INSERT INTO tasks (
  task_id, tenant_id, parent_task_id, root_task_id, title, objective, description,
  status, owner_agent_id, assigned_agent_id, source_handoff_id,
  agent_id, agent_version, policy_set_id, schema_version, version,
  created_at, updated_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (task_id) DO NOTHING`,
		task.TaskID, task.TenantID, taskIDArg(task.ParentTaskID), taskIDArg(task.RootTaskID),
		task.Title, task.Objective, nullString(task.Description), task.Status,
		nullString(string(task.OwnerAgentID)), nullString(string(task.AssignedAgentID)), handoffIDArg(task.SourceHandoffID),
		task.AgentID, task.AgentVersion, task.PolicySetID, nullString(task.SchemaVersion), task.Version,
		task.CreatedAt.UTC(), task.UpdatedAt.UTC(), nullTime(task.CompletedAt),
	)
	return duplicateIfNoRows(result, err)
}

func taskSelectSQL() string {
	return `SELECT task_id, tenant_id, parent_task_id, root_task_id, title, objective, description,
status, owner_agent_id, assigned_agent_id, source_handoff_id,
agent_id, agent_version, policy_set_id, schema_version, version,
created_at, updated_at, completed_at FROM tasks`
}

func scanTask(row interface {
	Scan(dest ...any) error
}) (contracts.Task, error) {
	var taskID, tenantID, status, agentID, agentVersion, policySetID string
	var parentID, rootID, description, ownerID, assignedID, sourceHandoffID, schemaVersion sql.NullString
	var completed sql.NullTime
	task := contracts.Task{}
	err := row.Scan(
		&taskID, &tenantID, &parentID, &rootID, &task.Title, &task.Objective, &description,
		&status, &ownerID, &assignedID, &sourceHandoffID,
		&agentID, &agentVersion, &policySetID, &schemaVersion, &task.Version,
		&task.CreatedAt, &task.UpdatedAt, &completed,
	)
	if err != nil {
		return contracts.Task{}, mapSQLError(err)
	}
	task.TaskID = contracts.TaskID(taskID)
	task.TenantID = contracts.TenantID(tenantID)
	task.ParentTaskID = taskIDPtr(parentID)
	task.RootTaskID = taskIDPtr(rootID)
	if description.Valid {
		task.Description = description.String
	}
	task.Status = contracts.TaskStatus(status)
	task.OwnerAgentID = contracts.AgentID(ownerID.String)
	task.AssignedAgentID = contracts.AgentID(assignedID.String)
	if sourceHandoffID.Valid {
		id := contracts.HandoffID(sourceHandoffID.String)
		task.SourceHandoffID = &id
	}
	task.AgentID = contracts.AgentID(agentID)
	task.AgentVersion = contracts.AgentVersion(agentVersion)
	task.PolicySetID = contracts.PolicySetID(policySetID)
	if schemaVersion.Valid {
		task.SchemaVersion = schemaVersion.String
	}
	task.CompletedAt = timePtr(completed)
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	return task, nil
}

func scanTasks(rows *sql.Rows) ([]contracts.Task, error) {
	out := make([]contracts.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func updateTask(ctx context.Context, q dbtx, task contracts.Task, expectedVersion int64) error {
	result, err := q.ExecContext(ctx, `
UPDATE tasks SET
  parent_task_id=$2, root_task_id=$3, title=$4, objective=$5, description=$6,
  status=$7, owner_agent_id=$8, assigned_agent_id=$9, source_handoff_id=$10,
  agent_id=$11, agent_version=$12, policy_set_id=$13, schema_version=$14,
  version=$15, updated_at=$16, completed_at=$17
WHERE task_id=$1 AND version=$18`,
		task.TaskID, taskIDArg(task.ParentTaskID), taskIDArg(task.RootTaskID), task.Title,
		task.Objective, nullString(task.Description), task.Status, nullString(string(task.OwnerAgentID)),
		nullString(string(task.AssignedAgentID)), handoffIDArg(task.SourceHandoffID), task.AgentID,
		task.AgentVersion, task.PolicySetID, nullString(task.SchemaVersion), expectedVersion+1,
		task.UpdatedAt.UTC(), nullTime(task.CompletedAt), expectedVersion,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return conflictOrNotFound(ctx, q, "tasks", "task_id", task.TaskID)
	}
	return nil
}

func appendTaskEvent(ctx context.Context, q dbtx, event contracts.TaskEvent) error {
	payload, err := jsonValue(event.Payload)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `
INSERT INTO task_events (event_id, task_id, tenant_id, type, actor_id, actor_type, payload, run_id, step_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.TaskID, event.TenantID, event.Type, event.ActorID, event.ActorType,
		payload, nullString(string(event.RunID)), nullString(event.StepID), event.CreatedAt.UTC(),
	)
	return duplicateIfNoRows(result, err)
}

func scanTaskEventRows(rows *sql.Rows) (contracts.TaskEvent, error) {
	var eventID, taskID, tenantID, runID, stepID sql.NullString
	var payload []byte
	event := contracts.TaskEvent{}
	if err := rows.Scan(&eventID, &taskID, &tenantID, &event.Type, &event.ActorID, &event.ActorType, &payload, &runID, &stepID, &event.CreatedAt); err != nil {
		return contracts.TaskEvent{}, err
	}
	event.EventID = contracts.TaskEventID(eventID.String)
	event.TaskID = contracts.TaskID(taskID.String)
	event.TenantID = contracts.TenantID(tenantID.String)
	event.RunID = contracts.AgentRunID(runID.String)
	event.StepID = stepID.String
	if err := scanJSON(payload, &event.Payload); err != nil {
		return contracts.TaskEvent{}, err
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.CreatedAt = event.CreatedAt.UTC()
	return event, nil
}

func taskIDArg(value *contracts.TaskID) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func handoffIDArg(value *contracts.HandoffID) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

type RunRepository struct {
	db  *sql.DB
	now func() time.Time
}

func (r *RunRepository) Create(ctx context.Context, run contracts.AgentRun) error {
	applyRunCarrierDefaults(&run)
	snapshot, err := jsonValue(run.VersionSnapshot)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
INSERT INTO agent_runs (
  run_id, trace_id, tenant_id, agent_id, agent_version,
  carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash,
  task_id, input, conversation_id, thread_id, message_id, status,
  step_count, tool_call_count, policy_set_id, version_snapshot,
  started_at, completed_at, error_code, error_message
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
ON CONFLICT (run_id) DO NOTHING`,
		run.RunID, run.TraceID, run.TenantID, run.AgentID, run.AgentVersion,
		run.CarrierKind, run.RuntimeContract, run.SourceKind, nullString(run.SourceProviderID), nullString(string(run.CarrierVersion)), nullString(run.ManifestHash),
		nullString(string(run.TaskID)),
		run.Input, nullString(run.ConversationID), nullString(run.ThreadID), nullString(run.MessageID),
		run.Status, run.StepCount, run.ToolCallCount, run.PolicySetID, snapshot,
		run.StartedAt.UTC(), nullTime(run.CompletedAt), nullString(string(run.ErrorCode)), nullString(run.ErrorMessage),
	)
	return duplicateIfNoRows(result, err)
}

func (r *RunRepository) Get(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return scanRun(r.db.QueryRowContext(ctx, runSelectSQL()+" WHERE run_id=$1", runID))
}

func (r *RunRepository) List(ctx context.Context, filter runrepo.ListFilter) ([]contracts.AgentRun, error) {
	where := make([]string, 0)
	args := make([]any, 0)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.TenantID != "" {
		add("tenant_id=$%d", filter.TenantID)
	}
	if filter.AgentID != "" {
		add("agent_id=$%d", filter.AgentID)
	}
	if filter.Status != "" {
		add("status=$%d", filter.Status)
	}
	if filter.TraceID != "" {
		add("trace_id=$%d", filter.TraceID)
	}
	if filter.TaskID != "" {
		add("task_id=$%d", filter.TaskID)
	}
	if !filter.From.IsZero() {
		add("started_at >= $%d", filter.From.UTC())
	}
	if !filter.To.IsZero() {
		add("started_at <= $%d", filter.To.UTC())
	}
	query := runSelectSQL()
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at DESC, run_id DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.AgentRun, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *RunRepository) MarkRunning(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.updateStatus(ctx, runID, contracts.RunRunning, nil, nil)
}

func (r *RunRepository) MarkCompleted(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	now := r.now().UTC()
	return r.updateStatus(ctx, runID, contracts.RunCompleted, &now, nil)
}

func (r *RunRepository) MarkFailed(ctx context.Context, runID contracts.AgentRunID, runtimeErr *contracts.RuntimeError) (contracts.AgentRun, error) {
	now := r.now().UTC()
	return r.updateStatus(ctx, runID, contracts.RunFailed, &now, runtimeErr)
}

func (r *RunRepository) MarkWaitingInput(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.updateStatus(ctx, runID, contracts.RunWaitingInput, nil, nil)
}

func (r *RunRepository) MarkWaitingApproval(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return r.updateStatus(ctx, runID, contracts.RunWaitingApproval, nil, nil)
}

func (r *RunRepository) MarkCancelled(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	now := r.now().UTC()
	return r.updateStatus(ctx, runID, contracts.RunCancelled, &now, nil)
}

func (r *RunRepository) UpdateVersionSnapshot(ctx context.Context, runID contracts.AgentRunID, snapshot contracts.VersionSnapshot) (contracts.AgentRun, error) {
	value, err := jsonValue(snapshot)
	if err != nil {
		return contracts.AgentRun{}, err
	}
	return scanRun(r.db.QueryRowContext(ctx, `
UPDATE agent_runs SET
  version_snapshot=$2,
  carrier_kind=$3,
  runtime_contract=$4,
  source_kind=$5,
  source_provider_id=$6,
  carrier_version=$7,
  manifest_hash=$8
WHERE run_id=$1
RETURNING run_id, trace_id, tenant_id, agent_id, agent_version, task_id, status,
input, conversation_id, thread_id, message_id, step_count, tool_call_count, policy_set_id, version_snapshot, started_at, completed_at, error_code, error_message,
carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash`,
		runID, value, snapshot.CarrierKind, snapshot.RuntimeContract, snapshot.SourceKind, nullString(snapshot.SourceProviderID), nullString(string(snapshot.CarrierVersion)), nullString(snapshot.ManifestHash),
	))
}

func (r *RunRepository) IncrementStep(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, string, error) {
	run, err := scanRun(r.db.QueryRowContext(ctx, `
UPDATE agent_runs SET step_count = step_count + 1
WHERE run_id=$1
RETURNING run_id, trace_id, tenant_id, agent_id, agent_version, task_id, status,
input, conversation_id, thread_id, message_id, step_count, tool_call_count, policy_set_id, version_snapshot, started_at, completed_at, error_code, error_message,
carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash`, runID))
	if err != nil {
		return contracts.AgentRun{}, "", err
	}
	return run, fmt.Sprintf("step_%s_%d", run.RunID, run.StepCount), nil
}

func (r *RunRepository) IncrementToolCall(ctx context.Context, runID contracts.AgentRunID) (contracts.AgentRun, error) {
	return scanRun(r.db.QueryRowContext(ctx, `
UPDATE agent_runs SET tool_call_count = tool_call_count + 1
WHERE run_id=$1
RETURNING run_id, trace_id, tenant_id, agent_id, agent_version, task_id, status,
input, conversation_id, thread_id, message_id, step_count, tool_call_count, policy_set_id, version_snapshot, started_at, completed_at, error_code, error_message,
carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash`, runID))
}

func (r *RunRepository) updateStatus(ctx context.Context, runID contracts.AgentRunID, status contracts.RunStatus, completedAt *time.Time, runtimeErr *contracts.RuntimeError) (contracts.AgentRun, error) {
	var code, message string
	if runtimeErr != nil {
		code = string(runtimeErr.Code)
		message = runtimeErr.Message
	}
	return scanRun(r.db.QueryRowContext(ctx, `
UPDATE agent_runs
SET status=$2, completed_at=$3, error_code=$4, error_message=$5
WHERE run_id=$1
RETURNING run_id, trace_id, tenant_id, agent_id, agent_version, task_id, status,
input, conversation_id, thread_id, message_id, step_count, tool_call_count, policy_set_id, version_snapshot, started_at, completed_at, error_code, error_message,
carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash`,
		runID, status, nullTime(completedAt), nullString(code), nullString(message),
	))
}

func runSelectSQL() string {
	return `SELECT run_id, trace_id, tenant_id, agent_id, agent_version, task_id, status,
input, conversation_id, thread_id, message_id, step_count, tool_call_count, policy_set_id, version_snapshot, started_at, completed_at, error_code, error_message,
carrier_kind, runtime_contract, source_kind, source_provider_id, carrier_version, manifest_hash
FROM agent_runs`
}

func scanRun(row interface {
	Scan(dest ...any) error
}) (contracts.AgentRun, error) {
	var runID, traceID, tenantID, agentID, agentVersion, taskID, status, policySetID string
	var input, conversationID, threadID, messageID sql.NullString
	var carrierKind, runtimeContract, sourceKind string
	var sourceProviderID, carrierVersion, manifestHash sql.NullString
	var snapshot []byte
	var completed sql.NullTime
	var code, message sql.NullString
	run := contracts.AgentRun{}
	err := row.Scan(&runID, &traceID, &tenantID, &agentID, &agentVersion, &taskID, &status,
		&input, &conversationID, &threadID, &messageID, &run.StepCount, &run.ToolCallCount, &policySetID, &snapshot, &run.StartedAt,
		&completed, &code, &message, &carrierKind, &runtimeContract, &sourceKind, &sourceProviderID, &carrierVersion, &manifestHash)
	if err != nil {
		return contracts.AgentRun{}, mapSQLError(err)
	}
	run.RunID = contracts.AgentRunID(runID)
	run.TraceID = contracts.TraceID(traceID)
	run.TenantID = contracts.TenantID(tenantID)
	run.AgentID = contracts.AgentID(agentID)
	run.AgentVersion = contracts.AgentVersion(agentVersion)
	run.CarrierKind = contracts.AgentCarrierKind(carrierKind)
	run.RuntimeContract = contracts.RuntimeContractKind(runtimeContract)
	run.SourceKind = contracts.AgentSourceKind(sourceKind)
	run.SourceProviderID = sourceProviderID.String
	run.CarrierVersion = contracts.AgentVersion(carrierVersion.String)
	run.ManifestHash = manifestHash.String
	run.TaskID = contracts.TaskID(taskID)
	run.Input = input.String
	run.ConversationID = conversationID.String
	run.ThreadID = threadID.String
	run.MessageID = messageID.String
	run.Status = contracts.RunStatus(status)
	run.PolicySetID = contracts.PolicySetID(policySetID)
	if err := scanJSON(snapshot, &run.VersionSnapshot); err != nil {
		return contracts.AgentRun{}, err
	}
	applyRunCarrierDefaults(&run)
	run.CompletedAt = timePtr(completed)
	run.ErrorCode = contracts.ErrorCode(code.String)
	run.ErrorMessage = message.String
	run.StartedAt = run.StartedAt.UTC()
	return run, nil
}

func applyRunCarrierDefaults(run *contracts.AgentRun) {
	contracts.NormalizeRunCarrierSnapshot(run)
}

type ConversationStore struct {
	db *sql.DB
}

func (s *ConversationStore) UpsertThread(ctx context.Context, thread conversationstore.Thread) error {
	if thread.ThreadID == "" {
		thread.ThreadID = thread.ConversationID
	}
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = time.Now().UTC()
	}
	if thread.UpdatedAt.IsZero() {
		thread.UpdatedAt = thread.CreatedAt
	}
	refs, err := jsonValue(thread.ExternalRefs)
	if err != nil {
		return err
	}
	var lastMessageAt sql.NullTime
	if !thread.LastMessageAt.IsZero() {
		lastMessageAt = sql.NullTime{Time: thread.LastMessageAt.UTC(), Valid: true}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO conversation_threads (
  tenant_id, conversation_id, thread_id, kind, provider, external_refs, last_message_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (tenant_id, conversation_id, thread_id)
DO UPDATE SET
  kind=EXCLUDED.kind,
  provider=EXCLUDED.provider,
  external_refs=EXCLUDED.external_refs,
  last_message_at=EXCLUDED.last_message_at,
  updated_at=EXCLUDED.updated_at`,
		thread.TenantID, thread.ConversationID, thread.ThreadID, thread.Kind, thread.Provider, refs,
		lastMessageAt, thread.CreatedAt.UTC(), thread.UpdatedAt.UTC(),
	)
	return err
}

func (s *ConversationStore) GetThread(ctx context.Context, tenantID contracts.TenantID, conversationID string, threadID string) (conversationstore.Thread, error) {
	if threadID == "" {
		threadID = conversationID
	}
	return scanConversationThread(s.db.QueryRowContext(ctx, conversationThreadSelectSQL()+` WHERE tenant_id=$1 AND conversation_id=$2 AND thread_id=$3`, tenantID, conversationID, threadID))
}

func (s *ConversationStore) ListThreads(ctx context.Context, tenantID contracts.TenantID, kind string, limit int, offset int) ([]conversationstore.Thread, error) {
	args := []any{tenantID}
	query := conversationThreadSelectSQL() + ` WHERE tenant_id=$1`
	if kind != "" {
		args = append(args, kind)
		query += fmt.Sprintf(" AND kind=$%d", len(args))
	}
	query += ` ORDER BY updated_at DESC, conversation_id DESC`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if offset > 0 {
		args = append(args, offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]conversationstore.Thread, 0)
	for rows.Next() {
		thread, err := scanConversationThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

func (s *ConversationStore) AppendMessage(ctx context.Context, message conversationstore.MessageRecord) error {
	if message.ThreadID == "" {
		message.ThreadID = message.ConversationID
	}
	if message.Message.ThreadID == "" {
		message.Message.ThreadID = message.ThreadID
	}
	mentions, err := jsonValue(message.Message.Mentions)
	if err != nil {
		return err
	}
	metadata, err := jsonValue(message.Metadata)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO conversation_messages (
  tenant_id, conversation_id, thread_id, message_id, external_message_id,
  speaker_id, speaker_type, speaker_name, text, reply_to_message_id, mentions, metadata, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (tenant_id, conversation_id, message_id) DO NOTHING`,
		message.TenantID, message.ConversationID, message.ThreadID, message.Message.MessageID, nullString(message.Message.ExternalMessageID),
		message.Message.SpeakerID, message.Message.SpeakerType, nullString(message.Message.SpeakerName), message.Message.Text,
		nullString(message.Message.ReplyToMessageID), mentions, metadata, message.Message.CreatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	existing, err := s.GetMessage(ctx, message.TenantID, message.ConversationID, message.Message.MessageID)
	if err != nil {
		return err
	}
	if existing.Text == message.Message.Text &&
		existing.SpeakerID == message.Message.SpeakerID &&
		existing.SpeakerType == message.Message.SpeakerType &&
		existing.ThreadID == message.Message.ThreadID {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeTaskConflict, "conversation message id already exists with different content", map[string]any{
		"conversation_id": message.ConversationID,
		"message_id":      message.Message.MessageID,
	})
}

func (s *ConversationStore) RecentMessages(ctx context.Context, tenantID contracts.TenantID, conversationID string, threadID string, limit int) ([]contracts.ConversationMessage, error) {
	if threadID == "" {
		threadID = conversationID
	}
	args := []any{tenantID, conversationID, threadID}
	query := conversationMessageSelectSQL() + ` WHERE tenant_id=$1 AND conversation_id=$2 AND thread_id=$3 ORDER BY created_at DESC, message_id DESC`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ConversationMessage, 0)
	for rows.Next() {
		message, err := scanConversationMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *ConversationStore) GetMessage(ctx context.Context, tenantID contracts.TenantID, conversationID string, messageID string) (contracts.ConversationMessage, error) {
	return scanConversationMessage(s.db.QueryRowContext(ctx, conversationMessageSelectSQL()+` WHERE tenant_id=$1 AND conversation_id=$2 AND message_id=$3`, tenantID, conversationID, messageID))
}

func conversationMessageSelectSQL() string {
	return `SELECT message_id, external_message_id, speaker_id, speaker_type, speaker_name, text, created_at, reply_to_message_id, thread_id, mentions, metadata FROM conversation_messages`
}

func scanConversationMessage(row interface {
	Scan(dest ...any) error
}) (contracts.ConversationMessage, error) {
	var message contracts.ConversationMessage
	var externalMessageID, speakerName, replyToMessageID sql.NullString
	var mentions, metadata []byte
	if err := row.Scan(&message.MessageID, &externalMessageID, &message.SpeakerID, &message.SpeakerType, &speakerName, &message.Text, &message.CreatedAt, &replyToMessageID, &message.ThreadID, &mentions, &metadata); err != nil {
		return contracts.ConversationMessage{}, mapSQLError(err)
	}
	message.ExternalMessageID = externalMessageID.String
	message.SpeakerName = speakerName.String
	message.ReplyToMessageID = replyToMessageID.String
	if err := scanJSON(mentions, &message.Mentions); err != nil {
		return contracts.ConversationMessage{}, err
	}
	_ = scanJSON(metadata, &message.Metadata)
	message.CreatedAt = message.CreatedAt.UTC()
	return message, nil
}

func conversationThreadSelectSQL() string {
	return `SELECT tenant_id, conversation_id, thread_id, kind, provider, external_refs, last_message_at, created_at, updated_at FROM conversation_threads`
}

func scanConversationThread(row interface {
	Scan(dest ...any) error
}) (conversationstore.Thread, error) {
	var thread conversationstore.Thread
	var tenantID string
	var refs []byte
	var lastMessageAt sql.NullTime
	if err := row.Scan(&tenantID, &thread.ConversationID, &thread.ThreadID, &thread.Kind, &thread.Provider, &refs, &lastMessageAt, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
		return conversationstore.Thread{}, mapSQLError(err)
	}
	thread.TenantID = contracts.TenantID(tenantID)
	if err := scanJSON(refs, &thread.ExternalRefs); err != nil {
		return conversationstore.Thread{}, err
	}
	thread.LastMessageAt = timeValue(lastMessageAt)
	thread.CreatedAt = thread.CreatedAt.UTC()
	thread.UpdatedAt = thread.UpdatedAt.UTC()
	return thread, nil
}

type ToolRepository struct {
	db *sql.DB
}

func (r *ToolRepository) SaveCall(ctx context.Context, call contracts.ToolCall) (contracts.ToolCall, bool, error) {
	if call.IdempotencyKey != "" {
		if existing, ok, err := r.getCallByIdempotencyKey(ctx, call.TenantID, call.IdempotencyKey); err != nil || ok {
			return existing, ok, err
		}
	}
	effectiveIdempotencyKey := call.IdempotencyKey
	if effectiveIdempotencyKey == "" {
		effectiveIdempotencyKey = string(call.ToolCallID)
	}
	args, err := jsonValue(call.Arguments)
	if err != nil {
		return contracts.ToolCall{}, false, err
	}
	result, err := r.db.ExecContext(ctx, `
INSERT INTO tool_calls (tool_call_id, tenant_id, trace_id, run_id, task_id, plan_step_id, tool_id, tool_name, arguments_json, idempotency_key, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (tenant_id, idempotency_key) DO NOTHING`,
		call.ToolCallID, call.TenantID, nullString(string(call.TraceID)), call.RunID, nullString(string(call.TaskID)), nullString(call.PlanStepID),
		call.ToolID, call.Name, args, effectiveIdempotencyKey, call.CreatedAt.UTC(),
	)
	if err != nil {
		return contracts.ToolCall{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contracts.ToolCall{}, false, err
	}
	if affected == 0 {
		existing, ok, err := r.getCallByIdempotencyKey(ctx, call.TenantID, call.IdempotencyKey)
		return existing, ok, err
	}
	return call, false, nil
}

func (r *ToolRepository) GetCall(ctx context.Context, callID contracts.ToolCallID) (contracts.ToolCall, bool, error) {
	call, err := scanToolCall(r.db.QueryRowContext(ctx, toolCallSelectSQL()+" WHERE tool_call_id=$1", callID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ToolCall{}, false, nil
	}
	return call, err == nil, err
}

func (r *ToolRepository) SaveResult(ctx context.Context, result contracts.ToolResult) error {
	output, err := jsonValue(result.Output)
	if err != nil {
		return err
	}
	errJSON, err := jsonValue(result.Error)
	if err != nil {
		return err
	}
	refs, err := jsonValue(result.ArtifactRefs)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO tool_results (tool_result_id, tool_call_id, status, output_json, error_json, artifact_refs, started_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (tool_call_id) DO UPDATE SET
  tool_result_id=EXCLUDED.tool_result_id,
  status=EXCLUDED.status,
  output_json=EXCLUDED.output_json,
  error_json=EXCLUDED.error_json,
  artifact_refs=EXCLUDED.artifact_refs,
  started_at=EXCLUDED.started_at,
  completed_at=EXCLUDED.completed_at`,
		result.ToolResultID, result.ToolCallID, result.Status, output, errJSON, refs,
		result.StartedAt.UTC(), result.CompletedAt.UTC(),
	)
	return err
}

func (r *ToolRepository) GetResultByCall(ctx context.Context, callID contracts.ToolCallID) (contracts.ToolResult, bool, error) {
	result, err := scanToolResult(r.db.QueryRowContext(ctx, toolResultSelectSQL()+" WHERE tool_call_id=$1", callID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ToolResult{}, false, nil
	}
	return result, err == nil, err
}

func (r *ToolRepository) GetResultByIdempotencyKey(ctx context.Context, tenantID contracts.TenantID, key string) (contracts.ToolResult, bool, error) {
	if key == "" {
		return contracts.ToolResult{}, false, nil
	}
	result, err := scanToolResult(r.db.QueryRowContext(ctx, toolResultSelectSQL()+`
 WHERE tool_call_id = (
   SELECT tool_call_id FROM tool_calls WHERE tenant_id=$1 AND idempotency_key=$2
 )`, tenantID, key))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ToolResult{}, false, nil
	}
	return result, err == nil, err
}

func (r *ToolRepository) ListCallsByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.ToolCall, error) {
	rows, err := r.db.QueryContext(ctx, toolCallSelectSQL()+" WHERE run_id=$1 ORDER BY created_at ASC", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ToolCall, 0)
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	return out, rows.Err()
}

func (r *ToolRepository) ListResultsByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.ToolResult, error) {
	return r.listResultsByRun(ctx, runID, 0)
}

func (r *ToolRepository) ListResultsByRunLimit(ctx context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ToolResult, error) {
	return r.listResultsByRun(ctx, runID, limit)
}

func (r *ToolRepository) listResultsByRun(ctx context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ToolResult, error) {
	query := toolResultSelectSQL() + `
 WHERE tool_call_id IN (SELECT tool_call_id FROM tool_calls WHERE run_id=$1)
 ORDER BY completed_at ASC`
	args := []any{runID}
	if limit > 0 {
		query = `SELECT tool_result_id, tool_call_id, status, output_json, error_json, artifact_refs, started_at, completed_at
FROM (` + toolResultSelectSQL() + `
  WHERE tool_call_id IN (SELECT tool_call_id FROM tool_calls WHERE run_id=$1)
  ORDER BY completed_at DESC, tool_result_id DESC
  LIMIT $2
) limited_tool_results
ORDER BY completed_at ASC, tool_result_id ASC`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ToolResult, 0)
	for rows.Next() {
		result, err := scanToolResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, rows.Err()
}

func (r *ToolRepository) ListArtifactRefsByRunLimit(ctx context.Context, runID contracts.AgentRunID, limit int) ([]contracts.ArtifactRef, error) {
	queryPrefix := `
WITH refs AS (
  SELECT
    ref.value AS ref_json,
    ref.value->>'artifact_id' AS artifact_id,
    tr.completed_at,
    tr.tool_result_id,
    ref.ordinality
  FROM tool_results tr
  JOIN tool_calls tc ON tr.tool_call_id = tc.tool_call_id
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(tr.artifact_refs, '[]'::jsonb)) WITH ORDINALITY AS ref(value, ordinality)
  WHERE tc.run_id = $1 AND ref.value->>'artifact_id' <> ''
),
latest AS (
  SELECT DISTINCT ON (artifact_id) ref_json, completed_at, tool_result_id, ordinality
  FROM refs
  ORDER BY artifact_id, completed_at DESC, tool_result_id DESC, ordinality ASC
)
`
	args := []any{runID}
	query := queryPrefix + `SELECT ref_json
FROM latest
ORDER BY completed_at ASC, tool_result_id ASC, ordinality ASC`
	if limit > 0 {
		query = queryPrefix + `, selected AS (
  SELECT ref_json, completed_at, tool_result_id, ordinality
  FROM latest
  ORDER BY completed_at DESC, tool_result_id DESC, ordinality DESC
  LIMIT $2
)
SELECT ref_json
FROM selected
ORDER BY completed_at ASC, tool_result_id ASC, ordinality ASC`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ArtifactRef, 0)
	for rows.Next() {
		var refJSON []byte
		if err := rows.Scan(&refJSON); err != nil {
			return nil, err
		}
		var ref contracts.ArtifactRef
		if err := json.Unmarshal(refJSON, &ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (r *ToolRepository) ListCallsByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolCall, error) {
	rows, err := r.db.QueryContext(ctx, toolCallSelectSQL()+" WHERE tenant_id=$1 AND task_id=$2 ORDER BY created_at ASC", tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ToolCall, 0)
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	return out, rows.Err()
}

func (r *ToolRepository) ListResultsByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) ([]contracts.ToolResult, error) {
	return r.listResultsByTask(ctx, tenantID, taskID, 0)
}

func (r *ToolRepository) ListResultsByTaskLimit(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, limit int) ([]contracts.ToolResult, error) {
	return r.listResultsByTask(ctx, tenantID, taskID, limit)
}

func (r *ToolRepository) listResultsByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, limit int) ([]contracts.ToolResult, error) {
	query := toolResultSelectSQL() + `
 WHERE tool_call_id IN (SELECT tool_call_id FROM tool_calls WHERE tenant_id=$1 AND task_id=$2)
 ORDER BY completed_at ASC`
	args := []any{tenantID, taskID}
	if limit > 0 {
		query = `SELECT tool_result_id, tool_call_id, status, output_json, error_json, artifact_refs, started_at, completed_at
FROM (` + toolResultSelectSQL() + `
  WHERE tool_call_id IN (SELECT tool_call_id FROM tool_calls WHERE tenant_id=$1 AND task_id=$2)
  ORDER BY completed_at DESC, tool_result_id DESC
  LIMIT $3
) limited_tool_results
ORDER BY completed_at ASC, tool_result_id ASC`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ToolResult, 0)
	for rows.Next() {
		result, err := scanToolResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, rows.Err()
}

func (r *ToolRepository) getCallByIdempotencyKey(ctx context.Context, tenantID contracts.TenantID, key string) (contracts.ToolCall, bool, error) {
	call, err := scanToolCall(r.db.QueryRowContext(ctx, toolCallSelectSQL()+" WHERE tenant_id=$1 AND idempotency_key=$2", tenantID, key))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ToolCall{}, false, nil
	}
	return call, err == nil, err
}

func toolCallSelectSQL() string {
	return `SELECT tool_call_id, tenant_id, trace_id, run_id, task_id, plan_step_id, tool_id, tool_name, arguments_json, idempotency_key, created_at FROM tool_calls`
}

func scanToolCall(row interface {
	Scan(dest ...any) error
}) (contracts.ToolCall, error) {
	var callID, tenantID, traceID, runID, taskID, planStepID, toolID, toolName sql.NullString
	var args []byte
	call := contracts.ToolCall{}
	if err := row.Scan(&callID, &tenantID, &traceID, &runID, &taskID, &planStepID, &toolID, &toolName, &args, &call.IdempotencyKey, &call.CreatedAt); err != nil {
		return contracts.ToolCall{}, mapSQLError(err)
	}
	call.ToolCallID = contracts.ToolCallID(callID.String)
	call.TenantID = contracts.TenantID(tenantID.String)
	call.TraceID = contracts.TraceID(traceID.String)
	call.RunID = contracts.AgentRunID(runID.String)
	call.TaskID = contracts.TaskID(taskID.String)
	call.PlanStepID = planStepID.String
	call.ToolID = toolID.String
	call.Name = toolName.String
	if err := scanJSON(args, &call.Arguments); err != nil {
		return contracts.ToolCall{}, err
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	call.CreatedAt = call.CreatedAt.UTC()
	return call, nil
}

func toolResultSelectSQL() string {
	return `SELECT tool_result_id, tool_call_id, status, output_json, error_json, artifact_refs, started_at, completed_at FROM tool_results`
}

func scanToolResult(row interface {
	Scan(dest ...any) error
}) (contracts.ToolResult, error) {
	var resultID, callID, status string
	var outputJSON, errorJSON, refsJSON []byte
	result := contracts.ToolResult{}
	if err := row.Scan(&resultID, &callID, &status, &outputJSON, &errorJSON, &refsJSON, &result.StartedAt, &result.CompletedAt); err != nil {
		return contracts.ToolResult{}, mapSQLError(err)
	}
	result.ToolResultID = contracts.ToolResultID(resultID)
	result.ToolCallID = contracts.ToolCallID(callID)
	result.Status = contracts.ToolResultStatus(status)
	if err := scanJSON(outputJSON, &result.Output); err != nil {
		return contracts.ToolResult{}, err
	}
	if err := scanJSON(errorJSON, &result.Error); err != nil {
		return contracts.ToolResult{}, err
	}
	if err := scanJSON(refsJSON, &result.ArtifactRefs); err != nil {
		return contracts.ToolResult{}, err
	}
	result.StartedAt = result.StartedAt.UTC()
	result.CompletedAt = result.CompletedAt.UTC()
	return result, nil
}

type ToolCatalogStore struct {
	db *sql.DB
}

func (s *ToolCatalogStore) UpsertProvider(ctx context.Context, provider toolcatalog.ToolProvider) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tool_providers (
  tenant_id, provider_id, provider_type, name, description, service_connection_id, status,
  health_status, last_health_check_at, last_health_error, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (tenant_id, provider_id) DO UPDATE SET
  provider_type=EXCLUDED.provider_type,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  service_connection_id=EXCLUDED.service_connection_id,
  status=EXCLUDED.status,
  health_status=EXCLUDED.health_status,
  last_health_check_at=EXCLUDED.last_health_check_at,
  last_health_error=EXCLUDED.last_health_error,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		provider.TenantID, provider.ProviderID, provider.ProviderType, provider.Name, nullString(provider.Description),
		nullString(provider.ServiceConnectionID), provider.Status, provider.HealthStatus, nullTime(provider.LastHealthCheckAt),
		nullString(provider.LastHealthError), provider.Version, now, now,
	)
	return err
}

func (s *ToolCatalogStore) UpsertGroup(ctx context.Context, group toolcatalog.ToolGroup) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tool_groups (tenant_id, group_id, name, description, status, version, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (tenant_id, group_id) DO UPDATE SET
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  status=EXCLUDED.status,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		group.TenantID, group.GroupID, group.Name, nullString(group.Description), group.Status, group.Version, now, now,
	)
	return err
}

func (s *ToolCatalogStore) UpsertAdapterOperation(ctx context.Context, operation toolcatalog.AdapterOperation) error {
	whenToUse, err := jsonValue(operation.WhenToUse)
	if err != nil {
		return err
	}
	headers, err := jsonValue(operation.Headers)
	if err != nil {
		return err
	}
	inputSchema, err := jsonValue(operation.InputSchema)
	if err != nil {
		return err
	}
	outputSchema, err := nullableJSONValue(operation.OutputSchema)
	if err != nil {
		return err
	}
	requestMapping, err := jsonValue(operation.RequestMapping)
	if err != nil {
		return err
	}
	responseMapping, err := jsonValue(operation.ResponseMapping)
	if err != nil {
		return err
	}
	parameterSchema, err := nullableJSONValue(operation.ParameterSchema)
	if err != nil {
		return err
	}
	redactColumns := operation.RedactColumns
	if redactColumns == nil {
		redactColumns = []string{}
	}
	redactColumnsJSON, err := jsonValue(redactColumns)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_adapter_operations (
  tenant_id, provider_id, operation_id, tool_id, group_id, name, description, when_to_use_json,
  service_connection_id, method, path, headers_json, input_schema_json, output_schema_json,
  request_mapping_json, response_mapping_json, resource_id, query_template, parameter_schema_json, max_rows, redact_columns_json, read_only,
  risk_level, visibility, status, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
ON CONFLICT (tenant_id, provider_id, operation_id) DO UPDATE SET
  tool_id=EXCLUDED.tool_id,
  group_id=EXCLUDED.group_id,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  when_to_use_json=EXCLUDED.when_to_use_json,
  service_connection_id=EXCLUDED.service_connection_id,
  method=EXCLUDED.method,
  path=EXCLUDED.path,
  headers_json=EXCLUDED.headers_json,
  input_schema_json=EXCLUDED.input_schema_json,
  output_schema_json=EXCLUDED.output_schema_json,
  request_mapping_json=EXCLUDED.request_mapping_json,
  response_mapping_json=EXCLUDED.response_mapping_json,
  resource_id=EXCLUDED.resource_id,
  query_template=EXCLUDED.query_template,
  parameter_schema_json=EXCLUDED.parameter_schema_json,
  max_rows=EXCLUDED.max_rows,
  redact_columns_json=EXCLUDED.redact_columns_json,
  read_only=EXCLUDED.read_only,
  risk_level=EXCLUDED.risk_level,
  visibility=EXCLUDED.visibility,
  status=EXCLUDED.status,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		operation.TenantID, operation.ProviderID, operation.OperationID, operation.ToolID, nullString(operation.GroupID),
		operation.Name, operation.Description, whenToUse, operation.ServiceConnectionID, operation.Method, operation.Path,
		headers, inputSchema, outputSchema, requestMapping, responseMapping,
		nullString(operation.ResourceID), nullString(operation.QueryTemplate), parameterSchema, operation.MaxRows, redactColumnsJSON, operation.ReadOnly,
		string(operation.RiskLevel), string(operation.Visibility), operation.Status, operation.Version, now, now,
	)
	return err
}

func (s *ToolCatalogStore) UpsertManifest(ctx context.Context, manifest toolcatalog.ToolManifest) error {
	whenToUse, err := jsonValue(manifest.WhenToUse)
	if err != nil {
		return err
	}
	inputSchema, err := jsonValue(manifest.InputSchema)
	if err != nil {
		return err
	}
	outputSchema, err := nullableJSONValue(manifest.OutputSchema)
	if err != nil {
		return err
	}
	executor, err := jsonValue(manifest.Executor)
	if err != nil {
		return err
	}
	manifestJSON, err := jsonValue(manifest)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_manifests (
  tenant_id, tool_id, group_id, name, description, when_to_use_json,
  input_schema_json, output_schema_json, risk_level, visibility, execution_profile,
  executor_json, status, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (tenant_id, tool_id) DO UPDATE SET
  group_id=EXCLUDED.group_id,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  when_to_use_json=EXCLUDED.when_to_use_json,
  input_schema_json=EXCLUDED.input_schema_json,
  output_schema_json=EXCLUDED.output_schema_json,
  risk_level=EXCLUDED.risk_level,
  visibility=EXCLUDED.visibility,
  execution_profile=EXCLUDED.execution_profile,
  executor_json=EXCLUDED.executor_json,
  status=EXCLUDED.status,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		manifest.TenantID, manifest.ToolID, nullString(manifest.GroupID), manifest.Name, manifest.Description, whenToUse,
		inputSchema, outputSchema, string(manifest.RiskLevel), string(manifest.Visibility), manifest.ExecutionProfile, executor,
		manifest.Status, manifest.Version, now, now,
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_manifest_versions (version_id, tenant_id, tool_id, version, manifest_json, created_at)
VALUES ($1,$2,$3,$4,$5,$6)`,
		idgen.New("toolmanifestver"), manifest.TenantID, manifest.ToolID, manifest.Version, manifestJSON, now,
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tool_runtime_registry_cache (tenant_id, tool_id, manifest_version, status, cached_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, tool_id) DO UPDATE SET
  manifest_version=EXCLUDED.manifest_version,
  status=EXCLUDED.status,
  cached_at=EXCLUDED.cached_at`,
		manifest.TenantID, manifest.ToolID, manifest.Version, manifest.Status, now,
	)
	return err
}

func (s *ToolCatalogStore) UpsertRuntimeCache(ctx context.Context, tenantID contracts.TenantID, toolID string, version string, status string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tool_runtime_registry_cache (tenant_id, tool_id, manifest_version, status, cached_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, tool_id) DO UPDATE SET
  manifest_version=EXCLUDED.manifest_version,
  status=EXCLUDED.status,
  cached_at=EXCLUDED.cached_at`,
		tenantID, toolID, version, status, time.Now().UTC(),
	)
	return err
}

func (s *ToolCatalogStore) ListProviders(ctx context.Context) ([]toolcatalog.ToolProvider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, provider_id, provider_type, name, description, service_connection_id, status, health_status, last_health_check_at, last_health_error,
       version
FROM tool_providers
ORDER BY tenant_id, provider_id`)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]toolcatalog.ToolProvider, 0)
	for rows.Next() {
		provider, err := scanToolProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, rows.Err()
}

func (s *ToolCatalogStore) ListGroups(ctx context.Context) ([]toolcatalog.ToolGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, group_id, name, description, status, version
FROM tool_groups
ORDER BY tenant_id, group_id`)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]toolcatalog.ToolGroup, 0)
	for rows.Next() {
		group, err := scanToolGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, group)
	}
	return out, rows.Err()
}

func (s *ToolCatalogStore) ListAdapterOperations(ctx context.Context) ([]toolcatalog.AdapterOperation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, provider_id, operation_id, tool_id, group_id, name, description, when_to_use_json,
  service_connection_id, method, path, headers_json, input_schema_json, output_schema_json,
  request_mapping_json, response_mapping_json, resource_id, query_template, parameter_schema_json, max_rows, redact_columns_json, read_only,
  risk_level, visibility, status, version
FROM tool_adapter_operations
ORDER BY tenant_id, provider_id, operation_id`)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]toolcatalog.AdapterOperation, 0)
	for rows.Next() {
		operation, err := scanAdapterOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, operation)
	}
	return out, rows.Err()
}

func (s *ToolCatalogStore) ListManifests(ctx context.Context) ([]toolcatalog.ToolManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, tool_id, group_id, name, description, when_to_use_json,
  input_schema_json, output_schema_json, risk_level, visibility, execution_profile,
  executor_json, status, version
FROM tool_manifests
ORDER BY tenant_id, tool_id`)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]toolcatalog.ToolManifest, 0)
	for rows.Next() {
		manifest, err := scanToolManifest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, manifest)
	}
	return out, rows.Err()
}

func scanToolProvider(row interface {
	Scan(dest ...any) error
}) (toolcatalog.ToolProvider, error) {
	var provider toolcatalog.ToolProvider
	var tenantID, description, serviceConnectionID, lastHealthError sql.NullString
	var lastHealthCheckAt sql.NullTime
	if err := row.Scan(
		&tenantID, &provider.ProviderID, &provider.ProviderType, &provider.Name, &description, &serviceConnectionID,
		&provider.Status, &provider.HealthStatus, &lastHealthCheckAt, &lastHealthError, &provider.Version,
	); err != nil {
		return toolcatalog.ToolProvider{}, mapSQLError(err)
	}
	provider.TenantID = contracts.TenantID(tenantID.String)
	provider.Description = description.String
	provider.ServiceConnectionID = serviceConnectionID.String
	provider.LastHealthCheckAt = timePtr(lastHealthCheckAt)
	provider.LastHealthError = lastHealthError.String
	return provider, nil
}

func scanToolGroup(row interface {
	Scan(dest ...any) error
}) (toolcatalog.ToolGroup, error) {
	var group toolcatalog.ToolGroup
	var tenantID, description sql.NullString
	if err := row.Scan(&tenantID, &group.GroupID, &group.Name, &description, &group.Status, &group.Version); err != nil {
		return toolcatalog.ToolGroup{}, mapSQLError(err)
	}
	group.TenantID = contracts.TenantID(tenantID.String)
	group.Description = description.String
	return group, nil
}

func scanAdapterOperation(row interface {
	Scan(dest ...any) error
}) (toolcatalog.AdapterOperation, error) {
	var operation toolcatalog.AdapterOperation
	var tenantID, groupID, resourceID, queryTemplate sql.NullString
	var whenToUseJSON, headersJSON, inputSchemaJSON, requestMappingJSON, responseMappingJSON, redactColumnsJSON []byte
	var outputSchemaJSON, parameterSchemaJSON sql.NullString
	var riskLevel, visibility string
	if err := row.Scan(
		&tenantID, &operation.ProviderID, &operation.OperationID, &operation.ToolID, &groupID, &operation.Name, &operation.Description,
		&whenToUseJSON, &operation.ServiceConnectionID, &operation.Method, &operation.Path, &headersJSON, &inputSchemaJSON,
		&outputSchemaJSON, &requestMappingJSON, &responseMappingJSON, &resourceID, &queryTemplate, &parameterSchemaJSON,
		&operation.MaxRows, &redactColumnsJSON, &operation.ReadOnly, &riskLevel, &visibility, &operation.Status, &operation.Version,
	); err != nil {
		return toolcatalog.AdapterOperation{}, mapSQLError(err)
	}
	operation.TenantID = contracts.TenantID(tenantID.String)
	operation.GroupID = groupID.String
	operation.ResourceID = resourceID.String
	operation.QueryTemplate = queryTemplate.String
	operation.RiskLevel = contracts.RiskLevel(riskLevel)
	operation.Visibility = contracts.ToolVisibility(visibility)
	if err := scanJSON(whenToUseJSON, &operation.WhenToUse); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	if err := scanJSON(headersJSON, &operation.Headers); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	if err := scanJSON(inputSchemaJSON, &operation.InputSchema); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	if outputSchemaJSON.Valid {
		if err := scanJSON([]byte(outputSchemaJSON.String), &operation.OutputSchema); err != nil {
			return toolcatalog.AdapterOperation{}, err
		}
	}
	if parameterSchemaJSON.Valid {
		if err := scanJSON([]byte(parameterSchemaJSON.String), &operation.ParameterSchema); err != nil {
			return toolcatalog.AdapterOperation{}, err
		}
	}
	if err := scanJSON(requestMappingJSON, &operation.RequestMapping); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	if err := scanJSON(responseMappingJSON, &operation.ResponseMapping); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	if err := scanJSON(redactColumnsJSON, &operation.RedactColumns); err != nil {
		return toolcatalog.AdapterOperation{}, err
	}
	operation.RedactColumns = normalizeStringSlice(operation.RedactColumns)
	return operation, nil
}

func scanToolManifest(row interface {
	Scan(dest ...any) error
}) (toolcatalog.ToolManifest, error) {
	var manifest toolcatalog.ToolManifest
	var tenantID, groupID sql.NullString
	var riskLevel, visibility string
	var whenToUseJSON, inputSchemaJSON, executorJSON []byte
	var outputSchemaJSON sql.NullString
	if err := row.Scan(
		&tenantID, &manifest.ToolID, &groupID, &manifest.Name, &manifest.Description, &whenToUseJSON,
		&inputSchemaJSON, &outputSchemaJSON, &riskLevel, &visibility, &manifest.ExecutionProfile,
		&executorJSON, &manifest.Status, &manifest.Version,
	); err != nil {
		return toolcatalog.ToolManifest{}, mapSQLError(err)
	}
	manifest.TenantID = contracts.TenantID(tenantID.String)
	manifest.GroupID = groupID.String
	manifest.RiskLevel = contracts.RiskLevel(riskLevel)
	manifest.Visibility = contracts.ToolVisibility(visibility)
	if err := scanJSON(whenToUseJSON, &manifest.WhenToUse); err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	if err := scanJSON(inputSchemaJSON, &manifest.InputSchema); err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	if outputSchemaJSON.Valid {
		if err := scanJSON([]byte(outputSchemaJSON.String), &manifest.OutputSchema); err != nil {
			return toolcatalog.ToolManifest{}, err
		}
	}
	if err := scanJSON(executorJSON, &manifest.Executor); err != nil {
		return toolcatalog.ToolManifest{}, err
	}
	if manifest.InputSchema == nil {
		manifest.InputSchema = map[string]any{}
	}
	return manifest, nil
}

type RuntimeHookStore struct {
	db *sql.DB
}

func (s *RuntimeHookStore) UpsertProvider(ctx context.Context, provider runtimehook.Provider) error {
	providerJSON, err := jsonValue(provider)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO runtime_hook_providers (
  tenant_id, provider_id, provider_type, name, description, endpoint,
  provider_json, status, health_status, last_health_check_at, last_health_error,
  version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (tenant_id, provider_id) DO UPDATE SET
  provider_type=EXCLUDED.provider_type,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  endpoint=EXCLUDED.endpoint,
  provider_json=EXCLUDED.provider_json,
  status=EXCLUDED.status,
  health_status=EXCLUDED.health_status,
  last_health_check_at=EXCLUDED.last_health_check_at,
  last_health_error=EXCLUDED.last_health_error,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		provider.TenantID, provider.ProviderID, string(provider.ProviderType), provider.Name,
		nullString(provider.Description), nullString(provider.Endpoint), providerJSON,
		provider.Status, provider.HealthStatus, nullTime(provider.LastHealthCheckAt),
		nullString(provider.LastHealthError), provider.Version, now, now,
	)
	return err
}

func (s *RuntimeHookStore) GetProvider(ctx context.Context, tenantID contracts.TenantID, providerID string) (runtimehook.Provider, bool, error) {
	provider, ok, err := s.getProvider(ctx, tenantID, providerID)
	if err != nil || ok || tenantID == "" {
		return provider, ok, err
	}
	return s.getProvider(ctx, "", providerID)
}

func (s *RuntimeHookStore) getProvider(ctx context.Context, tenantID contracts.TenantID, providerID string) (runtimehook.Provider, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, provider_id, provider_type, name, description, endpoint, provider_json, status, health_status, last_health_check_at, last_health_error, version
FROM runtime_hook_providers
WHERE tenant_id=$1 AND provider_id=$2`, tenantID, providerID)
	provider, err := scanRuntimeHookProvider(row)
	if err != nil {
		if errors.Is(err, storagerepo.ErrNotFound) {
			return runtimehook.Provider{}, false, nil
		}
		return runtimehook.Provider{}, false, err
	}
	return provider, true, nil
}

func (s *RuntimeHookStore) ListProviders(ctx context.Context, tenantID contracts.TenantID) ([]runtimehook.Provider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, provider_id, provider_type, name, description, endpoint, provider_json, status, health_status, last_health_check_at, last_health_error, version
FROM runtime_hook_providers
WHERE tenant_id=$1 OR tenant_id=''
ORDER BY tenant_id, provider_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]runtimehook.Provider{}
	for rows.Next() {
		provider, err := scanRuntimeHookProvider(rows)
		if err != nil {
			return nil, err
		}
		existing, ok := byID[provider.ProviderID]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byID[provider.ProviderID] = provider
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]runtimehook.Provider, 0, len(byID))
	for _, provider := range byID {
		out = append(out, provider)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ProviderID < out[j].ProviderID
	})
	return out, nil
}

func (s *RuntimeHookStore) UpsertManifest(ctx context.Context, manifest runtimehook.HookManifest) error {
	manifestJSON, err := jsonValue(manifest)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO runtime_hook_manifests (
  tenant_id, hook_id, provider_id, phase, name, description,
  manifest_json, status, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (tenant_id, hook_id) DO UPDATE SET
  provider_id=EXCLUDED.provider_id,
  phase=EXCLUDED.phase,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  manifest_json=EXCLUDED.manifest_json,
  status=EXCLUDED.status,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		manifest.TenantID, manifest.HookID, nullString(manifest.ProviderID), string(manifest.Phase),
		manifest.Name, nullString(manifest.Description), manifestJSON, manifest.Status, manifest.Version, now, now,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO runtime_hook_manifest_versions (
  tenant_id, hook_id, version, provider_id, phase, name, description,
  manifest_json, status, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (tenant_id, hook_id, version) DO UPDATE SET
  provider_id=EXCLUDED.provider_id,
  phase=EXCLUDED.phase,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  manifest_json=EXCLUDED.manifest_json,
  status=EXCLUDED.status,
  updated_at=EXCLUDED.updated_at`,
		manifest.TenantID, manifest.HookID, manifest.Version, nullString(manifest.ProviderID), string(manifest.Phase),
		manifest.Name, nullString(manifest.Description), manifestJSON, manifest.Status, now, now,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *RuntimeHookStore) GetManifest(ctx context.Context, tenantID contracts.TenantID, hookID string) (runtimehook.HookManifest, bool, error) {
	manifest, ok, err := s.getManifest(ctx, tenantID, hookID)
	if err != nil || ok || tenantID == "" {
		return manifest, ok, err
	}
	return s.getManifest(ctx, "", hookID)
}

func (s *RuntimeHookStore) getManifest(ctx context.Context, tenantID contracts.TenantID, hookID string) (runtimehook.HookManifest, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, hook_id, provider_id, phase, name, description, manifest_json, status, version
FROM runtime_hook_manifests
WHERE tenant_id=$1 AND hook_id=$2`, tenantID, hookID)
	manifest, err := scanRuntimeHookManifest(row)
	if err != nil {
		if errors.Is(err, storagerepo.ErrNotFound) {
			return runtimehook.HookManifest{}, false, nil
		}
		return runtimehook.HookManifest{}, false, err
	}
	return manifest, true, nil
}

func (s *RuntimeHookStore) ListManifests(ctx context.Context, tenantID contracts.TenantID) ([]runtimehook.HookManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, hook_id, provider_id, phase, name, description, manifest_json, status, version
FROM runtime_hook_manifests
WHERE tenant_id=$1 OR tenant_id=''
ORDER BY tenant_id, phase, hook_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]runtimehook.HookManifest{}
	for rows.Next() {
		manifest, err := scanRuntimeHookManifest(rows)
		if err != nil {
			return nil, err
		}
		existing, ok := byID[manifest.HookID]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byID[manifest.HookID] = manifest
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]runtimehook.HookManifest, 0, len(byID))
	for _, manifest := range byID {
		out = append(out, manifest)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Phase == out[j].Phase {
			return out[i].HookID < out[j].HookID
		}
		return out[i].Phase < out[j].Phase
	})
	return out, nil
}

func (s *RuntimeHookStore) GetManifestVersion(ctx context.Context, tenantID contracts.TenantID, hookID string, version string) (runtimehook.HookManifest, bool, error) {
	manifest, ok, err := s.getManifestVersion(ctx, tenantID, hookID, version)
	if err != nil || ok || tenantID == "" {
		return manifest, ok, err
	}
	return s.getManifestVersion(ctx, "", hookID, version)
}

func (s *RuntimeHookStore) getManifestVersion(ctx context.Context, tenantID contracts.TenantID, hookID string, version string) (runtimehook.HookManifest, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, hook_id, provider_id, phase, name, description, manifest_json, status, version
FROM runtime_hook_manifest_versions
WHERE tenant_id=$1 AND hook_id=$2 AND version=$3`, tenantID, hookID, version)
	manifest, err := scanRuntimeHookManifest(row)
	if err != nil {
		if errors.Is(err, storagerepo.ErrNotFound) {
			return runtimehook.HookManifest{}, false, nil
		}
		return runtimehook.HookManifest{}, false, err
	}
	return manifest, true, nil
}

func (s *RuntimeHookStore) ListManifestVersions(ctx context.Context, tenantID contracts.TenantID, hookID string) ([]runtimehook.HookManifest, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, hook_id, provider_id, phase, name, description, manifest_json, status, version
FROM runtime_hook_manifest_versions
WHERE hook_id=$2 AND (tenant_id=$1 OR tenant_id='')
ORDER BY tenant_id, version`, tenantID, hookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byVersion := map[string]runtimehook.HookManifest{}
	for rows.Next() {
		manifest, err := scanRuntimeHookManifest(rows)
		if err != nil {
			return nil, err
		}
		existing, ok := byVersion[manifest.Version]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byVersion[manifest.Version] = manifest
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]runtimehook.HookManifest, 0, len(byVersion))
	for _, manifest := range byVersion {
		out = append(out, manifest)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (s *RuntimeHookStore) UpsertBinding(ctx context.Context, binding runtimehook.Binding) error {
	bindingJSON, err := jsonValue(binding)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_runtime_hook_bindings (
  tenant_id, agent_id, agent_version, hook_id, provider_type, provider_id,
  phase, binding_json, status, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, agent_id, agent_version, hook_id, phase) DO UPDATE SET
  provider_type=EXCLUDED.provider_type,
  provider_id=EXCLUDED.provider_id,
  binding_json=EXCLUDED.binding_json,
  status=EXCLUDED.status,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		binding.TenantID, binding.AgentID, binding.AgentVersion, binding.HookID, string(binding.ProviderType),
		nullString(binding.ProviderID), string(binding.Phase), bindingJSON, bindingStatus(binding), binding.Version, now, now,
	)
	return err
}

func (s *RuntimeHookStore) ListBindings(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, agentVersion contracts.AgentVersion) ([]runtimehook.Binding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, agent_version, hook_id, provider_type, provider_id, phase, binding_json, status, version
FROM agent_runtime_hook_bindings
WHERE (tenant_id=$1 OR tenant_id='') AND agent_id=$2 AND (agent_version=$3 OR agent_version='')
ORDER BY tenant_id, hook_id, phase`, tenantID, agentID, agentVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]runtimehook.Binding{}
	for rows.Next() {
		binding, err := scanRuntimeHookBinding(rows)
		if err != nil {
			return nil, err
		}
		key := binding.HookID + "\x00" + string(binding.Phase)
		existing, ok := byID[key]
		if ok && runtimeHookBindingSpecificity(existing, tenantID, agentVersion) >= runtimeHookBindingSpecificity(binding, tenantID, agentVersion) {
			continue
		}
		byID[key] = binding
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]runtimehook.Binding, 0, len(byID))
	for _, binding := range byID {
		out = append(out, binding)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Phase == out[j].Phase {
			return out[i].HookID < out[j].HookID
		}
		return out[i].Phase < out[j].Phase
	})
	return out, nil
}

func (s *RuntimeHookStore) SaveEvent(ctx context.Context, event runtimehook.HookEvent) error {
	patchJSON, err := jsonValue(event.Patch)
	if err != nil {
		return err
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO runtime_hook_events (
  event_id, tenant_id, trace_id, run_id, task_id, agent_id,
  hook_id, provider_id, provider_type, phase, status, reason, latency_ms, patch_json, created_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.TenantID, event.TraceID, event.RunID, event.TaskID, event.AgentID,
		event.HookID, nullString(event.ProviderID), nullString(string(event.ProviderType)), string(event.Phase),
		event.Status, nullString(event.Reason), event.LatencyMS, patchJSON, event.CreatedAt.UTC(),
	)
	return err
}

func (s *RuntimeHookStore) ListEvents(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]runtimehook.HookEvent, error) {
	where := "tenant_id=$1"
	args := []any{tenantID}
	if traceID != "" {
		args = append(args, traceID)
		where += fmt.Sprintf(" AND trace_id=$%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT event_id, tenant_id, trace_id, run_id, task_id, agent_id, hook_id, provider_id, provider_type, phase, status, reason, latency_ms, patch_json, created_at
FROM runtime_hook_events
WHERE `+where+`
ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]runtimehook.HookEvent, 0)
	for rows.Next() {
		event, err := scanRuntimeHookEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func scanRuntimeHookProvider(row interface {
	Scan(dest ...any) error
}) (runtimehook.Provider, error) {
	var provider runtimehook.Provider
	var tenantID, description, endpoint, lastHealthError sql.NullString
	var lastHealthCheckAt sql.NullTime
	var providerJSON []byte
	var providerType string
	err := row.Scan(
		&tenantID, &provider.ProviderID, &providerType, &provider.Name, &description, &endpoint, &providerJSON,
		&provider.Status, &provider.HealthStatus, &lastHealthCheckAt, &lastHealthError, &provider.Version,
	)
	if err != nil {
		return runtimehook.Provider{}, mapSQLError(err)
	}
	provider.TenantID = contracts.TenantID(tenantID.String)
	provider.ProviderType = runtimehook.ProviderType(providerType)
	provider.Description = description.String
	provider.Endpoint = endpoint.String
	provider.LastHealthCheckAt = timePtr(lastHealthCheckAt)
	provider.LastHealthError = lastHealthError.String
	var stored runtimehook.Provider
	if err := scanJSON(providerJSON, &stored); err == nil && stored.ProviderID != "" {
		stored.TenantID = provider.TenantID
		stored.ProviderID = provider.ProviderID
		stored.ProviderType = provider.ProviderType
		stored.Name = provider.Name
		stored.Description = provider.Description
		stored.Endpoint = provider.Endpoint
		stored.Status = provider.Status
		stored.HealthStatus = provider.HealthStatus
		stored.LastHealthCheckAt = provider.LastHealthCheckAt
		stored.LastHealthError = provider.LastHealthError
		stored.Version = provider.Version
		return stored, nil
	}
	return provider, nil
}

func scanRuntimeHookManifest(row interface {
	Scan(dest ...any) error
}) (runtimehook.HookManifest, error) {
	var manifest runtimehook.HookManifest
	var tenantID, providerID, description sql.NullString
	var phase string
	var manifestJSON []byte
	err := row.Scan(
		&tenantID, &manifest.HookID, &providerID, &phase, &manifest.Name, &description,
		&manifestJSON, &manifest.Status, &manifest.Version,
	)
	if err != nil {
		return runtimehook.HookManifest{}, mapSQLError(err)
	}
	var stored runtimehook.HookManifest
	if err := scanJSON(manifestJSON, &stored); err == nil && stored.HookID != "" {
		manifest = stored
	}
	manifest.TenantID = contracts.TenantID(tenantID.String)
	manifest.ProviderID = providerID.String
	manifest.Phase = runtimehook.HookPoint(phase)
	manifest.Description = description.String
	return manifest, nil
}

func scanRuntimeHookBinding(row interface {
	Scan(dest ...any) error
}) (runtimehook.Binding, error) {
	var binding runtimehook.Binding
	var tenantID, providerID sql.NullString
	var providerType, phase, status, agentID, agentVersion, hookID, version string
	var bindingJSON []byte
	err := row.Scan(&tenantID, &agentID, &agentVersion, &hookID, &providerType, &providerID, &phase, &bindingJSON, &status, &version)
	if err != nil {
		return runtimehook.Binding{}, mapSQLError(err)
	}
	var stored runtimehook.Binding
	if err := scanJSON(bindingJSON, &stored); err == nil && stored.HookID != "" {
		binding = stored
	}
	binding.TenantID = contracts.TenantID(tenantID.String)
	binding.AgentID = contracts.AgentID(agentID)
	binding.AgentVersion = contracts.AgentVersion(agentVersion)
	binding.HookID = hookID
	binding.ProviderType = runtimehook.ProviderType(providerType)
	binding.ProviderID = providerID.String
	binding.Phase = runtimehook.HookPoint(phase)
	binding.Version = version
	binding.Enabled = status != runtimehook.StatusDisabled
	return binding, nil
}

func scanRuntimeHookEvent(row interface {
	Scan(dest ...any) error
}) (runtimehook.HookEvent, error) {
	var event runtimehook.HookEvent
	var tenantID, traceID, runID, taskID, agentID, providerID, providerType, reason sql.NullString
	var phase string
	var patchJSON []byte
	err := row.Scan(
		&event.EventID, &tenantID, &traceID, &runID, &taskID, &agentID, &event.HookID,
		&providerID, &providerType, &phase, &event.Status, &reason, &event.LatencyMS, &patchJSON, &event.CreatedAt,
	)
	if err != nil {
		return runtimehook.HookEvent{}, mapSQLError(err)
	}
	event.TenantID = contracts.TenantID(tenantID.String)
	event.TraceID = contracts.TraceID(traceID.String)
	event.RunID = contracts.AgentRunID(runID.String)
	event.TaskID = contracts.TaskID(taskID.String)
	event.AgentID = contracts.AgentID(agentID.String)
	event.ProviderID = providerID.String
	event.ProviderType = runtimehook.ProviderType(providerType.String)
	event.Phase = runtimehook.HookPoint(phase)
	event.Reason = reason.String
	if err := scanJSON(patchJSON, &event.Patch); err != nil {
		return runtimehook.HookEvent{}, err
	}
	event.CreatedAt = event.CreatedAt.UTC()
	return event, nil
}

func bindingStatus(binding runtimehook.Binding) string {
	if binding.Enabled {
		return runtimehook.StatusEnabled
	}
	return runtimehook.StatusDisabled
}

func runtimeHookBindingSpecificity(binding runtimehook.Binding, tenantID contracts.TenantID, agentVersion contracts.AgentVersion) int {
	score := 0
	if binding.TenantID == tenantID && tenantID != "" {
		score += 2
	}
	if binding.AgentVersion == agentVersion && agentVersion != "" {
		score++
	}
	return score
}

type AgentDelegationStore struct {
	db *sql.DB
}

func (s *AgentDelegationStore) Upsert(ctx context.Context, delegation agentdelegation.Delegation) error {
	now := time.Now().UTC()
	if delegation.CreatedAt.IsZero() {
		delegation.CreatedAt = now
	}
	if delegation.UpdatedAt.IsZero() {
		delegation.UpdatedAt = now
	}
	metadata, err := jsonValue(delegation.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_delegations (
  delegation_id, tenant_id, trace_id, parent_run_id, parent_task_id,
  source_tool_call_id, tool_id, operation, provider_agent_id,
  child_run_id, child_task_id, status, result_status, result_summary,
  error_summary, metadata_json, started_at, completed_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
ON CONFLICT (tenant_id, source_tool_call_id)
DO UPDATE SET
  trace_id=EXCLUDED.trace_id,
  parent_run_id=EXCLUDED.parent_run_id,
  parent_task_id=EXCLUDED.parent_task_id,
  tool_id=EXCLUDED.tool_id,
  operation=EXCLUDED.operation,
  provider_agent_id=EXCLUDED.provider_agent_id,
  child_run_id=EXCLUDED.child_run_id,
  child_task_id=EXCLUDED.child_task_id,
  status=EXCLUDED.status,
  result_status=EXCLUDED.result_status,
  result_summary=EXCLUDED.result_summary,
  error_summary=EXCLUDED.error_summary,
  metadata_json=EXCLUDED.metadata_json,
  started_at=COALESCE(agent_delegations.started_at, EXCLUDED.started_at),
  completed_at=EXCLUDED.completed_at,
  updated_at=EXCLUDED.updated_at`,
		delegation.DelegationID, delegation.TenantID, nullString(string(delegation.TraceID)),
		nullString(string(delegation.ParentRunID)), nullString(string(delegation.ParentTaskID)),
		delegation.ToolCallID, delegation.ToolID, nullString(delegation.Operation), delegation.ProviderAgentID,
		nullString(string(delegation.ChildRunID)), nullString(string(delegation.ChildTaskID)),
		delegation.Status, nullString(delegation.ResultStatus), nullString(delegation.ResultSummary),
		nullString(delegation.ErrorSummary), metadata, nullTime(delegation.StartedAt), nullTime(delegation.CompletedAt),
		delegation.CreatedAt.UTC(), delegation.UpdatedAt.UTC(),
	)
	return err
}

func (s *AgentDelegationStore) ListByParentRun(ctx context.Context, tenantID contracts.TenantID, parentRunID contracts.AgentRunID) ([]agentdelegation.Delegation, error) {
	rows, err := s.db.QueryContext(ctx, agentDelegationSelectSQL()+`
WHERE tenant_id=$1 AND parent_run_id=$2
ORDER BY COALESCE(started_at, created_at, updated_at), delegation_id`, tenantID, parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentDelegations(rows)
}

func (s *AgentDelegationStore) ListByTrace(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]agentdelegation.Delegation, error) {
	rows, err := s.db.QueryContext(ctx, agentDelegationSelectSQL()+`
WHERE tenant_id=$1 AND trace_id=$2
ORDER BY COALESCE(started_at, created_at, updated_at), delegation_id`, tenantID, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentDelegations(rows)
}

func agentDelegationSelectSQL() string {
	return `SELECT delegation_id, tenant_id, trace_id, parent_run_id, parent_task_id,
source_tool_call_id, tool_id, operation, provider_agent_id, child_run_id, child_task_id,
status, result_status, result_summary, error_summary, metadata_json, started_at, completed_at,
created_at, updated_at FROM agent_delegations `
}

func scanAgentDelegations(rows *sql.Rows) ([]agentdelegation.Delegation, error) {
	out := make([]agentdelegation.Delegation, 0)
	for rows.Next() {
		item, err := scanAgentDelegation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanAgentDelegation(row interface {
	Scan(dest ...any) error
}) (agentdelegation.Delegation, error) {
	var tenantID, sourceToolCallID, toolID, providerAgentID string
	var traceID, parentRunID, parentTaskID, operation, childRunID, childTaskID, resultStatus, resultSummary, errorSummary sql.NullString
	var metadataJSON []byte
	var startedAt, completedAt sql.NullTime
	item := agentdelegation.Delegation{}
	err := row.Scan(
		&item.DelegationID, &tenantID, &traceID, &parentRunID, &parentTaskID,
		&sourceToolCallID, &toolID, &operation, &providerAgentID, &childRunID, &childTaskID,
		&item.Status, &resultStatus, &resultSummary, &errorSummary, &metadataJSON,
		&startedAt, &completedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return agentdelegation.Delegation{}, mapSQLError(err)
	}
	item.TenantID = contracts.TenantID(tenantID)
	item.TraceID = contracts.TraceID(traceID.String)
	item.ParentRunID = contracts.AgentRunID(parentRunID.String)
	item.ParentTaskID = contracts.TaskID(parentTaskID.String)
	item.ToolCallID = contracts.ToolCallID(sourceToolCallID)
	item.ToolID = toolID
	item.Operation = operation.String
	item.ProviderAgentID = contracts.AgentID(providerAgentID)
	item.ChildRunID = contracts.AgentRunID(childRunID.String)
	item.ChildTaskID = contracts.TaskID(childTaskID.String)
	item.ResultStatus = resultStatus.String
	item.ResultSummary = resultSummary.String
	item.ErrorSummary = errorSummary.String
	if err := scanJSON(metadataJSON, &item.Metadata); err != nil {
		return agentdelegation.Delegation{}, err
	}
	item.StartedAt = timePtr(startedAt)
	item.CompletedAt = timePtr(completedAt)
	item.CreatedAt = item.CreatedAt.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}

type TraceRecorder struct {
	db *sql.DB
}

func (r *TraceRecorder) Record(ctx context.Context, event contracts.TraceEvent) error {
	payload, err := jsonValue(event.Payload)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO trace_events (trace_id, tenant_id, span_id, run_id, task_id, type, payload, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		event.TraceID, event.TenantID, event.SpanID, nullString(string(event.RunID)), nullString(string(event.TaskID)),
		event.Type, payload, event.CreatedAt.UTC(),
	)
	return err
}

func (r *TraceRecorder) ListByTrace(ctx context.Context, traceID contracts.TraceID) ([]contracts.TraceEvent, error) {
	return r.list(ctx, "trace_id=$1", traceID)
}

func (r *TraceRecorder) ListByRun(ctx context.Context, runID contracts.AgentRunID) ([]contracts.TraceEvent, error) {
	return r.list(ctx, "run_id=$1", runID)
}

func (r *TraceRecorder) ListByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TraceEvent, error) {
	return r.list(ctx, "task_id=$1", taskID)
}

func (r *TraceRecorder) List(ctx context.Context, filter tracequery.ListFilter) ([]contracts.TraceEvent, error) {
	where := make([]string, 0)
	args := make([]any, 0)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.TenantID != "" {
		add("tenant_id=$%d", filter.TenantID)
	}
	if filter.TraceID != "" {
		add("trace_id=$%d", filter.TraceID)
	}
	if filter.RunID != "" {
		add("run_id=$%d", filter.RunID)
	}
	if filter.TaskID != "" {
		add("task_id=$%d", filter.TaskID)
	}
	if filter.Type != "" {
		add("type=$%d", filter.Type)
	}
	if filter.From != nil {
		add("created_at >= $%d", filter.From.UTC())
	}
	if filter.To != nil {
		add("created_at <= $%d", filter.To.UTC())
	}
	query := `SELECT trace_id, tenant_id, span_id, run_id, task_id, type, payload, created_at FROM trace_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	return r.scanEvents(ctx, query, args...)
}

func (r *TraceRecorder) list(ctx context.Context, where string, arg any) ([]contracts.TraceEvent, error) {
	return r.scanEvents(ctx, `
SELECT trace_id, tenant_id, span_id, run_id, task_id, type, payload, created_at
FROM trace_events WHERE `+where+` ORDER BY created_at ASC, id ASC`, arg)
}

func (r *TraceRecorder) scanEvents(ctx context.Context, query string, args ...any) ([]contracts.TraceEvent, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.TraceEvent, 0)
	for rows.Next() {
		var traceID, tenantID, spanID, runID, taskID sql.NullString
		var payload []byte
		event := contracts.TraceEvent{}
		if err := rows.Scan(&traceID, &tenantID, &spanID, &runID, &taskID, &event.Type, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.TraceID = contracts.TraceID(traceID.String)
		event.TenantID = contracts.TenantID(tenantID.String)
		event.SpanID = contracts.SpanID(spanID.String)
		event.RunID = contracts.AgentRunID(runID.String)
		event.TaskID = contracts.TaskID(taskID.String)
		if err := scanJSON(payload, &event.Payload); err != nil {
			return nil, err
		}
		if event.Payload == nil {
			event.Payload = map[string]any{}
		}
		event.CreatedAt = event.CreatedAt.UTC()
		out = append(out, event)
	}
	return out, rows.Err()
}

type AuditLogger struct {
	db *sql.DB
}

func (l *AuditLogger) Log(ctx context.Context, event contracts.AuditEvent) error {
	if event.AuditID == "" {
		event.AuditID = idgen.New("audit")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := l.db.ExecContext(ctx, `
INSERT INTO audit_events (
  audit_id, tenant_id, actor_id, actor_type, action, resource_type, resource_id,
  decision, reason, trace_id, task_id, run_id, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (audit_id) DO NOTHING`,
		event.AuditID, event.TenantID, event.ActorID, event.ActorType, event.Action,
		event.ResourceType, event.ResourceID, event.Decision, nullString(event.Reason),
		nullString(string(event.TraceID)), nullString(string(event.TaskID)), nullString(string(event.RunID)),
		event.CreatedAt.UTC(),
	)
	return err
}

func (l *AuditLogger) Search(ctx context.Context, filter audit.Filter) ([]contracts.AuditEvent, error) {
	where := make([]string, 0)
	args := make([]any, 0)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.TenantID != "" {
		add("tenant_id=$%d", filter.TenantID)
	}
	if filter.Action != "" {
		add("action=$%d", filter.Action)
	}
	if filter.ResourceID != "" {
		add("resource_id=$%d", filter.ResourceID)
	}
	if filter.ResourceType != "" {
		add("resource_type=$%d", filter.ResourceType)
	}
	if filter.RunID != "" {
		add("run_id=$%d", filter.RunID)
	}
	if filter.TaskID != "" {
		add("task_id=$%d", filter.TaskID)
	}
	query := `SELECT audit_id, tenant_id, actor_id, actor_type, action, resource_type, resource_id,
decision, reason, trace_id, task_id, run_id, created_at FROM audit_events`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at ASC, audit_id ASC"
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.AuditEvent, 0)
	for rows.Next() {
		var tenantID, reason, traceID, taskID, runID sql.NullString
		event := contracts.AuditEvent{}
		if err := rows.Scan(&event.AuditID, &tenantID, &event.ActorID, &event.ActorType, &event.Action,
			&event.ResourceType, &event.ResourceID, &event.Decision, &reason, &traceID, &taskID,
			&runID, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.TenantID = contracts.TenantID(tenantID.String)
		event.Reason = reason.String
		event.TraceID = contracts.TraceID(traceID.String)
		event.TaskID = contracts.TaskID(taskID.String)
		event.RunID = contracts.AgentRunID(runID.String)
		event.CreatedAt = event.CreatedAt.UTC()
		out = append(out, event)
	}
	return out, rows.Err()
}

type ArtifactStore struct {
	db *sql.DB
}

func (s *ArtifactStore) CreateArtifact(ctx context.Context, artifact contracts.Artifact) error {
	return s.insertArtifact(ctx, s.db, artifact)
}

func (s *ArtifactStore) CreateArtifactWithContent(ctx context.Context, artifact contracts.Artifact, content string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.insertArtifact(ctx, tx, artifact); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO artifact_contents (artifact_id, tenant_id, content)
VALUES ($1,$2,$3)
ON CONFLICT (artifact_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, content=EXCLUDED.content`,
		artifact.ArtifactID, artifact.TenantID, content,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ArtifactStore) GetArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.Artifact, error) {
	return scanArtifact(s.db.QueryRowContext(ctx, artifactSelectSQL()+" WHERE artifact_id=$1 AND tenant_id=$2", artifactID, tenantID))
}

func (s *ArtifactStore) ReadArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID) (contracts.Artifact, error) {
	artifact, err := s.GetArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return contracts.Artifact{}, err
	}
	_ = (&AuditLogger{db: s.db}).Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       "artifact.read",
		ResourceType: "artifact",
		ResourceID:   string(artifactID),
		Decision:     "allowed",
		TraceID:      traceID,
		CreatedAt:    time.Now().UTC(),
	})
	return artifact, nil
}

func (s *ArtifactStore) DeleteArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM artifact_contents WHERE artifact_id=$1 AND tenant_id=$2`, artifactID, tenantID)
	if err != nil {
		return err
	}
	_, _ = result.RowsAffected()
	result, err = tx.ExecContext(ctx, `DELETE FROM artifacts WHERE artifact_id=$1 AND tenant_id=$2`, artifactID, tenantID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	if err := (&AuditLogger{db: s.db}).Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       contracts.AuditArtifactDelete,
		ResourceType: "artifact",
		ResourceID:   string(artifactID),
		Decision:     "allowed",
		Reason:       reason,
		TraceID:      traceID,
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ArtifactStore) ReadContent(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx, `
SELECT content FROM artifact_contents WHERE artifact_id=$1 AND tenant_id=$2`, artifactID, tenantID).Scan(&content)
	if err != nil {
		return "", mapSQLError(err)
	}
	return content, nil
}

func (s *ArtifactStore) Summary(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.ArtifactRef, error) {
	artifact, err := s.GetArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return contracts.ArtifactRef{}, err
	}
	return contracts.ArtifactRef{
		ArtifactID: artifact.ArtifactID,
		Type:       artifact.Type,
		URI:        artifact.StorageURI,
		Summary:    artifact.Name,
		Hash:       artifact.Hash,
	}, nil
}

func (s *ArtifactStore) insertArtifact(ctx context.Context, q dbtx, artifact contracts.Artifact) error {
	result, err := q.ExecContext(ctx, `
INSERT INTO artifacts (artifact_id, tenant_id, type, name, storage_uri, mime_type, size_bytes, hash, created_by, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (artifact_id) DO NOTHING`,
		artifact.ArtifactID, artifact.TenantID, artifact.Type, artifact.Name, artifact.StorageURI,
		nullString(artifact.MimeType), artifact.SizeBytes, artifact.Hash, artifact.CreatedBy, artifact.CreatedAt.UTC(),
	)
	return duplicateIfNoRows(result, err)
}

func artifactSelectSQL() string {
	return `SELECT artifact_id, tenant_id, type, name, storage_uri, mime_type, size_bytes, hash, created_by, created_at FROM artifacts`
}

func scanArtifact(row interface {
	Scan(dest ...any) error
}) (contracts.Artifact, error) {
	var artifactID, tenantID string
	var mimeType sql.NullString
	artifact := contracts.Artifact{}
	err := row.Scan(&artifactID, &tenantID, &artifact.Type, &artifact.Name, &artifact.StorageURI,
		&mimeType, &artifact.SizeBytes, &artifact.Hash, &artifact.CreatedBy, &artifact.CreatedAt)
	if err != nil {
		return contracts.Artifact{}, mapSQLError(err)
	}
	artifact.ArtifactID = contracts.ArtifactID(artifactID)
	artifact.TenantID = contracts.TenantID(tenantID)
	artifact.MimeType = mimeType.String
	artifact.CreatedAt = artifact.CreatedAt.UTC()
	return artifact, nil
}

type MemoryStore struct {
	db    *sql.DB
	audit audit.Logger
	now   func() time.Time
}

func (s *MemoryStore) WriteMemory(ctx context.Context, event contracts.MemoryEvent, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error) {
	return s.WriteMemoryWithPolicy(ctx, event, contracts.MemoryPolicy{AllowWrite: true, AllowRead: true}, actorID, actorType, traceID)
}

func (s *MemoryStore) WriteMemoryWithPolicy(ctx context.Context, event contracts.MemoryEvent, policy contracts.MemoryPolicy, actorID string, actorType string, traceID contracts.TraceID) (contracts.MemoryEvent, error) {
	if event.TenantID == "" {
		return contracts.MemoryEvent{}, fmt.Errorf("memory tenant_id is required")
	}
	if !policy.AllowWrite {
		return contracts.MemoryEvent{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "memory write is denied by policy", nil)
	}
	if len(policy.Scopes) > 0 && !memoryScopeAllowed(policy.Scopes, event.Scope) {
		return contracts.MemoryEvent{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "memory scope is denied by policy", map[string]any{"scope": event.Scope})
	}
	if event.MemoryID == "" {
		event.MemoryID = contracts.MemoryID(idgen.New("memory"))
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO memory_events (
  memory_id, tenant_id, agent_id, user_id, scope, content, summary,
  source_event_id, visibility, confidence, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (memory_id) DO NOTHING`,
		event.MemoryID, event.TenantID, nullString(string(event.AgentID)), nullString(string(event.UserID)),
		event.Scope, event.Content, nullString(event.Summary), nullString(event.SourceEventID),
		event.Visibility, event.Confidence, event.CreatedAt.UTC(),
	)
	if err := duplicateIfNoRows(result, err); err != nil {
		return contracts.MemoryEvent{}, err
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			TenantID:     event.TenantID,
			ActorID:      actorID,
			ActorType:    actorType,
			Action:       contracts.AuditMemoryWrite,
			ResourceType: "memory",
			ResourceID:   string(event.MemoryID),
			Decision:     "allowed",
			TraceID:      traceID,
			CreatedAt:    s.now().UTC(),
		})
	}
	return event, nil
}

func memoryScopeAllowed(allowed []string, scope string) bool {
	for _, current := range allowed {
		if current == scope {
			return true
		}
	}
	return false
}

func (s *MemoryStore) GetMemory(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryEvent, error) {
	return scanMemory(s.db.QueryRowContext(ctx, `
SELECT memory_id, tenant_id, agent_id, user_id, scope, content, summary, source_event_id, visibility, confidence, created_at
FROM memory_events WHERE tenant_id=$1 AND memory_id=$2`, tenantID, memoryID))
}

func (s *MemoryStore) ListMemory(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID) ([]contracts.MemorySummary, error) {
	return s.ListMemoryLimit(ctx, tenantID, agentID, userID, nil, 0)
}

func (s *MemoryStore) ListMemoryLimit(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID, scopes []string, limit int) ([]contracts.MemorySummary, error) {
	where := []string{"tenant_id=$1"}
	args := []any{tenantID}
	if agentID != "" {
		args = append(args, agentID)
		where = append(where, fmt.Sprintf("agent_id=$%d", len(args)))
	}
	if userID != "" {
		args = append(args, userID)
		where = append(where, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if normalizedScopes := normalizedMemoryScopes(scopes); len(normalizedScopes) > 0 {
		scopeWhere := make([]string, 0, len(normalizedScopes))
		for _, scope := range normalizedScopes {
			args = append(args, scope)
			scopeWhere = append(scopeWhere, fmt.Sprintf("scope=$%d", len(args)))
		}
		where = append(where, "("+strings.Join(scopeWhere, " OR ")+")")
	}
	query := `
SELECT memory_id, summary, scope
FROM memory_events
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY created_at ASC, memory_id ASC`
	if limit > 0 {
		args = append(args, limit)
		query = `
SELECT memory_id, summary, scope
FROM (
  SELECT memory_id, summary, scope, created_at
  FROM memory_events
  WHERE ` + strings.Join(where, " AND ") + `
  ORDER BY created_at DESC, memory_id DESC
  LIMIT $` + fmt.Sprint(len(args)) + `
) limited_memory_events
ORDER BY created_at ASC, memory_id ASC`
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.MemorySummary, 0)
	for rows.Next() {
		var memoryID string
		var summary sql.NullString
		var scope string
		if err := rows.Scan(&memoryID, &summary, &scope); err != nil {
			return nil, err
		}
		out = append(out, contracts.MemorySummary{MemoryID: contracts.MemoryID(memoryID), Summary: summary.String, Scope: scope})
	}
	return out, rows.Err()
}

func normalizedMemoryScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

type EvalStore struct {
	db *sql.DB
}

func (s *EvalStore) SaveSuite(ctx context.Context, suite eval.Suite) error {
	cases, err := jsonValue(suite.Cases)
	if err != nil {
		return err
	}
	gates, err := jsonValue(suite.Gates)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO eval_suites (
  suite_id, tenant_id, name, cases_json, gates_json, created_by, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (suite_id) DO UPDATE SET
  name=EXCLUDED.name,
  cases_json=EXCLUDED.cases_json,
  gates_json=EXCLUDED.gates_json`,
		suite.SuiteID, suite.TenantID, suite.Name, cases, gates, suite.CreatedBy, suite.CreatedAt.UTC(),
	)
	return err
}

func (s *EvalStore) GetSuite(ctx context.Context, suiteID contracts.EvalSuiteID) (eval.Suite, bool, error) {
	var suiteIDRaw, tenantID string
	var casesJSON, gatesJSON []byte
	suite := eval.Suite{}
	err := s.db.QueryRowContext(ctx, `
SELECT suite_id, tenant_id, name, cases_json, gates_json, created_by, created_at
FROM eval_suites WHERE suite_id=$1`, suiteID).
		Scan(&suiteIDRaw, &tenantID, &suite.Name, &casesJSON, &gatesJSON, &suite.CreatedBy, &suite.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return eval.Suite{}, false, nil
	}
	if err != nil {
		return eval.Suite{}, false, mapSQLError(err)
	}
	suite.SuiteID = contracts.EvalSuiteID(suiteIDRaw)
	suite.TenantID = contracts.TenantID(tenantID)
	if err := scanJSON(casesJSON, &suite.Cases); err != nil {
		return eval.Suite{}, false, err
	}
	if err := scanJSON(gatesJSON, &suite.Gates); err != nil {
		return eval.Suite{}, false, err
	}
	suite.CreatedAt = suite.CreatedAt.UTC()
	return suite, true, nil
}

func (s *EvalStore) SaveResult(ctx context.Context, result eval.SuiteResult) error {
	failures, err := jsonValue(result.Failures)
	if err != nil {
		return err
	}
	results, err := jsonValue(result.Results)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO eval_suite_results (
  eval_run_id, suite_id, tenant_id, passed, pass_rate, tool_misuse_rate,
  failures_json, results_json, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (eval_run_id) DO UPDATE SET
  passed=EXCLUDED.passed,
  pass_rate=EXCLUDED.pass_rate,
  tool_misuse_rate=EXCLUDED.tool_misuse_rate,
  failures_json=EXCLUDED.failures_json,
  results_json=EXCLUDED.results_json`,
		result.EvalRunID, result.SuiteID, result.TenantID, result.Passed, result.PassRate,
		result.ToolMisuseRate, failures, results, result.CreatedAt.UTC(),
	)
	return err
}

func (s *EvalStore) GetResult(ctx context.Context, evalRunID contracts.EvalRunID) (eval.SuiteResult, bool, error) {
	var evalRunIDRaw, suiteID, tenantID string
	var failuresJSON, resultsJSON []byte
	result := eval.SuiteResult{}
	err := s.db.QueryRowContext(ctx, `
SELECT eval_run_id, suite_id, tenant_id, passed, pass_rate, tool_misuse_rate,
       failures_json, results_json, created_at
FROM eval_suite_results WHERE eval_run_id=$1`, evalRunID).
		Scan(&evalRunIDRaw, &suiteID, &tenantID, &result.Passed, &result.PassRate,
			&result.ToolMisuseRate, &failuresJSON, &resultsJSON, &result.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return eval.SuiteResult{}, false, nil
	}
	if err != nil {
		return eval.SuiteResult{}, false, mapSQLError(err)
	}
	result.EvalRunID = contracts.EvalRunID(evalRunIDRaw)
	result.SuiteID = contracts.EvalSuiteID(suiteID)
	result.TenantID = contracts.TenantID(tenantID)
	if err := scanJSON(failuresJSON, &result.Failures); err != nil {
		return eval.SuiteResult{}, false, err
	}
	if err := scanJSON(resultsJSON, &result.Results); err != nil {
		return eval.SuiteResult{}, false, err
	}
	result.CreatedAt = result.CreatedAt.UTC()
	return result, true, nil
}

func scanMemory(row interface {
	Scan(dest ...any) error
}) (contracts.MemoryEvent, error) {
	var memoryID, tenantID, agentID, userID, summary, sourceEventID sql.NullString
	event := contracts.MemoryEvent{}
	err := row.Scan(&memoryID, &tenantID, &agentID, &userID, &event.Scope, &event.Content,
		&summary, &sourceEventID, &event.Visibility, &event.Confidence, &event.CreatedAt)
	if err != nil {
		return contracts.MemoryEvent{}, mapSQLError(err)
	}
	event.MemoryID = contracts.MemoryID(memoryID.String)
	event.TenantID = contracts.TenantID(tenantID.String)
	event.AgentID = contracts.AgentID(agentID.String)
	event.UserID = contracts.UserID(userID.String)
	event.Summary = summary.String
	event.SourceEventID = sourceEventID.String
	event.CreatedAt = event.CreatedAt.UTC()
	return event, nil
}

type ContextPackageStore struct {
	db *sql.DB
}

func (s *ContextPackageStore) SaveContextPackage(ctx context.Context, pkg contracts.HandoffContextPackage) error {
	content, err := jsonValue(pkg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO handoff_context_packages (
  package_id, tenant_id, parent_task_id, source_run_id, from_agent_id, to_agent_id,
  mode, content_json, hash, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (package_id) DO NOTHING`,
		pkg.PackageID, pkg.TenantID, pkg.ParentTaskID, pkg.SourceRunID, pkg.FromAgentID,
		pkg.ToAgentID, pkg.Mode, content, pkg.Hash, pkg.CreatedAt.UTC(),
	)
	return err
}

func (s *ContextPackageStore) GetContextPackage(ctx context.Context, tenantID contracts.TenantID, packageID contracts.ContextPackageID) (contracts.HandoffContextPackage, error) {
	var content []byte
	err := s.db.QueryRowContext(ctx, `
SELECT content_json FROM handoff_context_packages WHERE package_id=$1 AND tenant_id=$2`, packageID, tenantID).Scan(&content)
	if err != nil {
		return contracts.HandoffContextPackage{}, mapSQLError(err)
	}
	var pkg contracts.HandoffContextPackage
	if err := scanJSON(content, &pkg); err != nil {
		return contracts.HandoffContextPackage{}, err
	}
	return pkg, nil
}

type PlanRepository struct {
	db *sql.DB
}

func (r *PlanRepository) CreatePlan(ctx context.Context, plan contracts.TaskPlan, steps []contracts.PlanStep) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO task_plans (plan_id, task_id, objective, status, created_by, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (plan_id) DO NOTHING`,
		plan.PlanID, plan.TaskID, plan.Objective, plan.Status, plan.CreatedBy,
		plan.CreatedAt.UTC(), plan.UpdatedAt.UTC(),
	)
	if err := duplicateIfNoRows(result, err); err != nil {
		return err
	}
	for _, step := range steps {
		if err := r.insertStep(ctx, tx, step); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *PlanRepository) UpdatePlan(ctx context.Context, plan contracts.TaskPlan) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE task_plans SET objective=$2, status=$3, created_by=$4, created_at=$5, updated_at=$6
WHERE plan_id=$1`,
		plan.PlanID, plan.Objective, plan.Status, plan.CreatedBy, plan.CreatedAt.UTC(), plan.UpdatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (r *PlanRepository) GetPlan(ctx context.Context, planID string) (contracts.TaskPlan, error) {
	return scanPlan(r.db.QueryRowContext(ctx, planSelectSQL()+" WHERE plan_id=$1", planID))
}

func (r *PlanRepository) ActivePlan(ctx context.Context, taskID contracts.TaskID) (contracts.TaskPlan, bool, error) {
	plan, err := scanPlan(r.db.QueryRowContext(ctx, planSelectSQL()+`
 WHERE task_id=$1 AND status IN ($2,$3)
 ORDER BY created_at DESC
 LIMIT 1`, taskID, contracts.PlanRunning, contracts.PlanPending))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.TaskPlan{}, false, nil
	}
	return plan, err == nil, err
}

func (r *PlanRepository) ListPlansByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.TaskPlan, error) {
	rows, err := r.db.QueryContext(ctx, planSelectSQL()+" WHERE task_id=$1 ORDER BY created_at ASC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.TaskPlan, 0)
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (r *PlanRepository) GetStep(ctx context.Context, stepID string) (contracts.PlanStep, error) {
	return scanPlanStep(r.db.QueryRowContext(ctx, planStepSelectSQL()+" WHERE step_id=$1", stepID))
}

func (r *PlanRepository) UpdateStep(ctx context.Context, step contracts.PlanStep) error {
	hints, err := jsonValue(step.ExpectedToolHints)
	if err != nil {
		return err
	}
	refs, err := jsonValue(step.ResultRefs)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE plan_steps SET
  plan_id=$2, task_id=$3, step_index=$4, title=$5, description=$6,
  expected_tool_hints=$7, status=$8, result_refs=$9, failure_reason=$10,
  created_at=$11, updated_at=$12
WHERE step_id=$1`,
		step.StepID, step.PlanID, step.TaskID, step.Index, step.Title, step.Description,
		hints, step.Status, refs, nullString(step.FailureReason), step.CreatedAt.UTC(), step.UpdatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (r *PlanRepository) ListStepsByPlan(ctx context.Context, planID string) ([]contracts.PlanStep, error) {
	rows, err := r.db.QueryContext(ctx, planStepSelectSQL()+" WHERE plan_id=$1 ORDER BY step_index ASC", planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.PlanStep, 0)
	for rows.Next() {
		step, err := scanPlanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, step)
	}
	return out, rows.Err()
}

func (r *PlanRepository) AppendEvent(ctx context.Context, event contracts.PlanEvent) error {
	payload, err := jsonValue(event.Payload)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
INSERT INTO plan_events (event_id, plan_id, task_id, type, actor_id, actor_type, payload, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.PlanID, event.TaskID, event.Type, event.ActorID, event.ActorType,
		payload, event.CreatedAt.UTC(),
	)
	return duplicateIfNoRows(result, err)
}

func (r *PlanRepository) ListEventsByTask(ctx context.Context, taskID contracts.TaskID) ([]contracts.PlanEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT event_id, plan_id, task_id, type, actor_id, actor_type, payload, created_at
FROM plan_events WHERE task_id=$1 ORDER BY created_at ASC, event_id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.PlanEvent, 0)
	for rows.Next() {
		event, err := scanPlanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r *PlanRepository) insertStep(ctx context.Context, q dbtx, step contracts.PlanStep) error {
	hints, err := jsonValue(step.ExpectedToolHints)
	if err != nil {
		return err
	}
	refs, err := jsonValue(step.ResultRefs)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `
INSERT INTO plan_steps (
  step_id, plan_id, task_id, step_index, title, description, expected_tool_hints,
  status, result_refs, failure_reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (step_id) DO NOTHING`,
		step.StepID, step.PlanID, step.TaskID, step.Index, step.Title, step.Description,
		hints, step.Status, refs, nullString(step.FailureReason), step.CreatedAt.UTC(), step.UpdatedAt.UTC(),
	)
	return duplicateIfNoRows(result, err)
}

func planSelectSQL() string {
	return `SELECT plan_id, task_id, objective, status, created_by, created_at, updated_at FROM task_plans`
}

func scanPlan(row interface {
	Scan(dest ...any) error
}) (contracts.TaskPlan, error) {
	var planID, taskID, status string
	plan := contracts.TaskPlan{}
	if err := row.Scan(&planID, &taskID, &plan.Objective, &status, &plan.CreatedBy, &plan.CreatedAt, &plan.UpdatedAt); err != nil {
		return contracts.TaskPlan{}, mapSQLError(err)
	}
	plan.PlanID = planID
	plan.TaskID = contracts.TaskID(taskID)
	plan.Status = contracts.PlanStatus(status)
	plan.CreatedAt = plan.CreatedAt.UTC()
	plan.UpdatedAt = plan.UpdatedAt.UTC()
	return plan, nil
}

func planStepSelectSQL() string {
	return `SELECT step_id, plan_id, task_id, step_index, title, description, expected_tool_hints,
status, result_refs, failure_reason, created_at, updated_at FROM plan_steps`
}

func scanPlanStep(row interface {
	Scan(dest ...any) error
}) (contracts.PlanStep, error) {
	var taskID, status string
	var hintsJSON, refsJSON []byte
	var failure sql.NullString
	step := contracts.PlanStep{}
	if err := row.Scan(&step.StepID, &step.PlanID, &taskID, &step.Index, &step.Title, &step.Description,
		&hintsJSON, &status, &refsJSON, &failure, &step.CreatedAt, &step.UpdatedAt); err != nil {
		return contracts.PlanStep{}, mapSQLError(err)
	}
	step.TaskID = contracts.TaskID(taskID)
	step.Status = contracts.PlanStepStatus(status)
	if err := scanJSON(hintsJSON, &step.ExpectedToolHints); err != nil {
		return contracts.PlanStep{}, err
	}
	if err := scanJSON(refsJSON, &step.ResultRefs); err != nil {
		return contracts.PlanStep{}, err
	}
	step.FailureReason = failure.String
	step.CreatedAt = step.CreatedAt.UTC()
	step.UpdatedAt = step.UpdatedAt.UTC()
	return step, nil
}

func scanPlanEvent(row interface {
	Scan(dest ...any) error
}) (contracts.PlanEvent, error) {
	var taskID string
	var payload []byte
	event := contracts.PlanEvent{}
	if err := row.Scan(&event.EventID, &event.PlanID, &taskID, &event.Type, &event.ActorID, &event.ActorType, &payload, &event.CreatedAt); err != nil {
		return contracts.PlanEvent{}, err
	}
	event.TaskID = contracts.TaskID(taskID)
	if err := scanJSON(payload, &event.Payload); err != nil {
		return contracts.PlanEvent{}, err
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	event.CreatedAt = event.CreatedAt.UTC()
	return event, nil
}

type HandoffRepository struct {
	db *sql.DB
}

func (r *HandoffRepository) Save(ctx context.Context, handoff contracts.AgentHandoff, pkg contracts.HandoffContextPackage) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if pkg.PackageID != "" {
		content, err := jsonValue(pkg)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO handoff_context_packages (
  package_id, tenant_id, parent_task_id, source_run_id, from_agent_id, to_agent_id,
  mode, content_json, hash, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (package_id) DO NOTHING`,
			pkg.PackageID, pkg.TenantID, pkg.ParentTaskID, pkg.SourceRunID, pkg.FromAgentID,
			pkg.ToAgentID, pkg.Mode, content, pkg.Hash, pkg.CreatedAt.UTC(),
		)
		if err != nil {
			return err
		}
	}
	if err := r.insertHandoff(ctx, tx, handoff); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *HandoffRepository) CreateWithChild(ctx context.Context, handoff contracts.AgentHandoff, pkg contracts.HandoffContextPackage, childTask *contracts.Task, childEvent *contracts.TaskEvent, parentEvent contracts.TaskEvent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if pkg.PackageID != "" {
		content, err := jsonValue(pkg)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO handoff_context_packages (
  package_id, tenant_id, parent_task_id, source_run_id, from_agent_id, to_agent_id,
  mode, content_json, hash, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (package_id) DO NOTHING`,
			pkg.PackageID, pkg.TenantID, pkg.ParentTaskID, pkg.SourceRunID, pkg.FromAgentID,
			pkg.ToAgentID, pkg.Mode, content, pkg.Hash, pkg.CreatedAt.UTC(),
		)
		if err != nil {
			return err
		}
	}
	if err := r.insertHandoff(ctx, tx, handoff); err != nil {
		return err
	}
	if childTask != nil {
		if err := (&TaskStoreTx{tx: tx}).Create(ctx, *childTask); err != nil {
			return err
		}
		if childEvent != nil {
			if err := appendTaskEvent(ctx, tx, *childEvent); err != nil {
				return err
			}
		}
	}
	if err := appendTaskEvent(ctx, tx, parentEvent); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *HandoffRepository) Get(ctx context.Context, handoffID contracts.HandoffID) (contracts.AgentHandoff, bool, error) {
	handoff, err := scanHandoff(r.db.QueryRowContext(ctx, handoffSelectSQL()+" WHERE handoff_id=$1", handoffID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.AgentHandoff{}, false, nil
	}
	return handoff, err == nil, err
}

func (r *HandoffRepository) Update(ctx context.Context, handoff contracts.AgentHandoff) error {
	refs, err := jsonValue(handoff.ArtifactRefs)
	if err != nil {
		return err
	}
	expected, err := jsonValue(handoff.ExpectedOutput)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE agent_handoffs SET
  tenant_id=$2, parent_task_id=$3, child_task_id=$4, from_agent_id=$5, to_agent_id=$6,
  objective=$7, reason=$8, context_package_ref=$9, artifact_refs=$10,
  expected_output=$11, status=$12, created_at=$13, completed_at=$14
WHERE handoff_id=$1`,
		handoff.HandoffID, handoff.TenantID, handoff.ParentTaskID, taskIDArg(handoff.ChildTaskID),
		handoff.FromAgentID, handoff.ToAgentID, handoff.Objective, handoff.Reason,
		handoff.ContextPackageRef, refs, expected, handoff.Status, handoff.CreatedAt.UTC(),
		nullTime(handoff.CompletedAt),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (r *HandoffRepository) insertHandoff(ctx context.Context, q dbtx, handoff contracts.AgentHandoff) error {
	refs, err := jsonValue(handoff.ArtifactRefs)
	if err != nil {
		return err
	}
	expected, err := jsonValue(handoff.ExpectedOutput)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `
INSERT INTO agent_handoffs (
  handoff_id, tenant_id, parent_task_id, child_task_id, from_agent_id, to_agent_id,
  objective, reason, context_package_ref, artifact_refs, expected_output,
  status, created_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (handoff_id) DO NOTHING`,
		handoff.HandoffID, handoff.TenantID, handoff.ParentTaskID, taskIDArg(handoff.ChildTaskID),
		handoff.FromAgentID, handoff.ToAgentID, handoff.Objective, handoff.Reason,
		handoff.ContextPackageRef, refs, expected, handoff.Status, handoff.CreatedAt.UTC(),
		nullTime(handoff.CompletedAt),
	)
	return duplicateIfNoRows(result, err)
}

func handoffSelectSQL() string {
	return `SELECT handoff_id, tenant_id, parent_task_id, child_task_id, from_agent_id, to_agent_id,
objective, reason, context_package_ref, artifact_refs, expected_output, status, created_at, completed_at
FROM agent_handoffs`
}

func scanHandoff(row interface {
	Scan(dest ...any) error
}) (contracts.AgentHandoff, error) {
	var handoffID, tenantID, parentTaskID, childTaskID, fromAgentID, toAgentID, packageID, status sql.NullString
	var refsJSON, expectedJSON []byte
	var completed sql.NullTime
	handoff := contracts.AgentHandoff{}
	if err := row.Scan(&handoffID, &tenantID, &parentTaskID, &childTaskID, &fromAgentID, &toAgentID,
		&handoff.Objective, &handoff.Reason, &packageID, &refsJSON, &expectedJSON,
		&status, &handoff.CreatedAt, &completed); err != nil {
		return contracts.AgentHandoff{}, mapSQLError(err)
	}
	handoff.HandoffID = contracts.HandoffID(handoffID.String)
	handoff.TenantID = contracts.TenantID(tenantID.String)
	handoff.ParentTaskID = contracts.TaskID(parentTaskID.String)
	handoff.ChildTaskID = taskIDPtr(childTaskID)
	handoff.FromAgentID = contracts.AgentID(fromAgentID.String)
	handoff.ToAgentID = contracts.AgentID(toAgentID.String)
	handoff.ContextPackageRef = contracts.ContextPackageID(packageID.String)
	if err := scanJSON(refsJSON, &handoff.ArtifactRefs); err != nil {
		return contracts.AgentHandoff{}, err
	}
	if err := scanJSON(expectedJSON, &handoff.ExpectedOutput); err != nil {
		return contracts.AgentHandoff{}, err
	}
	handoff.Status = contracts.HandoffStatus(status.String)
	handoff.CreatedAt = handoff.CreatedAt.UTC()
	handoff.CompletedAt = timePtr(completed)
	return handoff, nil
}

type GovernanceProcessStore struct {
	db *sql.DB
}

func (s *GovernanceProcessStore) UpsertTemplate(ctx context.Context, template contracts.GovernanceProcessTemplate) error {
	value, err := jsonValue(template)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO governance_process_templates (
  tenant_id, template_id, name, version, status, template_json,
  created_by, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (tenant_id, template_id) DO UPDATE SET
  name=EXCLUDED.name,
  version=EXCLUDED.version,
  status=EXCLUDED.status,
  template_json=EXCLUDED.template_json,
  updated_at=EXCLUDED.updated_at`,
		template.TenantID, template.TemplateID, template.Name, template.Version, template.Status,
		value, nullString(template.CreatedBy), template.CreatedAt.UTC(), template.UpdatedAt.UTC())
	return err
}

func (s *GovernanceProcessStore) GetTemplate(ctx context.Context, tenantID contracts.TenantID, templateID contracts.GovernanceProcessTemplateID) (contracts.GovernanceProcessTemplate, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT template_json FROM governance_process_templates
WHERE tenant_id=$1 AND template_id=$2`, tenantID, templateID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GovernanceProcessTemplate{}, false, nil
	}
	if err != nil {
		return contracts.GovernanceProcessTemplate{}, false, mapSQLError(err)
	}
	var template contracts.GovernanceProcessTemplate
	if err := scanJSON(data, &template); err != nil {
		return contracts.GovernanceProcessTemplate{}, false, err
	}
	return template, true, nil
}

func (s *GovernanceProcessStore) CreateRun(ctx context.Context, run contracts.GovernanceProcessRun, gates []contracts.GovernanceGateRun) error {
	runValue, err := jsonValue(run)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO governance_process_runs (
  run_id, tenant_id, template_id, status, subject_type, subject_id,
  task_id, agent_run_id, trace_id, run_json, created_at, updated_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		run.RunID, run.TenantID, nullString(string(run.TemplateID)), run.Status, run.SubjectType, run.SubjectID,
		nullString(string(run.TaskID)), nullString(string(run.AgentRunID)), nullString(string(run.TraceID)),
		runValue, run.CreatedAt.UTC(), run.UpdatedAt.UTC(), nullTime(run.CompletedAt)); err != nil {
		return err
	}
	for _, gate := range gates {
		if err := insertGovernanceGate(ctx, tx, gate); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *GovernanceProcessStore) GetRun(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) (contracts.GovernanceProcessRun, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT run_json FROM governance_process_runs WHERE tenant_id=$1 AND run_id=$2`, tenantID, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GovernanceProcessRun{}, false, nil
	}
	if err != nil {
		return contracts.GovernanceProcessRun{}, false, mapSQLError(err)
	}
	var run contracts.GovernanceProcessRun
	if err := scanJSON(data, &run); err != nil {
		return contracts.GovernanceProcessRun{}, false, err
	}
	return run, true, nil
}

func (s *GovernanceProcessStore) UpdateRun(ctx context.Context, run contracts.GovernanceProcessRun) error {
	value, err := jsonValue(run)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE governance_process_runs SET
  status=$3,
  run_json=$4,
  updated_at=$5,
  completed_at=$6
WHERE tenant_id=$1 AND run_id=$2`,
		run.TenantID, run.RunID, run.Status, value, run.UpdatedAt.UTC(), nullTime(run.CompletedAt))
	return notFoundIfNoRows(result, err)
}

func (s *GovernanceProcessStore) GetGate(ctx context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) (contracts.GovernanceGateRun, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT gate_json FROM governance_gate_runs WHERE tenant_id=$1 AND gate_run_id=$2`, tenantID, gateRunID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GovernanceGateRun{}, false, nil
	}
	if err != nil {
		return contracts.GovernanceGateRun{}, false, mapSQLError(err)
	}
	var gate contracts.GovernanceGateRun
	if err := scanJSON(data, &gate); err != nil {
		return contracts.GovernanceGateRun{}, false, err
	}
	return gate, true, nil
}

func (s *GovernanceProcessStore) GetGateByDefinition(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID, gateID string) (contracts.GovernanceGateRun, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT gate_json FROM governance_gate_runs
WHERE tenant_id=$1 AND process_run_id=$2 AND gate_id=$3`, tenantID, runID, gateID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GovernanceGateRun{}, false, nil
	}
	if err != nil {
		return contracts.GovernanceGateRun{}, false, mapSQLError(err)
	}
	var gate contracts.GovernanceGateRun
	if err := scanJSON(data, &gate); err != nil {
		return contracts.GovernanceGateRun{}, false, err
	}
	return gate, true, nil
}

func (s *GovernanceProcessStore) ListGates(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceGateRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT gate_json FROM governance_gate_runs
WHERE tenant_id=$1 AND process_run_id=$2
ORDER BY created_at ASC, gate_run_id ASC`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.GovernanceGateRun, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var gate contracts.GovernanceGateRun
		if err := scanJSON(data, &gate); err != nil {
			return nil, err
		}
		out = append(out, gate)
	}
	return out, rows.Err()
}

func (s *GovernanceProcessStore) UpdateGate(ctx context.Context, gate contracts.GovernanceGateRun) error {
	value, err := jsonValue(gate)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE governance_gate_runs SET
  status=$3,
  gate_json=$4,
  updated_at=$5,
  resolved_at=$6
WHERE tenant_id=$1 AND gate_run_id=$2`,
		gate.TenantID, gate.GateRunID, gate.Status, value, gate.UpdatedAt.UTC(), nullTime(gate.ResolvedAt))
	return notFoundIfNoRows(result, err)
}

func (s *GovernanceProcessStore) SaveReview(ctx context.Context, review contracts.GovernanceReview) error {
	value, err := jsonValue(review)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO governance_reviews (
  review_id, gate_run_id, process_run_id, tenant_id, reviewer_id,
  reviewer_type, decision, review_json, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (review_id) DO UPDATE SET
  decision=EXCLUDED.decision,
  review_json=EXCLUDED.review_json`,
		review.ReviewID, review.GateRunID, review.ProcessRunID, review.TenantID,
		review.ReviewerID, review.ReviewerType, review.Decision, value, review.CreatedAt.UTC())
	return err
}

func (s *GovernanceProcessStore) ListReviews(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceReview, error) {
	return s.listReviews(ctx, "tenant_id=$1 AND process_run_id=$2", tenantID, runID)
}

func (s *GovernanceProcessStore) ListReviewsByGate(ctx context.Context, tenantID contracts.TenantID, gateRunID contracts.GovernanceGateRunID) ([]contracts.GovernanceReview, error) {
	return s.listReviews(ctx, "tenant_id=$1 AND gate_run_id=$2", tenantID, gateRunID)
}

func (s *GovernanceProcessStore) listReviews(ctx context.Context, where string, args ...any) ([]contracts.GovernanceReview, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT review_json FROM governance_reviews WHERE `+where+`
ORDER BY created_at ASC, review_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.GovernanceReview, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var review contracts.GovernanceReview
		if err := scanJSON(data, &review); err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	return out, rows.Err()
}

func (s *GovernanceProcessStore) SaveConflict(ctx context.Context, conflict contracts.GovernanceConflict) error {
	return s.upsertConflict(ctx, conflict)
}

func (s *GovernanceProcessStore) UpdateConflict(ctx context.Context, conflict contracts.GovernanceConflict) error {
	return s.upsertConflict(ctx, conflict)
}

func (s *GovernanceProcessStore) upsertConflict(ctx context.Context, conflict contracts.GovernanceConflict) error {
	value, err := jsonValue(conflict)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO governance_conflicts (
  conflict_id, gate_run_id, process_run_id, tenant_id, status,
  conflict_json, created_at, resolved_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (conflict_id) DO UPDATE SET
  status=EXCLUDED.status,
  conflict_json=EXCLUDED.conflict_json,
  resolved_at=EXCLUDED.resolved_at`,
		conflict.ConflictID, conflict.GateRunID, conflict.ProcessRunID, conflict.TenantID,
		conflict.Status, value, conflict.CreatedAt.UTC(), nullTime(conflict.ResolvedAt))
	return err
}

func (s *GovernanceProcessStore) GetConflict(ctx context.Context, tenantID contracts.TenantID, conflictID contracts.GovernanceConflictID) (contracts.GovernanceConflict, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `
SELECT conflict_json FROM governance_conflicts
WHERE tenant_id=$1 AND conflict_id=$2`, tenantID, conflictID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GovernanceConflict{}, false, nil
	}
	if err != nil {
		return contracts.GovernanceConflict{}, false, mapSQLError(err)
	}
	var conflict contracts.GovernanceConflict
	if err := scanJSON(data, &conflict); err != nil {
		return contracts.GovernanceConflict{}, false, err
	}
	return conflict, true, nil
}

func (s *GovernanceProcessStore) ListConflicts(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) ([]contracts.GovernanceConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT conflict_json FROM governance_conflicts
WHERE tenant_id=$1 AND process_run_id=$2
ORDER BY created_at ASC, conflict_id ASC`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.GovernanceConflict, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var conflict contracts.GovernanceConflict
		if err := scanJSON(data, &conflict); err != nil {
			return nil, err
		}
		out = append(out, conflict)
	}
	return out, rows.Err()
}

func insertGovernanceGate(ctx context.Context, tx *sql.Tx, gate contracts.GovernanceGateRun) error {
	value, err := jsonValue(gate)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO governance_gate_runs (
  gate_run_id, process_run_id, tenant_id, gate_id, status,
  gate_json, created_at, updated_at, resolved_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		gate.GateRunID, gate.ProcessRunID, gate.TenantID, gate.GateID, gate.Status,
		value, gate.CreatedAt.UTC(), gate.UpdatedAt.UTC(), nullTime(gate.ResolvedAt))
	return err
}

func notFoundIfNoRows(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

type MigrationStore struct {
	db *sql.DB
}

func (s *MigrationStore) Applied(ctx context.Context) (map[string]migration.AppliedMigration, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT version, name, checksum, applied_at FROM clean_core_schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]migration.AppliedMigration{}
	for rows.Next() {
		var record migration.AppliedMigration
		if err := rows.Scan(&record.Version, &record.Name, &record.Checksum, &record.AppliedAt); err != nil {
			return nil, err
		}
		record.AppliedAt = record.AppliedAt.UTC()
		out[record.Version] = record
	}
	return out, rows.Err()
}

func (s *MigrationStore) MarkApplied(ctx context.Context, record migration.AppliedMigration) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO clean_core_schema_migrations (version, name, checksum, applied_at)
VALUES ($1,$2,$3,$4)
ON CONFLICT (version) DO UPDATE SET
  name=EXCLUDED.name,
  checksum=EXCLUDED.checksum,
  applied_at=EXCLUDED.applied_at`,
		record.Version, record.Name, record.Checksum, record.AppliedAt.UTC(),
	)
	return err
}

func (s *MigrationStore) ensure(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS clean_core_schema_migrations (
  version TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL
)`)
	return err
}

type PackageStore struct {
	db *sql.DB
}

func (s *PackageStore) SaveAgentAsset(ctx context.Context, asset agentpackage.AgentAsset) error {
	ensureAgentAssetCarrierDefaults(&asset)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_assets (
  tenant_id, agent_id, name, description, owner_id, status,
  active_version, default_version,
  carrier_kind, runtime_contract, source_kind, source_provider_id, manifest_hash, conformance_status,
  created_by, created_at, updated_at, deleted_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (tenant_id, agent_id) DO UPDATE SET
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  owner_id=EXCLUDED.owner_id,
  status=EXCLUDED.status,
  active_version=EXCLUDED.active_version,
  default_version=EXCLUDED.default_version,
  carrier_kind=EXCLUDED.carrier_kind,
  runtime_contract=EXCLUDED.runtime_contract,
  source_kind=EXCLUDED.source_kind,
  source_provider_id=EXCLUDED.source_provider_id,
  manifest_hash=EXCLUDED.manifest_hash,
  conformance_status=EXCLUDED.conformance_status,
  updated_at=EXCLUDED.updated_at,
  deleted_at=EXCLUDED.deleted_at`,
		asset.TenantID, asset.AgentID, asset.Name, nullString(asset.Description), nullString(asset.OwnerID), asset.Status,
		nullString(string(asset.ActiveVersion)), nullString(string(asset.DefaultVersion)),
		asset.CarrierKind, asset.RuntimeContract, asset.SourceKind, nullString(asset.SourceProviderID), nullString(asset.ManifestHash), asset.ConformanceStatus,
		nullString(string(asset.CreatedBy)),
		asset.CreatedAt.UTC(), asset.UpdatedAt.UTC(), nullTime(asset.DeletedAt),
	)
	return err
}

func (s *PackageStore) GetAgentAsset(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (agentpackage.AgentAsset, bool, error) {
	asset, err := scanAgentAsset(s.db.QueryRowContext(ctx, agentAssetSelectSQL()+" WHERE tenant_id=$1 AND agent_id=$2", tenantID, agentID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.AgentAsset{}, false, nil
	}
	return asset, err == nil, err
}

func (s *PackageStore) ListAgentAssets(ctx context.Context, tenantID contracts.TenantID) ([]agentpackage.AgentAsset, error) {
	query := agentAssetSelectSQL()
	args := []any{}
	if tenantID != "" {
		args = append(args, tenantID)
		query += " WHERE tenant_id=$1"
	}
	query += " ORDER BY created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.AgentAsset, 0)
	for rows.Next() {
		asset, err := scanAgentAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

func (s *PackageStore) ListAgentDefinitions(ctx context.Context) ([]contracts.AgentDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT definition_json
FROM agent_definitions
ORDER BY created_at ASC, tenant_id ASC, agent_id ASC, version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.AgentDefinition, 0)
	for rows.Next() {
		var definitionJSON []byte
		if err := rows.Scan(&definitionJSON); err != nil {
			return nil, err
		}
		definition := contracts.AgentDefinition{}
		if err := scanJSON(definitionJSON, &definition); err != nil {
			return nil, err
		}
		out = append(out, definition)
	}
	return out, rows.Err()
}

func agentAssetSelectSQL() string {
	return `SELECT tenant_id, agent_id, name, description, owner_id, status, active_version, default_version,
carrier_kind, runtime_contract, source_kind, source_provider_id, manifest_hash, conformance_status,
created_by, created_at, updated_at, deleted_at FROM agent_assets`
}

func scanAgentAsset(row interface {
	Scan(dest ...any) error
}) (agentpackage.AgentAsset, error) {
	var tenantID, agentID, status string
	var carrierKind, runtimeContract, sourceKind, conformanceStatus string
	var description, ownerID, activeVersion, defaultVersion, sourceProviderID, manifestHash, createdBy sql.NullString
	var deletedAt sql.NullTime
	asset := agentpackage.AgentAsset{}
	if err := row.Scan(&tenantID, &agentID, &asset.Name, &description, &ownerID, &status,
		&activeVersion, &defaultVersion, &carrierKind, &runtimeContract, &sourceKind, &sourceProviderID, &manifestHash, &conformanceStatus,
		&createdBy, &asset.CreatedAt, &asset.UpdatedAt, &deletedAt); err != nil {
		return agentpackage.AgentAsset{}, mapSQLError(err)
	}
	asset.TenantID = contracts.TenantID(tenantID)
	asset.AgentID = contracts.AgentID(agentID)
	asset.Description = description.String
	asset.OwnerID = ownerID.String
	asset.Status = status
	asset.ActiveVersion = contracts.AgentVersion(activeVersion.String)
	asset.DefaultVersion = contracts.AgentVersion(defaultVersion.String)
	asset.CarrierKind = contracts.AgentCarrierKind(carrierKind)
	asset.RuntimeContract = contracts.RuntimeContractKind(runtimeContract)
	asset.SourceKind = contracts.AgentSourceKind(sourceKind)
	asset.SourceProviderID = sourceProviderID.String
	asset.ManifestHash = manifestHash.String
	asset.ConformanceStatus = contracts.RuntimeConformanceStatus(conformanceStatus)
	asset.CreatedBy = createdBy.String
	asset.CreatedAt = asset.CreatedAt.UTC()
	asset.UpdatedAt = asset.UpdatedAt.UTC()
	asset.DeletedAt = timePtr(deletedAt)
	ensureAgentAssetCarrierDefaults(&asset)
	return asset, nil
}

func ensureAgentAssetCarrierDefaults(asset *agentpackage.AgentAsset) {
	if asset == nil {
		return
	}
	if asset.SourceKind == "" {
		asset.SourceKind = contracts.AgentSourceKindPackage
	}
	asset.CarrierKind = contracts.NormalizeCarrierKind(asset.SourceKind, asset.CarrierKind)
	asset.RuntimeContract = contracts.NormalizeRuntimeContract(asset.CarrierKind, asset.RuntimeContract)
	if asset.ConformanceStatus == "" {
		asset.ConformanceStatus = contracts.RuntimeConformanceUnknown
	}
}

func (s *PackageStore) SaveDraft(ctx context.Context, draft agentpackage.Draft) error {
	source, err := jsonValue(draft.Source)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_package_drafts (
  draft_id, tenant_id, agent_id, version, source_json, status,
  created_by, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (draft_id) DO UPDATE SET
  source_json=EXCLUDED.source_json,
  status=EXCLUDED.status,
  updated_at=EXCLUDED.updated_at`,
		draft.DraftID, draft.TenantID, draft.AgentID, draft.Version, source, draft.Status,
		draft.CreatedBy, draft.CreatedAt.UTC(), draft.UpdatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	return s.saveAgentResourceProjections(ctx, s.db, "draft", draft.DraftID, "", draft.DraftID, draft.TenantID, draft.AgentID, draft.Version, draft.Status, draft.Source, contracts.AgentDefinition{}, draft.UpdatedAt.UTC())
}

func (s *PackageStore) GetDraft(ctx context.Context, draftID string) (agentpackage.Draft, bool, error) {
	draft, err := scanPackageDraft(s.db.QueryRowContext(ctx, `
SELECT draft_id, tenant_id, agent_id, version, source_json, status, created_by, created_at, updated_at
FROM agent_package_drafts WHERE draft_id=$1`, draftID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.Draft{}, false, nil
	}
	return draft, err == nil, err
}

func (s *PackageStore) ListDrafts(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.Draft, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT draft_id, tenant_id, agent_id, version, source_json, status, created_by, created_at, updated_at
FROM agent_package_drafts
WHERE tenant_id=$1 AND ($2 = '' OR agent_id=$2)
ORDER BY updated_at DESC`, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.Draft, 0)
	for rows.Next() {
		draft, err := scanPackageDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, draft)
	}
	return out, rows.Err()
}

func scanPackageDraft(row interface {
	Scan(dest ...any) error
}) (agentpackage.Draft, error) {
	var tenantID, agentID, version, status string
	var sourceJSON []byte
	draft := agentpackage.Draft{}
	if err := row.Scan(&draft.DraftID, &tenantID, &agentID, &version, &sourceJSON, &status, &draft.CreatedBy, &draft.CreatedAt, &draft.UpdatedAt); err != nil {
		return agentpackage.Draft{}, mapSQLError(err)
	}
	draft.TenantID = contracts.TenantID(tenantID)
	draft.AgentID = contracts.AgentID(agentID)
	draft.Version = contracts.AgentVersion(version)
	draft.Status = contracts.ReleaseStatus(status)
	if err := scanJSON(sourceJSON, &draft.Source); err != nil {
		return agentpackage.Draft{}, err
	}
	draft.CreatedAt = draft.CreatedAt.UTC()
	draft.UpdatedAt = draft.UpdatedAt.UTC()
	return draft, nil
}

func (s *PackageStore) saveAgentResourceProjections(ctx context.Context, q dbtx, sourceKind string, sourceID string, packageVersionID string, draftID string, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, status contracts.ReleaseStatus, source agentpackage.AgentPackageSource, compiled contracts.AgentDefinition, updatedAt time.Time) error {
	if sourceID == "" {
		return nil
	}
	if compiled.AgentID == "" {
		var err error
		compiled, err = agentpackage.Compile(agentID, version, source)
		if err != nil {
			compiled = contracts.AgentDefinition{
				TenantID:         tenantID,
				AgentID:          agentID,
				Version:          version,
				IdentityPrompt:   source.Prompt,
				Tools:            source.ToolBindings,
				Collaborators:    source.Collaborators,
				Exports:          source.Exports,
				RuntimeHooks:     source.RuntimeHooks,
				PackageVersionID: contracts.PackageVersionID(packageVersionID),
			}
		}
	}
	compiled.TenantID = tenantID
	if compiled.PackageVersionID == "" && packageVersionID != "" {
		compiled.PackageVersionID = contracts.PackageVersionID(packageVersionID)
	}
	for _, table := range []string{"agent_prompt_profiles", "agent_skill_definitions", "agent_tool_bindings", "agent_collaborators", "agent_exported_tools"} {
		if _, err := q.ExecContext(ctx, "DELETE FROM "+table+" WHERE tenant_id=$1 AND source_kind=$2 AND source_id=$3", tenantID, sourceKind, sourceID); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `
INSERT INTO agent_prompt_profiles (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, identity_prompt, system_prompt, developer_prompt, agents_md, source_json, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		tenantID, agentID, version, sourceKind, sourceID, nullString(packageVersionID), nullString(draftID),
		status, nullString(compiled.IdentityPrompt), nullString(compiled.SystemPrompt), nullString(compiled.DeveloperPrompt),
		nullString(source.AgentsMD), jsonBytes(source), updatedAt); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
INSERT INTO agent_tool_bindings (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, bindings_json, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tenantID, agentID, version, sourceKind, sourceID, nullString(packageVersionID), nullString(draftID),
		status, jsonBytes(compiled.Tools), updatedAt); err != nil {
		return err
	}
	for _, skill := range compiled.SkillDefinitions {
		skillID := skill.Card.SkillID
		skillVersion := skill.Card.Version
		if skillID == "" {
			continue
		}
		if _, err := q.ExecContext(ctx, `
INSERT INTO agent_skill_definitions (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, skill_id, skill_version, definition_json, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			tenantID, agentID, version, sourceKind, sourceID, nullString(packageVersionID), nullString(draftID),
			status, skillID, skillVersion, jsonBytes(skill), updatedAt); err != nil {
			return err
		}
	}
	for _, collaborator := range compiled.Collaborators {
		if collaborator.AgentID == "" {
			continue
		}
		if _, err := q.ExecContext(ctx, `
INSERT INTO agent_collaborators (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, collaborator_agent_id, collaborator_version, collaborator_json, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			tenantID, agentID, version, sourceKind, sourceID, nullString(packageVersionID), nullString(draftID),
			status, collaborator.AgentID, collaborator.Version, jsonBytes(collaborator), updatedAt); err != nil {
			return err
		}
	}
	for _, exported := range compiled.Exports.Tools {
		if exported.ToolID == "" {
			continue
		}
		if _, err := q.ExecContext(ctx, `
INSERT INTO agent_exported_tools (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, tool_id, tool_version, tool_json, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			tenantID, agentID, version, sourceKind, sourceID, nullString(packageVersionID), nullString(draftID),
			status, exported.ToolID, exported.Version, jsonBytes(exported), updatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *PackageStore) GetPromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (agentpackage.PromptProfileProjection, bool, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok {
		return agentpackage.PromptProfileProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       COALESCE(identity_prompt, ''), COALESCE(system_prompt, ''),
       COALESCE(developer_prompt, ''), COALESCE(agents_md, ''), updated_at
FROM agent_prompt_profiles WHERE `+where+`
ORDER BY updated_at DESC LIMIT 1`, args...)
	profile, err := scanPromptProfileProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.PromptProfileProjection{}, false, nil
	}
	return profile, err == nil, err
}

func (s *PackageStore) GetActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (agentpackage.PromptProfileProjection, bool, error) {
	if tenantID == "" || agentID == "" {
		return agentpackage.PromptProfileProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       COALESCE(identity_prompt, ''), COALESCE(system_prompt, ''),
       COALESCE(developer_prompt, ''), COALESCE(agents_md, ''), updated_at
FROM agent_prompt_profiles
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4
ORDER BY updated_at DESC LIMIT 1`,
		tenantID, agentID, agentpackage.PromptProfileSourceKind, string(agentID))
	profile, err := scanPromptProfileProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.PromptProfileProjection{}, false, nil
	}
	return profile, err == nil, err
}

func (s *PackageStore) UpsertPromptProfileProjection(ctx context.Context, profile agentpackage.PromptProfileProjection) error {
	source := agentpackage.AgentPackageSource{
		AgentsMD: profile.AgentsMD,
		Prompt:   profile.IdentityPrompt,
		Metadata: map[string]any{
			"system_prompt":    profile.SystemPrompt,
			"developer_prompt": profile.DeveloperPrompt,
		},
	}
	if profile.SourceKind == "" {
		profile.SourceKind = agentpackage.PromptProfileSourceKind
	}
	if profile.SourceID == "" {
		profile.SourceID = string(profile.AgentID)
	}
	if profile.Status == "" {
		profile.Status = contracts.ReleaseStable
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_prompt_profiles (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, identity_prompt, system_prompt, developer_prompt, agents_md, source_json, updated_at
) VALUES ($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, source_kind, source_id) DO UPDATE SET
  agent_id=EXCLUDED.agent_id,
  version=EXCLUDED.version,
  package_version_id=NULL,
  draft_id=NULL,
  status=EXCLUDED.status,
  identity_prompt=EXCLUDED.identity_prompt,
  system_prompt=EXCLUDED.system_prompt,
  developer_prompt=EXCLUDED.developer_prompt,
  agents_md=EXCLUDED.agents_md,
  source_json=EXCLUDED.source_json,
  updated_at=EXCLUDED.updated_at`,
		profile.TenantID, profile.AgentID, profile.Version, profile.SourceKind, profile.SourceID, profile.Status,
		nullString(profile.IdentityPrompt), nullString(profile.SystemPrompt), nullString(profile.DeveloperPrompt),
		nullString(profile.AgentsMD), jsonBytes(source), profile.UpdatedAt.UTC())
	return err
}

func (s *PackageStore) DeleteActivePromptProfileProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error {
	if tenantID == "" || agentID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM agent_prompt_profiles
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4`,
		tenantID, agentID, agentpackage.PromptProfileSourceKind, string(agentID))
	return err
}

func (s *PackageStore) GetToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (agentpackage.ToolBindingProjection, bool, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok {
		return agentpackage.ToolBindingProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       bindings_json, updated_at
FROM agent_tool_bindings WHERE `+where+`
ORDER BY updated_at DESC LIMIT 1`, args...)
	binding, err := scanToolBindingProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.ToolBindingProjection{}, false, nil
	}
	return binding, err == nil, err
}

func (s *PackageStore) GetActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) (agentpackage.ToolBindingProjection, bool, error) {
	if tenantID == "" || agentID == "" {
		return agentpackage.ToolBindingProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       bindings_json, updated_at
FROM agent_tool_bindings
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4
ORDER BY updated_at DESC LIMIT 1`,
		tenantID, agentID, agentpackage.ToolBindingSourceKind, string(agentID))
	binding, err := scanToolBindingProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.ToolBindingProjection{}, false, nil
	}
	return binding, err == nil, err
}

func (s *PackageStore) UpsertToolBindingProjection(ctx context.Context, binding agentpackage.ToolBindingProjection) error {
	if binding.SourceKind == "" {
		binding.SourceKind = agentpackage.ToolBindingSourceKind
	}
	if binding.SourceID == "" {
		binding.SourceID = string(binding.AgentID)
	}
	if binding.Status == "" {
		binding.Status = contracts.ReleaseStable
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_tool_bindings (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, bindings_json, updated_at
) VALUES ($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8)
ON CONFLICT (tenant_id, source_kind, source_id) DO UPDATE SET
  agent_id=EXCLUDED.agent_id,
  version=EXCLUDED.version,
  package_version_id=NULL,
  draft_id=NULL,
  status=EXCLUDED.status,
  bindings_json=EXCLUDED.bindings_json,
  updated_at=EXCLUDED.updated_at`,
		binding.TenantID, binding.AgentID, binding.Version, binding.SourceKind, binding.SourceID, binding.Status,
		jsonBytes(binding.Bindings), binding.UpdatedAt.UTC())
	return err
}

func (s *PackageStore) DeleteActiveToolBindingProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error {
	if tenantID == "" || agentID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM agent_tool_bindings
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4`,
		tenantID, agentID, agentpackage.ToolBindingSourceKind, string(agentID))
	return err
}

func (s *PackageStore) ListSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.SkillDefinitionProjection, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       skill_id, skill_version, definition_json, updated_at
FROM agent_skill_definitions WHERE `+where+`
ORDER BY skill_id ASC, skill_version ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.SkillDefinitionProjection, 0)
	for rows.Next() {
		skill, err := scanSkillDefinitionProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, skillID string) (agentpackage.SkillDefinitionProjection, bool, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok || strings.TrimSpace(skillID) == "" {
		return agentpackage.SkillDefinitionProjection{}, false, nil
	}
	args = append(args, skillID)
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       skill_id, skill_version, definition_json, updated_at
FROM agent_skill_definitions WHERE `+where+fmt.Sprintf(" AND skill_id=$%d", len(args))+`
ORDER BY skill_version DESC LIMIT 1`, args...)
	skill, err := scanSkillDefinitionProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.SkillDefinitionProjection{}, false, nil
	}
	return skill, err == nil, err
}

func (s *PackageStore) ListActiveSkillDefinitionProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.SkillDefinitionProjection, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       skill_id, skill_version, definition_json, updated_at
FROM agent_skill_definitions
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4
  AND status=$5
ORDER BY skill_id ASC, skill_version ASC`,
		tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), contracts.ReleaseStable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.SkillDefinitionProjection, 0)
	for rows.Next() {
		skill, err := scanSkillDefinitionProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) (agentpackage.SkillDefinitionProjection, bool, error) {
	if tenantID == "" || agentID == "" || strings.TrimSpace(skillID) == "" {
		return agentpackage.SkillDefinitionProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       skill_id, skill_version, definition_json, updated_at
FROM agent_skill_definitions
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5
  AND status=$6
ORDER BY updated_at DESC LIMIT 1`,
		tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), strings.TrimSpace(skillID), contracts.ReleaseStable)
	skill, err := scanSkillDefinitionProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.SkillDefinitionProjection{}, false, nil
	}
	return skill, err == nil, err
}

func (s *PackageStore) UpsertSkillDefinitionProjection(ctx context.Context, skill agentpackage.SkillDefinitionProjection) error {
	if skill.SourceKind == "" {
		skill.SourceKind = agentpackage.SkillDefinitionSourceKind
	}
	if skill.SourceID == "" {
		skill.SourceID = string(skill.AgentID)
	}
	if skill.Status == "" {
		skill.Status = contracts.ReleaseStable
	}
	if skill.SkillID == "" {
		skill.SkillID = skill.Definition.Card.SkillID
	}
	if skill.SkillVersion == "" {
		skill.SkillVersion = skill.Definition.Card.Version
	}
	if skill.UpdatedAt.IsZero() {
		skill.UpdatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if skill.Status == contracts.ReleaseStable {
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_skill_definitions
SET status=$6
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5 AND status=$7`,
			skill.TenantID, skill.AgentID, skill.SourceKind, skill.SourceID, skill.SkillID, contracts.ReleasePublished, contracts.ReleaseStable); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM agent_skill_definitions
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5 AND skill_version=$6`,
		skill.TenantID, skill.AgentID, skill.SourceKind, skill.SourceID, skill.SkillID, skill.SkillVersion); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_skill_definitions (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, skill_id, skill_version, definition_json, updated_at
) VALUES ($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8,$9,$10)`,
		skill.TenantID, skill.AgentID, skill.Version, skill.SourceKind, skill.SourceID, skill.Status,
		skill.SkillID, skill.SkillVersion, jsonBytes(skill.Definition), skill.UpdatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) DeleteActiveSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) error {
	if tenantID == "" || agentID == "" || strings.TrimSpace(skillID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE agent_skill_definitions
SET status=$6,
    definition_json=jsonb_set(definition_json::jsonb, '{card,status}', '"deleted"'::jsonb, true),
    updated_at=$7
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5`,
		tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), strings.TrimSpace(skillID), contracts.ReleaseDeprecated, time.Now().UTC())
	return err
}

func (s *PackageStore) ListSkillDefinitionProjectionVersions(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string) ([]agentpackage.SkillDefinitionProjection, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	filterSkillID := strings.TrimSpace(skillID)
	args := []any{tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), contracts.ReleaseStable}
	whereSkill := ""
	if filterSkillID != "" {
		args = append(args, filterSkillID)
		whereSkill = fmt.Sprintf(" AND skill_id=$%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       skill_id, skill_version, definition_json, updated_at
FROM agent_skill_definitions
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4`+whereSkill+`
ORDER BY
  CASE WHEN status=$5 THEN 0 ELSE 1 END,
  updated_at DESC,
  skill_version DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.SkillDefinitionProjection, 0)
	for rows.Next() {
		skill, err := scanSkillDefinitionProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func (s *PackageStore) ActivateSkillDefinitionProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, skillID string, skillVersion string) error {
	skillID = strings.TrimSpace(skillID)
	skillVersion = strings.TrimSpace(skillVersion)
	if tenantID == "" || agentID == "" || skillID == "" || skillVersion == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE agent_skill_definitions
SET status=$7, updated_at=$8
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5 AND skill_version=$6`,
		tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), skillID, skillVersion, contracts.ReleaseStable, time.Now().UTC())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "skill version not found", map[string]any{"skill_id": skillID, "skill_version": skillVersion})
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_skill_definitions
SET status=$7
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND skill_id=$5 AND skill_version<>$6 AND status=$8`,
		tenantID, agentID, agentpackage.SkillDefinitionSourceKind, string(agentID), skillID, skillVersion, contracts.ReleasePublished, contracts.ReleaseStable); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) ListCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.CollaboratorProjection, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       collaborator_agent_id, collaborator_version, collaborator_json, updated_at
FROM agent_collaborators WHERE `+where+`
ORDER BY collaborator_agent_id ASC, collaborator_version ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.CollaboratorProjection, 0)
	for rows.Next() {
		collaborator, err := scanCollaboratorProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, collaborator)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, collaboratorAgentID contracts.AgentID) (agentpackage.CollaboratorProjection, bool, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok || collaboratorAgentID == "" {
		return agentpackage.CollaboratorProjection{}, false, nil
	}
	args = append(args, collaboratorAgentID)
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       collaborator_agent_id, collaborator_version, collaborator_json, updated_at
FROM agent_collaborators WHERE `+where+fmt.Sprintf(" AND collaborator_agent_id=$%d", len(args))+`
ORDER BY collaborator_version DESC LIMIT 1`, args...)
	collaborator, err := scanCollaboratorProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.CollaboratorProjection{}, false, nil
	}
	return collaborator, err == nil, err
}

func (s *PackageStore) ListActiveCollaboratorProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.CollaboratorProjection, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       collaborator_agent_id, collaborator_version, collaborator_json, updated_at
FROM agent_collaborators
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4
ORDER BY collaborator_agent_id ASC, collaborator_version ASC`,
		tenantID, agentID, agentpackage.CollaboratorSourceKind, string(agentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.CollaboratorProjection, 0)
	for rows.Next() {
		collaborator, err := scanCollaboratorProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, collaborator)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) (agentpackage.CollaboratorProjection, bool, error) {
	if tenantID == "" || agentID == "" || collaboratorAgentID == "" {
		return agentpackage.CollaboratorProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       collaborator_agent_id, collaborator_version, collaborator_json, updated_at
FROM agent_collaborators
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND collaborator_agent_id=$5
ORDER BY updated_at DESC LIMIT 1`,
		tenantID, agentID, agentpackage.CollaboratorSourceKind, string(agentID), collaboratorAgentID)
	collaborator, err := scanCollaboratorProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.CollaboratorProjection{}, false, nil
	}
	return collaborator, err == nil, err
}

func (s *PackageStore) UpsertCollaboratorProjection(ctx context.Context, collaborator agentpackage.CollaboratorProjection) error {
	if collaborator.SourceKind == "" {
		collaborator.SourceKind = agentpackage.CollaboratorSourceKind
	}
	if collaborator.SourceID == "" {
		collaborator.SourceID = string(collaborator.AgentID)
	}
	if collaborator.Status == "" {
		collaborator.Status = contracts.ReleaseStable
	}
	if collaborator.CollaboratorAgentID == "" {
		collaborator.CollaboratorAgentID = collaborator.Collaborator.AgentID
	}
	if collaborator.CollaboratorVersion == "" {
		collaborator.CollaboratorVersion = collaborator.Collaborator.Version
	}
	if collaborator.UpdatedAt.IsZero() {
		collaborator.UpdatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
DELETE FROM agent_collaborators
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND collaborator_agent_id=$5`,
		collaborator.TenantID, collaborator.AgentID, collaborator.SourceKind, collaborator.SourceID, collaborator.CollaboratorAgentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_collaborators (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, collaborator_agent_id, collaborator_version, collaborator_json, updated_at
) VALUES ($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8,$9,$10)`,
		collaborator.TenantID, collaborator.AgentID, collaborator.Version, collaborator.SourceKind, collaborator.SourceID, collaborator.Status,
		collaborator.CollaboratorAgentID, collaborator.CollaboratorVersion, jsonBytes(collaborator.Collaborator), collaborator.UpdatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) DeleteActiveCollaboratorProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, collaboratorAgentID contracts.AgentID) error {
	if tenantID == "" || agentID == "" || collaboratorAgentID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM agent_collaborators
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND collaborator_agent_id=$5`,
		tenantID, agentID, agentpackage.CollaboratorSourceKind, string(agentID), collaboratorAgentID)
	return err
}

func (s *PackageStore) ListExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) ([]agentpackage.ExportedToolProjection, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       tool_id, tool_version, tool_json, updated_at
FROM agent_exported_tools WHERE `+where+`
ORDER BY tool_id ASC, tool_version ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.ExportedToolProjection, 0)
	for rows.Next() {
		tool, err := scanExportedToolProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string, toolID string) (agentpackage.ExportedToolProjection, bool, error) {
	where, args, ok := agentProjectionSelector(tenantID, agentID, version, draftID)
	if !ok || strings.TrimSpace(toolID) == "" {
		return agentpackage.ExportedToolProjection{}, false, nil
	}
	args = append(args, strings.TrimSpace(toolID))
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       tool_id, tool_version, tool_json, updated_at
FROM agent_exported_tools WHERE `+where+fmt.Sprintf(" AND tool_id=$%d", len(args))+`
ORDER BY tool_version DESC LIMIT 1`, args...)
	tool, err := scanExportedToolProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.ExportedToolProjection{}, false, nil
	}
	return tool, err == nil, err
}

func (s *PackageStore) ListActiveExportedToolProjections(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]agentpackage.ExportedToolProjection, error) {
	if tenantID == "" || agentID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       tool_id, tool_version, tool_json, updated_at
FROM agent_exported_tools
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4
ORDER BY tool_id ASC, tool_version ASC`,
		tenantID, agentID, agentpackage.ExportedToolSourceKind, string(agentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentpackage.ExportedToolProjection, 0)
	for rows.Next() {
		tool, err := scanExportedToolProjection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	return out, rows.Err()
}

func (s *PackageStore) GetActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) (agentpackage.ExportedToolProjection, bool, error) {
	if tenantID == "" || agentID == "" || strings.TrimSpace(toolID) == "" {
		return agentpackage.ExportedToolProjection{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, agent_id, version, source_kind, source_id,
       COALESCE(package_version_id, ''), COALESCE(draft_id, ''), status,
       tool_id, tool_version, tool_json, updated_at
FROM agent_exported_tools
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND tool_id=$5
ORDER BY updated_at DESC LIMIT 1`,
		tenantID, agentID, agentpackage.ExportedToolSourceKind, string(agentID), strings.TrimSpace(toolID))
	tool, err := scanExportedToolProjection(row)
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.ExportedToolProjection{}, false, nil
	}
	return tool, err == nil, err
}

func (s *PackageStore) UpsertExportedToolProjection(ctx context.Context, tool agentpackage.ExportedToolProjection) error {
	if tool.SourceKind == "" {
		tool.SourceKind = agentpackage.ExportedToolSourceKind
	}
	if tool.SourceID == "" {
		tool.SourceID = string(tool.AgentID)
	}
	if tool.Status == "" {
		tool.Status = contracts.ReleaseStable
	}
	if tool.ToolID == "" {
		tool.ToolID = tool.Tool.ToolID
	}
	if tool.ToolVersion == "" {
		tool.ToolVersion = tool.Tool.Version
	}
	if tool.UpdatedAt.IsZero() {
		tool.UpdatedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
DELETE FROM agent_exported_tools
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND tool_id=$5`,
		tool.TenantID, tool.AgentID, tool.SourceKind, tool.SourceID, tool.ToolID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_exported_tools (
  tenant_id, agent_id, version, source_kind, source_id, package_version_id, draft_id,
  status, tool_id, tool_version, tool_json, updated_at
) VALUES ($1,$2,$3,$4,$5,NULL,NULL,$6,$7,$8,$9,$10)`,
		tool.TenantID, tool.AgentID, tool.Version, tool.SourceKind, tool.SourceID, tool.Status,
		tool.ToolID, tool.ToolVersion, jsonBytes(tool.Tool), tool.UpdatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) DeleteActiveExportedToolProjection(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, toolID string) error {
	if tenantID == "" || agentID == "" || strings.TrimSpace(toolID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM agent_exported_tools
WHERE tenant_id=$1 AND agent_id=$2 AND source_kind=$3 AND source_id=$4 AND tool_id=$5`,
		tenantID, agentID, agentpackage.ExportedToolSourceKind, string(agentID), strings.TrimSpace(toolID))
	return err
}

func agentProjectionSelector(tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, draftID string) (string, []any, bool) {
	if tenantID == "" || agentID == "" {
		return "", nil, false
	}
	if strings.TrimSpace(draftID) != "" {
		return "tenant_id=$1 AND agent_id=$2 AND source_kind='draft' AND draft_id=$3", []any{tenantID, agentID, strings.TrimSpace(draftID)}, true
	}
	if version == "" {
		return "", nil, false
	}
	return "tenant_id=$1 AND agent_id=$2 AND source_kind='release' AND version=$3", []any{tenantID, agentID, version}, true
}

func scanProjectionBase(row interface {
	Scan(dest ...any) error
}, extra ...any) (contracts.TenantID, contracts.AgentID, contracts.AgentVersion, string, string, contracts.PackageVersionID, string, contracts.ReleaseStatus, time.Time, error) {
	var tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status string
	var updatedAt time.Time
	dest := []any{&tenantID, &agentID, &version, &sourceKind, &sourceID, &packageVersionID, &draftID, &status}
	dest = append(dest, extra...)
	dest = append(dest, &updatedAt)
	if err := row.Scan(dest...); err != nil {
		return "", "", "", "", "", "", "", "", time.Time{}, mapSQLError(err)
	}
	return contracts.TenantID(tenantID), contracts.AgentID(agentID), contracts.AgentVersion(version), sourceKind, sourceID, contracts.PackageVersionID(packageVersionID), draftID, contracts.ReleaseStatus(status), updatedAt.UTC(), nil
}

func scanPromptProfileProjection(row interface {
	Scan(dest ...any) error
}) (agentpackage.PromptProfileProjection, error) {
	var identityPrompt, systemPrompt, developerPrompt, agentsMD string
	tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, err := scanProjectionBase(row, &identityPrompt, &systemPrompt, &developerPrompt, &agentsMD)
	if err != nil {
		return agentpackage.PromptProfileProjection{}, err
	}
	return agentpackage.PromptProfileProjection{
		TenantID:         tenantID,
		AgentID:          agentID,
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: packageVersionID,
		DraftID:          draftID,
		Status:           status,
		IdentityPrompt:   identityPrompt,
		SystemPrompt:     systemPrompt,
		DeveloperPrompt:  developerPrompt,
		AgentsMD:         agentsMD,
		UpdatedAt:        updatedAt,
	}, nil
}

func scanToolBindingProjection(row interface {
	Scan(dest ...any) error
}) (agentpackage.ToolBindingProjection, error) {
	var bindingsJSON []byte
	tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, err := scanProjectionBase(row, &bindingsJSON)
	if err != nil {
		return agentpackage.ToolBindingProjection{}, err
	}
	var bindings contracts.AgentToolsConfig
	if err := scanJSON(bindingsJSON, &bindings); err != nil {
		return agentpackage.ToolBindingProjection{}, err
	}
	return agentpackage.ToolBindingProjection{
		TenantID:         tenantID,
		AgentID:          agentID,
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: packageVersionID,
		DraftID:          draftID,
		Status:           status,
		Bindings:         bindings,
		UpdatedAt:        updatedAt,
	}, nil
}

func scanSkillDefinitionProjection(row interface {
	Scan(dest ...any) error
}) (agentpackage.SkillDefinitionProjection, error) {
	var skillID, skillVersion string
	var definitionJSON []byte
	tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, err := scanProjectionBase(row, &skillID, &skillVersion, &definitionJSON)
	if err != nil {
		return agentpackage.SkillDefinitionProjection{}, err
	}
	var definition contracts.SkillDefinition
	if err := scanJSON(definitionJSON, &definition); err != nil {
		return agentpackage.SkillDefinitionProjection{}, err
	}
	return agentpackage.SkillDefinitionProjection{
		TenantID:         tenantID,
		AgentID:          agentID,
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: packageVersionID,
		DraftID:          draftID,
		Status:           status,
		SkillID:          skillID,
		SkillVersion:     skillVersion,
		Definition:       definition,
		UpdatedAt:        updatedAt,
	}, nil
}

func scanCollaboratorProjection(row interface {
	Scan(dest ...any) error
}) (agentpackage.CollaboratorProjection, error) {
	var collaboratorAgentID, collaboratorVersion string
	var collaboratorJSON []byte
	tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, err := scanProjectionBase(row, &collaboratorAgentID, &collaboratorVersion, &collaboratorJSON)
	if err != nil {
		return agentpackage.CollaboratorProjection{}, err
	}
	var collaborator contracts.AgentCollaboratorRef
	if err := scanJSON(collaboratorJSON, &collaborator); err != nil {
		return agentpackage.CollaboratorProjection{}, err
	}
	return agentpackage.CollaboratorProjection{
		TenantID:            tenantID,
		AgentID:             agentID,
		Version:             version,
		SourceKind:          sourceKind,
		SourceID:            sourceID,
		PackageVersionID:    packageVersionID,
		DraftID:             draftID,
		Status:              status,
		CollaboratorAgentID: contracts.AgentID(collaboratorAgentID),
		CollaboratorVersion: contracts.AgentVersion(collaboratorVersion),
		Collaborator:        collaborator,
		UpdatedAt:           updatedAt,
	}, nil
}

func scanExportedToolProjection(row interface {
	Scan(dest ...any) error
}) (agentpackage.ExportedToolProjection, error) {
	var toolID, toolVersion string
	var toolJSON []byte
	tenantID, agentID, version, sourceKind, sourceID, packageVersionID, draftID, status, updatedAt, err := scanProjectionBase(row, &toolID, &toolVersion, &toolJSON)
	if err != nil {
		return agentpackage.ExportedToolProjection{}, err
	}
	var tool contracts.AgentExportedTool
	if err := scanJSON(toolJSON, &tool); err != nil {
		return agentpackage.ExportedToolProjection{}, err
	}
	return agentpackage.ExportedToolProjection{
		TenantID:         tenantID,
		AgentID:          agentID,
		Version:          version,
		SourceKind:       sourceKind,
		SourceID:         sourceID,
		PackageVersionID: packageVersionID,
		DraftID:          draftID,
		Status:           status,
		ToolID:           toolID,
		ToolVersion:      toolVersion,
		Tool:             tool,
		UpdatedAt:        updatedAt,
	}, nil
}

func (s *PackageStore) SaveProposal(ctx context.Context, proposal agentpackage.Proposal) error {
	patch, err := jsonValue(proposal.Patch)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_package_proposals (
  proposal_id, tenant_id, draft_id, agent_id, version, proposal_type,
  title, reason, patch_json, status, created_by, reviewed_by, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (proposal_id) DO UPDATE SET
  title=EXCLUDED.title,
  reason=EXCLUDED.reason,
  patch_json=EXCLUDED.patch_json,
  status=EXCLUDED.status,
  reviewed_by=EXCLUDED.reviewed_by,
  updated_at=EXCLUDED.updated_at`,
		proposal.ProposalID, proposal.TenantID, proposal.DraftID, proposal.AgentID, proposal.Version, proposal.Type,
		nullString(proposal.Title), nullString(proposal.Reason), patch, proposal.Status, proposal.CreatedBy,
		nullString(proposal.ReviewedBy), proposal.CreatedAt.UTC(), proposal.UpdatedAt.UTC(),
	)
	return err
}

func (s *PackageStore) GetProposal(ctx context.Context, proposalID contracts.ProposalID) (agentpackage.Proposal, bool, error) {
	proposal, err := scanPackageProposal(s.db.QueryRowContext(ctx, `
SELECT proposal_id, tenant_id, draft_id, agent_id, version, proposal_type,
title, reason, patch_json, status, created_by, reviewed_by, created_at, updated_at
FROM agent_package_proposals WHERE proposal_id=$1`, proposalID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return agentpackage.Proposal{}, false, nil
	}
	return proposal, err == nil, err
}

func scanPackageProposal(row interface {
	Scan(dest ...any) error
}) (agentpackage.Proposal, error) {
	var proposalID, tenantID, draftID, agentID, version, proposalType, status string
	var title, reason, reviewedBy sql.NullString
	var patchJSON []byte
	proposal := agentpackage.Proposal{}
	if err := row.Scan(&proposalID, &tenantID, &draftID, &agentID, &version, &proposalType,
		&title, &reason, &patchJSON, &status, &proposal.CreatedBy, &reviewedBy, &proposal.CreatedAt, &proposal.UpdatedAt); err != nil {
		return agentpackage.Proposal{}, mapSQLError(err)
	}
	proposal.ProposalID = contracts.ProposalID(proposalID)
	proposal.TenantID = contracts.TenantID(tenantID)
	proposal.DraftID = draftID
	proposal.AgentID = contracts.AgentID(agentID)
	proposal.Version = contracts.AgentVersion(version)
	proposal.Type = proposalType
	proposal.Title = title.String
	proposal.Reason = reason.String
	proposal.Status = contracts.ProposalStatus(status)
	proposal.ReviewedBy = reviewedBy.String
	if len(patchJSON) > 0 {
		if err := scanJSON(patchJSON, &proposal.Patch); err != nil {
			return agentpackage.Proposal{}, err
		}
	}
	proposal.CreatedAt = proposal.CreatedAt.UTC()
	proposal.UpdatedAt = proposal.UpdatedAt.UTC()
	return proposal, nil
}

func (s *PackageStore) SaveRelease(ctx context.Context, release contracts.AgentPackageVersion, source agentpackage.AgentPackageSource, compiled contracts.AgentDefinition) error {
	sourceJSON, err := jsonValue(source)
	if err != nil {
		return err
	}
	compiledJSON, err := jsonValue(compiled)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO agent_package_versions (
  package_version_id, tenant_id, agent_id, version, status, source_hash,
  compiled_hash, source_kind, source_provider_id, manifest_version, manifest_hash,
  carrier_kind, runtime_contract, conformance_status,
  source_json, compiled_json, created_by, created_at, published_at,
  canary_percent, canary_scope_json
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (tenant_id, agent_id, version) DO UPDATE SET
  status=EXCLUDED.status,
  source_hash=EXCLUDED.source_hash,
  compiled_hash=EXCLUDED.compiled_hash,
  source_kind=EXCLUDED.source_kind,
  source_provider_id=EXCLUDED.source_provider_id,
  manifest_version=EXCLUDED.manifest_version,
  manifest_hash=EXCLUDED.manifest_hash,
  carrier_kind=EXCLUDED.carrier_kind,
  runtime_contract=EXCLUDED.runtime_contract,
  conformance_status=EXCLUDED.conformance_status,
  source_json=EXCLUDED.source_json,
  compiled_json=EXCLUDED.compiled_json,
  published_at=EXCLUDED.published_at,
  canary_percent=EXCLUDED.canary_percent,
  canary_scope_json=EXCLUDED.canary_scope_json`,
		release.PackageVersionID, release.TenantID, release.AgentID, release.Version, release.Status,
		release.SourceHash, release.CompiledHash,
		contracts.NormalizeSourceKind(release.SourceKind), nullString(release.SourceProviderID), nullString(release.ManifestVersion), nullString(release.ManifestHash),
		contracts.NormalizeCarrierKind(release.SourceKind, release.CarrierKind), contracts.NormalizeRuntimeContract(contracts.NormalizeCarrierKind(release.SourceKind, release.CarrierKind), release.RuntimeContract), release.ConformanceStatus,
		sourceJSON, compiledJSON, release.CreatedBy,
		release.CreatedAt.UTC(), nullTime(release.PublishedAt), release.CanaryPercent, jsonBytes(release.CanaryScope),
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO agent_definitions (tenant_id, agent_id, version, definition_json, package_version_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (tenant_id, agent_id, version) DO UPDATE SET
  definition_json=EXCLUDED.definition_json,
  package_version_id=EXCLUDED.package_version_id`,
		release.TenantID, release.AgentID, release.Version, compiledJSON,
		release.PackageVersionID, release.CreatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	if err := s.saveAgentResourceProjections(ctx, tx, "release", string(release.PackageVersionID), string(release.PackageVersionID), "", release.TenantID, release.AgentID, release.Version, release.Status, source, compiled, release.CreatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) UpdateReleaseStatus(ctx context.Context, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE agent_package_versions SET status=$2 WHERE package_version_id=$1`, packageVersionID, status)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	if err := updateAgentProjectionStatus(ctx, tx, packageVersionID, status); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PackageStore) UpdateReleaseCanary(ctx context.Context, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus, percent int, scope []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE agent_package_versions SET status=$2, canary_percent=$3, canary_scope_json=$4 WHERE package_version_id=$1`,
		packageVersionID, status, percent, jsonBytes(scope))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	if err := updateAgentProjectionStatus(ctx, tx, packageVersionID, status); err != nil {
		return err
	}
	return tx.Commit()
}

func updateAgentProjectionStatus(ctx context.Context, q dbtx, packageVersionID contracts.PackageVersionID, status contracts.ReleaseStatus) error {
	for _, table := range []string{"agent_prompt_profiles", "agent_skill_definitions", "agent_tool_bindings", "agent_collaborators", "agent_exported_tools"} {
		if _, err := q.ExecContext(ctx, "UPDATE "+table+" SET status=$2 WHERE source_kind='release' AND source_id=$1", packageVersionID, status); err != nil {
			return err
		}
	}
	return nil
}

func (s *PackageStore) MarkEvalResult(ctx context.Context, packageVersionID contracts.PackageVersionID, passed bool) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_package_eval_results (package_version_id, passed, evaluated_at)
VALUES ($1,$2,$3)
ON CONFLICT (package_version_id) DO UPDATE SET
  passed=EXCLUDED.passed,
  evaluated_at=EXCLUDED.evaluated_at`,
		packageVersionID, passed, time.Now().UTC(),
	)
	return err
}

func (s *PackageStore) GetRelease(ctx context.Context, packageVersionID contracts.PackageVersionID) (contracts.AgentPackageVersion, bool, error) {
	release, err := scanPackageRelease(s.db.QueryRowContext(ctx, packageReleaseSelectSQL()+" WHERE package_version_id=$1", packageVersionID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.AgentPackageVersion{}, false, nil
	}
	return release, err == nil, err
}

func (s *PackageStore) ListReleases(ctx context.Context) ([]contracts.AgentPackageVersion, error) {
	rows, err := s.db.QueryContext(ctx, packageReleaseSelectSQL()+" ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.AgentPackageVersion, 0)
	for rows.Next() {
		release, err := scanPackageRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, release)
	}
	return out, rows.Err()
}

func packageReleaseSelectSQL() string {
	return `SELECT package_version_id, tenant_id, agent_id, version, status, source_hash, compiled_hash,
source_kind, source_provider_id, manifest_version, manifest_hash, carrier_kind, runtime_contract, conformance_status,
compiled_json, created_by, created_at, published_at, canary_percent, canary_scope_json FROM agent_package_versions`
}

func scanPackageRelease(row interface {
	Scan(dest ...any) error
}) (contracts.AgentPackageVersion, error) {
	var packageID, tenantID, agentID, version, status string
	var published sql.NullTime
	var sourceKind, carrierKind, runtimeContract, conformanceStatus string
	var sourceProviderID, manifestVersion, manifestHash sql.NullString
	var compiledJSON, scopeJSON []byte
	release := contracts.AgentPackageVersion{}
	if err := row.Scan(&packageID, &tenantID, &agentID, &version, &status, &release.SourceHash, &release.CompiledHash,
		&sourceKind, &sourceProviderID, &manifestVersion, &manifestHash, &carrierKind, &runtimeContract, &conformanceStatus,
		&compiledJSON,
		&release.CreatedBy, &release.CreatedAt, &published, &release.CanaryPercent, &scopeJSON); err != nil {
		return contracts.AgentPackageVersion{}, mapSQLError(err)
	}
	release.SourceKind = contracts.AgentSourceKind(sourceKind)
	release.SourceProviderID = sourceProviderID.String
	release.ManifestVersion = manifestVersion.String
	release.ManifestHash = manifestHash.String
	release.CarrierKind = contracts.AgentCarrierKind(carrierKind)
	release.RuntimeContract = contracts.RuntimeContractKind(runtimeContract)
	release.ConformanceStatus = contracts.RuntimeConformanceStatus(conformanceStatus)
	_ = scanJSON(scopeJSON, &release.CanaryScope)
	var compiled contracts.AgentDefinition
	if err := scanJSON(compiledJSON, &compiled); err == nil {
		if release.SourceKind == "" {
			release.SourceKind = compiled.SourceKind
		}
		if release.SourceProviderID == "" {
			release.SourceProviderID = compiled.SourceProviderID
		}
		if release.ManifestVersion == "" {
			release.ManifestVersion = compiled.ManifestVersion
		}
		if release.ManifestHash == "" {
			release.ManifestHash = compiled.ManifestHash
		}
		if release.CarrierKind == "" {
			release.CarrierKind = compiled.CarrierKind
		}
		if release.RuntimeContract == "" {
			release.RuntimeContract = compiled.RuntimeContract
		}
		if release.ConformanceStatus == "" {
			release.ConformanceStatus = compiled.ConformanceStatus
		}
		if strategyHash, err := hash.StableJSON(compiled.Strategies); err == nil {
			release.StrategyHash = strategyHash
		}
	}
	release.SourceKind = contracts.NormalizeSourceKind(release.SourceKind)
	release.CarrierKind = contracts.NormalizeCarrierKind(release.SourceKind, release.CarrierKind)
	release.RuntimeContract = contracts.NormalizeRuntimeContract(release.CarrierKind, release.RuntimeContract)
	if release.ConformanceStatus == "" {
		release.ConformanceStatus = contracts.RuntimeConformanceUnknown
	}
	release.PackageVersionID = contracts.PackageVersionID(packageID)
	release.TenantID = contracts.TenantID(tenantID)
	release.AgentID = contracts.AgentID(agentID)
	release.Version = contracts.AgentVersion(version)
	release.Status = contracts.ReleaseStatus(status)
	release.CreatedAt = release.CreatedAt.UTC()
	release.PublishedAt = timePtr(published)
	return release, nil
}

func (s *PackageStore) RecordCanaryHit(ctx context.Context, hit contracts.CanaryHit) error {
	if hit.CreatedAt.IsZero() {
		hit.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_package_canary_hits (
  hit_id, tenant_id, agent_id, requested_version, resolved_version,
  package_version_id, run_id, trace_id, caller_id, canary_percent, reason, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (hit_id) DO NOTHING`,
		hit.HitID, hit.TenantID, hit.AgentID, nullString(string(hit.RequestedVersion)), hit.ResolvedVersion,
		nullString(string(hit.PackageVersionID)), nullString(string(hit.RunID)), nullString(string(hit.TraceID)),
		nullString(hit.CallerID), hit.CanaryPercent, nullString(hit.Reason), hit.CreatedAt.UTC())
	return err
}

func (s *PackageStore) ListCanaryHits(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) ([]contracts.CanaryHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT hit_id, tenant_id, agent_id, requested_version, resolved_version,
package_version_id, run_id, trace_id, caller_id, canary_percent, reason, created_at
FROM agent_package_canary_hits
WHERE tenant_id=$1 AND agent_id=$2
ORDER BY created_at DESC, hit_id DESC`, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.CanaryHit, 0)
	for rows.Next() {
		hit, err := scanCanaryHit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func scanCanaryHit(row interface {
	Scan(dest ...any) error
}) (contracts.CanaryHit, error) {
	var hit contracts.CanaryHit
	var hitID, tenantID, agentID, resolvedVersion string
	var requestedVersion, packageVersionID, runID, traceID, callerID, reason sql.NullString
	if err := row.Scan(&hitID, &tenantID, &agentID, &requestedVersion, &resolvedVersion, &packageVersionID, &runID, &traceID, &callerID, &hit.CanaryPercent, &reason, &hit.CreatedAt); err != nil {
		return contracts.CanaryHit{}, err
	}
	hit.HitID = contracts.CanaryHitID(hitID)
	hit.TenantID = contracts.TenantID(tenantID)
	hit.AgentID = contracts.AgentID(agentID)
	if requestedVersion.Valid {
		hit.RequestedVersion = contracts.AgentVersion(requestedVersion.String)
	}
	hit.ResolvedVersion = contracts.AgentVersion(resolvedVersion)
	if packageVersionID.Valid {
		hit.PackageVersionID = contracts.PackageVersionID(packageVersionID.String)
	}
	if runID.Valid {
		hit.RunID = contracts.AgentRunID(runID.String)
	}
	if traceID.Valid {
		hit.TraceID = contracts.TraceID(traceID.String)
	}
	if callerID.Valid {
		hit.CallerID = callerID.String
	}
	if reason.Valid {
		hit.Reason = reason.String
	}
	hit.CreatedAt = hit.CreatedAt.UTC()
	return hit, nil
}

type ExternalTaskBindingStore struct {
	db *sql.DB
}

func (s *ExternalTaskBindingStore) SaveBinding(ctx context.Context, binding contracts.ExternalTaskBinding) (contracts.ExternalTaskBinding, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO external_task_bindings (
  provider, external_task_id, core_task_id, tenant_id, sync_mode, status,
  last_error, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (provider, external_task_id) DO UPDATE SET
  core_task_id=EXCLUDED.core_task_id,
  tenant_id=EXCLUDED.tenant_id,
  sync_mode=EXCLUDED.sync_mode,
  status=EXCLUDED.status,
  last_error=EXCLUDED.last_error,
  updated_at=EXCLUDED.updated_at`,
		binding.Provider, binding.ExternalTaskID, binding.CoreTaskID, binding.TenantID,
		binding.SyncMode, binding.Status, nullString(binding.LastError),
		binding.CreatedAt.UTC(), binding.UpdatedAt.UTC(),
	)
	if err != nil {
		return contracts.ExternalTaskBinding{}, err
	}
	return binding, nil
}

func (s *ExternalTaskBindingStore) GetBinding(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID) (contracts.ExternalTaskBinding, bool, error) {
	binding, err := scanExternalTaskBinding(s.db.QueryRowContext(ctx, `
SELECT provider, external_task_id, core_task_id, tenant_id, sync_mode, status, last_error, created_at, updated_at
FROM external_task_bindings WHERE provider=$1 AND external_task_id=$2`, provider, externalTaskID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ExternalTaskBinding{}, false, nil
	}
	return binding, err == nil, err
}

func (s *ExternalTaskBindingStore) GetBindingByCoreTask(ctx context.Context, tenantID contracts.TenantID, coreTaskID contracts.TaskID) (contracts.ExternalTaskBinding, bool, error) {
	binding, err := scanExternalTaskBinding(s.db.QueryRowContext(ctx, `
SELECT provider, external_task_id, core_task_id, tenant_id, sync_mode, status, last_error, created_at, updated_at
FROM external_task_bindings WHERE tenant_id=$1 AND core_task_id=$2 ORDER BY created_at DESC LIMIT 1`, tenantID, coreTaskID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ExternalTaskBinding{}, false, nil
	}
	return binding, err == nil, err
}

func (s *ExternalTaskBindingStore) UpdateBindingStatus(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID, status string, lastError string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE external_task_bindings SET status=$3, last_error=$4, updated_at=$5
WHERE provider=$1 AND external_task_id=$2`,
		provider, externalTaskID, status, nullString(lastError), updatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (s *ExternalTaskBindingStore) EnqueueDelivery(ctx context.Context, item contracts.ExternalDeliveryOutboxItem) (contracts.ExternalDeliveryOutboxItem, error) {
	payload, err := json.Marshal(item.Payload)
	if err != nil {
		return contracts.ExternalDeliveryOutboxItem{}, err
	}
	if item.AttemptCount < 0 {
		item.AttemptCount = 0
	}
	var outboxID string
	err = s.db.QueryRowContext(ctx, `
INSERT INTO external_delivery_outbox (
  outbox_id, tenant_id, provider, external_task_id, core_task_id, event_type,
  channel, payload_json, idempotency_key, status, attempt_count, last_error,
  next_attempt_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET
  payload_json=EXCLUDED.payload_json,
  status=EXCLUDED.status,
  updated_at=EXCLUDED.updated_at
RETURNING outbox_id`,
		item.OutboxID, item.TenantID, item.Provider, item.ExternalTaskID, nullString(string(item.CoreTaskID)),
		item.EventType, item.Channel, payload, item.IdempotencyKey, item.Status, item.AttemptCount,
		nullString(item.LastError), nullableTime(item.NextAttemptAt), item.CreatedAt.UTC(), item.UpdatedAt.UTC(),
	).Scan(&outboxID)
	if err != nil {
		return contracts.ExternalDeliveryOutboxItem{}, err
	}
	item.OutboxID = outboxID
	return item, nil
}

func (s *ExternalTaskBindingStore) MarkDeliveryAttempt(ctx context.Context, outboxID string, status string, lastError string, nextAttemptAt time.Time, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE external_delivery_outbox
SET status=$2,
    attempt_count=attempt_count+1,
    last_error=$3,
    next_attempt_at=$4,
    updated_at=$5
WHERE outbox_id=$1`,
		outboxID, status, nullString(lastError), nullableTime(nextAttemptAt), updatedAt.UTC(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return storagerepo.ErrNotFound
	}
	return nil
}

func (s *ExternalTaskBindingStore) GetDelivery(ctx context.Context, tenantID contracts.TenantID, outboxID string) (contracts.ExternalDeliveryOutboxItem, bool, error) {
	item, err := scanExternalDelivery(s.db.QueryRowContext(ctx, externalDeliverySelectSQL()+" WHERE tenant_id=$1 AND outbox_id=$2", tenantID, outboxID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.ExternalDeliveryOutboxItem{}, false, nil
	}
	return item, err == nil, err
}

func (s *ExternalTaskBindingStore) ListDeliveriesDue(ctx context.Context, tenantID contracts.TenantID, statuses []string, limit int, now time.Time) ([]contracts.ExternalDeliveryOutboxItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if len(statuses) == 0 {
		statuses = []string{"failed", "pending"}
	}
	statusPlaceholders := make([]string, 0, len(statuses))
	args := []any{tenantID}
	for _, status := range statuses {
		args = append(args, status)
		statusPlaceholders = append(statusPlaceholders, fmt.Sprintf("$%d", len(args)))
	}
	args = append(args, now.UTC(), limit)
	rows, err := s.db.QueryContext(ctx, externalDeliverySelectSQL()+fmt.Sprintf(`
WHERE tenant_id=$1
  AND status IN (%s)
  AND (next_attempt_at IS NULL OR next_attempt_at <= $%d)
ORDER BY created_at ASC
LIMIT $%d`, strings.Join(statusPlaceholders, ","), len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.ExternalDeliveryOutboxItem, 0)
	for rows.Next() {
		item, err := scanExternalDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func externalDeliverySelectSQL() string {
	return `SELECT outbox_id, tenant_id, provider, external_task_id, core_task_id,
event_type, channel, payload_json, idempotency_key, status, attempt_count,
last_error, next_attempt_at, created_at, updated_at FROM external_delivery_outbox`
}

func scanExternalDelivery(row interface {
	Scan(dest ...any) error
}) (contracts.ExternalDeliveryOutboxItem, error) {
	var externalTaskID, coreTaskID, lastError sql.NullString
	var payloadJSON []byte
	var nextAttemptAt sql.NullTime
	item := contracts.ExternalDeliveryOutboxItem{}
	err := row.Scan(&item.OutboxID, &item.TenantID, &item.Provider, &externalTaskID, &coreTaskID,
		&item.EventType, &item.Channel, &payloadJSON, &item.IdempotencyKey, &item.Status,
		&item.AttemptCount, &lastError, &nextAttemptAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return contracts.ExternalDeliveryOutboxItem{}, mapSQLError(err)
	}
	if err := json.Unmarshal(payloadJSON, &item.Payload); err != nil {
		return contracts.ExternalDeliveryOutboxItem{}, err
	}
	item.ExternalTaskID = contracts.ExternalTaskID(externalTaskID.String)
	item.CoreTaskID = contracts.TaskID(coreTaskID.String)
	item.LastError = lastError.String
	if nextAttemptAt.Valid {
		item.NextAttemptAt = nextAttemptAt.Time.UTC()
	}
	item.CreatedAt = item.CreatedAt.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}

func nullableTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func scanExternalTaskBinding(row interface {
	Scan(dest ...any) error
}) (contracts.ExternalTaskBinding, error) {
	var externalTaskID, coreTaskID, tenantID, lastError sql.NullString
	binding := contracts.ExternalTaskBinding{}
	err := row.Scan(&binding.Provider, &externalTaskID, &coreTaskID, &tenantID, &binding.SyncMode,
		&binding.Status, &lastError, &binding.CreatedAt, &binding.UpdatedAt)
	if err != nil {
		return contracts.ExternalTaskBinding{}, mapSQLError(err)
	}
	binding.ExternalTaskID = contracts.ExternalTaskID(externalTaskID.String)
	binding.CoreTaskID = contracts.TaskID(coreTaskID.String)
	binding.TenantID = contracts.TenantID(tenantID.String)
	binding.LastError = lastError.String
	binding.CreatedAt = binding.CreatedAt.UTC()
	binding.UpdatedAt = binding.UpdatedAt.UTC()
	return binding, nil
}
