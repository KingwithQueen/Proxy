# Jray 与修改版 v2rayN 架构及构建指南

本文档说明仓库中 [`Jray/`](../Jray/) 与 [`v2rayN/`](../v2rayN/) 的改造内容、默认核心策略、Cloudflare R2 更新机制以及本地编译打包流程。

## 1. 组件定位

- **[`Jray/`](../Jray/)**：基于上游 `XTLS/Xray-core`（当前基线 `26.11.9`）的修改版核心，原生实现 **AnyTLS**（v1/v2）入站与出站协议，零新增第三方 GPL 依赖，兼容 Windows 与 Linux。详情见 [`Jray/README_JRAY.md`](../Jray/README_JRAY.md)。
- **[`v2rayN/`](../v2rayN/)**：基于上游 `2dust/v2rayN`（当前基线 `7.25.6`）的改造版桌面客户端（.NET 10，含 Windows WPF 版 `v2rayN/v2rayN/v2rayN.csproj` 与跨平台 Avalonia 版 `v2rayN/v2rayN.Desktop/v2rayN.Desktop.csproj`）。

## 2. 主要改造点

### 默认核心策略
- 在 `ServiceLib/Global.cs` 将 `CoreTypes` 顺序调整为 `["Jray", "Xray", "sing_box"]`，并通过 `GetDefaultCoreType` 统一决定默认核心：
  - `TUIC`、`Naive` → `sing_box`
  - 其他协议（`VMess`、`VLESS`、`Shadowsocks`、`Trojan`、`Hysteria2`、`WireGuard`、`Anytls`、`SOCKS`、`HTTP`、`Custom`、`Outbound`、`MASQUE`、`PolicyGroup`、`ProxyChain`）→ **`Jray`**
- `ConfigHandler.LoadConfig()`（`EnsureDefaultCoreTypes`）、`AppManager.GetCoreType()`、`CoreManager.LoadCoreConfigSpeedtest()`、`OptionSettingViewModel`、`AddGroupServerViewModel`、`V2rayFmt` 均默认指向 `Jray`，确保首次运行开箱即用。

### 核心与客户端更新源
- **自有 Cloudflare R2 源**（`CoreInfo.UseReleaseApiOnly = true`）：
  - **`v2rayN`**：`https://v2rayn-release.jsnowly.com/{Windows|Linux|macOS}/releases.json`
  - **`Jray`**：`https://jray-release.jsnowly.com/{Windows|Linux|macOS}/releases.json`
- **官方 GitHub 源（保持不变）**：
  - `Xray`、`mihomo`、`sing_box` 与 `GeoFiles` 仍使用原版官方 GitHub 地址。
- **R2 存储桶布局**：
  - `v2rayn` 桶：`Windows/releases.json`、`Windows/<version>/v2rayN-windows-64.zip`（轻量更新包，含 `v2rayN.exe`、`AmazTool.exe`、运行库 DLL 与 `bin/jray/{jray.exe,wintun.dll}`；升级时 `AmazTool` 自动跳过已存在的 `bin/` 内文件）；未来 Linux 版放置于 `Linux/` 下。
  - `jray` 桶：`Windows/releases.json`、`Windows/<version>/Jray-windows-64.zip`（根目录直接包含 `jray.exe` 与 Windows TUN 所需的 `wintun.dll`）；未来 Linux 版放置于 `Linux/` 下。
  - `releases.json` 采用兼容 GitHub Releases 的 JSON 数组结构（`tag_name`、`prerelease`、`assets[].browser_download_url`），R2 响应头配置 `Cache-Control: no-cache, max-age=0, must-revalidate`。

## 3. 本地构建与测试

### 编译 Jray（Go 1.27+）
```powershell
cd Jray
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -o jray.exe -trimpath -ldflags "-s -w -buildid=" ./main
```

### 构建与测试 v2rayN（.NET 10 SDK）
```powershell
# 构建 WPF 与 Avalonia 客户端
dotnet build .\v2rayN\v2rayN\v2rayN\v2rayN.csproj -c Release
dotnet build .\v2rayN\v2rayN\v2rayN.Desktop\v2rayN.Desktop.csproj -c Release

# 运行 R2 与默认核心单元测试
dotnet run --project .\v2rayN\v2rayN\ServiceLib.Tests\ServiceLib.Tests.csproj -c Release -- --treenode-filter "/*/*/UpdateServiceR2Tests/*"

# 自包含发布 Windows x64 客户端与升级工具 AmazTool
dotnet publish .\v2rayN\v2rayN\v2rayN\v2rayN.csproj -c Release -r win-x64 -p:SelfContained=true -o .\dist\v2rayN-windows-64
dotnet publish .\v2rayN\v2rayN\AmazTool\AmazTool.csproj -c Release -r win-x64 -p:SelfContained=true -p:PublishTrimmed=true -o .\dist\v2rayN-windows-64
```

### 运行期目录要求
- `Jray` 二进制位于 `bin/jray/jray.exe`（Windows 下若启用 Jray TUN 模式，同目录需存在 `wintun.dll`）；Linux 下位于 `bin/jray/jray`。
- 自升级包 ZIP 内部必须保留一层顶层目录（例如 `v2rayN-windows-64/v2rayN.exe`），以匹配 `AmazTool` 的路径剥离解压逻辑。
