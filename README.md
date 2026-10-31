# Allocation Core — Business Onboarding Update / 通用分摊引擎

## Finance-friendly percentage factors / 财务人员百分比因子配置

In /multi → 科目规则 → 独立因子配置, enter 75% / 25% instead of fractional JSON values. Shares are checked exactly to 0.0001% precision and must sum to 100% for each active stage.

Go validates the entire effective rule before writing overrides.csv; stale pages return HTTP 409 and cannot overwrite changed account assignments, templates, or factor overrides. Saved rules still require Inspect, Run and independent coordinate-level reconciliation.

Compatibility: external API clients calling POST /api/multi/factor must provide the versions object (SHA-256 for assignments, template and overrides) returned from GET /api/multi/factor. Missing/stale tokens are rejected.

一个 EXE、一个工作台，不改变现有金额计算算法；保存成功不等于业务验收。

**One Go module · One Windows executable · One workspace.**
**一个 Go 模块、一个 Windows 程序、一个业务工作台。**

## 财务人员快速配置 / Finance-first quick setup

在现有 **/multi** 工作台顶部使用「快速配置」：选择项目，搜索科目，再选择分摊模板。系统从经过校验的真实规则自动展示分摊阶段、Driver、精确比例、目标维度和负权重风险；不读取金额明细，也不推测业务规则。选择模板后先形成未保存草稿；保存时沿用现有 SHA-256 并发保护。批量指定必须先输入筛选词并二次确认，避免误改全项目。保存后必须按 **体检 → 实际分摊 → 独立参考结果逐坐标对账** 执行，规则说明和体检通过均不代表业务计算正确。

The compact quick-start reuses the same multi-account rule engine and guarded assignment API. It is a read-only template explanation followed by explicit draft / save / inspect steps. It adds no calculation logic, no user data to Git, no additional executable and no documentation file. Existing advanced Designer, mapping, auditing and batch release remain available below.

This repository contains **no company data or proprietary allocation rules**. Inputs, outputs, mappings and runtime records stay outside version control. / 本仓库不包含企业真实数据和私有规则；输入、输出、映射与运行状态默认不纳入 Git。

> **MySQL 注意 / IMPORTANT**：本 ZIP 内附的离线 Windows EXE 保留全部已有功能，但**不包含官方 MySQL 驱动**。需要使用在线构建的 MySQL-enabled EXE（GitHub Actions）或 `go mod download && go build -tags mysql` 才能执行 `data mysql` / `mysql-bundle`。源码已实现，模拟事务测试通过；**目标企业 MySQL 实库联调仍待执行**。

## Financial semantics and adversarial verification / 财务语义与对抗验证

- **Rounding policy / 尾差政策:** Optional `"rounding_policy":"LARGEST_REMAINDER_IDENTITY"`. Omitting it preserves the historical integer allocation and output bytes; ties follow stable target identity, not CSV order. Other policies are rejected until independently validated. / 缺省维持旧算法，不会默默重算历史结果。
- **Signed drivers / 负因子准入:** Default remains REJECT. `"negative_driver_policy":"ALLOW_SIGNED"` now requires an explicit `"signed_driver_reason":"<specific finance purpose>"` per allocation (8..256 characters, one line), recorded in rule SHA-256 and displayed in Multi effective rule explanations. Signed weights may produce negative or above-source target amounts. **Migration:** existing custom ALLOW_SIGNED rules without a reason must be updated to run. This is a business-intent record, **not an approval or identity-control system**. / 负权重需要明确业务用途，旧配置须补字段，不冒充真正审批。
- **Exact decimal input / 数值契约:** Allowed scale 0..6; maximum 128 text bytes after trimming surrounding spaces, no silent rounding or truncation. Existing accepted syntax (`1.`, `+1.23`, outer spaces) is preserved. Signed int64 limits and malformed inputs have boundary tests and a Go native fuzz target. / 固定精度且拒绝超长输入，保持既有格式兼容。
- **Multi-stage independent oracle / 多阶段独立对账:** Tests cover +101/-101 minimum money units, three non-divisible stages and 27 leaf coordinates, including a strict per-coordinate drift bound **only for the tested nonnegative single-path graph**, and memory/disk parity. This is not a universal bound for merged or signed DAGs; enterprise reference acceptance is still required. / 三阶段测试不能替代企业真实业务对账。
- **Late-skew disk guard / 延迟重复坐标:** Intermediate shards >=16 MiB use streaming coalescing rather than relying on the first 256-row sample. Smaller shards retain the prior heuristic. Streaming memory still depends on coordinate cardinality; **not fixed RAM**. Test checks exact output/lineage parity. / 大分区规避首段采样误判，但没有宣称恒定内存。

## Business onboarding: a visible product improvement / 新版：从金额 CSV 自动创建业务项目

**This iteration intentionally stops chasing microbenchmarks.** It makes the existing multi-account engine easier for non-developers to use without modifying its financial formulas. / **这一轮停止追逐局部微基准**，优先完成业务人员能实际使用的数据接入流程。

