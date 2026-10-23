# Allocation Core

一套规则驱动、多维度、多阶段的分摊计算引擎。输入来源金额、驱动数据和规则文件，输出分摊结果以及可审计的证据。

引擎不绑定任何行业。零售、制造、化妆品，或者其他需要把公共费用分摊到商品、品牌、区域、科目的业务，用的都是同一套内核；变的只是规则文件和输入 CSV，不是计算逻辑。

仓库内所有示例数据均为虚构，不包含任何真实业务资料。

## 它做什么

- **动态维度**：期间、科目、组织、地区、品牌、SKU、币种，维度的名称和数量由规则定义，不是写死的。
- **多因子分摊**：一笔费用按多个驱动拆分，例如 75% 按材料用量、25% 按收入。
- **多阶段分摊**：前一阶段的输出作为后一阶段的输入，例如「工厂费用 → 商品系列 → 商品」。
- **DAG 拓扑**：阶段可以分叉、合并，环会被拒绝。
- **守恒核对**：每个金额池都会校验输入输出是否相等，结果写入 `reconciliation.csv`。
- **审计链路**：`ledger.csv` 记录每行用了哪个驱动、分母、比例和结果；`lineage_links.csv` 记录合并行如何回溯到上游行。
- **整数运算**：金额用最小货币单位（int64）计算，余数确定性分配，避免浮点尾差。
- **并发**：多线程，支持磁盘分区和外部驱动索引，适合较大数据量。
- **检查点 / 断点恢复**：长任务可以保存进度并恢复。
- **HTTP 服务**：同一套引擎可以跑成本机 HTTP 服务，供 Java、Python 或其他系统调用。

## 目录结构

```
cmd/alloc/main.go        CLI 入口
internal/engine/         解析、编译、运行、doctor、trace、compare、generate
internal/batch/          批量任务运行器
internal/service/        HTTP 服务
internal/workspace/      持久化任务工作区（publish/submit/verify/diff）
examples/                五个示例场景，含规则 + 金额 + 驱动 CSV
adapters/                Java 和 Python 客户端示例
tools/                   离线规则生成器（HTML）
docs/                    指南、评审、性能说明
alloc_*                  预编译的 Windows / Linux / macOS 二进制
```

## 环境要求

- **Windows / Linux / macOS**：直接用发行包里的预编译二进制，不需要 Go 或 Python。
- **从源码编译**：Go 1.21 或更高。

## 快速开始

随便挑一个示例场景跑一遍。Windows 下项目根目录的 `.bat` 文件就是对这些命令的封装。

```bash
# 校验规则和数据文件（只查表头契约，不做计算）。
./alloc_windows_amd64.exe doctor examples/split_merge/rule.json

# 运行分摊。
./alloc_windows_amd64.exe run examples/split_merge/rule.json output_demo --workers 4 --disk-partitions 4

# 把第一条结果行反查回来源。
./alloc_windows_amd64.exe trace-first examples/split_merge/rule.json output_demo

# 反查指定行。
./alloc_windows_amd64.exe trace examples/split_merge/rule.json output_demo <ROW_ID>

# 按科目和期间对比分摊前后。
./alloc_windows_amd64.exe compare examples/split_merge/rule.json output_demo account,period
```

Linux 或 macOS 换成对应的二进制（`alloc_linux_amd64` 或 `alloc_darwin_arm64`）。

### Windows 双击

1. 完整解压发行 ZIP，不要在压缩包内双击 BAT。
2. 双击 `00_Windows_Diagnostic.bat` 检查环境。
3. 双击 `16_Windows_Run_Trace_Demo.bat`，跑「75% + 25% 两路合并」示例并打开输出文件夹。
4. 双击 `17_Windows_Two_Stage_Demo.bat`，跑「费用 → 商品系列 → 商品」示例，同时生成 `comparison.csv`。

## 输入与输出

一个场景由三样东西组成：

- **金额 CSV**：每行一笔来源金额，包含维度列和金额列。
- **驱动 CSV**：每行一个分摊目标，包含权重字段和选择目标所需的维度列。
- **规则 JSON**：维度、来源归集、目标筛选、驱动、份额、阶段顺序、守恒键。

运行后生成这些文件：

| 文件 | 用途 |
|---|---|
| `results.csv` | 最终分摊金额。一个商品如果被多个驱动分摊过，会出现在多行上。 |
| `ledger.csv` | 每行的驱动、分母、比例、结果。 |
| `reconciliation.csv` | 每个金额池的守恒核对（PASS / FAIL）。 |
| `source_to_pool.csv` | 来源金额进了哪个池。 |
| `lineage_links.csv` | 合并行的父子关系，保证上游行不丢失。 |
| `comparison.csv` | 按维度聚合的分摊前 / 后 / 差额（由 `compare` 生成）。 |

