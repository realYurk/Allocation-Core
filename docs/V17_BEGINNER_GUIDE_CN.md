# V17 小白操作手册：完整审计无损归档和快速结果追溯

## 要解决的问题

公司需要对上百万甚至千万条费用分摊结果保留计算证据。`ledger.csv` 可能几 GB。以前为了查一个 SKU 的费用来源，还要等待整个文件扫描。

V17 保留原有多步分摊引擎和简单的 HTML 前端，新加入两个工具：

- **快速追溯索引**：根据一条结果行号，快速查到哪一步、哪个驱动、哪些上游金额池。
- **审计压缩包**：把结果、分摊明细、守恒记录、来源映射和路径关联一起无损存档。

## 零基础：不用输入命令

下载完整 V17 ZIP 并右键「全部解压」。

1. `00_Windows_Diagnostic.bat`：确认程序能在 Windows 运行。
2. `18_Windows_Trace_Index_Demo.bat`：体验“双驱动/分叉合并→查商品来源”。结果在 `v17_runs/trace_.../trace_first.json`。
3. `19_Windows_Audit_Pack_Demo.bat`：体验“完成分摊→生成归档 ZIP→校验归档”。不会自动删除分摊结果。

所有新增 BAT 文件名都使用英文和 ASCII，不重复过去中文文件名乱码问题。

## 新公司接入

- **需要什么数据？** 金额 CSV（科目、期间、组织、金额）、驱动 CSV（销量/工时/收入及其对应维度）、JSON 规则。
- **如何开始？** 打开 `tools/Allocation_Studio_V15_Offline.html`，先从内置多因子/多步骤示例改。或用 `generate` 命令生成新公司模板。
- **如何运行？** `alloc_windows_amd64.exe doctor rule.json`，然后 `alloc_windows_amd64.exe run rule.json run_001 --workers 4 --disk-partitions 8`。
- **检查什么？** `reconciliation.csv` 每个金额池 PASS；必要时 `compare rule.json run_001 period,account` 输出前后对比。
- **如何查一笔？** 先 `trace-index run_001`，再 `trace rule.json run_001 ROW_ID`。索引只提高查询速度，不改变金额。
- **如何归档？** `audit-pack run_001 run_001_audit.zip`，再 `audit-verify run_001_audit.zip`。需恢复时 `audit-extract run_001_audit.zip restore_001`。

## 三条特别重要的限制

1. **ZIP 并没有令分摊计算更快。** 它是运行完成后的无损归档步骤，用来节省长期存储成本，归档还要额外占用一些 CPU 和 I/O。
2. **索引并非计算血缘本身。** 它是针对原始 CSV 的查询辅助结构。旧索引过期会被拒绝。要做强校验，运行 `trace-index-verify`。
3. **不直接删除原始文件。** 在签署公司存储、审计保留策略之前，不要凭压缩率就替换原始生产资料。V17 没有审批和正式数据库事务。

## 中文和 Windows

业务 CSV 是 UTF-8 BOM，Excel 新版本应能识别中文。如旧版 Excel 打开乱码，使用「数据 → 从文本/CSV → UTF-8」导入。Windows BAT 文件名使用 ASCII，文件内部先 `chcp 65001`。公司设备仍需做真实验收。