1. Run `AllocationCore.exe` and open **Workbench → 规则与执行 / Run**. / 双击程序，打开工作台的“规则与执行”。
2. Create a synthetic template project once, or select a previously configured project as the template source. / 先建立样例模板项目，或选择已经配置过规则的项目。
3. In **从自己的金额 CSV 创建项目 / Import wizard**, type a **new** project name and select the initial rule. The page shows the exact required CSV columns and offers a downloadable header. / 在“从自己的金额 CSV 创建项目”中输入**新项目名称**，选择初始规则，可直接下载必需 CSV 表头。
4. Upload an amount CSV (**up to 256 MiB**, streamed, unlike the separate legacy 5 MiB JSON uploader). The engine verifies dimensions and fixed-point decimals, identifies distinct `account` values, and generates `assignments.csv` for every account. All records are saved in a NEW project; existing projects and published results are never replaced. / 上传金额 CSV，程序流式验证维度与金额、自动识别科目、为每个科目生成规则分派；仅创建新项目，不覆盖原项目或已发布结果。
5. Review and adjust individual account rules, **replace the synthetic/copied Driver datasets with real data**, then run **检查全部科目和因子 / Inspect → 一键执行 → 参考结果逐坐标对账**. / 导入完成后，再逐科目配置规则、替换真实分摊因子，执行体检、分摊与逐坐标验收。

**Required columns** depend on the chosen rule template, e.g. `period,department,sku,account,currency,amount`. Optional extra columns such as `row_id` are preserved. The CSV header may have an UTF-8 BOM; column order does not matter. / 必须字段由选择的规则决定，允许 UTF-8 BOM、附加列与不同列顺序。

**Boundaries:** This onboarding is for CSV input, not Excel `.xlsx`, automatic business-rule inference, a live MySQL connection, or a guarantee of correct financial allocations. Import PASS only verifies input format; business validation still requires real Drivers, eligibility checks, and independent reference reconciliation. For data larger than 256 MiB, use the local project folder. / 注意：此向导不自动识别企业分摊政策，不支持直接上传 XLSX，也不等于业务结果正确；超大文件可放入本地项目目录。

## Business onboarding: reusable mapping profiles / 跨场景字段映射配置复用

The unified /multi workspace can now save and reuse **amount** and **Driver** CSV column mappings across projects with matching dimensions, precision and required weight fields. Saved profiles contain only explicit source column names and the canonical schema: **no business values, source CSV data, credentials or project paths**. Profiles live under the private local data directory `mapping-profiles/`, outside Git and outside published results.

- Select a compatible profile to restore field mappings before importing a new file; the real CSV header and every row are still checked.
- Explicitly export a small JSON profile to move it to another machine; importing a profile never imports financial data.
- Existing names cannot be overwritten. Broken, ambiguous or incompatible mappings fail closed.
- Always run Inspect, actual Core allocation and independent coordinate-level reconciliation. A mapping profile is not a claim of financial correctness.

金额与 Driver 的字段映射可跨项目/版本复用，同名拒绝覆盖；导出 JSON 只包含字段关系，可能含企业字段名，分享前请检查。正式分摊仍须完成数据体检、运行和独立对账。

## Unified visual business-rule workflow / 可视化规则直接进入多科目分摊

No new editor, executable, or documentation tree is needed. / 不新增设计器、不新增 EXE、不扩张文档体系。

1. Double-click **AllocationCore.exe**. From **/designer**, clone or open a saved rule and visually configure one or more stages, inputs, driver shares, target dimensions and conservation keys. For multi-account use, **account must be an immutable dimension, AS_IS/SOURCE and a conservation key in every stage**. / 在规则设计器中配置多阶段、因子、目标维度；多科目规则必须始终保持 account 不变并守恒。
2. Open **/multi** and choose an existing business project. Under **可视化设计规则 → 多科目模板**, refresh designer cases and choose the saved rule. Enter a new template filename. / 进入多科目工作台，选中当前项目，选择设计器保存的规则，输入新模板文件名。
3. Click **检查规则与 Driver 映射**. The server checks the existing project's dimension order/precision and the rule's full graph. Map driver dataset aliases to project-local `drivers/*.csv`. Missing files or columns are shown, **not** invented or copied from the designer. / 预览检查维度、精度、守恒、阶段依赖与 Driver 列头，缺失的真实 Driver 需自行补齐。
4. Click **创建新规则模板**, then explicitly choose the new template for the required accounts and save. Imports never replace an existing template, amount CSV, assignment or published run. / 导入仅创建独立新规则，不会自动改任何科目。
5. **Inspect → Run → Compare to independent reference**. Preflight PASS or driver-header readiness is not a valid financial acceptance by itself. / 完成完整体检、执行与独立参考结果逐坐标验收。

The same workspace also exports/imports all account-to-rule mappings as Excel-compatible CSV drafts and shows searchable, downloadable per-account run summaries. / 科目规则分派可通过 Excel 兼容 CSV 批量调整；逐科目运行结果可搜索、筛选、导出。

