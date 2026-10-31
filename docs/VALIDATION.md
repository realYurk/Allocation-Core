# Testing, Release and Security / 验收、发布与安全

## Clean checkout / 干净检出

```bash
go build -o AllocationCore .
./AllocationCore demo .
go test ./...
go vet ./...
./AllocationCore run examples/manufacturing/rule.json output/demo --workers 4
./AllocationCore multi -project testdata/multi_sample -out output/multi --engine ./AllocationCore
```

Use `.exe` on Windows. Generated CSV files are local and ignored by Git. / Windows 使用 `.exe`；演示 CSV 仅本机生成、Git 不跟踪。

## Golden validation / 计算一致性

The old authoritative Core and the new Clean Core were run on exactly the same synthetic `manufacturing` input and rule. Five outputs matched **byte for byte**: `results.csv`, `reconciliation.csv`, `ledger.csv`, `source_to_pool.csv`, `lineage_links.csv`. / 用相同合成输入独立运行旧引擎与 Clean Core，上述五份文件逐字节一致。

New edge case: `Apportion(math.MinInt64, [1], ["X"])` now correctly returns the unchanged amount. / 极端负金额溢出边界新增回归。

1000-account synthetic batch: 1000 different share configurations, PASS, 1000000.00 input and output, 4000 terminal rows, ~7.23 s in the current Linux test container (small synthetic data; not large-data performance evidence). / 千科目小样例通过，不可等同于千万行压测。

## Release / 发行

The unified distribution contains one Windows executable and complete Go source. All generated account rules, private CSV, run outputs and archives are excluded. Build artifact checks include SHA-256 and ZIP CRC. / 统一包只有一个 Windows EXE 及全部源码；过滤真实数据和运行产物，进行哈希与 ZIP 校验。

Windows binaries are cross-compiled and structurally inspected in Linux; Windows application policy, firewall and real double-click behavior **must be verified on an actual Windows machine**. / Windows 交叉编译不能代替实际电脑验收。

## Outstanding production gates / 未关闭的生产验收

- Real business golden comparison for every rule and target coordinate. / 每套真实规则逐坐标验收。
- Windows smoke test on target machine and CI runner. / Windows 实机/CI。
- Multi-million and hundred-million row benchmarks with evidence and disk quotas. / 百万至亿级容量及性能压测。
- Controlled interruption and recovery of expensive runs, retention/GC and multi-instance safety. / 中断恢复、归档清理、多实例安全。
- Native MySQL connector exists but full verified-TLS and enterprise UAT remain open; Cube vendor connector not implemented. / MySQL 源码已有，真实证书全链路与企业 UAT 仍需验证；Cube 厂商连接器尚未实现。

## Publishing safety / 公开安全

The public source does not bundle proprietary business logic. `.gitignore` keeps private input and outputs out of Git by default, but does **not** erase an earlier repository's history or replace legal/IT approval. Do not commit real account codes, rule tables, credentials, exported data, or reference output. / 默认不提交私有数据，公开前仍必须审查原仓库历史、授权及内部知识产权要求。

## Clean Core V2 acceptance / V2 验收补充

Use the **same data**, settings and hardware to compare before/after. / 所有性能数据必须在相同输入、硬件和选项下比较。

```bash
AllocationCore.exe demo .
AllocationCore.exe inspect examples/manufacturing/rule.json
AllocationCore.exe inspect examples/manufacturing/rule.json --json
AllocationCore.exe run examples/manufacturing/rule.json output/check-v2 --workers 4 --evidence-gzip
go test ./...
go vet ./...
go test -race ./internal/engine ./internal/multi ./internal/studio ./internal/explorer
go test ./internal/engine -run '^$' -bench BenchmarkPoolScheduler -benchmem
```

**Measured in the development Linux container (not Windows): / 开发容器实测（不是 Windows 实机）**

