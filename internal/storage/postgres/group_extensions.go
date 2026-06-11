package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type GroupMemberStore struct{ db *sql.DB }

func (s *GroupMemberStore) SaveMember(ctx context.Context, profile contracts.GroupMemberProfile) error {
	aliases, err := jsonValue(profile.Aliases)
	if err != nil {
		return err
	}
	roles, err := jsonValue(profile.Roles)
	if err != nil {
		return err
	}
	refs, err := jsonValue(profile.PermissionRefs)
	if err != nil {
		return err
	}
	metadata, err := jsonValue(profile.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO group_members (
  tenant_id, group_id, member_id, external_user_id, display_name, aliases_json,
  member_type, roles_json, permission_refs_json, status, metadata_json, last_seen_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, group_id, member_id) DO UPDATE SET
  external_user_id=EXCLUDED.external_user_id,
  display_name=EXCLUDED.display_name,
  aliases_json=EXCLUDED.aliases_json,
  member_type=EXCLUDED.member_type,
  roles_json=EXCLUDED.roles_json,
  permission_refs_json=EXCLUDED.permission_refs_json,
  status=EXCLUDED.status,
  metadata_json=EXCLUDED.metadata_json,
  last_seen_at=EXCLUDED.last_seen_at`,
		profile.TenantID, profile.GroupID, profile.MemberID, nullString(profile.ExternalUserID),
		nullString(profile.DisplayName), aliases, profile.MemberType, roles, refs, profile.Status,
		metadata, nullTimePtr(profile.LastSeenAt),
	)
	return err
}

func (s *GroupMemberStore) ResolveMember(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, externalUserID string) (contracts.GroupMemberProfile, bool, error) {
	member, err := scanGroupMember(s.db.QueryRowContext(ctx, groupMemberSelectSQL()+`
WHERE tenant_id=$1 AND group_id=$2 AND external_user_id=$3`, tenantID, groupID, externalUserID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.GroupMemberProfile{}, false, nil
	}
	return member, err == nil, err
}

func (s *GroupMemberStore) ListGroupMembers(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupMemberProfile, error) {
	rows, err := s.db.QueryContext(ctx, groupMemberSelectSQL()+`
WHERE tenant_id=$1 AND group_id=$2 ORDER BY display_name ASC, member_id ASC`, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.GroupMemberProfile, 0)
	for rows.Next() {
		member, err := scanGroupMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func groupMemberSelectSQL() string {
	return `SELECT tenant_id, group_id, member_id, external_user_id, display_name, aliases_json,
member_type, roles_json, permission_refs_json, status, metadata_json, last_seen_at FROM group_members `
}

func scanGroupMember(row interface{ Scan(dest ...any) error }) (contracts.GroupMemberProfile, error) {
	var tenantID, groupID, memberID, memberType, status string
	var externalUserID, displayName sql.NullString
	var aliasesJSON, rolesJSON, refsJSON, metadataJSON []byte
	var lastSeen sql.NullTime
	if err := row.Scan(&tenantID, &groupID, &memberID, &externalUserID, &displayName, &aliasesJSON,
		&memberType, &rolesJSON, &refsJSON, &status, &metadataJSON, &lastSeen); err != nil {
		return contracts.GroupMemberProfile{}, mapSQLError(err)
	}
	member := contracts.GroupMemberProfile{
		TenantID:       contracts.TenantID(tenantID),
		GroupID:        contracts.GroupID(groupID),
		MemberID:       contracts.GroupMemberID(memberID),
		ExternalUserID: externalUserID.String,
		DisplayName:    displayName.String,
		MemberType:     memberType,
		Status:         status,
		LastSeenAt:     timeValue(lastSeen),
	}
	_ = scanJSON(aliasesJSON, &member.Aliases)
	_ = scanJSON(rolesJSON, &member.Roles)
	_ = scanJSON(refsJSON, &member.PermissionRefs)
	_ = scanJSON(metadataJSON, &member.Metadata)
	return member, nil
}

type GroupPermissionPolicyStore struct{ db *sql.DB }

func (s *GroupPermissionPolicyStore) SavePolicy(ctx context.Context, policy contracts.GroupPermissionPolicy) error {
	actions, err := jsonValue(policy.Actions)
	if err != nil {
		return err
	}
	scopes, err := jsonValue(policy.ResourceScopes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO group_permission_policies (
  tenant_id, group_id, subject_type, subject_id, actions_json, resource_scopes_json,
  requires_approval, reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (tenant_id, group_id, subject_type, subject_id) DO UPDATE SET
  actions_json=EXCLUDED.actions_json,
  resource_scopes_json=EXCLUDED.resource_scopes_json,
  requires_approval=EXCLUDED.requires_approval,
  reason=EXCLUDED.reason,
  updated_at=EXCLUDED.updated_at`,
		policy.TenantID, policy.GroupID, policy.SubjectType, policy.SubjectID, actions, scopes,
		policy.RequiresApproval, nullString(policy.Reason), policy.CreatedAt.UTC(), policy.UpdatedAt.UTC(),
	)
	return err
}

