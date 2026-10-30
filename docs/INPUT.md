# Generic Input Contract / 通用数据接入规范

## 1. Five concepts / 五个核心概念

**Dimensions / 维度** are labels used for grouping (period, site, product, account, currency, etc.). / 维度是分组标签；名称由接入方决定。
**Members / 成员** are values of dimensions. A member named `Unassigned` or `No Model` is a legitimate source grain, **not a missing value**. / 未细分成员是合法的记账粒度，不得当成缺失值或复制成所有明细。
**Amounts / 金额** use exact decimal strings with a declared precision. / 金额用精确十进制字符串，统一精度。
**Drivers / 因子** provide candidate destinations and weights. / 因子指定目标及权重。
**Rules / 规则** determine how source pools, filters, targets, weights and stages interact. / 规则决定来源池、筛选、目标、因子和阶段。

## 2. CSV format / 输入示例

Amounts `input/amounts/amounts.csv` (synthetic values only): / 待分摊金额示例：

```csv
period,site,product,account,currency,amount,row_id
2026-01,Site-A,Unassigned,Cost-001,CUR,1200.00,ROW-001
```

Driver `input/drivers/units.csv`: / 分摊因子示例：

```csv
period,site,product,units
2026-01,Site-A,Item-A,60
2026-01,Site-A,Item-B,40
```

The engine will calculate 720.00 / 480.00 when the rule allocates by units. / 当规则按数量分配时，得到 720 与 480。

Rules are **`allocation-ir/2.0`** JSON, with sections `schema`, `precision`, `dimensions`, `immutable_dimensions`, `datasets` and `stages`. Source `grouping` can keep a dimension `AS_IS` or aggregate `TOTAL`; targets can use `SOURCE`, `INCLUDE` or `ELEMENT`. / 通用规则由上述字段组成，明确源粒度、目标和守恒键。参见 `examples/` 下纯合成规则 JSON。

**No Cartesian expansion. / 不做维度笛卡尔积。** Export only real sparse rows. Do not prefill the amount to every possible product. / 导出实际存在的稀疏记录，不允许把一条来源金额复制到全部商品。

## 3. How to integrate a new company / 如何接入新企业

1. Extract only required source leaves and posting-grain coordinates. / 按原始记账粒度抽取数据。
2. Declare column mapping **outside the engine**. / 在私有映射中确定账户、维度、单位和来源。
3. Separate costs from driver indicators. / 区分费用与统计因子。
4. Transform enterprise-specific rule files into generic JSON, and run `AllocationCore.exe validate`. Unknown rule semantics must fail review. / 外部规则表经私有适配器转为 JSON，未知语义必须阻断。
5. Calculate, check `PASS` and every cost pool; compare complete target coordinates to the external system. / 分摊后不仅检查总额，也核对各目标坐标。

## 4. Multiple accounts / 千科目差异规则

For a batch project, `project.json` declares relative paths; `assignments.csv` maps `account,template`, one record per account; `templates/*.json` contains generic templates and optional `overrides.csv` adjusts the selected drivers and shares. / 一个项目按科目分派模板，每个科目可用不同的生效规则。

To see the exact files, run `AllocationCore.exe demo .`, then inspect `testdata/multi_sample/`. They are generated on your computer, not published business data. / 运行演示命令后可查看生成的数据模板。

## 5. MySQL or Cube / 数据库或多维 Cube

Use a read-only exporter to produce the same sparse contract. Multi-dimensional members become columns; a database record already supplies those columns. / 多维系统导出维度成员，关系型数据库导出字段，进入相同通用接口。

A native, live MySQL driver is **not yet included**. The engine must not hold production credentials or hard-code database-specific business logic. / 暂未提供实时 MySQL 连接器；正式凭据不得硬编码在公共引擎中。

## 6. Reference comparison / 分摊前后对比

The Cube-style viewer compares `before`, `after` and `engine` datasets by actual coordinates. It cannot automatically infer that a coarse source member is equivalent to a set of detail targets. Select an explicit aggregation grain to compare sums; use full target coordinates for exact validation. / 数据对比支持原始记账粒度与结果明细；粗粒度和细粒度不得自动一一映射，必须明确汇总口径。

## 7. Preflight without calculation / 运行前完整体检

After preparing data and `rule.json`, use / 放入 CSV 和规则后：

```bash
AllocationCore.exe validate input/rules/rule.json
AllocationCore.exe inspect input/rules/rule.json
AllocationCore.exe inspect input/rules/rule.json --json
AllocationCore.exe run input/rules/rule.json output/my-run --workers 4
```

`inspect` uses streaming CSV reads and bounded memory. It checks all amount rows and the driver fields actually used by enabled stages, scans a shared driver dataset once, reports numeric errors with row/column, and sums amounts as exact decimal strings. / `inspect` 流式读取每一条金额记录，并对所有启用规则使用的因子字段做扫描；同一 Driver 文件不会重复扫描。报错给出行号与字段，金额通过精确整数累计。

