# Allocation Visual Lab

Visual Lab 是 Studio 内的独立页面 `/lab`。修改集中在独立的 HTML、CSS、JavaScript 和展示接口；Engine 的金额算法、精度、最大余数分配、守恒约束和正式发布流程没有修改。不使用 CDN、网络字体或新增前端运行依赖。

## Windows 启动

新电脑：把整个离线 ZIP 复制过去并解压，双击 `启动可视化实验室.cmd`。程序会自动打开浏览器；保持弹出的窗口运行，关闭窗口即可停止。无需安装 Python、Go 或 Node。默认使用当前 Windows 用户的本地 Studio 数据目录；只体验示例时选择「合成实验」。如需其他电脑的历史任务，需要自行安全迁移其 Studio 数据目录，本工具不会自动同步或上传。

已有本分支编译的 `AllocationCore.exe` 时，不需要 Python、Node 或 Go：

```powershell
& '.\AllocationCore.exe' serve --data-dir "$env:LOCALAPPDATA\AllocationCoreVisualLab" --listen 127.0.0.1:8866 --no-browser
Start-Process 'http://127.0.0.1:8866/lab'
```

保持第一个终端运行。第一次只体验合成实验时，使用一个新的独立 `--data-dir`。没有内置旧示例时，启动日志可能提示 `Optional synthetic demo unavailable`；Visual Lab 自带的 A–D 实验不依赖旧示例目录。

用户自行查看已有历史任务时，将 `--data-dir` 指向原 Studio 数据目录；页面只读已完成的历史任务和保存的证据，不读取当前项目的可变 Driver。需要保存各规则组输出；没有 `groups/.../output` 的旧任务会明确拒绝展示，不能用不完整证据伪造结果。

源码构建（已有本地 Go 工具链及模块缓存，无需联网）：

```powershell
Set-Location 'D:\01_Work\03_Projects\pa\Allocation Studio'
$env:GOTOOLCHAIN='local'
$env:GOPROXY='off'
$env:GOSUMDB='off'
$env:GOTELEMETRY='off'
& 'C:\Program Files\Go\bin\go.exe' build -trimpath -o "$env:TEMP\AllocationCore-VisualLab.exe" .
& "$env:TEMP\AllocationCore-VisualLab.exe" serve --root (Get-Location).Path --data-dir "$env:LOCALAPPDATA\AllocationCoreVisualLab" --listen 127.0.0.1:8866 --no-browser
```

然后在浏览器打开 `http://127.0.0.1:8866/lab`。原首页、多科目中心和规则设计器均增加了 Visual Lab 链接，原有功能继续使用原接口。

## 30 秒体验

1. 打开页面，默认 A：100 万 → 工时 3:2 → 车型 A 60 万、B 40 万。
2. 拖动权重滑块，或输入精确权重。旧结果立即隐藏，停止输入 350ms 后由本机 Core 重算。可以取消某个目标的参与。
3. 点击「播放过程」，用上一步、下一步或阶段节点查看规则、来源范围、目标集合和 Ledger 的原始权重依据。
4. 查看 Before / After 和最终坐标表，再分别检查守恒、范围、独立对账和发布状态。

| 实验 | 原始金额与规则 | 默认最终结果（独立手算测试） |
|---|---|---|
| A | E1 1,000,000，3:2 | A 600,000；B 400,000 |
| B | E1 1,200,000，先 3:2；A 内 1:3，B 内 2:3 | A1 180,000；A2 540,000；B1 192,000；B2 288,000 |
| C | E1 1,000,000 按工时 3:2；E2 500,000 按销量 1:4 | E1：600,000 / 400,000；E2：100,000 / 400,000 |
| D | 零 Driver / 不存在目标 / 来源过滤不匹配 | 前两种 BLOCK；范围未命中时原金额保留，显示 PARTIAL |

D 的范围未命中遵循原 Core 的透传语义：守恒可能 PASS，但未完成预期目标分摊。没有为了制造失败或动画修改 Core 行为。负来源金额也保留原符号。Demo 金额固定 2 位，权重最多 6 位；超精度拒绝，绝不静默四舍五入。每次 Demo 在私有临时目录调用原 `engine.RunWithOptions`，结束删除临时计算目录，不写项目、历史任务、Vault 或正式发布目录。

## 历史结果与证据范围

- 切换到「历史真实运行」，选择项目、成功任务、科目。列表复用 `/api/multi/jobs`（最近 100 条）。
- `/api/lab/run` 只读原任务保存的生效规则、Core 摘要、`group_amounts.csv` 和最终 `results.csv`。核对规则、来源、结果及摘要指纹；旧任务缺少清单时显示 PARTIAL，文件与清单不符时阻断。
- 金额卡片、Before / After 和明细属于所选科目及筛选。阶段图始终属于原历史规则组；图内来源、阶段、终端使用同一组范围，明确标注它与筛选金额不同。不能用组总额冒充单科目阶段值。
- 过程按实际 `inputs` 依赖绘制，支持分支、合并、PASS 和停用阶段；不把不同阶段金额累加。动画只高亮节点和连线，金额文本不插值、不重新计算。
- 选目标维度过滤时，来源可能没有相同成员。因此 Before 为零不自动判为金额丢失；没有伪造一一配对。混合币种的 KPI、柱形合计和阶段总额不显示为一个金额，要求先筛选币种。
- 复用 `/api/multi/stage-evidence` 查看所选科目全部期间的实际 Ledger。兼容增加 `sample_ledger`，每个规则最多 3 条，带来源池、目标坐标、规则金额、权重、分母和分摊金额。明确标注是有限样例，其他界面筛选不适用于此摘要。
- 复用 `/api/multi/acceptance` 选择历史独立对账报告。守恒通过不等于参考对账通过，参考对账也不证明原系统业务范围定义正确。
- 复用 `/api/multi/compare-runs` 比较两个历史最终结果；展示后端 `top_differences` 中的坐标、旧金额、新金额和差额，完整证据通过原 CSV 下载接口取得。输入和规则指纹变化仅作为事实，不推断根因。
- 结果文件生成与 Vault 批次发布分开说明。Lab 不提供绕过原正式工作流的发布按钮。

