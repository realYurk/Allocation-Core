# 生产容量准入与硬资源边界（开发/运维）

## 为什么必须做

一次分摊输出数量近似为金额池数 × 每池有效目标数 × 多因子/多路径输出倍数。这个公式是**预算上界的一种近似**，不能据此证明最终精确行数。内核不做增量计算，每次从原始数据全量重新计算。

## 两个互补的入口控制

**Go 原生执行保护**：配置进程环境变量 `ALLOC_CAPACITY_POLICY` 为**可信管理员配置文件的绝对路径**。`run`、`batch`、`workspace` 以及通过同一引擎执行的 HTTP 任务，进入分摊执行函数时都会重新检查预算和目标盘剩余可用容量；如果预算文件丢失、格式错误或超限，则直接拒绝执行。若不配置环境变量，则兼容原有单机操作，不开启强制预算功能。

**HTTP 提交前保护**：`alloc serve --budget-file BUDGET.json` 在接收 POST `/v1/jobs` 时检查预算，超限返回 HTTP 507，不会创建本次任务；任务从队列派发前会再次检查。生产应同时设置环境变量和 `--budget-file`（指向同一可信文件）。

在 Windows 中，进入 `core/` 后可以先运行 `26_Windows_Capacity_Preflight.bat`，然后运行 `27_Windows_Protected_Full_Run.bat`。脚本都用英文文件名、保留命令窗口，无需 Python。

## 预算文件字段（示例：core/examples/capacity_safe_budget.json）

```json
{
  "pools": 3600,
  "targets_per_pool": 1000,
  "factor_paths": 2,
  "result_bytes_per_row": 144,
  "evidence_bytes_per_row": 300,
  "overhead_percent": 150,
  "max_rows": 50000000,
  "max_peak_bytes": 107374182400,
  "reserve_free_bytes": 5368709120
}
```

- 前三个字段**必须由业务接入方按规则、数据特征独立估计**，不可从外部 HTTP 请求中的自由数字直接信任。
- `max_peak_bytes` 是本任务允许的**估算峰值存储预算**；`reserve_free_bytes` 是保留的磁盘空闲容量。
- 计算时不用 float 处理金额，但这些容量预算以整数/高精度整数运算。
- 预算指标使用预估行数和每行大小；复杂 DAG、热点金额池、压缩模式、其他任务同时运行可能使实际峰值偏离计划值。
- **这不是文件系统配额或实时中止机制。** 真正的硬保护必须配合独立磁盘卷或配额、进程/作业超时、外部监控和并发任务上限。
- 启动前检测不能阻止程序运行中其他进程耗尽磁盘；不能宣称“永远写不满”。

## 入口示例

Windows CMD：

```bat
set "ALLOC_CAPACITY_POLICY=D:\Allocation\capacity_budget.json"
alloc_windows_amd64.exe capacity "%ALLOC_CAPACITY_POLICY%" D:\Allocation\output
alloc_windows_amd64.exe run rule.json D:\Allocation\output\unique_run --workers 4
alloc_windows_amd64.exe serve --root D:\Allocation\inputs --store D:\Allocation\service_data --budget-file "%ALLOC_CAPACITY_POLICY%"
```

测试 50 万目标且 3,600 金额池：按双份行宽、150% 开销估计约 18 亿终端行，约 1.2 TB 空间预算，**必然被示例上限阻止**。该数字不是引擎实际已写出的数据量。

## 已知未覆盖路径

- 没有配置 `ALLOC_CAPACITY_POLICY` 的直接调用仍按旧逻辑运行；这种用法不应被认为已经具备生产磁盘硬限制。
- 独立执行 Vault 归档、数据库导出等功能产生的磁盘量不由核心计算入口预算单独约束；生产须再配磁盘监控与容量预算。
- 本方案不提供多进程间全局剩余配额原子预留；大量并发任务仍需要外部调度和独立卷配额。
- HTTP `--budget-file` 可以被管理员修改，服务会重新加载并 fail-closed，不应该允许普通调用方修改这个文件。
