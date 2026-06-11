CREATE TABLE agent_package_versions (
  package_version_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  source_hash TEXT NOT NULL,
  compiled_hash TEXT NOT NULL,
  source_json JSONB NOT NULL,
  compiled_json JSONB NOT NULL,
  canary_percent INTEGER NOT NULL DEFAULT 0,
  canary_scope_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  UNIQUE (tenant_id, agent_id, version)
);

CREATE TABLE agent_assets (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  owner_id TEXT,
  status TEXT NOT NULL,
  active_version TEXT,
  default_version TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, agent_id)
);

CREATE INDEX idx_agent_assets_tenant_status ON agent_assets (tenant_id, status);

CREATE TABLE agent_package_canary_hits (
  hit_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  requested_version TEXT,
  resolved_version TEXT NOT NULL,
  package_version_id TEXT,
  run_id TEXT,
  trace_id TEXT,
  caller_id TEXT,
  canary_percent INTEGER NOT NULL DEFAULT 0,
  reason TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_package_canary_hits_tenant_agent ON agent_package_canary_hits (tenant_id, agent_id, created_at DESC);

CREATE TABLE agent_package_drafts (
  draft_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_json JSONB NOT NULL,
  status TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE agent_package_proposals (
  proposal_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  proposal_type TEXT NOT NULL,
  title TEXT,
  reason TEXT,
  patch_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL,
  created_by TEXT NOT NULL,
  reviewed_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_package_proposals_tenant_draft ON agent_package_proposals (tenant_id, draft_id, created_at DESC);

CREATE TABLE agent_prompt_profiles (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  package_version_id TEXT,
  draft_id TEXT,
  status TEXT NOT NULL,
  identity_prompt TEXT,
  system_prompt TEXT,
  developer_prompt TEXT,
  agents_md TEXT,
  source_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, source_kind, source_id)
);

CREATE INDEX idx_agent_prompt_profiles_agent_status ON agent_prompt_profiles (tenant_id, agent_id, status);

CREATE TABLE agent_skill_definitions (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  package_version_id TEXT,
  draft_id TEXT,
  status TEXT NOT NULL,
  skill_id TEXT NOT NULL,
  skill_version TEXT NOT NULL DEFAULT '',
  definition_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, source_kind, source_id, skill_id, skill_version)
);

CREATE INDEX idx_agent_skill_definitions_agent_status ON agent_skill_definitions (tenant_id, agent_id, status);

CREATE TABLE agent_tool_bindings (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  package_version_id TEXT,
  draft_id TEXT,
  status TEXT NOT NULL,
  bindings_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, source_kind, source_id)
);

CREATE INDEX idx_agent_tool_bindings_agent_status ON agent_tool_bindings (tenant_id, agent_id, status);

CREATE TABLE agent_collaborators (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  package_version_id TEXT,
  draft_id TEXT,
  status TEXT NOT NULL,
  collaborator_agent_id TEXT NOT NULL,
  collaborator_version TEXT NOT NULL DEFAULT '',
  collaborator_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, source_kind, source_id, collaborator_agent_id, collaborator_version)
);

CREATE INDEX idx_agent_collaborators_agent_status ON agent_collaborators (tenant_id, agent_id, status);

CREATE TABLE agent_exported_tools (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  package_version_id TEXT,
  draft_id TEXT,
  status TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  tool_version TEXT NOT NULL DEFAULT '',
  tool_json JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, source_kind, source_id, tool_id, tool_version)
);

CREATE INDEX idx_agent_exported_tools_agent_status ON agent_exported_tools (tenant_id, agent_id, status);