## 大数据、精度与交互边界

服务端流式读取，不向浏览器下发全量 CSV。金额先调用 Core 的固定精度解析，再用整数进行展示聚合；浏览器仅做文本格式化、排序和图形宽度布局。每页最多 50 行；排序保留至多 `offset+50` 行；偏移上限 10,000。精确筛选成员、搜索和金额排序由服务端处理。汇总最多 10,000 个成员，显示绝对金额前 12 名和其余成员的有符号合计；超过上限会阻断并要求收窄范围，不返回冒充完整结果的截断金额。完整数据可以通过原结果下载入口获得。

聚合及分页每次仍需要扫描所选规则组文件，是 O(文件行数)，目前没有新增持久化索引或聚合缓存。两个并发扫描槽位，繁忙返回 429；请求取消会终止扫描。百万行测试只证明该合成场景下响应有界，不代表千万行、高基数或生产 SLA 已验收。

页面切换参数会立即清除旧结果，取消请求并检查请求代次；旧响应不能覆盖新选择。支持暂停、手动步进、键盘焦点、横向画布滚动和 `prefers-reduced-motion`。CSV 导出保留金额文本精度，并中和以公式开头的业务字符串；下载原始完整结果则使用既有接口，不修改原证据。

## 修改文件

| 文件 | 用途 |
|---|---|
| `internal/studio/visual_lab.go` | 页面注册、受限合成实验、原 Core 调用 |
| `internal/studio/visual_lab_data.go` | 历史证据验证、流式精确聚合、有界排序分页 |
| `internal/studio/public/lab.html` | 独立页面结构 |
| `internal/studio/public/lab.css` | 浅色 Bento 布局、响应式、微动画 |
| `internal/studio/public/lab.js` | 参数交互、依赖图、前后对比、历史与验收证据 |
| `internal/studio/multi.go` | 注册 Lab 路由 |
| `internal/studio/multi_stage_evidence.go` | 兼容增加有限 Ledger 权重样例 |
| `internal/studio/public/index.html`、`multi.html`、`designer.html` | 增加新页面入口 |
| `internal/studio/visual_lab_test.go` | 手算、异常、精度、历史完整性、分页、百万行测试 |
| `internal/studio/visual_lab_node_test.cjs` | 请求竞争、控件、依赖图、精确文本、DOM 安全与导出测试 |
| `internal/studio/multi_stage_evidence_test.go` | 断言实际 Ledger 样例 |
| `docs/VISUAL_LAB.md` | 使用说明、数据契约、验证和限制 |
| `README.md` | 功能入口及文档链接 |

## 验证与仍需完成的验收

2026-11-02，本地合成验证：

| 状态 | 内容 |
|---|---|
| PASS | A/B/C 与独立手算值逐目标一致；多阶段终端不重复加总 |
| PASS | 零权重、目标不存在、空目标、超精度阻断；负数金额守恒；来源过滤未命中正确保留原金额 |
| PASS | 实际 Multi 历史运行 → 新只读接口；修改当前项目不改变历史展示；历史证据被改动会阻断 |
| PASS | 1,000,000 合成行聚合，返回 50 明细行，JSON 3,665 字节；单次本机扫描约 0.60 秒 |
| PASS | HTTP 启动检查：`/lab`、两个新静态资源及原首页、multi、designer、workspace 均返回 200；HTTP Demo B 返回 4 个正确最终目标 |
| PASS | 产品 Go 回归 374 个测试节点通过（含子测试）、2 个原有平台相关测试跳过、0 失败；13 个前端逻辑测试及 `go vet` 通过。先前本地验收工具不在本次提交或这个产品测试计数内 |
| PARTIAL | 已实现桌面/窄屏 CSS、减少动态效果和控件交互；DOM 测试不能代替浏览器视觉验收 |
| NOT_TESTED | 实际 Chrome/Edge 的视觉、不同宽度下的布局与辅助技术验收：Windows Computer Use 工具因无法可靠识别当前浏览器 URL 停止，未绕过该限制 |
| NOT_TESTED | 真实企业数据、真实 Driver/层级/成员资格定义、真实独立参考及生产容量 SLA；本次未接触这些数据 |
| NOT_TESTED | 千万行、高基数、长时间并发、复杂 DAG 的大规模视觉性能 |

自动化复现（需预先具备离线 Go/Node）：

```powershell
# 只生成仓库自带的合成测试夹具；不会覆盖已存在文件
go run . demo .
go test ./... -count=1 -timeout 240s
go vet ./...
node --test internal/studio/visual_lab_node_test.cjs internal/studio/driver_contract_node_test.cjs internal/studio/factor_editor_node_test.cjs
```

正式业务验收仍需要用户在断网环境独立完成。此版本的结论是：可运行的可视化模块和合成实验已实现，后端证据链复用及自动化回归通过；不是完整生产适配验收 PASS。
