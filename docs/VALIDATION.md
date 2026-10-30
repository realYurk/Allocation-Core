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
- Read-only relational database connector, if needed; currently use external extraction. / 原生数据库连接器待做。

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
