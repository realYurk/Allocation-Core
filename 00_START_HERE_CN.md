# Allocation Core V17｜先看这里

这是一套后端优先、行业无关的多维费用分摊框架。**分摊金额算法没有因为 V17 的优化而改动**。原来两步骤、75/25 双驱动、三驱动、分叉合并、动态维度、并行、检查点和 HTTP 接口继续保留。

## Windows 用户：三个双击入口

1. 完整解压 ZIP（不能直接在压缩包内双击）。
2. 双击 `00_Windows_Diagnostic.bat`，检查程序和示例是否存在。
3. 双击 `18_Windows_Trace_Index_Demo.bat`，自动运行双路径分摊、建立快速血缘索引、做 SHA-256 检查，并打开追溯结果。
4. 双击 `19_Windows_Audit_Pack_Demo.bat`，自动运行两阶段分摊，把**完整结果和审计证据**无损压缩到 ZIP，并验证压缩包。原始 CSV 默认保留。

如果要修改费用或双因子比例，打开 `tools/Allocation_Studio_V15_Offline.html`。这只是易懂的辅助界面；正式的计算、追溯、审计由 Go 后端执行。

## V17 解决了什么

- 旧版每查一笔商品链路，要重新顺序读一遍大型 `ledger.csv`。现在用 `trace-index` 为结果行和来源金额池建立磁盘索引，查询只查相关分片。没有索引仍能用原先扫描模式；原始计算证据不变。
- 旧版千万行 `ledger.csv` 可以有数 GB。现在用 `audit-pack` 把结果、明细和血缘**原样保存成单个无损 ZIP**。有文件内容 SHA-256 校验、无损恢复命令、原文件默认保留。**压缩是计算完成后的另一步操作，不宣称计算本身提速**。
- 索引构建采用临时目录，完整建成后才发布。原 CSV 文件大小/修改时间变更时拒绝使用旧索引；`trace-index-verify` 再进行完整的 SHA-256 校验；索引分片损坏时拒绝返回不完整追溯。

## 技术命令（不懂代码可以跳过）

```bat
alloc_windows_amd64.exe trace-index runs\my_run
alloc_windows_amd64.exe trace-index-verify runs\my_run
alloc_windows_amd64.exe trace-first examples\split_merge\rule.json runs\my_run
alloc_windows_amd64.exe audit-pack runs\my_run runs\my_run_audit.zip
alloc_windows_amd64.exe audit-verify runs\my_run_audit.zip
alloc_windows_amd64.exe audit-extract runs\my_run_audit.zip restored_run
```

**注意：** 追溯索引是可随时重建的辅助文件，审计 ZIP 则保存所有完整证据。归档默认**不删除**原始 CSV；需要释放磁盘空间时，应先确认 `audit-verify` 成功且能够恢复，再由公司制定保留/清理流程。不是文件签名或可信第三方存证。

进一步阅读 `docs/V17_PERFORMANCE_AND_RISKS_CN.md` 和 `docs/V17_BEGINNER_GUIDE_CN.md`。
