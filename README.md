> **Windows 新电脑启动 / Windows setup:** 先看 [README_DOWNLOAD_FIRST_CN.md](README_DOWNLOAD_FIRST_CN.md)。GitHub Source ZIP 不含 EXE；下载完整发行包。

# Allocation Core

规则驱动的多维成本分摊引擎。以精确整数金额运算为核心，把一组成本池，按可配置的驱动因子（销量、工时、用料等）与多阶段拓扑，分摊到目标对象，并在每一步保证金额守恒。

> A rule-driven, multi-dimensional cost allocation engine. It distributes cost pools across target objects using configurable drivers and a multi-stage DAG, with exact integer arithmetic and conservation enforced at every step.

---

## 它解决什么问题 / What problem it solves

企业里有一类重复出现的计算：把一笔间接成本（水电、人工、管理费）按某种权重（销量、工时、面积）分摊到产品、部门、客户或 SKU。这类计算看似简单，实则容易踩坑：

- 用 `float` 累加导致分钱对不上账；
- 分摊后总额变多或变少（不守恒）；
- 多步分摊（先分到系列、再分到商品）时中间结果污染最终结果；
- 规则一改就要重写代码，无法复用。

Allocation Core 把这些诉求固化成一条可声明、可审计、可复现的规则：**输入金额 + 驱动因子 → 声明式规则 → 输出分摊结果 + 对账凭证 + 血缘索引**。你只写 CSV 和一份 JSON 规则，不写分摊逻辑。

This engine targets the recurring task of distributing indirect costs (utilities, labor, overhead) across products, departments, customers, or SKUs by weights (sales, hours, material). It avoids the classic pitfalls — float drift, non-conservation, cross-stage contamination — by turning allocation into a declarative, auditable, reproducible rule.

---

## 核心特性 / Key properties

| 特性 | 说明 |
|---|---|
| **精确金额** | 金额用 `int64` 定点数表示（精度 2 默认，可配 0–6），`math/big` 只用于分摊中间量，全程无浮点误差 |
| **守恒强制** | 每个池分摊前后总额相等，逐池对账写入 `reconciliation.csv`；顶层 `input_amount == output_amount` |
| **最大余数法** | 无法整除的余数按确定性规则分配（余数大者优先，并列按标识排序），结果可复现 |
| **多阶段 DAG** | 支持 `ALLOCATE` / `MERGE` / `PASS`，多因子、多步拓扑，阶段间不互相污染 |
| **多驱动因子** | 同一阶段可叠加多个驱动（销量 + 工时），按 `share` 加权 |
| **血缘追踪** | 每一行结果都能追溯到来源池与驱动权重（`trace` / `trace-index`） |
| **审计归档** | 无损 ZIP 归档（`audit-pack`），流式 gzip 压缩证据（`--evidence-gzip`） |
| **检查点续算** | 已完成阶段复用，中断后 `--resume` 不重算 |
| **容量准入** | 提交前估算输出规模，超预算直接拒绝，防止磁盘写爆 |
| **零依赖** | 单个静态二进制，无运行时、无数据库、无 Redis |

| Property | Description |
|---|---|
| **Exact amounts** | Amounts are `int64` fixed-point (precision 2 by default, 0–6 configurable); `math/big` used only for intermediate apportionment, no float drift |
| **Conservation enforced** | Per-pool input equals output, written to `reconciliation.csv`; top-level `input_amount == output_amount` |
| **Largest remainder** | Indivisible remainders distributed deterministically (largest remainder first, ties by identity), reproducible |
| **Multi-stage DAG** | `ALLOCATE` / `MERGE` / `PASS`; multiple factors and stages without cross-contamination |
| **Multiple drivers** | Stack several drivers per stage, weighted by `share` |
| **Lineage tracing** | Every output row traces back to its source pool and driver weights |
| **Audit archive** | Lossless ZIP archive; streaming gzip evidence |
| **Checkpoint resume** | Completed stages are reused; `--resume` skips recomputation |
| **Capacity admission** | Estimates output size before running and rejects jobs that exceed budget |
| **Zero dependencies** | Single static binary; no runtime, no database, no Redis |

---

## 性能（实测）/ Measured performance

环境：Intel Core i5-12490F（6 核 12 线程）、16 GB 内存、Windows 11，`--workers 4 --evidence-gzip`。数据为合成数据，规模 = 成本池数 × 目标 SKU 数。

| 规模 | 结果行数 | 耗时 | 输出体积 | 守恒 |
|---|---|---|---|---|
| 100 科目 × 12 期 × 1,000 SKU | 1,200,000 | 4.4 s | 147 MB | ✓ |
| 100 科目 × 12 期 × 10,000 SKU | 12,000,000 | 43.5 s | 1.47 GB | ✓ |
| 1,000 科目 × 12 期 × 10,000 SKU | 120,000,000 | 432.6 s（7.2 分钟） | 14.7 GB | ✓ |

