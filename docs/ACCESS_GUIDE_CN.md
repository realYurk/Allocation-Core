# 接入教程：怎么把你的数据喂给这个分摊引擎，并接到你自己的系统里

这份文档回答一个最实在的问题：**我已经有一套数据（数据库里、或者能导出 CSV），我该怎么让这个分摊组件帮我算，然后怎么把它嵌进我的系统？**

读完你会清楚三件事：① 数据要准备成什么样子；② 到底怎么"调用"它（是不是非得点 bat）；③ 接到系统里会遇到哪些坑，以及软件挂了怎么办。

---

## 一、先纠正一个根本误解：它不是"装了才能用"的桌面软件

这个分摊引擎的本质是**一个独立的命令行程序（一个 Go 编译出来的可执行文件）**，不是一个需要你打开界面、点按钮、然后它在那儿"等着"的 GUI 应用。

- Windows 下那个 `alloc_windows_amd64.exe`，Linux 下 `alloc_linux_amd64`，macOS 下 `alloc_darwin_*`，**是同一个东西的三个平台版本**。
- 你看到的那些 `.bat` 文件（`01_Windows_Run_Two_Stage.bat` 等），**只是为了方便没有命令行习惯的人在 Windows 上双击一下**。它们内部干的就一件事：`alloc.exe run xxx/rule.json output`。**bat 不是"启动器"，更不是"必须开着"的宿主程序。**
- 所以答案是：**你完全不需要点 bat，也不需要"先启动个软件再等它"**。你可以在自己的代码里直接调用它。

这个程序有三种被调用的形态，按推荐程度排序：

| 形态 | 怎么用 | 适合谁 |
|---|---|---|
| **① HTTP 常驻服务**（推荐） | 用 `serve` 命令起一个后台进程，你的程序用 HTTP 调它 | Java / Python / 微服务 / 多系统共享 |
| **② CLI 子进程** | 你的程序用 `subprocess`/`ProcessBuilder` 直接拉起 exe 跑一次 | 批处理、定时任务、单机脚本 |
| **③ 本地库** | 无。**不提供** JNI / C ABI 那种进程内调用 | —— |

> 关键点：无论哪种形态，**计算逻辑都只在这一份 Go 程序里**。那个 Java JAR、那个 Python 脚本，都只是"薄薄的 HTTP 客户端"，它们**不包含任何分摊算法**。所以你不会遇到"Java 版和 Go 版算出来不一样"的问题——只有一份算法。

---

## 二、数据要准备成什么样子（CSV 部分）

引擎的输入是**三类 CSV 文件 + 一个规则文件 rule.json**。你不用改代码，只需要把数据整理成它认识的列。

### 2.1 三份输入 CSV

以自带示例 `core/examples/manufacturing/` 为例（制造业：工厂→系列→商品两步分摊）：

**① 金额表 `amounts.csv`**（待分摊的钱）：

```csv
period,plant,brand,family,sku,account,currency,amount
2026-01,工厂一,待分配品牌,待分配系列,待分配,科目001,CNY,1200.00
```

**② 工时驱动 `labor.csv`**（第一步怎么分）：

```csv
period,plant,family,hours
2026-01,工厂一,系列甲,100
2026-01,工厂一,系列乙,50
```

**③ 销量驱动 `sales.csv`**（第二步怎么分）：

```csv
period,plant,family,sku,units
2026-01,工厂一,系列甲,商品A,3
2026-01,工厂一,系列甲,商品B,1
```

### 2.2 核心：列名和维度是可以换成你自己的

这三张表的**列名（维度）不是写死的**。你在 `rule.json` 里声明你要用哪些维度。比如你公司是"部门→项目→产品"分摊，那你的维度就是 `dept, project, product`，而不是 `plant, family, sku`。

**唯一不能动的硬性约定只有一条：金额必须是十进制数，绝对不能用 float/double。** CSV 里金额写成字符串 `"1200.00"` 最稳妥。程序内部用精确整数+最小单位计算，最后做最大余数法尾差分配，保证一分钱不差、池子守恒。

### 2.3 你从 SQL 来的数据，转成 CSV 时要注意

如果你数据在数据库里，正确的做法是：