| Test / 测试 | Old Clean / 原版 | V2 / 新版 |
|---|---:|---:|
| Pool scheduler, 4 workers, 4096 synthetic pools, median / 微基准中位数 | 1.94 ms/op | 0.79 ms/op |
| Allocations in same microbenchmark / 单轮分配次数 | 3584 | 1031 |
| 1000-account synthetic end-to-end / 千科目合成计算单次运行 | 7.81 s | 7.31 s |
| Actual output matching / 完整业务结果 | identical 4000 records / 4000 条一致 | identical / 一致 |
| Inspect 1,000,000 synthetic source records / 流式扫描百万记录 | not available / 无 | 0.71 s, ~14.6 MB process peak RSS |

The microbenchmark **only** measures pool scheduling overhead; it cannot be extrapolated as an end-to-end speedup. Single end-to-end timings are subject to noise. The inspection benchmark does not include allocation execution. / 微基准只测调度器，不能换算为整体分摊提速；单次端到端结果有随机波动；输入扫描不等于执行分摊。

**Byte-for-byte evidence checks / 证据字节级一致：** identical `results.csv`, `reconciliation.csv`, `ledger.csv.gz`, `source_to_pool.csv.gz`, `lineage_links.csv.gz`, `evidence_stream_manifest.json` for the same two-stage rule and inputs. Run summaries contain timestamps and are compared by business fields. / 对同一两阶段样例，上述六个结果与证据文件字节级一致；任务摘要的时间字段不参加字节比较。

**Outstanding / 仍待完成:** realistic million-row and hundred-million-row allocation benchmarks, Java/Python golden cross-language tests, Windows hardware validation and native database connectors. / 真实复杂业务百万至亿级压力测试、跨语言黄金对账、Windows 实机与原生数据库接入仍需单独验收。

## Clean V3 verification / V3 回归验证

1. `go test ./... && go vet ./...` / 全量测试和静态检查。
2. `go test -race ./internal/multi ./internal/engine ./internal/studio` / 关键执行模块竞态检测。
3. Create synthetic data: `AllocationCore demo . 1000`; then run `AllocationCore multi inspect -project testdata/scale_1000 -json`. / 本地按需生成千科目合成样例。
4. Run the **same physical project directory** with `reuse_workers` off and on, using fresh output destinations. Compare `results.csv` and `account_reconciliation.csv` byte-for-byte. / 对同一项目比较逐坐标金额及逐科目对账，要求完全一致。
5. For a failed driver/invalid rule or canceled task, assert no published output directory. / 失败或取消绝不能发布正式结果。

Timings must include the machine, workload, cache settings and evidence configuration. A 1000-group tiny synthetic benchmark does not predict multi-million-row production speed. / 必须注明机器、数据和证据选项；小数据量 1000 组规则结果不能外推为生产吞吐。


## Clean V4 verification / V4 验收

- New `internal/acceptance` tests check shuffled header order, duplicate-coordinate aggregation, offsetting-but-wrong amounts, missing/extra coordinates, failed-input nonpublication, tolerance, negatives and cancellation. / 新增逐坐标验收测试覆盖错配抵消、缺失/多余、负数、容差和失败不发布。
- New HTTP integration test verifies that a successful job can be checked against its own project's private `reference.csv`, while cross-project use and symlinked reference inputs are blocked. / 新增接口测试覆盖正确使用、跨项目隔离和符号链接阻断。
- Driver cache keys exclude allocation labels and shares that do not affect the stored index; data bytes and filtering changes always invalidate the index. `driver_cache_hits` and `driver_cache_builds` are reported when persistent disk indices are used. / 索引键进一步收敛到真正影响数据的语义；金额分摊语义保持不变。

```bash
go test ./...
go vet ./...
go test -race ./internal/acceptance ./internal/engine ./internal/multi ./internal/studio
AllocationCore reconcile --actual output/example/results.csv --reference input/reference/reference.csv --out output/acceptance-new --precision 2 --tolerance 0
```

**Performance boundary / 性能边界：** The 32-partition comparison bounds aggregation to the largest partition but still uses disk proportional to the inputs; extremely skewed data can create an oversized partition. Initial cache hash verification still re-reads the source data. Neither feature guarantees a fixed speedup. / 对账采用分区聚合，但临时磁盘占用随输入增长，极端倾斜也可能造成分区内存峰值过高；Driver 缓存仍需重新校验源文件哈希，不能承诺固定提速。

