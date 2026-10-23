# 零基础：六步接入一家全新公司的费用分摊

以零售企业举例。其他行业只是维度和驱动不同，计算内核相同。全程只使用虚构数据练习，**不要直接对正式财务库写回**。

## 第 1 步：定义业务维度

问财务人员：一笔费用需要按哪些维度观察？比如 `year`、`month`、`account`、`region`、`brand`、`sku`、`currency`。字段名建议用稳定英文编号，给用户看的中文名称由其自己的前端或字段字典映射。

每家公司可以增删维度：本项目不要求使用固定的品牌、区域、SKU 名称。

## 第 2 步：准备费用金额 CSV

每行是一笔具体金额来源。例子（示意，表头需与 `rule.json` 中 Dimensions 完全对应）：

```csv
month,account,region,brand,sku,currency,amount
2026-01,营销费用,东区,品牌甲,待分摊,CNY,1000.00
```

## 第 3 步：准备 Driver CSV

示例按销量权重分到商品：

```csv
month,region,brand,sku,units
2026-01,东区,品牌甲,商品A,3
2026-01,东区,品牌甲,商品B,1
```

注意：Driver **不一定需要 account 科目列**。同一份销量可以服务多个科目，避免重复数据。必须为需要限定的目标维度提供成员列。

## 第 4 步：创建通用分摊规则

使用 `tools/rule_generator_offline.html`（双击浏览器打开）生成规则草案，也可以先复制 `examples/manufacturing/rule.json` 并按业务修改。

重点由财务决定：

- **来源归集**：每个维度是保持原值 `AS_IS`，还是汇总到金额池 `TOTAL`？
- **目标范围**：与来源相同 `SOURCE`，还是指定 `INCLUDE`/`INCLUDE_EXCLUDE`/`ELEMENT`？
- **驱动及份额**：是销量、工时、收入还是混合权重（如 75% + 25%）？
- **多阶段链路**：第一阶段到哪些节点，下一阶段从哪个节点取值？支持顺序阶段与分叉合并 DAG；不能构造环。
- **守恒维度**：每个月、科目、品牌或地区是不是必须各自守恒？

## 第 5 步：自动生成模板并试算

在项目根目录的 CMD 运行：

```bat
alloc_windows_amd64.exe generate examples\manufacturing\rule.json new_company_demo
```

将生成 `new_company_demo/`、`rule.json`、金额/Driver CSV 模板、`Windows_Run_Allocation.bat`（安全的 ASCII 文件名）、`NEW_COMPANY_START_HERE.md`、Java/Python 接口样例。

把编译好的 `alloc_windows_amd64.exe` 复制进去，填好模板后双击 `Windows_Run_Allocation.bat`，或运行：

```bat
alloc_windows_amd64.exe validate rule.json
alloc_windows_amd64.exe run rule.json runs\trial1 --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir cache
```

不需要安装 Go/Python。Windows ARM64 则使用包内对应的 `alloc_windows_arm64.exe`。

**注意：空模板不应当直接当成计算结果。** 必须填入有效数据之后，才能跑出真实金额。

## 第 6 步：对账后交给业务系统

优先打开 `reconciliation.csv`，确保每个金额池守恒；再查看 `results.csv`、`source_to_pool.csv`、`ledger.csv` 验证每条分摊链路、分母和商品金额。Excel 显示乱码时按 UTF-8 方式导入。

- **Java/Spring Boot 项目**：后端输出两类 CSV 和统一规则 JSON，调用 `Allocation Core` 的 HTTP API，取得结果后按自有系统的业务 ID 存储。
- **Python 项目**：通过 Python 标准库 `urllib` 调用 API 或 `subprocess` 启动 EXE；不用重复实现分摊公式。
- **其他语言**：使用 HTTP JSON + CSV，不受语言限制。
- **原有多维模型/其他预算平台**：通过导出金额与驱动 CSV、调用内核、结果 CSV 回写到其模型接口的方式复用；**原生模型计算脚本生成器尚未实现**。

## 与其他企业系统集成时要做的工作

实施开发人员至少要提供：维度编号映射、金额来源查询、Driver 查询、规则配置存储、任务 ID、结果落地表、错误告警和可追溯历史。上述部分与企业自己的数据库和业务接口有关，公共内核不可能无条件自动识别。
