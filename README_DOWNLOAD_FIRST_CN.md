# 从你提供的 Allocation-Core-main.zip 构建的完整项目

## 重要：两个不同的下载概念

- 本次**完整项目 ZIP**：包含上传源码中的所有文件、`core/` 和 `studio-src/`、所有示例、原始测试、Windows/Linux/macOS 的交叉编译程序、自动构建与验证工具。无需再合并任何补丁。
- GitHub 绿色 `Code → Download ZIP`：只包含 Git 跟踪的源码，默认**没有** `studio_windows_amd64.exe`，不能直接双击 `Start_Windows.cmd`。这是你原先报错的直接原因。

## Windows 使用

1. 完整解压 ZIP 至全新的文件夹；不要覆盖旧项目。
2. 先运行 `Check_Windows_Installation.cmd`，确认 Studio 和 Core 四个 Windows 程序齐全。
3. 双击 `Start_Windows.cmd`，程序自动打开浏览器。没有打开时访问 `http://127.0.0.1:8866/designer`。
4. 用户数据保存在 `%LOCALAPPDATA%\AllocationStudioData`，不会放在程序 ZIP 内。运行时请保留 CMD 窗口。
5. 运行后若出现端口占用，关闭旧 Studio 进程或检查 8866 端口。

## 开发者构建（无需改金额算法）

安装 Go 1.21+，分别在 `core/` 和 `studio-src/` 执行 `go test ./...`，然后运行项目仓库中 `.github/workflows/build-complete.yml` 所列编译命令。`tools/verify_distribution.py` 用于验证四个 Windows PE 架构、源文件和发行 SHA-256。

## 来源/改动边界

输入源：**用户本轮上传的 `Allocation-Core-main.zip`**。此包以其原始源码为基准，**没有拉取或假冒 GitHub 之后的未提供提交**。具体输入 ZIP SHA-256、修改过的文件与测试记录请见 `BUILD_PROVENANCE.json`。没有改动 `core/internal`、`core/cmd` 的计算业务实现；各平台 EXE 均从本次上传的源码交叉编译生成。

本容器是 Linux，已做跨平台编译及本机验证；**Windows 真机双击还需在你的 Windows 电脑或 GitHub Actions Windows Runner 上验收**。
