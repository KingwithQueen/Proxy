# v2rayN (Windows 7 SP1 适配版)

面向 Windows 7 SP1 的桌面代理客户端，基于上游 [v2rayN 5.39](https://github.com/2dust/v2rayN/releases/tag/5.39)（提交 `529b661`）改造，运行于 **Windows Forms** 与 **.NET Framework 4.8**。开源许可证见 [`LICENSE`](LICENSE) (GPL-3.0)。

## 主要改动

- **集成 Cray 内核**：将 [`Cray`](../Cray/) 作为 Trojan (TCP/TLS) 节点的默认轻量内核，通过标准输入（stdin）安全传递节点配置，无需落地明文密码配置文件。
- **主界面与交互优化**：针对日常节点管理与搜索过滤改进主窗口交互。
- **一键真连接延迟测速**：支持对当前列表中的节点并发执行真实连接测速（过程中可随时点击取消），也支持按 `Ctrl+R` 对选中节点单独测速。

## 构建与工程结构

- **一键打包**：在仓库根目录执行 `.\build-win7.ps1`，即可同时编译 `Cray` 与本客户端，并生成 `x86` / `x64` 发布包（详见 [`docs/Build-Win7.md`](../docs/Build-Win7.md)）。
- **解决方案结构**：Visual Studio 解决方案位于 `v2rayN.sln`，主客户端项目为 `v2rayN/v2rayN.csproj`（解决方案中同时保留了上游的 `v2rayUpgrade` 升级辅助项目）。
- **内置系统代理组件**：`v2rayN/Resources/sysproxy.exe.gz` 与 `v2rayN/Resources/sysproxy64.exe.gz` 为上游 v2rayN 5.39 原版附带的系统代理设置工具，编译时作为嵌入式资源打包进客户端，分别用于在 32 位和 64 位 Windows 上配置系统代理。
