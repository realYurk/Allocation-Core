# Allocation Core Clean V9 / 通用分摊引擎

**One Go module · One Windows executable · One workspace.**
**一个 Go 模块、一个 Windows 程序、一个业务工作台。**

This repository contains **no company data or proprietary allocation rules**. Inputs, outputs, mappings and runtime records stay outside version control. / 本仓库不包含企业真实数据和私有规则；输入、输出、映射与运行状态默认不纳入 Git。

> **MySQL 注意 / IMPORTANT**：本 ZIP 内附的离线 Windows EXE 保留全部已有功能，但**不包含官方 MySQL 驱动**。需要使用在线构建的 MySQL-enabled EXE（GitHub Actions）或 `go mod download && go build -tags mysql` 才能执行 `data mysql` / `mysql-bundle`。源码已实现，模拟事务测试通过；**目标企业 MySQL 实库联调仍待执行**。

## Clean V9: CI reliability and bounded disk sharding / V9 CI 修复与分区内存优化

- **Fix GitHub Linux CI / 修复 Linux CI**: Create `output/` before redirecting benchmark output. Prior GitHub test results showed the financial/race tests had passed; the job only failed at the benchmark redirect (`No such file or directory`). / 在写 `output/ci-benchmark.txt` 前创建目录，防止 CI 假失败。
- **MySQL TLS read-only integration / 严格 TLS 只读验收**: The isolated MySQL test database and `REQUIRE SSL` SELECT-only account are now provisioned through the container-local administrative socket. The Go integration test verifies **the real production TLS connector**, a non-empty server-side TLS cipher, a rejected `UPDATE`, exact DECIMAL import and wrong-SAN rejection. The job checks `have_ssl=YES` and `require_secure_transport=ON`, and prints container logs if the test fails. **CI must be run after updating GitHub; it has not been demonstrated as passing yet.** / 隔离 MySQL 管理员仅负责准备数据；正式验证改由 TLS 校验的只读账号完成，并在失败时输出诊断。尚需更新仓库后运行 CI。
- **Bounded partition buffers / 有界分区缓冲**: Up to 64 temporary JSONL shards use a **2 MiB aggregate user-space buffer budget**, instead of the previous 16 MiB eager allocation. Shard files are opened lazily; empty shard files are preserved for existing readers. / 分区写入器按需创建，64 分区缓冲预算从 16 MiB 降到 2 MiB；旧文件名与 JSONL 格式完全兼容。
- **No change to financial arithmetic or checkpoints / 金额及检查点不变**: Decimal arithmetic, rule evaluation, ledger, lineage, deterministic output and checkpoint record format are unchanged. An actual V8 checkpoint was resumed by V9 with all five outputs identical. / 本次不修改分摊公式、尾差、计算规则、审计或检查点格式；已用 V8 检查点在 V9 续算并核对五份结果。

Quick checks / 快速验证：

```text
AllocationCore.exe demo .
AllocationCore.exe run examples/manufacturing/rule.json output/v9-verify --workers 4 --disk-partitions 64 --evidence-gzip
```

## New in V7: Financial Correctness & Production Hardening / V7 财务正确性与生产加固

**No new program entrances / 不增加新的程序入口。** Still one Go module, one Windows EXE, four bilingual Markdown files. The public engine is unchanged at the amount-formula layer. / 保持一个 Go 模块、一个 Windows EXE、四份中英双语文档；金额分摊公式未改。

- **Independent financial oracle / 独立金额黄金测试**: manually calculated two-stage outcomes and 5,000 deterministic rational-number cases verify exact cents, signed weights, tie handling, conservation and permutation stability. / 手工预期值与独立有理数计算器共同验证尾差、负金额、因子、顺序及守恒。
- **Same-transaction MySQL validation / MySQL 元数据与数据同事务**: InnoDB table, unique signed BIGINT key and exact DECIMAL/integer measure are checked inside the SAME `REPEATABLE READ` read-only transaction as all pages. / 所有源表预检、分页及多表导出共用同一个快照事务；拒绝 FLOAT/DOUBLE。
- **Publish after snapshot release / 先关闭事务再发布**: a single MySQL CSV remains in a private staging directory until all pages, checks and the transaction release succeed. A multi-table snapshot publishes the entire directory once. / 任何断线、非法数据、事务释放失败都不产生正式结果。
- **Import integrity / 输入文件指纹**: every normalized CSV report now contains exact output `SHA-256` and byte size. MySQL bundle manifest records these for all exported files. / 输出记录哈希与字节数，便于后续验证。
- **Strict duplicate-coordinate option / 严格重复坐标验收**: `reconcile --reject-duplicates` rejects repeated full business coordinates on either side, rather than silently aggregating. The legacy aggregation mode remains available and now reports duplicate counts. The Studio page offers a checkbox. / 同坐标重复金额既可按旧方式显式聚合并报告数量，也可严格拒绝。

