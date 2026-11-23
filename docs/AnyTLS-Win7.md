# AnyTLS 接入 Xray 的 Win7 可行性

2026-11-23 在 Windows 7 SP1 x64（`6.1.7601`）验证。固定 Xray `v26.11.9` 和补丁版 Go `1.27.4` 后，可以编译社区 AnyTLS 实现。修复首个 TLS 记录的认证帧后，原型在来宾上与 AnyTLS 参考实现完成了双向互通。下一步适合以 **AnyTLS TCP/TLS 客户端出站**为范围做独立集成评审。发行包随附 Cray，Xray 维护基线使用原版 v26.11.9。 [Xray 基线与工具链](Xray-Win7.md) · [AnyTLS 协议文档](https://github.com/anytls/anytls-go/blob/v0.0.13/docs/protocol.md)

## 社区实现

| 候选 | 评审结果 |
| --- | --- |
| XTLS [PR #5907](https://github.com/XTLS/Xray-core/pull/5907) | `a6e6ec0286545d938abf63c18bcf46d9e536bab1`，已关闭，未合并。提供出站和入站，共 16 个文件、2,093 行新增。发布构建 CI 成功，protobuf 校验 CI 失败。适合复现 Xray 接入点和做互通原型。 [发布构建](https://github.com/XTLS/Xray-core/actions/runs/24256181021) · [测试 CI](https://github.com/XTLS/Xray-core/actions/runs/24256181016) |
| [Sir-MmD/Xray-core](https://github.com/Sir-MmD/Xray-core/tree/bacaa47268398bbeeddc9c3c88e1ee143101da21) | Xray `v26.4.17` 上通过 `sing-anytls v0.0.13` 实现出站和入站。会话库已有一次写入认证、逐客户端填充状态和 `Close()`，其出站适配结构值得优先参考。整套 fork 还改动了其他协议和服务端调度。 `sing-anytls` 的授权为 GPL-3.0-or-later，分发前需要评审授权和源码交付。 [出站源码](https://github.com/Sir-MmD/Xray-core/blob/bacaa47268398bbeeddc9c3c88e1ee143101da21/proxy/anytls/outbound.go) · [库许可证](https://github.com/anytls/sing-anytls/blob/v0.0.13/LICENSE) |
| [v2rayA 内嵌核心](https://github.com/v2rayA/v2rayA/tree/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/hint/proxy/anytls) | 近版 Xray 上的客户端出站参考，使用 `anytls-go v0.0.12`。默认校验域名，也可配置证书链固定。代码自行包装 TLS，且给多个出站传递同一个默认填充工厂，接入时需要保留 Xray 的 TLS 配置语义并核验状态隔离。 [出站源码](https://raw.githubusercontent.com/v2rayA/v2rayA/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/hint/proxy/anytls/outbound.go) · [依赖](https://raw.githubusercontent.com/v2rayA/v2rayA/29a76535ca8bce3932a621bbed1ed0c93d01c5fb/core/go.mod) |

妙妙屋 X 公开说明其内嵌内核采用了 #5907，属于同一实现的下游案例。SMProxy 公开文档也描述了基于 Xray v26.11.9 的 AnyTLS 出站，其底层实现未纳入本次源码验证。XTLS #6298 仍是通用插件出站提案，当前 v26.11.9 没有这个扩展入口。 [妙妙屋 X](https://miaomiaowux.com/docs/en/protocol-anytls/) · [SMProxy](https://docs.smproxy.io/en/dev-docs/deep-links-smproxy/) · [XTLS #6298](https://github.com/XTLS/Xray-core/pull/6298)

## 复现实验构建

维护脚本已固定 Xray 源码、编译器和该实验补丁。可以直接用 `-ExperimentalAnyTls` 构建。 [可复现构建](../xray/README.md)

以 [Xray-Win7](Xray-Win7.md) 中的原始源码提交 `676eaf66f8f05d4d558ee7a74b4736623d3feeb9` 和补丁工具链准备独立目录。下载 [#5907.patch](https://github.com/XTLS/Xray-core/pull/5907.patch)，本次使用的补丁 SHA-256 为 `99aa8526c36c0a54cfb60215d6682225b3548188271676f283539ecf8c1bc165`。

1. 执行 `git apply --exclude=infra/conf/xray.go 5907.patch`。补丁对该文件的两段上下文在 v26.11.9 已变更。在现有的入站和出站配置注册表分别将 `"anytls"` 指向 `new(AnyTLSServerConfig)` 和 `new(AnyTLSClientConfig)`。其余改动可直接应用。
2. 恢复旧版 `common/singbridge/destination.go` 中的地址转换桥接。本次取自上述 Sir-MmD 固定提交，比较内容与 #5907 基线一致，仅文件换行不同。它使用的依赖在 v26.11.9 已移除。在隔离目录执行 `go get github.com/sagernet/sing@v0.6.1`。 [原始桥接文件](https://github.com/XTLS/Xray-core/blob/e5a9fb752e0dcc127dd1740316c853571c16052f/common/singbridge/destination.go)
3. 在 `proxy/anytls/outbound.go` 的连接错误中删除已移除的 `.AtWarning()` 调用。对改动文件执行 `gofmt`。此时保留 PR 的原始认证写入行为。
4. 以 `GOOS=windows`、`GOARCH=amd64`、`GOAMD64=v1`、`CGO_ENABLED=0`、`GOTOOLCHAIN=local` 运行下面的构建。需要验证认证修复时，将认证哈希 32 字节、填充长度 2 字节、填充本体放入同一缓冲区后调用一次 `conn.Write`，并将构建标签和输出名分别换成 `v26.11.9-anytls-auth-qa` 和 `xray-anytls-auth-qa.exe`。 [PR 的出站源码](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/outbound.go)

```powershell
& $go mod verify
& $go build -o xray-anytls-wyx.exe -trimpath -buildvcs=false `
    '-ldflags=-X github.com/xtls/xray-core/core.build=v26.11.9-anytls-qa -s -w -buildid=' ./main
& $go test ./proxy/anytls
```

`go mod verify` 和编译通过。PR 的 `proxy/anytls` 无测试文件。独立的 `sing-anytls v0.0.13`（`479cb5bd490a2f4b1b6e8cd82b821afb392a94c8`）在构建机上用同一工具链运行 `go test ./...` 成功，其中会话包有实际测试。

| 实验构建 | SHA-256 |
| --- | --- |
| `xray-anytls-wyx.exe` | `7c1f773a088023e4394ed4ba607f2c5594b5f9091cff01819196500d01e75f0f` |
| `xray-anytls-auth-qa.exe` | `8a968e96a60750615cf034ba43bd0f62d9ce4661d1b85ea2a3b5e66c174b8539` |
| `reference-server-strict.exe` | `e9a0399f35881853b3c4c06cd4c0fbb05c3dd8f925ac29f5105bac9847595b44` |
| `reference-client-strict.exe` | `f7670a6bdcb7a11db3f9c165d749df6ca887bed1e48131c129342ccff50f39c6` |

参考端固定为 [anytls-go v0.0.13](https://github.com/anytls/anytls-go/releases/tag/v0.0.13)，提交 `9666872946857b50a74fdb692896d77b53773cb2`。分别用同一 SDK 和环境执行 `go build -trimpath -buildvcs=false -o reference-server-strict.exe ./cmd/server` 和对应的 `./cmd/client` 构建。测试版只调整证书装配。参考服务端从测试目录读取固定 PEM 证书和密钥，参考客户端显式信任该测试证书并启用名称校验。

## Win7 互通结果

来宾上的源站、参考端和 Xray 均使用回环地址。TCP/TLS 证书 SAN 为 `xray.fixture.test`，客户端的 SNI 与其一致。两端使用同一临时密码。测试配置中 Xray 入站通过 `settings.users[].password` 认证，出站用 `settings.address`、`port`、`password`，均设置 `streamSettings.security = "tls"`。Xray 服务端转发使用单独的 `freedom` 出站。先运行 `xray.exe version` 和 `xray.exe run -test -config <配置>`，再通过独立 HTTP 代理与 SOCKS5 请求回环 HTTP 源站，匹配 HTTP 200 及正文 `cray-anytls-tls`。

| 客户端 → 服务端 | HTTP | SOCKS5 |
| --- | --- | --- |
| #5907 构建适配版 → 同版 Xray | 通过 | 通过 |
| AnyTLS v0.0.13 参考端 → #5907 构建适配版 | 通过 | 通过 |
| #5907 构建适配版 → AnyTLS v0.0.13 参考端 | HTTP 503。Xray 记录 `unexpected EOF`，参考服务端进入 `fallback` | HTTP 失败后该轮停止 |
| #5907 合并认证写入实验版 → AnyTLS v0.0.13 参考端 | 通过 | 通过 |

原 PR 把密码哈希和填充长度写入 TLS 后，再用单独一次写入发送填充。参考端只在首次 TLS 明文读取中检查完整认证帧。Xray 原型服务端使用 `ReadFull`，两个同实现端点可以通信，因而只有交叉测试暴露了这个问题。 [参考端认证读取](https://github.com/anytls/anytls-go/blob/v0.0.13/cmd/server/inbound_tcp.go)

## 正式合入前的工作

- 出站优先使用 Xray 的 TLS 配置、证书和主机名校验。配置层应强制正常 TLS，覆盖错误密码、证书不可信和名称不匹配的测试。GUI 需要识别 AnyTLS 节点并切换到有该出站能力的内核，沿用 v26.11.9 的证书字段。
- #5907 的填充更新只存于当前会话，后续新会话仍取默认值。协议要求按客户端和服务端隔离并复用更新。原型还缺少出站 `Close()`，应补齐取消、断线、配置重载和会话池回收。会话的版本协商状态有跨协程访问，生产合入前应做并发检查，并重新生成和校验 protobuf。 [PR 会话实现](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/session.go) · [出站实现](https://github.com/wyx2685/Xray-core/blob/a6e6ec0286545d938abf63c18bcf46d9e536bab1/proxy/anytls/outbound.go)
- 本次对照覆盖 AnyTLS TCP/TLS 小流量转发。长连接、并行和复用压力、跨实现 UDP-over-TCP、REALITY、TUN 暂无本次验收结果。以客户端出站为第一阶段时，可优先评审 `sing-anytls` 的最小适配器及其授权，再以本报告的 Xray 固定基线复测。