1. 用一条**只读 SQL**，把"本次要分摊的金额"和"本次要用到的驱动"导成 CSV 快照。
2. **在一个一致性快照点上导出**——不要金额表导一半、驱动表导一半，中间有人改了数据，那样会跨表不一致。
3. CSV 里金额字段**保持字符串原样**，不要经过任何 float 中间变量。

---

## 三、怎么调用：三种方式的具体做法

### 方式 ① HTTP 服务（推荐给要长期接入的系统）

**第一步：启动服务（一次，作为后台常驻进程）**

```bash
# Windows
core\alloc_windows_amd64.exe serve --root core\examples --store .\service_data --listen 127.0.0.1:8787 --jobs 2

# Linux（生产建议配 systemd 或容器）
core/alloc_linux_amd64 serve --root /data/inputs --store /data/service_data --listen 127.0.0.1:8787
```

注意：**它默认只监听本机回环地址（127.0.0.1）**，这是刻意的安全设计。要跨机器访问，得在前面加一层你自己公司的鉴权反向代理（Nginx/网关），不要裸奔到公网。

**第二步：你的程序发 HTTP 请求**

提交一个任务：

```bash
curl -X POST http://127.0.0.1:8787/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{"rule":"manufacturing/rule.json","workers":2,"evidence_gzip":true}'
```

返回一个 JSON，里面有 `id`。然后轮询：

```bash
curl http://127.0.0.1:8787/v1/jobs/<id>
```

直到 `status` 变成 `PASS` / `FAILED` / `INTERRUPTED`。**只有 `PASS` 才去下载结果**：

```bash
curl -o results.csv http://127.0.0.1:8787/v1/jobs/<id>/results.csv
```

**用 Python（项目里已自带 SDK）：**

```python
from http_client import AllocationClient
client = AllocationClient('http://127.0.0.1:8787')
job = client.submit('manufacturing/rule.json')      # 提交
status = client.wait(job['id'], timeout=180)         # 等它算完
if status['status'] != 'PASS':
    raise RuntimeError(str(status))
client.stream_result(job['id'], 'results.csv', 'results.csv')  # 下载结果
```

**用 Java（项目里已自带 JAR）：**

```java
import org.allocation.integration.AllocationClient;
AllocationClient client = new AllocationClient("http://127.0.0.1:8787", "");
String body = client.submit("manufacturing/rule.json");
// 解析出 id，轮询 client.status(id) 直到 PASS
client.download(id, "results.csv", Paths.get("results.csv"));
```

### 方式 ② CLI 子进程（适合批处理/定时任务，不想常驻服务）

你的程序直接拉起 exe 跑一次，跑完进程自己退出：

```bash
core\alloc_windows_amd64.exe run rule.json output_dir --workers 4 --evidence-gzip
```

在 Python 里：

```python
import subprocess
r = subprocess.run(
    ["core/alloc_windows_amd64.exe", "run", "rule.json", "output_dir", "--workers", "4"],
    capture_output=True, text=True, timeout=600)
# r.returncode == 0 且 output_dir/run_summary.json 里 status=PASS 才算成功
```

> **这里就是对你"是不是必须点 bat"的直接回答：你完全可以在代码里直接 `subprocess` 调用 exe，或者用 HTTP 服务，根本不需要任何 bat，也不需要任何人去点鼠标。**

---

## 四、"软件挂了怎么办" —— 这是接入时必须想清楚的问题

你想到的这个点非常关键，而且方向是对的。分几个层面说清楚：

### 4.1 这个引擎"挂"不会污染你已有的数据

- 它**从不做增量计算**。每次都是拿原始输入**全量重算**，生成一个**全新的 `run_id` 输出目录**，不会去覆盖你上一版已经发布的结果。
- 所以即使某次计算中途崩了，你数据库里的"当前有效结果"还是上一次那个好的版本，不受影响。
- 计算输出是**原子提交**的：先写到临时目录，全部算完、校验通过，才 `rename` 成最终目录。半成品不会被当成成品。

### 4.2 但"挂"这件事，责任要分清

这个引擎管的是"**算对钱**"。下面这些它**不负责**，属于你自己接入层 / 运维层的责任（文档里也明确写了"上线前必须补齐的企业责任"）：

