# V20｜千万级分摊结果怎么落入 SQL？

## 先回答你最担心的事情

**千万行逐条 `INSERT` 往往是错误方案**：每行的网络往返、SQL 解析、索引维护和事务开销，可能把秒级计算拖成大量后处理时间。本引擎一直主要将结果写到 CSV/压缩证据，**当前没有声称已把千万行全部写入 SQL**。

建议把存储分成三级：

| 层 | 保存什么 | 推荐位置 |
|---|---|---|
| 元数据 | 场景、版本、任务状态、正式/试算、哈希、总额、路径、有效指针 | 关系型数据库，记录数很少 |
| 业务查询 | 常用「科目 × 期间 × 品牌」汇总；按需要保留正式版 SKU 明细 | 数据库或列式分析仓库；批量导入、按场景/期间分区 |
| 全量计算证据 | 完整 ledger、lineage_links、source_to_pool、运行快照、可重放输入 | V20 Vault 压缩归档，或后续对象存储 |

不要为了“数据可查询”就把每一步的几千万条中间 ledger 全部写进 OLTP 表。它们可以通过审计查询接口按需获取。

## V20 已实现的最小通用协议

```text
vault export-db VAULT RUN_ID NEW_BULK.csv
```

生成字段：`run_id,row_id,terminal_stage,amount,dimensions_json`。

- `run_id`：同一个计算版本的唯一归档 ID（不是 FC 场景名）。
- `row_id`：这笔输出的计算行 ID。
- `terminal_stage`：终端步骤名称；多个终端分支可共存。
- `amount`：保持计算输出里的精确十进制文本，不进行二次浮点运算。
- `dimensions_json`：该行其余所有自定义维度，如 account/period/brand/sku/region 等。

`export-db` 使用 Go 标准库**流式读取、流式生成 CSV**，不会整表读入内存。中文是 UTF-8 BOM 格式。当前这是**批量导入数据准备器，不是数据库客户端**。

## PostgreSQL 参考流程（由企业 DBA 执行）

```sql
CREATE TABLE IF NOT EXISTS allocation_result (
    run_id TEXT NOT NULL,
    row_id TEXT NOT NULL,
    terminal_stage TEXT NOT NULL,
    amount NUMERIC(38,6) NOT NULL,
    dimensions_json JSONB NOT NULL,
    PRIMARY KEY (run_id, terminal_stage, row_id)
);

CREATE UNLOGGED TABLE IF NOT EXISTS allocation_result_stage (
    run_id TEXT, row_id TEXT, terminal_stage TEXT,
    amount TEXT, dimensions_json TEXT
);
```

使用 `psql` 的客户端 `\\copy`（**是 psql 命令，不是普通 SQL 文本**）：

```text
\copy allocation_result_stage(run_id,row_id,terminal_stage,amount,dimensions_json) FROM 'C:/allocation/my_bulk.csv' WITH (FORMAT csv, HEADER true, ENCODING 'UTF8');
```

> 提示：PostgreSQL CSV 的 UTF-8 BOM 可能成为首列内容，具体依赖导入方式；可使用 psql 验证首行或先移除 BOM，并在执行正式 `COPY` 之前先校验样本。示例中 `UNLOGGED` 仅用于可重建临时表，数据库宕机可能丢失临时表数据，不可直接当作正式财务存储。

之后在事务中完成校验与正式导入（示意）：

```sql
BEGIN;
INSERT INTO allocation_result(run_id,row_id,terminal_stage,amount,dimensions_json)
SELECT run_id,row_id,terminal_stage,
       amount::NUMERIC(38,6),dimensions_json::JSONB
FROM allocation_result_stage
ON CONFLICT (run_id,terminal_stage,row_id) DO NOTHING;
-- 检查行数、金额总和、run_id 归属以及引擎守恒文件后，再 COMMIT
COMMIT;
```

实际生产还应增加任务 ID 白名单、总额对账、审批以外的技术确认、清理 staging、索引/分区设计、失败回滚及可靠重试。**V20 没有替你连接数据库执行这些事务。**

## SQL Server / MySQL 的通用建议

- SQL Server：文件可由 DB Server 读取时考虑 `BULK INSERT`，或用应用程序 `SqlBulkCopy` 批量写入；先 staging，再校验并切换有效版本。
- MySQL：评估 `LOAD DATA`，需要注意服务端/客户端文件访问权限和本地加载安全设置；批次事务与二级索引成本需实际压测。
- ClickHouse / DuckDB / Parquet：对于大规模多维查询，列式读取和压缩可能更合适；当前 V20 **未提供 Parquet 原生写入**，如采用应由适配器处理。

## 14 场景、十个临时版本的建议

- 关系库只建 `allocation_run` / `allocation_active_version` 元数据：例如 FC-10+2 的正式版指向某个唯一 `run_id`。
- 业务常用报表可保存聚合结果；需要 SKU 明细时优先保留正式版，试算版按需生成临时结果。
- 换正式版时修改**有效版本指针**，不必先删除上亿行历史再重写。
- 如确实要求试算版本入库，设置 TTL 和清理任务；删除应在完整性校验、法定留存策略确认后执行。
- 数据库写入速度必须在目标库按真实数据、索引和硬件测量；本文件不虚构 SQL 入库的“每秒千万行”指标。

## 如何理解「替换一个版本」？

不能简单拿新的 `version` 文件覆盖旧目录。正确步骤是：新计算产生唯一 `run_id` → 校验守恒 → 登记归档 → 可选批量载入 → 原子更新有效版本指针 → 旧 draft 进入保留期 → 预览清理 → 归档 GC。

**数学引擎与财务结果不变；改变的是版本存储、审计保留和下游发布策略。**