> 吞吐约 **27–28 万行/秒**，线性扩展。完整方法、守恒逐池对账、精度边界与历史基线见 [`docs/PERFORMANCE_REPORT.md`](docs/PERFORMANCE_REPORT.md)。

Environment: Intel Core i5-12490F (6C/12T), 16 GB RAM, Windows 11, `--workers 4 --evidence-gzip`. Synthetic data; scale = cost pools × target SKUs. Throughput is roughly 270–280k rows/s, scaling linearly. Full methodology, per-pool conservation, precision boundaries, and historical baselines in [`docs/PERFORMANCE_REPORT.md`](docs/PERFORMANCE_REPORT.md).

---

## 快速开始 / Quick start

### 1. 拿一个二进制 / Get a binary

`core/` 下是跨平台静态二进制，无需安装：

| 平台 | 文件 |
|---|---|
| Windows x64 / arm64 | `core/alloc_windows_amd64.exe` / `core/alloc_windows_arm64.exe` |
| Linux x64 / arm64 | `core/alloc_linux_amd64` / `core/alloc_linux_arm64` |
| macOS x64 / arm64 | `core/alloc_darwin_amd64` / `core/alloc_darwin_arm64` |

也可以从源码编译（需要 Go 1.21+）：

```bash
cd core && go build -o alloc ./cmd/alloc
```

### 2. 跑一个内置示例 / Run a shipped example

```bash
# 两阶段制造业示例：工厂 → 系列 → 商品
./core/alloc_windows_amd64.exe run core/examples/manufacturing/rule.json ./out
```

输出目录会生成：

```
out/
├── results.csv          # 最终分摊结果（terminal rows）
├── reconciliation.csv   # 逐池对账（delta 必须为 0）
├── ledger.csv.gz        # 完整流水（压缩）
├── source_to_pool.csv.gz
├── lineage_links.csv.gz # 血缘
└── run_summary.json     # 状态 + 输入/输出总额
```

### 3. 写你自己的规则 / Write your own rule

最小规则由三类文件组成：**金额表 + 驱动表 + 规则 JSON**。

`amounts.csv`（成本池，`sku` 列为占位符）：

```csv
period,region,brand,account,sku,currency,amount,row_id
2026-01,东区,品牌甲,科目001,待分摊,CNY,5000.00,SRC-1
```

`drivers.csv`（驱动因子）：

```csv
period,region,brand,sku,units
2026-01,东区,品牌甲,商品A,120
2026-01,东区,品牌甲,商品B,80
```

`rule.json`（声明分摊逻辑）：

```json
{
  "schema": "allocation-ir/2.0",
  "name": "按销量分摊到商品",
  "precision": 2,
  "dimensions": ["period", "region", "brand", "account", "sku", "currency"],
  "immutable_dimensions": ["period", "currency"],
  "hierarchies": {},
  "datasets": {
    "amounts": { "expenses": "amounts.csv" },
    "drivers": { "sales": "drivers.csv" }
  },
  "stages": [
    {
      "id": "按销量分摊",
      "inputs": [ { "from": "expenses", "share": "1" } ],
      "source": {
        "grouping": { "period": "AS_IS", "region": "AS_IS", "brand": "AS_IS", "account": "AS_IS", "sku": "TOTAL", "currency": "AS_IS" },
        "filters": {}
      },
      "target": {
        "period": { "mode": "SOURCE" },
        "region": { "mode": "SOURCE" },
        "brand": { "mode": "SOURCE" },
        "account": { "mode": "SOURCE" },
        "sku": { "mode": "INCLUDE", "include": ["商品A", "商品B"] },
        "currency": { "mode": "SOURCE" }
      },
      "conservation_keys": ["period", "region", "brand", "account", "currency"],
      "allocations": [
        { "id": "sales_share", "share": "1", "driver_dataset": "sales", "weight_field": "units", "negative_driver_policy": "REJECT" }
      ]
    }
  ]
}
```

关键语义：

- `grouping`：`AS_IS` 保留该维度原值，`TOTAL` 忽略该维度（聚合）。
- `target.mode`：`SOURCE` 沿用来源值；`INCLUDE` 枚举目标值；`ELEMENT` 固定为单值。
- `conservation_keys`：守恒的粒度。引擎会在这些键组成的每个池上做逐池对账。
- `allocations`：驱动因子及其 `share`（同一阶段可叠多个）。

---

## 调用方式 / Integration

有三种接入方式，按推荐顺序：

### A. 命令行子进程（最简单）

```bash
alloc run RULE.json OUT_DIR --workers 4
```