## Clean V5 reproducible profiling and source adapters / V5 性能与适配器验收

From a clean checkout / 从全新检出开始：

```bash
go build -o AllocationCore .
./AllocationCore demo .
go test ./...
go vet ./...
go test -race ./internal/engine ./adapter ./internal/profiling
./AllocationCore data csv --config examples/import_mapping.json output/normalized.csv
./AllocationCore profile examples/manufacturing/rule.json output/manufacturing-profile --workers 4 --disk-partitions 8 --evidence-gzip
```

All profiling artifacts, normalized CSV and generated fixtures must remain local. / 全部 Profile、生成 CSV 和真实适配配置均不得提交 GitHub。

Golden regression / 黄金结果回归：same input and V4 rule must produce byte-identical `results.csv`, `reconciliation.csv`, `ledger`, `source_to_pool`, `lineage_links`, and `evidence_stream_manifest` in both memory and disk modes. Local CPU benchmarks are not proof of production throughput. / 同数据旧版与新版需在内存及磁盘模式下逐文件一致，微基准不能代表真实生产吞吐。

### Measured synthetic V5 observations / 本轮合成实测结果

**Environment / 环境:** Linux x64 / AMD EPYC VM; NOT a Windows production workload. / 不是 Windows 生产环境成绩。

- **50,000 source rows, two-stage disk executor (`--disk-partitions 8`, gzip evidence, CPU profiling active):** ~904 ms measured wall time; sampled peak `HeapAlloc` ~11.27 MB; total Go heap allocated ~252.17 MB; persisted output ~301 KB; `/proc/self/io` write-byte delta ~20.96 MB. The 20.96 MB includes temporary spool and profile writes, **not** just the published results. / 5 万行两阶段磁盘计算开启 CPU Profile，耗时约 904ms、采样堆峰值约 11.27MB、累计 Go 堆分配约 252MB。最终文件 301KB，但进程写盘约 20.96MB，包含临时文件与 Profile；不能当成单次业务结果体积。
- **CPU profile:** `encoding/json` spool decoding and temporary Go object allocation were prominent; `encoding/json.(*decodeState).object` accumulated approximately 330ms of 930ms sampled CPU. This motivates **future spool serialization work**, not an untested format change in V5. / 证据显示 JSON 中间数据解码和 GC 是值得继续优化的热点，这轮不冒险改掉检查点文件格式。
- **20k rows / 100 unique coordinates**, synthetic grouping microbenchmark: legacy ~9.09ms / 4.02MB allocated vs adaptive packed grouping ~4.30ms / 0.60MB allocated; **20k mostly unique coordinates**: packed path not chosen and adaptive ~16.4ms vs legacy ~16.0ms in single runs. / 高重复度坐标局部优化效果明显，稀疏唯一场景无总体提速保证。
- **1,000 accounts, 1,000 different rules:** V4 and V5 full `results.csv` and account reconciliation SHA-256 identical. One unprofiled run V4 ~5.90s vs V5 ~6.22s on the same host, **no end-to-end speedup demonstrated**. / 千科目结果完全一致，但本轮单次耗时没有取得整体提升，不能夸大。
- **Byte-for-byte regression** against V4 for synthetic manufacturing with memory/disk mode and compressed evidence: complete final outputs, ledger, lineage, membership and manifest agree. / 两种执行模式的金额、审计和血缘文件与 V4 完全一致。

## Clean V6 native SQL connector test gate / MySQL 生产验收门槛

