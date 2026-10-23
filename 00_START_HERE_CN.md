# Allocation Core V19｜先看这里

## 这一版做了什么

V19 保留 V18 的全部计算能力，新增**压缩证据直接查询**：不用将 `ledger.csv.gz` 解压成几 GB 的明文文件，就可以反查商品费用沿多步骤、分叉、合并的完整上游路径。

**金额算法没有修改。** 这是可选的查询加速模块，而不是重新实现分摊。

## Windows 零基础操作

1. 下载完整 ZIP，右键「全部解压」，不要在压缩包预览窗口内运行 BAT。
2. 双击 `22_Windows_Compressed_Trace_Demo.bat`。它会运行虚构的双因子分叉合并案例，自动创建压缩证据、建立索引、反查并验签。
3. 在自动打开的结果目录中看 `trace_first.json`：可以看到同一个最终商品经过两条分摊支路，再追溯到原始费用行。
4. 双击 `23_Windows_Verify_Trace_Index.bat`，复核数据与索引有没有损坏。
5. 如果无法运行，先双击 `00_Windows_Diagnostic.bat`，确认程序架构与文件存在。

所有 BAT 文件名为英文，业务示例保留中文。仍然无需安装 Python、Go 或 Node.js。

## 新公司接入

原有规则格式与 Java/Python/HTTP 集成方式都不变。对于旧任务：

- 如果目录里有 `ledger.csv.gz`、`source_to_pool.csv.gz`、`lineage_links.csv.gz` 及 `evidence_stream_manifest.json`，**不需要重算费用**，直接运行 `alloc_windows_amd64.exe trace-pack-index <结果目录>` 即可创建索引。
- 用 `alloc_windows_amd64.exe trace-pack-verify <结果目录>` 做完整校验。
- 用 `alloc_windows_amd64.exe trace RULE.json <结果目录> <结果行ID>` 追溯指定结果。V19 自动发现压缩索引。
- `trace-first` 可查第一条结果，无需复制行 ID。

### 需要特别注意

- 建立压缩索引会一次性扫描现有压缩证据；**首次建索引不可能零成本**。
- 索引是可删除、可重建的衍生数据；不能代替原始 gzip 审计文件。
- 默认保留 V18 的 gzip 审计，因此索引会**额外占磁盘空间**，不会神奇减少原有文件大小。
- `trace-pack-verify` 会做全量 SHA-256 校验，比单笔查询慢得多，适合归档、发布或定期核验。
- 目前能追踪业务因果链路和分摊金额池；不能把合并后每一分钱自动精确归因给某一张凭证。
- 前端保留 V15 的简洁示例；核心重心仍是后端。
