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

## 4. 手动打包与压缩参数（防遗忘备忘）

分发完整包时仅保留 `v2rayN` 与 `Jray` 两个目录（各自按 `Windows/`、`Linux/` 存放）：
- **单文件程序**（如独立内核 `jray.exe`、`jray`）：直接存放原文件，不再套一层压缩包。
- **多文件完整包**（`v2rayN-windows-64-Jray.rar`、`v2rayN-linux-64-Jray.tar.xz`）：包内保留一层顶层目录，按以下参数最大化压缩率并测试归档完整性。

### Windows 打包（WinRAR CLI `Rar.exe`）

```powershell
# 进入包含 v2rayN-windows-64-Jray 目录的工作路径后打包
& "E:\Program Files\WinRAR\Rar.exe" a -ma5 -m5 -md128m -s -htb -oi -r `
  v2rayN-windows-64-Jray.rar v2rayN-windows-64-Jray

# 测试压缩文档完整性与文件校验和
& "E:\Program Files\WinRAR\Rar.exe" t v2rayN-windows-64-Jray.rar
```

- `-ma5`：使用 RAR 5.0 归档格式（`-htb` 与 `-oi` 均需 RAR 5.0 支持）。
- `-m5`：最高压缩等级（最大压缩）。
- `-md128m`：128 MB 压缩字典（配合固实模式提升跨文件压缩率，同时解压内存需求可控；可选 `-md256m`）。
- `-s`：固实压缩（Solid Archive），将全部文件视为连续数据流压缩以消除跨文件冗余。
- `-htb`：使用 **BLAKE2sp（256 位）** 校验码替代默认 CRC32。
- `-oi`：将完全相同的重复文件（默认 >= 64 KB）保存为引用，不重复存储内容（如 `bin\jray\wintun.dll` 与 `bin\xray\wintun.dll`）。
- `t`：完整校验压缩文档内容与 BLAKE2 校验和。

### Linux 打包（GNU `tar` + `xz`，可在 WSL / Linux 下执行）

```bash
# 1. 修正 POSIX 文件与可执行权限（避免 NTFS 默认权限导致 Linux 下无 +x）
find v2rayN-linux-64-Jray -type d -exec chmod 0755 {} +
find v2rayN-linux-64-Jray -type f -exec chmod 0644 {} +
chmod 0755 \
  v2rayN-linux-64-Jray/v2rayN \
  v2rayN-linux-64-Jray/AmazTool \
  v2rayN-linux-64-Jray/bin/jray/jray \
  v2rayN-linux-64-Jray/bin/xray/xray \
  v2rayN-linux-64-Jray/bin/sing_box/sing-box \
  v2rayN-linux-64-Jray/bin/mihomo/mihomo \
  v2rayN-linux-64-Jray/*.so \
  v2rayN-linux-64-Jray/bin/sing_box/*.so

# （可选）若包内存在相同大文件，先用硬链接去重，tar 会自动保存为引用（等效 -oi）：
# rdfind -makehardlinks true v2rayN-linux-64-Jray

# 2. 按文件名排序 + 单流固实极限压缩（-9e -T1）+ SHA-256 流校验
tar --sort=name --owner=0 --group=0 --numeric-owner -cf - v2rayN-linux-64-Jray \
  | xz -9e -T1 --check=sha256 > v2rayN-linux-64-Jray.tar.xz

# 3. 测试压缩文档（验证 SHA-256 块校验与 tar 结构完整性）
xz -t -v v2rayN-linux-64-Jray.tar.xz
tar -tJf v2rayN-linux-64-Jray.tar.xz > /dev/null
```

- `--sort=name --owner=0 --group=0 --numeric-owner`：按文件名确定性排列（便于固实压缩跨文件匹配相似二进制），剥离本地用户名/UID，并保留硬链接引用。
- `-9e`（`-9 --extreme`）：`xz` 最高压缩等级（64 MiB 字典）+ 极限搜索模式。
- `-T1`：单线程单流 LZMA2 压缩，避免 `-T0` 默认按块切分削弱跨文件固实压缩率。
- `--check=sha256`：开启 **SHA-256** 完整性校验（`xz` 原生支持的最高强度校验算法）。
- `xz -t -v` 与 `tar -tJf`：解压校验全部块校验和并验证归档结构。