func (s *GroupPermissionPolicyStore) ListPolicies(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupPermissionPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, group_id, subject_type, subject_id, actions_json, resource_scopes_json,
requires_approval, reason, created_at, updated_at
FROM group_permission_policies
WHERE tenant_id=$1 AND (group_id=$2 OR group_id='')
ORDER BY group_id DESC, subject_type ASC, subject_id ASC`, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.GroupPermissionPolicy, 0)
	for rows.Next() {
		policy, err := scanGroupPermissionPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	return out, rows.Err()
}

func scanGroupPermissionPolicy(row interface{ Scan(dest ...any) error }) (contracts.GroupPermissionPolicy, error) {
	var tenantID, groupID, subjectType, subjectID string
	var actionsJSON, scopesJSON []byte
	var reason sql.NullString
	policy := contracts.GroupPermissionPolicy{}
	if err := row.Scan(&tenantID, &groupID, &subjectType, &subjectID, &actionsJSON, &scopesJSON,
		&policy.RequiresApproval, &reason, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
		return contracts.GroupPermissionPolicy{}, mapSQLError(err)
	}
	policy.TenantID = contracts.TenantID(tenantID)
	policy.GroupID = contracts.GroupID(groupID)
	policy.SubjectType = subjectType
	policy.SubjectID = subjectID
	policy.Reason = reason.String
	_ = scanJSON(actionsJSON, &policy.Actions)
	_ = scanJSON(scopesJSON, &policy.ResourceScopes)
	policy.CreatedAt = policy.CreatedAt.UTC()
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, nil
}

type MemoryScopeStore struct{ db *sql.DB }

func (s *MemoryScopeStore) SaveScope(ctx context.Context, scope contracts.MemoryScope) error {
	shared, err := jsonValue(scope.SharedWithGroupIDs)
	if err != nil {
		return err
	}
	readRoles, err := jsonValue(scope.ReadRoles)
	if err != nil {
		return err
	}
	writeRoles, err := jsonValue(scope.WriteRoles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_scopes (
  tenant_id, memory_id, scope_type, scope_id, visibility, owner_group_id,
  shared_with_group_ids_json, read_roles_json, write_roles_json, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (tenant_id, memory_id) DO UPDATE SET
  scope_type=EXCLUDED.scope_type,
  scope_id=EXCLUDED.scope_id,
  visibility=EXCLUDED.visibility,
  owner_group_id=EXCLUDED.owner_group_id,
  shared_with_group_ids_json=EXCLUDED.shared_with_group_ids_json,
  read_roles_json=EXCLUDED.read_roles_json,
  write_roles_json=EXCLUDED.write_roles_json,
  updated_at=EXCLUDED.updated_at`,
		scope.TenantID, scope.MemoryID, scope.ScopeType, scope.ScopeID, scope.Visibility,
		nullString(string(scope.OwnerGroupID)), shared, readRoles, writeRoles,
		scope.CreatedAt.UTC(), scope.UpdatedAt.UTC(),
	)
	return err
}

