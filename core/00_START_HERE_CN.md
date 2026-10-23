# Allocation Core V20 | 先看这里

**这次不是新的计算算法。** 它保留你已经验证过的全部分摊后端功能，只新增：

1. **场景/版本归档 Vault**：同内容按 SHA-256 去重，普通 CSV 压缩存储；完全恢复。
2. **正式与试算分开**：正式版本保护；试算版可按场景保留最近 N 版。
3. **安全清理**：先预览，只有加 `--apply` 才清理；始终不会自动覆盖正式版。
4. **高性能 DB 接入出口**：把动态维度转成批量 CSV，交给目标数据库 `COPY` / `BULK INSERT`，而非逐条 INSERT。

## Windows 第一次使用

- 下载 ZIP → **全部解压**。
- 双击 `24_Windows_Vault_Lifecycle_Demo.bat`，看三个版本如何去重。
- 双击 `25_Windows_Vault_Prune_Preview.bat`，仅预览清理，不会删除文件。
- 详细操作：`docs/V20_BEGINNER_CN.md`。
- 数据库方案：`docs/V20_DATABASE_INTEGRATION_CN.md`。
- 实测与风险：`docs/V20_PERFORMANCE_AUDIT_CN.md`。

依旧可以运行老版本的 `01_Windows_Run_Two_Stage.bat`、 `22_Windows_Compressed_Trace_Demo.bat`；数学引擎没有修改，业务数据都为虚构示例。所有脚本、程序、内部路径使用 ASCII 文件名，避免 Windows ZIP 文件名乱码。

**界限**：当前 Vault 默认保存已完成结果及完整计算证据，未自动打包所有原始输入，因此独立归档不等于能从零重演一次分摊。正式复现应额外保留规则与输入数据快照。V20 不是数据库客户端，也不会自动将千万行写入 SQL。