CSV 输出带 UTF-8 BOM，Excel 一般能正确打开中文表头。如果还是乱码，用 Excel 的「数据 → 自文本/CSV」按 UTF-8 导入。

## CLI 命令

```
alloc validate RULE.json
alloc explain RULE.json
alloc doctor RULE.json
alloc compare RULE.json OUTPUT_DIR period,account
alloc trace RULE.json OUTPUT_DIR ROW_ID
alloc trace-first RULE.json OUTPUT_DIR
alloc graph RULE.json
alloc run RULE.json OUTPUT_DIR [--workers N] [--disk-partitions N]
           [--checkpoint-dir DIR] [--resume] [--driver-disk-index] [--driver-cache-dir DIR]
alloc batch MANIFEST.json OUTPUT_DIR [--jobs N] [--workers N] [--disk-partitions N]
alloc generate RULE.json NEW_COMPANY_DIR
alloc serve --root ./examples --store ./service_data --listen 127.0.0.1:8787 --jobs 2 [--token SECRET]
```

### 持久化工作区

```
alloc workspace publish RULE.json WORKSPACE
alloc workspace rules WORKSPACE
alloc workspace diff WORKSPACE RULE_ID LEFT_REVISION RIGHT_REVISION
alloc workspace submit RULE.json WORKSPACE [--workers N]
alloc workspace list WORKSPACE
alloc workspace history WORKSPACE
alloc workspace verify-latest WORKSPACE
alloc workspace inspect WORKSPACE JOB_ID
alloc workspace verify WORKSPACE JOB_ID
alloc workspace recover WORKSPACE
alloc workspace retry WORKSPACE JOB_ID [--workers N]
```

## 核心概念

- **AS_IS / TOTAL / SOURCE** 是三个最容易配错的模式，`docs/` 里有通俗解释。简单说：`AS_IS` 让每个来源成员保持独立金额池，`TOTAL` 把这一维度合并成一个池，`SOURCE` 让目标维度保持不变。
- **两种不同的份额**：`stages[].inputs[].share` 是阶段之间边的路由份额；`stages[].allocations[].share` 是单个阶段内多因子的费用份额。两者各自激活项之和都必须等于 1，且不能混用。
- **多因子不是多步骤**：单个阶段内驱动份额合计必须 100%；阶段之间边的份额也必须独立满足 100%。

## 系统对接

Java、Python 和其他系统调用同一套后端，不必重写分摊公式：

- **CLI**：运行 `alloc run ...`，读取输出 CSV。
- **HTTP**：启动 `alloc serve`，`POST /v1/jobs` 提交任务，`GET /v1/jobs/{id}` 轮询，下载 `results.csv` 等。见 `docs/02_HTTP_Service.md`。
- **Java**：见 `adapters/AllocationHTTPClient.java`（Java 8+ 标准库）。
- **Python**：见 `adapters/python_http_client.py`（仅标准库）。

HTTP 服务只监听回环地址，不是面向公网的服务，没有 RBAC、TLS 或完整身份管理。

## 性能

Linux amd64、Go 1.23.2、合成数据（100 科目、12 期间、1000 商品，费用 → 系列 → 商品，4 线程、16 磁盘分区）实测：

| 版本 | 最终记录 | 两阶段金额池 | 耗时 | 峰值常驻内存 |
|---|---:|---:|---:|---:|
| V15 基线 | 1,200,000 | 3,600 | 5.52 秒 | 约 39 MB |
| V16（血缘 + 校验） | 1,200,000 | 3,600 | 5.33 秒 | 约 45 MB |

两版之间 `results.csv`、`ledger.csv`、`reconciliation.csv` 的 SHA-256 完全一致。耗时的微小差异只是单次运行波动，不是真实提速。数据严重倾斜的多阶段、极端多因子、以及 10M+ 驱动行仍需专项压测。

## 它不是什么

- 不是审批系统，也不会写回你的 ERP 或数据库。那些属于对接项目。
- 不声称覆盖所有企业财务规则。缺失驱动兜底、币种转换、期间跨期，都需要明确配置语义，而不是静默默认。
- `trace` 针对单行做顺序文件扫描。高频全量血缘查询需要持久化血缘索引。
- 离线 HTML 工具只是规则起点，不是生产级 DAG 编辑器。

## 文档

- `docs/`：快速上手、接入指南、新公司接入、HTTP 服务、驱动缓存、评审和性能说明。
- `README.md`：本文档的英文版。
- `docs/TUTORIAL.md`：零基础新手教程。

## 许可证

仓库目前未包含许可证文件。示例数据为虚构，无业务含义。
