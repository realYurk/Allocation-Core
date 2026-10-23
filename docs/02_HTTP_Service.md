# V14｜HTTP 接入与持久任务（给 Java / Python / 其他系统）

## 启动服务

Windows：双击 `07_Windows_Start_HTTP_Server.bat`，并保持服务窗口运行。示例监听 `127.0.0.1:8787`，默认不能被其他电脑直接访问。

开发人员在本机运行：

```bat
alloc_windows_amd64.exe serve --root examples --store v14_service_data --listen 127.0.0.1:8787 --jobs 2 --queue 16
```

生产服务器如需使用令牌，可再加 `--token YOUR_LONG_RANDOM_SECRET`。不要把真实密钥写进提交给客户的启动脚本。服务目前的密钥校验只是一层本机接口保护，不能替代完整身份管理。

## 提交任务

向 `POST http://127.0.0.1:8787/v1/jobs` 发送 JSON：

```json
{
  "rule": "manufacturing/rule.json",
  "workers": 2,
  "disk_partitions": 8,
  "driver_disk_index": true,
  "driver_cache": true
}
```

返回 HTTP 202、任务 ID 和 `QUEUED` 状态。**不要把文件内容直接拼在 URL 里**；`rule` 必须是 `--root` 指定目录下的相对路径，读取的数据文件也必须在该目录内。

## 查询与下载

- `GET /healthz` 健康状态。
- `POST /v1/validate` 规则结构检查，JSON 格式与上面相同。
- `GET /v1/jobs` 历史任务列表（本机工作区）。
- `GET /v1/jobs/{id}` 查询状态。
- `GET /v1/jobs/{id}/results.csv` 商品/费用分摊结果。
- `GET /v1/jobs/{id}/ledger.csv` 完整计算证据。
- `GET /v1/jobs/{id}/reconciliation.csv` 各金额池守恒核对。
- `GET /v1/jobs/{id}/source_to_pool.csv` 来源金额与金额池的关联。

只有 `PASS` 状态允许通过 API 下载正式输出。失败任务可以查看错误原因，但不会发布半成品结果。

## V14 的持久化机制

`v14_service_data/` 中包含：

```
state/{job-id}.json            // 已持久化的任务状态
snapshots/{job-id}/...         // 提交时冻结的规则与输入 CSV（仅内部使用）
jobs/{job-id}/results.csv      // PASS 时才存在正式输出
cache/{hash}.jsonl            // 可复用的磁盘 Driver 索引
cache/{hash}.meta.json        // 索引元数据与校验哈希
```

规则和金额 CSV 即使之后被外部系统修改，**已经提交的任务仍使用提交时的输入快照**。重启服务，既有成功任务依旧可以查询、下载；重启时未结束的任务会显示 `INTERRUPTED`，提交新任务重新计算。**当前未实现队列自动恢复和暂停在某个金额池内部继续计算**。

## 接入不同技术栈

- Java 8+/Spring Boot：使用标准 HTTP 客户端向 `/v1/jobs` 提交 JSON；拿到任务 ID 后定时查询并读取输出 CSV。参考 `adapters/AllocationHTTPClient.java`。
- Python：标准库 `urllib.request` 即可使用同一 API。参考 `adapters/python_http_client.py`。
- 数据库、ERP 或其他系统：通过任何能发 JSON HTTP 的客户端提交，不需要把计算引擎改写成对应语言。

## 安全与规模边界

- 默认只允许本机监听；没有完整 RBAC、审计人员身份、TLS 或网关。
- 输入快照会占用额外磁盘，需对 `service_data` 设置空间配额并定期归档。
- 当前状态 JSON 是**单实例本地持久化**；不能让多台服务进程同时写一个工作区。
- 服务级 `jobs`、`queue`、单任务线程预算只是逻辑限额，不是操作系统级内存硬限额；企业部署需额外进程/容器级限额。
