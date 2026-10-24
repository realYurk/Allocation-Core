# Allocation Core V21.1：第三方攻击性审计修订报告

**交付定位：仅修改集成套件，不修改任何计算核心文件。** 每次完整重算，禁止用增量版本代替审计可重演的全量计算。

## 1. 审计问题与处置

| 审计发现 | 处置方式 | 本版验证 | 尚存边界 |
|---|---|---|---|
| Java SDK 只能下载 4 种文件，服务端支持 10 种 | Java/Python 都使用与冻结服务端相同的 10 名称白名单；Java JAR 重新编译 | 真实 HTTP 服务 + Java 8 SDK 下载 results、ledger.gz、source_to_pool.gz、lineage_links.gz、manifest、summary 等，Python 下载结果哈希一致 | 客户端允许某种文件≠某个任务一定生成该文件；缺失返回 HTTP 404 |
| 下载中断可能留下半成品 | 两客户端先写同目录临时文件，验证收到的 Content-Length 后原子替换；异常删除临时文件 | Python 截断模拟和保留旧文件测试 | 若服务未提供 Content-Length，协议无法仅凭客户端确认全量：应另外校验文件 SHA / manifest |
| V21/V20/go-core/0.18.0/health 0.14 混用 | 新增 **RELEASE_IDENTITY.json**，发行版唯一版本 V21.1.0，并显式列出冻结核心原有自报版本； `release_info.py` 同时校验 156 个 SHA | 156/156 双向核验通过 | **冻结核心的 `help`/`run_summary`/`/healthz` 仍输出旧字符串**；不能在“核心字节完全不变”的前提下声称它们已统一。这是发行元数据层的统一，不是修改核心服务报文 |
| 50 万 SKU 导出可能写满磁盘 | 新增 `capacity_preflight.py`，计算估算行数、数据盘空间预算、上限，超限拒绝启动包裹的子进程 | 3,600 池 × 500,000 目标触发 BLOCK、子进程未启动；小案例允许执行 | **不是自动精确预报、不是操作系统磁盘配额。** 不能由它保证所有 DAG 上限；生产须加独立文件系统限额、监控与任务超时 |
| `export-db` 的 dimensions_json 放大 CSV | 文档加入**不经 `export-db` 中转、直接按目标数据库表结构批量装载 `results.csv`** 的设计选择 | 本版未连接真实数据库进行装载压测 | `export-db` 保留不变、没有改写格式；现有 SQL 模板需项目方按库方言验证 |
| Vault 只存结果，不含原始输入 | 集成层新增 `capture_replay_inputs.py`：捕获规则、全部金额/Driver，哈希校验，完整快照重演 | 用新快照从零计算，四份核心 CSV SHA 与原始一致；篡改测试拒绝 | **Vault 本身仍是结果归档**，只有配套保存输入快照、引擎版本才有可重演材料，且外部数据源/运行环境仍需版本化 |

## 2. 容量估算：必须看业务粒度

对于简单情形，终端记录数的粗估：

`金额池数 × 每池实际有效目标行数 × 产生终端记录的驱动分支数`

请勿直接拿公司“全量 SKU 总数”代替“每池实际目标数”。例如不同品牌/期间分配到的目标集合可能互不相同。复杂 DAG、去重、汇合及中间数据会让该公式不再精确。它是**防止明显失控的容量预算近似**。

```powershell
# 需要 Python 的接入开发环境（并非 Go 计算核心的必要依赖）
python integration/tools/capacity_preflight.py --pools 3600 --targets-per-pool 500000 --disk-path C:\AllocationRuns
```

此案例估算 18 亿条终端记录，按照默认每行 results 144B、ledger 300B、整体附加系数 1.5 的保守假设，需约 1,116 GiB 临时空间。输出 BLOCK，不会运行引擎。

```powershell
# 小样例：预检通过才执行 **完整计算**（以下为 Windows x64）
python integration/tools/capacity_preflight.py --pools 3 --targets-per-pool 4 --reserve-free-gib 0.01 --disk-path C:\AllocationRuns --exec core\alloc_windows_amd64.exe run core\examples\manufacturing\rule.json C:\AllocationRuns\demo_v211 --workers 2
```

**严肃边界：** `--exec` 启动才受预检覆盖。绕过该入口直接 CLI/HTTP 提交的任务不会被这个脚本拦截。生产应在服务调度层调用预检，对输出目录使用独立磁盘卷与限额，并记录实际磁盘用量。默认 `--max-rows=50,000,000`、`--max-output-gib=100` 是示例保护阈值，应由业务和 DBA 定义。

## 3. 数据库写入应分层

- 少量任务元数据、有效版本指针、汇总：普通 SQL 事务。
- 需要查询的 SKU 结果：按 `run_id` 直接批量导入扁平 `results.csv` 的 staging 表（Postgres COPY、SQL Server SqlBulkCopy、MySQL LOAD DATA、Oracle SQL*Loader 等）。**不要同时无必要地生成 dimensions_json 中间 CSV。**
- 审计 ledger / lineage / 历史试算：按保留政策放压缩归档，不默认逐行入库。
- 批量导入成功后验证 **行数、金额 NUMERIC/DECIMAL 精度、逐池对账**；最后在同一个事务中切换有效指针。失败回滚，旧有效版本不变。
- 实际批量装载吞吐因数据库、索引、网络和磁盘而变；当前只有 SQLite 教学示例经过端到端验证。

## 4. 版本与冻结边界

发行身份：**Integration V21.1.0**。冻结核心的源码、发行二进制、说明、脚本等 **156 个文件均维持 V20 原始字节**。

老核心内部不同的历史显示值继续存在：`help=V20`、`run_summary=go-core/0.18.0`、`/healthz=allocation-core/0.14`。这些是**不同层次的标识**，不应再当成发行版号；在无法触碰核心时不能强行修改。监控和运维以 `RELEASE_IDENTITY.json` + `CORE_SHA256.json` 为准。未来若允许升级核心，再计划统一服务版本接口，但必须作为新核心版本单独审计。

## 5. 严格验收与未完成项

已验证：V20 SHA 全部一致、Java 8 JAR 真实 HTTP 下载、Python 下载与截断拒绝、SQLite 双全量运行/事务回滚、输入快照独立重算一致、容量拦截、发行包解压校验。

未宣称：自动准确预测复杂 DAG 最终行数、真实 SQL Server/PostgreSQL/Oracle 压测、服务端跨机器 TLS / RBAC / 分布式高可用、硬磁盘配额、所有运行模式的端到端断电恢复、原始 V20 Vault 自动成为可重演归档。
