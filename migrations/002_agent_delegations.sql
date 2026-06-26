CREATE TABLE agent_delegations (
  delegation_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  trace_id TEXT,
  parent_run_id TEXT,
  parent_task_id TEXT,
  source_tool_call_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  operation TEXT,
  provider_agent_id TEXT NOT NULL,
  child_run_id TEXT,
  child_task_id TEXT,
  status TEXT NOT NULL,
  result_status TEXT,
  result_summary TEXT,
  error_summary TEXT,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, source_tool_call_id)
);

CREATE INDEX idx_agent_delegations_parent_run ON agent_delegations (tenant_id, parent_run_id, started_at);
CREATE INDEX idx_agent_delegations_trace ON agent_delegations (tenant_id, trace_id, started_at);
CREATE INDEX idx_agent_delegations_child_run ON agent_delegations (tenant_id, child_run_id);
