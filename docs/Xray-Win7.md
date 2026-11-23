# Xray 在 Windows 7 SP1 x64 上的验证

截至 2026-11-23，Xray-core 最新正式版为 `v26.11.9`，源码提交为 `676eaf66f8f05d4d558ee7a74b4736623d3feeb9`。用 XTLS 的 Win7 补丁版 Go 1.27.4 可以直接编译该版本的原始源码。上游 Win7 版和自行编译版均在 Windows 7 SP1 x64 虚拟机上通过了 Trojan TCP/TLS 回环转发。项目可以继续以 Cray 作为随包内核，将这组版本固定为后续接入 Xray 的维护基线。 [Xray 发布记录](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9) · [上游 Win7 构建流程](https://github.com/XTLS/Xray-core/blob/v26.11.9/.github/workflows/release-win7.yml)

## 固定版本与校验值

| 文件 | SHA-256 |
| --- | --- |
| [Xray v26.11.9 源码 ZIP](https://codeload.github.com/XTLS/Xray-core/zip/refs/tags/v26.11.9) | `eab6fad20dfabc862c2537552e136e195de93d166c9a0bab72e408b4295eb7dc` |
| [Go 1.27.4 Win7 补丁版构建工具](https://github.com/XTLS/go-win7/releases/download/patched-1.27.4/go-for-win7-windows-amd64.zip) | `2fccdac532798af3e7ffe73a18e25f0763d22d4c87f963075c0f59fcef8e3645` |
| [上游 Xray-win7-64.zip](https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-win7-64.zip) | `df87ef380c58d3e2286b4e49834f4b6a4819a8155ebf9124b4610b0d8148af7e` |
| 本次编译的 `xray.exe` | `54510acb15de1f4aee1a6eb44ea76e0f99b8edef867f70334645878693bdd9bb` |

在现代 Windows 构建机上用 `Get-FileHash -Algorithm SHA256` 核对下载文件，将源码解压为 `Xray-core-26.11.9`，将 SDK 解压为同级的 `go-win7`。在这两个目录的上一级执行以下命令。编译输出是独立的 `xray.exe`。

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

Go 1.20 是最后一个由 Go 官方支持 Win7 的版本。实测原版 Go 1.20.14 读取最新 Xray 的 `go.mod` 时无法识别 `tool`。将临时模块文件的版本改为 1.20 并移除 `tool` 后，仍缺少 `cmp`、`iter`、`weak`、`crypto/fips140`、`crypto/hpke` 等标准库，QUIC 与 uTLS 等依赖也要求 Go 1.27。回移到原版 Go 1.20 需要同时改造源码、依赖和加密实现，现有补丁版工具链已能直接编译，因此本轮采用最新发布版。 [Go 1.20 发布说明](https://go.dev/doc/go1.20) · [Xray 的 go.mod](https://github.com/XTLS/Xray-core/blob/v26.11.9/go.mod)

## Win7 来宾验收

- 在 Windows 7 SP1 x64（`6.1.7601`）上，上游 `Xray-win7-64.zip` 中的程序和本次编译程序均可运行 `xray.exe version`。普通 `Xray-windows-64.zip` 的程序在加载阶段被系统拒绝。Win7 专版与本次编译程序的 PE 系统和子系统标记为 6.0，普通版为 10.0。 [上游发布文件及哈希](https://github.com/XTLS/Xray-core/releases/expanded_assets/v26.11.9)
- 两份 Win7 程序均通过 `xray.exe run -test -config server.json` 和 `xray.exe run -test -config client.json`。用 `xray.exe run -config` 分别启动这两个配置后，经本地 HTTP 与 SOCKS5 代理访问回环 HTTP 测试源站，均经 Trojan TCP/TLS 链路取得 HTTP 200 和预期正文。客户端使用独立测试证书和匹配的 SNI，代理、服务端和源站均监听 `127.0.0.1`。服务端只为测试源站端口添加了一条精确的 `freedom.finalRules` 允许规则。 [Freedom 的默认规则](https://github.com/XTLS/Xray-core/blob/v26.11.9/proxy/freedom/freedom.go)
- XTLS 为补丁版 Go 1.27 指定的基线为 Win7 SP1 加 `KB3020369`、`KB3125574`、`KB4474419`、`KB4490628`，或 2016 年 4 月之前的等效更新加两项 SHA-2 相关更新。本次结果覆盖已更新来宾，待覆盖仅安装 SP1 的环境、TUN、UDP 和其他协议。 [补丁工具链系统更新要求](https://github.com/XTLS/go-win7#supported-operating-systems--minimum-update-requirements)

## 接入 v2rayN

本次用独立 Xray 配置验证运行和转发。旧版 v2rayN 生成的配置仍需单独验收，特别是 `allowInsecure=true` 已被 v26.11.9 移除。接入时应检查生成的 JSON，并按需要提供证书固定或名称校验。Xray 当前保留普通 Trojan 支持并会发出弃用提示。维护时可固定补丁 Go 与 Xray 的版本和哈希，每次升级重新跑配置校验和 Win7 回环，日常使用也可选用上游已经发布的 Win7 专版。 [TLS 配置解析](https://github.com/XTLS/Xray-core/blob/v26.11.9/infra/conf/transport_security.go) · [Trojan 配置解析](https://github.com/XTLS/Xray-core/blob/v26.11.9/infra/conf/trojan.go)
