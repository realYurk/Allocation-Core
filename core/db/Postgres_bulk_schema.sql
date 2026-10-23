-- Allocation Core V20 bulk import reference. Run this with appropriate DBA approval.
-- The CSV is produced by: alloc vault export-db VAULT RUN_ID bulk.csv
-- IMPORTANT: staging data must be validated against the run's reconciliation.
CREATE TABLE IF NOT EXISTS allocation_result (
    run_id TEXT NOT NULL,
    row_id TEXT NOT NULL,
    terminal_stage TEXT NOT NULL,
    amount NUMERIC(38,6) NOT NULL,
    dimensions_json JSONB NOT NULL,
    PRIMARY KEY (run_id, terminal_stage, row_id)
);
CREATE UNLOGGED TABLE IF NOT EXISTS allocation_result_stage (
    run_id TEXT,
    row_id TEXT,
    terminal_stage TEXT,
    amount TEXT,
    dimensions_json TEXT
);
-- Use a client bulk load such as psql \copy, not 12 million row-by-row INSERTs.
-- Example: \copy allocation_result_stage(run_id,row_id,terminal_stage,amount,dimensions_json) FROM 'C:/allocation/bulk.csv' WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
-- Verify BOM behavior, row counts, unique row IDs, and reconciled totals.
-- Then INSERT ... SELECT with cast inside a transaction. See documentation.
