# Allocation Core · 本次可复现实测报告

测试时间：2026-10-24。环境：当前 Linux x86_64 容器。全部使用合成数据，**不是**用户企业的真实生产环境。

## 本次真正运行过的验收

| 项目 | 实测 | 判定 |
|---|---|---|
| 4 类规则：两阶段、双因子、三因子、分叉合并 | 四个独立计算输出 | PASS |
| FC 0+12 到 FC 12+0 + ACT | **14 场景 × 2 版本 = 28 次独立全量重算** | PASS |
| 多场景并发 | 最多 3 任务同时运行，合计约 0.14 秒（小规模） | PASS |
| SQLite SQL → 核心 → SQL | 两次全量重算，旧版本保留，新版本生效 | PASS |
| SQLite 故意制造发布失败 | 整个事务回滚，原生效版本不变 | PASS |
| 阶段检查点恢复 | 复用 2 个已完成阶段，5 份结果/证据 SHA 对照一致 | PASS |
| HTTP 服务 | 3 任务（限 2 并发），拒绝无令牌访问、重启历史可查 | PASS |
| 容量准入 | 小容量预算放行、明显超限预算拒绝 | PASS |
| 120 万终端行 | 1,200 金额池，耗时 **5.56 秒** | PASS |
| 120 万终端行总金额 | **6000000.00**，逐池、全量核对 | PASS |
| 120 万终端行的完整输出 | **143.5 MiB**（results.csv + 压缩证据等） | 已测 |
| Go `test`, `vet`, `test -race` | 本次重新运行 | PASS |
| PostgreSQL 真实批量入库 | 未连接真实实例 | **待项目环境验收** |
| SQL Server 真实批量入库 | 未连接真实实例 | **待项目环境验收** |
| Windows x64/ARM64、Mac Intel/ARM、Linux ARM64 真机启动 | 已构建/检查二进制格式，但未真机执行 | **待目标系统验收** |

### 不应夸大的边界

- 28 次并发试算使用四行左右的小规模业务案例，只证明隔离和调度路径，并**不等于** 28 次千万行压力测试。
- 120 万行样例是单阶段、大规模终端结果，不代表所有复杂多阶段 DAG 的固定耗时。
- SQLite 成功并不意味着 PostgreSQL / SQL Server 的 COPY、ODBC、事务日志、锁和网络性能已经通过。
- 目前的阶段恢复验证是成功阶段的复用，并未模拟中途杀进程、断电和磁盘填满。
- `run_summary.json` 与完整 `reconciliation.csv`、`results.csv` 三重核对；金额使用 Decimal/整数表达，不使用浮点进行金融加总。
- 真实生产准入还需要你们的复杂 FC 规则对照、数据库分档压测、资源硬配额和异常恢复演练。

## 复现

```bash
python3 acceptance.py --mode all --workspace ./acceptance_runs
python3 acceptance.py --mode million --workspace ./acceptance_runs --timeout 600
python3 -m unittest discover -s tests -p test_db_source_fingerprint.py -v
```

完整机器可读测量结果：`ACCEPTANCE_MEASURED_RESULTS.json`。