func (s *MemoryScopeStore) GetScope(ctx context.Context, tenantID contracts.TenantID, memoryID contracts.MemoryID) (contracts.MemoryScope, bool, error) {
	scope, err := scanMemoryScope(s.db.QueryRowContext(ctx, memoryScopeSelectSQL()+`
WHERE tenant_id=$1 AND memory_id=$2`, tenantID, memoryID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.MemoryScope{}, false, nil
	}
	return scope, err == nil, err
}

func memoryScopeSelectSQL() string {
	return `SELECT tenant_id, memory_id, scope_type, scope_id, visibility, owner_group_id,
shared_with_group_ids_json, read_roles_json, write_roles_json, created_at, updated_at FROM memory_scopes `
}

func scanMemoryScope(row interface{ Scan(dest ...any) error }) (contracts.MemoryScope, error) {
	var tenantID, memoryID, scopeType, scopeID, visibility string
	var ownerGroupID sql.NullString
	var sharedJSON, readRolesJSON, writeRolesJSON []byte
	scope := contracts.MemoryScope{}
	if err := row.Scan(&tenantID, &memoryID, &scopeType, &scopeID, &visibility, &ownerGroupID,
		&sharedJSON, &readRolesJSON, &writeRolesJSON, &scope.CreatedAt, &scope.UpdatedAt); err != nil {
		return contracts.MemoryScope{}, mapSQLError(err)
	}
	scope.TenantID = contracts.TenantID(tenantID)
	scope.MemoryID = contracts.MemoryID(memoryID)
	scope.ScopeType = scopeType
	scope.ScopeID = scopeID
	scope.Visibility = visibility
	scope.OwnerGroupID = contracts.GroupID(ownerGroupID.String)
	_ = scanJSON(sharedJSON, &scope.SharedWithGroupIDs)
	_ = scanJSON(readRolesJSON, &scope.ReadRoles)
	_ = scanJSON(writeRolesJSON, &scope.WriteRoles)
	scope.CreatedAt = scope.CreatedAt.UTC()
	scope.UpdatedAt = scope.UpdatedAt.UTC()
	return scope, nil
}

type KnowledgeStore struct{ db *sql.DB }

func (s *KnowledgeStore) SaveKnowledgeBase(ctx context.Context, base contracts.KnowledgeBase) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO knowledge_bases (
  knowledge_base_id, tenant_id, name, owner_group_id, visibility, source_type,
  index_type, search_mode, status, created_by, document_count, last_indexed_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (knowledge_base_id) DO UPDATE SET
  name=EXCLUDED.name,
  owner_group_id=EXCLUDED.owner_group_id,
  visibility=EXCLUDED.visibility,
  source_type=EXCLUDED.source_type,
  index_type=EXCLUDED.index_type,
  search_mode=EXCLUDED.search_mode,
  status=EXCLUDED.status,
  created_by=EXCLUDED.created_by,
  document_count=EXCLUDED.document_count,
  last_indexed_at=EXCLUDED.last_indexed_at,
  updated_at=EXCLUDED.updated_at`,
		base.KnowledgeBaseID, base.TenantID, base.Name, nullString(string(base.OwnerGroupID)),
		base.Visibility, base.SourceType, base.IndexType, firstNonEmpty(base.SearchMode, contracts.KnowledgeSearchBM25),
		base.Status, nullString(base.CreatedBy), base.DocumentCount, nullTime(base.LastIndexedAt),
		base.CreatedAt.UTC(), base.UpdatedAt.UTC(),
	)
	return err
}

func (s *KnowledgeStore) GetKnowledgeBase(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) (contracts.KnowledgeBase, bool, error) {
	base, err := scanKnowledgeBase(s.db.QueryRowContext(ctx, knowledgeBaseSelectSQL()+`
WHERE tenant_id=$1 AND knowledge_base_id=$2`, tenantID, knowledgeBaseID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.KnowledgeBase{}, false, nil
	}
	return base, err == nil, err
}

func (s *KnowledgeStore) ListKnowledgeBases(ctx context.Context, tenantID contracts.TenantID) ([]contracts.KnowledgeBase, error) {
	rows, err := s.db.QueryContext(ctx, knowledgeBaseSelectSQL()+`WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.KnowledgeBase, 0)
	for rows.Next() {
		base, err := scanKnowledgeBase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, base)
	}
	return out, rows.Err()
}

func knowledgeBaseSelectSQL() string {
	return `SELECT knowledge_base_id, tenant_id, name, owner_group_id, visibility, source_type,
index_type, search_mode, status, created_by, document_count, last_indexed_at, created_at, updated_at FROM knowledge_bases `
}

func scanKnowledgeBase(row interface{ Scan(dest ...any) error }) (contracts.KnowledgeBase, error) {
	var id, tenantID, ownerGroupID, createdBy sql.NullString
	var lastIndexedAt sql.NullTime
	base := contracts.KnowledgeBase{}
	if err := row.Scan(&id, &tenantID, &base.Name, &ownerGroupID, &base.Visibility, &base.SourceType,
		&base.IndexType, &base.SearchMode, &base.Status, &createdBy, &base.DocumentCount, &lastIndexedAt, &base.CreatedAt, &base.UpdatedAt); err != nil {
		return contracts.KnowledgeBase{}, mapSQLError(err)
	}
	base.KnowledgeBaseID = contracts.KnowledgeBaseID(id.String)
	base.TenantID = contracts.TenantID(tenantID.String)
	base.OwnerGroupID = contracts.GroupID(ownerGroupID.String)
	base.CreatedBy = createdBy.String
	base.LastIndexedAt = timePtr(lastIndexedAt)
	base.CreatedAt = base.CreatedAt.UTC()
	base.UpdatedAt = base.UpdatedAt.UTC()
	return base, nil
}

