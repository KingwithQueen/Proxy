# Xray for Windows 7 SP1 x64

默认构建固定的 XTLS Xray-core v26.11.9（`676eaf66f8f05d4d558ee7a74b4736623d3feeb9`）。AnyTLS 原型以可应用的源码补丁保存，需用 `-ExperimentalAnyTls` 单独构建。正式版和实验版使用独立的输出文件名。 [Xray v26.11.9](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9) · [Win7 验证报告](../docs/Xray-Win7.md)

## 构建

在 Windows x64 构建机上通过 PowerShell 执行。实验模式还需要 Git。脚本会下载并校验 Xray 源码和 XTLS 补丁版 Go 1.27.4，首次下载 Go 模块也需要网络。

```powershell
.\build-win7.ps1
.\build-win7.ps1 -ExperimentalAnyTls
```

默认输出为 `dist/xray.exe` 和 `dist/xray-anytls-auth-qa.exe`。使用 `-OutputDirectory` 可以指定其他目录。已有 ZIP 时可在两种模式下用 `-SourceArchive` 和 `-GoSdkArchive` 提供本地路径，脚本同样会检查 SHA-256。源码检出和 SDK 解压目录临时使用系统临时目录。

| 已固定的下载文件 | SHA-256 |
| --- | --- |
| [Xray v26.11.9 源码](https://codeload.github.com/XTLS/Xray-core/zip/refs/tags/v26.11.9) | `eab6fad20dfabc862c2537552e136e195de93d166c9a0bab72e408b4295eb7dc` |
| [XTLS Go 1.27.4 Windows x64](https://github.com/XTLS/go-win7/releases/download/patched-1.27.4/go-for-win7-windows-amd64.zip) | `2fccdac532798af3e7ffe73a18e25f0763d22d4c87f963075c0f59fcef8e3645` |

构建目标为 `windows/amd64`，`GOAMD64=v1`，`CGO_ENABLED=0`。Win7 来宾使用 XTLS 工具链要求的 SP1 更新基线。涉及 `geoip.dat`、`geosite.dat` 或其他运行时资源的配置需要另行准备相应文件。 [上游工具链与更新要求](https://github.com/XTLS/go-win7#supported-operating-systems--minimum-update-requirements)

迁移后用同一工具链重新构建了两个版本，得到与 Win7 验证时相同的 SHA-256。

| 构建产物 | SHA-256 |
| --- | --- |
| `xray.exe` | `54510acb15de1f4aee1a6eb44ea76e0f99b8edef867f70334645878693bdd9bb` |
| `xray-anytls-auth-qa.exe` | `8a968e96a60750615cf034ba43bd0f62d9ce4661d1b85ea2a3b5e66c174b8539` |

## AnyTLS 实验源码

[补丁](patches/anytls-v26.11.9-experimental.patch) 复现 #5907 提交 `a6e6ec0286545d938abf63c18bcf46d9e536bab1` 在 v26.11.9 上的适配和认证互通修复。它包含两处协议注册、`common/singbridge/destination.go`、固定的 `github.com/sagernet/sing v0.6.1` 依赖、去除过时日志 API，以及一次 TLS 写入完整认证帧的改动。补丁的 SHA-256 为 `2685bb3ce536ee8e8fccf2e9049f2fb344e98fca584852b3c37b90a3a25af72f`。 [XTLS PR #5907](https://github.com/XTLS/Xray-core/pull/5907) · [桥接源码](https://github.com/XTLS/Xray-core/blob/e5a9fb752e0dcc127dd1740316c853571c16052f/common/singbridge/destination.go)

如果要保留完整的实验源码目录供检查，先解压上表的 Xray ZIP，在本目录执行以下命令。此例将源码解压到与本文件同级的 `Xray-core-26.11.9`。

```powershell
$patch = (Resolve-Path .\patches\anytls-v26.11.9-experimental.patch).Path
git -C .\Xray-core-26.11.9 apply --check $patch
git -C .\Xray-core-26.11.9 apply $patch
```

兼容性验证覆盖 Win7 SP1 x64 上与 AnyTLS v0.0.13 参考实现的 TCP/TLS 回环。该分支仍有待处理的会话和填充状态问题。Xray 上游许可见本目录 [LICENSE](LICENSE)，实验构建另依赖 GPL-3.0-or-later 的 SagerNet sing。 [AnyTLS 评审与对照测试](../docs/AnyTLS-Win7.md) · [SagerNet sing 许可证](https://github.com/SagerNet/sing/blob/v0.6.1/LICENSE)
