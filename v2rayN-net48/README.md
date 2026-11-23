# v2rayN 7.25.6 for Windows 7 SP1 x64

此目录基于 [v2rayN 7.25.6 正式版](https://github.com/2dust/v2rayN/releases/tag/7.25.6) 的 Windows WPF 客户端（上游提交 `1883178b5f1e222b6c6db2782e7a3d989b7cc18f`）。项目目标是 `net48`，并移除了 sing-box 的界面入口、配置生成、下载和启动流程。Xray 使用上游 Win7 专版。

## 编译

构建机使用 Windows 和 .NET 10 SDK（已验证版本 `10.0.404`）。SDK 负责提供 C# 14 编译器，NuGet 会还原 .NET Framework 4.8 引用程序集。在仓库根目录执行：

```powershell
dotnet build .\v2rayN-net48\v2rayN\v2rayN.csproj -c Release
```

输出目录是 `v2rayN-net48\v2rayN\bin\Release\net48\`。部署时复制整个输出目录，再在已安装 .NET Framework 4.8 的 Windows 7 SP1 x64 上运行 `v2rayN.exe`。

## 配置 Xray

将 [Xray Win7 x64 发布包](https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-win7-64.zip) 中的 `xray.exe` 解压到客户端目录下的 `bin\xray\`。将 `geoip.dat` 和 `geosite.dat` 放进 `bin\`。新版可在独立目录运行，通过客户端的导入功能迁移节点。首次运行时，系统代理选项默认为「不改变系统代理」。客户端更新由本仓库的构建产物提供。

使用 Cray 时，可以继续运行现有的 Cray 独立程序，或使用仓库的 [v2rayN 5.39 客户端](../v2rayN-win7/)。新版 WPF 客户端使用 Xray 作为默认内核。
