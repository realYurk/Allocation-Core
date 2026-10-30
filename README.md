# Allocation Core Clean V2 / 通用分摊引擎

**One Go module · One Windows executable · One workspace.**
**一个 Go 模块、一个 Windows 程序、一个业务工作台。**

This repository contains **no company data or proprietary allocation rules**. Inputs, outputs, mappings and runtime records stay outside version control. / 本仓库不包含企业真实数据和私有规则；输入、输出、映射与运行状态默认不纳入 Git。

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