**What is NOT proven / 尚未完成的验收**: official-driver TLS/SAN client-to-server test with a purpose-issued certificate; real enterprise MySQL credentials/permissions, concurrent DDL fault injection, multi-million-row representative business references and Windows target-device UAT. Current bundled offline EXE does not link the external MySQL driver; build with `-tags mysql` in a networked environment or use the corresponding GitHub CI artifact. / 官方驱动 TLS 全链路、目标企业生产前 UAT、复杂业务大规模基准仍需独立验证；离线 EXE 不含外部 MySQL 驱动。

Quick verification / 快速检查：

```text
AllocationCore.exe version
AllocationCore.exe demo .
AllocationCore.exe inspect examples/manufacturing/rule.json
AllocationCore.exe run examples/manufacturing/rule.json output/v7-run --workers 4
AllocationCore.exe reconcile --actual output/v7-run/results.csv --reference input/reference/reference.csv --out output/v7-acceptance --precision 2 --tolerance 0 --reject-duplicates
```

Only the last command needs your independently prepared reference file; never use engine output as the sole correctness oracle. / 最后一条需自行提供独立参考结果；不能只用引擎输出证明自己正确。

## 30-second start / 小白快速启动

1. Download the release ZIP and extract **all** files to a new folder. / 下载完整发行 ZIP 并全部解压到新文件夹。
2. Double-click **`AllocationCore.exe`**. No PowerShell, Python, Java, Node or Go is needed for running the supplied Windows binary. / 双击程序即可；使用发行包不需要安装开发环境。
3. Wait for **`READY http://127.0.0.1:xxxx/workspace`**, then use the browser workspace. / 等待 READY 后使用浏览器；端口被占用时会尝试 8867—8885。
4. Select **Create demo project**, then **Run**. The demo is entirely synthetic and generated under your user data directory on first launch. / 创建演示项目并运行，样例是合成数据，首次使用时在本机生成。
5. Check the result: **input total, output total, PASS, elapsed time**, then open the comparison tab. / 查看分摊前后金额、校验结果、耗时以及数据对比。

**If the browser cannot connect**, keep the console window open and inspect the displayed error. No browser is opened before the local health endpoint responds. / 如连接被拒绝，以终端实际提示为准；健康检查未通过不会先打开浏览器。

## Build from source / 本地源码构建

Install Go 1.23+ **only when building**. / 只有自己重新编译时才需安装 Go 1.23+。

```bash
go build -trimpath -ldflags="-s -w" -o AllocationCore.exe .
```

Build once, run the same executable for all modes. / 只编译一个程序，所有功能共用它。


## Clean V8: Verified I/O & TLS guardrails / 经过验证的 I/O 与 TLS 加固