func (s *KnowledgeStore) SaveDocument(ctx context.Context, doc contracts.KnowledgeDocument) error {
	metadata, err := jsonValue(doc.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO knowledge_documents (
  document_id, knowledge_base_id, tenant_id, source_group_id, title, content,
  source_uri, visibility, index_status, indexed_at, metadata_json, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (document_id) DO UPDATE SET
  title=EXCLUDED.title,
  content=EXCLUDED.content,
  source_uri=EXCLUDED.source_uri,
  visibility=EXCLUDED.visibility,
  index_status=EXCLUDED.index_status,
  indexed_at=EXCLUDED.indexed_at,
  metadata_json=EXCLUDED.metadata_json`,
		doc.DocumentID, doc.KnowledgeBaseID, doc.TenantID, nullString(string(doc.SourceGroupID)),
		doc.Title, doc.Content, nullString(doc.SourceURI), nullString(doc.Visibility),
		firstNonEmpty(doc.IndexStatus, contracts.KnowledgeDocumentIndexReady), nullTime(doc.IndexedAt), metadata, doc.CreatedAt.UTC(),
	)
	return err
}

func (s *KnowledgeStore) ListDocuments(ctx context.Context, tenantID contracts.TenantID) ([]contracts.KnowledgeDocument, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT document_id, knowledge_base_id, tenant_id, source_group_id, title, content,
source_uri, visibility, index_status, indexed_at, metadata_json, created_at
FROM knowledge_documents WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.KnowledgeDocument, 0)
	for rows.Next() {
		doc, err := scanKnowledgeDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func scanKnowledgeDocument(row interface{ Scan(dest ...any) error }) (contracts.KnowledgeDocument, error) {
	var id, baseID, tenantID string
	var sourceGroupID, sourceURI, visibility, indexStatus sql.NullString
	var indexedAt sql.NullTime
	var metadataJSON []byte
	doc := contracts.KnowledgeDocument{}
	if err := row.Scan(&id, &baseID, &tenantID, &sourceGroupID, &doc.Title, &doc.Content,
		&sourceURI, &visibility, &indexStatus, &indexedAt, &metadataJSON, &doc.CreatedAt); err != nil {
		return contracts.KnowledgeDocument{}, mapSQLError(err)
	}
	doc.DocumentID = contracts.KnowledgeDocumentID(id)
	doc.KnowledgeBaseID = contracts.KnowledgeBaseID(baseID)
	doc.TenantID = contracts.TenantID(tenantID)
	doc.SourceGroupID = contracts.GroupID(sourceGroupID.String)
	doc.SourceURI = sourceURI.String
	doc.Visibility = visibility.String
	doc.IndexStatus = indexStatus.String
	doc.IndexedAt = timePtr(indexedAt)
	_ = scanJSON(metadataJSON, &doc.Metadata)
	doc.CreatedAt = doc.CreatedAt.UTC()
	return doc, nil
}

func (s *KnowledgeStore) SaveIngestionJob(ctx context.Context, job contracts.KnowledgeIngestionJob) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO knowledge_ingestion_jobs (
  job_id, tenant_id, knowledge_base_id, document_id, source_group_id, status,
  index_type, search_mode, error, created_by, created_at, updated_at, completed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (job_id) DO UPDATE SET
  status=EXCLUDED.status,
  document_id=EXCLUDED.document_id,
  source_group_id=EXCLUDED.source_group_id,
  index_type=EXCLUDED.index_type,
  search_mode=EXCLUDED.search_mode,
  error=EXCLUDED.error,
  updated_at=EXCLUDED.updated_at,
  completed_at=EXCLUDED.completed_at`,
		job.JobID, job.TenantID, job.KnowledgeBaseID, nullString(string(job.DocumentID)), nullString(string(job.SourceGroupID)),
		job.Status, nullString(job.IndexType), nullString(job.SearchMode), nullString(job.Error), nullString(job.CreatedBy),
		job.CreatedAt.UTC(), job.UpdatedAt.UTC(), nullTime(job.CompletedAt),
	)
	return err
}

func (s *KnowledgeStore) GetIngestionJob(ctx context.Context, tenantID contracts.TenantID, jobID contracts.KnowledgeIngestionJobID) (contracts.KnowledgeIngestionJob, bool, error) {
	job, err := scanKnowledgeIngestionJob(s.db.QueryRowContext(ctx, knowledgeIngestionJobSelectSQL()+`
WHERE tenant_id=$1 AND job_id=$2`, tenantID, jobID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.KnowledgeIngestionJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *KnowledgeStore) ListIngestionJobs(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) ([]contracts.KnowledgeIngestionJob, error) {
	query := knowledgeIngestionJobSelectSQL() + `WHERE tenant_id=$1`
	args := []any{tenantID}
	if knowledgeBaseID != "" {
		query += ` AND knowledge_base_id=$2`
		args = append(args, knowledgeBaseID)
	}
	query += ` ORDER BY created_at DESC, job_id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.KnowledgeIngestionJob, 0)
	for rows.Next() {
		job, err := scanKnowledgeIngestionJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func knowledgeIngestionJobSelectSQL() string {
	return `SELECT job_id, tenant_id, knowledge_base_id, document_id, source_group_id, status,
index_type, search_mode, error, created_by, created_at, updated_at, completed_at FROM knowledge_ingestion_jobs `
}

func scanKnowledgeIngestionJob(row interface{ Scan(dest ...any) error }) (contracts.KnowledgeIngestionJob, error) {
	var jobID, tenantID, baseID, status string
	var documentID, sourceGroupID, indexType, searchMode, jobError, createdBy sql.NullString
	var completedAt sql.NullTime
	job := contracts.KnowledgeIngestionJob{}
	if err := row.Scan(&jobID, &tenantID, &baseID, &documentID, &sourceGroupID, &status,
		&indexType, &searchMode, &jobError, &createdBy, &job.CreatedAt, &job.UpdatedAt, &completedAt); err != nil {
		return contracts.KnowledgeIngestionJob{}, mapSQLError(err)
	}
	job.JobID = contracts.KnowledgeIngestionJobID(jobID)
	job.TenantID = contracts.TenantID(tenantID)
	job.KnowledgeBaseID = contracts.KnowledgeBaseID(baseID)
	job.DocumentID = contracts.KnowledgeDocumentID(documentID.String)
	job.SourceGroupID = contracts.GroupID(sourceGroupID.String)
	job.Status = status
	job.IndexType = indexType.String
	job.SearchMode = searchMode.String
	job.Error = jobError.String
	job.CreatedBy = createdBy.String
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	job.CompletedAt = timePtr(completedAt)
	return job, nil
}

type CrossGroupSharePolicyStore struct{ db *sql.DB }

func (s *CrossGroupSharePolicyStore) SaveSharePolicy(ctx context.Context, policy contracts.CrossGroupSharePolicy) error {
	baseIDs, err := jsonValue(policy.KnowledgeBaseIDs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO cross_group_share_policies (
  policy_id, tenant_id, source_group_id, target_group_id, knowledge_base_ids_json,
  redaction_policy, requires_approval, status, reason, created_by, approved_by,
  approval_id, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (policy_id) DO UPDATE SET
  source_group_id=EXCLUDED.source_group_id,
  target_group_id=EXCLUDED.target_group_id,
  knowledge_base_ids_json=EXCLUDED.knowledge_base_ids_json,
  redaction_policy=EXCLUDED.redaction_policy,
  requires_approval=EXCLUDED.requires_approval,
  status=EXCLUDED.status,
  reason=EXCLUDED.reason,
  approved_by=EXCLUDED.approved_by,
  approval_id=EXCLUDED.approval_id,
  updated_at=EXCLUDED.updated_at`,
		policy.PolicyID, policy.TenantID, policy.SourceGroupID, policy.TargetGroupID, baseIDs,
		policy.RedactionPolicy, policy.RequiresApproval, policy.Status, nullString(policy.Reason),
		nullString(policy.CreatedBy), nullString(policy.ApprovedBy), nullString(string(policy.ApprovalID)),
		policy.CreatedAt.UTC(), policy.UpdatedAt.UTC(),
	)
	return err
}

func (s *CrossGroupSharePolicyStore) GetSharePolicy(ctx context.Context, tenantID contracts.TenantID, policyID contracts.CrossGroupSharePolicyID) (contracts.CrossGroupSharePolicy, bool, error) {
	policy, err := scanCrossGroupSharePolicy(s.db.QueryRowContext(ctx, crossGroupSharePolicySelectSQL()+`
WHERE tenant_id=$1 AND policy_id=$2`, tenantID, policyID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.CrossGroupSharePolicy{}, false, nil
	}
	return policy, err == nil, err
}

func (s *CrossGroupSharePolicyStore) ListSharePolicies(ctx context.Context, tenantID contracts.TenantID, sourceGroupID contracts.GroupID, targetGroupID contracts.GroupID) ([]contracts.CrossGroupSharePolicy, error) {
	query := crossGroupSharePolicySelectSQL() + `WHERE tenant_id=$1`
	args := []any{tenantID}
	if sourceGroupID != "" {
		args = append(args, sourceGroupID)
		query += fmt.Sprintf(` AND source_group_id=$%d`, len(args))
	}
	if targetGroupID != "" {
		args = append(args, targetGroupID)
		query += fmt.Sprintf(` AND target_group_id=$%d`, len(args))
	}
	query += ` ORDER BY updated_at DESC, policy_id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.CrossGroupSharePolicy, 0)
	for rows.Next() {
		policy, err := scanCrossGroupSharePolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].PolicyID < out[j].PolicyID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, rows.Err()
}

func crossGroupSharePolicySelectSQL() string {
	return `SELECT policy_id, tenant_id, source_group_id, target_group_id, knowledge_base_ids_json,
redaction_policy, requires_approval, status, reason, created_by, approved_by, approval_id, created_at, updated_at FROM cross_group_share_policies `
}

func scanCrossGroupSharePolicy(row interface{ Scan(dest ...any) error }) (contracts.CrossGroupSharePolicy, error) {
	var policyID, tenantID, sourceGroupID, targetGroupID, redactionPolicy, status string
	var baseIDsJSON []byte
	var reason, createdBy, approvedBy, approvalID sql.NullString
	policy := contracts.CrossGroupSharePolicy{}
	if err := row.Scan(&policyID, &tenantID, &sourceGroupID, &targetGroupID, &baseIDsJSON,
		&redactionPolicy, &policy.RequiresApproval, &status, &reason, &createdBy, &approvedBy, &approvalID,
		&policy.CreatedAt, &policy.UpdatedAt); err != nil {
		return contracts.CrossGroupSharePolicy{}, mapSQLError(err)
	}
	policy.PolicyID = contracts.CrossGroupSharePolicyID(policyID)
	policy.TenantID = contracts.TenantID(tenantID)
	policy.SourceGroupID = contracts.GroupID(sourceGroupID)
	policy.TargetGroupID = contracts.GroupID(targetGroupID)
	_ = scanJSON(baseIDsJSON, &policy.KnowledgeBaseIDs)
	policy.RedactionPolicy = redactionPolicy
	policy.Status = status
	policy.Reason = reason.String
	policy.CreatedBy = createdBy.String
	policy.ApprovedBy = approvedBy.String
	policy.ApprovalID = contracts.ApprovalID(approvalID.String)
	policy.CreatedAt = policy.CreatedAt.UTC()
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, nil
}

type GroupTaskBindingStore struct{ db *sql.DB }

func (s *GroupTaskBindingStore) SaveBinding(ctx context.Context, binding contracts.GroupTaskBinding) (contracts.GroupTaskBinding, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO group_task_bindings (
  binding_id, tenant_id, group_id, message_id, task_id, run_id, handoff_id,
  agent_id, objective, created_by, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (binding_id) DO UPDATE SET
  message_id=EXCLUDED.message_id,
  run_id=EXCLUDED.run_id,
  handoff_id=EXCLUDED.handoff_id,
  agent_id=EXCLUDED.agent_id,
  objective=EXCLUDED.objective`,
		binding.BindingID, binding.TenantID, binding.GroupID, nullString(binding.MessageID), binding.TaskID,
		nullString(string(binding.RunID)), nullString(string(binding.HandoffID)), binding.AgentID,
		binding.Objective, binding.CreatedBy, binding.CreatedAt.UTC(),
	)
	return binding, err
}

func (s *GroupTaskBindingStore) FindRecentByGroup(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, limit int) ([]contracts.GroupTaskBinding, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, groupTaskBindingSelectSQL()+`
WHERE tenant_id=$1 AND group_id=$2 ORDER BY created_at DESC LIMIT $3`, tenantID, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGroupTaskBindings(rows)
}

func (s *GroupTaskBindingStore) FindByTask(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (contracts.GroupTaskBinding, bool, error) {
	binding, err := scanGroupTaskBinding(s.db.QueryRowContext(ctx, groupTaskBindingSelectSQL()+`
WHERE tenant_id=$1 AND task_id=$2 ORDER BY created_at DESC LIMIT 1`, tenantID, taskID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.GroupTaskBinding{}, false, nil
	}
	return binding, err == nil, err
}

func groupTaskBindingSelectSQL() string {
	return `SELECT binding_id, tenant_id, group_id, message_id, task_id, run_id, handoff_id,
agent_id, objective, created_by, created_at FROM group_task_bindings `
}

func scanGroupTaskBindings(rows *sql.Rows) ([]contracts.GroupTaskBinding, error) {
	out := make([]contracts.GroupTaskBinding, 0)
	for rows.Next() {
		binding, err := scanGroupTaskBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func scanGroupTaskBinding(row interface{ Scan(dest ...any) error }) (contracts.GroupTaskBinding, error) {
	var bindingID, tenantID, groupID, taskID, agentID string
	var messageID, runID, handoffID sql.NullString
	binding := contracts.GroupTaskBinding{}
	if err := row.Scan(&bindingID, &tenantID, &groupID, &messageID, &taskID, &runID, &handoffID,
		&agentID, &binding.Objective, &binding.CreatedBy, &binding.CreatedAt); err != nil {
		return contracts.GroupTaskBinding{}, mapSQLError(err)
	}
	binding.BindingID = contracts.GroupTaskBindingID(bindingID)
	binding.TenantID = contracts.TenantID(tenantID)
	binding.GroupID = contracts.GroupID(groupID)
	binding.MessageID = messageID.String
	binding.TaskID = contracts.TaskID(taskID)
	binding.RunID = contracts.AgentRunID(runID.String)
	binding.HandoffID = contracts.HandoffID(handoffID.String)
	binding.AgentID = contracts.AgentID(agentID)
	binding.CreatedAt = binding.CreatedAt.UTC()
	return binding, nil
}

type SkillUpdateRequestStore struct{ db *sql.DB }

func (s *SkillUpdateRequestStore) Save(ctx context.Context, request contracts.SkillUpdateRequest) error {
	patch, err := jsonValue(request.ProposedPatch)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO skill_update_requests (
  request_id, tenant_id, agent_id, group_id, requested_by, objective,
  target_skill_id, proposed_patch_json, status, approval_task_id, reason, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (request_id) DO UPDATE SET
  proposed_patch_json=EXCLUDED.proposed_patch_json,
  status=EXCLUDED.status,
  approval_task_id=EXCLUDED.approval_task_id,
  reason=EXCLUDED.reason,
  updated_at=EXCLUDED.updated_at`,
		request.RequestID, request.TenantID, request.AgentID, request.GroupID, request.RequestedBy,
		request.Objective, nullString(request.TargetSkillID), patch, request.Status,
		nullString(string(request.ApprovalTaskID)), nullString(request.Reason), request.CreatedAt.UTC(), request.UpdatedAt.UTC(),
	)
	return err
}

func (s *SkillUpdateRequestStore) Get(ctx context.Context, requestID contracts.SkillUpdateRequestID) (contracts.SkillUpdateRequest, bool, error) {
	request, err := scanSkillUpdateRequest(s.db.QueryRowContext(ctx, `
SELECT request_id, tenant_id, agent_id, group_id, requested_by, objective,
target_skill_id, proposed_patch_json, status, approval_task_id, reason, created_at, updated_at
FROM skill_update_requests WHERE request_id=$1`, requestID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.SkillUpdateRequest{}, false, nil
	}
	return request, err == nil, err
}

func scanSkillUpdateRequest(row interface{ Scan(dest ...any) error }) (contracts.SkillUpdateRequest, error) {
	var id, tenantID, agentID, groupID, targetSkillID, approvalTaskID, reason sql.NullString
	var patchJSON []byte
	request := contracts.SkillUpdateRequest{}
	if err := row.Scan(&id, &tenantID, &agentID, &groupID, &request.RequestedBy, &request.Objective,
		&targetSkillID, &patchJSON, &request.Status, &approvalTaskID, &reason, &request.CreatedAt, &request.UpdatedAt); err != nil {
		return contracts.SkillUpdateRequest{}, mapSQLError(err)
	}
	request.RequestID = contracts.SkillUpdateRequestID(id.String)
	request.TenantID = contracts.TenantID(tenantID.String)
	request.AgentID = contracts.AgentID(agentID.String)
	request.GroupID = contracts.GroupID(groupID.String)
	request.TargetSkillID = targetSkillID.String
	request.ApprovalTaskID = contracts.TaskID(approvalTaskID.String)
	request.Reason = reason.String
	_ = scanJSON(patchJSON, &request.ProposedPatch)
	request.CreatedAt = request.CreatedAt.UTC()
	request.UpdatedAt = request.UpdatedAt.UTC()
	return request, nil
}

type AgentCapabilityStore struct{ db *sql.DB }

func (s *AgentCapabilityStore) Save(ctx context.Context, capability contracts.AgentCapability) error {
	tags, err := jsonValue(capability.Tags)
	if err != nil {
		return err
	}
	when, err := jsonValue(capability.WhenToUse)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_capabilities (
  capability_id, tenant_id, agent_id, version, name, description,
  tags_json, when_to_use_json, risk_level, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (capability_id) DO UPDATE SET
  version=EXCLUDED.version,
  name=EXCLUDED.name,
  description=EXCLUDED.description,
  tags_json=EXCLUDED.tags_json,
  when_to_use_json=EXCLUDED.when_to_use_json,
  risk_level=EXCLUDED.risk_level`,
		capability.CapabilityID, capability.TenantID, capability.AgentID, nullString(string(capability.Version)),
		nullString(capability.Name), nullString(capability.Description), tags, when, nullString(string(capability.RiskLevel)), capability.CreatedAt.UTC(),
	)
	return err
}

func (s *AgentCapabilityStore) ListByTenant(ctx context.Context, tenantID contracts.TenantID) ([]contracts.AgentCapability, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT capability_id, tenant_id, agent_id, version, name, description, tags_json, when_to_use_json, risk_level, created_at
FROM agent_capabilities WHERE tenant_id=$1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contracts.AgentCapability, 0)
	for rows.Next() {
		capability, err := scanAgentCapability(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, capability)
	}
	return out, rows.Err()
}

func scanAgentCapability(row interface{ Scan(dest ...any) error }) (contracts.AgentCapability, error) {
	var id, tenantID, agentID string
	var version, name, description, riskLevel sql.NullString
	var tagsJSON, whenJSON []byte
	capability := contracts.AgentCapability{}
	if err := row.Scan(&id, &tenantID, &agentID, &version, &name, &description, &tagsJSON, &whenJSON, &riskLevel, &capability.CreatedAt); err != nil {
		return contracts.AgentCapability{}, mapSQLError(err)
	}
	capability.CapabilityID = contracts.AgentCapabilityID(id)
	capability.TenantID = contracts.TenantID(tenantID)
	capability.AgentID = contracts.AgentID(agentID)
	capability.Version = contracts.AgentVersion(version.String)
	capability.Name = name.String
	capability.Description = description.String
	capability.RiskLevel = contracts.RiskLevel(riskLevel.String)
	_ = scanJSON(tagsJSON, &capability.Tags)
	_ = scanJSON(whenJSON, &capability.WhenToUse)
	capability.CreatedAt = capability.CreatedAt.UTC()
	return capability, nil
}

type AgentDraftRequestStore struct{ db *sql.DB }

func (s *AgentDraftRequestStore) Save(ctx context.Context, request contracts.AgentDraftRequest) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_draft_requests (
  request_id, tenant_id, group_id, requested_by, agent_id, name,
  objective, status, draft_id, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (request_id) DO UPDATE SET
  status=EXCLUDED.status,
  draft_id=EXCLUDED.draft_id,
  updated_at=EXCLUDED.updated_at`,
		request.RequestID, request.TenantID, nullString(string(request.GroupID)), request.RequestedBy,
		request.AgentID, request.Name, request.Objective, request.Status, nullString(request.DraftID),
		request.CreatedAt.UTC(), request.UpdatedAt.UTC(),
	)
	return err
}

func (s *AgentDraftRequestStore) Get(ctx context.Context, requestID contracts.AgentDraftRequestID) (contracts.AgentDraftRequest, bool, error) {
	request, err := scanAgentDraftRequest(s.db.QueryRowContext(ctx, `
SELECT request_id, tenant_id, group_id, requested_by, agent_id, name, objective, status, draft_id, created_at, updated_at
FROM agent_draft_requests WHERE request_id=$1`, requestID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.AgentDraftRequest{}, false, nil
	}
	return request, err == nil, err
}

func scanAgentDraftRequest(row interface{ Scan(dest ...any) error }) (contracts.AgentDraftRequest, error) {
	var id, tenantID, groupID, agentID, draftID sql.NullString
	request := contracts.AgentDraftRequest{}
	if err := row.Scan(&id, &tenantID, &groupID, &request.RequestedBy, &agentID, &request.Name,
		&request.Objective, &request.Status, &draftID, &request.CreatedAt, &request.UpdatedAt); err != nil {
		return contracts.AgentDraftRequest{}, mapSQLError(err)
	}
	request.RequestID = contracts.AgentDraftRequestID(id.String)
	request.TenantID = contracts.TenantID(tenantID.String)
	request.GroupID = contracts.GroupID(groupID.String)
	request.AgentID = contracts.AgentID(agentID.String)
	request.DraftID = draftID.String
	request.CreatedAt = request.CreatedAt.UTC()
	request.UpdatedAt = request.UpdatedAt.UTC()
	return request, nil
}

type TonePolicyStore struct{ db *sql.DB }

func (s *TonePolicyStore) SavePolicy(ctx context.Context, policy contracts.TonePolicy) error {
	rules, err := jsonValue(policy.Rules)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tone_policies (tenant_id, group_id, default_style, group_style, rules_json)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id, group_id) DO UPDATE SET
  default_style=EXCLUDED.default_style,
  group_style=EXCLUDED.group_style,
  rules_json=EXCLUDED.rules_json`,
		policy.TenantID, policy.GroupID, policy.DefaultStyle, nullString(policy.GroupStyle), rules,
	)
	return err
}

func (s *TonePolicyStore) GetPolicy(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) (contracts.TonePolicy, bool, error) {
	policy, err := scanTonePolicy(s.db.QueryRowContext(ctx, `
SELECT tenant_id, group_id, default_style, group_style, rules_json
FROM tone_policies WHERE tenant_id=$1 AND group_id=$2`, tenantID, groupID))
	if errors.Is(err, storagerepo.ErrNotFound) {
		return contracts.TonePolicy{}, false, nil
	}
	return policy, err == nil, err
}

func scanTonePolicy(row interface{ Scan(dest ...any) error }) (contracts.TonePolicy, error) {
	var tenantID, groupID string
	var groupStyle sql.NullString
	var rulesJSON []byte
	policy := contracts.TonePolicy{}
	if err := row.Scan(&tenantID, &groupID, &policy.DefaultStyle, &groupStyle, &rulesJSON); err != nil {
		return contracts.TonePolicy{}, mapSQLError(err)
	}
	policy.TenantID = contracts.TenantID(tenantID)
	policy.GroupID = contracts.GroupID(groupID)
	policy.GroupStyle = groupStyle.String
	_ = scanJSON(rulesJSON, &policy.Rules)
	return policy, nil
}

func nullTimePtr(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func timeValue(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("no rows affected")
	}
	return nil
}