- `PASS`: structure and input numeric values pass, **not** a claim that the allocation itself is correct. / 仅代表输入格式与数值可用，不是分摊结果 PASS。
- `BLOCK`: missing file, missing/duplicate header, malformed coordinates or decimals, invalid row length, or empty dataset. / 数据文件、字段、行宽、坐标或数值错误时拒绝。
- `--json`: structured output for external systems; a failed check returns nonzero. / 供其他系统读取，失败时返回非零退出码。
- A coarse-grained member like `Unassigned` or `No Model` is legitimate. Do not expand it to every product before running. / 未细分成员仍属于合法记账粒度，不做预展开。

Input inspection does **not** check per-pool driver coverage, zero denominator, business routing, or reference-system correctness. These require execution and comparison. / 输入体检不验证每个分摊池是否有合格 Driver、分母是否为零，也不验证业务规则与原系统完全一致。

## Multi-project preparation & one-click inspection / 多科目接入与一键体检

Keep all company data and mappings inside your local private project directory; the public repository contains synthetic descriptors only. / 真实财务数据、规则和映射请保存在本机项目目录，不放入公开仓库。

A multi-project folder contains `project.json`, `amounts.csv`, `assignments.csv`, optional `overrides.csv`, `drivers/*.csv`, and `templates/*.json`. / 一个项目包含项目配置、金额 CSV、科目分派、可选覆盖配置、Driver 和规则模板。

- `assignments.csv`: `account,template` — one account per row; duplicate/missing assignments block execution. / 每个科目唯一对应一套有效规则。
- `amounts.csv`: every declared business coordinate + `amount`; one row is one *real* sparse input coordinate, never a Cartesian expansion. / 使用稀疏真实坐标，不提前展开笛卡尔积。
- `drivers/*.csv`: actual target coordinates and weight columns required by the effective rules. / 每份因子文件保留相应目标坐标和权重字段。
- `project.json`: add `"reuse_workers": true` to reuse a bounded number of calculation processes, or `false` to retain isolated per-group startup. / 可配置复用计算进程，也可切回逐组独立进程模式。

Run **Inspect** from the workspace or `AllocationCore.exe multi inspect -project PROJECT_DIR -json`. It reads data but does not write results or a persistent cache. If PASS, you may submit the allocation; the engine still snapshots and verifies its inputs during execution. / 体检为只读操作。真正执行时仍重新创建并校验输入快照，确保不会把先前的体检结论当作财务结果。


## Reference-result acceptance (V4) / 参考系统逐坐标核验（V4）

After a successful multi-account run, prepare `reference.csv` with **exactly the same dimension column names** as the engine results (ordering may differ). Use one `amount` or `value` column; stage/row ID fields from the engine are ignored, while all business dimensions are preserved. Do not create a full Cartesian product or replace legitimate coarse granularity with zero or empty strings. / 批量计算 PASS 后，准备参考结果，所有维度列名必须与新引擎输出一致；列顺序可以不同。保留原本的记账粒度，**不能为了对账预先虚构笛卡尔积或猜测占位成员**。

Example / 简单示例：

```csv
account,period,product,value
Cost-A,2026-01,Product-A,60.00
Cost-A,2026-01,Product-B,40.00
```

Reference amounts must represent the **same units and business scope**, be independent of the calculation you are testing, and use the same configured decimal precision. If a source has different field names, transform it **outside** the engine and document the mapping. / 参考数据与计算结果必须在业务范围、金额单位和精度上对齐；如源表字段不同，应在外部适配层清洗，不能让引擎猜测映射。

For Studio / 工作台操作：

1. Choose a multi-account project and run it to `PASS`. / 选择多科目项目并运行成功。
2. Copy private `reference.csv` into the displayed project folder, or upload a <=5 MB file using the new `reference.csv` option. / 将参考文件放进页面显示的本机项目目录；小文件可直接上传。
3. Select a successful historical job; set precision (0..6) and acceptable absolute tolerance (default 0). / 选择通过的历史任务、精度和容差。
4. Click **Compare**. Inspect status, missing and extra coordinates, mismatches and top 25 differences. / 点击“对比”，检查缺失、多余、金额不符和差异最大的坐标。
5. Find `summary.json` and `differences.csv` under the job's newly created `acceptance-*` directory. / 在新建的本地验收目录查看完整 CSV 与摘要。

**Important:** `MATCH` proves *only that these two snapshots agree at every business coordinate within tolerance*. Financial conservation alone is not equivalent to reference agreement. No reference file = not verified. The engine does not provide a native MySQL or Cube connector yet; extract into this explicit contract. / `MATCH` 只证明两份快照在所选精度和容差下逐坐标一致，**不自动证明原系统规则正确**。没有独立参考结果不得写“验收一致”。目前数据库和 Cube 仍需外部适配为明确的输入契约。

## V5: one mapping for CSV, SQL and Cube / 同一份数据映射接入不同来源

