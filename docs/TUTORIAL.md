# 新手教程

这篇教程给第一次接触 Allocation Core 的人。不需要会编程，按顺序走一遍就能跑通一个分摊场景，并看懂输出。

## 一、先搞清楚「分摊」在算什么

假设你有一笔费用 1000 元，要分给几个商品。你给了一条规则：「75% 按材料用量，25% 按收入」。

引擎会先把 1000 拆成 750 和 250，再分别按各自的驱动比例分下去，最后把同一个商品收到的两部分加起来。

财务上真正要关心的不是算法，而是四件事：

1. 总额对不对（钱有没有多一分或少一分）。
2. 分给了谁。
3. 为什么这么分（用了哪个驱动、比例是多少）。
4. 每一笔的来源在哪。

引擎的输出就是围绕这四点设计的。

还有一种更常见的场景是多阶段：工厂费用 1200 元 → 商品系列 → 四个具体商品。第一阶段的输出变成第二阶段的输入。两种方式可以混用。

## 二、用 Windows 跑一遍（不用编程）

1. 下载发行 ZIP 并完整解压，不要在压缩包里直接双击 BAT。
2. 双击 `00_Windows_Diagnostic.bat`，确认环境正常。
3. 双击 `16_Windows_Run_Trace_Demo.bat`。跑的是「75% + 25% 两路合并」的例子，结束会自动打开结果文件夹。
4. 用 Excel 打开 `results.csv`，能看到最终金额。这是虚构数据，商品 A = 725、B = 275，只作演示。
5. 打开 `reconciliation.csv`，每一行金额池都应该显示 `PASS`。
6. 用记事本打开 `first_sku_trace.json`，能看到材料路径和收入路径都汇到了最终商品，并且能向上找到费用来源。
7. 再双击 `17_Windows_Two_Stage_Demo.bat`，跑「费用 → 商品系列 → 商品」，并打开 `comparison.csv` 看分摊前后对比。

CSV 带 UTF-8 BOM，Excel 一般能正常显示中文。如果还是乱码，用 Excel 的「数据 → 自文本/CSV」按 UTF-8 导入。整个过程不会连接企业数据库。

## 三、看懂输出文件

| 文件 | 看什么 |
|---|---|
| `results.csv` | 最终分到的金额。注意：一个商品可能占多行，因为不同驱动各自贡献了一部分。 |
| `reconciliation.csv` | 每个金额池有没有守恒。先看这个，全 `PASS` 再往下看。 |
| `ledger.csv` | 每一行用了哪个驱动、分母多大、比例多少、结果多少。 |
| `source_to_pool.csv` | 原始金额进了哪个池。 |
| `lineage_links.csv` | 合并时哪些上游行汇成了哪一行。 |
| `comparison.csv` | 分摊前 / 分摊后 / 差额，按你指定的维度聚合。 |

## 四、三个最容易配错的术语

规则里每个维度都要决定两件事：来源怎么归集、目标怎么选。三个关键词最常见：

- **AS_IS**：来源里这个维度的每个成员保持独立金额池。例如每个工厂各自算自己的。
- **TOTAL**：先把这个维度合并成一笔。例如把 SKU 上分散的费用合并成一笔待分配金额。
- **SOURCE**：目标维度保持不变。例如分摊后仍然保留原工厂，配合 `AS_IS` 用。

一个常见的目标是「工厂内部把费用分到商品」：工厂用 `AS_IS → SOURCE`，商品用 `TOTAL → INCLUDE`。

不要把所有维度都设成 `TOTAL`，尤其守恒维度不能随意改动。守恒键（`conservation_keys`）里的维度必须是 `AS_IS + SOURCE`。

另外记住两种「份额」不是一回事：

- `stages[].inputs[].share`：阶段之间边的路由份额。同一来源的所有出边合计必须等于 1。
- `stages[].allocations[].share`：单个阶段内多因子的费用份额。激活因子合计必须等于 1。

## 五、接入一家新公司的六个动作

以零售企业为例。换行业只是换维度和驱动，内核不变。全程用虚构数据练习，不要直接对正式财务库写回。

### 1. 定义业务维度

问财务人员：这笔费用需要按哪些维度观察？比如 `year`、`month`、`account`、`region`、`brand`、`sku`、`currency`。字段名建议用稳定的英文，给用户看的中文名称由前端或字段字典负责映射。每家公司可以增删维度。

### 2. 准备金额 CSV