| 风险 | 谁来兜底 | 建议做法 |
|---|---|---|
| 进程崩了没人知道 | 你 | 进程监控 + 告警（supervisor / systemd / K8s 探针 / 云监控） |
| 任务跑太久卡死 | 你 | 任务超时 + 取消机制 |
| 磁盘写满了 | 你 | 独立磁盘卷 + OS 配额（它有容量预检，但那是"护栏"不是"刹车"） |
| 谁来调、谁有权限 | 你 | 鉴权、租户隔离、TLS、密钥管理 |
| 服务单点挂了 | 你 | 反向代理 + 多副本 + 健康检查 `/healthz` |

### 4.3 它自带的两道"不让你挂得更惨"的保护

1. **容量准入**：设一个预算文件，算之前先估算"要写多少行、占多少磁盘"，超限就直接拒绝、连算都不开始。避免你提交一个"分到 50 万 SKU"的任务把磁盘写爆。配置方式：
   ```bash
   # 方式A：环境变量（对所有 run/batch/http 路径生效）
   set ALLOC_CAPACITY_POLICY=D:\Allocation\capacity_budget.json
   alloc run rule.json output
   # 方式B：HTTP 服务启动时指定
   alloc serve --budget-file D:\Allocation\capacity_budget.json ...
   ```
2. **状态可重放**：服务重启后，未完成的任务会被标记为 `INTERRUPTED`，不会假装成功；你要么重跑，要么查它为什么断了。

---

## 五、接入别的系统，你可能会踩到的坑（提前想好）

1. **跨币种不能直接加**。如果你的金额有人民币有美元，`currency` 必须设成"不可跨越"维度，分摊前先统一汇率换算。否则会算出"人民币+美元"的荒谬数字。

2. **结果行数会爆炸**。结果行数 ≈ 金额池数 × 每池有效目标数 × 驱动分支数。你以为"就分到 5000 个 SKU"，实际可能是"12 期 × 100 科目 × 5000 SKU = 600 万行"。**上线前先算清这个乘积**，再用容量准入兜住。

3. **不要把所有 ledger 都灌进 OLTP 数据库**。`results.csv`（最终结果）可以入库，但 `ledger.csv`（完整证据链）动辄几 GB，应该放压缩归档（它支持 gzip 压缩证据 + Vault 归档），需要追溯时再解。

4. **数据库写入要"先隔离后切换"，不要 DELETE 再 INSERT**。正确姿势：新 `run_id` 的结果先写进 staging 表 → 校验行数/金额/唯一键 → 通过后一个事务里切换 `ACTIVE_POINTER` 指针 → 提交。失败就回滚，旧版本还在。

5. **`scenario` / `business_version` / `run_id` 是三个不同概念**，别混：业务场景（如 FC 10+2）≠ 版本号 ≠ 每次运行的技术 ID。数据库唯一约束建议用 `(run_id, terminal_stage, row_id)`。

6. **服务默认只监听本机**。想跨机器，必须自己加鉴权反向代理，不要用它的单机 token 直接暴露公网。

7. **不是所有平台都有开箱即用的本地库**。如果你非要"进程内调用"（不想要 HTTP、不想要子进程），当前**没有** JNI/C 库给你。要么接受 HTTP/子进程，要么等未来版本。

---

## 六、一分钟速览：我要接入，该走哪条路

```
你的数据（SQL / 已有 CSV）
   │  导出成一致性快照 CSV + 写一份 rule.json
   ▼
调用引擎（三选一）：
   ├─ HTTP 服务（推荐）：serve 常驻 → 你的程序 POST /v1/jobs → 轮询 → 下载结果
   ├─ CLI 子进程：直接 subprocess 拉起 exe run 一次
   └─ （不提供进程内库）
   ▼
检查结果：run_summary.json 里 status=PASS 且 input_amount==output_amount
   │  每个金额池 reconciliation.csv 都 PASS 且 delta=0
   ▼
写回你的系统：staging 表 → 校验 → 事务切换 ACTIVE_POINTER
```

**一句话总结你的所有疑问**：它不是一个"点 bat 才能跑"的软件，而是一个可以让你在代码里直接调用的计算程序；数据喂给它 CSV（列名可自定义、金额必须精确十进制），它全量重算返回结果；"挂了"不污染已有数据，但高可用、权限、磁盘配额这些是你的接入层要自己补的，它已经用"容量准入 + 原子提交 + 全量重算"帮你兜住了最危险的三类事故。