- **Local offline tests / 离线**: `go test ./... && go vet ./...`; simulated `database/sql` driver tests verify `REPEATABLE READ + ReadOnly` transaction, cursor progression, one snapshot for two files, exact DECIMAL, SQL identifier safety, atomic failure and rollback. / 离线驱动模拟验证事务选项、分批、精度、失败隔离。
- **Online native build / 在线**: `go mod download` then `go build -tags mysql -o AllocationCore.exe .`; only this executable includes the official MySQL driver. / 只有该构建真正包含 MySQL 协议驱动。
- **Ephemeral real MySQL 8.4 / 隔离实库**: CI job with `mysql` + `mysql_integration` build tags tests InnoDB key metadata, cross-page consistent snapshot despite concurrent update, and multi-table bundle. CI must report PASS before production pilot. / GitHub CI 需在真实隔离 MySQL 中校验并发更新下的跨页快照。
- **Actual enterprise UAT / 企业预生产**: Verify SELECT-only credentials, TLS PKI and hostname, schema grants, InnoDB & unique BIGINT primary key, batch sizes, 1M–10M+ rows, loss/reconnection, disk capacity, server load and end-to-end results against independent reference coordinates. / 生产环境权限、证书、负载、回滚及逐坐标财务验收必须单独执行。
- **Prohibited claims / 禁止误报**: Mock tests do not establish connectivity. A read-only transaction is not proof that the login lacks DML rights. An import PASS is not an allocation or reference reconciliation PASS. / 模拟测试不等于实库连通，只读事务不等于账号最小权限，数据导出 PASS 不等于分摊正确。

## V7 Financial Correctness & Production Hardening / V7 财务正确性与生产加固

**Baseline / 版本基线**: latest audited public V6 source plus the Windows 8.3-path correction and pinned `go.sum`, followed by V7 changes. / 在 GitHub 公共 V6 最新基线上开发，保留短路径修复及官方依赖校验文件。

**Independent correctness / 独立准确性**:

```bash
go test ./internal/engine -run TestV7 -count=1 -v
go test ./adapter ./internal/acceptance -run TestV7 -count=1 -v
go test -race ./internal/engine ./internal/studio ./internal/multi ./adapter ./internal/acceptance
go vet ./...
```

- Static amount cases include exact cents, negative totals, signed/zero drivers, ties, `MinInt64` and `MaxInt64`. / 预设金额正确答案，包含极值、尾差等。
- A *separate* `big.Rat`-based oracle evaluates 5,000 deterministic random cases, order permutations, conservation and repeatability. / 独立有理数验算 5000 组。
- The two-stage example independently expects 100.00, 300.00, 400.00, 400.00 on four products. Memory(1), memory(4), and disk(4 partitions) results are byte-identical. / 多阶段样例手工验收，并行/磁盘完全一致。
- `ImportSQLBundle` preflight and data reading must happen in the same read-only REPEATABLE READ transaction. Injected validation failure, network interruption and transaction-release failure must publish **nothing**. / 同事务预检、断线及回滚错误不得发布。
- The isolated MySQL 8.4 CI test now reads through a SELECT-only account, and rejects non-exact measure columns. The native TLS+SAN test is opt-in when a purpose-issued certificate is supplied; **do not count it as passed without actual execution**. / 隔离 MySQL 最小权限实际读取；TLS 全链路另需证书验证。
- Duplicate full business coordinates are counted in default reconciliation, and strictly rejected when `--reject-duplicates` is supplied. / 记录重复行，严格模式失败关闭。

**Synthetic 50,000-row disk profile (Linux Go 1.23.2, current environment, CPU profiling ON) / 5 万行合成磁盘执行与剖析**:

| Configuration / 配置 | V6 baseline | V7 | V7 plain evidence |
|---|---:|---:|---:|
| Engine elapsed / 引擎耗时 | 1.087 s | 0.946 s | 0.807 s |
| Sampled Go heap peak / 采样堆峰值 | 11.26 MB | 11.06 MB | 11.03 MB |
| Allocated Go heap / 累计堆分配 | 252.19 MB | 252.16 MB | 248.27 MB |
| Persisted files / 最终输出体积 | 301,425 B | 301,425 B | 7,993,229 B |
| Linux process write_bytes / 进程写入计数 | 20.94 MB | 20.94 MB | 28.63 MB |

All five V6/V7 financial and gzip evidence files match byte-for-byte. / 5 份财务数据及压缩证据逐字节一致。

