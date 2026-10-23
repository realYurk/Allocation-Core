-- DESIGN TEMPLATE ONLY: not executed against a real PostgreSQL server here.
-- Load each full run using COPY into a per-run staging table, validate, then
-- insert immutable new results and atomically move active pointer.
CREATE TABLE IF NOT EXISTS allocation_run (
  run_id TEXT PRIMARY KEY,
  scenario TEXT NOT NULL,
  business_version TEXT NOT NULL,
  source_sha256 TEXT NOT NULL,
  rule_sha256 TEXT NOT NULL,
  expected_amount NUMERIC(38,6) NOT NULL,
  expected_rows BIGINT NOT NULL,
  status TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS allocation_result (
  run_id TEXT NOT NULL REFERENCES allocation_run(run_id),
  terminal_stage TEXT NOT NULL,
  row_id TEXT NOT NULL,
  amount NUMERIC(38,6) NOT NULL,
  dimensions_json JSONB NOT NULL,
  PRIMARY KEY (run_id, terminal_stage, row_id)
);
CREATE TABLE IF NOT EXISTS allocation_active (
  scenario TEXT NOT NULL,
  business_version TEXT NOT NULL,
  active_run_id TEXT NOT NULL REFERENCES allocation_run(run_id),
  PRIMARY KEY (scenario, business_version)
);
-- Implement a server-side `publish_run` transaction after staging COPY:
-- 1) check expected_rows, expected_amount, duplicates, precision, foreign keys
-- 2) INSERT into allocation_result for a NEW run_id
-- 3) INSERT ... ON CONFLICT DO UPDATE allocation_active
-- 4) COMMIT or ROLLBACK atomically. Do not delete old active rows first.