- **Temporary JSONL spool passthrough / 临时中间文件透传**: intermediate disk-shard routing decodes each JSONL row once, uses its original raw record bytes in the destination shard, and avoids the second JSON serialization. The durable checkpoint format and public financial CSV outputs are **unchanged**. Oversized records, truncated records and exact-byte preservation have new regression tests. / 中间磁盘分区减少重复 JSON 编码；检查点格式和正式结果不变，新增超长行及损坏文件的失败关闭测试。
- **Independent TLS policy tests / 独立 TLS 证书测试**: ephemeral CA + signed server cert are tested without any database dependency; valid chain/SAN succeeds, wrong SAN or untrusted CA fails, with TLS 1.2 minimum. / 本地无需数据库即可验证 CA、SAN、服务器名称和最小 TLS 版本。
- **Live MySQL fault gates / 实库故障门槛**: CI now defines an **additional** ephemeral TLS-required MySQL 8.4 job with a temporary CA/SAN certificate and a negative wrong-hostname test; its standard MySQL job also tests blocking a concurrent DDL until the read-only snapshot is released. These jobs are **not validated locally** and must actually pass on GitHub after updating the repository. / 新增隔离实库 TLS 和并发 DDL 检验，必须在 GitHub CI 真正运行通过后才能声明已验收。
- **No new executables / 不增加入口**: one Go Module, one Windows EXE, no CMD or BAT. This package's offline Windows EXE has no linked native MySQL driver; to build a MySQL-enabled EXE, use `go mod download && go build -tags mysql -o AllocationCore.exe .` online (or the CI Windows artifact). / 交付仍只有一个用户程序；离线 EXE 不包含官方 MySQL 驱动。

## Clean V5 baseline / V5 已有基础

- **Adaptive dimension-ID grouping / 自适应维度编码**: repeated coordinates are grouped with compact temporary integer IDs; sparse mostly-unique workloads retain their existing path. Original full-coordinate sorting and monetary semantics are preserved. / 对重复坐标使用临时整数 ID，对大部分唯一坐标保留原路径，不更改金额、证据和输出顺序。
- **Performance profiling / CPU、内存与磁盘剖析**: `AllocationCore.exe profile RULE.json OUTPUT_DIR --workers 4 --disk-partitions 8 --evidence-gzip` writes local CPU/heap profiles and measurements alongside normal verified outputs. / CPU、Go 堆内存和输出体积统计均本地保存。
- **Unified read-only data mapping / 统一只读数据映射**: `AllocationCore.exe data csv --config examples/import_mapping.json output/normalized.csv` is ready to use. The Cube connector remains vendor-supplied; the native MySQL driver is linked only when built with `-tags mysql` (see V6 section below). / CSV 可直接使用；Cube 保留厂商无关适配接口，MySQL 的实际驱动需要使用 `-tags mysql` 重新构建。

See [`docs/INPUT.md`](docs/INPUT.md) and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md). / 详细操作和架构说明见这两份中英双语文档。

## New in Clean Core V4 / V4 新功能

**Reference reconciliation / 参考系统逐坐标验收.** After an allocation has passed conservation checks, import a privately stored `reference.csv` into the same project. The workspace now compares **every full business coordinate** with the authoritative `results.csv`: matching amounts, differing amounts, missing coordinates and extra coordinates are counted separately. Two files with the same grand total can still **FAIL**. Use the reference-check card on the Multi page, or run:

**参考结果验收：** 完成并通过分摊后，把独立参考系统的 `reference.csv` 放进对应项目目录。在“多科目分摊”页面点击“检查选中任务与参考结果”，即可按**全部业务维度成员坐标**核对金额，不会将总额守恒误判成分摊准确。也可在命令行执行：

```bash
AllocationCore reconcile --actual output/run/results.csv --reference input/reference/reference.csv --out output/acceptance-01 --precision 2 --tolerance 0
```

The reference file needs all **same business coordinate names** (column order may differ), and exactly one `amount` or `value` column. The engine ignores only its generated provenance fields `row_id` and `terminal_stage`; it does not infer mappings or discard any other dimension. Duplicate full coordinates are aggregated exactly. Large comparisons use 32 local temporary partitions and fixed-point integer amounts; no arbitrary result data is uploaded. Results are `summary.json` and `differences.csv` in a **new** private directory. Exit status 0=matched, 3=differences, 1/2=input error. The matching verdict is limited to the chosen snapshots and configured precision/tolerance.

参考 CSV 需具备完全相同名称的**业务维度列**（可不同顺序），以及唯一的 `amount` 或 `value` 金额列；仅忽略 `row_id`、`terminal_stage` 两个引擎元数据字段。相同完整坐标会精确汇总，绝不会自动忽略公司专属维度。分区临时文件和验收结果均保留在本机私有目录，成功后输出 `summary.json` 和 `differences.csv`。只有 MATCH 才返回退出码 0；存在差异返回 3；输入错误返回 1/2。**与参考文件一致仍不等于参考规则本身正确。**

