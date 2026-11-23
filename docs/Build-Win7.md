# 构建 Windows 7 SP1 发布包

本文说明如何在 64 位 Windows 构建机上编译 `v2rayN` 客户端与 `Cray` 内核，并打包生成面向 Windows 7 SP1 的 `x86` / `x64` 发布压缩包。

## 1. 构建机前置要求

在 64 位 Windows 构建机上需安装以下组件：
- **Visual Studio**（或 **Visual Studio Build Tools**）：需包含 `MSBuild` 与 **.NET Framework 4.8 开发人员包**（Developer Pack）。
- **CMake 3.20+** 与 **Ninja**：构建脚本会优先查找 Visual Studio 内置的 CMake 与 Ninja，未找到时回退至系统 `PATH`。
- **网络环境**：首次构建需访问 NuGet 官方源以还原 .NET 依赖包。

## 2. 准备外部工具链与依赖源码

下载并解压以下固定版本的工具链与源码包。默认情况下，请将它们直接放在仓库根目录的 `.tools/win7/` 下（也可放置在其他公共目录，通过环境变量 `CRAY_TOOLS_ROOT` 或命令行参数 `-ToolsRoot` 指定，命令行参数优先级最高）：

| 解压后目录名 | 官方下载包与校验值 |
| --- | --- |
| `dotnet-sdk-10.0.404` | [.NET SDK 10.0.404 Windows x64 ZIP](https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.404/dotnet-sdk-10.0.404-win-x64.zip)<br>SHA-512: `330ddd2665b43cd2e428216a0d8ea1ac7bb19b5434fdc530e62010dc3b52823a620bf95489d628e1ed6f5122401793ee4887cca4a019122361a1231497f632ee` |
| `llvm-mingw-20261117-msvcrt-x86_64` | [LLVM-MinGW 20261117 MSVCRT x64 ZIP](https://github.com/mstorsjo/llvm-mingw/releases/download/20261117/llvm-mingw-20261117-msvcrt-x86_64.zip)<br>SHA-256: `17db10bd2c0bcbf55e5e4382475190165d0fc90dfe52ca165a51f1dfb1ca19c8` |
| `mbedtls-3.6.8` | [Mbed TLS 3.6.8 Release](https://github.com/Mbed-TLS/mbedtls/releases/tag/mbedtls-3.6.8) 中的 `mbedtls-3.6.8.tar.bz2`<br>SHA-256: `bc54b818e87f49c0b3281dd85d24adaede0e63b4e822e3fbd49c005a38b9595b` |

> **说明**：上方便携版 .NET SDK、LLVM-MinGW（链接系统 MSVCRT 以兼容 Win7）与 Mbed TLS 源码仅在构建阶段使用；最终运行于 Windows 7 SP1 目标机时，仅需安装 .NET Framework 4.8 运行时。

## 3. 执行构建

在仓库根目录打开 PowerShell 执行构建脚本。默认同时构建 `x86` 和 `x64` 两个架构，生成的 ZIP 发布包输出至 `dist/`，并在终端打印各自的 SHA-256 校验值；编译中间产物位于 `build/`。

```powershell
# 默认构建 x86 与 x64 发布包（从 .tools\win7 或 $env:CRAY_TOOLS_ROOT 读取工具链）
.\build-win7.ps1

# 仅构建 x64 架构，并显式指定工具链目录
.\build-win7.ps1 -Architecture x64 -ToolsRoot '..\shared-tools\win7'
```

### 可选参数
- `-Architecture <x86|x64>`：指定要构建的目标架构（默认同时构建 `x86` 与 `x64`）。
- `-ToolsRoot <路径>`：指定存放上述三个工具/源码目录的父目录路径。
- `-VisualStudioPath <路径>`：当 `vswhere` 无法自动定位时，手动指定 Visual Studio 安装根目录。
- `-SkipArchives`：仅在 `build/` 下生成未压缩的发布目录并输出路径，跳过 ZIP 打包与临时目录清理，便于本地调试检查。

## 4. 注意事项与许可证

- **移动仓库目录后**：由于 CMake 与 MSBuild 缓存中记录了绝对路径，若移动或重命名了仓库目录，请清理 `build/` 后重新运行构建脚本。
- **开源许可**：Cray 内核采用 `GPL-3.0-or-later` 协议授权，许可证原文见源码目录 [`Cray/LICENSE`](../Cray/LICENSE)，打包时会自动复制至发布包的 `licenses/Cray-GPL-3.0.txt`。