**Interpretation / 解释**: these are single short runs, NOT stable speedup estimates. The plain-evidence mode produces much larger output but avoided gzip CPU cost on this sample. CPU samples continue to implicate JSON spool decoding and temporary allocations; replacing checkpoint storage is deferred until compatibility and failover can be proven. / 单次短测不能证明整体提速。证据压缩显著减小磁盘体积但消耗 CPU；JSON 解码仍有优化空间，暂不冒险改检查点格式。

**Pending / 待验收**: MySQL official-driver TLS/SAN end-to-end, simultaneous DDL, 10-million-row real reference correctness, actual Windows launch, power-loss/disk-full recovery, and enterprise-specific legal/IP approval. / 实库证书、并发 DDL、千万行参考验收、Windows 实机、断电与磁盘写满及知识产权审批仍未关闭。

**1,000-account V6/V7 gold run / 千科目新旧版本对跑（Linux Go 1.23.2，纯合成）**:

| Metric / 指标 | V6 | V7 |
|---|---:|---:|
| Accounts and effective rule groups / 科目与规则组 | 1,000 / 1,000 | 1,000 / 1,000 |
| Terminal rows / 结果明细 | 4,000 | 4,000 |
| Input and output total / 前后金额 | 1,000,000.00 / 1,000,000.00 | 1,000,000.00 / 1,000,000.00 |
| Observed wall time / 单次墙钟耗时 | 4.46 s | 4.62 s |
| Results and account reconciliation SHA-256 / 结果与逐科目对账哈希 | **Identical / 一致** | **Identical / 一致** |

**Do not claim a performance speedup from V7 / V7 不宣传速度提升**: this release primarily strengthens correctness, fail-closed publication and type validation. / 本轮主要是正确性、异常阻断与数值类型安全的工程加固。


## V8 validation / V8 验收记录

**Baseline / 基线：** Clean V7 source from the previous signed-off release; `go run . demo .` generates only synthetic fixtures before tests. / V7 作为结果基线，先生成合成数据再运行完整测试。

**Local tested / 本地实际执行：**

```bash
go run . demo .
go test ./... -count=1
go vet ./...
go test -race ./internal/engine ./adapter ./internal/multi ./internal/studio ./internal/acceptance -count=1
go test ./internal/engine -run '^TestV8Spool' -count=1 -v
go test ./adapter -run '^TestV8MySQLTLS' -count=1 -v
go test ./internal/engine -run '^$' -bench '^BenchmarkV8PartitionSpool$' -benchmem -benchtime=300ms -count=3
```

- Temporary spool regression verifies exact original JSONL bytes, Unicode/escaped strings, oversized rows >256 KiB, corrupt and incomplete records. / 验证临时分区记录逐字节保持，含中文、超大坐标及破损文件拒绝。
- Offline TLS tests use ephemeral certificates and a real local TLS handshake. They are **not** a MySQL protocol test. / 离线 TLS 证书测试不是完整 MySQL 协议验收。
- 5,000 synthetic JSONL records (8 shards), same Linux VM: legacy medians around **15.3 ms/op**, V8 passthrough around **11.2 ms/op**, with ~115k vs ~100k allocations/op. These are local *partition-only* benchmarks, not overall allocation speed guarantees. / 局部分区微基准减少重复序列化；不可将比例外推为总体性能。
- Two-stage disk calculation on identical 50k synthetic source input, `--workers 4 --disk-partitions 8 --evidence-gzip`: V7 ~0.81 s, V8 ~0.76 s (single cold runs). `results.csv`, `reconciliation.csv`, `ledger.csv.gz`, `source_to_pool.csv.gz`, and `lineage_links.csv.gz` all have identical bytes. Timestamp-derived `run_summary.json` and the evidence manifest naturally differ. / 5 万行旧版与新版的五份财务输出和证据文件逐字节一致。
- 1,000 synthetic accounts and 1,000 effective rule groups: 4000 terminal rows; V7/V8 `results.csv` and `account_reconciliation.csv` are SHA-256-identical. One-run elapsed: ~4.84s vs ~4.50s on this host; NOT a controlled repeated throughput claim. / 千科目结果逐字节一致，单次耗时不构成稳定提速承诺。

