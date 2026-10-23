# 接入不同 SQL：怎么读，怎么写，怎么保证不把原系统搞坏

## 先说结论

**Allocation Core V20 不是 ORM，不要求你把企业数据迁到一种数据库里，也不绑定 MySQL/PostgreSQL/SQL Server。**

它的跨数据库契约是：

- **SQL → Core**：企业按自己已有的查询方式取得一份一致性快照，映射成规则要求的金额 CSV、Driver CSV。
- **Core → SQL**：先运行、校验，再将 *最终需要查询的结果* 按批导入新 `run_id`，最后事务中切换生效版本。
- **Core → Vault**：完整 ledger、合并血缘、计算快照独立压缩存储；不要求写进财务 OLTP 数据库。

## 1. 支持哪些 SQL？

| 数据库 | 读取现有表 | 大批量写入推荐 | 这次是否实际验证 |
|---|---|---|---|
| SQLite | `sqlite3` | `executemany` + 单事务（教学/小规模） | **是，完整端到端验证** |
| PostgreSQL | JDBC / psycopg / 导出 SQL | `COPY FROM STDIN` / `psql \copy` | 只提供模板，**未连接实例测试** |
| SQL Server | JDBC / JPA / 原生查询 | .NET `SqlBulkCopy` / `BULK INSERT` / BCP | 只提供模板，未连接实例测试 |
| MySQL / MariaDB | JDBC / Python Connector | `LOAD DATA [LOCAL] INFILE` | 只提供模板，未连接实例测试 |
| Oracle | JDBC / ODP.NET | SQL*Loader、外部表、批量绑定 | 接口方案，未连接实例测试 |
| ClickHouse / DuckDB 等 | SQL/列式接口 | 按其原生 CSV/Parquet 导入 | 可作为查询层选择，当前**不是内置驱动** |

连接业务库的账号应只读；写结果应使用权限受限的专用账号。数据库驱动和凭据属于**已有业务系统接入层**，不能塞入可分发的 Core 发行包。

## 2. 推荐的数据库表

仅说明逻辑概念；具体数据库的类型、分区/索引、事务语法必须适配：

```
allocation_run       : run_id, scenario, business_version, source_hash, rule_hash,
                       status, expected_total, output_rows, created_at
allocation_result    : run_id, terminal_stage, row_id, account, period, sku,
                       amount DECIMAL(...), dimensions_json
allocation_active    : scenario + business_version → active_run_id
```

**每次从头计算，产生一个新 run_id**；不覆盖之前已成功的 run_id 数据。写入成功且逐笔检查后，只改 `allocation_active`。这不是增量计算，也不是上次结果加减差额。

## 3. PostgreSQL（模板）

1. `CREATE TEMP TABLE tmp_allocation_import(run_id TEXT,row_id TEXT,terminal_stage TEXT,amount TEXT,dimensions_json TEXT)`。
2. 使用客户端 `\copy tmp_allocation_import (...) FROM 'bulk.csv' WITH (FORMAT csv, HEADER true, ENCODING 'UTF8')` 或驱动的 `COPY FROM STDIN`。
3. **注意 BOM**：V20 导出的 CSV 有 UTF-8 BOM，有的导入路径会把 BOM 吸入首个列名/字段，需要在接入适配器中明确剔除、先进行样本验证。可在业务系统流式剥除前三字节 `EF BB BF` 后执行 COPY。
4. 导入暂存数据后，SQL 中检查：唯一 ID、行数、`SUM(amount::NUMERIC)`、run_id 是否相同，和引擎 PASS 摘要逐项比对。
5. 在同一个事务中写入新 `run_id` 结果、更新 `allocation_active`，然后 `COMMIT`。任何检查失败 `ROLLBACK`。
6. 生产建议 `NUMERIC(38,6)` 和 `JSONB`，视报表口径提取 account/period/sku 的实体列并建立索引；超大数据按 `run_id/period` 考虑分区。

> `core/db/Postgres_bulk_schema.sql` 是 V20 自带的设计参考，**不等于生产迁移脚本**。新增模板 `sql/postgres_template.sql` 列举了逻辑表。

## 4. SQL Server（模板）

- 使用应用侧 `SqlBulkCopy`（.NET）或 DBA 管控的 `BULK INSERT` / `bcp`，导入每个新 run_id 的 staging 表；不要用百万次普通网络 INSERT。
- SQL Server 内可以使用 `DECIMAL(38,6)`，维度可存 NVARCHAR(MAX) 的 JSON；重要维度应冗余成独立列或计算列供索引使用。
- 使用显式事务：校验输入行数/金额/唯一键，然后新版本写入并 `UPDATE allocation_active`。
- Windows `BULK INSERT` 运行时文件路径属于**数据库服务器**视角，不等于应用程序客户端的 `C:\...`；跨机器建议应用侧批量上传或共享安全路径。
- `sql/sqlserver_template.sql` 提供必要表结构概念，正式运行前需根据 SQL Server 版本和批量工具调整。

## 5. MySQL（模板）

- `LOAD DATA LOCAL INFILE` 是否启用，取决于服务端和客户端配置；**不要为方便而不受控地放开本地文件读取权限**。
- 建议先从受控 staging 表导入，再使用 DECIMAL 和 JSON 校验及事务切换。支持的字符集要明确设成 `utf8mb4`。
- 若产品使用 `LOAD DATA`，需测量是否使用 InnoDB、事务日志、二级索引带来的额外成本。

## 6. Oracle（模板）

采用 SQL*Loader、外部表或 JDBC batch；用 Oracle NUMBER 表示金额，JSON/文本承载动态维度。临时表装载、校验、事务发布逻辑保持一致。此发行包没有 Oracle 专有可执行导入器。

## 7. “1200 万行是不是必须全入 SQL？”

**不是。** 采用分层策略：

| 数据 | 建议持久化地点 |
|---|---|
| 场景、业务版本、run_id、规则/输入 SHA、状态、总额 | 企业原有 SQL |
| 用户频繁查询的科目×期间×品牌汇总 | 原有 SQL（小很多） |
| 正式 SKU 级结果 | 按业务必要性进 SQL 或列式分析库 |
| 临时测算 SKU 结果 | 可只在 Vault / 临时查询存储，限期保留 |
| 完整 ledger、lineage、驱动分母证据 | 压缩 Vault 或对象存储，不要求 OLTP 入库 |

如果业务确实需要查询千万 SKU 明细，可以入库，但必须**用真实数据库/硬件验证批量装载与查询**。核心的 30 秒计算速度≠ SQL 入库 30 秒。

## 8. 单元测试中的 SQL 一次完整发布

本发行包自带一条已跑通的真实 SQLite 演示（无任何外部数据库安装）：

```bash
python integration/python/sqlite_full_run.py --workspace demo_workspace --reset-demo
python integration/python/sqlite_full_run.py --workspace demo_workspace --amount 2400.00
```

读 `source_amount/source_labor/source_sales`，每次重新生成完整输入并调用 Go，校验 `run_summary.json` + 所有 `reconciliation.csv`，然后**在一个 SQLite 事务内**写入新结果和更新生效指针。失败回滚，老版本不受影响。

这项演示证明**协议可落地与事务处理逻辑可测试**，不证明 SQLite 适合 1200 万行企业吞吐。
