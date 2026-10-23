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
