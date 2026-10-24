# DBA 与应用开发人员：真实数据库生产验收手册

## 为什么这一轮还不能宣称“生产数据库已验证”

当前执行环境没有你们公司的数据库实例、凭据、网络路由、磁盘卷与真实业务规则。这一轮验证了 SQLite 的真实事务发布和回滚，并提供 PostgreSQL/SQL Server **可以在隔离测试库上实际执行的脚本**。不能把本机 CSV 导出速度或 SQLite 耗时称为正式 SQL Server / PostgreSQL 写入速度。

## 你们 DBA 需要提供的条件

1. 一个**独立的测试数据库**和受限测试账号，建好 `sql/postgres_template.sql` 或 `sql/sqlserver_template.sql` 所列三张业务表。
2. 测试库至少可容纳两次完整运行的明细、索引与事务日志；容量按 `金额池数 × 有效目标数 × 因子路径` 粗估并加预留。
3. 明确测试的计算实例规格、CPU 核、内存、底层磁盘吞吐、数据库位置、网络 RTT、连接数、索引及并发任务数。
4. 安全团队批准测试库连接方式。密码通过 `ALLOC_DB_DSN` 环境变量注入，**不要放进脚本/ZIP**。
5. 重要：运行下面的命令会**持久写入**测试数据库，不自动删除试算历史；需要 DBA 按组织策略清理。

## PostgreSQL 真实测试

```bash
python3 -m pip install psycopg[binary]
# 先让 DBA 在隔离测试库执行 sql/postgres_template.sql
export ALLOC_DB_DSN='host=... dbname=allocation_test user=... password=...'
export ALLOC_TEST_ALLOW_DB_WRITES=YES

python3 acceptance_live_db.py --db postgres --profile demo \
 --i-understand-this-writes-to-my-test-database

# 经过容量评估、确认测试库有足够空间后：
python3 acceptance_live_db.py --db postgres --profile million \
 --i-understand-this-writes-to-my-test-database
```

`--profile million` 会执行**两次独立全量计算，分别新增 120 万结果行到数据库**，旧运行不删除，当前有效版本切换到第二次。该脚本记录引擎耗时、实际 SQL `COPY` 写入及事务完成耗时、数据库行数/金额、有效版本和输出体积；这是真实测试库的结果，不是模拟。超大测试前设置数据库/OS 磁盘硬限制，单独观察事务日志。

**PostgreSQL DBA 参考观测 SQL**（需要相应权限）：

```sql
SELECT pg_current_wal_lsn(); -- 测试前、后各采集一次
SELECT pg_wal_lsn_diff('AFTER_LSN', 'BEFORE_LSN'); -- WAL 增长字节
SELECT pg_total_relation_size('allocation_result') AS result_table_plus_index_bytes;
SELECT datname, tup_inserted, deadlocks, temp_bytes
  FROM pg_stat_database WHERE datname = current_database();
```

检查活跃指针：

```sql
SELECT scenario, business_version, active_run_id FROM allocation_active;
SELECT run_id, COUNT(*), SUM(amount) FROM allocation_result GROUP BY run_id;
```

## SQL Server 真实测试

在隔离测试库上 DBA 执行 `sql/sqlserver_template.sql`，安装公司批准的 ODBC Driver，配置 `pyodbc`：

```powershell
py -m pip install pyodbc
$env:ALLOC_DB_DSN = 'DRIVER={ODBC Driver 18 for SQL Server};SERVER=...;DATABASE=allocation_test;UID=...;PWD=...;Encrypt=yes;TrustServerCertificate=no'
$env:ALLOC_TEST_ALLOW_DB_WRITES = 'YES'
py acceptance_live_db.py --db sqlserver --profile demo --i-understand-this-writes-to-my-test-database
# 评估空间后再将 demo 换成 million
```

**SQL Server DBA 参考观测 SQL**（某些 DMV 需要额外权限）：

```sql
SELECT total_log_size_in_bytes, used_log_space_in_bytes, used_log_space_in_percent
FROM sys.dm_db_log_space_usage;
SELECT OBJECT_NAME(object_id) AS table_name, SUM(reserved_page_count)*8*1024 AS reserved_bytes
FROM sys.dm_db_partition_stats WHERE object_id = OBJECT_ID('dbo.allocation_result')
GROUP BY object_id;
SELECT scenario, business_version, active_run_id FROM dbo.allocation_active;
```

SQL Server 适配器当前走 `pyodbc.fast_executemany`，不是原生 `SqlBulkCopy`；可以在实际 Java/.NET 项目中按已有技术栈替换，前提是保持同样的事务校验与发布边界。SQL Server 大型表导入期间事务日志可能非常大，必须结合恢复模式、日志备份和批量策略由 DBA 评估。

## 必须跑的异常测试（建议在**专用测试库**做）

| 验收项目 | 测试方式 | 必须的结果 |
|---|---|---|
| 两次全量重算 | 改原始金额，分别计算导入 | 历史两个 run_id 均在，生效版本仅指向第二次 |
| 行数与总额 | 运行后 DB COUNT + 精确 SUM | 等于引擎摘要；每个金额池守恒 |
| 导入过程中失败 | 人为中断/让测试事务失败 | 数据回滚，旧指针不动，无部分生效 |
| 重复提交相同 run_id | 第二次导入同一 run_id | 唯一约束拒绝或幂等机制按接口设计拒绝 |
| 并发发布同一 scenario+version | 在隔离测试环境同时提交 | 没有“指针成功、结果不完整”的撕裂发布 |
| 磁盘快满 | 专用配额卷测试 | 任务失败可检测，不对外发布不完整结果 |
| 网络中断 | 断开测试数据库会话 | 事务回滚，业务系统明确报告失败 |
| 百万级与千万级 | 从 120 万逐步增加 | 记录总耗时、DB 装载耗时、日志、索引膨胀、CPU、内存、磁盘和网络 |

当前 `acceptance_live_db.py` 自动验证**前两项**；其余需真实部署环境下单独执行和签署结论。不要在真实财务生产库上用故障注入。

## 源输入与金额精度

Go 引擎始终从原始费用与各 Driver **全量重新计算**。SQL 入库时保持 `Decimal`/`NUMERIC(38,6)` 或 `DECIMAL(38,6)`，任何路径都不得转为 float。数据库中按新 `run_id` 保存不可变结果，完成结果行数和金额校验后，**事务内**切换 `(scenario,business_version)→active_run_id`。原始业务数据和规则要另建带 SHA 的快照；Vault 仅保存完成结果与证据，不等于输入备份。