**中文：** 数据源不同，规则中使用的标准维度、金额精度和分摊因子字段不应该不同。V5 先提供可立即使用的 CSV 映射命令；MySQL 与多维 Cube 的 Go 流式适配接口也已实现，但当前不内置数据库驱动、连接账号或厂商 SDK。

**English:** The source provider is separate from the allocation contract. V5 offers a working CSV normalization command and Go interfaces for a caller-injected SQL driver or Cube cell cursor. No MySQL credentials, network connection, or proprietary Cube client is built into this release.

### 1. Map columns / 建立列映射

`examples/import_mapping.json` is a **synthetic, vendor-neutral** example. Set `source.path` to your exported file, then map every canonical dimension and the exact decimal value field. Run:

```bash
AllocationCore.exe demo .
AllocationCore.exe data csv --config examples/import_mapping.json output/normalized.csv
AllocationCore.exe inspect examples/manufacturing/rule.json
```

`output/normalized.csv` will contain exactly the declared dimension columns and one named amount field; existing sparse coordinates and duplicate rows are preserved. / 输出严格保留稀疏坐标与重复数据，不对空维度擅自填 `TOTAL`，也不把独立记录合并。

**For Driver / 用于分摊因子：** specify `value_name` as the exact target Driver column such as `units`, and `precision` as `6`. Amounts commonly use `value_name=amount`, `precision=2`. No silent rounding; over-precision, invalid decimals and `int64` overflow fail closed. / 分摊因子一般用 6 位精度；金额按业务精度声明，超精度拒绝而不是偷偷四舍五入。

**CLI limitation / 命令行边界：** `data csv` only supports source.type=`csv`. Native MySQL requires a separately registered `database/sql` driver and a **read-only database account**. Cube requires a separately implemented vendor cell cursor, with dimensions and exact decimal values. Both reuse `ingest.ImportCSV(ctx, source, mapping, output)`. / MySQL 和 Cube 当前是可编译可测试的标准接入接口，不是“填连接字符串就能直连”。

### 2. Quick acceptance / 小白验收步骤

1. Export or prepare a **small slice** of source amounts and drivers. Never share credentials or proprietary rules in the public Git repo. / 准备一小份金额与因子，不上传真实规则和数据库密码。
2. Normalize each source with its explicit mapping. `data csv` returns a row count and status; errors do not publish a partial output. / 运行映射命令，检查行数和报错，不会发布半成品。
3. Reference those normalized files from a private rule JSON; run `inspect` first. / 在私有规则中引用标准文件，先运行数据体检。
4. Execute `run` or `multi`; require conservation **and** independent full-coordinate comparison with a trusted reference, not just equal totals. / 执行分摊后同时核对守恒和完整坐标差异。

### 3. Failure examples / 常见拒绝原因

| Error / 错误 | Meaning / 处理 |
|---|---|
| `missing source column` | Fix column mapping; do not guess a column. / 修改映射，禁止猜字段 |
| `missing dimension` | Explicitly map a real member. Legitimate `No Product` is not an error; empty string is. / 保留明确的未分配成员，空值拒绝 |
| `invalid amount` | The declared decimal precision is exceeded or the value is invalid. / 精度不匹配或金额非法 |
| `output already exists` | Choose a new versioned output file; inputs are immutable. / 改用新输出文件，避免覆盖历史结果 |
| SQL connection error | Check private read-only account, driver and network in the private integration application. / 私有接入程序校验权限、驱动和网络 |

### 4. Developer read-only connector examples / 开发者只读接入示例

For a private MySQL integration program (using your own registered `database/sql` driver), construct a source **without accepting arbitrary SQL text**:

```go
src := adapter.SQLSource{
    DB: db, // caller provides a least-privilege read-only *sql.DB
    Table: "finance.expenses",
    ColumnsList: []string{"period", "site", "item", "amount"},
    Equal: map[string]string{"period": "2026-01"},
}
report, err := adapter.ImportCSV(ctx, src, mapping, privateOutputPath)
```

**中文：** `DB` 必须由你私有工程初始化，驱动和口令不得写到公共仓库。库表名与列名采用严格标识符白名单，筛选值全部通过参数传入，且会请求只读事务。仍应在数据库端使用真正的只读账号，不依赖程序自身的 SELECT 限制作为唯一安全边界。

For a private Cube API client, implement `adapter.CellCursor` and return `adapter.Cell{Members:..., Value:"123.45"}` with **explicit source grain**. Then pass `adapter.CubeSource{Cursor: cursor, Dimensions: dimensions, ValueColumn: "amount"}` to `adapter.ImportCSV`. / 对 Cube 只需实现单元格迭代器，返回明确的维度成员和金额字符串，绝不能把未分配成员擅自替换为默认商品。

**Current integration boundary / 当前限制：** This repository uses a short local Go module path (`allocationcore`). The public `adapter` package is usable inside this module or a local Go workspace; publishing a separately versioned external Go SDK requires canonical module-path/version governance. / 当前适配包已公开在代码根目录，可供同一工程或 Go Workspace 使用；未来要把它作为外部独立依赖，还需要正式治理公开模块路径和版本兼容。
