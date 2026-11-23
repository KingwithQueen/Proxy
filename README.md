# Proxy

面向 Windows 7 SP1 的代理客户端与内核维护仓库，提供 x64（主维护架构）与 x86 预编译支持。

| 目录 | 说明 |
| --- | --- |
| [`v2rayN-win7`](v2rayN-win7/) | 基于 v2rayN 5.39 改造的桌面客户端（Windows Forms / .NET Framework 4.8），集成 Cray 内核与批量真连接测速 |
| [`Cray`](Cray/) | 默认随包内核，基于 C11 与 Mbed TLS 3.6，支持 Trojan、VMess AEAD、标准 VLESS 和 AnyTLS 的 TCP 出站，提供本地 HTTP 与 SOCKS5 代理 |
| [`xray`](xray/) | 基于 XTLS Win7 补丁版 Go 1.27.4 的 Xray-core v26.11.9 独立构建脚本，含 AnyTLS 实验性出站/入站补丁 |
| [`docs`](docs/) | 使用说明、编译指南与 Win7 兼容性验证文档 |
| [`licenses`](licenses/) | 随包第三方依赖的开源许可证与版权声明 |

## 快速开始与构建

- **日常使用**：在 Windows 7 SP1 上安装 .NET Framework 4.8，解压发布包后运行 `v2rayN.exe`。详细配置及 Cray 独立运行方法见 [使用说明](docs/Usage-Win7.md)。
- **构建客户端与 Cray**：在仓库根目录执行 `.\build-win7.ps1 -Architecture x64`。工具链准备、文件校验值及构建参数见 [Win7 构建指南](docs/Build-Win7.md)。
- **构建 Xray (Win7)**：独立于客户端构建，基线版本固定为 Xray-core `v26.11.9` 与 `go-win7` `1.27.4`。构建方法见 [Xray 构建说明](xray/README.md)，系统补丁要求与实测记录见 [Xray Win7 验证记录](docs/Xray-Win7.md)。
- **AnyTLS**：Cray 的节点导入与独立配置见 [使用说明](docs/Usage-Win7.md)。Xray 实验补丁的认证帧修复和互通记录见 [AnyTLS 技术评估](docs/AnyTLS-Win7.md)。

## 兼容性范围与许可证

当前维护与实机测试基线为 **Windows 7 SP1 x64**（x86 构建同步生成）。Windows XP 暂未支持，仍需针对系统 API 与运行库另行适配验证。

各组件开源许可证分别见 [v2rayN](v2rayN-win7/LICENSE) (GPL-3.0)、[Cray](Cray/LICENSE) (GPL-3.0-or-later) 与 [Xray](xray/LICENSE) (MPL-2.0)；随包分发的第三方库声明汇总于 [`licenses/`](licenses/)。
