-- DESIGN TEMPLATE ONLY: version and index sizes depend on target SQL Server.
CREATE TABLE dbo.allocation_run (
  run_id NVARCHAR(100) NOT NULL PRIMARY KEY,
  scenario NVARCHAR(100) NOT NULL,
  business_version NVARCHAR(100) NOT NULL,
  expected_amount DECIMAL(38,6) NOT NULL,
  expected_rows BIGINT NOT NULL,
  status NVARCHAR(20) NOT NULL
);
CREATE TABLE dbo.allocation_result (
  run_id NVARCHAR(100) NOT NULL,
  terminal_stage NVARCHAR(200) NOT NULL,
  row_id NVARCHAR(120) NOT NULL,
  amount DECIMAL(38,6) NOT NULL,
  dimensions_json NVARCHAR(MAX) NOT NULL,
  CONSTRAINT pk_allocation_result PRIMARY KEY(run_id,terminal_stage,row_id)
);
CREATE TABLE dbo.allocation_active (
  scenario NVARCHAR(100) NOT NULL,
  business_version NVARCHAR(100) NOT NULL,
  active_run_id NVARCHAR(100) NOT NULL,
  CONSTRAINT pk_allocation_active PRIMARY KEY(scenario,business_version)
);
-- Bulk load into a staging table via SqlBulkCopy / bcp / BULK INSERT.
-- Then BEGIN TRANSACTION: verify count/amount, insert new run_id,
-- update active version pointer; COMMIT. On ANY mismatch ROLLBACK.
