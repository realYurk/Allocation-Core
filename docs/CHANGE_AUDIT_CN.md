# 代码身份清理与生产接入改造审计

## 要求与取舍

- **删除运行时硬编码产品版本**：现在命令帮助、HTTP 健康响应和运行摘要均输出 `Allocation Core` 或 `allocation-core`，不再输出 V20/0.18.0/0.14 这三个互相矛盾的产品标识。
- **不删除格式协议标识**：例如 `allocation-ir/2.0`、`/v1/jobs`、压缩审计格式和旧索引目录，仍是历史数据兼容所必需。这些不是营销/发行版本号；删除会破坏原有文件读取能力。
- **不再宣称代码文件冻结**：新增容量控制以及身份清理会改变部分 Go 文件和二进制。旧版哈希保存在 `BASELINE_SHA256_BEFORE_IDENTITY_CHANGE.json`，当前完整发行哈希在 `CORE_SHA256.json`；新增源码被重新审计。
- **金额算法不变**：在相同输入下，旧版与新版的制造业两步、双因子、分叉合并案例五份核心金额/血缘文件（包括压缩 CSV）逐字节一致；`run_summary.json` 的 engine 字段发生了预期变化，因此包含其哈希的 `evidence_stream_manifest.json` 也随之变化。
- **不做增量分摊**：每次都从完整源数据重新计算。

## 代码变动范围

1. HTTP / CLI / Summary 的发布身份字符串。
2. 原引擎函数入口添加可选的 `ALLOC_CAPACITY_POLICY` 参数检查（不改下游公式和数据遍历算法）。
3. 服务端接收前 / 排队后再检查可信容量预算。
4. 新增 PostgreSQL COPY 与 SQL Server `fast_executemany` 数据库接入适配器；它们不在 Go 金额内核里。
5. Go 源码文件改为能力名称，去掉历史开发代号；旧格式标识原样保留。

## 测试与暂未验证

- Go 全套单元测试、race detector、go vet。
- Windows AMD64/ARM64, Linux AMD64, macOS AMD64/ARM64 交叉编译。
- 旧版与新版关键 CSV 逐字节对照。
- SQLite 两次全量重算 / 事务发布 / 回滚；数据库适配器 `--check-only`。
- 可信容量策略在 CLI 引擎与 HTTP 提交阶段的真实拦截，丢失策略 fail-closed。
- PostgreSQL 与 SQL Server 的实库导入速度**未验证**；没有可访问数据库服务器或连接授权。
- 操作系统级磁盘配额、进程取消/超时、多节点调度以及审计证据异地备份仍属于部署工程，不能从本次预检推断已实现。
