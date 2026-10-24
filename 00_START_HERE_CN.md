# Allocation Core | 跨平台生产验收发行包

**先看结论**：这不是新的金额计算算法。原有 Go 金额引擎、多因子、多阶段 DAG、守恒、血缘、磁盘索引、Vault 和全量重算全部保留。此包重点新增跨平台启动器、Linux ARM64 原生程序、可重复的集成/并发/恢复/SQLite 事务验收，以及可在真实 PostgreSQL/SQL Server 测试库执行的可选验收程序。

**不等于所有环境即插即用**：已经在本次 Linux x86_64 容器实际运行；Windows、macOS 及 Linux ARM64 的原生文件已生成/检查文件格式，但需要对应系统进行真实运行验收。数据库生产吞吐不能从 SQLite 演示推导。

## 1. 我不懂代码，只想先运行

| 你用的电脑 | 首次运行 | 要安装什么 |
|---|---|---|
| Windows Intel/AMD 64 位 | 双击 `run_windows.cmd` | **无**（先完整解压 ZIP） |
| Windows ARM64 | 双击 `run_windows.cmd` | **无** |
| Linux x86_64 / ARM64 | 终端 `chmod +x run.sh && ./run.sh smoke` | **无** |
| macOS Intel / Apple Silicon | 终端 `chmod +x run.sh && ./run.sh smoke` | **无**；需允许可信来源运行 |
| 任何支持 HTTP 的其他应用 | 启动上述系统对应的 Allocation Core 服务，调用 `/v1/jobs` | 宿主语言不需要重写算法 |

运行成功后，默认会在 `runs/` 目录生成 `results.csv`、`reconciliation.csv` 等结果。`run.sh smoke` 使用合成制造业两阶段示例，不会连接任何公司系统。

macOS：可执行文件是针对 Intel 或 Apple Silicon 编译的，**不是** Windows `.exe`。如果 macOS 安全机制阻止了未经签名的程序，需要由你们的 IT 按企业政策核查并批准；不能绕开组织安全政策。

## 2. 为什么要做生产验收

你每年有 FC 0+12 到 FC 12+0 加 ACT 共 14 个场景，单场景可反复全量测算。这套工具提供：

- **单条规则验收**：双因子、三因子、两步骤、分叉合并；每次检查源/目标守恒和逐池对账。
- **28 次并发验收**：14 个场景标签 × 2 版本，28 次都是**独立完整计算**，不使用增量算法。
- **服务级验收**：真实启动 Go HTTP 服务，提交 3 个任务，并发上限为 2，验证服务重启持久化及令牌拒绝。
- **数据库验收**：SQLite 两次完整重算、发布新 `run_id`，失败时回滚并保持旧的生效指针。
- **检查点复用验收**：同输入同规则恢复已完成阶段并对照完整输出；不是模拟断电或执行中途 kill。
- **120 万行压测**：独立生成纯合成数据，再从头计算并核对每行金额总计。

## 3. 开发人员如何跑完整验收

Python **仅用于验收脚本，不是计算引擎的运行依赖**。

```bash
python3 acceptance.py --mode all --workspace ./acceptance_runs
python3 acceptance.py --mode million --workspace ./acceptance_runs --timeout 600
```

验收输出保存在 `acceptance_runs/session_*/acceptance_report.json`，另有 `latest_acceptance_report.json`。同一工作区重复运行会创建新会话，不覆盖旧证据。

## 4. 真实 SQL 数据库怎么验收

见 `docs/REAL_DB_ACCEPTANCE_CN.md`。**只能使用隔离的测试库**，自行配置数据库驱动/账户、执行建表脚本，再显式开启写入开关；运行后记录真实写入秒数、事务日志增长、索引和数据库磁盘占用。本环境未连接真实数据库，这部分必须在项目部署环境跑。

## 5. 安全边界

- 默认 HTTP 服务只监听 `127.0.0.1`。不能简单公网暴露，需要 TLS、网关、身份鉴别和操作系统/容器权限控制。
- 容量预检是软预算；同时配置**独立磁盘卷、硬配额、监控、超时及告警**。
- 原始输入和正式结果的备份应分开管理；Vault 的结果归档本身不等于完整可重演输入快照。
- 本次只增加平台原生文件与**外围验收/接入代码**；Go 内核源文件及原发行包内原有程序保持字节级一致。
