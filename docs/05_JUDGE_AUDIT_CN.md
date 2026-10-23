# V21 集成发行版独立审计报告（判官视角）

审计时间：2026-10-24　机器：i5-12490F / 16 GB RAM / Windows

对 V21 集成发行版（V20 计算核心 + 接入套件）做独立攻击性测试。核心计算引擎 V20 未改动（与 V19 相同），本轮重点是**新增的 Vault 生命周期归档、DB 出口、SQL/Java/Python 集成客户端**。

结论先行：**核心冻结声明属实，Vault 和集成链路没有会损坏数据或算错钱的 bug**，但有几个能力不一致和工程边界需要正视。

---

## 一、结论速览

| 维度 | 判定 | 说明 |
|---|---|---|
| 核心冻结（156 文件 SHA） | ✅ 属实 | 双向核验 0 缺失 0 篡改 0 多余 |
| Vault 生命周期 | ✅ 通过 | 24 项黑盒测试全过 |
| 篡改/损坏 fail-closed | ✅ 通过 | 对象损坏、源篡改、reconciliation 造假全部拒绝 |
| SQL 集成全链路 | ✅ 通过 | 两次全量重算 + 指针切换 + 回滚正确 |
| 金额精度 | ✅ 通过 | precision=6 微单位原样保留，不走 float |
| Java 客户端 | ✅ 通过 | `--release 8` 编译通过，JAR 与源码逐字节一致 |
| 并发锁 | ✅ 通过 | 目录锁正确互斥 |
| 能力一致性 | ⚠️ 有缺口 | Java 下载白名单漏文件、版本号多处不一致 |

---

## 二、测试覆盖明细

### A. 核心冻结（CORE_SHA256.json）

- 声明的 156 个文件 SHA256 **全部匹配**；反向核验 `core/` 目录**无多余文件**。
- "核心冻结、未修改 V20"的声明**属实**。

### B. Vault 生命周期（24 项黑盒，全过）

用真实二进制跑完整链路：

- `add` 三版本（2 draft + 1 official），同内容**去重**生效（unique < 3×files）
- `stats` 压缩生效（stored < logical），统计正确
- `verify` / `restore` 逐字节无损
- `prune` dry-run 正确保护 official + 最近 draft
- `export-db` 表头 / 维度 JSON / 拒绝覆盖 正确
- **路径穿越拒绝**（`../evil` scenario）
- **损坏对象 fail-closed**（篡改 blob 后 verify/restore/复用全部拒绝）
- **并发锁**：锁占用时 add 拒绝（"vault busy"），释放后恢复

### C. SQL 集成全链路

- `verify_integration.py` 通过：两次完整重算（1200→2400）、active 指针切换、历史 run 保留、错误行数回滚（事务原子性）。

### D. 金额精度

- 构造 precision=6 的 `0.000001` 金额，`export-db` 原样输出 `0.000001`，**不经过 float**。

### E. Java 客户端

- `javac --release 8` 编译通过（Java 8 兼容属实）
- JAR 内 2 个 .class 与源码编译产物**逐字节一致**（JAR 是真实源码产物，非占位）

### F. HTTP 服务端到端

- `serve` 启动、`/healthz`、`POST /v1/jobs` 提交正常。

---

## 三、发现的缺点（必须正视，非致命 bug）

1. **Java 客户端下载白名单漏文件。** 服务端 `/v1/jobs/{id}/{file}` 支持 10 个文件（含 `lineage_links.csv`、所有 `.gz` 变体、`evidence_stream_manifest.json`、`run_summary.json`），但 Java SDK `download()` 只白名单了 4 个（results/reconciliation/ledger/source_to_pool）。**Java 用户无法通过 SDK 下载血缘文件或压缩证据**。Python 客户端则完全没做客户端侧白名单（靠服务端兜底）。

2. **版本号多处不一致（老毛病延续）。** `help` 横幅 "V20"、`run` summary engine `go-core/0.18.0`、HTTP `/healthz` 返回 `allocation-core/0.14`、VERSION.txt "V20"、根 README "V21 集成发行版"。**同一发行版有 4 种版本表述**，运维排查会困惑。

3. **导出体积随 SKU 数量级爆炸（最重要的工程风险）。** 实测把 SKU 放大到 50 万时，`results.csv` 膨胀到 **6.4GB 且未写完**（被迫终止）。根因：结果行数 ≈ 金额池数 × 每池目标 SKU 数，每行还带 7 个维度列 + row_id。**不是 bug，但上线前必须算清 `行数 = 金额池 × 目标数`**，否则一次导出就能打爆磁盘。

4. **`export-db` 的 dimensions_json 会放大体积。** 每个结果行的全部维度被打成一个 JSON 字符串塞进 CSV，比原始扁平 CSV 更大（约 1.5–2 倍）。DB bulk 导入方便了，但中间 CSV 体积更大。

5. **Vault 归档不是"可重演"。** 文档已诚实声明：Vault 保存"已完成结果 + 证据"，不自动打包原始输入，因此**独立归档 ≠ 能从零重演分摊**。这是能力边界不是 bug，但易被误读为"备份 = 可复现"。

---

## 四、优点（予以保留）

1. **核心冻结机制真实可靠**：156 文件 SHA 双向一致，是可审计的发行品控。
2. **Vault 安全设计到位**：内容寻址去重、原子写、目录锁互斥、路径穿越防护、dry-run 默认、`ReleaseSource` 删除前逐个哈希核对、`Prune` 绝不碰 official。
3. **金额处理全程不走 float**：Vault 字符串原样投影，SQLite 演示 int64 cents，Java/Python 客户端不重复实现算法。
4. **集成客户端职责清晰**：Java/Python 都只是"轻量客户端"，不重新实现分摊——避免了双实现漂移。
5. **SQL 模板诚实**：明确标注"DESIGN TEMPLATE ONLY，未对真实服务器压测"，不夸大。
6. **fail-closed 一致**：损坏对象、篡改源、reconciliation 造假的处理路径全部拒绝，无静默接受。

---

## 五、建议

- **上线前**：统一版本号（建议 build 时单一注入），补全 Java SDK 下载白名单（对齐服务端 10 个文件）。
- **估算导出体积**：先算 `金额池数 × 每池目标 SKU 数` 得行数，再乘每行字节数。
- **Vault 定位**：是"结果归档 + 去重"，不是"全量备份"或"可重演快照"；需要可重演就额外保留规则 + 输入快照。

---

## 附：可复现测试脚本

- `tests/vault_blackbox_test.py` — Vault 24 项黑盒测试
- `tests/vault_bigdata_test.py` — Vault 大数据导出测试（注意：`--skus` 过大时 results.csv 体积会爆炸，默认 10 万 SKU 较安全）
- `tests/verify_integration.py` — 用户已有，SQLite 集成回归
