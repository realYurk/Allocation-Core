# Allocation Core 集成发行版 V21 —— 先看这里

> **这是 V20 计算核心的接入套件，不是新的分摊算法版本。**
> 不修改金额计算、尾差、DAG、驱动筛选、守恒、账本或血缘。每次计算都从原始输入 **完整重算**，不做增量复算，也不引入审批。

## 你应该打开哪份文件？

| 角色 | 第一份文档 |
|---|---|
| 没有编程经验，只想知道能否放进自己公司系统 | `docs/01_Beginner_CN.md` |
| Java / Spring Boot 开发人员 | `docs/02_Developer_Integration_CN.md`，配合 `integration/java/allocation-client-java8.jar` |
| Python 开发人员 | `docs/02_Developer_Integration_CN.md`，配合 `integration/python/http_client.py` |
| 数据库/ETL 开发人员 | `docs/03_SQL_Database_Integration_CN.md` |
| 技术负责人/部署人员 | `docs/04_Boundaries_Security_Acceptance_CN.md` |

## 产品真正的形态

```
[现有 Java / Python / ERP / 模型系统]
  ├── 取数：SQL SELECT / 数据接口 / CSV / 文件
  ├── 调用：HTTP (推荐) 或本机 EXE/CLI
  ├── 配置：allocation-ir/2.0 JSON
  ▼
[唯一计算核心：Go EXE / 常驻 HTTP Service]
  ├── 全量重算、多步 DAG、多因子、守恒、血缘
  ├── 输出：results.csv、reconciliation.csv、压缩证据
  ▼
[接入方的数据适配器]
  ├── 验证全量结果
  ├── 批量写入新 run_id
  └── 原子切换当前有效版本
```

**Java JAR 是轻量 HTTP 客户端，不是另一套 Java 计算核心。** Python 文件也是客户端，不是 Python 重新实现分摊算法。默认不需要 Redis。

## 3 个最短上手动作

1. **只想试验计算**：Windows 双击 `core/01_Windows_Run_Two_Stage.bat`，打开输出目录。
2. **想验证 SQL 读写整条链路**：电脑有 Python 时执行（示例库是 SQLite，不需要 pip）
   ```bash
   python integration/python/sqlite_full_run.py --workspace ./demo_workspace --reset-demo
   python integration/python/sqlite_full_run.py --workspace ./demo_workspace --amount 2400.00
   ```
   两次运行都会从头计算，SQL 中保留历史，当前有效指针切换到第二次。
3. **Java 项目接入**：使用 `integration/java/allocation-client-java8.jar`，参考 `docs/02_Developer_Integration_CN.md`。项目里仍要自行完成来源数据映射、数据库批量导入及事务发布。

## 已测试/未测试

- ✅ 原 V20 Go 核心未修改，文件 SHA256 可对照 `CORE_SHA256.json`。
- ✅ SQLite 示例真正执行 SQL 取数 → Go 全量计算 → 守恒检查 → 单事务发布；包括第二次完整重算与生效版本切换。
- ✅ Java 8 JAR 在 HTTP 服务上真实提交任务；Python HTTP 客户端真实提交、等待、下载结果。
- ⚠️ PostgreSQL/SQL Server/MySQL/Oracle 的 SQL 是接入模板，当前环境没有这些数据库服务，**不宣称已对真实服务器完成压测和集成验收**。
- ⚠️ 单机示例不包含 Kubernetes/RBAC/TLS/分布式高可用；新公司上线前要由自身运维和 DBA 验收。
