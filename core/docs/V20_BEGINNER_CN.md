# Allocation Core V20｜零基础：14 个场景与多版本如何不撑爆磁盘

## 先看结论

**V20 没有更改分摊公式**，只是为计算结果增加一层独立的、可以关掉不用的「归档仓库 Vault」。你原来的 Windows BAT、双因子、三因子、DAG、多步骤、守恒、ledger、压缩证据、断点、HTTP、索引、trace 等命令继续使用。

以前每次运行分别产生 results、ledger、索引、检查点等文件。现在可把**完成且 PASS 的结果**统一登记入 Vault：

- 同样的文件内容只存一份（SHA-256 内容寻址，按文件去重）。
- 普通 CSV 在 Vault 中按字节无损 gzip；已经是 `.gz` 的文件保持原始字节，不再二次压缩。
- `official` 正式版本不会被 V20 自动清理；`draft` 临时试算支持按场景保留最新 N 个。
- 默认 `prune`、`release-source`、`gc` **只显示预览，不删除**；执行必须显式加 `--apply`。
- 还原后的所有输出文件与存入 Vault 前**字节一致**。CSV 的中文 BOM、完整计算证据和 SHA-256 都保留。
- `trace_packed_v19`、临时中间分区等属于**可重建索引**，默认不进入长期归档。

## 使用步骤（Windows 解压即用，不需要 Go、Python）

1. ZIP 下载后，在资源管理器里**全部解压**，不要在 ZIP 内运行 BAT。
2. 双击 `24_Windows_Vault_Lifecycle_Demo.bat`：运行两阶段合成案例，登记两个 draft、一个 official，显示复用节省的磁盘空间，预览清理规则。
3. 双击 `25_Windows_Vault_Prune_Preview.bat`：只看清理计划，**不会删除文件**。
4. 打开 `v20_demo/vault/runs` 可看到每一次登记的元数据；不要手动修改这些 JSON。
5. 在实际使用时，把 `FC-10+2` 换成你的场景代码，把 `WIP-03` 换成你的计算版本。

## 最常见的命令（在 V20 所在文件夹打开 CMD）

```bat
alloc_windows_amd64.exe run examples\manufacturing\rule.json my_run --workers 4 --disk-partitions 4 --evidence-gzip

alloc_windows_amd64.exe vault add my_vault FC-10+2 WIP-01 draft my_run
alloc_windows_amd64.exe vault add my_vault FC-10+2 FINAL official my_run

alloc_windows_amd64.exe vault stats my_vault
alloc_windows_amd64.exe vault list my_vault
```

`vault add` 输出一个长的 `id`，**这个 ID 才是该次归档的唯一标识**，不要把场景名或版本名当成 ID。重复的试算版本可以不同 ID 并存，但重复内容不会再次占用一整份存储。

复制这个 ID 后，可以检查和还原：

```bat
alloc_windows_amd64.exe vault verify my_vault 归档ID
alloc_windows_amd64.exe vault restore my_vault 归档ID restored_run
```

重要：`restored_run` 必须事先不存在，防止意外覆盖。

## 针对 14 场景 × 10 次试算的推荐策略

FC 0+12、FC 1+11、……、FC 12+0（共 13 种）+ ACT（共 14 种）。**不用默认保留 140 份完整中间输出。**

- 按场景分组，所有正式发布的版本 `official` 保护，不参加试算清理。
- 每个场景通常保留最新 **2 个 draft**（用于对比当前和上一版）；如果你们经常比较三版，可以改成 3。
- 其他 draft 等完成 7 天后，才进入清理候选。这个保留期按公司业务自行调整。
- 初次使用一律先预览清理计划：

```bat
alloc_windows_amd64.exe vault prune my_vault --keep-drafts 2 --older-than-days 7
```

- 确认预览里的 `remove_ids` 无误，再正式执行：

```bat
alloc_windows_amd64.exe vault prune my_vault --keep-drafts 2 --older-than-days 7 --apply
```

- 删除不再被引用的孤儿对象（例如注册进程意外中断留下的碎片）：

```bat
alloc_windows_amd64.exe vault gc my_vault
alloc_windows_amd64.exe vault gc my_vault --apply
```

**注意**：默认并不自动删除你现有的业务结果目录。成功归档和核验后，如果确实想回收源目录输出文件：

```bat
alloc_windows_amd64.exe vault release-source my_vault 归档ID my_run
alloc_windows_amd64.exe vault release-source my_vault 归档ID my_run --apply
```

这个命令**仅删除档案中已完整验证且 SHA-256 一致的结果文件**，不会递归删文件夹，不删除 `run_summary.json`，更不会自动删除你的原始费用或销量数据。若你还想继续使用原目录做 trace，请先不要清理，或者先通过 `vault restore` 恢复到新目录。

## 如何查看数据库接入样例

```bat
alloc_windows_amd64.exe vault export-db my_vault 归档ID my_bulk.csv
```

它把结果整理成五列 `run_id,row_id,terminal_stage,amount,dimensions_json` 的批量导入 CSV。不同公司的自定义维度放入 `dimensions_json`，不需要每加一个维度就改数据库表结构。执行此命令**不会自动连接或写入任何数据库**。

详细的 PostgreSQL/SQL Server/MySQL 批量入库建议见 `docs/V20_DATABASE_INTEGRATION_CN.md`。

## 哪些事情 V20 还没有做？

1. **没有自动全量去重业务金额行**。这属于更复杂的增量数据存储，数据不同就必须保留新的实际信息。
2. **尚不支持无限版本数量下恒定的磁盘空间**。不同内容的正式版本仍会占空间；可以设置归档/冷存储策略。
3. **Vault 保存的是计算输出和证据，不等于已经备齐可重演的全部输入**。想脱离上游系统重算，仍须保留当次规则和 Driver / 金额输入快照（此前的任务工作区支持快照）；本版不假称输出归档可单独重算。
4. **无审批、无自动覆盖正式版本**。正式版本不参与自动清理，业务必须明确决定是否做其他保留策略。
5. **这是单机文件 Vault，不是多机事务数据库或分布式对象存储。** 同一仓库同时进行修改会受维护目录锁约束，异常中断可能遗留锁目录；必须排查进程后由管理员处理，不可盲目删锁。
6. **SHA-256 证明文件字节未变，不是抵御恶意篡改的数字签名**。正式合规要求需要不可变对象存储、外部签名或 WORM 机制。

我们优先保留你的既有计算核心，先解决「存在哪、什么时候保留、何时清理、怎样复用、怎样批量交给数据库」这五个实用问题。
