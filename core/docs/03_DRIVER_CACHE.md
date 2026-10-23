# V14｜可复用 Driver 磁盘索引：原理、操作与边界

## 这解决什么问题？

一家企业可能对 100 个科目、12 个月不断试算不同分摊方案，但销量/工时/收入数据经常相同。V13 每次运行都要重新解析、排序和写出大 Driver 索引。V14 可以把**索引作为可验证的衍生资产**保存在单独缓存目录，多次运行复用。

## 命令行

Windows：双击 `10_Windows_Reused_Driver_Index.bat`，它会对同一条两阶段规则执行两次。第一次建立索引，第二次从 `v14_cache/` 读取。

开发者使用：

```bat
alloc_windows_amd64.exe run "examples/manufacturing/rule.json" "runs/first" --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir "v14_cache"
alloc_windows_amd64.exe run "examples/manufacturing/rule.json" "runs/second" --workers 4 --disk-partitions 8 --driver-disk-index --driver-cache-dir "v14_cache"
```

注：输出目录每次必须不同；这是避免覆盖历史财务试算的安全设计。

## 缓存命中的前提

缓存键采用 SHA-256，绑定以下信息：

- 原始 Driver CSV 的**实际文件内容**（不是只看文件名/修改时间）。
- 驱动数值字段，如 `units` 或 `hours`。
- 维度列表及维度层级结构。
- 对应 Allocation 的筛选条件。
- 目标成员选择方式和 Include/Exclude 条件。

只要其中任何影响含义的值发生变化，就建立新索引。相同文件内容和规则语义可以在不同任务之间命中缓存。

磁盘布局为 `cache/<sha256>.jsonl` 与 `.meta.json`。元数据记录索引文件哈希和各驱动范围位置；读取时重新验证数据完整性。并行首次构建使用排他 `.lock` 文件和完整发布标志，以防读到半成品。

## 怎么清除/恢复？

确保没有任何任务使用 `v14_cache/` 后，可以整体删除这个目录；下次运行会重新建立。**不要运行中手工删除缓存**。如果程序提示锁等待超时，先确认没有其他任务正在建索引或没有残留的已退出程序；再由 IT 人员移除确认已过期的 `.lock` 文件。

## 不能过度宣传的部分

- 它仍需要读取输入计算 SHA-256，并读取缓存文件验证哈希，因此缓存不是“零 I/O”。
- 缓存不是数据库查询服务；极端宽维度、海量成员集中在同一个金额池时仍可能占用高内存。
- 不保证有缓存就一定显著加快**整个分摊**，若 CPU 主要花在分摊/账务输出，索引节约的时间有限。
- 本地文件缓存适合单机或共享同一可信文件系统上的受控实例；没有实现跨服务器的分布式租约、清理策略和磁盘配额。
