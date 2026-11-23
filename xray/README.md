# Xray-core Windows 7 SP1 x64 构建目录

本目录提供面向 Windows 7 SP1 x64 的 `Xray-core` 独立可复现构建脚本与 AnyTLS 实验性补丁。

- **稳定版默认构建**：固定编译上游 [Xray-core v26.11.9](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9)（提交 `676eaf66f8f05d4d558ee7a74b4736623d3feeb9`），详细实机验证记录见 [`docs/Xray-Win7.md`](../docs/Xray-Win7.md)。
- **AnyTLS 实验版构建**：通过附加 `-ExperimentalAnyTls` 参数，在 `v26.11.9` 源码基础上自动打入 [`patches/anytls-v26.11.9-experimental.patch`](patches/anytls-v26.11.9-experimental.patch) 并编译，输出独立文件名以避免与正式版混淆。

## 1. 快速构建

在 64 位 Windows 构建机上打开 PowerShell 进入本目录执行。脚本会自动下载（或读取本地指定的）Xray 源码包与 `XTLS/go-win7`（Go `1.27.4`）编译器并校验 SHA-256，首次编译需联网下载 Go Module 依赖；启用 `-ExperimentalAnyTls` 时还需本机已安装 `git`。

```powershell
# 构建原版 Xray-core v26.11.9 -> dist\xray.exe
.\build-win7.ps1

# 构建包含 AnyTLS 支持的实验版 -> dist\xray-anytls-auth-qa.exe
.\build-win7.ps1 -ExperimentalAnyTls
```

### 脚本参数
- `-OutputDirectory <路径>`：指定编译产物的输出目录（默认为本目录下的 `dist/`）。
- `-SourceArchive <路径>`：指定本地已下载的 `Xray-core` v26.11.9 源码 ZIP 路径（跳过网络下载，仍校验 SHA-256）。
- `-GoSdkArchive <路径>`：指定本地已下载的 `go-for-win7-windows-amd64.zip` 路径（跳过网络下载，仍校验 SHA-256）。
- `-ExperimentalAnyTls`：应用 AnyTLS 实验补丁并编译 `xray-anytls-auth-qa.exe`。

> 编译过程中的源码解压与 Go SDK 目录均创建在系统临时目录（`%TEMP%\xray-win7-*`）中，构建结束后会自动清理。

## 2. 固定依赖与构建产物校验值

编译环境变量固定为 `GOOS=windows`、`GOARCH=amd64`、`GOAMD64=v1`、`CGO_ENABLED=0`。

### 输入源码与工具链

| 依赖项 | SHA-256 |
| --- | --- |
| [Xray-core v26.11.9 源码包](https://codeload.github.com/XTLS/Xray-core/zip/refs/tags/v26.11.9) | `eab6fad20dfabc862c2537552e136e195de93d166c9a0bab72e408b4295eb7dc` |
| [XTLS Go 1.27.4 Win7 补丁版 (Windows x64)](https://github.com/XTLS/go-win7/releases/download/patched-1.27.4/go-for-win7-windows-amd64.zip) | `2fccdac532798af3e7ffe73a18e25f0763d22d4c87f963075c0f59fcef8e3645` |

### 可复现编译产物

在不同路径下使用同一工具链构建，输出的二进制文件 SHA-256 与 Win7 实机验收时完全一致：

| 构建产物 | 说明 | SHA-256 |
| --- | --- | --- |
| `xray.exe` | 原版 Xray-core `v26.11.9` | `54510acb15de1f4aee1a6eb44ea76e0f99b8edef867f70334645878693bdd9bb` |
| `xray-anytls-auth-qa.exe` | 含 AnyTLS 补丁的实验版（`v26.11.9-anytls-auth-qa`） | `8a968e96a60750615cf034ba43bd0f62d9ce4661d1b85ea2a3b5e66c174b8539` |

> **运行环境提示**：目标 Windows 7 SP1 机器需满足 [`XTLS/go-win7` 的系统更新基线](https://github.com/XTLS/go-win7#supported-operating-systems--minimum-update-requirements)。若路由配置中引用了 `geoip.dat` 或 `geosite.dat`，需另行将对应数据文件放置在 `xray.exe` 同级目录。

## 3. AnyTLS 实验补丁说明

[`patches/anytls-v26.11.9-experimental.patch`](patches/anytls-v26.11.9-experimental.patch)（SHA-256: `2685bb3ce536ee8e8fccf2e9049f2fb344e98fca584852b3c37b90a3a25af72f`）将 [XTLS PR #5907](https://github.com/XTLS/Xray-core/pull/5907)（提交 `a6e6ec0286545d938abf63c18bcf46d9e536bab1`）迁移至 `v26.11.9`，主要包含以下改动：
1. 在 `infra/conf/xray.go` 中注册 `"anytls"` 入站与出站配置解析；
2. 恢复 [`common/singbridge/destination.go`](https://github.com/XTLS/Xray-core/blob/e5a9fb752e0dcc127dd1740316c853571c16052f/common/singbridge/destination.go) 地址转换桥接代码，并在 `go.mod` 中引入 `github.com/sagernet/sing v0.6.1`；
3. 移除 `proxy/anytls/outbound.go` 中已废弃的 `.AtWarning()` 日志调用；
4. **修复首帧认证互通问题**：将 32 字节密码哈希、2 字节填充长度与初始填充字节合并至单次 TLS `Write` 调用中发送，确保与官方 `anytls-go` 服务端兼容。

若需在本地保留打好补丁的源码树以便阅读或二次开发，可先将上表中的 Xray `v26.11.9` 源码包解压至本目录的 `Xray-core-26.11.9`，然后执行：

```powershell
$patch = (Resolve-Path .\patches\anytls-v26.11.9-experimental.patch).Path
git -C .\Xray-core-26.11.9 apply --check $patch
git -C .\Xray-core-26.11.9 apply $patch
```

该实验分支目前仅用于验证 Win7 SP1 x64 下与 `anytls-go v0.0.13` 的 TCP/TLS 回环互通，仍存在填充策略跨会话持久化、出站 `Close()` 资源释放等问题待完善（详见 [`docs/AnyTLS-Win7.md`](../docs/AnyTLS-Win7.md)）。

## 4. 许可证

- `Xray-core` 本身采用 MPL-2.0 协议授权，详见本目录 [`LICENSE`](LICENSE)。
- 启用 `-ExperimentalAnyTls` 构建的实验版额外引入了 `github.com/sagernet/sing v0.6.1`，该依赖采用 **GPL-3.0-or-later** 协议（见 [SagerNet sing LICENSE](https://github.com/SagerNet/sing/blob/v0.6.1/LICENSE)）。