每行是一笔具体金额来源。示意（表头要和规则里的 Dimensions 完全对应）：

```csv
month,account,region,brand,sku,currency,amount
2026-01,营销费用,东区,品牌甲,待分摊,CNY,1000.00
```

### 3. 准备驱动 CSV

按销量权重分到商品：

```csv
month,region,brand,sku,units
2026-01,东区,品牌甲,商品A,3
2026-01,东区,品牌甲,商品B,1
```

注意：驱动不一定需要 `account` 科目列。同一份销量可以服务多个科目，避免重复数据。但为目标维度提供的成员列必须有。

### 4. 写规则

用 `tools/rule_generator_offline.html`（双击浏览器打开）生成规则草案，或直接复制 `examples/manufacturing/rule.json` 改。要财务拍板的有：

- 每个维度来源是 `AS_IS` 还是 `TOTAL`。
- 目标范围是 `SOURCE`、`INCLUDE`、`INCLUDE_EXCLUDE` 还是 `ELEMENT`。
- 驱动和份额（销量、工时、收入，还是混合权重）。
- 多阶段链路：先到哪些节点，下一阶段从哪个节点取值。支持顺序和分叉合并，但不能成环。
- 守恒维度：哪些组合必须各自守恒。

### 5. 生成模板并试算

在项目根目录的 CMD 运行：

```bat
alloc_windows_amd64.exe generate examples\manufacturing\rule.json new_company_demo
```

会生成 `new_company_demo/`，里面有 `rule.json`、金额/驱动 CSV 模板、`Windows_Run_Allocation.bat`、`NEW_COMPANY_START_HERE.md`，以及 Java/Python 接口样例。

把编译好的 `alloc_windows_amd64.exe` 复制进去，填好模板后双击 `Windows_Run_Allocation.bat`，或者手动跑：

```bat
alloc_windows_amd64.exe validate rule.json
alloc_windows_amd64.exe run rule.json runs\trial1 --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir cache
```

不需要装 Go 或 Python。Windows ARM64 用对应的 `alloc_windows_arm64.exe`。

空模板不是结果。必须填了有效数据，才能跑出真实金额。

### 6. 对账后交给业务系统

先看 `reconciliation.csv`，确认每个池守恒；再看 `results.csv`、`source_to_pool.csv`、`ledger.csv` 核对链路、分母和金额。

对接方式按技术栈选：

- **Java / Spring Boot**：调用 HTTP API，拿结果后按自有业务 ID 存储。参考 `adapters/AllocationHTTPClient.java`。
- **Python**：标准库 `urllib` 调 API，或 `subprocess` 启动 EXE。参考 `adapters/python_http_client.py`。
- **其他语言**：HTTP JSON + CSV，不限语言。
- **已有模型 / 预算平台**：导出金额和驱动 CSV → 调内核 → 结果 CSV 回写。原生模型脚本生成器目前没做。

对接项目至少要提供：维度映射、金额来源查询、驱动查询、规则存储、任务 ID、结果落地表、错误告警、历史追溯。这些和企业的数据库有关，公共内核没法自动识别。

## 六、常见问题

**Q：`results.csv` 里一个商品有好几行，是不是重复入账？**

不是。多因子分摊时，同一个商品会从每个驱动各得到一行。看 `ledger.csv` 能区分来源。最终报表按维度归并即可。

**Q：`doctor` 通过了是不是就一定没问题？**

不是。`doctor` 只查规则结构、数据文件存在和 CSV 表头契约，不遍历全表，也不证明分母非零。真正跑一遍 `run` 才算数。

**Q：为什么建议先看 `reconciliation.csv`？**

守恒是最基本的要求。如果某个池不是 `PASS`，说明钱对不上，后面再看明细就没有意义。

**Q：能直接对生产数据库写回吗？**

不建议。引擎不做写回，也不做审批。先在测试数据上跑通、核对清楚，再通过你自己的接入层落地。

**Q：为什么有些 CSV 打开是乱码？**

输出是 UTF-8 带 BOM。用 Excel「数据 → 自文本/CSV」按 UTF-8 导入即可。

## 下一步

- 英文说明见 `../README.md`，中文说明见 `../README_CN.md`。
- 更详细的接入协议看 `02_INTEGRATION_GUIDE_CN.md` 和 `02_HTTP_Service.md`。
- V16 的评审和已知边界看 `V16_BACKEND_AUDIT_CN.md`。
