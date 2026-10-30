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