**CI evidence:** [Windows + Go + MySQL + TLS, all PASS (2026-10-31)](https://github.com/realYurk/Allocation-Core/actions/runs/41157325587). This covers synthetic integration tests only; enterprise rules and production data still require business acceptance. / 全部 CI 已通过，但企业真实数据与业务规则仍需单独验收。

## Clean V10: Skewed coordinate coalescing & verified CI readiness / V10 偏斜坐标合并及 CI 就绪校验

**What changed / 本轮改进**

- **Low-cardinality disk merge / 低基数磁盘合并**: MERGE stages keep just one aggregate row per complete business coordinate instead of buffering an entire input shard in `[]Row`. Large nonterminal-stage shards use the same streaming approach **only** when they exceed 2 MiB and the 256-row sample shows >=32 repeated coordinates. Mostly-unique rows keep the existing single-pass fast path. / MERGE 阶段只保存每个完整业务坐标的一条累计记录；大型且坐标高度重复的非终结阶段合并也采用流式算法，稀疏高基数场景维持旧路径。
- **Lineage stays exact / 血缘证据不变**: For coalesced rows the target identity is always the first source row's ID. We can therefore write `COALESCE` edges in original input order while streaming, without buffering every source row. Negative amounts and signed overflow checks are unchanged. / 在首次出现完整坐标时锁定合并后的目标行 ID，按原始顺序输出血缘；金额精度、负数及溢出保护不变。
- **Cross-version replay / 旧版检查点恢复**: A V9 disk checkpoint was resumed with V10, and the five published financial CSV and compressed evidence files were byte identical. / V9 检查点由 V10 恢复，财务结果与压缩证据逐字节一致。
- **MySQL TLS CI readiness / TLS 集成测试就绪门槛**: V9's latest online run showed successful Linux/Windows/normal MySQL jobs, but the TLS job failed **before import**, at container initialization, because `mysqladmin ping` did not guarantee authenticated readiness. V10 waits until a real authenticated `SELECT 1` succeeds. **Historical note:** the initial V10 readiness change alone did not resolve MySQL 8.4's removed `have_ssl` variable; the current CI verifies a certificate-checked TCP/TLS handshake and passes. / 历史记录：V10 的就绪检查尚不足以修复 MySQL 8.4 的旧变量问题；当前 CI 已通过真实 TLS 握手验证。

**Performance evidence / 性能边界**: In a synthetic 30,000-row/100-coordinate coalesce microbenchmark, cumulative allocations fell from about 28 MB to 21 MB. However, a 250,000-row PASS→MERGE end-to-end case showed similar elapsed time and process RSS to V9; **do not claim overall throughput or peak-RSS improvement**. The optimization bounds retained rows by distinct coordinate count, not by a fixed memory quota. / 局部减分配已验证，完整计算未证明显著提速，也不能保证高基数时内存固定。

**Use / 使用**: the existing `AllocationCore.exe` entrypoint and `--disk-partitions` option work unchanged. No new program, configuration key or business rule is required. / 使用方式不变，不新增程序或配置。

## Clean V9: CI reliability and bounded disk sharding / V9 CI 修复与分区内存优化

- **Fix GitHub Linux CI / 修复 Linux CI**: Create `output/` before redirecting benchmark output. Prior GitHub test results showed the financial/race tests had passed; the job only failed at the benchmark redirect (`No such file or directory`). / 在写 `output/ci-benchmark.txt` 前创建目录，防止 CI 假失败。
- **MySQL TLS read-only integration / 严格 TLS 只读验收**: The isolated MySQL test database and `REQUIRE SSL` SELECT-only account are now provisioned through the container-local administrative socket. The Go integration test verifies **the real production TLS connector**, a non-empty server-side TLS cipher, a rejected `UPDATE`, exact DECIMAL import and wrong-SAN rejection. The current CI requires secure transport and confirms a real SELECT-only TCP session with verified CA/identity and a negotiated TLS cipher; the job passed on GitHub. / CI 已验证强制 TLS、只读账号、证书身份与加密协商，相关 GitHub 测试通过。
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

**What is NOT proven / 尚未完成的验收**: enterprise-specific MySQL credentials and permissions, representative million-row business-reference reconciliation and Windows target-device UAT. The isolated official-driver TLS/SAN and concurrent DDL CI gates have passed, but they are not a substitute for enterprise acceptance. Offline EXE builds without `-tags mysql` do not include the native driver. / 已通过隔离 TLS/SAN 和并发 DDL 集成测试；仍需目标企业真实数据、权限与现场 Windows 验收。

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
- **Live MySQL fault gates / 实库故障门槛**: CI now defines an **additional** ephemeral TLS-required MySQL 8.4 job with a temporary CA/SAN certificate and a negative wrong-hostname test; its standard MySQL job also tests blocking a concurrent DDL until the read-only snapshot is released. These isolated jobs have passed on GitHub; real enterprise connectivity remains unverified. / 隔离集成测试已在 GitHub 通过，目标企业实库连接仍未验收。
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
