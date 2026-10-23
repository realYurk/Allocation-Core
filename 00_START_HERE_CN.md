# Allocation Core V16 · 从这里开始

**这是一个通用分摊计算后端，不是依赖某个行业的财务软件。** 可以配置动态维度、多因子、串行多步骤、DAG 分叉合并。全部示例数据为虚构数据，不包含企业业务资料。

## 最简单的用法：Windows 双击

1. 完整解压发行 ZIP，不要在压缩包内双击 BAT。
2. 双击 `00_Windows_Diagnostic.bat` 检查运行环境。
3. **双击 `16_Windows_Run_Trace_Demo.bat`**，自动执行“75%＋25% 两条路径合并”。程序会打开结果文件夹：`results.csv` 是最终金额，`first_sku_trace.json` 是第一个商品的来源链路。
4. 再双击 `17_Windows_Two_Stage_Demo.bat`，运行“费用 → 商品系列 → 商品”，并自动生成 `comparison.csv` 查看分摊前/后/差异。
5. 要改变因子或比例，可打开 `tools/Allocation_Studio_V15_Offline.html` 从正确示例出发，修改后导出业务包。这个网页只是辅助；正式结果由后端计算。

**重点看这些文件：**

| 文件 | 什么意思 |
|---|---|
| `results.csv` | 最终输出金额，Excel 可打开 |
| `comparison.csv` | 财务口径的分摊前、分摊后和差额（运行 compare 生成） |
| `reconciliation.csv` | 每个金额池是否守恒 |
| `ledger.csv` | 使用哪个驱动、分母、比例、结果 |
| `source_to_pool.csv` | 原始金额进入哪个来源金额池 |
| **`lineage_links.csv`** | **V16 新增**：合并时，哪些上游金额行汇成了哪一行，避免追溯断链 |
| `first_sku_trace.json` | 从最终商品反向追到路径、驱动和原始金额行 |

## 开发人员怎样用

- 下载包内有 `alloc_windows_amd64.exe`（Win x64）、`alloc_linux_amd64`、`alloc_darwin_arm64`。
- 规则：`examples/<场景>/rule.json`，金额 CSV、驱动 CSV 与规则配套。
- 校验：`alloc_windows_amd64.exe doctor examples\split_merge\rule.json`
- 运行：`alloc_windows_amd64.exe run examples\split_merge\rule.json output_demo --workers 4 --disk-partitions 4`
- 查看链路：`alloc_windows_amd64.exe trace-first examples\split_merge\rule.json output_demo`
- 指定结果行：`alloc_windows_amd64.exe trace examples\split_merge\rule.json output_demo <ROW_ID>`。
- Java、Python、其他业务系统可使用 HTTP API 或调用可执行程序；它们**不必重写分摊公式**。

## 这次真正修改了什么

1. 同坐标多路径合并产生的中间血缘不再静默丢失：输出 `lineage_links.csv`。
2. 同名的外部来源行 ID，不同数据集或行号不会互相碰撞。
3. 输入 CSV 有重复表头、空表头会明确报错，不再默默选择最后同名列。
4. 规则 JSON 后附带第二段内容会被拒绝。
5. 新增 `trace` / `trace-first`，按结果反查来源，输出结构化 JSON；不会改变原有分摊金额。
6. 磁盘检查点保存并回放新血缘证据。**V16 检查点格式升级**：不能用旧版检查点直接恢复，需使用新的 checkpoint 文件夹。

## 有什么没有做

- 没有审批、没有重做前端。
- 没有声称百分百覆盖所有企业财务规则；例如按驱动缺失进行兜底、币种转换、期间跨期处理，需要明确定义并扩充配置语义。
- `trace` 是按单条结果回溯的诊断命令，针对百万行数据会进行顺序文件扫描；若需要高频查询全量血缘，下一阶段应接入独立持久血缘索引。
- 本次 Linux 测试通过，Windows 程序已交叉编译；公司 Windows 设备仍需完成实际验收。

**建议继续阅读：** `docs/V16_BACKEND_AUDIT_CN.md` 和 `docs/V16_QUICKSTART_CN.md`。
