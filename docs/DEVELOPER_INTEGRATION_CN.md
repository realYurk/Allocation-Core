# Allocation Studio · 开发者接入与部署说明

## 1. 组件边界

- `core/alloc_*`：既有权威 Go 计算内核，本轮**文件字节不变**；输入 `allocation-ir/2.0`，输出结果、守恒与审计证据。
- `studio_*`：全新独立前端启动器，嵌入两个页面：`/designer`（可编辑）和 `/`（检查工作台）。本地启动器只允许 `127.0.0.1` / `localhost` 回环访问，严格检查 Host / Origin，不提供跨域访问。
- `studio-src/`：前端启动器源码；无需 Node 或 npm，使用 Go 的 `embed` 嵌入静态 HTML。
- `<user data>/cases`：可编辑项目，包括 `rule.json`、`amounts.csv`、Driver CSV；`<user data>/jobs`：独立任务结果。

## 2. 本地启动

```bash
# Linux x64
./studio_linux_amd64 -root . -data-dir ./local_test_data -listen 127.0.0.1:8866 -no-browser
# http://127.0.0.1:8866/designer
```

Windows 使用 `Start_Windows.cmd`，macOS 使用 `Start_Mac.command`。默认数据路径已在小白手册中说明。

## 3. 规则设计器 API（本地，仅提供给同源浏览器）

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/cases` | 列举内置只读示例及用户项目 |
| GET | `/api/designer/rule?case=dual_driver` | 获取完整 JSON、可编辑状态、SHA 指纹 |
| POST | `/api/designer/clone` | 将示例复制为独立用户项目 |
| POST | `/api/designer/check` | 用未保存的 JSON 草稿调用权威内核 `doctor` |
| POST | `/api/designer/rule` | 带 SHA 乐观锁校验的原子保存，仅限用户项目 |
| GET | `/api/designer/dataset?case=...&file=...` | 读取小 CSV（超过1MB仅返回体积） |
| POST | `/api/designer/dataset` | 原子替换当前用户项目已登记 CSV |
| POST | `/api/designer/import` | 导入新的 CSV 数据集，并返回待保存的更新规则 |
| POST | `/api/jobs` | 调用原核心全量执行，异步返回 Job ID |
| GET | `/api/jobs/{id}` | 获取执行状态、摘要、前300条结果、守恒预览及文件列表 |
| GET | `/api/jobs/{id}/download/{file}` | 下载受白名单保护的计算证据 |
| POST | `/api/trace` | 让原核心反查指定结果行的上游链路 |

示例：复制项目：

```json
{"from":"manufacturing","name":"company_fc_rule"}
```

保存规则：

```json
{"case":"user_company_fc_rule","base_sha":"<GET rule 返回的 sha256>","rule":{"schema":"allocation-ir/2.0","name":"...","dimensions":["..."],"datasets":{"amounts":{},"drivers":{}},"stages":[...]}}
```

备注：上述保存请求中的 `rule` 需提供完整合法 Allocation IR，不可只发送差异字段。失败返回 4xx，旧版规则文件不变。保存前使用 Go `doctor`（检查规则、CSV 表头）；驱动分母有效、来源值及完整守恒需实际运行后才能判断。

## 4. 动态规则和外部数据库

用户可以在界面中维护金额池、输入路径（DAG）、维度 `AS_IS/TOTAL`、目标 `SOURCE/ELEMENT/INCLUDE/INCLUDE_EXCLUDE`、来源过滤、守恒键、多驱动 `share/weight_field`、负驱动策略及精度。层级映射或额外字段可通过高级 JSON 管理，**不会在浏览器里复制金额算法**。

正式 Java/Python 工程接入仍建议：数据库提取完整金额+Driver数据 → 输出符合 schema 的 CSV → 调用既有 CLI/JAR HTTP 客户端 → 验证守恒 → 用数据库原生批量导入 → 原子切换有效版本。不要把千万行强塞进此本地界面。

## 5. 数据一致性和安全

- 内置示例只读；用户项目使用随机中间目录创建、原子重命名。
- 项目名、文件名必须满足英文安全规则；数据集只允许项目目录内部相对 CSV；阻止路径穿越与符号链接逃逸。
- 保存规则调用原 Go 内核 `doctor` 后才原子替换；`base_sha` 用于防止多窗口覆盖。运行期间 UI 禁止修改该项目。
- 每次任务保存 `studio_rule_snapshot.json`，供历史血缘查询使用；计算每次**全量重算**。
- 不是互联网 SaaS；不允许绑定到 `0.0.0.0`，无权限体系、远端用户管理或 Redis 依赖。若要企业局域网多人共用，需要另开发专门身份认证及多租户服务层，不应简单改变监听地址。
- CSV 编辑/导入为 1MB 前端管理上限，不等于核心上限。大规模文件请使用后台 ETL 或正式输入快照工具。

## 6. 重建前端启动器

```bash
cd studio-src
go test -race ./...
go vet ./...
# 示例：构建 Windows x64，需已安装 Go 1.21+
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o ../studio_windows_amd64.exe .
```

计算核心不参与该命令。发行时通过 `CORE_UNCHANGED_SHA256.json` 对核心文件重新核验。

## 7. 测试边界

本轮在 Linux x64 上完成本地 HTTP、浏览器交互（测试桥接）、双因子 70/30、真实两阶段、CSV 修改再运行、冲突拒绝与损坏规则拒绝。六种 Studio 程序完成交叉编译，但 Windows、Linux ARM64、macOS 原生运行仍需要对应设备验收。正式多用户部署和企业级文件上传不是本轮交付范围。
