# 跨平台运行与部署矩阵

## 真正可运行的原生文件

| 系统 | CPU 架构 | 二进制 | 本轮状态 |
|---|---|---|---|
| Windows 10/11 / Windows Server（受支持版本） | x86_64 | `core/alloc_windows_amd64.exe` | 已打包，PE 文件格式已检查；**未在本次 Windows 主机实跑** |
| Windows ARM64 | arm64 | `core/alloc_windows_arm64.exe` | 已打包，PE ARM64 已检查；**未实跑** |
| Linux（常见 x64 发行版） | x86_64 | `core/alloc_linux_amd64` | **本次容器实际执行** |
| Linux ARM64（如 Graviton/Ampere） | aarch64 | `core/alloc_linux_arm64` | **新增交叉编译**，ELF AArch64 格式已检查；**未在 ARM 主机实跑** |
| macOS Intel | x86_64 | `core/alloc_darwin_amd64` | 已打包，Mach-O 格式检查；**未在本次 macOS 实跑** |
| macOS Apple Silicon | arm64 | `core/alloc_darwin_arm64` | 已打包，Mach-O 格式检查；**未在本次 macOS 实跑** |

**不是字面意义上的任意环境**：本次没有 Android、iOS、AIX、32 位 OS、FreeBSD 或其他 CPU 的发行二进制。Windows EXE 不能直接在 macOS/Linux 上原生运行；需要对应原生文件。未来可基于 Go 源码评估更多平台，但必须逐平台编译和验收。

## 不需要额外安装什么

运行既有二进制：不需要 Go、Python、Node、Java、Redis。Java JAR 客户端需要宿主 Java（兼容 Java 8+）；Python SDK 需要宿主 Python；独立验收脚本需要 Python 3 标准库。连接 PostgreSQL 或 SQL Server 的示例，还需要对应数据库驱动与访问权限。

## Linux/macOS

```bash
unzip Allocation_Core_Production_Acceptance.zip
cd Allocation_Core_Production_Acceptance
chmod +x run.sh
./run.sh smoke
./run.sh help
./run.sh serve  # 127.0.0.1:8787，保持终端不关闭
```

如果你的解压工具没有保留 Unix 执行权限，`run.sh` 会尝试为对应二进制设置 `u+x`；若文件所在目录只读或 `noexec`，应换到企业批准的可执行路径，不要使用绕过安全限制的方式。

macOS 的安全校验可能要求 IT 对未签名软件进行批准。本次没有 Apple notarization，也没有实际在 Mac 上完成签名/运行验收，不应据此承诺所有 macOS 版本能直接双击。

## Windows

ZIP 完整解压，双击根目录 `run_windows.cmd`。它会根据 `%PROCESSOR_ARCHITECTURE%` 选择 `core/alloc_windows_amd64.exe` 或 `core/alloc_windows_arm64.exe`，窗口保留错误信息。也可执行 `run_windows.cmd serve` 启动本地 HTTP 服务。运行目录不要放在需要管理员权限才能写的地方。

## Docker / Kubernetes（可选）

仓库提供 `Dockerfile`，使用上述**Linux 原生 Go 文件**构建容器，不引入 Java/Python 计算引擎。示例：

```bash
docker build --platform linux/amd64 -t allocation-core:local .
mkdir -p runs
docker run --rm --user "$(id -u):$(id -g)" \
  -v "$(pwd)/core/examples:/work/examples:ro" -v "$(pwd)/runs:/work/runs" \
  allocation-core:local run /work/examples/manufacturing/rule.json /work/runs/demo --workers 2
```

容器构建、`buildx` 多架构镜像、Kubernetes、SELinux/AppArmor、卷配额与 TLS 代理**尚未在本次环境实际验收**。生产必须先验证镜像可执行、数据卷写权限以及宿主/容器的 CPU、内存、磁盘限额。不可把 HTTP 8787 直接暴露到公网。