**Still pending until GitHub Actions actually runs / 必须等待 CI 实际执行：**

- `mysql-integration`: official MySQL driver, SELECT-only reader, concurrent `ALTER TABLE` blocked until snapshot release. / 实库并发 DDL 元数据锁保护。
- `mysql-tls-integration`: CA-signed SAN server cert, enforced encrypted transport, official-driver import through a least-privilege user, rejection of incorrect server name and no output publication. / 真正 MySQL TLS/SAN 正反用例。
- Enterprise UAT: actual enterprise certificates, DBA permissions, 1M–10M+ row data, long-running snapshot undo pressure, power loss and disk full. / 企业实库、百万到千万行、undo 负载及断电/写满测试仍需专项验收。

## V9 verification scope / V9 验收边界

- **GitHub evidence / GitHub 失败证据:** V8 CI run `41151488612` had `test=FAIL` (shell redirect to missing `output/ci-benchmark.txt`) and `mysql-tls-integration=FAIL` (`unexpected EOF / invalid connection` in administrative initialization). The `windows` and `mysql-integration` jobs both passed. CI corrections in V9 are **not claimed PASS until a fresh GitHub run actually succeeds**.
- **Financial regression / 金额回归:** Use the exact same source CSVs, rule JSON, flags `--workers 4 --disk-partitions 64 --evidence-gzip` on V8 and V9; compare SHA-256 of `results.csv`, `reconciliation.csv`, `ledger.csv.gz`, `source_to_pool.csv.gz`, `lineage_links.csv.gz`. Timestamp-bearing run summaries are not expected to match. / 完整业务输出和证据必须保持字节一致。
- **Memory scope / 内存口径:** `shardBufferBytes(64)` uses 32 KiB per *active* writer; at most 64 active shards, 2 MiB total. Other process allocations and pool skew remain separate risks. Compare process peak RSS and elapsed time on identical machines, and repeat trials. / 并非整个程序峰值内存的硬上限。
- **MySQL integration / 实库门槛:** Only the ephemeral container creates test schema/user; connector tests authenticate a SELECT-only account via purpose-issued CA/SAN TLS and must reject wrong hostname and DML writes. `have_ssl=YES` and `require_secure_transport=ON` are hard gates. If the TLS job still fails, inspect Docker logs and client handshake; do not weaken certificate verification. / TLS 失败不可通过禁用校验绕过；目标公司 UAT 仍未执行。
- **Reproducibility / 可复现性:** Build with Go 1.23+, run `AllocationCore demo .` to generate pure synthetic fixtures, then run `go test ./... -count=1 && go vet ./...` and `go test -race ./internal/engine ./adapter ./internal/multi ./internal/studio ./internal/acceptance`. Never commit generated CSV, real rules or outputs. / 全量、竞态、独立黄金测试持续保留。

**Additional Linux CI correction / 第二项 Linux CI 修复：** The V8 synthetic multi-account result contains legitimate repeated terminal coordinates; `reconcile --reject-duplicates` was inadvertently used for a self-comparison smoke test, which correctly refused those duplicates. V9 self-compare uses the documented aggregation-compatible default, while `TestV7DuplicateCoordinatePolicy` independently checks the fail-closed strict option. / 合成结果包含合法重复坐标，CI 自对比改用兼容模式；严格重复坐标拒绝仍由独立测试保证。

### V9 actual local measurements / V9 本地实测

