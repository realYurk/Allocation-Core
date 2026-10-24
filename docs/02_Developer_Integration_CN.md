# 开发人员接入协议 | Allocation Core Integration V21

## 0. 先读约束

- **本版锁定 V20 计算核心：不得修改 `core/internal/engine`、`core/alloc_*`。** 接入层独立部署。
- 每个运行都使用**完整源数据全量重新计算**，不使用上一次结果作为缓存增量，不做局部依赖复算。
- 财务金额解析只用 Decimal 或数据库 NUMERIC/DECIMAL，禁止 float/double。
- 以 `allocation-ir/2.0`、规则文件和字段映射为跨语言协议；连接器没有分摊算法。

## 1. 选择集成形态（按推荐顺序）

| 模式 | 使用场景 | 技术组成 | 主要优点/限制 |
|---|---|---|---|
| **HTTP Sidecar**（推荐） | Java / Python / 微服务 / 多团队共享 | Go Core 常驻进程 + HTTP 客户端 | 对计算内核侵入最小；V20 默认只监听本机；生产跨机器通信需受控网关/TLS |
| **CLI 子进程** | Windows 定时任务、批作业、单应用部署 | Java ProcessBuilder / Python subprocess 调 Go EXE | 不需要常驻服务；需限制并发、确保权限与超时、处理文件隔离 |
| **本地库** | 强制只运行在同进程内 | 当前**未提供** JNI/C ABI / Java 原生内核 | 不能声称一个 JAR 就有全部分摊算法 |
| **模型平台适配** | 既有模型引擎、Excel 或其他工具 | 工具自身导出/导入 + Go CLI/HTTP | 数据迁移接口需按该模型平台二次实现 |

产品交付方式：`core/alloc_<os>_<arch>` 是计算进程；`integration/java/allocation-client-java8.jar` 是轻量 HTTP SDK，`integration/python/http_client.py` 是无依赖 Python SDK；`sql/` 是不同数据库导入参考。

## 2. 业务源数据映射（完整快照）

V20 规则样例使用：

- 来源金额 `amounts.csv`：`period,plant,brand,family,sku,account,currency,amount,row_id`
- 工时 Driver `labor.csv`：`period,plant,family,hours`
- 销量 Driver `sales.csv`：`period,plant,family,sku,units`

实际公司可以换成自己的 `dimensions` 和 CSV 结构，修改 `rule.json` 的 `datasets`、`stages`、`source.grouping`、`target`、`conservation_keys`；**不要修改计算源码**。

关键守恒：`immutable_dimensions` 强制不可跨越，`conservation_keys` 指定每阶段各自检查的边界。多因子以 `allocations[*].share` 表达份额，多阶段以 `stages[*].inputs[*].from` 构造 DAG。规则语义不确定时先 `doctor`、`validate`、`explain`，不要直接在生产数据上碰运气。

## 3. 执行接口

### HTTP 服务启动（本机示范）

```bash
core/alloc_linux_amd64 serve --root core/examples --store ./service_state --listen 127.0.0.1:8787 --jobs 2 --queue 16
```

Windows 使用 `core/alloc_windows_amd64.exe`。服务启动后 `GET /healthz`。

`POST /v1/jobs`：

```json
{"rule":"manufacturing/rule.json","workers":2,"disk_partitions":4,"evidence_gzip":true}
```

响应 `202`，获取 JSON 中 `id`，轮询 `GET /v1/jobs/{id}` 直到 `PASS/FAILED/INTERRUPTED`。只有 `PASS` 才下载：

- `results.csv`：分摊结果；
- `reconciliation.csv`：金额池检查；
- `ledger.csv`：完整计算证据（HTTP 可用性以服务实际 endpoint/输出模式验证）；
- `source_to_pool.csv`：输入归池关系。

具体按 `core/docs/02_HTTP_Service.md`。**不要把任意客户端上传文件路径当作服务器本地路径。** 生产中由服务侧受控落盘到 `--root` 下，再提交相对规则路径；数据目录必须隔离租户，不允许跨目录访问。V20 的单机令牌不足以支撑公网部署。

### Java 8 SDK（已编译为 JAR）

```java
import org.allocation.integration.AllocationClient;
AllocationClient client = new AllocationClient("http://127.0.0.1:8787", "");
String body = client.submit("manufacturing/rule.json");
// 用项目现有 Jackson/Gson 将 body 解析为 id；轮询 client.status(id)
// 仅 PASS 后 client.download(id, "results.csv", Paths.get("results.csv"))
```

构建时把 `integration/java/allocation-client-java8.jar` 加到 Java 项目的依赖（或将源码放入已有项目）。这只提供接口 SDK；不包含计算算法。JAR 使用 Java 8 标准库，不要求 Spring Boot 或第三方包。