**Safer cache sharing / 更充分但受控的 Driver 索引复用.** Cache identity now depends on source bytes, weight field, Driver filters, target constraints, dimension schema and hierarchies, not on allocation labels or shares that are used *after* lookup. Changed input or filtering always invalidates the key. Core and Multi summaries expose `driver_cache_hits` / `driver_cache_builds` only in disk-index/cache mode. / 共享仅发生在索引内容真正一致的规则之间，金额算法和负 Driver 处理策略不变。

## New in Clean Core V2 / 本轮更新

- **Streaming input inspection / 流式输入体检**: `AllocationCore.exe inspect RULE.json` checks actual CSV rows, generic dimensions, decimal validity and the weight columns referenced by the rule. It prints source sums and counts without loading a full CSV into memory. / 运行前先扫描源数据，检查字段、数值、坐标，统计金额与因子记录数；无需把整个 CSV 加载到内存。
- **Machine-readable inspection / 机器可读报告**: `AllocationCore.exe inspect RULE.json --json`. Invalid input returns a non-zero exit code; no output is published. / 可给 Java、Python 或 CI 读取，检查失败不发布分摊结果。
- **Persistent pool workers / 可复用分摊池工作线程**: worker goroutines are reused within a stage, but batch memory and deterministic output order are unchanged. / 单阶段内重复复用计算协程，保留有界内存和稳定结果顺序。
- **No new executables / 不增加程序**: still a single Windows EXE, one Go module and four bilingual Markdown documents. / 仍然只有一个 Windows 程序。

**Inspection PASS is not allocation PASS. / 输入体检通过不等于分摊正确。** Driver eligibility and conservation still require an actual engine run; equivalence to another system requires a full-coordinate golden comparison. / 因子匹配与金额守恒必须实际运行；与原系统一致性还要对完整业务坐标。

## Input and rule quickstart / 数据与规则快速接入

The engine does not connect to a company database or understand company account names. / 引擎不直接认识企业数据库、专用账户或业务口径。

| Local folder / 本地目录 | Content / 内容 |
|---|---|
| `input/amounts/` | Sparse amount CSV / 稀疏金额表 |
| `input/drivers/` | Sparse driver CSV / 稀疏分摊因子 |
| `input/rules/` | Generic allocation rules JSON / 通用规则 |
| `input/mapping/` | Source-specific adapter mapping / 来源适配配置 |
| `input/reference/` | External system's reference output / 原系统参照结果 |

Detailed generic CSV and JSON fields: **[docs/INPUT.md](docs/INPUT.md)**. / 具体格式见输入指南。

After preparing a rule whose dataset paths are relative to the rule file: / 准备好规则文件及其相对路径引用的数据后：

```bash
AllocationCore.exe validate input/rules/rule.json
AllocationCore.exe inspect input/rules/rule.json
AllocationCore.exe run input/rules/rule.json output/my-run --workers 4 --evidence-gzip
```

Results / 结果：`results.csv`, `reconciliation.csv`, `run_summary.json`, optional evidence and trace files. / 输出包含结果、逐池对账、任务摘要、可选审计文件。

To generate examples locally for CLI tests: / 生成纯合成演示数据：

```bash
AllocationCore.exe demo .
AllocationCore.exe demo . 1000
AllocationCore.exe multi -project testdata/scale_1000 -out output/scale-test -engine AllocationCore.exe
```

The second command generates 1,000 **distinct** synthetic rule templates on demand; no 1,000-file template bundle is shipped. / 第二条命令可按需生成 1000 套不同规则，不预装上千个 JSON。

## What is actually unified / 真正整合的内容

- One Go module with packages under `internal/`. / 单一模块。
- Embedded Studio UI and Cube Explorer **in the same HTTP service**, with no Explorer helper executable. / 工作台与 Cube 对比在同一个 HTTP 服务。
- Long-running cancellable runs use **the same executable as a worker process**, not a separate app package or unpacked EXE. / 长任务仍以同一个程序启动可取消子任务，保留失败隔离。
- Exact integer money, deterministic largest-remainder rounding, stages, multiple drivers, disk indexing, per-pool conservation, evidence and batch vault remain available. / 精确金额、确定性舍入、多阶段多因子、磁盘索引、守恒、审计和版本管理均保留。

