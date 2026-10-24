# PostgreSQL / SQL Server 批量入库与全量版本发布

## 定位（先看）

业务系统负责从已有 SQL 取出**完整费用金额、完整 Driver 和规则文件**，让 Allocation Core 每次从零开始全量计算。内核不自行连接企业数据库，也不接管企业数据库的账号、安全策略、审计日志。`integration/python/sql_bulk_publish.py` 是接入方可复用的**可选适配器**，不是金额算法的一部分。

## 三种数据库集成模式

| 方法 | 适合什么情况 | 注意事项 |
|---|---|---|
| Java/Spring Boot `JdbcTemplate` + JDBC `COPY`/BulkCopy | 已有 Spring Boot 的公司 | 更容易统一已有数据库连接池、凭据、监控与事务 |
| Python `psycopg` 的 COPY（已提供实现） | PostgreSQL | `psycopg[binary]` 或 psycopg3，数据库须先执行建表脚本 |
| Python `pyodbc.fast_executemany`（已提供实现） | SQL Server | 需 ODBC Driver、部署环境网络可达；批量写入仍需目标库实测 |

**不建议千万条逐条 INSERT。** PostgreSQL 的接入使用 `COPY ... FROM STDIN`，SQL Server 使用带 `fast_executemany` 的固定批次执行。若生产 Java 项目直接调用 `SqlBulkCopy` 可根据团队现有栈替换，不修改引擎。

## 与规则引擎的数据契约

- 输入：规则 `rule.json`、金额 CSV、各驱动 CSV。全部为输入快照，每次执行从头计算。
- 输出：`run_summary.json`（`PASS`）、`results.csv`（业务明细）、`reconciliation.csv`（所有金额池 `PASS` 且差额为零）、完整压缩证据（需要审计时）。
- 准入：先运行容量预算工具，检查估算文件空间和任务行数。
- 数据库导入：写入一个新的 `run_id`，不覆盖历史 `run_id`。
- 发布：数据行数 + 精确金额和摘要相符之后，在一个数据库事务内更新 `(scenario,business_version) → active_run_id`。
- 金额使用 `Decimal` 和 `NUMERIC(38,6)` / `DECIMAL(38,6)`；禁止用浮点中转。
- 对于 7 个维度这类简单场景，直接流式读取 `results.csv`。不必先生成更大的 `export-db dimensions_json` 中间 CSV；代码在内存里组装每行 JSON 并直接批量写入。

## 必须提前部署表

- PostgreSQL：`sql/postgres_template.sql`。此脚本是 schema 模板，请 DBA 在测试库检查索引策略和约束。
- SQL Server：`sql/sqlserver_template.sql`。目标项目中需在表上增加 `run_id` 定位查询用的索引并根据吞吐调优。

PostgreSQL 示例（开发环境内）：

```bash
export ALLOC_DB_DSN='host=127.0.0.1 dbname=allocation user=app password=...'
python -m pip install psycopg[binary]
python integration/python/sql_bulk_publish.py --db postgres \
 --output-dir ./runs/full_run_001 --run-id RUN_001 --scenario FC_10_2 --business-version WIP_01
```

SQL Server 示例：

```powershell
$env:ALLOC_DB_DSN = "DRIVER={ODBC Driver 18 for SQL Server};SERVER=localhost;DATABASE=allocation;UID=...;PWD=...;Encrypt=yes;TrustServerCertificate=no"
python -m pip install pyodbc
python integration/python/sql_bulk_publish.py --db sqlserver --output-dir .\runs\full_run_001 --run-id RUN_001 --scenario FC_10_2 --business-version WIP_01
```

测试时可先用 `--check-only`（不连接数据库），只校验分摊结果与完整对账文件。

**验收边界**：当前环境没有真实的 PostgreSQL / SQL Server 服务、访问凭据与驱动，因此无法提供它们的实测写入时间、锁等待和日志增长数据。`--check-only` 不是批量入库性能测试。供生产使用前必须在目标数据库完成**百万/千万行实测、断连回滚、重复 run_id、并发发布、事务日志与磁盘容量**验收。

## Redis 的结论

**不需要 Redis**。当前单节点 Go 计算服务 + 已有业务系统 + 企业现有数据库即可运转。多节点分布式任务调度是否需要消息中间件，以真实运维场景另行决策。
