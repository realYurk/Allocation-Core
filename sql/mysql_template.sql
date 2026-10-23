-- DESIGN TEMPLATE ONLY. Choose types and importer per MySQL version.
CREATE TABLE allocation_run (
 run_id VARCHAR(100) PRIMARY KEY,
 scenario VARCHAR(100) NOT NULL,
 business_version VARCHAR(100) NOT NULL,
 expected_amount DECIMAL(38,6) NOT NULL,
 expected_rows BIGINT NOT NULL,
 status VARCHAR(20) NOT NULL
) CHARACTER SET utf8mb4;
CREATE TABLE allocation_result (
 run_id VARCHAR(100) NOT NULL,
 terminal_stage VARCHAR(200) NOT NULL,
 row_id VARCHAR(120) NOT NULL,
 amount DECIMAL(38,6) NOT NULL,
 dimensions_json JSON NOT NULL,
 PRIMARY KEY(run_id, terminal_stage, row_id)
) CHARACTER SET utf8mb4;
CREATE TABLE allocation_active (
 scenario VARCHAR(100) NOT NULL,
 business_version VARCHAR(100) NOT NULL,
 active_run_id VARCHAR(100) NOT NULL,
 PRIMARY KEY(scenario,business_version)
) CHARACTER SET utf8mb4;
-- Use LOAD DATA [LOCAL] INFILE only with DBA-approved config and paths;
-- validate staging in one transaction before publishing active pointer.