### Python 标准库 SDK

```python
from http_client import AllocationClient
client = AllocationClient('http://127.0.0.1:8787')
job = client.submit('manufacturing/rule.json')
status = client.wait(job['id'],timeout=180)
if status['status'] != 'PASS':
    raise RuntimeError(str(status))
client.stream_result(job['id'],'results.csv','results.csv')
```

### CLI 模式

```bash
core/alloc_linux_amd64 doctor company/rule.json
core/alloc_linux_amd64 validate company/rule.json
core/alloc_linux_amd64 run company/rule.json runs/full_001 --workers 4 --disk-partitions 8 --evidence-gzip
```

CLI 完成后先检查 `run_summary.json` 的 `status=PASS`、`input_amount=output_amount`，并逐条检查 `reconciliation.csv` 每个金额池 `PASS` 且 delta=0，才能提交数据库加载。失败时保留旧有效结果。

## 4. SQL 输入、SQL 输出的推荐层次

**读：** 自己的 JDBC/ORM/DB-API 使用只读 SQL 查询，以单一业务截点输出完整金额和驱动数据。保证一致性快照，否则在提取过程中源数据变更会导致跨表不一致。

**算：** 不给 Go 引擎增加数据库驱动：使用 CSV/受控文件数据契约，它不绑定任何 SQL 方言。这对上百个科目、14 场景、动态维度特别重要。

**写：** 

```
Core PASS + Source hashes
    → 每个 run_id 的 staging 表/临时文件
    → 批量 COPY/SqlBulkCopy/LOAD DATA/SQL*Loader
    → 数据库事务内行数/金额/唯一键检查
    → INSERT 完整新 run_id 的最终结果
    → 原子修改 ACTIVE_POINTER
    → COMMIT
```

**只对正式需要查询的最终结果入库，不要把所有 ledger/血缘默认写入 OLTP。** 高级分析场景可用列式库/Parquet；V20 原生不写 Parquet。

在任何 SQL/批量适配器里：不要直接 DELETE 当前有效结果然后 INSERT；要隔离新 run_id 并在校验后更新有效指针。

## 5. 实际已验证的 DB 集成例子

`integration/python/sqlite_full_run.py` 真实执行：SQLite 表 → CSV 快照 → Go V20 全量计算 → 检查每个池 → SQLite 事务写入并切换有效指针。

```bash
python integration/python/sqlite_full_run.py --workspace demo_workspace --reset-demo
python integration/python/sqlite_full_run.py --workspace demo_workspace --amount 2400.00
```

这两次**都从源数据完整重新计算**；数据库保留旧结果。SQLite 是方便任何开发者在本机完成验收的教学实现，**不是千万行性能基准**。

## 6. 数据契约与幂等、隔离

- `scenario` 是业务场景，如 FC 10+2；`business_version` 是版本；`run_id` 是每次运行唯一生成的技术 ID。绝不能三者混为一谈。
- 同场景可以运行多个版本；任何重算都生成新的 `run_id`，不直接覆盖已发布明细。
- 规则与每份输入按 SHA-256 快照；结果的 `run_summary` 和分组守恒仍需复核。
- 数据库唯一约束推荐 `(run_id, terminal_stage, row_id)`；中断导入必须回滚或清理该未发布 run_id。
- SQL 出口 `vault export-db` 只提供 `run_id,row_id,terminal_stage,amount,dimensions_json`；对常用维度可以从 JSON 提取列建索引。
- 多源币种不得跨币种相加，必须将 `currency` 保持不可跨越，或在分摊前明确完成汇率换算。
- `precision` 0–6；目标数据库字段类型应满足数据精度及最大值范围；禁止用浮点。

## 7. 上线前必须补齐的企业责任

- 身份鉴权、租户隔离、TLS/反向代理、文件权限、密钥管理。
- 任务超时、取消、配额、磁盘水位、归档生命周期、监控告警。
- 真实目标数据库的批量入库性能与失败回滚测试。
- 企业每个科目场景的业务归属、分摊口径与对账责任。

这些不应混进数值计算核心；应由现有项目或独立接入层负责。


## V21.1 发布修订

请继续阅读 [V21.1 审计整改与能力边界](05_V21_1_Audit_Fixes_CN.md)。Java/Python SDK 现对齐全部十种文件下载，失败不会覆盖已下载文件。`capacity_preflight.py` 只进行部署层粗估且不能替代系统硬配额。冻结的 V20 `/healthz` 和运行摘要中的历史标识未被改写；发行身份以 `RELEASE_IDENTITY.json` 为准。
