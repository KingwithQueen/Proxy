# Windows 7 SP1 使用说明

## v2rayN 客户端

### 1. 环境准备与启动
1. 确保系统已安装 **.NET Framework 4.8**（可下载[微软官方离线安装包](https://support.microsoft.com/zh-cn/servicing/dotnetframework/2019/10/microsoft-net-framework-4-8-offline-installer-for-windows)）。
2. 根据系统位数选择 `x64` 或 `x86` 发布包，将压缩包内所有文件完整解压至同一可写目录（切勿直接在压缩包内运行，确保 `Cray.exe` 与 `v2rayN.exe` 位于同一目录下）。
3. 运行 `v2rayN.exe`，双击系统托盘图标打开主窗口。

### 2. 添加与启用节点
- **导入节点**：复制 `trojan://`、`vmess://` 或 `vless://` 节点链接后在主界面按 `Ctrl+V` 即可导入，也可通过顶部「服务器」菜单手动添加节点。
- **启动代理**：在列表中选中节点并设为活动节点（或按 `Enter`）。内核启动后，可在主窗口底部或托盘菜单中开启「系统代理」。
- **本地监听端口**：默认在 `127.0.0.1` 提供 SOCKS5（端口 `10808`）与 HTTP（端口 `10809`）代理。若修改过端口设置，请以主窗口底部状态栏显示的实际端口为准。

### 3. 节点检索与延迟测试
- **搜索过滤**：按 `Ctrl+F` 可按节点别名或服务器地址快速过滤列表。
- **批量真连接测速**：点击主界面「一键测试真连接延迟」，客户端会并发通过列表中的节点请求测试 URL，并将实测延迟更新至节点列表；测速过程中再次点击该按钮可随时中止。
- **单选/多选测速**：选中一个或多个节点后按 `Ctrl+R`（或通过右键菜单），可仅对所选节点测试真连接延迟。

### 4. Cray 内核特性与证书校验
- **协议范围**：Cray 支持 Trojan（TCP + TLS）、VMess AEAD（`alterId` 为 `0`）和标准 VLESS（`encryption=none`，`flow` 留空）。VMess 和 VLESS 使用标准 UUID，传输选 `tcp`，包头伪装选 `none`，TLS 可单独启用。导入时也接受 `raw` 作为 `tcp` 的别名。本地入站支持 SOCKS5 `CONNECT`、HTTP `CONNECT` 及普通 HTTP 代理转发。
- **内核选择**：设为「自动」时，兼容的 VMess 和 VLESS 节点使用 Cray，其他节点使用 Xray。已指定的节点内核或协议全局内核优先。可在「参数设置」的「Core类型设置」页恢复自动选择。
- **TLS 证书校验**：默认开启服务端证书链与主机名（SAN）校验。
  - 若节点地址填写的是 IP，而服务端证书签发给域名，请在节点配置中填写对应的 **SNI**。
  - 若服务端使用自签名或私有 CA 签发的证书，可将根证书导入 Windows「受信任的根证书颁发机构」存储，或在独立运行时通过 `ca_file` 指定。

---

## 独立运行 Cray 内核

通过 v2rayN 启动时，客户端会自动运行 `Cray.exe -config -` 并通过标准输入（stdin）传入配置。若无需 GUI，也可在命令行独立运行 Cray：

```powershell
Cray.exe -config config.json
```

配置文件需采用 **UTF-8** 编码。以下是 Trojan 配置示例。

```json
{
  "server": "trojan.example.com",
  "server_port": 443,
  "password": "替换为节点密码",
  "sni": "trojan.example.com",
  "verify_tls": true,
  "socks_port": 10808,
  "http_port": 10809
}
```

以下是 VMess AEAD 的 TCP + TLS 配置示例。

```json
{
  "protocol": "vmess",
  "server": "vmess.example.com",
  "server_port": 443,
  "uuid": "de305d54-75b4-431b-adb2-eb6b9e546014",
  "alter_id": 0,
  "security": "auto",
  "tls": true,
  "sni": "vmess.example.com",
  "socks_port": 10808,
  "http_port": 10809
}
```

以下是标准 VLESS 的 TCP + TLS 配置示例。`encryption` 省略时默认为 `none`，公网连接通过外层 TLS 保护 UUID 和代理数据。 [Xray VLESS 配置](https://xtls.github.io/config/inbounds/vless.html)

```json
{
  "protocol": "vless",
  "server": "vless.example.com",
  "server_port": 443,
  "uuid": "de305d54-75b4-431b-adb2-eb6b9e546014",
  "encryption": "none",
  "tls": true,
  "sni": "vless.example.com",
  "socks_port": 10808,
  "http_port": 10809
}
```

### 配置项说明

| 字段 | 类型 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `protocol` | `string` | 否 | `trojan` | 出站协议，可选 `trojan`、`vmess` 或 `vless` |
| `server` | `string` | 是 | - | 服务端域名或 IPv4/IPv6 地址 |
| `server_port` | `uint16` | 否 | `443` | 服务端端口（`1`–`65535`） |
| `password` | `string` | Trojan 二选一 | - | Trojan 节点明文密码 |
| `password_sha224` | `string` | Trojan 二选一 | - | 预先计算的 56 位十六进制 SHA-224 密码哈希 |
| `uuid` | `string` | VMess、VLESS 必填 | - | 用户 UUID，采用 `8-4-4-4-12` 十六进制格式 |
| `alter_id` | `integer` | 否 | `0` | VMess AEAD 固定为 `0` |
| `security` | `string` | 否 | `auto` | VMess 数据加密方式，`auto` 使用 `aes-128-gcm`，也可显式选 `aes-128-gcm` 或 `chacha20-poly1305` |
| `encryption` | `string` | 否 | `none` | VLESS 协议层加密设置，支持 `none` |
| `tls` | `bool` | 否 | Trojan `true`，VMess 和 VLESS `false` | 是否在 TCP 上启用 TLS。Trojan 固定启用 |
| `sni` | `string` | 否 | 同 `server` | TLS 握手与证书校验使用的主机名 |
| `verify_tls` | `bool` | 否 | `true` | 是否校验服务端 TLS 证书（建议保持开启） |
| `socks_port` | `uint16` | 否 | `10808` | 本地 `127.0.0.1` SOCKS5 监听端口，设为 `0` 则禁用 |
| `http_port` | `uint16` | 否 | `10809` | 本地 `127.0.0.1` HTTP 监听端口，设为 `0` 则禁用 |
| `alpn` | `string[]` | 否 | 不发送 | TLS ALPN 协议列表（最多 16 项，如 `["h2", "http/1.1"]`） |
| `ca_file` | `string` | 否 | 系统根证书 | 自定义 CA 证书路径（支持 PEM 或 DER 格式）；未指定时使用 Windows 系统证书存储校验 |

> **注意**：`socks_port` 与 `http_port` 不能相同，且不能同时设为 `0`（至少需保留一个本地监听端口）。

VMess 的 `none` 和 `zero` 用于兼容仍支持这些模式的服务端，两种模式都依赖外层 TLS 保护代理数据。Xray-core v26.11.9 支持 `auto`、`aes-128-gcm` 和 `chacha20-poly1305`。 [Xray VMess 出站配置](https://github.com/XTLS/Xray-docs-next/blob/main/docs/en/config/outbounds/vmess.md)
