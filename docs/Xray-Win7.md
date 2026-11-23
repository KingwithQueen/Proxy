# Xray-core 在 Windows 7 SP1 x64 上的编译与验证记录

## 1. 概述与基线结论

截至 2026-11-23，`Xray-core` 最新正式版本为 `v26.11.9`（对应提交 `676eaf66f8f05d4d558ee7a74b4736623d3feeb9`）。经实测，使用 XTLS 维护的 Win7 补丁版 `go-win7`（Go `1.27.4`）无需修改源码即可直接编译该版本。

上游官方发布的 Win7 专版构建与本地复现编译产物，均已在 Windows 7 SP1 x64（`6.1.7601`）虚拟机中通过 Trojan (TCP/TLS) 链路转发测试。本仓库继续以轻量的 `Cray` 作为默认随包内核，同时将 **Xray-core `v26.11.9` + `go-win7` `1.27.4`** 固定为后续可选接入 Xray 的维护基线。

- 相关链接：[Xray v26.11.9 发布页](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9) · [上游 Win7 构建工作流](https://github.com/XTLS/Xray-core/blob/v26.11.9/.github/workflows/release-win7.yml) · [本仓库自动化构建脚本](../xray/README.md)

## 2. 固定版本与文件校验值

| 文件 / 产物 | SHA-256 |
| --- | --- |
| [Xray v26.11.9 源码包 (ZIP)](https://codeload.github.com/XTLS/Xray-core/zip/refs/tags/v26.11.9) | `eab6fad20dfabc862c2537552e136e195de93d166c9a0bab72e408b4295eb7dc` |
| [XTLS Go 1.27.4 Win7 补丁版 SDK](https://github.com/XTLS/go-win7/releases/download/patched-1.27.4/go-for-win7-windows-amd64.zip) | `2fccdac532798af3e7ffe73a18e25f0763d22d4c87f963075c0f59fcef8e3645` |
| [上游发布包 `Xray-win7-64.zip`](https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-win7-64.zip) | `df87ef380c58d3e2286b4e49834f4b6a4819a8155ebf9124b4610b0d8148af7e` |
| 本地复现编译的 `xray.exe` | `54510acb15de1f4aee1a6eb44ea76e0f99b8edef867f70334645878693bdd9bb` |

### 手动复现编译步骤

除直接运行仓库中的 [`xray/build-win7.ps1`](../xray/README.md) 外，也可在 64 位 Windows 构建机上按以下步骤手动复现：

1. 下载上表中的源码包与 Go SDK，使用 `Get-FileHash -Algorithm SHA256` 校验哈希。
2. 将两者解压到同一工作目录下（目录名分别为 `Xray-core-26.11.9` 和 `go-win7`）。
3. 在该工作目录执行以下 PowerShell 命令，编译产物将输出至当前目录的 `xray.exe`：

```powershell
$env:GOROOT = (Resolve-Path .\go-win7).Path
$go = Join-Path $env:GOROOT 'bin\go.exe'
$env:GOTOOLCHAIN = 'local'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:GOAMD64 = 'v1'
$env:CGO_ENABLED = '0'

Set-Location .\Xray-core-26.11.9
& $go version
& $go mod download
if ($LASTEXITCODE -ne 0) { throw 'Go module download failed' }
& $go mod verify
if ($LASTEXITCODE -ne 0) { throw 'Go module verification failed' }
& $go build -o ..\xray.exe -trimpath -buildvcs=false '-gcflags=all=-l=4' `
    '-ldflags=-X github.com/xtls/xray-core/core.build=v26.11.9 -s -w -buildid=' ./main
if ($LASTEXITCODE -ne 0) { throw 'Xray build failed' }
& $go version -m ..\xray.exe
Get-FileHash -Algorithm SHA256 ..\xray.exe
```

### 为何不回退至官方 Go 1.20？

Go 1.20 是 Go 官方支持 Windows 7 的最后一个版本（[Go 1.20 Release Notes](https://go.dev/doc/go1.20)）。实测使用官方 Go `1.20.14` 编译新版 Xray 时：
1. 无法解析 [`go.mod`](https://github.com/XTLS/Xray-core/blob/v26.11.9/go.mod) 中的 `tool` 指令；
2. 即使手工降级 `go.mod` 版本并剔除 `tool`，Xray 及其依赖（如 QUIC、uTLS 等）也已深度依赖 Go 1.21–1.27 新增的 `cmp`、`iter`、`weak`、`crypto/fips140`、`crypto/hpke` 等标准库。

将新版 Xray 强行向后移植到 Go 1.20 需要大规模改动核心代码与密码学依赖；而采用 `XTLS/go-win7` 补丁版工具链可直接零改动编译上游最新正式版，维护成本与安全性均明显更优。

## 3. Windows 7 实机验证记录

- **二进制兼容性与 PE 头检查**：在 Windows 7 SP1 x64（`6.1.7601`）虚拟机中，上游 `Xray-win7-64.zip` 与本地编译的 `xray.exe` 均可正常执行 `xray.exe version`；而上游常规版 `Xray-windows-64.zip` 因 PE OS/Subsystem 版本号标记为 `10.0`，在进程加载阶段即被 Win7 拒绝（Win7 专版与本地编译版的 PE 子系统版本均为 `6.0`，详见[上游发布资产列表](https://github.com/XTLS/Xray-core/releases/expanded_assets/v26.11.9)）。
- **Trojan TCP/TLS 回环连通性**：两份 Win7 可执行文件均通过 `xray.exe run -test -config` 配置校验。在同一台 Win7 虚拟机内启动本地 Xray 客户端、Xray 服务端与 HTTP 测试源站（均绑定 `127.0.0.1`），客户端配置独立测试证书与匹配 SNI，服务端通过 [`freedom.finalRules`](https://github.com/XTLS/Xray-core/blob/v26.11.9/proxy/freedom/freedom.go) 仅放行测试源站端口。经本地 HTTP 与 SOCKS5 代理发起请求，均成功通过 Trojan TCP/TLS 链路返回 `HTTP 200` 与预期响应体。
- **系统补丁要求**：根据 [`XTLS/go-win7` 说明](https://github.com/XTLS/go-win7#supported-operating-systems--minimum-update-requirements)，运行基于补丁版 Go 1.27 编译的程序，需要 Windows 7 SP1 安装 `KB3020369`、`KB3125574`（2016 年 4 月便捷汇总更新）以及 `KB4474419`、`KB4490628`（SHA-2 签名支持更新）。本次测试在已安装上述补丁的环境中完成；未打补丁的纯净 SP1 环境、TUN 模式、UDP 转发及其他协议尚未纳入本轮测试。

## 4. 与 v2rayN 集成的注意事项

本轮验证使用了独立的 Xray 配置文件。若将 Xray `v26.11.9` 接入基于旧版 v2rayN 5.39 的客户端，需注意以下变更：
1. **`allowInsecure` 已移除**：Xray `v26.11.9` 已彻底移除 TLS 配置中的 `allowInsecure=true` 字段（见 [`transport_security.go`](https://github.com/XTLS/Xray-core/blob/v26.11.9/infra/conf/transport_security.go)）。客户端生成 JSON 配置时不能再输出该字段，自签名证书场景需改为配置证书指纹固定（`pinnedPeerCertSha256`）或自定义根证书。
2. **Trojan 弃用提示**：Xray 目前仍保留常规 Trojan 协议支持，但启动时会打印弃用提示（见 [`trojan.go`](https://github.com/XTLS/Xray-core/blob/v26.11.9/infra/conf/trojan.go)）。
3. **日常升级建议**：后续升级 Xray 版本时，建议继续固定 `go-win7` 与源码哈希，并在升级后复跑配置生成校验与 Win7 回环测试；普通用户日常使用也可直接下载上游发布的 `Xray-win7-64.zip`。
