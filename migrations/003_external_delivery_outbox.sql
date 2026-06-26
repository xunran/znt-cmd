CREATE TABLE external_delivery_outbox (
  outbox_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  external_task_id TEXT NOT NULL,
  core_task_id TEXT,
  event_type TEXT NOT NULL,
  channel TEXT NOT NULL,
  payload_json JSONB NOT NULL,
  idempotency_key TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  next_attempt_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_external_delivery_outbox_status ON external_delivery_outbox (tenant_id, status, next_attempt_at, created_at);
CREATE INDEX idx_external_delivery_outbox_task ON external_delivery_outbox (tenant_id, provider, external_task_id, created_at);
