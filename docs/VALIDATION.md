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
