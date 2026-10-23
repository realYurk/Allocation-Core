# Allocation Core V15｜给企业开发人员的接入说明

## 1. 后端定位

`allocation-ir/2.0` 是公共规则契约，Go 引擎为当前权威执行器。企业自己的 Java、Python、其他语言或模型平台只需要将原始数据映射到规则 JSON + CSV 的标准输入，调用 EXE 或 HTTP 服务，并消费结果/对账/证据数据。无需在各系统分别实现费用分配公式。

## 2. 核心对象

- `dimensions`：企业自定义维度数组；例如 `period/account/region/brand/sku`。
- `immutable_dimensions`：不可被重分配的维度，例如期间、币种。
- `datasets.amounts`：名称 -> 金额 CSV 路径；至少包含所有维度及 `amount`，可增加 `row_id`。
- `datasets.drivers`：名称 -> 驱动 CSV 路径；需包含目标被选择的维度和 `weight_field`，可省略与本驱动无关且能从来源继承的维度。
- `stages`：DAG 节点，`inputs` 表示从哪个数据源或上游阶段进入，`share` 是路由占比。
- `source.grouping`：每维 `AS_IS` 或 `TOTAL`，决定金额池粒度。
- `target`：每维 `SOURCE`、`ELEMENT`、`INCLUDE`、`INCLUDE_EXCLUDE`。
- `allocations`：同一阶段多个因子，每因子引用不同数据集及数值字段，激活因子 `share` 总和严格等于 1。
- `conservation_keys`：需要分别守恒的维度；每个键必须 `AS_IS + SOURCE`。

**两种不同的份额**：

1. `stages[].inputs[].share`：DAG 边路由份额。来自同一来源的所有出边合计必须 = 1。
2. `stages[].allocations[].share`：单一阶段中的多因子费用份额。该阶段的激活因子合计必须 = 1。

两者不能混为一谈。如果一条边后接两阶段顺序分摊，前一阶段通常把 100% 传递到下一阶段，而不是再分摊掉一遍费用。

## 3. 推荐命令

```text
alloc_windows_amd64.exe doctor examples/dual_driver/rule.json
alloc_windows_amd64.exe validate examples/dual_driver/rule.json
alloc_windows_amd64.exe explain examples/dual_driver/rule.json
alloc_windows_amd64.exe run examples/dual_driver/rule.json v15_runs/test --workers 4
alloc_windows_amd64.exe compare examples/dual_driver/rule.json v15_runs/test account,period
```

`doctor` 检查配置规则、数据文件存在性和 CSV 表头契约，**不遍历整表，也不会提前证明分母非零**。`run` 会执行真实检查；失败时不能把半成品作为正式财务结果。

`compare` 使用真实原始金额和**终端结果**，按传入的维度聚合：

```csv
account,period,before,after,delta_after_minus_before
管理费用,2026-01,1000.00,1000.00,0.00
__TOTAL__,__TOTAL__,1000.00,1000.00,0.00
```

分组维度建议优先选择来源、目标共享的守恒键，如 `period,account`。若指定 `sku` 且来源尚为“待分配”，系统会保留原始池项，不伪造 SKU 级前值；这些行的差额可能非零，不能单凭它们断言分摊失衡。

## 4. API / Java / Python

支持调用发行包中同一后端：

- CLI：`run` 生成 `results.csv`、`ledger.csv`、`reconciliation.csv`、`source_to_pool.csv`，以及摘要 JSON。
- HTTP：`serve --root ... --store ...` 启动本机服务，调用 `POST /v1/jobs`、`GET /v1/jobs/{id}` 获取结果。默认只能绑定 loopback，禁止作为未经保护的公网接口。
- Java：`adapters/AllocationHTTPClient.java` 或 CLI Client；Java 8+ 标准库，调用独立服务或进程。
- Python：`adapters/python_http_client.py` 或文件转换适配器；无需第三方依赖。
- 其他系统：输出规则 JSON + CSV，调用 CLI / HTTP；如果是模型平台，可以由数据装载过程导出标准数据并回写标准结果，不必在模型中重复实现内核。

目前并未实现所有平台的**原生计算代码生成**。规则生成器生成的是可移植的统一规则和接入模板，不等于将 Go 内核自动翻译成任意语言。

## 5. 稳定性与性能边界

V15 沿用此前 Go 多线程、分区、外部驱动索引、缓存及文件快照等实现。`results.csv` 可能一商品多行（多因子分别贡献），结果展示应对商品进行聚合，不能盲目以 CSV 行数认作 SKU 数量。企业部署前必须测试实际数据分布、极端数据倾斜、额外并发、精度、数据库事务、磁盘空间及容灾恢复。

当前前端是简化的规则示例生成器，不是企业级图形化 DAG 编辑器；审批不在设计范围内。
