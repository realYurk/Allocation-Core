# Allocation Core · 集成发行包

**产品代码不再硬编码发行版本**。唯一计算核心是 Go 独立程序或 HTTP 服务；Java JAR / Python 代码均为客户端，不会复制金额算法。

## 第一次使用

1. Windows：进入 `core/` 双击 `00_Windows_Diagnostic.bat`，再运行 `01_Windows_Run_Two_Stage.bat`。
2. 开发人员：`core/alloc_windows_amd64.exe run core/examples/manufacturing/rule.json output`。
3. 生产容量预算：先阅读 `docs/PRODUCTION_ADMISSION_CN.md`，并为生产执行环境配置 `ALLOC_CAPACITY_POLICY`。默认示例不强制启用，以免改变旧使用方式。
4. 接入 Java / Python：看 `docs/02_Developer_Integration_CN.md`。
5. PostgreSQL / SQL Server 实际批量导入：看 `docs/PRODUCTION_SQL_CN.md` 和 `integration/python/sql_bulk_publish.py`。需在你的数据库和驱动环境自行部署并测试。

## 明确不做

- 不做增量分摊：每次从原始数据全量重算。
- 不为另一门语言重写金额算法。
- 不要求 Redis，不内置用户公司的数据。
- 不把容量估算说成 OS 磁盘限额。

## 版本与兼容性

`help`、HTTP `/healthz`、运行摘要不再显示硬编码产品版本。`allocation-ir/2.0`、`/v1/jobs`、索引与快照的格式标识保留用于向后兼容；FC 业务 `version` 字段保留用于区分试算。

旧版发布说明仅作为历史材料保存于 `docs/`。当前实际可执行行为以本说明和新回归测试为准。
