# Clean Core Architecture / 清洁内核架构

```text
AllocationCore.exe
  ├── Studio HTTP workspace / 统一业务工作台
  │     ├── Multi-account projects and jobs / 多科目任务
  │     ├── In-process Cube Explorer / 同进程对比
  │     └── Rule designer / 规则编辑
  ├── Exact engine / 精确计算内核
  ├── Generic rule converter / 通用规则转换
  ├── Driver indexes and disk partitioner / 因子索引与磁盘分区
  ├── Evidence + Vault / 审计及版本档案
  └── CLI subcommands / 命令行子命令
```

All components live in **one Go module** `allocationcore`, most under `internal/`; no extra executable files or scripting runtimes are necessary for normal Windows use. / 单一 Go 模块，不需要额外可执行程序或脚本运行环境。

**Important distinction / 重要说明**: the workspace and Explorer are truly served within one process. A cancellable execution job starts a **child process of the same exact executable** (`AllocationCore.exe core run ...`, `AllocationCore.exe multi ...`) for OS-level termination and fail-closed behavior. No binary is unpacked or fetched. This is **one executable distribution**, not a claim that every operation is single-process. / UI 真正合并为同进程；长任务为了安全取消仍使用同一个文件的子进程，不再释放七个不同 EXE。

### Stable money semantics / 不可牺牲的金额语义

- Fixed-point `int64` amounts, `big.Int` intermediate operations. / 定点金额、大整数中间运算。
- Deterministic largest-remainder apportionment. / 确定性尾差分配。
- Conservation for each configured pool and successful run. / 逐池守恒。
- No permissive fallback for unknown weights, dimensions, or source rules. / 未知规则、维度、权重必须明确拒绝。
- Stage graphs, trace evidence and atomic final publication remain intact. / 多阶段、审计和最终原子发布保留。
- Correctly handle the `math.MinInt64` subtraction edge case without altering normal arithmetic. / 修复最小负整数边界，不改变常规计算语义。

### Performance strategy / 性能策略

Current Go engine uses sparse grouping, parallel pools, optional disk partitions and content-addressed Driver cache. The cleanup itself does not prove a speedup. The next performance gate requires CPU/heap/I/O profiling for million-to-hundred-million row workloads under identical settings before adopting integer-coded dimensions or custom columnar storage. / 清理本身不代表提升性能；先用同条件真实基准剖析，再决定坐标编码等重构。

## V2 pool scheduling / V2 分摊池调度

A stage creates a bounded set of workers **once**. Pools are still processed in batches of at most `2 × workers`, and the coordinator publishes each batch in sorted pool order. Workers do not write shared output files. / 每个阶段创建一次固定工作池；内存仍被 `2 × workers` 的批次限制；写出严格按有序分摊池顺序执行，财务结果与血缘顺序不随线程执行顺序变化。

This improves repeated goroutine setup cost without changing rounding, rule routing, evidence contracts, or the input/output file format. It does not remove multi-account child-process overhead. / 该优化只降低协程调度成本，不改金额计算与审计协议，千科目子进程开销仍有进一步优化空间。