把二进制当作外部程序调用，读写 CSV，与宿主语言解耦。适合批处理。

### B. HTTP 服务（推荐，长驻）

```bash
alloc serve --root ./rules --store ./service_data --listen 127.0.0.1:8787 --jobs 2 --token SECRET
```

```
GET  /healthz
POST /v1/validate     # 校验规则
POST /v1/jobs         # 提交任务
GET  /v1/jobs/{id}    # 查询状态
```

服务只监听回环地址；要对外暴露需在网关层加 TLS、鉴权和限流。Python 客户端见 `integration/python/http_client.py`，Java 客户端见 `integration/java/`。

### C. 直接嵌入（把 CSV 灌进数据库后调用）

引擎本身不连数据库。SQL 侧的做法是：用 `alloc run` 产出结果 CSV，再批量导入。仓库提供了三种数据库的建表模板：

```
sql/mysql_template.sql
sql/postgres_template.sql
sql/sqlserver_template.sql
```

导入脚本见 `integration/python/sql_bulk_publish.py`。**注意**：数据库只做结果存储，不重算金额；金额算法始终在引擎内，避免多处实现不一致。

### 关于 `.bat` / `.cmd`

`core/*.bat` 与根目录的 `run_windows.cmd` 只是 Windows 的便捷封装，**不是必须**。它们最终都调用同一个 `alloc` 二进制。你可以完全不点 `.bat`，直接在代码里 `exec` 这个二进制。

---

## 挂了怎么办 / Failure model

引擎按"要么全对、要么不留半成品"设计：

- **全量重算，无增量**：每次从原始输入完整计算。中途崩溃不会留下"只算了一半"的错误状态，重跑即可，结果一致。
- **原子提交**：结果先写到临时位置，全部成功后才落盘到输出目录；失败不产出半成品 `results.csv`。
- **容量准入**：运行前估算输出规模，超过预算直接拒绝（软预算，不是 OS 磁盘配额），防止磁盘写爆。
- **检查点续算**：`--checkpoint-dir` + `--resume` 复用已完成阶段，长任务中断后继续。

真正的进程挂了，处理方式就是：**删掉输出目录重跑**（或 `--resume`）。它不依赖任何持久状态。

---

## 命令速查 / Command reference

```
alloc validate RULE.json                 # 校验规则
alloc explain RULE.json                  # 打印阶段/DAG
alloc run RULE.json OUT_DIR [options]    # 执行分摊
alloc batch MANIFEST.json OUT_DIR        # 批量
alloc compare RULE.json OUT_DIR k1,k2    # 前后对比
alloc trace RULE.json OUT_DIR ROW_ID     # 血缘追踪
alloc serve ...                          # HTTP 服务
alloc vault add|list|stats|verify|restore|prune|gc|export-db ...  # 归档
alloc audit-pack|audit-verify|audit-extract ...
alloc trace-index|trace-index-verify ...
alloc capacity BUDGET.json OUT_DIR       # 容量预检
```

`run` 的可选项：

```
--workers N           并发 worker（1..128）
--disk-partitions N   磁盘分区（2..64，用 IO 换内存）
--checkpoint-dir DIR  检查点目录
--resume              续算
--driver-disk-index   驱动磁盘索引
--driver-cache-dir DIR
--evidence-gzip       流式 gzip 证据
```

---

## 目录结构 / Layout

```
core/                     # Go 引擎源码 + 跨平台二进制
  cmd/alloc/              # CLI 入口
  internal/engine/        # 分摊内核
  internal/service/       # HTTP 服务
  internal/vault/         # 归档生命周期
  internal/workspace/     # 持久工作区
  internal/batch/         # 批量
  internal/capacity/      # 容量准入
  bench/                  # 压测与精度验证脚本
  examples/               # 内置示例
integration/              # Java / Python 客户端与导入脚本
sql/                      # MySQL / PostgreSQL / SQL Server 模板
docs/                     # 详细文档（中文为主）
tests/                    # 集成与攻击测试
```

---

## 文档 / Docs

- 从零接入与压测报告：[`docs/PERFORMANCE_REPORT.md`](docs/PERFORMANCE_REPORT.md)
- 中文入门：[`docs/01_Beginner_CN.md`](docs/01_Beginner_CN.md)
- 开发者接入：[`docs/02_Developer_Integration_CN.md`](docs/02_Developer_Integration_CN.md)
- SQL 数据库接入：[`docs/03_SQL_Database_Integration_CN.md`](docs/03_SQL_Database_Integration_CN.md)
- 安全与容量边界：[`docs/04_Boundaries_Security_Acceptance_CN.md`](docs/04_Boundaries_Security_Acceptance_CN.md)

---

## 许可证 / License

个人作品，保留所有权利。未获作者书面许可，请勿用于商业用途或再分发。
