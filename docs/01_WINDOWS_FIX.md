# V14｜为什么以前的 BAT 文件名乱码？Windows 零基础排查

## 三种乱码不是一回事

1. **文件名乱码**：比如 `01_Windows_杩愯...bat`。这是 ZIP 里的**文件名**已经因错误编码转换而损坏。`chcp 65001` 只能改变 CMD 的文本解释，无法把错误的文件名自动修好。**V14 包中所有路径统一为 ASCII**，这才是根因修复。
2. **CMD 输出乱码**：命令窗口里把“第一步”显示成乱码。V14 保留 `chcp 65001` 和 CMD 的 UTF-8 设置；如果仍然有问题，检查 Windows 终端字体和系统区域设置。
3. **Excel CSV 乱码**：文件数据本身通常是 UTF-8，但 Excel 用了系统 ANSI 编码打开。V14 继续输出以 `EF BB BF` 开头的 UTF-8 BOM CSV；旧版 Excel 可通过“数据 → 自文本/CSV → 65001: Unicode (UTF-8)”导入。

## 一分钟验收

1. 在文件管理器里右键 ZIP，选「全部解压」。路径尽量短，例如 `D:\Allocation_Core_V14`。
2. 确认能看到 `00_Windows_Diagnostic.bat`、`01_Windows_Run_Two_Stage.bat`、`10_Windows_Reused_Driver_Index.bat`；**不应该出现任何 “Φ┐É” 或 “杩愯” 文件名**。
3. 双击 `00`，看到 `Diagnostic: PASS` 后运行 `01`。
4. 双击 `04` 打开结果，检查品牌甲、工厂甲、商品A 等中文。

## 如果 BAT 一闪而过

打开资源管理器，进入软件目录，在地址栏输入 `cmd` 按回车，然后输入：

```bat
call "00_Windows_Diagnostic.bat"
call "01_Windows_Run_Two_Stage.bat"
```

在这个窗口可以直接看到错误；或者打开 `logs/00_diagnostic_latest.log`、`logs/01_demo_latest.log`。

如果提示 `Access denied`/安全策略阻止 EXE，需要让公司的 IT 安全团队检查；**不要绕过企业的安全策略**。如果 `arm64` 或 `amd64` 与系统不符，请把诊断日志提供给实施人员。