CREATE TABLE agent_package_eval_results (
  package_version_id TEXT PRIMARY KEY,
  passed BOOLEAN NOT NULL,
  evaluated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE eval_suites (
  suite_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  name TEXT NOT NULL,
  cases_json JSONB NOT NULL,
  gates_json JSONB NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE eval_suite_results (
  eval_run_id TEXT PRIMARY KEY,
  suite_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  passed BOOLEAN NOT NULL,
  pass_rate DOUBLE PRECISION NOT NULL,
  tool_misuse_rate DOUBLE PRECISION NOT NULL,
  failures_json JSONB NOT NULL,
  results_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_eval_suites_tenant ON eval_suites (tenant_id, created_at);
CREATE INDEX idx_eval_suite_results_tenant_suite ON eval_suite_results (tenant_id, suite_id, created_at);

CREATE TABLE agent_definitions (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT NOT NULL,
  definition_json JSONB NOT NULL,
  package_version_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, agent_id, version)
);

CREATE TABLE policy_sets (
  tenant_id TEXT NOT NULL,
  policy_set_id TEXT NOT NULL,
  version TEXT NOT NULL,
  policy_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, policy_set_id)
);

CREATE TABLE policy_drafts (
  draft_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  policy_set_id TEXT NOT NULL,
  version TEXT NOT NULL,
  policy_json JSONB NOT NULL,
  status TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE policy_versions (
  policy_version_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  policy_set_id TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  policy_hash TEXT NOT NULL,
  policy_json JSONB NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  UNIQUE (tenant_id, policy_set_id, version)
);

CREATE INDEX idx_policy_versions_lookup ON policy_versions (tenant_id, policy_set_id, created_at);

CREATE TABLE tasks (
  task_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT,
  root_task_id TEXT,
  title TEXT NOT NULL,
  objective TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL,
  owner_agent_id TEXT,
  assigned_agent_id TEXT,
  source_handoff_id TEXT,
  agent_id TEXT NOT NULL,
  agent_version TEXT NOT NULL,
  policy_set_id TEXT NOT NULL,
  schema_version TEXT,
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_tasks_tenant_status ON tasks (tenant_id, status);
CREATE INDEX idx_tasks_parent ON tasks (parent_task_id);
CREATE INDEX idx_tasks_root ON tasks (root_task_id);

CREATE TABLE task_events (
  event_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  run_id TEXT,
  step_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_task_events_task_time ON task_events (task_id, created_at);
CREATE INDEX idx_task_events_type ON task_events (tenant_id, type);

CREATE TABLE agent_runs (
  run_id TEXT PRIMARY KEY,
  trace_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  agent_version TEXT NOT NULL,
  task_id TEXT,
  status TEXT NOT NULL,
  step_count INT NOT NULL DEFAULT 0,
  tool_call_count INT NOT NULL DEFAULT 0,
  policy_set_id TEXT NOT NULL,
  version_snapshot JSONB NOT NULL,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  error_code TEXT,
  error_message TEXT
);

CREATE TABLE task_plans (
  plan_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  objective TEXT NOT NULL,
  status TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_task_plans_task_status ON task_plans (task_id, status);

CREATE TABLE plan_steps (
  step_id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  step_index INT NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL,
  expected_tool_hints JSONB,
  status TEXT NOT NULL,
  result_refs JSONB,
  failure_reason TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_plan_steps_plan_index ON plan_steps (plan_id, step_index);

CREATE TABLE plan_events (
  event_id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_plan_events_task_time ON plan_events (task_id, created_at);

CREATE TABLE agent_handoffs (
  handoff_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT NOT NULL,
  child_task_id TEXT,
  from_agent_id TEXT NOT NULL,
  to_agent_id TEXT NOT NULL,
  objective TEXT NOT NULL,
  reason TEXT NOT NULL,
  context_package_ref TEXT NOT NULL,
  artifact_refs JSONB,
  expected_output JSONB,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_handoffs_parent ON agent_handoffs (parent_task_id);

CREATE TABLE governance_process_templates (
  tenant_id TEXT NOT NULL,
  template_id TEXT NOT NULL,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  template_json JSONB NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, template_id)
);

CREATE INDEX idx_governance_process_templates_tenant_status ON governance_process_templates (tenant_id, status);

CREATE TABLE governance_process_runs (
  run_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  template_id TEXT,
  status TEXT NOT NULL,
  subject_type TEXT NOT NULL,
  subject_id TEXT NOT NULL,
  task_id TEXT,
  agent_run_id TEXT,
  trace_id TEXT,
  run_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_governance_process_runs_tenant_subject ON governance_process_runs (tenant_id, subject_type, subject_id, created_at DESC);
CREATE INDEX idx_governance_process_runs_trace ON governance_process_runs (tenant_id, trace_id);

CREATE TABLE governance_gate_runs (
  gate_run_id TEXT PRIMARY KEY,
  process_run_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  gate_id TEXT NOT NULL,
  status TEXT NOT NULL,
  gate_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_governance_gate_runs_process ON governance_gate_runs (tenant_id, process_run_id, created_at);
CREATE UNIQUE INDEX idx_governance_gate_runs_definition ON governance_gate_runs (tenant_id, process_run_id, gate_id);

CREATE TABLE governance_reviews (
  review_id TEXT PRIMARY KEY,
  gate_run_id TEXT NOT NULL,
  process_run_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  reviewer_id TEXT NOT NULL,
  reviewer_type TEXT NOT NULL,
  decision TEXT NOT NULL,
  review_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_governance_reviews_gate ON governance_reviews (tenant_id, gate_run_id, created_at);
CREATE INDEX idx_governance_reviews_process ON governance_reviews (tenant_id, process_run_id, created_at);

CREATE TABLE governance_conflicts (
  conflict_id TEXT PRIMARY KEY,
  gate_run_id TEXT NOT NULL,
  process_run_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  status TEXT NOT NULL,
  conflict_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_governance_conflicts_process ON governance_conflicts (tenant_id, process_run_id, created_at);

CREATE TABLE handoff_context_packages (
  package_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  parent_task_id TEXT NOT NULL,
  source_run_id TEXT NOT NULL,
  from_agent_id TEXT NOT NULL,
  to_agent_id TEXT NOT NULL,
  mode TEXT NOT NULL,
  content_json JSONB NOT NULL,
  hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE tool_providers (
  tenant_id TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  provider_type TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  endpoint TEXT NOT NULL,
  status TEXT NOT NULL,
  health_status TEXT NOT NULL DEFAULT 'unknown',
  last_health_check_at TIMESTAMPTZ,
  last_health_error TEXT,
  auth_ref TEXT,
  timeout_ms INTEGER NOT NULL DEFAULT 0,
  retry_max INTEGER NOT NULL DEFAULT 0,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, provider_id)
);

CREATE INDEX idx_tool_providers_tenant_status ON tool_providers (tenant_id, status);
CREATE INDEX idx_tool_providers_tenant_health ON tool_providers (tenant_id, health_status);

CREATE TABLE tool_groups (
  tenant_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, group_id)
);

CREATE INDEX idx_tool_groups_tenant_status ON tool_groups (tenant_id, status);

CREATE TABLE tool_manifests (
  tenant_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  group_id TEXT,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  when_to_use_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  input_schema_json JSONB NOT NULL,
  output_schema_json JSONB,
  risk_level TEXT NOT NULL,
  visibility TEXT NOT NULL,
  execution_profile TEXT NOT NULL,
  executor_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, tool_id)
);

CREATE INDEX idx_tool_manifests_tenant_group ON tool_manifests (tenant_id, group_id);
CREATE INDEX idx_tool_manifests_tenant_status ON tool_manifests (tenant_id, status);

CREATE TABLE tool_manifest_versions (
  version_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  version TEXT NOT NULL,
  manifest_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_tool_manifest_versions_tool ON tool_manifest_versions (tenant_id, tool_id, created_at);

CREATE TABLE tool_runtime_registry_cache (
  tenant_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  manifest_version TEXT NOT NULL,
  status TEXT NOT NULL,
  cached_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, tool_id)
);

CREATE TABLE runtime_hook_providers (
  tenant_id TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  provider_type TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  endpoint TEXT,
  provider_json JSONB NOT NULL,
  status TEXT NOT NULL,
  health_status TEXT NOT NULL DEFAULT 'unknown',
  last_health_check_at TIMESTAMPTZ,
  last_health_error TEXT,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, provider_id)
);

CREATE INDEX idx_runtime_hook_providers_tenant_status ON runtime_hook_providers (tenant_id, status);
CREATE INDEX idx_runtime_hook_providers_tenant_health ON runtime_hook_providers (tenant_id, health_status);

CREATE TABLE runtime_hook_manifests (
  tenant_id TEXT NOT NULL,
  hook_id TEXT NOT NULL,
  provider_id TEXT,
  phase TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  manifest_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, hook_id)
);

CREATE INDEX idx_runtime_hook_manifests_phase ON runtime_hook_manifests (tenant_id, phase, status);

CREATE TABLE runtime_hook_manifest_versions (
  tenant_id TEXT NOT NULL,
  hook_id TEXT NOT NULL,
  version TEXT NOT NULL,
  provider_id TEXT,
  phase TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  manifest_json JSONB NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, hook_id, version)
);

CREATE INDEX idx_runtime_hook_manifest_versions_hook ON runtime_hook_manifest_versions (tenant_id, hook_id, status);

CREATE TABLE agent_runtime_hook_bindings (
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  agent_version TEXT NOT NULL,
  hook_id TEXT NOT NULL,
  provider_type TEXT NOT NULL,
  provider_id TEXT,
  phase TEXT NOT NULL,
  binding_json JSONB NOT NULL,
  status TEXT NOT NULL,
  version TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, agent_id, agent_version, hook_id, phase)
);

CREATE INDEX idx_agent_runtime_hook_bindings_agent_phase ON agent_runtime_hook_bindings (tenant_id, agent_id, agent_version, phase, status);

CREATE TABLE runtime_hook_events (
  event_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  trace_id TEXT,
  run_id TEXT,
  task_id TEXT,
  agent_id TEXT,
  hook_id TEXT NOT NULL,
  provider_id TEXT,
  provider_type TEXT,
  phase TEXT NOT NULL,
  status TEXT NOT NULL,
  reason TEXT,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  patch_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_runtime_hook_events_run ON runtime_hook_events (tenant_id, run_id, task_id);
CREATE INDEX idx_runtime_hook_events_trace ON runtime_hook_events (tenant_id, trace_id, created_at);
CREATE INDEX idx_runtime_hook_events_provider ON runtime_hook_events (tenant_id, provider_id, hook_id, phase, status);

CREATE TABLE tool_calls (
  tool_call_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  trace_id TEXT,
  run_id TEXT NOT NULL,
  task_id TEXT,
  plan_step_id TEXT,
  tool_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  tool_version TEXT,
  execution_profile TEXT,
  arguments_json JSONB NOT NULL,
  idempotency_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_tool_calls_run ON tool_calls (run_id);
CREATE INDEX idx_tool_calls_tenant_run ON tool_calls (tenant_id, run_id);

CREATE TABLE tool_results (
  tool_result_id TEXT PRIMARY KEY,
  tool_call_id TEXT NOT NULL,
  status TEXT NOT NULL,
  output_json JSONB,
  error_json JSONB,
  artifact_refs JSONB,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tool_call_id)
);

CREATE INDEX idx_tool_results_call ON tool_results (tool_call_id);

CREATE TABLE artifacts (
  artifact_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  storage_uri TEXT NOT NULL,
  mime_type TEXT,
  size_bytes BIGINT NOT NULL,
  hash TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE artifact_contents (
  artifact_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  content TEXT NOT NULL
);

CREATE TABLE memory_events (
  memory_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT,
  user_id TEXT,
  scope TEXT NOT NULL,
  content TEXT NOT NULL,
  summary TEXT,
  source_event_id TEXT,
  visibility TEXT NOT NULL,
  confidence DOUBLE PRECISION NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_memory_events_tenant_agent ON memory_events (tenant_id, agent_id);
CREATE INDEX idx_memory_events_tenant_user ON memory_events (tenant_id, user_id);

CREATE TABLE trace_events (
  id BIGSERIAL PRIMARY KEY,
  trace_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  run_id TEXT,
  task_id TEXT,
  type TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_trace_events_tenant_trace_time ON trace_events (tenant_id, trace_id, created_at);

CREATE TABLE audit_events (
  audit_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  decision TEXT NOT NULL,
  reason TEXT,
  trace_id TEXT,
  task_id TEXT,
  run_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE external_task_bindings (
  provider TEXT NOT NULL,
  external_task_id TEXT NOT NULL,
  core_task_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  sync_mode TEXT NOT NULL,
  status TEXT NOT NULL,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (provider, external_task_id)
);

CREATE INDEX idx_external_task_bindings_core_task ON external_task_bindings (tenant_id, core_task_id);

CREATE TABLE group_members (
  tenant_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  member_id TEXT NOT NULL,
  external_user_id TEXT,
  display_name TEXT,
  aliases_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  member_type TEXT NOT NULL,
  roles_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  permission_refs_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  status TEXT NOT NULL,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  last_seen_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, group_id, member_id)
);

CREATE UNIQUE INDEX idx_group_members_external ON group_members (tenant_id, group_id, external_user_id) WHERE external_user_id IS NOT NULL;
CREATE INDEX idx_group_members_group ON group_members (tenant_id, group_id, display_name);

CREATE TABLE group_permission_policies (
  tenant_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  subject_type TEXT NOT NULL,
  subject_id TEXT NOT NULL,
  actions_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  resource_scopes_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  requires_approval BOOLEAN NOT NULL DEFAULT FALSE,
  reason TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, group_id, subject_type, subject_id)
);

CREATE INDEX idx_group_permission_policies_group ON group_permission_policies (tenant_id, group_id);

CREATE TABLE memory_scopes (
  tenant_id TEXT NOT NULL,
  memory_id TEXT NOT NULL,
  scope_type TEXT NOT NULL,
  scope_id TEXT NOT NULL,
  visibility TEXT NOT NULL,
  owner_group_id TEXT,
  shared_with_group_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  read_roles_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  write_roles_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, memory_id)
);

CREATE INDEX idx_memory_scopes_owner_group ON memory_scopes (tenant_id, owner_group_id);

CREATE TABLE knowledge_bases (
  knowledge_base_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  name TEXT NOT NULL,
  owner_group_id TEXT,
  visibility TEXT NOT NULL,
  source_type TEXT NOT NULL,
  index_type TEXT NOT NULL,
  search_mode TEXT NOT NULL DEFAULT 'bm25',
  status TEXT NOT NULL,
  created_by TEXT,
  document_count INTEGER NOT NULL DEFAULT 0,
  last_indexed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_knowledge_bases_tenant_group ON knowledge_bases (tenant_id, owner_group_id);

CREATE TABLE knowledge_documents (
  document_id TEXT PRIMARY KEY,
  knowledge_base_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  source_group_id TEXT,
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  source_uri TEXT,
  visibility TEXT,
  index_status TEXT NOT NULL DEFAULT 'ready',
  indexed_at TIMESTAMPTZ,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_knowledge_documents_tenant_base ON knowledge_documents (tenant_id, knowledge_base_id);
CREATE INDEX idx_knowledge_documents_source_group ON knowledge_documents (tenant_id, source_group_id);

CREATE TABLE knowledge_ingestion_jobs (
  job_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  knowledge_base_id TEXT NOT NULL,
  document_id TEXT,
  source_group_id TEXT,
  status TEXT NOT NULL,
  index_type TEXT,
  search_mode TEXT,
  error TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_knowledge_ingestion_jobs_base ON knowledge_ingestion_jobs (tenant_id, knowledge_base_id, created_at DESC);

CREATE TABLE cross_group_share_policies (
  policy_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  source_group_id TEXT NOT NULL,
  target_group_id TEXT NOT NULL,
  knowledge_base_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  redaction_policy TEXT NOT NULL,
  requires_approval BOOLEAN NOT NULL DEFAULT FALSE,
  status TEXT NOT NULL,
  reason TEXT,
  created_by TEXT,
  approved_by TEXT,
  approval_id TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_cross_group_share_policies_groups ON cross_group_share_policies (tenant_id, source_group_id, target_group_id, status);

CREATE TABLE group_task_bindings (
  binding_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  message_id TEXT,
  task_id TEXT NOT NULL,
  run_id TEXT,
  handoff_id TEXT,
  agent_id TEXT NOT NULL,
  objective TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_group_task_bindings_group_time ON group_task_bindings (tenant_id, group_id, created_at DESC);
CREATE INDEX idx_group_task_bindings_task ON group_task_bindings (tenant_id, task_id);

CREATE TABLE skill_update_requests (
  request_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  requested_by TEXT NOT NULL,
  objective TEXT NOT NULL,
  target_skill_id TEXT,
  proposed_patch_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL,
  approval_task_id TEXT,
  reason TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_skill_update_requests_group ON skill_update_requests (tenant_id, group_id, created_at DESC);

CREATE TABLE agent_capabilities (
  capability_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  version TEXT,
  name TEXT,
  description TEXT,
  tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  when_to_use_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  risk_level TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_capabilities_tenant_agent ON agent_capabilities (tenant_id, agent_id);

CREATE TABLE agent_draft_requests (
  request_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  group_id TEXT,
  requested_by TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  name TEXT NOT NULL,
  objective TEXT NOT NULL,
  status TEXT NOT NULL,
  draft_id TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_agent_draft_requests_group ON agent_draft_requests (tenant_id, group_id, created_at DESC);

CREATE TABLE tone_policies (
  tenant_id TEXT NOT NULL,
  group_id TEXT NOT NULL DEFAULT '',
  default_style TEXT NOT NULL,
  group_style TEXT,
  rules_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  PRIMARY KEY (tenant_id, group_id)
);
