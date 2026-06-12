ALTER TABLE tool_providers
  ADD COLUMN IF NOT EXISTS secret_ref TEXT;