## Documentation / 文档

- [Input contract / 数据接入](docs/INPUT.md)
- [Architecture / 系统架构](docs/ARCHITECTURE.md)
- [Testing and release / 验收与发布](docs/VALIDATION.md)

## Important limits / 重要边界

- Clean Core reorganization **does not prove better throughput**. Benchmark on the same machine and workload before quoting speedups. / 目录清理不代表吞吐已经提升，应做同机同规则基准测试。
- Sums matching alone **do not prove** the distribution across members is correct. / 总额守恒不代表分摊去向正确。
- Generic SQL import is supported through external adapters or CSV exports; **there is no bundled live MySQL connector**. / SQL 可通过外部适配层接入，目前未内置实时 MySQL 连接器。
- GitHub scanning and `.gitignore` are safeguards, not a company security or IP clearance. / 技术过滤不能替代保密与知识产权审核。
- Original repository history is not rewritten by this package. / 本包不会改写原 GitHub 历史。

## Clean V3 — fewer repeat calculations / V3：减少重复执行

- **Reusable worker mode / 计算进程复用**: for many different account rules, optional `"reuse_workers": true` in a private `project.json` keeps at most `jobs` cancellable worker subprocesses active, rather than starting a new process for each group. It uses the same `AllocationCore` binary, and a failed group still prevents batch publication. / 1000 科目不同规则时，复用固定数量的可取消子进程，任何一组失败仍不发布整批结果。
- **Shared effective rule preparation / 生效规则准备复用**: identical template bytes and identical overrides are parsed and validated once per batch. Different effective rules remain separate. / 相同模板与相同覆盖配置共用规则准备；不同规则仍独立执行。
- **One-click project preflight / 一键数据体检**: on the multi-account page, choose a project and select **检查全部科目和因子 / Inspect** before clicking Run. The preflight scans amounts once and each distinct driver file once, validating all referenced weight columns. / 页面可检查科目覆盖、金额、实际 Driver 字段与数值；同一 Driver 文件不会因被多个科目引用而重复体检。

CLI / 命令行：

```bash
AllocationCore.exe multi inspect -project PATH_TO_PROJECT -json
AllocationCore.exe multi -project PATH_TO_PROJECT -out NEW_OUTPUT_PATH -engine AllocationCore.exe
```

**Important / 注意：** preflight PASS covers input shape and numeric validity only. It does not prove driver eligibility, exact allocation correctness, conservation or reference-system equivalence. Run the authoritative engine, inspect `run_summary.json`, then compare complete business coordinates. / 体检通过不代表金额计算已经通过，更不代表与外部系统逐坐标一致。

**Performance boundaries / 性能边界：** pooled workers target many *small* rule groups; they are optional and should be benchmarked under realistic workloads. For large groups, memory pressure can differ. Worker cancellation kills the responsible subprocess and leaves no published batch results. / 常驻进程适用于大量小规则组；复杂高内存负载需要另行压测。取消会终止子进程，不允许发布不完整结果。

## Clean V6 MySQL / MySQL 只读接入

**中文**：新增原生 MySQL 只读接入源码、Keyset 分页、同事务多表快照、精确 DECIMAL 校验与失败不发布。MySQL 官方驱动需在线构建：`go build -tags mysql -o AllocationCore.exe .`；本离线交付的预编译程序不包含外部驱动，**不能据此宣称 MySQL 已在本机生产连接成功**。配置模板见 `examples/mysql_mapping.example.json` 和 `examples/mysql_bundle.example.json`。凭据只通过环境注入，必须使用独立只读账号和验证 TLS。参见 `docs/INPUT.md`。

**English**: Native MySQL source implements verified TLS, read-only REPEATABLE READ, strict signed-BIGINT keyset pagination, exact DECIMAL streaming, and multi-table atomic snapshot bundles. Build with the official driver and `-tags mysql` on a network-connected developer machine or use the MySQL-enabled Windows artifact generated by CI. The included offline-build executable is **not** a MySQL-enabled binary. Live acceptance on the target company's DB remains required.