- **Two-stage 50,000 source-row synthetic case / 两阶段 5 万来源行：** `--workers 4 --disk-partitions 64 --evidence-gzip`; V8 and V9 generated byte-identical `results.csv`, `reconciliation.csv`, `ledger.csv.gz`, `source_to_pool.csv.gz`, `lineage_links.csv.gz` on every run. V8's checkpoint was successfully reused by V9 with `--resume` (2 reused stages). / 每次五份金额与审计文件完全一致；V8 检查点在 V9 成功复用两个阶段。
- **Repeated runs / 重复试验：** same Linux host, three runs each, wall-clock medians **V8 0.83 s vs V9 0.81 s**; OS peak RSS medians **V8 46,920 KiB vs V9 18,456 KiB**. These are small synthetic runs on one environment, not representative of 10M rows or Windows. Other process allocations remain unbounded by the 2 MiB writer-buffer limit. / 三次中位数，时间改善有限、峰值常驻内存明显下降，但不可外推到生产负载。
- **1000 accounts / 千科目：** 1000 effective rule groups, 4000 result rows, `results.csv` and `account_reconciliation.csv` identical SHA-256 for V8 vs V9. Single-run timings ~4.56 s vs ~4.02 s, **not** a controlled speedup claim. / 千科目业务输出完全一致，单次耗时差异仅作观察。
- **CI shell reproduction / GitHub CI 脚本复现：** `mkdir -p output` resolves benchmark redirection. The multi-sample self-comparison is now run with aggregation-compatible defaults because legitimate duplicate full coordinates are present, while strict rejection remains independently tested. The full Linux CI command sequence was executed locally through both the multi-sample and 1000-account stages. / 两个 Linux CI 脚本问题均在本地复现和修复；真实在线 CI 仍待更新 GitHub 后执行。
- **Clean-package verification / 全新解压：** one Windows x64 PE executable, one Go module, 4 bilingual Markdown files, no shipped business CSV/Excel or generated 1000-account JSON; source recompilation, `go test ./...`, `go vet ./...`, and local HTTP (`/api/health`, `/workspace`, `/multi`, `/explorer/api/meta`, `/rules`) all passed. **Actual Windows native execution and official MySQL driver linking were not performed here.** / Windows 实机及官方 MySQL 驱动联网编译仍需单独完成。


## V10 targeted validation / V10 针对性验收

- `go test ./...`, `go vet ./...`, `go test -race` on critical packages and Windows x64 cross-compilation are mandatory. / 完整测试、静态分析、重点竞态测试、Windows 交叉编译。
- `TestV10StreamingCoalesceExactOrderAndTrace`: 20,000 synthetic input rows across 16 coordinates; legacy and streaming aggregate rows **and COALESCE lineage edges** match exactly in original order. / 原算法和流式算法金额及血缘逐行一致。
- `TestV10SparseCoalesceUsesLegacyFastPath`: unique-coordinate sample does not activate streaming. / 高基数回退旧实现。
- `TestV10SkewOverflowAndCorruptionFailClosed`: signed int64 overflow and damaged JSONL cause an error rather than successful output. / 溢出与损坏记录阻断。
- `BenchmarkV10SkewCoalescing`: synthetic 30,000 rows, 100 coordinates, same lineage callbacks, Linux AMD EPYC. Two-run samples: legacy approximately 73–79 ms and 28.0 MB allocated; streaming approximately 75–77 ms and 20.9 MB allocated. **Microbenchmark only**. / 合并微基准，不能外推为整机加速。
- Synthetic 250,000-row PASS→MERGE disk run (64 partitions, 4 workers, gzip evidence): V9 ~4.82s, RSS ~24.2MiB; V10 ~4.93s, RSS ~24.6MiB. **All five published financial/evidence files SHA-256 identical. No demonstrated end-to-end speed or RSS improvement.** / 完整执行略有波动，不能宣称性能已提升。
- A V9-generated durable checkpoint (same 250,000-row synthetic rule) was resumed by V10. All five files (`results.csv`, `reconciliation.csv`, `ledger.csv.gz`, `source_to_pool.csv.gz`, `lineage_links.csv.gz`) were byte identical. / V9 检查点续算结果一致。
- Most recent V9 GitHub run (SHA `bf4ce8d668b0dba074d821da096c05808c4c4fed`): `test`, `windows`, `mysql-integration` passed; `mysql-tls-integration` failed while seeding before TLS import (`ERROR 1045 Access denied for root`). V10 replaces the unreliable `mysqladmin ping` readiness test with authenticated `SELECT 1`. **The V10 online TLS job is NOT yet verified.** / V9 真实 CI 故障已有日志，V10 修复待在线运行。
