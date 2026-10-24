# Integration V21 实测与边界报告

## 本次**新增**的是什么？

本次版本**只新增接入发行物**，不修改 V20 的任何计算程序或源文件。不存在 V21 金额计算算法。

## 验收结果

| 内容 | 实际结果 |
|---|---|
| 核心冻结 | 原始 V20 目录与发行目录 `core/` 的 **156 个文件 SHA-256 全部匹配**，详见 `CORE_SHA256.json` |
| Go 业务测试 | `go test ./...` 全通过 |
| Go 静态检查 | `go vet ./...` 通过 |
| SQLite 第一次完整计算 | 源数据库→CSV→Go 引擎→守恒→SQLite 事务发布，商品 A/B/C/D 为 100/300/400/400，总额 1,200 |
| SQLite 第二次完整计算 | 更新原始来源为 2,400，**从头执行**，输出 200/600/800/800；新 run_id 发布成功，保留旧 run_id |
| SQL 防半成品发布 | 故意制造应写入行数与实际行数不符，事务回滚；旧 active run_id 保持不变 |
| Java 轻量 SDK | `javac --release 8` 编译 JAR 成功；本机 HTTP 服务 Health/Submit 正常 |
| Python SDK | Python 标准库提交任务、轮询到 PASS、下载 `results.csv` 正常 |
| Windows ZIP 文件路径 | 所有发行文件路径只包含 ASCII（文档和业务数据内容仍可为中文） |

## 不得夸大的地方

- 没有对真实的 PostgreSQL/SQL Server/MySQL/Oracle 实例做批量写入测试。
- SQLite 演示行数极少，不能用来估算 1000 万行写库吞吐。
- Java JAR **只有客户端**，不是 Go 算法编译成的 JAR。
- V20 Go 服务不是多实例高可用、RBAC/TLS 完整生产平台；需企业边界服务隔离。
- 没有缓存参与财务增量复算，本包只执行**从源数据全量重算**。

---

## V21.1 独立审计问题修复（集成层）

- **冻结核心验证**：`python integration/tools/release_info.py`，156/156 SHA-256 一致；计算 core 完全没有修改。
- **Java 8 SDK**：`javac --release 8` 重编译；`javap` 显示 major 52。客户端文件白名单从 4 个扩展至服务端原生提供的 10 个。
- **实际 HTTP 验收**：真正启动冻结 core HTTP 服务，提交合成 split_merge 规则、轮询 PASS；Java 下载 7 个实存证据文件（包含 `ledger.csv.gz`、`lineage_links.csv.gz`、`evidence_stream_manifest.json`），Python 下载同一批文件字节一致。不存在的业务输出仍按 HTTP 404 处理。
- **下载完整性**：Java/Python 临时落盘并在 HTTP 2xx 且 Content-Length 完整时发布；Python 专门模拟连接截断验证保留原文件。
- **容量预算预检**：极端案例 3,600 池 × 500,000 目标 → 18 亿行，预算估算 1,116 GiB，未启动下游命令；小型场景允许调用子进程。
- **完整输入快照**：保留 rule + 全部金额与 Driver 并校验哈希；针对同一合成两阶段业务，重演四份计算文件 SHA 完全一致，篡改输入时拒绝验证。
- **原有集成回归**：SQLite 2 次全量重算、原子切换生效版本、故意出错的事务回滚通过；冻结核心 `go test ./...`、`go test -race ./...`、`go vet ./...` 通过。
- **版本显示边界**：`RELEASE_IDENTITY.json` 统一 V21.1.0 集成发行身份；V20 内部老版本字符串仍然存在并被明确列出（不触碰冻结核心）。

未覆盖：真实生产 SQL Server/PostgreSQL 入库压测、跨主机网络中断的每一种文件传输情况、操作系统级磁盘配额、复杂 DAG 绝对精确的行数预测、生产级多节点部署。
