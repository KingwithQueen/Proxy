# AnyTLS 接入 Xray-core 的 Win7 可行性与互通评估

## 1. 评估结论

2026-11-23 于 Windows 7 SP1 x64（`6.1.7601`）完成验证。在固定 **Xray-core `v26.11.9`** 与 **`XTLS/go-win7` `1.27.4`** 的基线上，可将社区 AnyTLS 补丁移植编译为 Win7 可执行文件。

在定位并修复了**首条 TLS 记录认证帧被拆分为两次写入**的兼容性缺陷后，实验版 Xray 已在 Win7 环境下与官方参考实现（`anytls-go v0.0.13`）完成双向互通测试。当前正式发布包仍默认随附 `Cray`，Xray 稳定基线保持原版 `v26.11.9`；后续若要正式集成 AnyTLS，建议第一阶段将范围收敛为 **AnyTLS TCP/TLS 客户端出站**。

- 参考资料：[Xray Win7 基线与工具链](Xray-Win7.md) · [AnyTLS 协议规范](https://github.com/anytls/anytls-go/blob/v0.0.13/docs/protocol.md)

## 2. 社区现有实现对比

| 候选方案 | 代码评估 |
| --- | --- |
| **XTLS [PR #5907](https://github.com/XTLS/Xray-core/pull/5907)** | 提交 `a6e6ec0286545d938abf63c18bcf46d9e536bab1`（已关闭未合入）。原生实现了出站与入站（共新增 16 个文件、2,093 行代码）。[发布构建 CI](https://github.com/XTLS/Xray-core/actions/runs/24256181021) 通过，[测试 CI](https://github.com/XTLS/Xray-core/actions/runs/24256181016) 因 protobuf 生成校验未通过而失败。结构清晰，适合作为 Xray 配置注册与协议接入的原型基线。 |
| **[Sir-MmD/Xray-core](https://github.com/Sir-MmD/Xray-core/tree/bacaa47268398bbeeddc9c3c88e1ee143101da21)** | 基于 Xray `v26.4.17`，直接调用官方 [`sing-anytls v0.0.13`](https://github.com/anytls/sing-anytls/blob/v0.0.13/LICENSE) 实现出站与入站。底层库已妥善处理单次写入认证帧、按客户端隔离填充策略（Padding）及 `Close()` 生命周期，其[出站适配层](https://github.com/Sir-MmD/Xray-core/blob/bacaa47268398bbeeddc9c3c88e1ee143101da21/proxy/anytls/outbound.go)极具参考价值。但该 fork 同时混入了其他协议与服务端调度改动；且 `sing-anytls` 采用 **GPL-3.0-or-later** 协议，若打包分发需满足 GPL 许可证与源码提供要求。 |
| **[v2rayA 内嵌核心](https://github.com/v2rayA/v2rayA/tree/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/hint/proxy/anytls)** | 仅实现客户端出站，依赖 [`anytls-go v0.0.12`](https://raw.githubusercontent.com/v2rayA/v2rayA/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/go.mod)。默认校验域名并支持证书链固定；但其[出站代码](https://raw.githubusercontent.com/v2rayA/v2rayA/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/hint/proxy/anytls/outbound.go)绕过 Xray 传输层自行封装了 TLS，且多个出站实例共享同一个默认填充工厂。若参考该方案，需改为复用 Xray 原生 TLS 配置并确保多节点状态隔离。 |

> **其他相关动态**：[妙妙屋 X](https://miaomiaowux.com/docs/en/protocol-anytls/) 文档明确说明其内嵌内核基于 PR #5907；[SMProxy](https://docs.smproxy.io/en/dev-docs/deep-links-smproxy/) 文档也提及了基于 Xray `v26.11.9` 的 AnyTLS 出站支持（未纳入本次源码审计）；[XTLS PR #6298](https://github.com/XTLS/Xray-core/pull/6298) 提出的通用插件化出站接口目前尚未合入 `v26.11.9`。

## 3. 实验版构建与移植步骤

本仓库 [`xray/build-win7.ps1`](../xray/README.md) 已将下述移植与修复固化为补丁，直接运行 `.\build-win7.ps1 -ExperimentalAnyTls` 即可一键生成实验版可执行文件。

若需从上游 PR #5907 原始补丁手动复现移植过程，可基于 [Xray-Win7](Xray-Win7.md) 中的 `v26.11.9` 源码（`676eaf66f8f05d4d558ee7a74b4736623d3feeb9`）与 `go-win7` 工具链按以下步骤操作：

1. **下载并应用原始 PR 补丁**：下载 [`5907.patch`](https://github.com/XTLS/Xray-core/pull/5907.patch)（SHA-256: `99aa8526c36c0a54cfb60215d6682225b3548188271676f283539ecf8c1bc165`）。由于 `v26.11.9` 中 `infra/conf/xray.go` 的上下文已变化，先跳过该文件应用其余改动：
   ```powershell
   git apply --exclude=infra/conf/xray.go 5907.patch
   ```
   随后在 `infra/conf/xray.go` 的入站和出站配置加载器中，手动将 `"anytls"` 分别注册至 `new(AnyTLSServerConfig)` 与 `new(AnyTLSClientConfig)`。
2. **恢复 `singbridge` 地址转换桥接**：`v26.11.9` 已移除旧版的 [`common/singbridge/destination.go`](https://github.com/XTLS/Xray-core/blob/e5a9fb752e0dcc127dd1740316c853571c16052f/common/singbridge/destination.go) 及其依赖。从上述历史提交恢复该文件，并执行 `go get github.com/sagernet/sing@v0.6.1`。
3. **适配日志接口**：在 `proxy/anytls/outbound.go` 中删去新版已移除的 `.AtWarning()` 链式调用，并对修改过的文件执行 `gofmt`。至此可编译出保留 PR #5907 原始认证写入逻辑的对照版（`xray-anytls-wyx.exe`）。
4. **修复首帧认证写入（关键）**：在 [`proxy/anytls/outbound.go`](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/outbound.go) 中，将「32 字节密码 SHA-256 哈希 + 2 字节大端序填充长度 + `padding0` 填充字节」拼装进同一个连续缓冲区，仅调用一次 `conn.Write` 写入 TLS 连接。

使用 `GOOS=windows`、`GOARCH=amd64`、`GOAMD64=v1`、`CGO_ENABLED=0`、`GOTOOLCHAIN=local` 编译：

```powershell
& $go mod verify
# 编译保留 PR 原始行为的对照版（修复版将输出名与 build 标签换为 xray-anytls-auth-qa.exe / v26.11.9-anytls-auth-qa）
& $go build -o xray-anytls-wyx.exe -trimpath -buildvcs=false `
    '-ldflags=-X github.com/xtls/xray-core/core.build=v26.11.9-anytls-qa -s -w -buildid=' ./main
& $go test ./proxy/anytls
```

> **注**：PR #5907 的 `proxy/anytls` 目录未附带单元测试；在同一构建机上使用相同工具链对独立的 `sing-anytls v0.0.13`（提交 `479cb5bd490a2f4b1b6e8cd82b821afb392a94c8`）执行 `go test ./...` 则全部通过。

### 测试产物与参考实现校验值

官方参考端基于 [`anytls-go v0.0.13`](https://github.com/anytls/anytls-go/releases/tag/v0.0.13)（提交 `9666872946857b50a74fdb692896d773cb2`），使用同一 `go-win7` 工具链编译 `./cmd/server` 与 `./cmd/client`（仅在测试入口配置加载本地测试 PEM 证书并开启严格域名校验）：

| 测试可执行文件 | 说明 | SHA-256 |
| --- | --- | --- |
| `xray-anytls-wyx.exe` | PR #5907 仅做编译适配（分两次写入认证帧） | `7c1f773a088023e4394ed4ba607f2c5594b5f9091cff01819196500d01e75f0f` |
| `xray-anytls-auth-qa.exe` | PR #5907 编译适配 + 单次写入完整认证帧修复 | `8a968e96a60750615cf034ba43bd0f62d9ce4661d1b85ea2a3b5e66c174b8539` |
| `reference-server-strict.exe` | `anytls-go v0.0.13` 参考服务端（加载固定测试证书） | `e9a0399f35881853b3c4c06cd4c0fbb05c3dd8f925ac29f5105bac9847595b44` |
| `reference-client-strict.exe` | `anytls-go v0.0.13` 参考客户端（强制校验证书与主机名） | `f7670a6bdcb7a11db3f9c165d749df6ca887bed1e48131c129342ccff50f39c6` |

## 4. Windows 7 交叉互通测试结果

在 Windows 7 SP1 x64 虚拟机内，HTTP 测试源站、参考端与 Xray 均监听 `127.0.0.1`。TLS 测试证书 SAN 设为 `xray.fixture.test`，客户端配置相同 SNI 与共享密码。Xray 入站使用 `settings.users[].password`，出站使用 `settings.address` / `port` / `password`，均开启 `streamSettings.security = "tls"`，服务端通过 `freedom` 出站转发至本地测试源站。通过 HTTP 与 SOCKS5 代理请求测试源站，校验是否返回 `HTTP 200` 及正文 `cray-anytls-tls`：

| 客户端 → 服务端组合 | HTTP 代理测试 | SOCKS5 代理测试 |
| --- | --- | --- |
| `xray-anytls-wyx` → `xray-anytls-wyx` | 通过 | 通过 |
| `anytls-go v0.0.13` 参考客户端 → `xray-anytls-wyx` | 通过 | 通过 |
| `xray-anytls-wyx` → `anytls-go v0.0.13` 参考服务端 | **失败 (HTTP 503)**：Xray 报错 `unexpected EOF`，参考服务端触发 `fallback` | 因前置 HTTP 失败中止 |
| `xray-anytls-auth-qa`（修复后）→ `anytls-go v0.0.13` 参考服务端 | **通过** | **通过** |

### 失败根因分析

PR #5907 的原始出站实现先将「32 字节密码哈希 + 2 字节填充长度」写入 TLS 连接，随后再用第二次 `Write` 发送 `padding0` 填充数据，导致认证头被拆分为两条 TLS 应用数据记录。

而官方参考服务端（见 [`inbound_tcp.go`](https://github.com/anytls/anytls-go/blob/v0.0.13/cmd/server/inbound_tcp.go)）要求在**第一次 TLS 明文读取**中就必须包含完整的认证头与 `padding0`，否则直接判定认证不完整并转入回落（fallback）或断开连接。由于 PR #5907 自己的服务端使用了 `io.ReadFull` 跨记录拼接读取，自发自收测试掩盖了该问题，唯有在与官方参考服务端交叉测试时才会暴露。

## 5. 正式合入前的待办事项

当前实验补丁仅用于验证编译与协议互通，在正式合入生产版本前还需完成以下工作：

1. **TLS 配置与客户端 GUI 联动**：出站需完整复用 Xray `v26.11.9` 的 TLS 配置、证书校验与主机名校验，并在配置解析层强制要求开启 TLS（补充密码错误、证书不受信任、SNI 不匹配等负向测试）。v2rayN 客户端需支持导入/编辑 AnyTLS 节点并调度带 AnyTLS 支持的 Xray 内核。
2. **填充策略状态复用与资源回收**（针对 PR #5907 代码缺陷）：
   - PR #5907 收到服务端下发的填充策略更新（`cmdUpdatePaddingScheme`）后仅保存在当前 `Session` 内，新建会话仍回退到默认填充方案；而协议规范要求按客户端/目标服务端维度持久化并复用最新填充方案。
   - 出站实现缺少 `Close()` 方法，需补齐连接取消、断线清理、配置重载与空闲会话池回收逻辑。
   - 会话层（[`session.go`](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/session.go) / [`outbound.go`](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/outbound.go)）的版本协商字段存在跨 goroutine 读写，需补充并发同步保护（或跑通 `-race` 检测），并重新生成与校验 protobuf 定义。
3. **扩展场景验证与选型建议**：本次测试仅覆盖 TCP/TLS 短连接回环转发；长连接稳定性、高并发流复用、跨实现 UDP-over-TCP、REALITY 传输及 TUN 模式尚未验证。若第一阶段仅需客户端出站能力，建议优先评估直接基于 `sing-anytls` 编写最小 Xray 出站适配器（同时确认 GPLv3 许可证合规性），并在本文固定的 Win7 基线上完成回归测试。
