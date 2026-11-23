# Proxy

本仓库维护面向旧版 Windows 的代理客户端和内核。目前随包支持 Windows 7 SP1，首选 x64，另提供 x86 构建。

| 目录 | 内容 |
| --- | --- |
| [v2rayN-win7](v2rayN-win7/) | 基于 v2rayN 5.39 的 WinForms 客户端，运行于 .NET Framework 4.8 |
| [Cray](Cray/) | 默认随包内核，使用 C 和 Mbed TLS 实现 Trojan TCP/TLS，提供本地 HTTP 和 SOCKS5 代理 |
| [xray](xray/) | 固定 Xray v26.11.9 和 Win7 补丁版 Go 的独立构建，以及 AnyTLS 实验版的可复现补丁 |
| [docs](docs/) | 构建、客户端用法和兼容性记录 |
| [licenses](licenses/) | 随包依赖的许可证与通知 |

## 构建和使用

- **Win7 客户端和 Cray** 从仓库根目录运行 `.uild-win7.ps1 -Architecture x64`。工具链准备、校验值和 x86 构建见 [Win7 构建说明](docs/Build-Win7.md)。
- **Xray** 独立构建。维护基线固定为 Xray v26.11.9 和 XTLS 的 Win7 补丁版 Go 1.27.4。实验补丁的构建入口及版本见 [Xray 构建说明](xray/README.md)。证书校验和 Win7 环境要求见 [兼容性记录](docs/Xray-Win7.md)。
- **AnyTLS** 实验版已在 Win7 SP1 x64 上与参考实现互通。目前验收范围为 TCP/TLS 回环转发。候选审查、测试范围和正式接入前的工作见 [AnyTLS 评审](docs/AnyTLS-Win7.md)。
- **日常使用** 在 Win7 SP1 安装 .NET Framework 4.8，解压后运行 `v2rayN.exe`。 [客户端与 Cray 用法](docs/Usage-Win7.md)

## 支持范围与许可证

Win7 SP1 x64 是维护和验证基线。Windows XP 兼容性仍需单独的源码和构建验证。

组件许可证分别见 [v2rayN](v2rayN-win7/LICENSE)、[Cray](Cray/LICENSE) 和 [Xray](xray/LICENSE)。附带的第三方依赖通知位于 [licenses](licenses/)。
