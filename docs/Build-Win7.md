# 构建 Windows 7 SP1 版

请在 64 位 Windows 构建机上安装 Visual Studio 或 Build Tools 的 MSBuild、.NET Framework 4.8 开发人员包、CMake 3.20 或更新版本，以及 Ninja。脚本优先使用 Visual Studio 随附的 CMake 和 Ninja，也可从 PATH 读取。首次还原依赖需要访问 NuGet 源。

解压下列发行文件，使各工具的目录直接位于项目的 `.tools/win7` 下。也可以用 `CRAY_TOOLS_ROOT` 指向统一存放工具的目录。命令行的 `-ToolsRoot` 优先级最高。

| 解压后的目录 | 发行文件与校验值 |
| --- | --- |
| `dotnet-sdk-10.0.404` | [.NET SDK 10.0.404 Windows x64 ZIP](https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.404/dotnet-sdk-10.0.404-win-x64.zip)，SHA-512 `330ddd2665b43cd2e428216a0d8ea1ac7bb19b5434fdc530e62010dc3b52823a620bf95489d628e1ed6f5122401793ee4887cca4a019122361a1231497f632ee` |
| `llvm-mingw-20261117-msvcrt-x86_64` | [LLVM-MinGW 20261117 MSVCRT x64 ZIP](https://github.com/mstorsjo/llvm-mingw/releases/download/20261117/llvm-mingw-20261117-msvcrt-x86_64.zip)，SHA-256 `17db10bd2c0bcbf55e5e4382475190165d0fc90dfe52ca165a51f1dfb1ca19c8` |
| `mbedtls-3.6.8` | [Mbed TLS 3.6.8 源码发行包](https://github.com/Mbed-TLS/mbedtls/releases/tag/mbedtls-3.6.8) 中的 `mbedtls-3.6.8.tar.bz2`，SHA-256 `bc54b818e87f49c0b3281dd85d24adaede0e63b4e822e3fbd49c005a38b9595b` |

从项目根目录执行以下命令。脚本默认生成 x86 和 x64 发布包，并在控制台打印 ZIP 的 SHA-256。发布包在 `dist`，中间产物在 `build`。

~~~powershell
.\build-win7.ps1
.\build-win7.ps1 -Architecture x64 -ToolsRoot '..\shared-tools\win7'
~~~

可用 `-VisualStudioPath` 指定 Visual Studio 的安装目录。若需直接查看未压缩的发布包，可传入 `-SkipArchives`，脚本会输出对应的临时包目录。

迁移源码时，在新位置重新运行构建。CMake 和 MSBuild 的旧缓存包含原目录的绝对路径。目标 Windows 7 SP1 电脑需要 .NET Framework 4.8 运行时。便携 .NET SDK、编译器及 Mbed TLS 源码用于构建。

Cray 源码采用 GPL-3.0-or-later。许可证全文位于源码目录的 `Cray/LICENSE` 和发布包的 `licenses/Cray-GPL-3.0.txt`。
