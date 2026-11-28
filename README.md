# Proxy

综合代理客户端与内核维护仓库，包含面向 **现代桌面系统（Windows 10/11+，规划支持 Linux）** 的 `Jray` + `v2rayN` 分支，以及面向 **Windows 7 SP1** 的 `v2rayN-win7` + `Cray` 分支。

| 目录 | 适用平台 | 说明 |
| --- | --- | --- |
| [`v2rayN/`](v2rayN/) | Windows 10/11+（规划 Linux） | 基于 `2dust/v2rayN`（`7.25.6`）改造版，集成 `Jray` 并设为默认核心，客户端与 `Jray` 更新源指向 Cloudflare R2（其余核心保持官方源） |
| [`Jray/`](Jray/) | Windows / Linux | 基于 `XTLS/Xray-core`（`v26.11.9`）改造版，原生实现 **AnyTLS**（v1/v2）入站与出站支持，无第三方 GPL 依赖 |
| [`v2rayN-win7/`](v2rayN-win7/) | Windows 7 SP1 | 基于 v2rayN 5.39 改造的桌面客户端（Windows Forms / .NET Framework 4.8），集成 `Cray` 内核与批量真连接测速 |
| [`Cray/`](Cray/) | Windows 7 SP1 | Win7 默认随包内核，基于 C11 与 Mbed TLS 3.6，支持 Trojan、VMess AEAD、标准 VLESS 和 AnyTLS 的 TCP 出站 |
| [`xray/`](xray/) | Windows 7 SP1 | 基于 XTLS Win7 补丁版 Go 1.27.4 的 Xray-core `v26.11.9` 独立构建脚本，含 AnyTLS 实验性出站/入站补丁 |
| [`docs/`](docs/) | 全部 | 使用说明、编译指南、R2 更新架构与 Win7 兼容性验证文档 |
| [`licenses/`](licenses/) | Windows 7 SP1 | Win7 随包第三方依赖的开源许可证与版权声明 |

## 现代平台：Jray 与修改版 v2rayN

- **内核（[`Jray/`](Jray/)）**：在 `Jray/` 目录运行 `.\build_windows.bat` 或 `go build -o jray.exe -trimpath -ldflags "-s -w -buildid=" ./main` 编译 Windows x64 二进制；详情见 [Jray 说明](Jray/README_JRAY.md) 与 [Jray + v2rayN 架构与构建文档](docs/Jray-v2rayN.md)。
- **客户端（[`v2rayN/`](v2rayN/)）**：基于 .NET 10（WPF 版 `v2rayN/v2rayN.csproj` 与跨平台 Avalonia 版 `v2rayN.Desktop/v2rayN.Desktop.csproj`）。默认核心已切换为 `Jray`（`TUIC`、`Naive` 默认 `sing_box`），`v2rayN` 与 `Jray` 通过 Cloudflare R2（`v2rayn-release.jsnowly.com` / `jray-release.jsnowly.com`）检查与拉取更新。

## Windows 7 SP1：v2rayN-win7 与 Cray

- **日常使用**：在 Windows 7 SP1 上安装 .NET Framework 4.8，解压发布包后运行 `v2rayN.exe`。详细配置及 Cray 独立运行方法见 [Win7 使用说明](docs/Usage-Win7.md)。
- **构建客户端与 Cray**：在仓库根目录执行 `.\build-win7.ps1 -Architecture x64`。工具链准备、文件校验值及构建参数见 [Win7 构建指南](docs/Build-Win7.md)。
- **构建 Xray (Win7)**：独立于客户端构建，基线版本固定为 Xray-core `v26.11.9` 与 `go-win7` `1.27.4`。构建方法见 [Xray 构建说明](xray/README.md)，系统补丁要求与实测记录见 [Xray Win7 验证记录](docs/Xray-Win7.md)。
- **AnyTLS (Win7)**：Cray 的节点导入与独立配置见 [Win7 使用说明](docs/Usage-Win7.md)。Xray 实验补丁的认证帧修复和互通记录见 [AnyTLS 技术评估](docs/AnyTLS-Win7.md)。

## 兼容性范围与许可证

- **现代版（`v2rayN/` + `Jray/`）**：面向 Windows 10/11 x64（`.NET 10` + `Go 1.27+`），后续支持 Linux。
- **Win7 版（`v2rayN-win7/` + `Cray/`）**：维护与实机测试基线为 **Windows 7 SP1 x64**（同步生成 x86）。Windows XP 暂未支持。

各组件开源许可证分别见 [`v2rayN`](v2rayN/LICENSE) (GPL-3.0)、[`Jray`](Jray/LICENSE) (MPL-2.0)、[`v2rayN-win7`](v2rayN-win7/LICENSE) (GPL-3.0)、[`Cray`](Cray/LICENSE) (GPL-3.0-or-later) 与 [`xray`](xray/LICENSE) (MPL-2.0)；Win7 随包第三方库声明汇总于 [`licenses/`](licenses/)。
